package main

import "testing"

// TestPadRGBTileReplicate verifies a partial edge tile is padded up to the full
// tile size by replicating the last valid row/column (TIFF requires uniform
// full-size tiles; the retile engine hands partial edge tiles at content size).
func TestPadRGBTileReplicate(t *testing.T) {
	// 2x2 content, distinct pixels, padded to 4x4.
	//   (0,0)=R (1,0)=G
	//   (0,1)=B (1,1)=W
	src := []byte{
		255, 0, 0, 0, 255, 0, // row 0: R G
		0, 0, 255, 255, 255, 255, // row 1: B W
	}
	dst := padRGBTileReplicate(src, 2, 2, 4, 4)
	if len(dst) != 4*4*3 {
		t.Fatalf("dst len = %d, want %d", len(dst), 4*4*3)
	}
	px := func(x, y int) [3]byte {
		o := (y*4 + x) * 3
		return [3]byte{dst[o], dst[o+1], dst[o+2]}
	}
	// Content preserved.
	if px(0, 0) != [3]byte{255, 0, 0} || px(1, 0) != [3]byte{0, 255, 0} {
		t.Errorf("content row 0 wrong: %v %v", px(0, 0), px(1, 0))
	}
	// Last column (x=1) replicated rightward into x=2,3 on row 0 (G).
	if px(2, 0) != [3]byte{0, 255, 0} || px(3, 0) != [3]byte{0, 255, 0} {
		t.Errorf("right-pad row 0 = %v %v, want G,G", px(2, 0), px(3, 0))
	}
	// Last row (y=1) replicated downward into y=2,3; column 0 stays B.
	if px(0, 2) != [3]byte{0, 0, 255} || px(0, 3) != [3]byte{0, 0, 255} {
		t.Errorf("bottom-pad col 0 = %v %v, want B,B", px(0, 2), px(0, 3))
	}
	// Bottom-right corner replicates the (1,1)=W pixel.
	if px(3, 3) != [3]byte{255, 255, 255} {
		t.Errorf("corner pad = %v, want W", px(3, 3))
	}
}

// TestPadRGBTileReplicateNoOpWhenFull confirms a full-size tile is unchanged
// shape-wise (the encoder skips padding for full tiles, but the helper must be
// correct if called).
func TestPadRGBTileReplicateNoOpWhenFull(t *testing.T) {
	src := make([]byte, 2*2*3)
	for i := range src {
		src[i] = byte(i)
	}
	dst := padRGBTileReplicate(src, 2, 2, 2, 2)
	for i := range src {
		if dst[i] != src[i] {
			t.Fatalf("full-size pad altered byte %d: got %d want %d", i, dst[i], src[i])
		}
	}
}
