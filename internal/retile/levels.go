package retile

import opentile "github.com/wsilabs/opentile-go"

// ComputeLevels derives an output pyramid from the L0 dims, tiling, and
// ratio/count. Levels are finest-first; level k has dims ceil(outL0 / ratio^k)
// (ceil-halving per octave when levelRatio==2). Index == k (engine-relative;
// 0 = finest). The descent stops early if a dimension reaches 1 before
// levelCount is met, so the returned slice may be shorter than levelCount.
//
// levelRatio is currently 2 (octave); the engine's 2× box descent only realizes
// octave pyramids. Other ratios compute correct geometry but are not yet
// produced by Run (reserved for SP3 --level-ratio).
func ComputeLevels(outL0 opentile.Size, tileW, tileH, overlap, levelRatio, levelCount int) []LevelSpec {
	if levelRatio < 2 {
		levelRatio = 2
	}
	levels := make([]LevelSpec, 0, levelCount)
	w, h := outL0.W, outL0.H
	for k := 0; k < levelCount; k++ {
		cols := (w + tileW - 1) / tileW
		rows := (h + tileH - 1) / tileH
		levels = append(levels, LevelSpec{
			Index: k, Width: w, Height: h,
			Cols: cols, Rows: rows, TileW: tileW, TileH: tileH, Overlap: overlap,
		})
		if w <= 1 && h <= 1 {
			break
		}
		w = ceilDiv(w, levelRatio)
		h = ceilDiv(h, levelRatio)
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
	}
	return levels
}

func ceilDiv(n, d int) int {
	if n <= 0 {
		return 1
	}
	return (n + d - 1) / d
}
