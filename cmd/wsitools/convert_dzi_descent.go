package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
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

// rgbImage is a 3-byte-per-pixel RGB image. Mirrors image.RGBA's
// shape (Pix + Stride + bounds) but skips the alpha channel that
// our pipeline doesn't use. Source pixels arrive from opentile-go
// as RGB; libjpeg-turbo's EncodeStandalone accepts RGB; carrying
// RGBA in between was pure overhead.
type rgbImage struct {
	Pix    []byte // length = h * Stride; pixel (x,y) at [y*Stride + x*3]
	Stride int    // = w * 3
	W, H   int
}

// rgbPixPool pools the backing []byte slice for rgbImage tile
// destinations produced by assembleTile and consumed by the encoder.
// Capacity grows on demand. Ownership: assembleTile borrows; encoder
// releases after EncodeStandalone returns.
var rgbPixPool = sync.Pool{
	New: func() any { b := make([]byte, 0, 258*258*3); return &b },
}

// newPooledRGB returns an *rgbImage whose Pix slice is borrowed
// from rgbPixPool. Callers must release via releaseRGB after the
// last read of Pix. The borrowed slice is not zeroed — callers must
// fully overwrite every byte (assembleTile + copyRGBBand do so by
// covering content + topOv*outW + bottomOv*outW + side bands).
func newPooledRGB(w, h int) *rgbImage {
	need := w * h * 3
	b := *(rgbPixPool.Get().(*[]byte))
	if cap(b) < need {
		b = make([]byte, need)
	} else {
		b = b[:need]
	}
	return &rgbImage{Pix: b, Stride: w * 3, W: w, H: h}
}

// releaseRGB returns img.Pix to the pool. Caller must not reference
// img after calling.
func releaseRGB(img *rgbImage) {
	b := img.Pix[:0]
	rgbPixPool.Put(&b)
}

// encodeJob hands a rendered tile to the encoder pool.
type encodeJob struct {
	level, col, row int
	img             *rgbImage
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
	prev, cur, next *rgbImage

	// Accumulator for the strip currently being filled from
	// downsampled parent rows. nil for the L_max builder; the
	// parent feeds full strips directly into feed().
	accum     *rgbImage
	accumRows int

	rowIndex int // next DZI row to emit

	child *levelBuilder
	jobs  chan<- encodeJob
}

