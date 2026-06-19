package main

import "testing"

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
