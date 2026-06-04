package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDICOMReadSmoke(t *testing.T) {
	bin := stripedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	one, _ := filepath.Glob(filepath.Join(dir, "*.dcm"))
	if len(one) == 0 {
		t.Skip("no .dcm instances in fixture")
	}

	t.Run("info-dir", func(t *testing.T) {
		out, err := runBin(bin, "info", dir)
		if err != nil || !strings.Contains(string(out), "Format:  dicom") {
			t.Fatalf("info dir: %v\n%s", err, out)
		}
	})
	t.Run("info-instance", func(t *testing.T) {
		if out, err := runBin(bin, "info", one[0]); err != nil {
			t.Fatalf("info instance: %v\n%s", err, out)
		}
	})
	t.Run("region", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "r.png")
		if o, err := runBin(bin, "region", "--x", "8000", "--y", "8000", "--w", "256", "--h", "256", "--level", "0", "-o", out, dir); err != nil {
			t.Fatalf("region: %v\n%s", err, o)
		}
	})
	t.Run("convert-cogwsi", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "o.cog.tiff")
		if o, err := runBin(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, dir); err != nil {
			t.Fatalf("convert: %v\n%s", err, o)
		}
	})
	t.Run("hash-pixel", func(t *testing.T) {
		if o, err := runBin(bin, "hash", "--mode", "pixel", dir); err != nil {
			t.Fatalf("hash pixel: %v\n%s", err, o)
		}
	})
}
