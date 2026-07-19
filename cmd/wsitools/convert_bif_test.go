package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConvertToBIF: a JPEG-tiled SVS converts to a BIF that re-detects as bif
// with VENTANA DP 200 identity and the source's level count.
func TestConvertToBIF(t *testing.T) {
	bin := strippedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.bif")
	if o, err := runBin(bin, "convert", "--to", "bif", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to bif: %v\n%s", err, o)
	}
	info, _ := runBin(bin, "info", out)
	for _, want := range []string{"Format:  bif", "VENTANA DP 200"} {
		if !strings.Contains(string(info), want) {
			t.Errorf("info missing %q:\n%s", want, info)
		}
	}
}

// TestConvertToBIFRejectsNonJPEG: BIF is a JPEG container; a non-JPEG (LZW)
// source without --codec must be rejected with a message pointing at the JPEG
// requirement, rather than silently producing a broken file.
func TestConvertToBIFRejectsNonJPEG(t *testing.T) {
	bin := strippedBinary(t)
	src := filepath.Join(testDir(t), "svs", "590_lzw_imagescope.tif")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.bif")
	o, err := runBin(bin, "convert", "--to", "bif", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected rejection of non-JPEG source, got success:\n%s", o)
	}
	if !strings.Contains(string(o), "JPEG") {
		t.Errorf("error should mention the JPEG requirement, got:\n%s", o)
	}
}

// TestConvertToBIFReencode: --codec jpeg re-encodes a non-JPEG (LZW) source to
// a valid BIF that re-detects as bif.
func TestConvertToBIFReencode(t *testing.T) {
	bin := strippedBinary(t)
	src := filepath.Join(testDir(t), "svs", "590_lzw_imagescope.tif")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.bif")
	if o, err := runBin(bin, "convert", "--to", "bif", "--codec", "jpeg", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to bif --codec jpeg: %v\n%s", err, o)
	}
	info, _ := runBin(bin, "info", out)
	if !strings.Contains(string(info), "Format:  bif") {
		t.Errorf("re-encoded output not detected as bif:\n%s", info)
	}
}

// TestConvertToBIFOverviewDims: the overview ("Label_Image") is emitted at the
// DP 200 canonical 1251×3685 portrait, and the reader crops the label as its
// top 1/3 (1251×1228) — for both carry-through and synthesized overviews.
func TestConvertToBIFOverviewDims(t *testing.T) {
	bin := strippedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.bif")
	if o, err := runBin(bin, "convert", "--to", "bif", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to bif: %v\n%s", err, o)
	}
	info := string(mustInfo(t, bin, out))
	if !strings.Contains(info, "overview") || !strings.Contains(info, "1251 × 3685") {
		t.Errorf("expected overview 1251 × 3685 (DP 200 canonical):\n%s", info)
	}
	if !strings.Contains(info, "label") || !strings.Contains(info, "1251 × 1228") {
		t.Errorf("expected synthesized label 1251 × 1228 (top 1/3 of overview):\n%s", info)
	}
}

func mustInfo(t *testing.T, bin, path string) []byte {
	t.Helper()
	out, _ := runBin(bin, "info", path)
	return out
}

// TestConvertToBIFOpentileRoundTrip: a verbatim SVS→BIF conversion reads back
// through opentile (wsitools' own reader) with an identical pixel hash — i.e.
// opentile places the row-major BIF tiles correctly (opentile-go #57, v0.45.3).
func TestConvertToBIFOpentileRoundTrip(t *testing.T) {
	bin := strippedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "rt.bif")
	if o, err := runBin(bin, "convert", "--to", "bif", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to bif: %v\n%s", err, o)
	}
	digest := func(p string) string {
		o, _ := runBin(bin, "hash", "--mode", "pixel", p)
		return pixelDigest(o)
	}
	ds, db := digest(src), digest(out)
	if ds == "" || ds != db {
		t.Errorf("opentile read of BIF != source pixels:\n src=%s\n bif=%s", ds, db)
	}
}

// TestConvertToBIFRejectsBadCodec: BIF only supports --codec jpeg.
func TestConvertToBIFRejectsBadCodec(t *testing.T) {
	bin := strippedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.bif")
	o, err := runBin(bin, "convert", "--to", "bif", "--codec", "jpeg2000", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected rejection of --codec jpeg2000, got success:\n%s", o)
	}
	if !strings.Contains(string(o), "JPEG container") {
		t.Errorf("error should explain BIF is a JPEG container, got:\n%s", o)
	}
}
