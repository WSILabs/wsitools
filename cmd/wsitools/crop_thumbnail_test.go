package main

import "testing"

func TestRenderCropThumbnail(t *testing.T) {
	l0 := make([]byte, 2000*1000*3)
	for i := range l0 {
		l0[i] = byte(i)
	}
	jpegBytes, tw, th, err := renderCropThumbnail(l0, 2000, 1000, 80)
	if err != nil {
		t.Fatalf("renderCropThumbnail: %v", err)
	}
	if tw != 1024 || th != 512 {
		t.Errorf("dims = %dx%d, want 1024x512", tw, th)
	}
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xFF || jpegBytes[1] != 0xD8 {
		t.Errorf("not a JPEG (no SOI marker), %d bytes", len(jpegBytes))
	}
}

func TestThumbDims(t *testing.T) {
	// Landscape: longest side → 1024.
	w, h := thumbDims(27836, 25633, 1024)
	if w != 1024 {
		t.Errorf("landscape W = %d, want 1024", w)
	}
	if h != 943 { // round(1024 * 25633/27836) = 943
		t.Errorf("landscape H = %d, want 943", h)
	}
	// Portrait: longest side is H.
	w, h = thumbDims(10000, 20000, 1024)
	if h != 1024 || w != 512 {
		t.Errorf("portrait = %dx%d, want 512x1024", w, h)
	}
	// Tiny image: never upscale.
	w, h = thumbDims(300, 200, 1024)
	if w != 300 || h != 200 {
		t.Errorf("tiny = %dx%d, want 300x200 (no upscale)", w, h)
	}
}
