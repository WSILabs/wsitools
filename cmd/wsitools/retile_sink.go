package main

import (
	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/codec"
	retile "github.com/wsilabs/wsitools/internal/retile"
)

// codecTileEncoder adapts a codec.Encoder to retile.TileEncoder. EncodeTile
// returns the ABBREVIATED tile body (no DQT/DHT); the level's JPEGTables tag
// (347) carries the shared tables from enc.LevelHeader(). One codecTileEncoder
// is shared across the engine's worker pool — codec.Encoder.EncodeTile is
// concurrency-safe (the existing transcode pipeline shares one the same way).
//
// tileW/tileH, when >0, are the full output tile dimensions. The retile engine
// hands edge/corner tiles (and whole levels smaller than one tile) at their
// truncated CONTENT size; TIFF requires every tile to be a uniform full
// TileWidth×TileLength (edge pixels beyond ImageWidth/Length are padding,
// ignored by readers), and OpenSlide/ImageScope (and the IFE 256px-tile format)
// enforce it — a sub-full-size JPEG tile reads as a "dimensional mismatch" and
// corrupts the slide. So partial tiles are edge-replicated up to the full size
// before encoding. DZI/SZI legitimately use partial edge tiles and go through
// internal/dzi's own encoders, not this type, so they are unaffected.
type codecTileEncoder struct {
	enc          codec.Encoder
	tileW, tileH int
}

func (e *codecTileEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	if e.tileW > 0 && e.tileH > 0 && (w < e.tileW || h < e.tileH) {
		rgb = padRGBTileReplicate(rgb, w, h, e.tileW, e.tileH)
		w, h = e.tileW, e.tileH
	}
	return e.enc.EncodeTile(rgb, w, h, nil)
}

// padRGBTileReplicate copies a w×h tightly-packed RGB tile into a tw×th buffer,
// replicating the last valid column and row across the padding. Edge replication
// (rather than a constant fill) keeps the padded pixels close to the content so
// the JPEG MCUs straddling the boundary don't bleed an alien colour back into
// the visible edge. Requires w>0, h>0, tw>=w, th>=h.
func padRGBTileReplicate(src []byte, w, h, tw, th int) []byte {
	dst := make([]byte, tw*th*3)
	srcStride, dstStride := w*3, tw*3
	for y := 0; y < th; y++ {
		sy := y
		if sy >= h {
			sy = h - 1 // replicate the last source row downward
		}
		srow := src[sy*srcStride : sy*srcStride+w*3]
		drow := dst[y*dstStride : y*dstStride+tw*3]
		copy(drow[:w*3], srow)
		if w < tw {
			last := srow[(w-1)*3 : w*3] // replicate the last source column rightward
			for x := w; x < tw; x++ {
				copy(drow[x*3:x*3+3], last)
			}
		}
	}
	return dst
}

// flooredLevelCount returns the number of octave levels (each half the previous,
// ceil-halving) from native w×h down to and including the first level whose
// smaller dimension is ≤ tile. Always ≥ 1. This floors the engine's octave
// pyramid at a normal WSI bottom (a thumbnail-sized level) rather than 1×1.
func flooredLevelCount(w, h, tile int) int {
	n := 1
	for min2(w, h) > tile {
		w = (w + 1) / 2
		h = (h + 1) / 2
		n++
	}
	return n
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// octaveLevelSpecsFor builds the floored octave LevelSpec list for a stitched
// source: OutL0 dims, square tiles of size tile, overlap 0, halving until the
// smaller dim ≤ tile. Finest-first, Index==k (the engine + sinks agree on this).
func octaveLevelSpecsFor(outL0 opentile.Size, tile int) []retile.LevelSpec {
	return retile.ComputeLevels(outL0, tile, tile, 0 /*overlap*/, 2 /*ratio*/, flooredLevelCount(outL0.W, outL0.H, tile))
}
