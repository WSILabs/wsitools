package downscale

import (
	"bytes"
	"testing"
)

func TestExtractTile_InteriorAndEdgePad(t *testing.T) {
	// 3×2 raster, RGB, distinct per-pixel values so we can spot misalignment.
	// pixel (x,y) = {x, y, 0}.
	w, h := 3, 2
	raster := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := (y*w + x) * 3
			raster[o], raster[o+1], raster[o+2] = byte(x), byte(y), 0
		}
	}
	// tileSize 2: tile (0,0) covers x∈[0,2), y∈[0,2) — fully inside.
	got := ExtractTile(raster, w, h, 0, 0, 2)
	if len(got) != 2*2*3 {
		t.Fatalf("tile len = %d, want %d", len(got), 2*2*3)
	}
	// row 0: (0,0),(1,0); row 1: (0,1),(1,1)
	want := []byte{0, 0, 0, 1, 0, 0, 0, 1, 0, 1, 1, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("interior tile = %v, want %v", got, want)
	}
	// tile (1,0) covers x∈[2,4): only x=2 valid, x=3 is edge → zero-padded.
	got = ExtractTile(raster, w, h, 1, 0, 2)
	want = []byte{2, 0, 0, 0, 0, 0, 2, 1, 0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("edge tile = %v, want %v", got, want)
	}
	// tile (0,1) covers y∈[2,4): fully out of range → all-zero tile.
	got = ExtractTile(raster, w, h, 0, 1, 2)
	if !bytes.Equal(got, make([]byte, 2*2*3)) {
		t.Errorf("fully out-of-bounds tile should be all zeros, got %v", got)
	}
}
