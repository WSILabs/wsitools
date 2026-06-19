package main

import "testing"

func TestScaleMPPMag(t *testing.T) {
	mx, my, mag := scaleMPPMag(0.25, 0.25, 40, 1)
	if mx != 0.25 || my != 0.25 || mag != 40 {
		t.Fatalf("factor 1 must be identity, got %v,%v,%v", mx, my, mag)
	}
	mx, my, mag = scaleMPPMag(0.25, 0.25, 40, 2)
	if mx != 0.5 || my != 0.5 || mag != 20 {
		t.Fatalf("factor 2: got %v,%v,%v want 0.5,0.5,20", mx, my, mag)
	}
}

func TestCropOutDims(t *testing.T) {
	w, h := outDimsForFactor(2049, 1025, 2)
	if w != 1024 || h != 512 {
		t.Fatalf("got %dx%d want 1024x512", w, h)
	}
	w, h = outDimsForFactor(2048, 1024, 1)
	if w != 2048 || h != 1024 {
		t.Fatalf("factor 1 identity, got %dx%d", w, h)
	}
}
