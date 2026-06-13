package main

import (
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// writeLosslessL0 emits pyramid level 0 by copying a contiguous block of source
// L0 tiles VERBATIM (byte-identical compressed bytes), propagating the source
// level's shared codec prefix (JPEG tables, tag 347). The crop origin must be
// tile-aligned (see snapRectToTiles) so output tile (ox,oy) maps 1:1 onto source
// tile (stx0+ox, sty0+oy). outW/outH are the snapped L0 dimensions.
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
		Photometric:     2, // RGB (Aperio) — same as encodeAndWriteLevel
		SamplesPerPixel: 3,
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
			// Tile() returns a fresh slice per call (stable for the deferred
			// write); do NOT use the buffer-reusing TileInto here.
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
