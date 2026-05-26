package dzi

import "testing"

func TestMaxLevel(t *testing.T) {
	cases := []struct{ w, h, want int }{
		{1, 1, 0},
		{2, 2, 1},
		{256, 256, 8},
		{2220, 2967, 12},
		{32914, 27243, 16},
	}
	for _, c := range cases {
		got := MaxLevel(c.w, c.h)
		if got != c.want {
			t.Errorf("MaxLevel(%d,%d)=%d, want %d", c.w, c.h, got, c.want)
		}
	}
}

func TestLevelDims(t *testing.T) {
	max := MaxLevel(2220, 2967)
	w, h := LevelDims(2220, 2967, 0)
	if w != 1 || h != 1 {
		t.Errorf("L0: %dx%d want 1x1", w, h)
	}
	w, h = LevelDims(2220, 2967, max)
	if w != 2220 || h != 2967 {
		t.Errorf("L%d: %dx%d want 2220x2967", max, w, h)
	}
}

func TestGridDims(t *testing.T) {
	cols, rows := GridDims(2220, 2967, 256)
	if cols != 9 || rows != 12 {
		t.Errorf("GridDims: %dx%d want 9x12", cols, rows)
	}
}

func TestEdgeTileDims(t *testing.T) {
	w, h := EdgeTileDims(2220, 2967, 256, 8, 11)
	if w != 172 || h != 151 {
		t.Errorf("EdgeTileDims corner: %dx%d want 172x151", w, h)
	}
	w, h = EdgeTileDims(2220, 2967, 256, 0, 0)
	if w != 256 || h != 256 {
		t.Errorf("EdgeTileDims interior: %dx%d want 256x256", w, h)
	}
}
