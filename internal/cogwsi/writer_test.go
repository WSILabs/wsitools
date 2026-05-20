package cogwsi_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cornish/wsitools/internal/cogwsi"
)

func TestPackageCompiles(t *testing.T) {
	var _ *cogwsi.Writer
}

func TestWriterCreateAndSpoolLevel(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")

	w, err := cogwsi.Create(out, cogwsi.Options{
		ToolsVersion: "test",
		BigTIFF:      cogwsi.BigTIFFAuto,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer w.Abort()

	h, err := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth:      8,
		ImageHeight:     8,
		TileWidth:       8,
		TileHeight:      8,
		Compression:     1, // none
		Photometric:     2, // RGB
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		IsL0:            true,
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}
	if err := h.WriteTile(0, 0, []byte("xxxxxxxx")); err != nil {
		t.Fatalf("WriteTile: %v", err)
	}

	// Spool file should exist.
	if _, err := os.Stat(out + ".spool/L0"); err != nil {
		t.Errorf("expected spool file: %v", err)
	}

	// Out-of-order write is an error.
	h2, err := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 16, ImageHeight: 8,
		TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Skip (0,0), jump to (1,0): error.
	if err := h2.WriteTile(1, 0, []byte("xxxxxxxx")); err == nil {
		t.Errorf("expected error for out-of-order tile (1,0) before (0,0)")
	}
}

func TestWriterAbortRemovesEverything(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := cogwsi.Create(out, cogwsi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Abort()
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output file should be gone")
	}
	if _, err := os.Stat(out + ".spool"); !os.IsNotExist(err) {
		t.Errorf("spool dir should be gone")
	}
}
