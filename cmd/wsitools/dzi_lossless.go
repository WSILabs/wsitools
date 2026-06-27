package main

import "fmt"

type losslessDZIInputs struct {
	isJPEG          bool
	srcTileSize     int
	factor          int
	rectSet         bool
	userSetTileSize bool
	userSetOverlap  bool
	reqTileSize     int
	reqOverlap      int
}

type losslessDZIResolved struct {
	tileSize int
	overlap  int
}

// losslessDZIConfig validates a lossless DZI/SZI request and resolves the tile
// grid (tile-size == source, overlap 0) that makes verbatim base-tile copy
// possible. An explicit conflicting --tile-size/--dzi-overlap is an error,
// not a silent override.
func losslessDZIConfig(in losslessDZIInputs) (losslessDZIResolved, error) {
	if !in.isJPEG {
		return losslessDZIResolved{}, fmt.Errorf("--lossless requires a JPEG source (Deep Zoom tiles are jpeg/png)")
	}
	if in.factor != 1 {
		return losslessDZIResolved{}, fmt.Errorf("--lossless cannot be combined with --factor/--target-mag (verbatim tiles can't be downsampled)")
	}
	if in.rectSet {
		return losslessDZIResolved{}, fmt.Errorf("--lossless --to dzi|szi is full-slide only (no --rect) in this release")
	}
	if in.userSetTileSize && in.reqTileSize != in.srcTileSize {
		return losslessDZIResolved{}, fmt.Errorf("--lossless requires the DZI tile size to match the source (%d); drop --tile-size or set it to %d", in.srcTileSize, in.srcTileSize)
	}
	if in.userSetOverlap && in.reqOverlap != 0 {
		return losslessDZIResolved{}, fmt.Errorf("--lossless requires --dzi-overlap 0 (overlap re-cuts tiles); drop --dzi-overlap")
	}
	return losslessDZIResolved{tileSize: in.srcTileSize, overlap: 0}, nil
}
