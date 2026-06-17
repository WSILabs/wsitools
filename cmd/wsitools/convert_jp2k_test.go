package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// B1: jpeg2000 is now a --codec re-encode target (OpenJPEG encoder). Lossless
// (reversible=true) must round-trip pixel-identical — this also guards
// opentile-go#53 (fixed v0.45.1): the JP2K decoder must honor the codestream's
// MCT/colorspace rather than force-converting RGB as YCbCr.
func TestConvertTIFFJPEG2000LosslessRoundTrip(t *testing.T) {
	in := cmuFixture(t)
	out := filepath.Join(t.TempDir(), "out.tiff")
	cmd := exec.Command(findBinary(t), "convert", "--to", "tiff",
		"--codec", "jpeg2000", "--quality", "reversible=true", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert --codec jpeg2000 reversible=true: %v\n%s", err, b)
	}
	if pixelHash(t, in) != pixelHash(t, out) {
		t.Errorf("lossless jpeg2000 re-encode changed pixels (encoder or opentile-go#53 regression)")
	}
}

func TestConvertTIFFJPEG2000LossyDecodable(t *testing.T) {
	in := cmuFixture(t)
	out := filepath.Join(t.TempDir(), "out.tiff")
	if b, err := exec.Command(findBinary(t), "convert", "--to", "tiff",
		"--codec", "jpeg2000", "-o", out, in).CombinedOutput(); err != nil {
		t.Fatalf("convert --codec jpeg2000 (lossy): %v\n%s", err, b)
	}
	// Lossy: pixels approximate, but the output must read back as a valid slide.
	if b, err := exec.Command(findBinary(t), "info", out).CombinedOutput(); err != nil {
		t.Fatalf("info on lossy jpeg2000 output: %v\n%s", err, b)
	}
}
