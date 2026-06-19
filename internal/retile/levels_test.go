package retile

import (
	"testing"

	opentile "github.com/wsilabs/opentile-go"
)

func TestComputeLevelsOctaveDownToOnePx(t *testing.T) {
	// 512×512, tile 256, overlap 1, ratio 2, 10 levels → finest=512, coarsest=1×1.
	got := ComputeLevels(opentile.Size{W: 512, H: 512}, 256, 256, 1, 2, 10)
	if len(got) != 10 {
		t.Fatalf("levels = %d, want 10", len(got))
	}
	if got[0].Index != 0 || got[0].Width != 512 || got[0].Height != 512 {
		t.Errorf("finest = %+v, want Index0 512×512", got[0])
	}
	if got[0].Cols != 2 || got[0].Rows != 2 {
		t.Errorf("finest grid = %d×%d, want 2×2", got[0].Cols, got[0].Rows)
	}
	if got[0].Overlap != 1 {
		t.Errorf("overlap = %d, want 1", got[0].Overlap)
	}
	last := got[9]
	if last.Index != 9 || last.Width != 1 || last.Height != 1 || last.Cols != 1 || last.Rows != 1 {
		t.Errorf("coarsest = %+v, want Index9 1×1 grid1×1", last)
	}
}

func TestComputeLevelsCeilHalvingMatchesDZI(t *testing.T) {
	// Odd dims exercise the ceil-halving identity ceil(ceil(n/2)/2)==ceil(n/4).
	got := ComputeLevels(opentile.Size{W: 300, H: 200}, 256, 256, 1, 2, 4)
	want := [][2]int{{300, 200}, {150, 100}, {75, 50}, {38, 25}}
	for i, w := range want {
		if got[i].Width != w[0] || got[i].Height != w[1] {
			t.Errorf("level %d = %d×%d, want %d×%d", i, got[i].Width, got[i].Height, w[0], w[1])
		}
	}
}

func TestComputeLevelsStopsAtDegenerateDim(t *testing.T) {
	// Asking for more levels than the image supports stops at 1×1.
	got := ComputeLevels(opentile.Size{W: 4, H: 4}, 256, 256, 0, 2, 10)
	if len(got) != 3 {
		t.Fatalf("levels = %d, want 3 (4→2→1)", len(got))
	}
	if got[len(got)-1].Width != 1 || got[len(got)-1].Height != 1 {
		t.Errorf("coarsest = %d×%d, want 1×1", got[len(got)-1].Width, got[len(got)-1].Height)
	}
}

func TestComputeLevelsArbitraryTileSizeAndOverlap(t *testing.T) {
	got := ComputeLevels(opentile.Size{W: 1024, H: 768}, 512, 384, 0, 2, 2)
	if got[0].Cols != 2 || got[0].Rows != 2 {
		t.Errorf("finest grid = %d×%d, want 2×2 (ceil(1024/512)×ceil(768/384))", got[0].Cols, got[0].Rows)
	}
	if got[0].TileW != 512 || got[0].TileH != 384 {
		t.Errorf("tile = %d×%d, want 512×384", got[0].TileW, got[0].TileH)
	}
	if got[0].Overlap != 0 {
		t.Errorf("overlap = %d, want 0", got[0].Overlap)
	}
}
