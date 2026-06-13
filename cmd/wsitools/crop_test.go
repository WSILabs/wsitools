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
