package main

import (
	"strings"
	"testing"
)

func TestConvertRectComboGuards(t *testing.T) {
	cases := []struct {
		name      string
		rectSet   bool
		factor    int
		targetMag int
		codec     string
		to        string
		wantSub   string
	}{
		{"rect+factor", true, 2, 0, "", "tiff", "--rect with --factor"},
		{"rect+target-mag", true, 1, 20, "", "tiff", "--target-mag"},
		{"rect+codec", true, 1, 0, "avif", "tiff", "--rect with --codec"},
		{"rect+dzi", true, 1, 0, "", "dzi", "--rect with --to dzi"},
		{"rect+szi", true, 1, 0, "", "szi", "--rect with --to szi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRectCombo(c.rectSet, c.factor, c.targetMag, c.codec, c.to)
			if err == nil || !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("err=%v, want substring %q", err, c.wantSub)
			}
		})
	}
}

func TestConvertRectComboAllowed(t *testing.T) {
	if err := validateRectCombo(true, 1, 0, "", "tiff"); err != nil {
		t.Fatalf("plain rect should be allowed, got %v", err)
	}
	if err := validateRectCombo(false, 2, 0, "avif", "dzi"); err != nil {
		t.Fatalf("no rect should be allowed regardless of other flags, got %v", err)
	}
}
