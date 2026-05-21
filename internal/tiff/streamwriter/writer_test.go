package streamwriter_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cornish/wsitools/internal/tiff"
	"github.com/cornish/wsitools/internal/tiff/streamwriter"
)

func TestCreateAndCloseEmpty(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer w.Abort()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("output missing after Close: %v", err)
	}
}

func TestAddLevelAndWriteTile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()

	h, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8,
		TileWidth: 8, TileHeight: 8,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3,
		Photometric: 2, Compression: tiff.CompressionNone,
		NewSubfileType: 0, WSIImageType: tiff.WSIImageTypePyramid,
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}
	if err := h.WriteTile(0, 0, []byte("xxxxxxxx")); err != nil {
		t.Fatalf("WriteTile: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAddStripped(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()

	h, _ := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3,
		Photometric: 2, Compression: tiff.CompressionNone,
		WSIImageType: tiff.WSIImageTypePyramid,
	})
	h.WriteTile(0, 0, []byte("xxxxxxxx"))

	if err := w.AddStripped(streamwriter.StrippedSpec{
		Width: 100, Height: 100, RowsPerStrip: 100,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3,
		Photometric: 2, Compression: tiff.CompressionNone,
		StripBytes:     make([]byte, 30000),
		NewSubfileType: 1, WSIImageType: tiff.WSIImageTypeLabel,
	}); err != nil {
		t.Fatalf("AddStripped: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWSIImageTypeValidation(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, _ := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	defer w.Abort()
	_, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3,
		Photometric: 2, Compression: tiff.CompressionNone,
		WSIImageType: "not-a-real-kind",
	})
	if err == nil {
		t.Errorf("expected validation error for bad WSIImageType")
	}
}
