package main

import "testing"

func TestFlooredLevelCount(t *testing.T) {
	cases := []struct {
		w, h, tile, want int
		note             string
	}{
		{1000, 800, 256, 3, "1000→500→250(≤256 stop): L0,1,2"},
		{256, 256, 256, 1, "already ≤ tile: single level"},
		{100, 100, 256, 1, "smaller than tile: single level"},
		{4096, 4096, 256, 5, "4096→2048→1024→512→256(≤256): 5 levels"},
		{300, 90, 256, 1, "min dim 90 ≤ 256 at L0: single level"},
		{4096, 4096, 512, 4, "4096→2048→1024→512(≤512): 4 levels"},
	}
	for _, c := range cases {
		if got := flooredLevelCount(c.w, c.h, c.tile); got != c.want {
			t.Errorf("flooredLevelCount(%d,%d,%d) = %d, want %d (%s)", c.w, c.h, c.tile, got, c.want, c.note)
		}
	}
}
