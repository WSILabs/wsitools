package main

import (
	"context"
	"os"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/resample"

	"github.com/wsilabs/wsitools/internal/retile"
)

// retileSink is a retile.TileSink that also knows how to drain/join itself.
// Both streamwriterSink and cogwsiSink satisfy it.
type retileSink interface {
	WriteTile(level, col, row int, encoded []byte) error
	finish() error
}

// countingSink wraps a retileSink, invoking onWrite after each forwarded tile
// (for a progress bar). finish delegates.
type countingSink struct {
	inner   retileSink
	onWrite func()
}

func (c *countingSink) WriteTile(level, col, row int, encoded []byte) error {
	if err := c.inner.WriteTile(level, col, row, encoded); err != nil {
		return err
	}
	if c.onWrite != nil {
		c.onWrite()
	}
	return nil
}

func (c *countingSink) finish() error { return c.inner.finish() }

// sumLevelTiles sums Cols*Rows across all output levels (for the progress bar).
func sumLevelTiles(levels []retile.LevelSpec) int64 {
	var n int64
	for _, l := range levels {
		n += int64(l.Cols) * int64(l.Rows)
	}
	return n
}

// runEngineRetile runs one streaming retile pass over srcRegion → outL0. The
// kernel is Nearest at identity scale (crop / factor-1) and Box on a real
// downscale (downsample). It wraps the sink in a progress bar and ALWAYS
// finishes it (joining drains), preferring the Run error.
func runEngineRetile(ctx context.Context, slide *opentile.Slide, srcRegion opentile.Region, outL0 opentile.Size, levels []retile.LevelSpec, enc retile.TileEncoder, sink retileSink, workers int) error {
	kernel := resample.Box
	if outL0 == srcRegion.Size {
		kernel = resample.Nearest // identity read (crop): no resampling
	}

	var progress *mpb.Progress
	var wrapped retileSink = sink
	if !flagQuiet {
		progress = mpb.New(mpb.WithOutput(os.Stderr))
		bar := progress.AddBar(sumLevelTiles(levels),
			mpb.PrependDecorators(decor.Name("encoding "), decor.Percentage(decor.WCSyncSpace)),
			mpb.AppendDecorators(decor.EwmaSpeed(0, "%.0f tiles/s", 30), decor.Name(" ETA "), decor.EwmaETA(decor.ET_STYLE_GO, 30)),
		)
		wrapped = &countingSink{inner: sink, onWrite: bar.Increment}
	}

	runErr := retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: srcRegion,
		OutL0:     outL0,
		Levels:    levels,
		Kernel:    kernel,
		Encoder:   enc,
		Sink:      wrapped,
		Workers:   workers,
	})
	if ferr := wrapped.finish(); ferr != nil && runErr == nil {
		runErr = ferr
	}
	if progress != nil {
		progress.Wait()
	}
	return runErr
}
