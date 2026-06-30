package main

import (
	"context"

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

// sumLevelTiles sums Cols*Rows across all output levels (for the progress bar).
// Intermediate (non-emitting) levels carry Cols=Rows=0, so they contribute 0.
func sumLevelTiles(levels []retile.LevelSpec) int64 {
	var n int64
	for _, l := range levels {
		n += int64(l.Cols) * int64(l.Rows)
	}
	return n
}

// runEngineRetile runs one streaming retile pass over srcRegion → outL0. The
// kernel is Nearest at identity scale (crop / factor-1) and Box on a real
// downscale (downsample). It shows a progress bar (via the engine's per-tile
// hook) and ALWAYS finishes the sink (joining drains), preferring the Run error.
func runEngineRetile(ctx context.Context, slide *opentile.Slide, srcRegion opentile.Region, outL0 opentile.Size, levels []retile.LevelSpec, enc retile.TileEncoder, sink retileSink, workers int) error {
	kernel := resample.Box
	if outL0 == srcRegion.Size {
		kernel = resample.Nearest // identity read (crop): no resampling
	}
	bar := newTileProgress("encoding", sumLevelTiles(levels))
	runErr := retile.Run(ctx, retile.Spec{
		Slide:         slide,
		SrcRegion:     srcRegion,
		OutL0:         outL0,
		Levels:        levels,
		Kernel:        kernel,
		Encoder:       enc,
		Sink:          sink,
		Workers:       workers,
		OnTileWritten: bar.Increment,
	})
	if ferr := sink.finish(); ferr != nil && runErr == nil {
		runErr = ferr
	}
	bar.Wait()
	return runErr
}
