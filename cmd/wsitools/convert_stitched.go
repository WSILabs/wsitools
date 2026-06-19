package main

import (
	"context"
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/resample"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// sourceIsOverlapping reports whether any source level has stitched/overlapping
// tiles (a Ventana BIF). Such sources require the engine path (per-tile copy/
// re-encode cannot consume overlapping tiles).
func sourceIsOverlapping(src source.Source) bool {
	for _, lvl := range src.Levels() {
		if lvl.Overlapping() {
			return true
		}
	}
	return false
}

// convertStitchedCOGWSI re-tiles an overlapping source into a COG-WSI via the
// retile engine: decode L0 once (ScaledStrips composites the stitched tiles),
// box-2× derive a floored octave pyramid, re-encode, feed the cogwsiSink, then
// copy associated images via writeCOGWSIAssociated.
func convertStitchedCOGWSI(ctx context.Context, slide *opentile.Slide, src source.Source, w *cogwsiwriter.Writer, plan assocEditPlan, workers int, knobs map[string]string, codecName string) error {
	l0 := slide.Pyramid(0).Levels[0]
	outL0 := opentile.Size{W: l0.Size.W, H: l0.Size.H}
	tile := l0.TileSize.W
	if tile <= 0 {
		tile = 256
	}
	levels := octaveLevelSpecsFor(outL0, tile)

	fac, err := codec.Lookup(codecName)
	if err != nil {
		return err
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{TileWidth: tile, TileHeight: tile, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: knobs})
	if err != nil {
		return err
	}
	defer enc.Close()

	handles := make([]*cogwsiwriter.LevelHandle, len(levels))
	for i, ls := range levels {
		h, err := w.AddLevel(cogwsiwriter.LevelSpec{
			ImageWidth: uint32(ls.Width), ImageHeight: uint32(ls.Height),
			TileWidth: uint32(ls.TileW), TileHeight: uint32(ls.TileH),
			Compression: enc.TIFFCompressionTag(), Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			JPEGTables: enc.LevelHeader(),
			IsL0:       i == 0,
		})
		if err != nil {
			return fmt.Errorf("add level %d: %w", i, err)
		}
		handles[i] = h
	}

	sink := newCogwsiSink(handles, levels)
	runErr := retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: l0.Size},
		OutL0:     outL0,
		Levels:    levels,
		Kernel:    resample.Nearest, // identity scale: ScaledStrips only stitches
		Encoder:   &codecTileEncoder{enc: enc},
		Sink:      sink,
		Workers:   workers,
	})
	// finish() must run unconditionally to drain/join the sink even when Run
	// errored mid-stream; otherwise the streamwriter drain goroutines leak.
	// Prefer the Run error if both fail.
	if ferr := sink.finish(); ferr != nil && runErr == nil {
		runErr = ferr
	}
	if runErr != nil {
		return runErr
	}
	return writeCOGWSIAssociated(w, src, plan)
}

// convertStitchedTIFF re-tiles an overlapping source into an svs/tiff/ome-tiff
// via the retile engine: decode L0 once (ScaledStrips composites the stitched
// tiles), box-2× derive a floored octave pyramid, re-encode, feed the
// streamwriterSink. Per-container LevelSpec shaping (NewSubfileType, WSIImageType,
// L0 ImageDescription) + the SVS thumbnail at IFD 1 + associated images reuse the
// existing helpers. The thumbnail is emitted between L0 and L1 AddLevels so it
// lands at IFD 1 (streamwriter emits IFDs in call order), exactly as
// transcodePyramid does.
func convertStitchedTIFF(ctx context.Context, slide *opentile.Slide, src source.Source, w *streamwriter.Writer, container, srcImageDesc string, plan omeEditPlan, omeSynthetic bool, workers int, fac codec.EncoderFactory, knobs map[string]string) error {
	l0 := slide.Pyramid(0).Levels[0]
	outL0 := opentile.Size{W: l0.Size.W, H: l0.Size.H}
	tile := l0.TileSize.W
	if tile <= 0 {
		tile = 256
	}
	levels := octaveLevelSpecsFor(outL0, tile)

	enc, err := fac.NewEncoder(codec.LevelGeometry{TileWidth: tile, TileHeight: tile, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: knobs})
	if err != nil {
		return err
	}
	defer enc.Close()

	specFor := func(i int) streamwriter.LevelSpec {
		ls := levels[i]
		spec := streamwriter.LevelSpec{
			ImageWidth: uint32(ls.Width), ImageHeight: uint32(ls.Height),
			TileWidth: uint32(ls.TileW), TileHeight: uint32(ls.TileH),
			Compression: enc.TIFFCompressionTag(), Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			JPEGTables:     enc.LevelHeader(),
			NewSubfileType: newSubfileTypeForLevel(i, container),
			WSIImageType:   tiff.WSIImageTypePyramid,
		}
		if i == 0 && srcImageDesc != "" && (container == "svs" || container == "ome-tiff") {
			spec.ExtraTags = buildL0ImageDescriptionTag(srcImageDesc)
		}
		return spec
	}

	handles := make([]*streamwriter.LevelHandle, len(levels))

	// L0 first.
	h0, err := w.AddLevel(specFor(0))
	if err != nil {
		return fmt.Errorf("add level 0: %w", err)
	}
	handles[0] = h0

	// SVS thumbnail at IFD 1 (no-op unless container==svs) — must precede L1.
	if _, err := emitSVSThumbnailAtL0(src, w, 0, container, omeSynthetic, plan); err != nil {
		return err
	}

	// Remaining levels.
	for i := 1; i < len(levels); i++ {
		h, err := w.AddLevel(specFor(i))
		if err != nil {
			return fmt.Errorf("add level %d: %w", i, err)
		}
		handles[i] = h
	}

	sink := newStreamwriterSink(handles)
	runErr := retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: l0.Size},
		OutL0:     outL0,
		Levels:    levels,
		Kernel:    resample.Nearest,
		Encoder:   &codecTileEncoder{enc: enc},
		Sink:      sink,
		Workers:   workers,
	})
	// finish() must run unconditionally to drain/join the per-level drain
	// goroutines even when Run errored mid-stream; otherwise they leak (each is
	// blocked in NextReady until CloseInput). Prefer the Run error if both fail.
	if ferr := sink.finish(); ferr != nil && runErr == nil {
		runErr = ferr
	}
	if runErr != nil {
		return runErr
	}
	return writeAssociatedImages(src, w, container, omeSynthetic, plan)
}
