// Package dzi provides Microsoft Deep Zoom Image format primitives:
// coordinate math, manifest emission, and a tile-tree writer. The
// reader-side equivalents live in opentile-go's internal/dzi; the
// two packages share the same level / grid / tile-coordinate math
// so writer output round-trips through the reader.
package dzi

import "math"

// MaxLevel returns the highest DZI level for an image of size w×h.
// DZI levels are 0..MaxLevel inclusive; level 0 is 1×1, level
// MaxLevel is native size.
func MaxLevel(w, h int) int {
	m := w
	if h > m {
		m = h
	}
	if m <= 1 {
		return 0
	}
	return int(math.Ceil(math.Log2(float64(m))))
}

// LevelDims returns the pixel dimensions of DZI level lvl for an
// image of native size w×h. Level MaxLevel is native; each coarser
// level halves dimensions (rounded up), down to 1×1 at level 0.
func LevelDims(w, h, lvl int) (int, int) {
	max := MaxLevel(w, h)
	scale := 1 << (max - lvl)
	if scale <= 0 {
		return w, h
	}
	lw := (w + scale - 1) / scale
	lh := (h + scale - 1) / scale
	if lw < 1 {
		lw = 1
	}
	if lh < 1 {
		lh = 1
	}
	return lw, lh
}

// GridDims returns the column/row count of the tile grid for an
// image of size w×h with the given tile size. Overlap does NOT
// change the grid count.
func GridDims(w, h, tileSize int) (int, int) {
	if tileSize <= 0 {
		return 0, 0
	}
	cols := (w + tileSize - 1) / tileSize
	rows := (h + tileSize - 1) / tileSize
	return cols, rows
}

// EdgeTileDims returns the actual content dimensions of the tile
// at (col, row) for an image of size w×h with the given tile size.
// Interior tiles return (tileSize, tileSize); right/bottom edge
// tiles return the truncated remainder. Overlap is added separately.
func EdgeTileDims(w, h, tileSize, col, row int) (int, int) {
	tw := tileSize
	if (col+1)*tileSize > w {
		tw = w - col*tileSize
	}
	th := tileSize
	if (row+1)*tileSize > h {
		th = h - row*tileSize
	}
	return tw, th
}
