package main

import (
	"bytes"
	stdpng "image/png"
	"testing"
)

// TestDZIStandalonePNGEncoder verifies the DZI PNG encoder (backed by the
// registered codec) emits a decodable lossless PNG of the right dims.
func TestDZIStandalonePNGEncoder(t *testing.T) {
	const w, h = 32, 24
	rgb := make([]byte, w*h*3)
	for i := range rgb {
		rgb[i] = byte(i * 3)
	}
	enc, err := newDZIStandaloneEncoder("png", w, 0)
	if err != nil {
		t.Fatalf("newDZIStandaloneEncoder: %v", err)
	}
	defer enc.Close()
	tile, err := enc.EncodeTile(rgb, w, h)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	img, err := stdpng.Decode(bytes.NewReader(tile))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("dims: got %v, want %dx%d", img.Bounds(), w, h)
	}
	r, g, b, _ := img.At(1, 0).RGBA()
	i := 1 * 3
	if byte(r>>8) != rgb[i] || byte(g>>8) != rgb[i+1] || byte(b>>8) != rgb[i+2] {
		t.Fatalf("pixel (1,0) mismatch: got %d,%d,%d want %d,%d,%d",
			byte(r>>8), byte(g>>8), byte(b>>8), rgb[i], rgb[i+1], rgb[i+2])
	}
}
