package tileorder

import (
	"testing"
)

func TestMortonName(t *testing.T) {
	if Morton.Name() != "morton" {
		t.Errorf("Name(): got %q, want %q", Morton.Name(), "morton")
	}
}

func TestMortonRegistered(t *testing.T) {
	got, err := ByName("morton")
	if err != nil {
		t.Fatalf("ByName(morton): %v", err)
	}
	if got != Morton {
		t.Errorf("ByName returned %v, want Morton", got)
	}
}

func TestMortonBijective(t *testing.T) {
	// Exhaustive: every (x,y) gets a unique index in [0, W*H), and
	// IndexToXY inverts Index.
	for _, dims := range [][2]uint32{{4, 4}, {8, 8}, {7, 11}, {1, 1}, {3, 5}, {16, 16}} {
		W, H := dims[0], dims[1]
		total := W * H
		seen := make(map[uint32]bool, total)
		for y := uint32(0); y < H; y++ {
			for x := uint32(0); x < W; x++ {
				idx := Morton.Index(x, y, W, H)
				if idx >= total {
					t.Errorf("W=%d H=%d (x=%d,y=%d): idx %d out of range [0,%d)", W, H, x, y, idx, total)
				}
				if seen[idx] {
					t.Errorf("W=%d H=%d: duplicate idx %d", W, H, idx)
				}
				seen[idx] = true
				gx, gy := Morton.IndexToXY(idx, W, H)
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

func TestMortonPow2OrderedAsZ(t *testing.T) {
	// On a 4x4 pow2 grid, Morton order is the canonical Z-curve.
	// (0,0)→0, (1,0)→1, (0,1)→2, (1,1)→3, (2,0)→4, (3,0)→5, ...
	W, H := uint32(4), uint32(4)
	wantSeq := []struct{ x, y uint32 }{
		{0, 0}, {1, 0}, {0, 1}, {1, 1},
		{2, 0}, {3, 0}, {2, 1}, {3, 1},
		{0, 2}, {1, 2}, {0, 3}, {1, 3},
		{2, 2}, {3, 2}, {2, 3}, {3, 3},
	}
	for i, p := range wantSeq {
		gx, gy := Morton.IndexToXY(uint32(i), W, H)
		if gx != p.x || gy != p.y {
			t.Errorf("idx=%d: got (%d,%d), want (%d,%d)", i, gx, gy, p.x, p.y)
		}
	}
}
