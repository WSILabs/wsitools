package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"

	"github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/dzi"
)

// encodeJob hands a rendered tile RGBA to the encoder pool.
type encodeJob struct {
	level, col, row int
	img             *image.RGBA
}

// writeJob hands encoded bytes to the sink-drain goroutine.
type writeJob struct {
	level, col, row int
	body            []byte
}

// levelBuilder builds DZI tiles for one pyramid level. The chain
// of levelBuilders is linked via the `child` pointer; each level
// downsamples its strips into the next coarser level. The L_max
// builder receives strips from the ScaledStrips iterator; the
// coarsest builder (level=0) has child=nil.
type levelBuilder struct {
	level int
	width int // pixel width at this level
	cfg   dzi.Config
	cols  int
	rows  int

	// Rolling 3-strip overlap buffer.
	prev, cur, next *image.RGBA

	// Accumulator for the strip currently being filled from
	// downsampled parent rows. nil for the L_max builder; the
	// parent feeds full strips directly into feed().
	accum     *image.RGBA
	accumRows int

	rowIndex int // next DZI row to emit

	child *levelBuilder
	jobs  chan<- encodeJob
}

// feed accepts one strip at this level's resolution.
//  1. Rotate buffer: prev = cur; cur = next; next = strip.
//  2. If cur != nil, emit DZI tiles for rowIndex.
//  3. Downsample 2× and feed to child (if any).
func (lb *levelBuilder) feed(strip *image.RGBA) {
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

// acceptDownsampled buffers a half-height parent strip until it
// has accumulated enough rows to promote to a full strip at this
// level's resolution.
func (lb *levelBuilder) acceptDownsampled(half *image.RGBA) {
	if lb.accum == nil {
		lb.accum = image.NewRGBA(image.Rect(0, 0, lb.width, lb.cfg.TileSize))
	}
	halfH := half.Bounds().Dy()
	halfW := half.Bounds().Dx()

	for y := 0; y < halfH; y++ {
		if lb.accumRows+y >= lb.cfg.TileSize {
			break
		}
		srcRow := half.Pix[y*half.Stride : y*half.Stride+halfW*4]
		dstRow := lb.accum.Pix[(lb.accumRows+y)*lb.accum.Stride : (lb.accumRows+y)*lb.accum.Stride+halfW*4]
		copy(dstRow, srcRow)
	}
	lb.accumRows += halfH

	if lb.accumRows >= lb.cfg.TileSize {
		completed := lb.accum
		lb.accum = nil
		lb.accumRows = 0
		lb.feed(completed)
	}
}

// flush emits remaining DZI rows after the source iterator finishes.
func (lb *levelBuilder) flush() {
	// Finalise any partial accumulator as a short strip.
	if lb.accum != nil && lb.accumRows > 0 {
		short := image.NewRGBA(image.Rect(0, 0, lb.width, lb.accumRows))
		for y := 0; y < lb.accumRows; y++ {
			copy(short.Pix[y*short.Stride:], lb.accum.Pix[y*lb.accum.Stride:y*lb.accum.Stride+lb.width*4])
		}
		lb.feed(short)
		lb.accum = nil
		lb.accumRows = 0
	}

	// One more rotation with next=nil to emit the last row.
	lb.prev = lb.cur
	lb.cur = lb.next
	lb.next = nil
	if lb.cur != nil && lb.rowIndex < lb.rows {
		lb.emitRow(lb.rowIndex)
		lb.rowIndex++
	}

	if lb.child != nil {
		lb.child.flush()
	}
}

// emitRow slices cur into the row's DZI tiles and enqueues
// encodeJobs.
func (lb *levelBuilder) emitRow(row int) {
	for col := 0; col < lb.cols; col++ {
		tile := lb.assembleTile(col, row)
		lb.jobs <- encodeJob{level: lb.level, col: col, row: row, img: tile}
	}
}

// assembleTile builds the RGBA for DZI tile (col, row) from the
// rolling buffer (prev/cur/next) with overlap rules.
func (lb *levelBuilder) assembleTile(col, row int) *image.RGBA {
	lw := lb.width
	// Use level height = rows * TileSize for EdgeTileDims; the last
	// row's content height comes from cur's actual height (which may
	// be short on the last strip).
	cw, ch := dzi.EdgeTileDims(lw, lb.rows*lb.cfg.TileSize, lb.cfg.TileSize, col, row)
	if lb.cur != nil && lb.cur.Bounds().Dy() < lb.cfg.TileSize {
		ch = lb.cur.Bounds().Dy()
	}

	leftOv := lb.cfg.Overlap
	if col == 0 {
		leftOv = 0
	}
	topOv := lb.cfg.Overlap
	if row == 0 || lb.prev == nil {
		topOv = 0
	}
	rightOv := lb.cfg.Overlap
	if col*lb.cfg.TileSize+cw >= lw {
		rightOv = 0
	}
	bottomOv := lb.cfg.Overlap
	if lb.next == nil {
		bottomOv = 0
	}

	outW := cw + leftOv + rightOv
	outH := ch + topOv + bottomOv

	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	srcX0 := col*lb.cfg.TileSize - leftOv

	if topOv > 0 && lb.prev != nil {
		prevH := lb.prev.Bounds().Dy()
		copyRGBABand(dst, 0, lb.prev, prevH-topOv, srcX0, outW, topOv)
	}
	if lb.cur != nil {
		copyRGBABand(dst, topOv, lb.cur, 0, srcX0, outW, ch)
	}
	if bottomOv > 0 && lb.next != nil {
		copyRGBABand(dst, topOv+ch, lb.next, 0, srcX0, outW, bottomOv)
	}
	return dst
}

// copyRGBABand copies a horizontal band between two RGBAs. Out-of-
// range source columns are white-filled (defensive against
// overlap-rule programming errors).
func copyRGBABand(dst *image.RGBA, dstY int, src *image.RGBA, srcY, srcX, w, h int) {
	srcW := src.Bounds().Dx()
	for r := 0; r < h; r++ {
		dstRowStart := (dstY + r) * dst.Stride
		srcRowStart := (srcY + r) * src.Stride
		for c := 0; c < w; c++ {
			sx := srcX + c
			var rr, gg, bb byte
			if sx >= 0 && sx < srcW {
				si := srcRowStart + sx*4
				rr = src.Pix[si+0]
				gg = src.Pix[si+1]
				bb = src.Pix[si+2]
			} else {
				rr, gg, bb = 0xFF, 0xFF, 0xFF
			}
			di := dstRowStart + c*4
			dst.Pix[di+0] = rr
			dst.Pix[di+1] = gg
			dst.Pix[di+2] = bb
			dst.Pix[di+3] = 0xFF
		}
	}
}

// encoderWorker pulls encodeJobs, encodes via enc, pushes writeJobs.
// Selects on ctx.Done() for cancellation. Exits cleanly when jobs
// is closed.
func encoderWorker(ctx context.Context, jobs <-chan encodeJob, out chan<- writeJob,
	cfg dzi.Config, enc *jpeg.Encoder, quality int) {

	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			body, err := encodeTile(job.img, cfg.Format, enc, quality)
			if err != nil {
				return
			}
			select {
			case out <- writeJob{level: job.level, col: job.col, row: job.row, body: body}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// encodeTile dispatches per cfg.Format ("jpeg" or "png").
func encodeTile(img *image.RGBA, format string, enc *jpeg.Encoder, quality int) ([]byte, error) {
	switch format {
	case "jpeg":
		w := img.Bounds().Dx()
		h := img.Bounds().Dy()
		rgb := make([]byte, w*h*3)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				si := y*img.Stride + x*4
				di := y*w*3 + x*3
				rgb[di+0] = img.Pix[si+0]
				rgb[di+1] = img.Pix[si+1]
				rgb[di+2] = img.Pix[si+2]
			}
		}
		return enc.EncodeStandalone(rgb, w, h)
	case "png":
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unsupported dzi format %q", format)
	}
}

// sinkDrainer pulls writeJobs and calls sink.WriteTile serially.
// Stores the first error in *firstErr; subsequent errors are
// dropped. Caller waits via a WaitGroup.
func sinkDrainer(jobs <-chan writeJob, sink dziTileSink, firstErr *error) {
	for job := range jobs {
		if err := sink.WriteTile(job.level, job.col, job.row, job.body); err != nil {
			if *firstErr == nil {
				*firstErr = err
			}
		}
	}
}

// boxDownsample2x reduces an RGBA by 2× in both dimensions via
// 2×2 box averaging. Alpha is set to 0xFF.
func boxDownsample2x(src *image.RGBA) *image.RGBA {
	srcW := src.Bounds().Dx()
	srcH := src.Bounds().Dy()
	w := srcW / 2
	h := srcH / 2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx, sy := x*2, y*2
			i0 := sy*src.Stride + sx*4
			i1 := sy*src.Stride + (sx+1)*4
			i2 := (sy+1)*src.Stride + sx*4
			i3 := (sy+1)*src.Stride + (sx+1)*4
			r := (uint32(src.Pix[i0+0]) + uint32(src.Pix[i1+0]) + uint32(src.Pix[i2+0]) + uint32(src.Pix[i3+0])) / 4
			g := (uint32(src.Pix[i0+1]) + uint32(src.Pix[i1+1]) + uint32(src.Pix[i2+1]) + uint32(src.Pix[i3+1])) / 4
			b := (uint32(src.Pix[i0+2]) + uint32(src.Pix[i1+2]) + uint32(src.Pix[i2+2]) + uint32(src.Pix[i3+2])) / 4
			di := y*dst.Stride + x*4
			dst.Pix[di+0] = byte(r)
			dst.Pix[di+1] = byte(g)
			dst.Pix[di+2] = byte(b)
			dst.Pix[di+3] = 0xFF
		}
	}
	return dst
}
