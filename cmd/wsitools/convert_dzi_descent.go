package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"runtime"
	"strconv"
	"sync"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/opentile-go/resample"

	"github.com/wsilabs/wsitools/internal/codec"
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

// runDescent drives the v0.17 pyramid-descent pipeline:
// one ScaledStrips iterator → linked levelBuilders → encoder pool
// → serialized sink drain. Returns the first error from any stage.
func runDescent(ctx context.Context, slide *opentile.Slide, sink dziTileSink, cfg dzi.Config, workers, quality int) error {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	encodeJobs := make(chan encodeJob, 2*workers)
	writeJobs := make(chan writeJob, 2*workers)

	// Build the level chain top-down: every loop iter builds a
	// builder one level coarser to finer. After the loop, `top` is
	// the L_max builder; each builder's `child` is the next coarser.
	maxLevel := dzi.MaxLevel(cfg.Width, cfg.Height)
	var coarsest *levelBuilder
	var top *levelBuilder
	for lvl := 0; lvl <= maxLevel; lvl++ {
		lw, lh := dzi.LevelDims(cfg.Width, cfg.Height, lvl)
		cols, rows := dzi.GridDims(lw, lh, cfg.TileSize)
		lb := &levelBuilder{
			level: lvl, width: lw, cfg: cfg,
			cols: cols, rows: rows,
			jobs: encodeJobs,
		}
		if coarsest == nil {
			coarsest = lb
		} else {
			lb.child = top
		}
		top = lb
		_ = lh
	}
	_ = coarsest // retained for clarity; the chain is reachable from top via .child

	// Encoder shared across workers (the cgo call is concurrency-safe).
	enc, err := jpeg.New(
		codec.LevelGeometry{TileWidth: cfg.TileSize, TileHeight: cfg.TileSize},
		codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}},
	)
	if err != nil {
		return fmt.Errorf("jpeg.New: %w", err)
	}
	defer enc.Close()

	var encWG sync.WaitGroup
	for i := 0; i < workers; i++ {
		encWG.Add(1)
		go func() {
			defer encWG.Done()
			encoderWorker(ctx, encodeJobs, writeJobs, cfg, enc, quality)
		}()
	}

	var sinkWG sync.WaitGroup
	var sinkErr error
	sinkWG.Add(1)
	go func() {
		defer sinkWG.Done()
		sinkDrainer(writeJobs, sink, &sinkErr)
	}()

	// Source iterator.
	//
	// At the top of the cascade, outSize == sourceLevel.Size (we read
	// the source at native resolution and downsample in process for
	// every coarser DZI level). No scaling is needed at the top, so
	// use Nearest — Lanczos otherwise dominates ~80% of CPU on the
	// identity-scale read path (profiled May 2026).
	stripOpts := []opentile.StripOption{
		opentile.WithStripContext(ctx),
		opentile.WithStripKernel(resample.Nearest),
	}
	if workers > 0 {
		stripOpts = append(stripOpts, opentile.WithStripWorkers(workers))
	}
	it := slide.ScaledStrips(
		image.Rect(0, 0, cfg.Width, cfg.Height),
		image.Point{X: cfg.Width, Y: cfg.Height},
		cfg.TileSize,
		stripOpts...,
	)
	defer it.Close()

	var srcErr error
	for {
		img, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			srcErr = err
			cancel()
			break
		}
		rgba := decoderImageToRGBA(img)
		top.feed(rgba)
	}
	if srcErr == nil {
		top.flush()
	}

	close(encodeJobs)
	encWG.Wait()
	close(writeJobs)
	sinkWG.Wait()

	if srcErr != nil {
		return srcErr
	}
	return sinkErr
}

// decoderImageToRGBA converts a decoder.Image (RGB or RGBA) into
// an *image.RGBA suitable for level builders.
func decoderImageToRGBA(img *decoder.Image) *image.RGBA {
	w, h := img.Width, img.Height
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if img.Format == decoder.PixelFormatRGBA {
		for y := 0; y < h; y++ {
			copy(dst.Pix[y*dst.Stride:], img.Pix[y*img.Stride:y*img.Stride+w*4])
		}
		return dst
	}
	// RGB → RGBA.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			si := y*img.Stride + x*3
			di := y*dst.Stride + x*4
			dst.Pix[di+0] = img.Pix[si+0]
			dst.Pix[di+1] = img.Pix[si+1]
			dst.Pix[di+2] = img.Pix[si+2]
			dst.Pix[di+3] = 0xFF
		}
	}
	return dst
}

// boxDownsample2x reduces an RGBA by 2× in both dimensions via
// 2×2 box averaging. Alpha is set to 0xFF. Odd-dimension sources
// are handled by treating the last row/column as duplicated (so the
// output is ceil(srcH/2) × ceil(srcW/2)).
func boxDownsample2x(src *image.RGBA) *image.RGBA {
	srcW := src.Bounds().Dx()
	srcH := src.Bounds().Dy()
	if srcW == 0 || srcH == 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	w := (srcW + 1) / 2
	h := (srcH + 1) / 2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx0, sy0 := x*2, y*2
			sx1 := sx0 + 1
			if sx1 >= srcW {
				sx1 = srcW - 1
			}
			sy1 := sy0 + 1
			if sy1 >= srcH {
				sy1 = srcH - 1
			}
			i0 := sy0*src.Stride + sx0*4
			i1 := sy0*src.Stride + sx1*4
			i2 := sy1*src.Stride + sx0*4
			i3 := sy1*src.Stride + sx1*4
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
