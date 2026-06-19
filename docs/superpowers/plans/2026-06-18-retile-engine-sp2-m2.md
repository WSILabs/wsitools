# Streaming Retile Engine (SP2) — Milestone 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drive the `streamwriter` (svs/tiff/ome-tiff) and `cogwsiwriter` (cog-wsi) writers through the M1 `internal/retile` engine via two new `TileSink`s, and route **overlapping/stitched sources** (Ventana BIF) to the engine for those four targets — retiring the hard-error overlap guard for them.

**Architecture:** When `source.Level.Overlapping()` is true and the target is one of `{cog-wsi, svs, tiff, ome-tiff}`, a new engine branch decodes L0 once via `ScaledStrips` (compositing the stitched tiles), box-2× derives a floored octave pyramid, encodes via a `codec.Encoder`-backed `TileEncoder`, and routes tiles to a per-container sink. The engine produces only pyramid tiles; associated images + metadata + SVS-thumbnail-at-IFD-1 reuse the existing driver helpers unchanged.

**Tech Stack:** Go; `internal/retile` (M1); `internal/codec`; `internal/tiff/streamwriter` + `internal/tiff/cogwsiwriter`; opentile-go.

**Scope:** M2 only — the two sinks + stitched-source routing. All new code is in `cmd/wsitools` (the engine + writers are unchanged). Non-overlapping sources keep today's per-tile path. Per the M2 spec (`docs/superpowers/specs/2026-06-18-retile-engine-sp2-m2-design.md`): match-source shape and non-overlapping transcode/downsample are M3/M4; the BIF sink and DICOM-via-engine are deferred.

---

## Key facts (ground truth from the codebase)

- **Engine (M1, done):** `retile.Run(ctx, retile.Spec{Slide, SrcRegion, OutL0, Levels, Kernel, Encoder, Sink, Workers}) error`; `retile.ComputeLevels(outL0 opentile.Size, tileW, tileH, overlap, levelRatio, levelCount int) []retile.LevelSpec` (finest-first, `Index==k`); `retile.TileEncoder` = `EncodeTile(rgb []byte, w, h int) ([]byte, error)`; `retile.TileSink` = `WriteTile(level, col, row int, encoded []byte) error`. The engine calls `Sink.WriteTile` from a **single** drainer goroutine, tiles **interleaved across levels** and **out of grid order within a level**.
- **streamwriter:** `w.AddLevel(streamwriter.LevelSpec) (*streamwriter.LevelHandle, error)`. The handle has a reorder buffer + the drain protocol `WriteTile(x,y,bytes)` (submit) / `NextReady() (idx, bytes, ok, err)` / `WriteTileAtIndex(idx, bytes)` / `CloseInput()` / `Abort(err)`. The existing `transcodeLevel` (convert_tiff.go:437) runs exactly one drain goroutine per level — copy that pattern.
- **cogwsiwriter:** `w.AddLevel(cogwsiwriter.LevelSpec) (*cogwsiwriter.LevelHandle, error)`; `handle.WriteTile(tx, ty uint32, bytes)` requires **strict row-major from (0,0)**, no reorder buffer. Sets `NewSubfileType`/`WSIImageType=Pyramid` internally; `LevelSpec.IsL0` drives L0 metadata.
- **codec:** `codec.Encoder` = `LevelHeader() []byte` / `EncodeTile(rgb []byte, w, h int, dst []byte) ([]byte, error)` (abbreviated) / `TIFFCompressionTag() uint16` / `Close()`. Built via `fac.NewEncoder(codec.LevelGeometry{TileWidth,TileHeight,PixelFormat:codec.PixelFormatRGB8}, codec.Quality{Knobs})`. Concurrency-safe (the existing pipeline shares one across workers).
- **Reused driver helpers:** `tightRGB(*decoder.Image) []byte`, `emitSVSThumbnailAtL0(src, w, lvlIndex, container, omeSynthetic, plan) (bool, error)`, `writeAssociatedImages(src, w, container, omeSynthetic, plan) error`, `newSubfileTypeForLevel(idx int, container string) uint32`, `buildL0ImageDescriptionTag(desc string) []tiff.RawTag`, `compressionTagFor(source.Compression) uint16`, `faithfulCOGWSISpec(a) (cogwsiwriter.AssociatedSpec, error)`, the cog-wsi associated loop in `writeCOGWSI` (associated_rebuild.go:58-96).
- **Routing:** `runConvert` (convert.go:106) calls `guardStitchedSource(input, cvTo)` before the `switch cvTo`. Targets: cog-wsi→`runConvertCOGWSI`, svs/tiff/ome-tiff→`runConvertTIFF`.
- **BIF fixtures (local only, NOT in CI):** `sample_files/bif/{1_19,S12-18199-1A,AC1.592,Ventana-1,OS-1}.bif`. `1_19.bif` is **non-overlapping**; the others are **stitched**. Smallest stitched = `S12-18199-1A.bif` (46 MB) — use it for integration tests. Integration tests are **fixture-gated** (skip if absent); the controller runs them.

---

## File Structure

All new code in `cmd/wsitools`:
- `cmd/wsitools/retile_sink.go` — `codecTileEncoder`, `flooredLevelCount`, and shared engine-branch helper (`octaveLevelSpecsFor`, `runStitchedEngine` core).
- `cmd/wsitools/retile_sink_streamwriter.go` — `streamwriterSink` (+ per-level drains).
- `cmd/wsitools/retile_sink_cogwsi.go` — `cogwsiSink` (+ per-level row-major reorder).
- `cmd/wsitools/convert_stitched.go` — `convertStitchedTIFF` (svs/tiff/ome-tiff) + `convertStitchedCOGWSI` (cog-wsi).
- Tests alongside each (`*_test.go`).
- Modified: `cmd/wsitools/convert_cogwsi.go`, `cmd/wsitools/convert_tiff.go` (overlap branch), `cmd/wsitools/convert_stitch_guard.go` (route instead of error for the 4 targets).

---

## Task 1: `flooredLevelCount` helper

