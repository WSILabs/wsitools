package main

import "github.com/wsilabs/wsitools/internal/codec"

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
