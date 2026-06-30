package main

import (
	"math"

	"github.com/wsilabs/wsitools/internal/retile"
)

// srcLevelDims is the minimal per-source-level geometry transcodeOctaveLevels
// needs (decoupled from source.Level for testability).
type srcLevelDims struct{ W, H, TileW, TileH int }

// transcodeOctaveLevels maps a source pyramid to the select-octave LevelSpec
// chain for a same-geometry transcode: octaves 0..D (D = the deepest source
// level's octave), box-derived dims, EMITTING only the octaves that match a
// source level (Index = emit position, source tile size) and marking the rest
// Intermediate. Returns ok=false if any source level's ratio to L0 is not a
// clean power of 2 (or geometry is degenerate) — caller falls back to the
// per-level transcode path. Levels are finest-first (octave 0 = L0 = Levels[0]).
func transcodeOctaveLevels(src []srcLevelDims) ([]retile.LevelSpec, bool) {
	if len(src) == 0 {
		return nil, false
	}
	l0 := src[0]
	octaveOf := map[int]srcLevelDims{}
	deepest := 0
	for _, s := range src {
		if s.W <= 0 || l0.W <= 0 || s.TileW <= 0 || s.TileH <= 0 {
			return nil, false
		}
		kW := int(math.Round(math.Log2(float64(l0.W) / float64(s.W))))
		kH := int(math.Round(math.Log2(float64(l0.H) / float64(s.H))))
		if kW < 0 || kW != kH {
			return nil, false // W and H must reduce by the same octave
		}
		k := kW
		// Box-halving L0 k times must reproduce the source dims within a small
		// tolerance (scanners floor/round differently than ceil-halving; the drift
		// is ≤ ~1-2px even for deep pyramids). A genuine non-power-of-2 ratio
		// misses by hundreds of px and is rejected.
		const dimTol = 2
		if abs(ceilHalve(l0.W, k)-s.W) > dimTol || abs(ceilHalve(l0.H, k)-s.H) > dimTol {
			return nil, false
		}
		if _, dup := octaveOf[k]; dup {
			return nil, false
		}
		octaveOf[k] = s
		if k > deepest {
			deepest = k
		}
	}

	levels := make([]retile.LevelSpec, 0, deepest+1)
	emitIdx := 0
	for k := 0; k <= deepest; k++ {
		w := ceilHalve(l0.W, k)
		h := ceilHalve(l0.H, k)
		if s, isEmit := octaveOf[k]; isEmit {
			cols := (w + s.TileW - 1) / s.TileW
			rows := (h + s.TileH - 1) / s.TileH
			levels = append(levels, retile.LevelSpec{
				Index: emitIdx, Width: w, Height: h,
				Cols: cols, Rows: rows, TileW: s.TileW, TileH: s.TileH,
				Overlap: 0, Intermediate: false,
			})
			emitIdx++
		} else {
			// Intermediate octave: never emits tiles, so Index/Cols/Rows are unused;
			// TileH=256 is only the internal box-reduction strip height.
			levels = append(levels, retile.LevelSpec{
				Index: -1, Width: w, Height: h,
				Cols: 0, Rows: 0, TileW: 256, TileH: 256,
				Overlap: 0, Intermediate: true,
			})
		}
	}
	return levels, true
}

// sourceOctaveSet returns the set of octaves {k} at which the source pyramid has
// a level (k = round(log2(L0/Lk))) and the deepest octave, or ok=false if the
// source is not cleanly power-of-2 octave-aligned. Handles INCONSISTENT source
// ratios (e.g. a 4× first step then 2× steps → octaves {0,2,3,4,…}); each level's
// octave is detected independently.
func sourceOctaveSet(src []srcLevelDims) (octaves map[int]bool, deepest int, ok bool) {
	if len(src) == 0 {
		return nil, 0, false
	}
	l0 := src[0]
	octaves = map[int]bool{}
	for _, s := range src {
		if s.W <= 0 || s.H <= 0 || l0.W <= 0 || l0.H <= 0 {
			return nil, 0, false
		}
		kW := int(math.Round(math.Log2(float64(l0.W) / float64(s.W))))
		kH := int(math.Round(math.Log2(float64(l0.H) / float64(s.H))))
		if kW < 0 || kW != kH {
			return nil, 0, false
		}
		k := kW
		const dimTol = 2
		if abs(ceilHalve(l0.W, k)-s.W) > dimTol || abs(ceilHalve(l0.H, k)-s.H) > dimTol {
			return nil, 0, false
		}
		octaves[k] = true
		if k > deepest {
			deepest = k
		}
	}
	return octaves, deepest, true
}

// selectOctaveLevelsFor builds a select-octave LevelSpec chain for an output L0
// of outW×outH that PRESERVES the source pyramid's inter-level downsample ratios
// and level count: it emits only the octaves at which the source has a level
// (re-based onto outW×outH), marking the intermediate octaves non-emitting (they
// feed the box-reduction descent but write no tiles). Returns ok=false if the
// source isn't cleanly octave-aligned, so the caller falls back to a full octave
// pyramid. Used by crop (outW×outH = crop extent, same resolution as the source
// L0, so the source octaves map straight onto it).
func selectOctaveLevelsFor(src []srcLevelDims, outW, outH, tile int) ([]retile.LevelSpec, bool) {
	if tile <= 0 {
		return nil, false
	}
	octaves, deepest, ok := sourceOctaveSet(src)
	if !ok {
		return nil, false
	}
	levels := make([]retile.LevelSpec, 0, deepest+1)
	emitIdx := 0
	for k := 0; k <= deepest; k++ {
		w := ceilHalve(outW, k)
		h := ceilHalve(outH, k)
		if octaves[k] {
			levels = append(levels, retile.LevelSpec{
				Index: emitIdx, Width: w, Height: h,
				Cols: (w + tile - 1) / tile, Rows: (h + tile - 1) / tile,
				TileW: tile, TileH: tile, Overlap: 0, Intermediate: false,
			})
			emitIdx++
		} else {
			levels = append(levels, retile.LevelSpec{
				Index: -1, Width: w, Height: h,
				Cols: 0, Rows: 0, TileW: tile, TileH: tile, Overlap: 0, Intermediate: true,
			})
		}
	}
	return levels, true
}

// ceilHalve halves v (ceil) n times: ceilHalve(v,0)=v.
func ceilHalve(v, n int) int {
	for i := 0; i < n; i++ {
		v = (v + 1) / 2
	}
	return v
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