**Files:**
- Create: `cmd/wsitools/retile_sink.go`
- Create: `cmd/wsitools/retile_sink_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/retile_sink_test.go`:

```go
package main

import "testing"

func TestFlooredLevelCount(t *testing.T) {
	cases := []struct {
		w, h, tile, want int
		note             string
	}{
		{1000, 800, 256, 3, "1000→500→250(≤256 stop): L0,1,2"},
		{256, 256, 256, 1, "already ≤ tile: single level"},
		{100, 100, 256, 1, "smaller than tile: single level"},
		{4096, 4096, 256, 5, "4096→2048→1024→512→256(≤256): 5 levels"},
		{300, 90, 256, 1, "min dim 90 ≤ 256 at L0: single level"},
		{4096, 4096, 512, 4, "4096→2048→1024→512(≤512): 4 levels"},
	}
	for _, c := range cases {
		if got := flooredLevelCount(c.w, c.h, c.tile); got != c.want {
			t.Errorf("flooredLevelCount(%d,%d,%d) = %d, want %d (%s)", c.w, c.h, c.tile, got, c.want, c.note)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestFlooredLevelCount -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: flooredLevelCount`.

- [ ] **Step 3: Implement `flooredLevelCount` in `cmd/wsitools/retile_sink.go`**

```go
package main

// flooredLevelCount returns the number of octave levels (each half the previous,
// ceil-halving) from native w×h down to and including the first level whose
// smaller dimension is ≤ tile. Always ≥ 1. This floors the engine's octave
// pyramid at a normal WSI bottom (a thumbnail-sized level) rather than 1×1.
func flooredLevelCount(w, h, tile int) int {
	n := 1
	for min2(w, h) > tile {
		w = (w + 1) / 2
		h = (h + 1) / 2
		n++
	}
	return n
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestFlooredLevelCount -v 2>&1 | grep -v 'duplicate librar'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/retile_sink.go cmd/wsitools/retile_sink_test.go
git commit -m "feat(convert): flooredLevelCount for the stitched-source octave pyramid"
```

---

## Task 2: `codecTileEncoder` adapter

