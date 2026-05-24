package tileorder

import (
	"sort"
	"sync"
)

// Morton emits tiles in Morton (Z-order) sequence. On power-of-2 grids
// this is the canonical bit-interleaved Z-curve. On non-pow2 grids, the
// effective order is the rank of each (x,y) when sorted by Morton
// interleaved index. Rank tables are memoized per (tilesX,tilesY) shape.
var Morton OrderStrategy = &mortonStrategy{}

type mortonStrategy struct {
	cache sync.Map // key: gridKey, value: *gridTables
}

type gridKey struct{ W, H uint32 }

type gridTables struct {
	xyToIdx []uint32 // length W*H; xyToIdx[y*W+x] = emission idx
	idxToXY []uint32 // length W*H; idxToXY[idx] = y*W+x
}

func (*mortonStrategy) Name() string { return "morton" }

func (m *mortonStrategy) Index(x, y, W, H uint32) uint32 {
	t := m.tables(W, H)
	return t.xyToIdx[y*W+x]
}

func (m *mortonStrategy) IndexToXY(idx, W, H uint32) (x, y uint32) {
	t := m.tables(W, H)
	packed := t.idxToXY[idx]
	return packed % W, packed / W
}

func (m *mortonStrategy) tables(W, H uint32) *gridTables {
	key := gridKey{W, H}
	if v, ok := m.cache.Load(key); ok {
		return v.(*gridTables)
	}
	t := buildMortonTables(W, H)
	actual, _ := m.cache.LoadOrStore(key, t)
	return actual.(*gridTables)
}

// mortonBits interleaves the low bits of x and y. Sufficient for our
// tile counts (rarely exceed 2^16 in either dimension).
func mortonBits(x, y uint32) uint64 {
	return spread(uint64(x)) | (spread(uint64(y)) << 1)
}

func spread(v uint64) uint64 {
	v &= 0xFFFFFFFF
	v = (v | (v << 16)) & 0x0000FFFF0000FFFF
	v = (v | (v << 8)) & 0x00FF00FF00FF00FF
	v = (v | (v << 4)) & 0x0F0F0F0F0F0F0F0F
	v = (v | (v << 2)) & 0x3333333333333333
	v = (v | (v << 1)) & 0x5555555555555555
	return v
}

func buildMortonTables(W, H uint32) *gridTables {
	total := W * H
	type entry struct {
		morton uint64
		packed uint32 // y*W + x
	}
	entries := make([]entry, 0, total)
	for y := uint32(0); y < H; y++ {
		for x := uint32(0); x < W; x++ {
			entries = append(entries, entry{morton: mortonBits(x, y), packed: y*W + x})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].morton < entries[j].morton })

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

func init() { register(Morton) }
