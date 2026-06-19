package main

import (
	"strings"
	"testing"
)

func TestConvertRectComboGuards(t *testing.T) {
	cases := []struct {
		name, codec, to, wantSub string
		rectSet                  bool
		factor, targetMag        int
	}{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRectCombo(c.rectSet, c.factor, c.targetMag, c.codec, c.to)
			if err == nil || !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("err=%v want substring %q", err, c.wantSub)
			}
		})
	}
}

func TestConvertRectComboAllowed(t *testing.T) {
	if err := validateRectCombo(true, 2, 0, "", "tiff"); err != nil {
		t.Fatalf("rect+factor+tiff should be allowed, got %v", err)
	}
	if err := validateRectCombo(true, 1, 0, "", "dicom"); err != nil {
		t.Fatalf("plain rect+dicom should be allowed, got %v", err)
	}
	if err := validateRectCombo(false, 2, 0, "avif", "dzi"); err != nil {
		t.Fatalf("no rect = always allowed, got %v", err)
	}
	if err := validateRectCombo(true, 1, 0, "", "svs"); err != nil {
		t.Fatalf("plain crop (factor 1) to svs must be allowed, got %v", err)
	}
	if err := validateRectCombo(true, 2, 0, "", "svs"); err != nil {
		t.Fatalf("rect+factor+svs is now allowed, got %v", err)
	}
	if err := validateRectCombo(true, 2, 0, "", "dzi"); err != nil {
		t.Fatalf("rect+factor+dzi is now allowed, got %v", err)
	}
	if err := validateRectCombo(true, 1, 0, "avif", "tiff"); err != nil {
		t.Fatalf("rect+codec is now allowed, got %v", err)
	}
	if err := validateRectCombo(true, 2, 0, "jpeg2000", "tiff"); err != nil {
		t.Fatalf("rect+factor+codec is now allowed, got %v", err)
	}
}
