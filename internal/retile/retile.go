package retile

import (
	"context"
	"io"
	"runtime"
	"sync"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/opentile-go/resample"
)

// Spec configures one streaming retile pass.
type Spec struct {
	Slide     *opentile.Slide
	SrcRegion opentile.Region // L0-coord source rect (full slide, or a crop)
	OutL0     opentile.Size   // output L0 dims (= Levels[0] dims)
	Levels    []LevelSpec     // output pyramid, finest first (Levels[0] = OutL0 resolution)
	Kernel    resample.Kernel // strip resample kernel (caller picks Nearest at identity, Box on downscale)
	Encoder   TileEncoder
	Sink      TileSink
	Workers   int
}

// Run executes the pass: ScaledStrips → level-builder chain (2× box descent) →
// encoder pool → sink. One L0 decode; memory bounded by the rolling strip
// buffers. Returns the first error from any stage. Requires an octave pyramid
// (each Levels[k+1] ≈ Levels[k]/2); ComputeLevels with levelRatio=2 produces one.
func Run(ctx context.Context, spec Spec) error {
	workers := spec.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if len(spec.Levels) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var encMu sync.Mutex
	var encErr error
	onEncErr := func(e error) {
		encMu.Lock()
		if encErr == nil {
			encErr = e
		}
		encMu.Unlock()
		cancel()
	}

	encodeJobs := make(chan encodeJob, 2*workers)
	writeJobs := make(chan writeJob, 2*workers)

	// Build the level chain finest→coarsest. Levels[0] is the finest (fed by the
	// iterator); each subsequent is the box-downsampled child.
	builders := make([]*levelBuilder, len(spec.Levels))
	for i := range spec.Levels {
		builders[i] = &levelBuilder{spec: spec.Levels[i], jobs: encodeJobs, ctx: ctx}
		if i > 0 {
			builders[i-1].child = builders[i]
		}
	}
	top := builders[0]

	var encWG sync.WaitGroup
	for i := 0; i < workers; i++ {
		encWG.Add(1)
		go func() {
			defer encWG.Done()
			encoderWorker(ctx, encodeJobs, writeJobs, spec.Encoder, onEncErr)
		}()
	}

	var sinkWG sync.WaitGroup
	var sinkErr error
	sinkWG.Add(1)
	go func() {
		defer sinkWG.Done()
		sinkDrainer(writeJobs, spec.Sink, &sinkErr)
	}()

	// workers is always > 0 here (normalized to NumCPU above).
	stripOpts := []opentile.StripOption{
		opentile.WithStripContext(ctx),
		opentile.WithStripKernel(spec.Kernel),
		opentile.WithStripWorkers(workers),
	}
	it := spec.Slide.Pyramid(0).ScaledStrips(spec.SrcRegion, spec.OutL0, spec.Levels[0].TileH, stripOpts...)
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
		top.feed(decoderImageToRGB(img))
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
	encMu.Lock()
	ee := encErr
	encMu.Unlock()
	if ee != nil {
		return ee
	}
	return sinkErr
}

// decoderImageToRGB returns an *RGBImage view (zero-copy when src is already
// RGB) or an alpha-stripped copy. Safe to alias src.Pix: ScaledStrips allocates
// a fresh *decoder.Image per Next() and top.feed processes it synchronously
// before the next Next().
func decoderImageToRGB(img *decoder.Image) *RGBImage {
	if img.Format == decoder.PixelFormatRGB {
		return &RGBImage{Pix: img.Pix, Stride: img.Stride, W: img.Width, H: img.Height}
	}
	dst := newPooledRGB(img.Width, img.Height)
	for y := 0; y < img.Height; y++ {
		for x := 0; x < img.Width; x++ {
			si := y*img.Stride + x*4
			di := y*dst.Stride + x*3
			dst.Pix[di+0], dst.Pix[di+1], dst.Pix[di+2] = img.Pix[si+0], img.Pix[si+1], img.Pix[si+2]
		}
	}
	return dst
}
