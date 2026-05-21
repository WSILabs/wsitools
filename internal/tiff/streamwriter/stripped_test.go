package streamwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cornish/wsitools/internal/tiff"
	"github.com/cornish/wsitools/internal/tiff/streamwriter"
)

// TestAddStripped writes an L0 pyramid IFD plus a stripped associated "label"
// IFD and verifies the resulting file has ≥2 IFDs via tiffinfo.
func TestAddStripped(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo not in PATH")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "with-assoc.tiff")
	w, _ := streamwriter.Create(path, streamwriter.Options{BigTIFF: tiff.BigTIFFOn})
	// Main pyramid level (one tile so we have a real L0).
	level, _ := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: tiff.CompressionNone, Photometric: 2,
	})
	level.WriteTile(0, 0, make([]byte, 8*8*3))

	// Synthetic "label" image: 4x4 RGB strip, all 0x55 bytes.
	labelStrip := make([]byte, 4*4*3)
	for i := range labelStrip {
		labelStrip[i] = 0x55
	}
	if err := w.AddStripped(streamwriter.StrippedSpec{
		WSIImageType:   tiff.WSIImageTypeLabel,
		StripBytes:     labelStrip,
		Width:          4,
		Height:         4,
		Compression:    tiff.CompressionNone,
		Photometric:    2,
		NewSubfileType: 1,
	}); err != nil {
		t.Fatalf("AddStripped: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	out, _ := exec.Command("tiffinfo", path).CombinedOutput()
	got := string(out)
	if strings.Count(got, "TIFF Directory") < 2 {
		t.Errorf("expected >=2 IFDs, got:\n%s", got)
	}
}
