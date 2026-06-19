package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

func TestStreamwriterSinkRoutesAndDrains(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.tif")
	// streamwriter.Create(path, opts) owns the file (writes to <path>.tmp and
	// renames to <path> on Close); there is no io.Writer-based New constructor
	// in this package (mirrors runConvertTIFF in convert_tiff.go).
	w, err := streamwriter.Create(out, streamwriter.Options{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	lh, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 512, ImageHeight: 512, TileWidth: 256, TileHeight: 256,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
	})
	if err != nil {
		t.Fatalf("addlevel: %v", err)
	}
	sink := newStreamwriterSink([]*streamwriter.LevelHandle{lh})

	tiles := [][2]int{{1, 1}, {0, 0}, {1, 0}, {0, 1}}
	for _, cr := range tiles {
		body := []byte{byte(cr[0]), byte(cr[1]), 0xAA}
		if err := sink.WriteTile(0, cr[0], cr[1], body); err != nil {
			t.Fatalf("WriteTile (%d,%d): %v", cr[0], cr[1], err)
		}
	}
	if err := sink.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("output not finalized: stat=%v", err)
	}
}
