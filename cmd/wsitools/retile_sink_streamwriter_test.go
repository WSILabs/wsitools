package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// TestStreamwriterSinkConcurrentDrainsNoRace guards the data race fixed by the
// Writer.appendMu lock. newStreamwriterSink drains ONE goroutine PER LEVEL, and
// each writes tile bodies through the shared Writer.appendBytes (off + f). With
// >=2 levels those drains append concurrently; before the fix they raced on
// Writer.off/f, splicing tile bodies and recording wrong offsets — a truncated
// tile in ~17% of runs (and a DATA RACE on every `-race` run). This test creates
// that exact >=2-level concurrent-drain condition so CI's `go test ./... -race`
// catches any regression. (A single-level sink never triggered it, which is why
// the existing tests missed it.)
func TestStreamwriterSinkConcurrentDrainsNoRace(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.tif")
	w, err := streamwriter.Create(out, streamwriter.Options{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Two tiled levels → the sink spawns two concurrent drain goroutines.
	specs := []streamwriter.LevelSpec{
		{ImageWidth: 1024, ImageHeight: 1024, TileWidth: 256, TileHeight: 256, Compression: 1, Photometric: 2, SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8}},
		{ImageWidth: 512, ImageHeight: 512, TileWidth: 256, TileHeight: 256, Compression: 1, Photometric: 2, SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8}},
	}
	var handles []*streamwriter.LevelHandle
	for _, s := range specs {
		h, err := w.AddLevel(s)
		if err != nil {
			t.Fatalf("addlevel: %v", err)
		}
		handles = append(handles, h)
	}
	sink := newStreamwriterSink(handles)

	// Feed both levels concurrently to maximize drain interleaving.
	grids := [][2]int{{4, 4}, {2, 2}} // cols,rows per level
	var wg sync.WaitGroup
	for lvl, g := range grids {
		for row := 0; row < g[1]; row++ {
			for col := 0; col < g[0]; col++ {
				wg.Add(1)
				go func(lvl, col, row int) {
					defer wg.Done()
					body := make([]byte, 64+(col+row)*8)
					for i := range body {
						body[i] = byte(lvl*100 + col*10 + row)
					}
					if err := sink.WriteTile(lvl, col, row, body); err != nil {
						t.Errorf("WriteTile lvl%d (%d,%d): %v", lvl, col, row, err)
					}
				}(lvl, col, row)
			}
		}
	}
	wg.Wait()
	if err := sink.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		t.Fatalf("output not finalized: stat=%v", err)
	}
}

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
