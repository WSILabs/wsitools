package main

import (
	"context"
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/resample"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
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
	if err := retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: l0.Size},
		OutL0:     outL0,
		Levels:    levels,
		Kernel:    resample.Nearest, // identity scale: ScaledStrips only stitches
		Encoder:   &codecTileEncoder{enc: enc},
		Sink:      sink,
		Workers:   workers,
	}); err != nil {
		return err
	}
	if err := sink.finish(); err != nil {
		return err
	}
	return writeCOGWSIAssociated(w, src, plan)
}
