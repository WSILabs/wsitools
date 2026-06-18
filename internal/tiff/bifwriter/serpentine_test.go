package bifwriter

import "testing"

func TestImageToSerpentineAnchors(t *testing.T) {
	// 24 cols x 21 rows, anchors from opentile-go formats/bif/serpentine_test.go.
	cases := []struct {
		col, row, want int
	}{
		{0, 20, 0},   // bottom-left image tile -> serpentine index 0
		{23, 20, 23}, // bottom-right -> 23 (stage row 0, L->R)
		{23, 19, 24}, // one up, right edge -> 24 (stage row 1, R->L starts at right)
		{0, 0, 480},  // top-left image tile -> last-ish
	}
	for _, c := range cases {
		if got := imageToSerpentine(c.col, c.row, 24, 21); got != c.want {
			t.Errorf("imageToSerpentine(%d,%d,24,21) = %d, want %d", c.col, c.row, got, c.want)
		}
	}
	if got := imageToSerpentine(24, 0, 24, 21); got != -1 {
		t.Errorf("out-of-grid col should be -1, got %d", got)
	}
}

func TestSerpentineRoundTrip(t *testing.T) {
	const cols, rows = 7, 5
	seen := make([]bool, cols*rows)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			idx := imageToSerpentine(col, row, cols, rows)
			if idx < 0 || idx >= cols*rows {
				t.Fatalf("idx out of range for (%d,%d): %d", col, row, idx)
			}
			if seen[idx] {
				t.Fatalf("idx %d produced twice (not a bijection)", idx)
			}
			seen[idx] = true
			gc, gr := serpentineToImage(idx, cols, rows)
			if gc != col || gr != row {
				t.Errorf("serpentineToImage(%d) = (%d,%d), want (%d,%d)", idx, gc, gr, col, row)
			}
		}
	}
}