// feed accepts one strip at this level's resolution.
//  1. Rotate buffer: prev = cur; cur = next; next = strip.
//  2. If cur != nil, emit DZI tiles for rowIndex.
//  3. Downsample 2× and feed to child (if any).
func (lb *levelBuilder) feed(strip *rgbImage) {
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
func (lb *levelBuilder) acceptDownsampled(half *rgbImage) {
	if lb.accum == nil {
		lb.accum = &rgbImage{
			Pix:    make([]byte, lb.width*lb.cfg.TileSize*3),
			Stride: lb.width * 3,
			W:      lb.width,
			H:      lb.cfg.TileSize,
		}
	}
	halfH := half.H
	halfW := half.W

	for y := 0; y < halfH; y++ {
		if lb.accumRows+y >= lb.cfg.TileSize {
			break
		}
		srcRow := half.Pix[y*half.Stride : y*half.Stride+halfW*3]
		dstRow := lb.accum.Pix[(lb.accumRows+y)*lb.accum.Stride : (lb.accumRows+y)*lb.accum.Stride+halfW*3]
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
		short := &rgbImage{
			Pix:    make([]byte, lb.width*lb.accumRows*3),
			Stride: lb.width * 3,
			W:      lb.width,
			H:      lb.accumRows,
		}
		for y := 0; y < lb.accumRows; y++ {
			copy(short.Pix[y*short.Stride:], lb.accum.Pix[y*lb.accum.Stride:y*lb.accum.Stride+lb.width*3])
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

// assembleTile builds the RGB tile for DZI tile (col, row) from the
// rolling buffer (prev/cur/next) with overlap rules.
func (lb *levelBuilder) assembleTile(col, row int) *rgbImage {
	lw := lb.width
	// Use level height = rows * TileSize for EdgeTileDims; the last
	// row's content height comes from cur's actual height (which may
	// be short on the last strip).
	cw, ch := dzi.EdgeTileDims(lw, lb.rows*lb.cfg.TileSize, lb.cfg.TileSize, col, row)
	if lb.cur != nil && lb.cur.H < lb.cfg.TileSize {
		ch = lb.cur.H
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

	dst := newPooledRGB(outW, outH)
	srcX0 := col*lb.cfg.TileSize - leftOv

	if topOv > 0 && lb.prev != nil {
		prevH := lb.prev.H
		copyRGBBand(dst, 0, lb.prev, prevH-topOv, srcX0, outW, topOv)
	}
	if lb.cur != nil {
		copyRGBBand(dst, topOv, lb.cur, 0, srcX0, outW, ch)
	}
	if bottomOv > 0 && lb.next != nil {
		copyRGBBand(dst, topOv+ch, lb.next, 0, srcX0, outW, bottomOv)
	}
	return dst
}

// copyRGBBand copies a horizontal band between two rgbImages. Out-of-
// range source columns are white-filled (defensive against
// overlap-rule programming errors).
func copyRGBBand(dst *rgbImage, dstY int, src *rgbImage, srcY, srcX, w, h int) {
	srcW := src.W
	for r := 0; r < h; r++ {
		dstRowStart := (dstY + r) * dst.Stride
		srcRowStart := (srcY + r) * src.Stride
		for c := 0; c < w; c++ {
			sx := srcX + c
			var rr, gg, bb byte
			if sx >= 0 && sx < srcW {
				si := srcRowStart + sx*3
				rr = src.Pix[si+0]
				gg = src.Pix[si+1]
				bb = src.Pix[si+2]
			} else {
				rr, gg, bb = 0xFF, 0xFF, 0xFF
			}
			di := dstRowStart + c*3
			dst.Pix[di+0] = rr
			dst.Pix[di+1] = gg
			dst.Pix[di+2] = bb
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

// rgbImageAsImage wraps rgbImage as image.Image for stdlib encoders.
// Reports NRGBA color model with alpha hard-coded to 255 so PNG output
// is logically equivalent to a fully-opaque RGB image (though PNG will
// still write a 4-byte-per-pixel format; PNG output is rare for DZI so
// this is OK).
type rgbImageAsImage struct{ *rgbImage }

func (r *rgbImageAsImage) ColorModel() color.Model { return color.NRGBAModel }
func (r *rgbImageAsImage) Bounds() image.Rectangle { return image.Rect(0, 0, r.W, r.H) }
func (r *rgbImageAsImage) At(x, y int) color.Color {
	i := y*r.Stride + x*3
	return color.NRGBA{R: r.Pix[i+0], G: r.Pix[i+1], B: r.Pix[i+2], A: 0xFF}
}

// encodeTile dispatches per cfg.Format ("jpeg" or "png"). For JPEG,
// passes img.Pix directly to EncodeStandalone (no conversion needed
// since the pipeline now carries RGB throughout). The encoded body
// is a fresh allocation (caller owns).
func encodeTile(img *rgbImage, format string, enc *jpeg.Encoder, quality int) ([]byte, error) {
	switch format {
	case "jpeg":
		body, err := enc.EncodeStandalone(img.Pix, img.W, img.H)
		releaseRGB(img)
		return body, err
	case "png":
		var b bytes.Buffer
		if err := png.Encode(&b, &rgbImageAsImage{img}); err != nil {
			return nil, err
		}
		releaseRGB(img)
		return b.Bytes(), nil
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
		rgb := decoderImageToRGB(img)
		top.feed(rgb)
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

// decoderImageToRGB returns an *rgbImage view or copy of a decoder.Image.
// When src is already RGB, wraps src.Pix directly (zero-copy). This is
// safe because ScaledStrips allocates a fresh *decoder.Image per Next()
// call and top.feed() processes the strip synchronously before the next
// it.Next() call, so the buffer won't be reused while still referenced.
// When src is RGBA, strips the alpha channel into a fresh pooled buffer.
func decoderImageToRGB(img *decoder.Image) *rgbImage {
	if img.Format == decoder.PixelFormatRGB {
		// Zero-copy view — buffer lifetime is bounded by the strip's
		// synchronous processing before the next it.Next() call.
		return &rgbImage{Pix: img.Pix, Stride: img.Stride, W: img.Width, H: img.Height}
	}
	// RGBA → RGB (rare path; would require ScaledStrips to be set to
	// RGBA, which we don't do).
	dst := newPooledRGB(img.Width, img.Height)
	for y := 0; y < img.Height; y++ {
		for x := 0; x < img.Width; x++ {
			si := y*img.Stride + x*4
			di := y*dst.Stride + x*3
			dst.Pix[di+0] = img.Pix[si+0]
			dst.Pix[di+1] = img.Pix[si+1]
			dst.Pix[di+2] = img.Pix[si+2]
		}
	}
	return dst
}

// boxDownsample2x reduces an rgbImage by 2× in both dimensions via
// 2×2 box averaging. Odd-dimension sources are handled by treating
// the last row/column as duplicated (so the output is
// ceil(srcH/2) × ceil(srcW/2)).
func boxDownsample2x(src *rgbImage) *rgbImage {
	srcW := src.W
	srcH := src.H
	if srcW == 0 || srcH == 0 {
		return &rgbImage{Pix: []byte{}, Stride: 0, W: 0, H: 0}
	}
	w := (srcW + 1) / 2
	h := (srcH + 1) / 2
	dst := newPooledRGB(w, h)
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
			i0 := sy0*src.Stride + sx0*3
			i1 := sy0*src.Stride + sx1*3
			i2 := sy1*src.Stride + sx0*3
			i3 := sy1*src.Stride + sx1*3
			r := (uint32(src.Pix[i0+0]) + uint32(src.Pix[i1+0]) + uint32(src.Pix[i2+0]) + uint32(src.Pix[i3+0])) / 4
			g := (uint32(src.Pix[i0+1]) + uint32(src.Pix[i1+1]) + uint32(src.Pix[i2+1]) + uint32(src.Pix[i3+1])) / 4
			b := (uint32(src.Pix[i0+2]) + uint32(src.Pix[i1+2]) + uint32(src.Pix[i2+2]) + uint32(src.Pix[i3+2])) / 4
			di := y*dst.Stride + x*3
			dst.Pix[di+0] = byte(r)
			dst.Pix[di+1] = byte(g)
			dst.Pix[di+2] = byte(b)
		}
	}
	return dst
}
