package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SVS downsample via convert must match the standalone downsample (pixel-equal),
// and scale metadata (factor-4 of a 40x slide → ~10x, MPP ×4).
func TestConvertFactorSVSParity(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	a := filepath.Join(t.TempDir(), "a.svs")
	b := filepath.Join(t.TempDir(), "b.svs")
	if o, err := runBin(bin, "downsample", "--factor", "4", "--quiet", "-f", "-o", a, src); err != nil {
		t.Fatalf("downsample: %v\n%s", err, o)
	}
	if o, err := runBin(bin, "convert", "--to", "svs", "--factor", "4", "-f", "-o", b, src); err != nil {
		t.Fatalf("convert --factor: %v\n%s", err, o)
	}
	ha, _ := runBin(bin, "hash", "--mode", "pixel", a)
	hb, _ := runBin(bin, "hash", "--mode", "pixel", b)
	if pixelDigest(ha) == "" || pixelDigest(ha) != pixelDigest(hb) {
		t.Errorf("pixel hash mismatch:\n a=%s\n b=%s", ha, hb)
	}
	// CMU-1-Small-Region.svs is 20x; factor-4 → 5x.
	// (A 40x fixture would yield 10x; adjust this line if the fixture changes.)
	info, _ := runBin(bin, "info", b)
	if !strings.Contains(string(info), "Magnification: 5x") {
		t.Errorf("expected Magnification 5x (source 20x / factor 4) in:\n%s", info)
	}
}

// --factor with dzi/szi is rejected.
func TestConvertFactorRejectsDZI(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.dzi")
	o, err := runBin(bin, "convert", "--to", "dzi", "--factor", "2", "-f", "-o", out, src)
	if err == nil || !strings.Contains(string(o), "factor") {
		t.Fatalf("expected --factor/dzi rejection, got err=%v\n%s", err, o)
	}
}

// invalid factor rejected (full message wired in a later task; here at least it must not silently pass).
func TestConvertFactorRejectsBadValue(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.svs")
	o, err := runBin(bin, "convert", "--to", "svs", "--factor", "3", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected invalid-factor rejection, got success:\n%s", o)
	}
}
