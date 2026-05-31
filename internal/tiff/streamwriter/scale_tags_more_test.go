package streamwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// helper: write a 1-tile L0 with the given Options and return tiffinfo output.
func writeAndTiffinfo(t *testing.T, opts streamwriter.Options) string {
	t.Helper()
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	path := filepath.Join(t.TempDir(), "o.tiff")
	opts.BigTIFF = tiff.BigTIFFOn
	w, err := streamwriter.Create(path, opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	l, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: tiff.CompressionNone, Photometric: 2,
		WSIImageType: tiff.WSIImageTypePyramid,
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}
	l.WriteTile(0, 0, make([]byte, 8*8*3))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	out, _ := exec.Command("tiffinfo", path).CombinedOutput()
	return string(out)
}

// Zero MPP/mag → none of the scale tags are emitted.
func TestScaleTagsAbsentWhenZero(t *testing.T) {
	got := strings.ToLower(writeAndTiffinfo(t, streamwriter.Options{}))
	for _, tag := range []string{"65085", "65086", "65087", "resolution"} {
		if strings.Contains(got, tag) {
			t.Errorf("unexpected scale tag %q present with zero MPP:\n%s", tag, got)
		}
	}
}

// Asymmetric MPP → XResolution and YResolution differ.
// 0.40 µm/px → 25000 px/cm; 0.50 µm/px → 20000 px/cm.
// tiffinfo prints: Resolution: 25000, 20000 pixels/cm
func TestScaleTagsAsymmetric(t *testing.T) {
	got := writeAndTiffinfo(t, streamwriter.Options{MPPX: 0.40, MPPY: 0.50})
	if !strings.Contains(got, "25000") || !strings.Contains(got, "20000") {
		t.Errorf("expected distinct XRes(25000)/YRes(20000) px/cm for asymmetric MPP:\n%s", got)
	}
	// Also confirm the two values are distinct (asymmetric check).
	if !strings.Contains(got, "25000") {
		t.Errorf("missing XResolution 25000 px/cm in tiffinfo output:\n%s", got)
	}
	if !strings.Contains(got, "20000") {
		t.Errorf("missing YResolution 20000 px/cm in tiffinfo output:\n%s", got)
	}
}
