package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A4b: convert --to dicom honors the --codec jpeg opt-in to re-encode a source
// whose tiles are not a DICOM transfer syntax (LZW / uncompressed / …), and
// errors (no silent codec assumption) when --codec is omitted — matching the
// TIFF family's --codec semantics.

func TestConvertDICOMReencodesExoticCodecWithJPEG(t *testing.T) {
	bin := stripedBinary(t)
	for _, fx := range []string{"590_crop_lzw_imagescope.tif", "590_crop_none_imagescope.tif"} {
		t.Run(fx, func(t *testing.T) {
			src := filepath.Join(testDir(t), "svs", fx)
			if _, err := os.Stat(src); err != nil {
				t.Skipf("fixture absent: %v", err)
			}
			out := filepath.Join(t.TempDir(), "out")
			if o, err := runBin(bin, "convert", "--to", "dicom", "--codec", "jpeg", "-o", out, src); err != nil {
				t.Fatalf("convert --to dicom --codec jpeg on %s: %v\n%s", fx, err, o)
			}
			// The re-encoded output must read back as a DICOM pyramid.
			if o, err := runBin(bin, "info", out); err != nil {
				t.Fatalf("info on output: %v\n%s", err, o)
			}
		})
	}
}

func TestConvertDICOMRejectsExoticCodecWithoutFlag(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "590_crop_lzw_imagescope.tif")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out")
	o, err := runBin(bin, "convert", "--to", "dicom", "-o", out, src)
	if err == nil {
		t.Fatal("expected error converting an LZW source to DICOM without --codec")
	}
	if !strings.Contains(string(o), "--codec jpeg") {
		t.Errorf("error should suggest --codec jpeg; got:\n%s", o)
	}
}

func TestConvertDICOMRejectsNonJPEGCodec(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out")
	o, err := runBin(bin, "convert", "--to", "dicom", "--codec", "jpeg2000", "-o", out, src)
	if err == nil {
		t.Fatal("expected error for --codec jpeg2000 on --to dicom")
	}
	if !strings.Contains(string(o), "only 'jpeg'") {
		t.Errorf("error should say only 'jpeg' is supported; got:\n%s", o)
	}
}
