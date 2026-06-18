package main

import (
	"archive/zip"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConvertToDZIEmitsAssociatedPNGs: convert --to dzi writes each associated
// image as a lossless PNG under <base>_associated/ (DZI has no standard slot, so
// wsitools emits siblings of the tile tree rather than dropping them).
func TestConvertToDZIEmitsAssociatedPNGs(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "slide.dzi")
	if o, err := runBin(bin, "convert", "--to", "dzi", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to dzi: %v\n%s", err, o)
	}
	base := strings.TrimSuffix(out, ".dzi")
	for _, ty := range []string{"thumbnail", "label", "overview"} {
		p := base + "_associated/" + ty + ".png"
		f, err := os.Open(p)
		if err != nil {
			t.Errorf("missing associated PNG %s: %v", p, err)
			continue
		}
		if _, err := png.Decode(f); err != nil {
			t.Errorf("associated %s is not a valid PNG: %v", ty, err)
		}
		f.Close()
	}
}

// TestConvertToDZINoAssociated: --no-associated skips the associated directory.
func TestConvertToDZINoAssociated(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "slide.dzi")
	if o, err := runBin(bin, "convert", "--to", "dzi", "--no-associated", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	if _, err := os.Stat(strings.TrimSuffix(out, ".dzi") + "_associated"); err == nil {
		t.Errorf("associated dir written despite --no-associated")
	}
}

// TestConvertToSZIEmitsAssociatedPNGs: convert --to szi stores each associated
// image as a PNG zip entry under <name>/<name>_associated/.
func TestConvertToSZIEmitsAssociatedPNGs(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "slide.szi")
	if o, err := runBin(bin, "convert", "--to", "szi", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to szi: %v\n%s", err, o)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open szi zip: %v", err)
	}
	defer zr.Close()
	have := map[string]*zip.File{}
	for _, f := range zr.File {
		have[f.Name] = f
	}
	for _, ty := range []string{"thumbnail", "label", "overview"} {
		name := "slide/slide_associated/" + ty + ".png"
		f, ok := have[name]
		if !ok {
			var names []string
			for n := range have {
				names = append(names, n)
			}
			t.Errorf("missing szi associated entry %s; entries: %v", name, names)
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Errorf("open %s: %v", name, err)
			continue
		}
		if _, err := png.Decode(rc); err != nil {
			t.Errorf("associated %s is not a valid PNG: %v", ty, err)
		}
		rc.Close()
	}
}
