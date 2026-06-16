package main

import (
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// C2 (Option B): a wsitools-produced generic-TIFF has a streamwriter byte layout
// the in-place splice engine rejects ("past cutoff" — the L0 directory sits past
// the associated data). associated remove/replace must fall back to a faithful,
// pixel-identical rebuild (tile-copied pyramid) for generic-TIFF.

// wsitoolsGenericTIFF converts a multi-level fixture to a wsitools generic-TIFF
// (non-tail-spliceable) that carries associated images, or skips.
func wsitoolsGenericTIFF(t *testing.T, bin string) string {
	t.Helper()
	src := filepath.Join(testDir(t), "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	tiff := filepath.Join(t.TempDir(), "in.tiff")
	if o, err := exec.Command(bin, "convert", "--to", "tiff", "-o", tiff, src).CombinedOutput(); err != nil {
		t.Fatalf("convert --to tiff: %v\n%s", err, o)
	}
	info, _ := exec.Command(bin, "info", tiff).CombinedOutput()
	if !strings.Contains(string(info), "label") {
		t.Skip("converted generic-TIFF has no label to edit")
	}
	return tiff
}

func TestAssociatedRemoveGenericTIFFRebuildFallback(t *testing.T) {
	bin := findBinary(t)
	tiff := wsitoolsGenericTIFF(t, bin)
	out := filepath.Join(t.TempDir(), "out.tiff")

	if o, err := exec.Command(bin, "label", "remove", tiff, "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("label remove on wsitools generic-TIFF (fallback): %v\n%s", err, o)
	}
	info, _ := exec.Command(bin, "info", out).CombinedOutput()
	if strings.Contains(string(info), "label ") {
		t.Errorf("label still present after remove:\n%s", info)
	}
	if !strings.Contains(string(info), "thumbnail") {
		t.Errorf("thumbnail should survive label remove:\n%s", info)
	}
	// Rebuild tile-copies the pyramid verbatim → pixel-identical.
	if pixelHash(t, tiff) != pixelHash(t, out) {
		t.Errorf("pyramid pixel hash changed across label remove (rebuild should be verbatim tile-copy)")
	}
}

func TestAssociatedReplaceGenericTIFFRebuildFallback(t *testing.T) {
	bin := findBinary(t)
	tiff := wsitoolsGenericTIFF(t, bin)

	// A replacement label image on disk.
	repPath := filepath.Join(t.TempDir(), "newlabel.png")
	f, err := os.Create(repPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, solidImage(400, 400, color.RGBA{R: 200, G: 30, B: 30, A: 255})); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	out := filepath.Join(t.TempDir(), "out.tiff")
	if o, err := exec.Command(bin, "label", "replace", tiff, "--image", repPath, "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("label replace on wsitools generic-TIFF (fallback): %v\n%s", err, o)
	}
	info, _ := exec.Command(bin, "info", out).CombinedOutput()
	if !strings.Contains(string(info), "label") {
		t.Errorf("label should be present after replace:\n%s", info)
	}
	// Pyramid still pixel-identical (only the associated image changed).
	if pixelHash(t, tiff) != pixelHash(t, out) {
		t.Errorf("pyramid pixel hash changed across label replace")
	}
}
