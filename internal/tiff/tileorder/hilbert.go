package tileorder

import (
	"sort"
	"sync"
)

// HilbertCurve emits tiles along a Hilbert space-filling curve. Better
// 2-D locality than Morton (consecutive emission indices are adjacent
// tiles). On non-pow2 grids the effective order is the rank of each
// (x,y) when sorted by the next-pow2 Hilbert index. Rank tables are
// memoized per (tilesX,tilesY) shape.
var HilbertCurve OrderStrategy = &hilbertStrategy{}

type hilbertStrategy struct {
	cache sync.Map // key: gridKey, value: *gridTables
}

func (*hilbertStrategy) Name() string { return "hilbert" }

func (h *hilbertStrategy) Index(x, y, W, H uint32) uint32 {
	t := h.tables(W, H)
	return t.xyToIdx[y*W+x]
}

func (h *hilbertStrategy) IndexToXY(idx, W, H uint32) (x, y uint32) {
	t := h.tables(W, H)
	packed := t.idxToXY[idx]
	return packed % W, packed / W
}

func (h *hilbertStrategy) tables(W, H uint32) *gridTables {
	key := gridKey{W, H}
	if v, ok := h.cache.Load(key); ok {
		return v.(*gridTables)
	}
	t := buildHilbertTables(W, H)
	actual, _ := h.cache.LoadOrStore(key, t)
	return actual.(*gridTables)
}

// hilbertD2XY: convert distance d along a curve in an n×n grid to (x,y).
// Standard iterative algorithm from Warren, Hacker's Delight §14-3.
// n must be a power of 2.
func hilbertD2XY(n, d uint32) (x, y uint32) {
	var rx, ry uint32
	t := d
	for s := uint32(1); s < n; s *= 2 {
		rx = 1 & (t / 2)
		ry = 1 & (t ^ rx)
		x, y = hilbertRot(s, x, y, rx, ry)
		x += s * rx
		y += s * ry
		t /= 4
	}
	return x, y
}

// hilbertXY2D: inverse of hilbertD2XY.
func hilbertXY2D(n, x, y uint32) uint32 {
	var rx, ry, d uint32
	for s := n / 2; s > 0; s /= 2 {
		if x&s > 0 {
			rx = 1
		} else {
			rx = 0
		}
		if y&s > 0 {
			ry = 1
		} else {
			ry = 0
		}
		d += s * s * ((3 * rx) ^ ry)
		x, y = hilbertRot(s, x, y, rx, ry)
	}
	return d
}

// hilbertRot rotates/flips a quadrant appropriately.
func hilbertRot(n, x, y, rx, ry uint32) (uint32, uint32) {
	if ry == 0 {
		if rx == 1 {
			x = n - 1 - x
			y = n - 1 - y
		}
		x, y = y, x
	}
	return x, y
}

func nextPow2(v uint32) uint32 {
	if v <= 1 {
		return 1
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	return v + 1
}

func buildHilbertTables(W, H uint32) *gridTables {
	total := W * H
	side := nextPow2(W)
	if h := nextPow2(H); h > side {
		side = h
	}

	type entry struct {
		hilbert uint32
		packed  uint32 // y*W + x in original grid
	}
	entries := make([]entry, 0, total)
	for y := uint32(0); y < H; y++ {
		for x := uint32(0); x < W; x++ {
			entries = append(entries, entry{
				hilbert: hilbertXY2D(side, x, y),
				packed:  y*W + x,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].hilbert < entries[j].hilbert })

	t := &gridTables{
		xyToIdx: make([]uint32, total),
		idxToXY: make([]uint32, total),
	}
	for emitIdx, e := range entries {
		t.xyToIdx[e.packed] = uint32(emitIdx)
		t.idxToXY[emitIdx] = e.packed
	}
	return t
}

func init() { register(HilbertCurve) }
