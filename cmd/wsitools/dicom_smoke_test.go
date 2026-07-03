package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
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
		// The extracted region must be a valid 256×256 PNG, not just a file.
		f, err := os.Open(out)
		if err != nil {
			t.Fatalf("open region png: %v", err)
		}
		defer f.Close()
		cfg, err := png.DecodeConfig(f)
		if err != nil {
			t.Fatalf("region output is not a valid PNG: %v", err)
		}
		if cfg.Width != 256 || cfg.Height != 256 {
			t.Errorf("region PNG = %dx%d, want 256x256", cfg.Width, cfg.Height)
		}
	})
	t.Run("convert-cogwsi", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "o.cog.tiff")
		if o, err := runBin(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, dir); err != nil {
			t.Fatalf("convert: %v\n%s", err, o)
		}
		// Output must re-open as a pyramid with at least one level.
		s, err := opentile.OpenFile(out)
		if err != nil {
			t.Fatalf("open cog-wsi output: %v", err)
		}
		defer s.Close()
		if n := len(s.Levels()); n == 0 {
			t.Errorf("cog-wsi output has no pyramid levels")
		}
	})
	t.Run("hash-pixel", func(t *testing.T) {
		o, err := runBin(bin, "hash", "--mode", "pixel", dir)
		if err != nil {
			t.Fatalf("hash pixel: %v\n%s", err, o)
		}
		if !strings.Contains(string(o), "sha256-pixel:") {
			t.Errorf("hash --mode pixel output missing sha256-pixel digest:\n%s", o)
		}
	})
}
