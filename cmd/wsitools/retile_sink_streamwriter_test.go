package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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

// TestStreamwriterSinkFinishJoinsOnPartialDelivery proves finish() joins the
// per-level drain goroutines even when retile.Run errored mid-stream (only some
// tiles delivered). CloseInput makes NextReady drain what's buffered then return
// ok=false, so finish() must return — not hang — and leak no goroutines.
func TestStreamwriterSinkFinishJoinsOnPartialDelivery(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.tif")
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
	before := runtime.NumGoroutine()
	sink := newStreamwriterSink([]*streamwriter.LevelHandle{lh})
	// Deliver only 1 of the 4 tiles (simulating a retile.Run error mid-stream).
	if err := sink.WriteTile(0, 0, 0, []byte{0, 0, 0xAA}); err != nil {
		t.Fatalf("WriteTile: %v", err)
	}
	// PRIMARY assertion: finish() must RETURN (CloseInput + join) without hanging.
	done := make(chan struct{})
	go func() {
		_ = sink.finish()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("finish() hung — drain goroutine leak on partial delivery")
	}
	_ = w.Close()
	// SECONDARY assertion: the joined goroutine is reaped (no leak).
	for i := 0; i < 50 && runtime.NumGoroutine() > before; i++ {
		runtime.Gosched()
	}
	if leaked := runtime.NumGoroutine() - before; leaked > 0 {
		t.Errorf("leaked %d goroutine(s) after finish() on partial delivery", leaked)
	}
}
