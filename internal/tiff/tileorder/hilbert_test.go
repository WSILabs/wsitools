package tileorder

import "testing"

func TestHilbertName(t *testing.T) {
	if HilbertCurve.Name() != "hilbert" {
		t.Errorf("Name(): got %q, want %q", HilbertCurve.Name(), "hilbert")
	}
}

func TestHilbertRegistered(t *testing.T) {
	got, err := ByName("hilbert")
	if err != nil {
		t.Fatalf("ByName(hilbert): %v", err)
	}
	if got != HilbertCurve {
		t.Errorf("ByName returned %v, want HilbertCurve", got)
	}
}

func TestHilbertBijective(t *testing.T) {
	for _, dims := range [][2]uint32{{4, 4}, {8, 8}, {7, 11}, {1, 1}, {3, 5}, {16, 16}, {15, 17}} {
		W, H := dims[0], dims[1]
		total := W * H
		seen := make(map[uint32]bool, total)
		for y := uint32(0); y < H; y++ {
			for x := uint32(0); x < W; x++ {
				idx := HilbertCurve.Index(x, y, W, H)
				if idx >= total {
					t.Errorf("W=%d H=%d (x=%d,y=%d): idx %d out of range [0,%d)", W, H, x, y, idx, total)
				}
				if seen[idx] {
					t.Errorf("W=%d H=%d: duplicate idx %d", W, H, idx)
				}
				seen[idx] = true
				gx, gy := HilbertCurve.IndexToXY(idx, W, H)
				if gx != x || gy != y {
					t.Errorf("round-trip W=%d H=%d (x=%d,y=%d)→idx=%d→(%d,%d)", W, H, x, y, idx, gx, gy)
				}
			}
		}
		if uint32(len(seen)) != total {
			t.Errorf("W=%d H=%d: covered %d indices, want %d", W, H, len(seen), total)
		}
	}
}

func TestHilbertContiguousLocality(t *testing.T) {
	// Sanity: on a 4x4 grid, consecutive Hilbert indices are
	// adjacent tiles (manhattan distance 1).
	W, H := uint32(4), uint32(4)
	prevX, prevY := HilbertCurve.IndexToXY(0, W, H)
	for i := uint32(1); i < W*H; i++ {
		x, y := HilbertCurve.IndexToXY(i, W, H)
		dx, dy := int32(x)-int32(prevX), int32(y)-int32(prevY)
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx+dy != 1 {
			t.Errorf("Hilbert step %d→%d: (%d,%d)→(%d,%d) manhattan=%d, want 1",
				i-1, i, prevX, prevY, x, y, dx+dy)
		}
		prevX, prevY = x, y
	}
}
