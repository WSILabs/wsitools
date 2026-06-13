package main

import "testing"

func TestCropPyramidLevels(t *testing.T) {
	if n := cropPyramidLevels(27836, 25633, 256); n != 7 {
		t.Errorf("cropPyramidLevels(27836,25633) = %d, want 7", n)
	}
	if n := cropPyramidLevels(200, 150, 256); n != 1 {
		t.Errorf("cropPyramidLevels(200,150) = %d, want 1", n)
	}
	if n := cropPyramidLevels(512, 512, 256); n != 2 {
		t.Errorf("cropPyramidLevels(512,512) = %d, want 2", n)
	}
}

func TestValidateCropBounds(t *testing.T) {
	if err := validateCropBounds(0, 0, 100, 100, 100, 100); err != nil {
		t.Errorf("flush-fit crop should be valid: %v", err)
	}
	if err := validateCropBounds(-1, 0, 10, 10, 100, 100); err == nil {
		t.Error("negative X must error")
	}
	if err := validateCropBounds(50, 0, 60, 10, 100, 100); err == nil {
		t.Error("X+W past L0 width must error")
	}
	if err := validateCropBounds(0, 50, 10, 60, 100, 100); err == nil {
		t.Error("Y+H past L0 height must error")
	}
}

func TestSnapRectToTiles(t *testing.T) {
	// Unaligned rect, base larger than the bbox: snaps to the enclosing tile box.
	snapX, snapY, snapW, snapH, stx0, sty0, ntx, nty := snapRectToTiles(100, 80, 300, 300, 256, 256, 4096, 4096)
	if snapX != 0 || snapY != 0 || snapW != 512 || snapH != 512 {
		t.Errorf("unaligned snap = %d,%d %dx%d, want 0,0 512x512", snapX, snapY, snapW, snapH)
	}
	if stx0 != 0 || sty0 != 0 || ntx != 2 || nty != 2 {
		t.Errorf("unaligned tiles = stx0=%d sty0=%d ntx=%d nty=%d, want 0,0,2,2", stx0, sty0, ntx, nty)
	}

	// Already tile-aligned: snapped == requested.
	snapX, snapY, snapW, snapH, stx0, sty0, ntx, nty = snapRectToTiles(256, 512, 512, 256, 256, 256, 4096, 4096)
	if snapX != 256 || snapY != 512 || snapW != 512 || snapH != 256 {
		t.Errorf("aligned snap = %d,%d %dx%d, want 256,512 512x256", snapX, snapY, snapW, snapH)
	}
	if stx0 != 1 || sty0 != 2 || ntx != 2 || nty != 1 {
		t.Errorf("aligned tiles = stx0=%d sty0=%d ntx=%d nty=%d, want 1,2,2,1", stx0, sty0, ntx, nty)
	}

	// Edge clamp: far edge would exceed the image → clamped; last tile partial.
	snapX, snapY, snapW, snapH, _, _, ntx, nty = snapRectToTiles(400, 400, 150, 150, 256, 256, 600, 600)
	if snapX != 256 || snapY != 256 || snapW != 344 || snapH != 344 {
		t.Errorf("edge snap = %d,%d %dx%d, want 256,256 344x344", snapX, snapY, snapW, snapH)
	}
	if ntx != 2 || nty != 2 {
		t.Errorf("edge tiles = %dx%d, want 2x2 (last partial)", ntx, nty)
	}
}

func TestHalveRaster(t *testing.T) {
	w, h := 4, 4
	raster := make([]byte, w*h*3)
	for i := range raster {
		raster[i] = byte(i)
	}
	out, ow, oh, err := halveRaster(raster, w, h)
	if err != nil {
		t.Fatalf("halveRaster: %v", err)
	}
	if ow != 2 || oh != 2 {
		t.Errorf("dims = %dx%d, want 2x2", ow, oh)
	}
	if len(out) != ow*oh*3 {
		t.Errorf("len = %d, want %d", len(out), ow*oh*3)
	}
	odd := make([]byte, 5*5*3)
	_, ow, oh, err = halveRaster(odd, 5, 5)
	if err != nil {
		t.Fatalf("halveRaster odd: %v", err)
	}
	if ow != 2 || oh != 2 {
		t.Errorf("odd dims = %dx%d, want 2x2 (5&^1=4, /2=2)", ow, oh)
	}
}
