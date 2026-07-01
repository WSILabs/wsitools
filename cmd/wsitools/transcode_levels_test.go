package main

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/retile"
)

func TestTranscodeOctaveLevels_PowerOfTwo(t *testing.T) {
	// L0 46000×40000 (tile 256), L1 = /4 (octave 2), L2 = /16 (octave 4).
	src := []srcLevelDims{
		{W: 46000, H: 40000, TileW: 256, TileH: 256},
		{W: 11500, H: 10000, TileW: 256, TileH: 256},
		{W: 2875, H: 2500, TileW: 256, TileH: 256},
	}
	levels, ok := transcodeOctaveLevels(src)
	if !ok {
		t.Fatal("expected ok for power-of-2 source")
	}
	if len(levels) != 5 {
		t.Fatalf("chain length = %d, want 5 (octaves 0..4)", len(levels))
	}
	emit := map[int]bool{}
	for k, l := range levels {
		if l.Intermediate {
			if k != 1 && k != 3 {
				t.Errorf("octave %d marked Intermediate, expected only 1,3", k)
			}
		} else {
			emit[k] = true
		}
	}
	if !emit[0] || !emit[2] || !emit[4] {
		t.Errorf("emitted octaves = %v, want {0,2,4}", emit)
	}
	if levels[0].Index != 0 || levels[2].Index != 1 || levels[4].Index != 2 {
		t.Errorf("emit indices = [%d,%d,%d], want [0,1,2]", levels[0].Index, levels[2].Index, levels[4].Index)
	}
	if levels[0].Width != 46000 || levels[0].Height != 40000 {
		t.Errorf("L0 = %d×%d, want 46000×40000", levels[0].Width, levels[0].Height)
	}
	if levels[2].Width != 11500 || levels[2].Height != 10000 {
		t.Errorf("octave2 = %d×%d, want 11500×10000", levels[2].Width, levels[2].Height)
	}
	if levels[2].TileW != 256 || levels[2].Cols != (11500+255)/256 {
		t.Errorf("octave2 tile/grid wrong: TileW=%d Cols=%d", levels[2].TileW, levels[2].Cols)
	}
}

func TestTranscodeOctaveLevels_NonPowerOfTwo(t *testing.T) {
	src := []srcLevelDims{
		{W: 9000, H: 9000, TileW: 256, TileH: 256},
		{W: 3000, H: 3000, TileW: 256, TileH: 256}, // ratio 3 — not a clean octave
	}
	if _, ok := transcodeOctaveLevels(src); ok {
		t.Error("expected ok=false for ratio-3 source")
	}
}

func TestTranscodeOctaveLevels_SingleLevel(t *testing.T) {
	src := []srcLevelDims{{W: 1000, H: 800, TileW: 256, TileH: 256}}
	levels, ok := transcodeOctaveLevels(src)
	if !ok || len(levels) != 1 || levels[0].Intermediate || levels[0].Index != 0 {
		t.Errorf("single-level: ok=%v levels=%d %+v", ok, len(levels), levels)
	}
}

func TestTranscodeOctaveLevels_ZeroTile(t *testing.T) {
	src := []srcLevelDims{{W: 1000, H: 800, TileW: 0, TileH: 0}}
	if _, ok := transcodeOctaveLevels(src); ok {
		t.Error("expected ok=false for zero tile size")
	}
}

func TestTranscodeOctaveLevels_RealSVSOddHeight(t *testing.T) {
	// Real CMU-1.svs: L0 46000×32914, L1 = floor /4 (11500×8228), L2 = floor /16
	// (2875×2057). ceilHalve(32914,2)=8229 != source 8228 — must still be accepted
	// via tolerance, else the engine path is never taken for real SVS.
	src := []srcLevelDims{
		{W: 46000, H: 32914, TileW: 256, TileH: 256},
		{W: 11500, H: 8228, TileW: 256, TileH: 256},
		{W: 2875, H: 2057, TileW: 256, TileH: 256},
	}
	levels, ok := transcodeOctaveLevels(src)
	if !ok {
		t.Fatal("real CMU-1 dims must be accepted (tolerance), else M4 never engages")
	}
	if len(levels) != 5 {
		t.Fatalf("chain = %d, want 5 (octaves 0..4)", len(levels))
	}
	// Emitted at 0,2,4; output dims are box-derived (H = ceilHalve, ±1 vs source).
	if levels[0].Intermediate || levels[2].Intermediate || levels[4].Intermediate {
		t.Error("octaves 0,2,4 must be emitted")
	}
	if !levels[1].Intermediate || !levels[3].Intermediate {
		t.Error("octaves 1,3 must be Intermediate")
	}
	if levels[2].Height != 8229 || levels[4].Height != 2058 {
		t.Errorf("box-derived H = [%d,%d], want [8229,2058] (ceil-halve, not source 8228/2057)", levels[2].Height, levels[4].Height)
	}
}

func emitDims(levels []retile.LevelSpec) [][2]int {
	var out [][2]int
	for _, l := range levels {
		if !l.Intermediate {
			out = append(out, [2]int{l.Width, l.Height})
		}
	}
	return out
}

// TestSelectOctaveLevelsFor_PreservesSourceRatios: a standard 4× source (octaves
// 0,2,4) maps onto a crop L0 as 3 emitted levels at 1×/4×/16×, with the 2×/8×
// octaves marked intermediate.
func TestSelectOctaveLevelsFor_PreservesSourceRatios(t *testing.T) {
	src := []srcLevelDims{
		{16000, 16000, 256, 256}, // 1×
		{4000, 4000, 256, 256},   // 4×  (octave 2)
		{1000, 1000, 256, 256},   // 16× (octave 4)
	}
	levels, ok := selectOctaveLevelsFor(src, 8000, 8000, 256)
	if !ok {
		t.Fatal("ok=false, want true for octave-aligned source")
	}
	got := emitDims(levels)
	want := [][2]int{{8000, 8000}, {2000, 2000}, {500, 500}} // 1×, 4×, 16× of the 8000 crop L0
	if len(got) != len(want) {
		t.Fatalf("emitted %d levels (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("emit level %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestSelectOctaveLevelsFor_InconsistentRatios: Grundium-style 4× then 2× steps
// (octaves 0,2,3) must be preserved, not normalized to a uniform octave.
func TestSelectOctaveLevelsFor_InconsistentRatios(t *testing.T) {
	src := []srcLevelDims{
		{16000, 16000, 256, 256}, // 1×
		{4000, 4000, 256, 256},   // 4×  (octave 2)
		{2000, 2000, 256, 256},   // 8×  (octave 3)
	}
	levels, ok := selectOctaveLevelsFor(src, 16000, 16000, 256)
	if !ok {
		t.Fatal("ok=false, want true")
	}
	got := emitDims(levels)
	want := [][2]int{{16000, 16000}, {4000, 4000}, {2000, 2000}} // 1×, 4×, 8×
	if len(got) != len(want) {
		t.Fatalf("emitted %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("emit level %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestSelectOctaveLevelsFor_NonOctaveFallsBack: a non-power-of-2 source ratio
// yields ok=false so the caller uses a full octave pyramid.
func TestSelectOctaveLevelsFor_NonOctaveFallsBack(t *testing.T) {
	src := []srcLevelDims{
		{16000, 16000, 256, 256},
		{5000, 5000, 256, 256}, // ratio 3.2× — not octave-aligned
	}
	if _, ok := selectOctaveLevelsFor(src, 16000, 16000, 256); ok {
		t.Error("ok=true, want false for non-octave-aligned source")
	}
}
