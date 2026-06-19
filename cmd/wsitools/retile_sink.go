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
type codecTileEncoder struct {
	enc codec.Encoder
}

func (e *codecTileEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	return e.enc.EncodeTile(rgb, w, h, nil)
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
