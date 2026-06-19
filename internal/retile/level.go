package retile

import "context"

// LevelSpec is one output pyramid level. The engine works finest-first; Index
// is engine-relative (0 = finest). Sinks translate Index to container numbering.
type LevelSpec struct {
	Index         int
	Width, Height int // level pixel dims (Height is set for sinks — e.g. TIFF IFDs; the engine derives content height from Rows/TileH and the strip)
	Cols, Rows    int // tile grid
	TileW, TileH  int
	Overlap       int // 0 for TIFF-family/cog-wsi; 1 for DZI
}

// edgeTileDims returns the content dimensions of tile (col,row) for a w×h image
// with the given tile size. Interior tiles return (tileSize,tileSize); right/
// bottom edge tiles return the truncated remainder. Overlap is added separately.
// Inlined from internal/dzi.EdgeTileDims to keep the engine dzi-free.
func edgeTileDims(w, h, tileSize, col, row int) (int, int) {
	tw := tileSize
	if (col+1)*tileSize > w {
		tw = w - col*tileSize
	}
	th := tileSize
	if (row+1)*tileSize > h {
		th = h - row*tileSize
	}
	return tw, th
}

// encodeJob hands a rendered tile to the encoder pool. level is the engine-
// relative level index (LevelSpec.Index).
type encodeJob struct {
	level, col, row int
	img             *RGBImage
}

// levelBuilder builds tiles for one pyramid level. The chain is linked via
// child; each level downsamples its strips into the next coarser level. The
// finest builder receives strips from the ScaledStrips iterator; the coarsest
// has child=nil.
type levelBuilder struct {
	spec LevelSpec

	// Rolling 3-strip overlap buffer.
	prev, cur, next *RGBImage

	// Accumulator for the strip currently being filled from downsampled parent
	// rows. nil for the finest builder (the iterator feeds full strips).
	accum     *RGBImage
	accumRows int

	rowIndex int // next tile row to emit

	child *levelBuilder
	jobs  chan<- encodeJob
	ctx   context.Context
}

// feed accepts one strip at this level's resolution: rotate the buffer, emit the
// now-complete row, and downsample 2× to the child.
func (lb *levelBuilder) feed(strip *RGBImage) {
	lb.prev = lb.cur
	lb.cur = lb.next
	lb.next = strip
	if lb.cur != nil {
		lb.emitRow(lb.rowIndex)
		lb.rowIndex++
	}
	if lb.child != nil {
		lb.child.acceptDownsampled(boxDownsample2x(strip))
	}
}

// acceptDownsampled buffers a half-height parent strip until it has accumulated
// a full strip at this level's resolution, then promotes it via feed.
func (lb *levelBuilder) acceptDownsampled(half *RGBImage) {
	if lb.accum == nil {
		lb.accum = &RGBImage{
			Pix:    make([]byte, lb.spec.Width*lb.spec.TileH*3),
			Stride: lb.spec.Width * 3,
			W:      lb.spec.Width,
			H:      lb.spec.TileH,
		}
	}
	halfH, halfW := half.H, half.W
	for y := 0; y < halfH; y++ {
		if lb.accumRows+y >= lb.spec.TileH {
			break
		}
		srcRow := half.Pix[y*half.Stride : y*half.Stride+halfW*3]
		dstRow := lb.accum.Pix[(lb.accumRows+y)*lb.accum.Stride : (lb.accumRows+y)*lb.accum.Stride+halfW*3]
		copy(dstRow, srcRow)
	}
	lb.accumRows += halfH
	if lb.accumRows >= lb.spec.TileH {
		completed := lb.accum
		lb.accum = nil
		lb.accumRows = 0
		lb.feed(completed)
	}
}

// flush emits remaining rows after the source iterator finishes, then cascades.
func (lb *levelBuilder) flush() {
	if lb.accum != nil && lb.accumRows > 0 {
		short := &RGBImage{
			Pix:    make([]byte, lb.spec.Width*lb.accumRows*3),
			Stride: lb.spec.Width * 3,
			W:      lb.spec.Width,
			H:      lb.accumRows,
		}
		for y := 0; y < lb.accumRows; y++ {
			copy(short.Pix[y*short.Stride:], lb.accum.Pix[y*lb.accum.Stride:y*lb.accum.Stride+lb.spec.Width*3])
		}
		lb.feed(short)
		lb.accum = nil
		lb.accumRows = 0
	}
	lb.prev = lb.cur
	lb.cur = lb.next
	lb.next = nil
	if lb.cur != nil && lb.rowIndex < lb.spec.Rows {
		lb.emitRow(lb.rowIndex)
		lb.rowIndex++
	}
	if lb.child != nil {
		lb.child.flush()
	}
}

// emitRow slices cur into the row's tiles and enqueues encodeJobs.
func (lb *levelBuilder) emitRow(row int) {
	for col := 0; col < lb.spec.Cols; col++ {
		tile := lb.assembleTile(col, row)
		select {
		case lb.jobs <- encodeJob{level: lb.spec.Index, col: col, row: row, img: tile}:
		case <-lb.ctx.Done():
			releaseRGB(tile)
			return
		}
	}
}

// assembleTile builds the RGB tile for (col,row) from the rolling buffer with
// overlap rules.
func (lb *levelBuilder) assembleTile(col, row int) *RGBImage {
	lw := lb.spec.Width
	cw, ch := edgeTileDims(lw, lb.spec.Rows*lb.spec.TileH, lb.spec.TileH, col, row)
	if lb.cur != nil && lb.cur.H < lb.spec.TileH {
		ch = lb.cur.H
	}
	leftOv := lb.spec.Overlap
	if col == 0 {
		leftOv = 0
	}
	topOv := lb.spec.Overlap
	if row == 0 || lb.prev == nil {
		topOv = 0
	}
	rightOv := lb.spec.Overlap
	if col*lb.spec.TileW+cw >= lw {
		rightOv = 0
	}
	bottomOv := lb.spec.Overlap
	if lb.next == nil {
		bottomOv = 0
	}
	outW := cw + leftOv + rightOv
	outH := ch + topOv + bottomOv

	dst := newPooledRGB(outW, outH)
	srcX0 := col*lb.spec.TileW - leftOv
	if topOv > 0 && lb.prev != nil {
		copyRGBBand(dst, 0, lb.prev, lb.prev.H-topOv, srcX0, outW, topOv)
	}
	if lb.cur != nil {
		copyRGBBand(dst, topOv, lb.cur, 0, srcX0, outW, ch)
	}
	if bottomOv > 0 && lb.next != nil {
		copyRGBBand(dst, topOv+ch, lb.next, 0, srcX0, outW, bottomOv)
	}
	return dst
}

// copyRGBBand copies a horizontal band between two RGBImages. Out-of-range
// source columns are white-filled (defensive against overlap-rule errors).
func copyRGBBand(dst *RGBImage, dstY int, src *RGBImage, srcY, srcX, w, h int) {
	srcW := src.W
	for r := 0; r < h; r++ {
		dstRowStart := (dstY + r) * dst.Stride
		srcRowStart := (srcY + r) * src.Stride
		for c := 0; c < w; c++ {
			sx := srcX + c
			var rr, gg, bb byte
			if sx >= 0 && sx < srcW {
				si := srcRowStart + sx*3
				rr, gg, bb = src.Pix[si+0], src.Pix[si+1], src.Pix[si+2]
			} else {
				rr, gg, bb = 0xFF, 0xFF, 0xFF
			}
			di := dstRowStart + c*3
			dst.Pix[di+0], dst.Pix[di+1], dst.Pix[di+2] = rr, gg, bb
		}
	}
}