Wraps a `codec.Encoder` as a `retile.TileEncoder` (abbreviated `EncodeTile`; the level's `JPEGTables` come from `LevelHeader()`).

**Files:**
- Modify: `cmd/wsitools/retile_sink.go`
- Modify: `cmd/wsitools/retile_sink_test.go`

- [ ] **Step 1: Write the failing test**

Add to `cmd/wsitools/retile_sink_test.go`:

```go
import (
	"bytes"
	stdjpeg "image/jpeg"
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
	_ "github.com/wsilabs/wsitools/internal/codec/all"
)

func TestCodecTileEncoderAbbreviatedRoundTrip(t *testing.T) {
	fac, err := codec.Lookup("jpeg")
	if err != nil {
		t.Fatalf("lookup jpeg: %v", err)
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{TileWidth: 64, TileHeight: 64, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: map[string]string{"q": "80"}})
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}
	defer enc.Close()
	te := &codecTileEncoder{enc: enc}

	rgb := make([]byte, 64*64*3)
	for i := range rgb {
		rgb[i] = 128
	}
	body, err := te.EncodeTile(rgb, 64, 64)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	// Abbreviated tile: stdlib JPEG decode FAILS without the tables (no DQT/DHT).
	if _, err := stdjpeg.Decode(bytes.NewReader(body)); err == nil {
		t.Errorf("expected abbreviated (table-less) JPEG to fail stdlib decode; it decoded — not abbreviated?")
	}
	// LevelHeader (tag 347) must be non-empty so the writer can supply the tables.
	if len(enc.LevelHeader()) == 0 {
		t.Errorf("LevelHeader empty; abbreviated tiles would be undecodable")
	}
}
```

Merge the `import` blocks (the file already has `import "testing"` from Task 1 — combine into one block).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestCodecTileEncoder -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: codecTileEncoder`.

- [ ] **Step 3: Implement `codecTileEncoder` in `retile_sink.go`**

Add (and add `"github.com/wsilabs/wsitools/internal/codec"` to the file's imports):

```go
// codecTileEncoder adapts a codec.Encoder to retile.TileEncoder. EncodeTile
// returns the ABBREVIATED tile body (no DQT/DHT); the level's JPEGTables tag
// (347) carries the shared tables from enc.LevelHeader(). One codecTileEncoder
// is shared across the engine's worker pool — codec.Encoder.EncodeTile is
// concurrency-safe (the existing transcode pipeline shares one the same way).
type codecTileEncoder struct {
	enc codec.Encoder
}

func (e *codecTileEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	return e.enc.EncodeTile(rgb, w, h, nil)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestCodecTileEncoder -v 2>&1 | grep -v 'duplicate librar'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/retile_sink.go cmd/wsitools/retile_sink_test.go
git commit -m "feat(convert): codecTileEncoder (codec.Encoder → retile.TileEncoder)"
```

---

## Task 3: `cogwsiSink` (per-level row-major reorder)

The engine delivers tiles out of grid order; cogwsiwriter requires strict row-major. The sink buffers per level and flushes the contiguous front.

**Files:**
- Create: `cmd/wsitools/retile_sink_cogwsi.go`
- Create: `cmd/wsitools/retile_sink_cogwsi_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/retile_sink_cogwsi_test.go`. A fake handle records the row-major order it receives:

```go
package main

import (
	"fmt"
	"testing"
)

// fakeRowMajorWriter records WriteTile calls and enforces strict row-major.
type fakeRowMajorWriter struct {
	cols, rows int
	got        []string
}

func (f *fakeRowMajorWriter) WriteTile(tx, ty uint32, body []byte) error {
	f.got = append(f.got, fmt.Sprintf("%d,%d:%s", tx, ty, string(body)))
	return nil
}

func TestCogwsiReorderEmitsRowMajor(t *testing.T) {
	// 3×2 grid (cols=3, rows=2), fed out of order; must emerge row-major.
	fw := &fakeRowMajorWriter{cols: 3, rows: 2}
	rb := newCogwsiLevelReorder(3, 2, fw.WriteTile)

	// Out-of-order submission (col,row) with body "<col><row>".
	order := [][2]int{{2, 0}, {0, 0}, {1, 1}, {1, 0}, {0, 1}, {2, 1}}
	for _, cr := range order {
		body := []byte(fmt.Sprintf("%d%d", cr[0], cr[1]))
		if err := rb.submit(cr[0], cr[1], body); err != nil {
			t.Fatalf("submit (%d,%d): %v", cr[0], cr[1], err)
		}
	}
	want := []string{"0,0:00", "1,0:10", "2,0:20", "0,1:01", "1,1:11", "2,1:21"}
	if len(fw.got) != len(want) {
		t.Fatalf("got %d writes, want %d: %v", len(fw.got), len(want), fw.got)
	}
	for i := range want {
		if fw.got[i] != want[i] {
			t.Errorf("write[%d] = %q, want %q (not strict row-major)", i, fw.got[i], want[i])
		}
	}
	if !rb.complete() {
		t.Errorf("reorder not complete after all tiles")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestCogwsiReorder -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: newCogwsiLevelReorder`.

- [ ] **Step 3: Implement `retile_sink_cogwsi.go`**

```go
package main

import (
	"fmt"

	retile "github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

// cogwsiLevelReorder turns the engine's out-of-order per-level tile delivery into
// the strict row-major sequence cogwsiwriter.LevelHandle.WriteTile requires. It
// holds out-of-order tiles in a map keyed by row-major index and flushes the
// contiguous front. The engine drives one sink from a single goroutine, so no
// locking is needed. Memory stays ~O(workers): the engine emits each level
// roughly row-major, so few tiles are ever held.
type cogwsiLevelReorder struct {
	cols, rows int
	next       int // next row-major index to emit
	held       map[int][]byte
	write      func(tx, ty uint32, body []byte) error
}

func newCogwsiLevelReorder(cols, rows int, write func(tx, ty uint32, body []byte) error) *cogwsiLevelReorder {
	return &cogwsiLevelReorder{cols: cols, rows: rows, held: map[int][]byte{}, write: write}
}

func (r *cogwsiLevelReorder) submit(col, row int, body []byte) error {
	idx := row*r.cols + col
	if idx < r.next || idx >= r.cols*r.rows {
		return fmt.Errorf("cogwsi reorder: tile (%d,%d) idx %d out of range [%d,%d)", col, row, idx, r.next, r.cols*r.rows)
	}
	r.held[idx] = body
	for {
		b, ok := r.held[r.next]
		if !ok {
			break
		}
		col := r.next % r.cols
		row := r.next / r.cols
		if err := r.write(uint32(col), uint32(row), b); err != nil {
			return err
		}
		delete(r.held, r.next)
		r.next++
	}
	return nil
}

func (r *cogwsiLevelReorder) complete() bool { return r.next == r.cols*r.rows && len(r.held) == 0 }

// cogwsiSink routes engine tiles to per-level cogwsiwriter handles through a
// per-level row-major reorder. Implements retile.TileSink.
type cogwsiSink struct {
	reorders []*cogwsiLevelReorder
}

func newCogwsiSink(handles []*cogwsiwriter.LevelHandle, levels []retile.LevelSpec) *cogwsiSink {
	rs := make([]*cogwsiLevelReorder, len(handles))
	for i, h := range handles {
		rs[i] = newCogwsiLevelReorder(levels[i].Cols, levels[i].Rows, h.WriteTile)
	}
	return &cogwsiSink{reorders: rs}
}

func (s *cogwsiSink) WriteTile(level, col, row int, encoded []byte) error {
	if level < 0 || level >= len(s.reorders) {
		return fmt.Errorf("cogwsiSink: level %d out of range", level)
	}
	return s.reorders[level].submit(col, row, encoded)
}

// finish errors if any level did not receive every tile (a lost-tile guard).
func (s *cogwsiSink) finish() error {
	for i, r := range s.reorders {
		if !r.complete() {
			return fmt.Errorf("cogwsiSink: level %d incomplete (next=%d of %d)", i, r.next, r.cols*r.rows)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestCogwsiReorder -v 2>&1 | grep -v 'duplicate librar'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/retile_sink_cogwsi.go cmd/wsitools/retile_sink_cogwsi_test.go
git commit -m "feat(convert): cogwsiSink with per-level row-major reorder"
```

---

## Task 4: `streamwriterSink` (per-level drains)

Reuses the existing `transcodeLevel` drain pattern: one ordered-drain goroutine per level, fed by the engine's `WriteTile`.

**Files:**
- Create: `cmd/wsitools/retile_sink_streamwriter.go`
- Create: `cmd/wsitools/retile_sink_streamwriter_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/retile_sink_streamwriter_test.go`. Drives a REAL `streamwriter.Writer` (pure Go) so the drain + routing are exercised end to end on a tiny 1-level grid, fed out of order:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

func TestStreamwriterSinkRoutesAndDrains(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.tif")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	w, err := streamwriter.New(f, streamwriter.Options{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// One level, 2×2 tiles (256px tiles → 512×512 image).
	lh, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 512, ImageHeight: 512, TileWidth: 256, TileHeight: 256,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
	})
	if err != nil {
		t.Fatalf("addlevel: %v", err)
	}
	sink := newStreamwriterSink([]*streamwriter.LevelHandle{lh})

	// Feed 4 tiles OUT of order; bodies are unique markers.
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
	// The file exists and is non-trivial; full byte-level read-back is covered by
	// the Task 6 integration test. Here we assert the sink drained without
	// deadlock and the writer finalized.
	st, err := os.Stat(out)
	if err != nil || st.Size() < int64(len(tiff.LittleEndianHeaderMagic())) {
		t.Fatalf("output not finalized: stat=%v size=%d", err, st.Size())
	}
}
```

NOTE for the implementer: verify the exact `streamwriter.New` constructor signature and `streamwriter.Options` zero-value, and whether a header-magic helper like `tiff.LittleEndianHeaderMagic()` exists. If `streamwriter.New`'s signature differs or there is no such tiff helper, adjust the test to the real API (e.g. assert `st.Size() > 0`) — the POINT of the test is: out-of-order in → `finish()` returns without deadlock → `w.Close()` succeeds. If the real constructor needs more (e.g. a path-based writer), mirror how `convert_tiff.go` constructs the writer. **If the streamwriter writer API is materially different from `AddLevel`+`Close` as used in convert_tiff.go, STOP and report BLOCKED with the real signature.**

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestStreamwriterSink -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: newStreamwriterSink`.

- [ ] **Step 3: Implement `retile_sink_streamwriter.go`**

```go
package main

import (
	"sync"

	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// streamwriterSink routes engine tiles to per-level streamwriter handles and
// runs the writer's ordered-drain protocol, one drain goroutine per level (the
// exact pattern in transcodeLevel). The handle's reorder buffer tolerates the
// engine's out-of-order delivery; the drain must run CONCURRENTLY with the
// engine or the bounded reorder buffer fills and WriteTile blocks. Implements
// retile.TileSink.
type streamwriterSink struct {
	handles []*streamwriter.LevelHandle
	wg      sync.WaitGroup
	mu      sync.Mutex
	firstErr error
}

func newStreamwriterSink(handles []*streamwriter.LevelHandle) *streamwriterSink {
	s := &streamwriterSink{handles: handles}
	for _, h := range handles {
		h := h
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				idx, body, ok, err := h.NextReady()
				if err != nil {
					s.setErr(err)
					return
				}
				if !ok {
					return
				}
				if err := h.WriteTileAtIndex(idx, body); err != nil {
					h.Abort(err)
					s.setErr(err)
					return
				}
			}
		}()
	}
	return s
}

func (s *streamwriterSink) setErr(err error) {
	s.mu.Lock()
	if s.firstErr == nil {
		s.firstErr = err
	}
	s.mu.Unlock()
}

func (s *streamwriterSink) WriteTile(level, col, row int, encoded []byte) error {
	return s.handles[level].WriteTile(uint32(col), uint32(row), encoded)
}

// finish signals end-of-input to every level and waits for all drains. Returns
// the first drain error (if any). Call AFTER retile.Run returns.
func (s *streamwriterSink) finish() error {
	for _, h := range s.handles {
		h.CloseInput()
	}
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstErr
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./cmd/wsitools/ -run TestStreamwriterSink -v 2>&1 | grep -v 'duplicate librar'`
Expected: PASS, no race. (Run with `-race` — this is the concurrency-bearing unit.)

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/retile_sink_streamwriter.go cmd/wsitools/retile_sink_streamwriter_test.go
git commit -m "feat(convert): streamwriterSink with per-level ordered drains"
```

---

## Task 5: shared engine-branch core + octave LevelSpecs

A helper that both container branches call: build the octave `[]retile.LevelSpec`, build the encoder, run `retile.Run` with a provided sink, and return. The per-container `AddLevel`/sink/metadata wiring stays in the container functions (Tasks 6, the cog-wsi half here).

**Files:**
- Modify: `cmd/wsitools/retile_sink.go`
- Modify: `cmd/wsitools/retile_sink_test.go`

- [ ] **Step 1: Write the failing test**

Add to `cmd/wsitools/retile_sink_test.go`:

```go
import opentile "github.com/wsilabs/opentile-go"

func TestOctaveLevelSpecsForFloors(t *testing.T) {
	// 1000×800, tile 256 → flooredLevelCount=3; specs finest-first, Index==k,
	// overlap 0, octave dims.
	specs := octaveLevelSpecsFor(opentile.Size{W: 1000, H: 800}, 256)
	if len(specs) != 3 {
		t.Fatalf("levels = %d, want 3", len(specs))
	}
	if specs[0].Index != 0 || specs[0].Width != 1000 || specs[0].Height != 800 || specs[0].Overlap != 0 {
		t.Errorf("L0 = %+v, want Index0 1000×800 overlap0", specs[0])
	}
	if specs[1].Width != 500 || specs[1].Height != 400 {
		t.Errorf("L1 = %d×%d, want 500×400", specs[1].Width, specs[1].Height)
	}
	if specs[2].Width != 250 || specs[2].Height != 200 {
		t.Errorf("L2 = %d×%d, want 250×200", specs[2].Width, specs[2].Height)
	}
	if specs[0].TileW != 256 || specs[0].TileH != 256 {
		t.Errorf("tile = %d×%d, want 256", specs[0].TileW, specs[0].TileH)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestOctaveLevelSpecsFor -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: octaveLevelSpecsFor`.

- [ ] **Step 3: Implement in `retile_sink.go`**

Add the `opentile` and `retile` imports, then:

```go
// octaveLevelSpecsFor builds the floored octave LevelSpec list for a stitched
// source: OutL0 dims, square tiles of size tile, overlap 0, halving until the
// smaller dim ≤ tile. Finest-first, Index==k (the engine + sinks agree on this).
func octaveLevelSpecsFor(outL0 opentile.Size, tile int) []retile.LevelSpec {
	return retile.ComputeLevels(outL0, tile, tile, 0 /*overlap*/, 2 /*ratio*/, flooredLevelCount(outL0.W, outL0.H, tile))
}
```

(Imports to add to `retile_sink.go`: `opentile "github.com/wsilabs/opentile-go"` and `retile "github.com/wsilabs/wsitools/internal/retile"`.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestOctaveLevelSpecsFor -v 2>&1 | grep -v 'duplicate librar'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/retile_sink.go cmd/wsitools/retile_sink_test.go
git commit -m "feat(convert): octaveLevelSpecsFor (floored octave LevelSpecs)"
```

---

## Task 6: cog-wsi stitched-source branch + wiring

Add `convertStitchedCOGWSI`, branch `runConvertCOGWSI` on overlap.

**Files:**
- Create: `cmd/wsitools/convert_stitched.go`
- Modify: `cmd/wsitools/convert_cogwsi.go`
- Test: controller-run integration (BIF→cog-wsi).

- [ ] **Step 1: Implement `convertStitchedCOGWSI` in `cmd/wsitools/convert_stitched.go`**

```go
package main

import (
	"context"
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/resample"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

// sourceIsOverlapping reports whether any source level has stitched/overlapping
// tiles (a Ventana BIF). The engine path is required for such sources.
func sourceIsOverlapping(src source.Source) bool {
	for _, lvl := range src.Levels() {
		if lvl.Overlapping() {
			return true
		}
	}
	return false
}

// convertStitchedCOGWSI re-tiles an overlapping source into a COG-WSI via the
// retile engine: decode L0 once (ScaledStrips composites the stitched tiles),
// box-2× derive a floored octave pyramid, encode, and feed the cogwsiSink.
// Associated images are copied via the existing writeCOGWSI associated logic.
func convertStitchedCOGWSI(ctx context.Context, slide *opentile.Slide, src source.Source, w *cogwsiwriter.Writer, plan assocEditPlan, workers int, knobs map[string]string, codecName string) error {
	l0 := slide.Pyramids()[0].Levels[0]
	outL0 := opentile.Size{W: l0.Size.W, H: l0.Size.H}
	tile := l0.TileSize.W
	if tile <= 0 {
		tile = 256
	}
	levels := octaveLevelSpecsFor(outL0, tile)

	fac, err := codec.Lookup(codecName)
	if err != nil {
		return err
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{TileWidth: tile, TileHeight: tile, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: knobs})
	if err != nil {
		return err
	}
	defer enc.Close()

	handles := make([]*cogwsiwriter.LevelHandle, len(levels))
	for i, ls := range levels {
		h, err := w.AddLevel(cogwsiwriter.LevelSpec{
			ImageWidth: uint32(ls.Width), ImageHeight: uint32(ls.Height),
			TileWidth: uint32(ls.TileW), TileHeight: uint32(ls.TileH),
			Compression: enc.TIFFCompressionTag(), Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			JPEGTables: enc.LevelHeader(),
			IsL0:       i == 0,
		})
		if err != nil {
			return fmt.Errorf("add level %d: %w", i, err)
		}
		handles[i] = h
	}

	sink := newCogwsiSink(handles, levels)
	if err := retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: l0.Size},
		OutL0:     outL0,
		Levels:    levels,
		Kernel:    resample.Nearest, // identity scale: ScaledStrips only stitches
		Encoder:   &codecTileEncoder{enc: enc},
		Sink:      sink,
		Workers:   workers,
	}); err != nil {
		return err
	}
	if err := sink.finish(); err != nil {
		return err
	}
	return writeCOGWSIAssociated(w, src, plan)
}
```

- [ ] **Step 2: Extract the associated-only half of `writeCOGWSI` into `writeCOGWSIAssociated`**

In `cmd/wsitools/associated_rebuild.go`, refactor: move the associated loop (the code after the pyramid loop, lines ~58-96) into a new exported-to-package function `writeCOGWSIAssociated(w *cogwsiwriter.Writer, src source.Source, plan assocEditPlan) error`, and have the existing `writeCOGWSI` call it after its pyramid loop. This keeps the verbatim-copy path identical AND lets the engine branch reuse the associated emission. Concretely:

```go
func writeCOGWSI(w *cogwsiwriter.Writer, src source.Source, plan assocEditPlan) error {
	for _, lvl := range src.Levels() {
		// ... existing verbatim pyramid copy loop (unchanged) ...
	}
	return writeCOGWSIAssociated(w, src, plan)
}

// writeCOGWSIAssociated writes src's associated images into w per plan (the
// associated half of writeCOGWSI, shared with the stitched-source engine path).
func writeCOGWSIAssociated(w *cogwsiwriter.Writer, src source.Source, plan assocEditPlan) error {
	if plan.dropAll {
		return nil
	}
	replaced := false
	for _, a := range src.Associated() {
		// ... existing associated loop body verbatim ...
	}
	if plan.replace != "" && !replaced {
		if err := w.AddAssociated(*plan.spec); err != nil {
			return fmt.Errorf("add new %s: %w", plan.replace, err)
		}
	}
	return nil
}
```

- [ ] **Step 3: Branch `runConvertCOGWSI` on overlap**

In `cmd/wsitools/convert_cogwsi.go`, the function opens `src` via `source.Open`. To reach the engine it needs the `*opentile.Slide` too — switch to `source.OpenWithSlide`. Replace the `source.Open` block and add the overlap branch before the verbatim `writeCOGWSI`:

```go
	src, slide, err := source.OpenWithSlide(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	// ... (keep the existing zero-levels check; SKIP the compressionTagFor
	// tile-copy precheck when the source is overlapping — the engine re-encodes,
	// so a source compression with no TIFF tag is fine on the engine path.)
```

Adjust the existing `compressionTagFor` precheck loop (convert_cogwsi.go:50-55) to run **only when not overlapping** (the engine path re-encodes, so it does not need a tile-copyable source compression):

```go
	overlapping := sourceIsOverlapping(src)
	if !overlapping {
		for _, lvl := range src.Levels() {
			if compressionTagFor(lvl.Compression()) == 0 {
				return fmt.Errorf("level %d: source compression %s has no standard TIFF Compression tag; cannot tile-copy",
					lvl.Index(), lvl.Compression())
			}
		}
	}
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
```

Then replace the `writeCOGWSI(w, src, plan)` call:

```go
	plan := assocEditPlan{dropAll: cvNoAssociated}
	if overlapping {
		err = convertStitchedCOGWSI(cmd.Context(), slide, src, w, plan, cvWorkers, parseCodecKnobs(cvQuality), resolveCodecName())
	} else {
		err = writeCOGWSI(w, src, plan)
	}
	if err != nil {
		w.Abort()
		return err
	}
```

NOTE: `parseCodecKnobs`/`resolveCodecName` are placeholders for however `runConvertTIFF` already resolves the `--codec`/`--quality` into a codec name + knobs map (find the existing logic — likely `cvCodec`/`cvQuality` parsed in convert_tiff.go — and reuse the SAME functions; do not invent new ones). If cog-wsi convert has no existing `--codec` flag plumbing, default the codec name to `"jpeg"` and knobs to the parsed `--quality`. **Confirm the real flag-resolution helpers before writing this; reuse them.**

- [ ] **Step 4: Build + run unit suite**

Run: `go build ./... 2>&1 | grep -v 'duplicate librar'; go test ./cmd/wsitools/ -run 'TestFlooredLevelCount|TestCodecTileEncoder|TestCogwsiReorder|TestStreamwriterSink|TestOctaveLevelSpecsFor' 2>&1 | grep -v 'duplicate librar' | tail -3`
Expected: build clean; the unit tests PASS. (No BIF needed yet.)

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/convert_stitched.go cmd/wsitools/convert_cogwsi.go cmd/wsitools/associated_rebuild.go
git commit -m "feat(convert): route overlapping source → cog-wsi via the retile engine"
```

- [ ] **Step 6: CONTROLLER integration verification (BIF→cog-wsi)**

The controller runs (fixture-gated, BIF is local-only):

```bash
make build
FIX=$(pwd)/sample_files/bif/S12-18199-1A.bif
./bin/wsitools convert "$FIX" --to cog-wsi -o /tmp/m2-cogwsi.tif -f 2>&1 | grep -v 'duplicate librar'
# Read back via opentile (info/validate): expect success, L0 dims = stitched hull,
# a floored octave level count, associated images preserved.
./bin/wsitools info /tmp/m2-cogwsi.tif 2>&1 | grep -v 'duplicate librar'
./bin/wsitools validate /tmp/m2-cogwsi.tif 2>&1 | grep -v 'duplicate librar'
```

Expected: convert succeeds (no "overlapping/stitched" error); `info` shows the hull L0 size + octave levels; `validate` clean. Compare L0 dims against the source's stitched `info`.

---

## Task 7: svs/tiff/ome-tiff stitched-source branch + wiring

Add `convertStitchedTIFF`, branch `runConvertTIFF` on overlap. Mirrors Task 6 but uses `streamwriter` + `streamwriterSink` + per-container `LevelSpec` shaping (NewSubfileType, WSIImageType=Pyramid, L0 ImageDescription) and the existing `emitSVSThumbnailAtL0` + `writeAssociatedImages`.

**Files:**
- Modify: `cmd/wsitools/convert_stitched.go`
- Modify: `cmd/wsitools/convert_tiff.go`
- Test: controller-run integration (BIF→svs/tiff/ome-tiff).

- [ ] **Step 1: Implement `convertStitchedTIFF` in `convert_stitched.go`**

```go
func convertStitchedTIFF(ctx context.Context, slide *opentile.Slide, src source.Source, w *streamwriter.Writer, container, srcImageDesc string, plan omeEditPlan, omeSynthetic bool, workers int, knobs map[string]string, codecName string) error {
	l0 := slide.Pyramids()[0].Levels[0]
	outL0 := opentile.Size{W: l0.Size.W, H: l0.Size.H}
	tile := l0.TileSize.W
	if tile <= 0 {
		tile = 256
	}
	levels := octaveLevelSpecsFor(outL0, tile)

	fac, err := codec.Lookup(codecName)
	if err != nil {
		return err
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{TileWidth: tile, TileHeight: tile, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: knobs})
	if err != nil {
		return err
	}
	defer enc.Close()

	handles := make([]*streamwriter.LevelHandle, len(levels))
	for i, ls := range levels {
		spec := streamwriter.LevelSpec{
			ImageWidth: uint32(ls.Width), ImageHeight: uint32(ls.Height),
			TileWidth: uint32(ls.TileW), TileHeight: uint32(ls.TileH),
			Compression: enc.TIFFCompressionTag(), Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			JPEGTables:     enc.LevelHeader(),
			NewSubfileType: newSubfileTypeForLevel(i, container),
			WSIImageType:   tiff.WSIImageTypePyramid,
		}
		if i == 0 && srcImageDesc != "" && (container == "svs" || container == "ome-tiff") {
			spec.ExtraTags = buildL0ImageDescriptionTag(srcImageDesc)
		}
		h, err := w.AddLevel(spec)
		if err != nil {
			return fmt.Errorf("add level %d: %w", i, err)
		}
		handles[i] = h
	}

	sink := newStreamwriterSink(handles)
	if err := retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: l0.Size},
		OutL0:     outL0,
		Levels:    levels,
		Kernel:    resample.Nearest,
		Encoder:   &codecTileEncoder{enc: enc},
		Sink:      sink,
		Workers:   workers,
	}); err != nil {
		return err
	}
	if err := sink.finish(); err != nil {
		return err
	}

	// SVS thumbnail at IFD 1 (only when container == svs); then associated images.
	if _, err := emitSVSThumbnailAtL0(src, w, 0 /*L0 index*/, container, omeSynthetic, plan); err != nil {
		return err
	}
	return writeAssociatedImages(src, w, container, omeSynthetic, plan)
}
```

Add the `streamwriter` + `tiff` imports to `convert_stitched.go`.

NOTE on `emitSVSThumbnailAtL0(src, w, lvlIndex, ...)`: in `transcodePyramid` it is called after EACH level with `lvl.Index()`, and internally only acts when `lvlIndex == 0 && container == "svs"`. Here the engine emits all pyramid levels first, so call it ONCE with `lvlIndex = 0` after the pyramid. **Verify** `emitSVSThumbnailAtL0`'s internal guard is `lvlIndex == 0` (read convert_tiff.go:812) — if it keys off something else, adjust the call so the thumbnail lands at IFD 1 exactly as the non-stitched path produces. The thumbnail must be emitted immediately after L0's IFD; since the engine writes L0 first and the thumbnail is added after `retile.Run`, confirm streamwriter orders IFDs by AddLevel/Add order such that the thumbnail becomes IFD 1. If streamwriter emits associated/thumbnail IFDs in call order after the pyramid levels, IFD 1 would NOT be the thumbnail (it'd follow all pyramid levels). **This is the one real risk in Task 7** — if the SVS thumbnail cannot be placed at IFD 1 via this post-hoc call, report DONE_WITH_CONCERNS and we will add the thumbnail level between L0 and L1 in the AddLevel sequence (emit it as a non-pyramid IFD right after L0's AddLevel, before the rest). Read `emitSVSThumbnailAtL0` + the streamwriter IFD ordering before implementing; pick the approach that reproduces IFD 1 = thumbnail for SVS.

- [ ] **Step 2: Branch `runConvertTIFF` on overlap**

In `cmd/wsitools/convert_tiff.go` (`runConvertTIFF`), it already resolves `src`, the container, `srcImageDesc`, `omeSynthetic`, the codec factory/knobs, and creates the `*streamwriter.Writer w`. Obtain the slide (`source.OpenWithSlide`, or `slide` if already available) and branch:

```go
	if sourceIsOverlapping(src) {
		err = convertStitchedTIFF(cmd.Context(), slide, src, w, resolvedContainer, srcImageDesc, omeEditPlan{dropAll: cvNoAssociated}, omeSynthetic, workers, knobs, codecName)
	} else {
		err = transcodePyramid(cmd.Context(), src, w, fac, knobs, workers, resolvedContainer, srcImageDesc, omeEditPlan{dropAll: cvNoAssociated}, omeSynthetic)
		if err == nil {
			err = writeAssociatedImages(src, w, resolvedContainer, omeSynthetic, omeEditPlan{})
		}
	}
	if err != nil {
		// existing error handling (Abort/remove temp)
	}
```

Match the EXISTING variable names in `runConvertTIFF` (`resolvedContainer`, `srcImageDesc`, `omeSynthetic`, `fac`, `knobs`, `workers`, and however the codec name is held). Read convert_tiff.go:300-345 to wire the branch with the real names; do not introduce new flag parsing. The non-overlapping arm must remain byte-for-byte the current behavior.

- [ ] **Step 3: Build + unit suite**

Run: `go build ./... 2>&1 | grep -v 'duplicate librar'; go test ./cmd/wsitools/ -run 'TestFlooredLevelCount|TestCodecTileEncoder|TestCogwsiReorder|TestStreamwriterSink|TestOctaveLevelSpecsFor' 2>&1 | grep -v 'duplicate librar' | tail -3`
Expected: build clean; unit tests PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/convert_stitched.go cmd/wsitools/convert_tiff.go
git commit -m "feat(convert): route overlapping source → svs/tiff/ome-tiff via the retile engine"
```

- [ ] **Step 5: CONTROLLER integration verification (BIF→svs/tiff/ome-tiff)**

```bash
make build
FIX=$(pwd)/sample_files/bif/S12-18199-1A.bif
for tgt in svs tiff ome-tiff; do
  ./bin/wsitools convert "$FIX" --to $tgt -o /tmp/m2-$tgt.tif -f 2>&1 | grep -v 'duplicate librar'
  echo "--- info $tgt ---"; ./bin/wsitools info /tmp/m2-$tgt.tif 2>&1 | grep -v 'duplicate librar'
  echo "--- validate $tgt ---"; ./bin/wsitools validate /tmp/m2-$tgt.tif 2>&1 | grep -v 'duplicate librar'
done
```

Expected: all three convert (no overlap error); `info` shows hull L0 + octave levels + associated images; SVS output shows the thumbnail at IFD 1; `validate` clean.

---

## Task 8: flip the guard to route; retire it for the 4 targets

The driver branches (Tasks 6-7) now handle overlap. Update `guardStitchedSource` so it no longer ERRORS for `{cog-wsi, svs, tiff, ome-tiff}` (they route internally), but STILL errors for `dicom`/`bif`.

**Files:**
- Modify: `cmd/wsitools/convert_stitch_guard.go`
- Modify: `cmd/wsitools/convert_integration_test.go` (the existing `TestConvertBIFBitExact` overlap expectation)

- [ ] **Step 1: Write/adjust the failing test**

In `cmd/wsitools/convert_integration_test.go`, `TestConvertBIFBitExact` currently expects stitched BIFs to produce the "overlapping/stitched" error for non-dzi/szi targets. Update the expectation: for `{cog-wsi, svs, tiff, ome-tiff}` a stitched BIF should now SUCCEED (or be skipped if that test is bit-exact-only); for `{dicom, bif}` it should still error. Add a focused routing unit test in a new `cmd/wsitools/convert_stitch_guard_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestGuardRoutesFourTargetsErrorsDicomBif(t *testing.T) {
	// The guard returns nil (route) for the engine-handled targets and an error
	// for dicom/bif. We assert the target classification directly via the helper.
	for _, tgt := range []string{"cog-wsi", "svs", "tiff", "ome-tiff", "dzi", "szi"} {
		if !guardTargetHandlesOverlap(tgt) {
			t.Errorf("target %q should be overlap-capable (route, not error)", tgt)
		}
	}
	for _, tgt := range []string{"dicom", "bif"} {
		if guardTargetHandlesOverlap(tgt) {
			t.Errorf("target %q should NOT be overlap-capable (still guarded)", tgt)
		}
	}
	// Message for a guarded target mentions the target.
	err := overlapGuardError("x.bif", "dicom")
	if err == nil || !strings.Contains(err.Error(), "dicom") {
		t.Errorf("guarded-target error = %v, want mention of dicom", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestGuardRoutesFourTargets -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: guardTargetHandlesOverlap`, `undefined: overlapGuardError`.

- [ ] **Step 3: Rewrite `guardStitchedSource`**

Replace `cmd/wsitools/convert_stitch_guard.go`'s body:

```go
// guardTargetHandlesOverlap reports whether target can consume an overlapping/
// stitched source. dzi/szi composite via their own descent; cog-wsi/svs/tiff/
// ome-tiff route through the retile engine (SP2 M2). dicom (derivedsource) and
// bif (no engine sink yet) cannot, so they stay guarded.
func guardTargetHandlesOverlap(target string) bool {
	switch target {
	case "dzi", "szi", "cog-wsi", "svs", "tiff", "ome-tiff", "":
		return true
	default:
		return false
	}
}

func overlapGuardError(input, target string) error {
	return fmt.Errorf("%s has overlapping/stitched tiles (e.g. a Ventana BIF): "+
		"re-tiling to %s is not yet supported — convert to a TIFF-family target "+
		"(cog-wsi/svs/tiff/ome-tiff) or dzi/szi, which composite the stitched image", input, target)
}

// guardStitchedSource refuses converting a stitched source to a target that
// cannot consume it (dicom/bif). Overlap-capable targets return nil and handle
// the source via their own descent (dzi/szi) or the retile engine (the four
// TIFF-family targets, SP2 M2).
func guardStitchedSource(input, target string) error {
	if guardTargetHandlesOverlap(target) {
		return nil
	}
	src, err := source.Open(input)
	if err != nil {
		return nil // let the actual convert path surface open errors
	}
	defer src.Close()
	for _, lvl := range src.Levels() {
		if lvl.Overlapping() {
			return overlapGuardError(input, target)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run unit tests**

Run: `go test ./cmd/wsitools/ -run 'TestGuardRoutesFourTargets' -v 2>&1 | grep -v 'duplicate librar'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/convert_stitch_guard.go cmd/wsitools/convert_stitch_guard_test.go cmd/wsitools/convert_integration_test.go
git commit -m "feat(convert): route stitched source to engine for TIFF family; guard only dicom/bif"
```

- [ ] **Step 6: CONTROLLER full verification**

```bash
make build
# Stitched BIF → each of the 4 targets succeeds; dicom/bif still error.
FIX=$(pwd)/sample_files/bif/S12-18199-1A.bif
for tgt in cog-wsi svs tiff ome-tiff; do ./bin/wsitools convert "$FIX" --to $tgt -o /tmp/m2-g-$tgt -f 2>&1 | grep -v 'duplicate librar'; done
./bin/wsitools convert "$FIX" --to dicom -o /tmp/m2-g.dcm -f 2>&1 | grep -iE 'overlap|stitch' && echo "DICOM still guarded OK"
# Non-overlapping source unchanged (a normal SVS still converts).
./bin/wsitools convert "$(pwd)/sample_files/svs/CMU-1-Small-Region.svs" --to tiff -o /tmp/m2-nonoverlap.tif -f 2>&1 | grep -v 'duplicate librar'
# Full race suite (heavy).
WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -race -count=1 -timeout 30m ./internal/retile/ ./cmd/wsitools/ 2>&1 | grep -v 'duplicate librar' | tail -4
```

Expected: 4 targets succeed from the stitched BIF; dicom guarded; non-overlapping SVS still converts; race suite green.

---

## Final verification + finish

After all tasks: dispatch a final code reviewer over `main..HEAD`, then use **superpowers:finishing-a-development-branch**. Branch: `feat/retile-engine-m2` off `main`.

**M2 acceptance:**
- Unit suite green (`flooredLevelCount`, `codecTileEncoder`, `cogwsiSink` reorder, `streamwriterSink` drain, `octaveLevelSpecsFor`).
- Stitched BIF → cog-wsi/svs/tiff/ome-tiff all succeed, read back via opentile with hull L0 dims + floored octave levels + associated images intact (+ SVS thumbnail at IFD 1).
- dicom/bif still guarded; non-overlapping sources unchanged.
- `cmd/wsitools` + `internal/retile` race suites green.

---

## Self-Review

**Spec coverage (M2):**
- streamwriterSink (reuse drain) → Task 4. ✓
- cogwsiSink (sink-side row-major reorder) → Task 3. ✓
- Octave-floored output + box-RGB resample (engine, unchanged) → Tasks 1/5 (`flooredLevelCount`, `octaveLevelSpecsFor`); resample is the M1 engine's box descent (no change). ✓
- TIFF-family TileEncoder (abbreviated + LevelHeader) → Task 2. ✓
- Routing: Overlapping()→engine for the 4 targets, guard for dicom/bif → Tasks 6/7 (branches) + Task 8 (guard). ✓
- Per-container shaping + metadata + associated + SVS thumbnail reuse → Tasks 6/7 (reuse `writeCOGWSIAssociated`, `emitSVSThumbnailAtL0`, `writeAssociatedImages`, `buildL0ImageDescriptionTag`, `newSubfileTypeForLevel`). ✓
- Error handling (first-error, Abort, atomic output) → sinks' `finish()` + drivers' existing Abort. ✓
- Testing: BIF read-back, reorder unit, race → Tasks 6/7/8 + 3/4. ✓

**Placeholder scan:** No TBD/TODO. The two "confirm the real helper/API" notes (codec-flag resolution in Task 6/7; streamwriter constructor in Task 4; SVS-thumbnail IFD-1 placement in Task 7) are explicit verification steps with a defined fallback, not placeholders — they exist because those signatures live in code the plan author should not guess.

**Type consistency:** `retile.LevelSpec{Index,Width,Height,Cols,Rows,TileW,TileH,Overlap}`, `retile.Spec`, `retile.TileEncoder.EncodeTile(rgb,w,h)`, `retile.TileSink.WriteTile(level,col,row,encoded)` — match M1. `codecTileEncoder{enc}`, `newCogwsiSink(handles,levels)`/`cogwsiSink.WriteTile`/`.finish()`, `newStreamwriterSink(handles)`/`.WriteTile`/`.finish()`, `octaveLevelSpecsFor`, `flooredLevelCount`, `sourceIsOverlapping`, `guardTargetHandlesOverlap`/`overlapGuardError`, `writeCOGWSIAssociated`, `convertStitchedCOGWSI`/`convertStitchedTIFF` — consistent across tasks. ✓

**Risk flagged:** Task 7 Step 1 — SVS thumbnail IFD-1 placement via a post-`retile.Run` call may not land at IFD 1 if streamwriter orders IFDs by add-order; the task includes the read-before-implement check + the DONE_WITH_CONCERNS fallback (emit the thumbnail right after L0's AddLevel). This is the one genuine integration unknown.
