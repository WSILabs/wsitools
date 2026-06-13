package main

import (
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// levelPhotometric returns the source level's PhotometricInterpretation (TIFF
// tag 262) so a verbatim tile copy declares the same colorspace the source did
// (e.g. 2=RGB or 6=YCbCr). A re-encode path can hardcode RGB, but a byte-copy
// must not relabel the data. Defaults to 2 (RGB, the Aperio convention) when the
// tag is unavailable.
func levelPhotometric(l *opentile.Level) uint16 {
	if tags, ok := l.TIFFTags(); ok {
		if t, ok := tags.Tag(262); ok {
			if vals, ok := t.Uints(); ok && len(vals) > 0 {
				return uint16(vals[0])
			}
		}
	}
	return 2
}

// writeLosslessL0 emits pyramid level 0 by copying a contiguous block of source
// L0 tiles VERBATIM (byte-identical compressed bytes), propagating the source
// level's shared codec prefix (JPEG tables, tag 347) and photometric. The crop
// origin must be tile-aligned (see snapRectToTiles) so output tile (ox,oy) maps
// 1:1 onto source tile (stx0+ox, sty0+oy). outW/outH are the snapped L0 dims.
//
// It mirrors encodeAndWriteLevel's concurrent NextReady drain (so non-row-major
// --tile-order still works and the reorder buffer never deadlocks on large
// grids), but the per-tile work is a raw byte copy instead of an encode.
func writeLosslessL0(w *streamwriter.Writer, srcL0 *opentile.Level, stx0, sty0, outTilesX, outTilesY, outW, outH int) error {
	lh, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth:      uint32(outW),
		ImageHeight:     uint32(outH),
		TileWidth:       uint32(srcL0.TileSize.W),
		TileHeight:      uint32(srcL0.TileSize.H),
		Compression:     opentile.CompressionToTIFFTag(srcL0.Compression),
		Photometric:     levelPhotometric(srcL0), // match source (verbatim copy must not relabel)
		SamplesPerPixel: 3,                       // SVS L0 is always 3×8-bit
		BitsPerSample:   []uint16{8, 8, 8},
		JPEGTables:      srcL0.TilePrefix(), // shared tag-347 tables (nil if none)
		NewSubfileType:  0,
		WSIImageType:    tiff.WSIImageTypePyramid,
	})
	if err != nil {
		return fmt.Errorf("AddLevel: %w", err)
	}

	drainErr := make(chan error, 1)
	go func() {
		for {
			idx, bytes, ok, err := lh.NextReady()
			if err != nil {
				drainErr <- err
				return
			}
			if !ok {
				drainErr <- nil
				return
			}
			if err := lh.WriteTileAtIndex(idx, bytes); err != nil {
				lh.Abort(err)
				drainErr <- err
				return
			}
		}
	}()

	var submitErr error
	for oy := 0; oy < outTilesY && submitErr == nil; oy++ {
		for ox := 0; ox < outTilesX; ox++ {
			// Level.Tile allocates a fresh []byte per call (opentile-go
			// level_reads.go: ImageRawTile), so the slice stays stable while the
			// reorder buffer holds it for the deferred write. Do NOT switch to
			// the buffer-reusing TileInto here without adding a copy.
			tile, err := srcL0.Tile(stx0+ox, sty0+oy)
			if err != nil {
				submitErr = fmt.Errorf("read source tile (%d,%d): %w", stx0+ox, sty0+oy, err)
				break
			}
			if err := lh.WriteTile(uint32(ox), uint32(oy), tile); err != nil {
				submitErr = err
				break
			}
		}
	}
	lh.CloseInput()
	if submitErr != nil {
		lh.Abort(submitErr)
		<-drainErr
		return submitErr
	}
	return <-drainErr
}
