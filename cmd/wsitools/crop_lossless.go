package main

import (
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// levelJPEGTables returns the source level's raw JPEGTables (TIFF tag 347), the
// shared DQT/DHT prefix referenced by every abbreviated tile. The streamwriter
// writes LevelSpec.JPEGTables verbatim into tag 347, so a byte-identical copy
// must use the RAW tag-347 payload here. NOTE: Level.TilePrefix() is NOT this —
// it returns a derived splice prefix (tables reshaped for in-place JPEG
// splicing, e.g. with APP14), which would mismatch the source's tag 347.
func levelJPEGTables(l *opentile.Level) []byte {
	if tags, ok := l.TIFFTags(); ok {
		if t, ok := tags.Tag(347); ok {
			return t.Raw
		}
	}
	return nil
}

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
// L0 tiles VERBATIM, reproducing the source's on-disk storage byte-for-byte: the
// abbreviated tile BODIES (TileBodyInto — the bytes as stored, WITHOUT the shared
// JPEG-tables prefix) plus the level's raw shared tables carried once in tag 347
// (JPEGTables=levelJPEGTables). This mirrors how encodeAndWriteLevel writes abbreviated
// tiles + a shared tables blob. (Do NOT use Level.Tile here: it splices the
// prefix+APP14 into each tile, which — combined with the tag-347 JPEGTables —
// would double the tables on read-back.) The crop origin must be tile-aligned
// (see snapRectToTiles) so output tile (ox,oy) maps 1:1 onto source tile
// (stx0+ox, sty0+oy). outW/outH are the snapped L0 dims.
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
		JPEGTables:      levelJPEGTables(srcL0), // raw tag-347 (NOT TilePrefix; see above)
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

	// Scratch buffer reused across the sequential submit loop; each tile's bytes
	// are copied into a fresh slice before WriteTile, since the reorder buffer
	// holds the slice for the deferred (concurrent) write.
	scratch := make([]byte, srcL0.TileBodyMaxSize())
	var submitErr error
	for oy := 0; oy < outTilesY && submitErr == nil; oy++ {
		for ox := 0; ox < outTilesX; ox++ {
			n, err := srcL0.TileBodyInto(stx0+ox, sty0+oy, scratch)
			if err != nil {
				submitErr = fmt.Errorf("read source tile body (%d,%d): %w", stx0+ox, sty0+oy, err)
				break
			}
			body := make([]byte, n)
			copy(body, scratch[:n])
			if err := lh.WriteTile(uint32(ox), uint32(oy), body); err != nil {
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
