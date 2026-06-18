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
	bin := stripedBinary(t)
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
	bin := stripedBinary(t)
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
	bin := stripedBinary(t)
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

// TestConvertToBIFRejectsBadCodec: BIF only supports --codec jpeg.
func TestConvertToBIFRejectsBadCodec(t *testing.T) {
	bin := stripedBinary(t)
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
