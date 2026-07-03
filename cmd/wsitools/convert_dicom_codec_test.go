package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
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
			// The re-encoded output must read back as a DICOM pyramid with levels.
			s, err := opentile.OpenFile(out)
			if err != nil {
				t.Fatalf("open DICOM output: %v", err)
			}
			defer s.Close()
			if got := s.Format(); got != opentile.FormatDICOM {
				t.Errorf("output format = %v, want dicom", got)
			}
			if n := len(s.Levels()); n == 0 {
				t.Errorf("DICOM output has no pyramid levels")
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

// convert --to dicom honors --codec jpeg2000 / htj2k (DICOM transfer syntaxes) by
// re-encoding the pyramid through the engine, matching what --factor already did.
// Regression: the plain path used to hardcode "only 'jpeg'" and reject them.
func TestConvertDICOMReencodesJP2KAndHTJ2K(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	for _, codec := range []string{"jpeg2000", "htj2k"} {
		t.Run(codec, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			if o, err := runBin(bin, "convert", "--to", "dicom", "--codec", codec, "-o", out, src); err != nil {
				t.Fatalf("convert --to dicom --codec %s: %v\n%s", codec, err, o)
			}
			o, err := runBin(bin, "info", out)
			if err != nil {
				t.Fatalf("info on output: %v\n%s", err, o)
			}
			if !strings.Contains(string(o), codec) {
				t.Errorf("--to dicom --codec %s did not store %s; info:\n%s", codec, codec, o)
			}
		})
	}
}

// avif / png have no DICOM transfer syntax and are still rejected (at the
// capability gate), with a clear error naming the supported codecs.
func TestConvertDICOMRejectsCodecWithoutTransferSyntax(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out")
	o, err := runBin(bin, "convert", "--to", "dicom", "--codec", "avif", "-o", out, src)
	if err == nil {
		t.Fatal("expected error for --codec avif on --to dicom")
	}
	if !strings.Contains(string(o), "not supported for --to dicom") {
		t.Errorf("error should say avif is not supported for --to dicom; got:\n%s", o)
	}
}
