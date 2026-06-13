package main

import "testing"

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
