package tileorder

import "testing"

func TestRowMajorName(t *testing.T) {
	if RowMajor.Name() != "row-major" {
		t.Errorf("Name(): got %q, want %q", RowMajor.Name(), "row-major")
	}
}

func TestRowMajorIndex(t *testing.T) {
	cases := []struct {
		x, y, W, H uint32
		want       uint32
	}{
		{0, 0, 4, 4, 0},
		{3, 0, 4, 4, 3},
		{0, 1, 4, 4, 4},
		{3, 3, 4, 4, 15},
		{5, 2, 7, 11, 2*7 + 5}, // non-square grid
	}
	for _, c := range cases {
		got := RowMajor.Index(c.x, c.y, c.W, c.H)
		if got != c.want {
			t.Errorf("Index(%d,%d,%d,%d): got %d, want %d", c.x, c.y, c.W, c.H, got, c.want)
		}
	}
}

func TestRowMajorRoundTrip(t *testing.T) {
	for _, dims := range [][2]uint32{{8, 8}, {7, 11}, {1, 1}, {17, 3}} {
		W, H := dims[0], dims[1]
		for y := uint32(0); y < H; y++ {
			for x := uint32(0); x < W; x++ {
				idx := RowMajor.Index(x, y, W, H)
				gx, gy := RowMajor.IndexToXY(idx, W, H)
				if gx != x || gy != y {
					t.Errorf("round-trip W=%d H=%d (x=%d,y=%d)→idx=%d→(%d,%d)", W, H, x, y, idx, gx, gy)
				}
			}
		}
	}
}

func TestRowMajorRegistered(t *testing.T) {
	got, err := ByName("row-major")
	if err != nil {
		t.Fatalf("ByName(row-major): %v", err)
	}
	if got != RowMajor {
		t.Errorf("ByName returned %v, want RowMajor", got)
	}
}
