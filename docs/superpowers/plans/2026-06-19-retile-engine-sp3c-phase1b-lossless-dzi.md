# SP3c Phase 1b — lossless DZI/SZI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Checkbox (`- [ ]`) steps.

**Goal:** `convert --to dzi|szi --lossless` copies the source's stored JPEG tiles
into the Deep Zoom base level verbatim (no generational loss); edges + lower levels
are engine-regenerated.

**Architecture:** A `losslessDZISink` wraps the existing `dziWriterSink`. The
engine (`emitDZIPyramid`) runs unchanged; for **engine level 0 (= DZI base =
native) interior tiles** the sink substitutes the verbatim source tile
(`l0.TileInto(col,row)` — a complete standalone JPEG) instead of the engine's
re-encode. `--lossless` is full-slide, jpeg-source only; it auto-configures the DZI
tile grid to match the source (`tile-size = source L0 tile size`, `overlap = 0`).

**Spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-phase1b-lossless-dzi-design.md`.

**Branch:** `feat/retile-engine-sp3c-1b-lossless-dzi` (off main@a81f694).

**Key code facts (verified):**
- `runConvertDZI` (`convert_dzi.go:29`): `src, slide := source.OpenWithSlide`; `l0
  := slide.Pyramids()[0].Levels[0]` (opentile `*Level`, has `.Size.W/H`,
  `.TileInto`, `.TileMaxSize`); builds `cfg := dzi.Config{… TileSize:
  cvDZITileSize, Overlap: cvDZIOverlap}`; calls `emitDZIPyramid(ctx, slide, w, cfg,
  srcRegion)`. `runConvertSZI` mirrors it.
- `emitDZIPyramid` builds `newDZIWriterSink(w, len(levels))` and drives
  `retile.Run`. `dziWriterSink` maps engine level k → DZI level `nLevels-1-k`, so
  **engine level 0 = DZI base = native**.
- `dzi.EdgeTileDims(w,h,tileSize,col,row)` returns `(tileSize,tileSize)` for
  interior tiles, the trimmed remainder for edges.
- `src.Levels()[0].Compression()` returns a `source.Compression`
  (`source.CompressionJPEG` for JPEG).

---

### Task 1: `--lossless` flag, validation, geometry auto-config

**Files:**
- Modify: `cmd/wsitools/convert.go` (flag + `runConvert` gate)
- Create: `cmd/wsitools/dzi_lossless.go` (the shared validate+auto-config helper)
- Modify: `cmd/wsitools/convert_dzi.go` (`runConvertDZI`), `cmd/wsitools/convert_szi.go`
  (`runConvertSZI`)
- Test: `cmd/wsitools/dzi_lossless_test.go`

- [ ] **Step 1: Add the `--lossless` flag**

In `cmd/wsitools/convert.go`: add `cvLossless bool` to the var block; in `init()`:
```go
	convertCmd.Flags().BoolVar(&cvLossless, "lossless", false, "lossless --to dzi|szi: copy source JPEG base tiles verbatim (no re-encode)")
```
In `runConvert`, after `--to` is resolved (and before the rect block), gate it:
```go
	if cvLossless && cvTo != "dzi" && cvTo != "szi" {
		return fmt.Errorf("--lossless is only supported with --to dzi|szi; use `crop --lossless` for the TIFF family")
	}
```

- [ ] **Step 2: Write the failing test for the validate+config helper**

Create `cmd/wsitools/dzi_lossless_test.go`:

```go
package main

import "testing"

func TestLosslessDZIConfig(t *testing.T) {
	// jpeg source, tile 256, no factor/rect, no explicit dzi flags →
	// auto-config tile-size=256, overlap=0, no error.
	cfg, err := losslessDZIConfig(losslessDZIInputs{
		isJPEG: true, srcTileSize: 256, factor: 1, rectSet: false,
		userSetTileSize: false, userSetOverlap: false,
		reqTileSize: 256, reqOverlap: 1, // requested (defaults); overridden
	})
	if err != nil {
		t.Fatalf("valid lossless: %v", err)
	}
	if cfg.tileSize != 256 || cfg.overlap != 0 {
		t.Fatalf("auto-config: got tile=%d overlap=%d want 256/0", cfg.tileSize, cfg.overlap)
	}
}

func TestLosslessDZIConfig_Errors(t *testing.T) {
	base := losslessDZIInputs{isJPEG: true, srcTileSize: 240, factor: 1, rectSet: false, reqTileSize: 240, reqOverlap: 0}
	// non-jpeg source
	bad := base
	bad.isJPEG = false
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("non-jpeg source must error")
	}
	// factor
	bad = base
	bad.factor = 2
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("--lossless + --factor must error")
	}
	// rect
	bad = base
	bad.rectSet = true
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("--lossless + --rect must error")
	}
	// explicit conflicting --dzi-overlap
	bad = base
	bad.userSetOverlap = true
	bad.reqOverlap = 1
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("explicit --dzi-overlap 1 with --lossless must error")
	}
	// explicit conflicting --dzi-tile-size
	bad = base
	bad.userSetTileSize = true
	bad.reqTileSize = 512 // != srcTileSize 240
	if _, err := losslessDZIConfig(bad); err == nil {
		t.Error("explicit --dzi-tile-size != source must error")
	}
}
```

- [ ] **Step 3: Run — expect FAIL** (`losslessDZIConfig` undefined)

Run: `go test ./cmd/wsitools/ -run TestLosslessDZIConfig`

- [ ] **Step 4: Implement the helper**

Create `cmd/wsitools/dzi_lossless.go`:

```go
package main

import "fmt"

// losslessDZIInputs gathers everything losslessDZIConfig needs to validate a
// lossless DZI/SZI request and resolve the tile grid.
type losslessDZIInputs struct {
	isJPEG          bool // source L0 codec is JPEG
	srcTileSize     int  // source L0 tile size (square)
	factor          int  // resolved downsample factor (1 = none)
	rectSet         bool // any --rect/--x/--y/--w/--h provided
	userSetTileSize bool // --dzi-tile-size explicitly set
	userSetOverlap  bool // --dzi-overlap explicitly set
	reqTileSize     int  // requested dzi tile size
	reqOverlap      int  // requested dzi overlap
}

type losslessDZIResolved struct {
	tileSize int
	overlap  int
}

// losslessDZIConfig validates a lossless DZI/SZI request and returns the tile grid
// that makes verbatim base-tile copy possible (tile-size == source, overlap 0).
// Verbatim copy needs the DZI grid to match the source L0 grid, so an explicit
// conflicting --dzi-tile-size/--dzi-overlap is an error rather than a silent
// override.
func losslessDZIConfig(in losslessDZIInputs) (losslessDZIResolved, error) {
	if !in.isJPEG {
		return losslessDZIResolved{}, fmt.Errorf("--lossless requires a JPEG source (Deep Zoom tiles are jpeg/png)")
	}
	if in.factor != 1 {
		return losslessDZIResolved{}, fmt.Errorf("--lossless cannot be combined with --factor/--target-mag (verbatim tiles can't be downsampled)")
	}
	if in.rectSet {
		return losslessDZIResolved{}, fmt.Errorf("--lossless --to dzi|szi is full-slide only (no --rect) in this release")
	}
	if in.userSetTileSize && in.reqTileSize != in.srcTileSize {
		return losslessDZIResolved{}, fmt.Errorf("--lossless requires the DZI tile size to match the source (%d); drop --dzi-tile-size or set it to %d", in.srcTileSize, in.srcTileSize)
	}
	if in.userSetOverlap && in.reqOverlap != 0 {
		return losslessDZIResolved{}, fmt.Errorf("--lossless requires --dzi-overlap 0 (overlap re-cuts tiles); drop --dzi-overlap")
	}
	return losslessDZIResolved{tileSize: in.srcTileSize, overlap: 0}, nil
}
```

- [ ] **Step 5: Run — expect PASS**

Run: `go test ./cmd/wsitools/ -run TestLosslessDZIConfig`

- [ ] **Step 6: Wire into `runConvertDZI` and `runConvertSZI`**

In both, after `cfg` is built (and `l0`/`src` are available), when `cvLossless`:
```go
	if cvLossless {
		srcTile := l0.TileSize.W // confirm the opentile Level tile-size accessor (.TileSize.W or .TileSize().X)
		res, lerr := losslessDZIConfig(losslessDZIInputs{
			isJPEG:          src.Levels()[0].Compression() == source.CompressionJPEG,
			srcTileSize:     srcTile,
			factor:          factor,
			rectSet:         rectFlagsSet(cmd),
			userSetTileSize: cmd.Flags().Changed("dzi-tile-size"),
			userSetOverlap:  cmd.Flags().Changed("dzi-overlap"),
			reqTileSize:     cvDZITileSize,
			reqOverlap:      cvDZIOverlap,
		})
		if lerr != nil {
			return lerr
		}
		cfg.TileSize = res.tileSize
		cfg.Overlap = res.overlap
		fmt.Printf("lossless: base tiles copied verbatim (tile-size %d, overlap 0); edges + lower levels regenerated\n", cfg.TileSize)
	}
```
(Confirm: `l0.TileSize` field/accessor on the opentile `*Level`; `source` is
imported in both files. Place this AFTER the `cfg := dzi.Config{…}` construction so
it overrides TileSize/Overlap, and BEFORE `dzi.NewWriter(... cfg)`.)

Pass `cvLossless` to `emitDZIPyramid` (Task 2 changes its signature).

- [ ] **Step 7: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'LosslessDZI|DZI|Dzi|Convert' -count=1`
Expected: PASS. `gofmt -l` → clean. (emitDZIPyramid signature change lands in Task 2;
if building between tasks, temporarily pass the flag — but prefer doing Task 2's
signature change first if the build breaks.)

- [ ] **Step 8: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/dzi_lossless.go cmd/wsitools/dzi_lossless_test.go cmd/wsitools/convert_dzi.go cmd/wsitools/convert_szi.go
git commit -m "$(cat <<'EOF'
feat(convert): --lossless flag + validation for dzi|szi

--lossless (dzi|szi only) validates a jpeg source, rejects --factor/--rect,
and auto-configures the DZI grid to match the source (tile-size = source,
overlap 0) so base tiles can be copied verbatim; explicit conflicting
--dzi-tile-size/--dzi-overlap errors. Sink wiring lands next.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `losslessDZISink` + `emitDZIPyramid` wiring

**Files:**
- Modify: `cmd/wsitools/convert_dzi.go` (`emitDZIPyramid` signature; new
  `losslessDZISink`)
- Test: `cmd/wsitools/dzi_lossless_sink_test.go`

- [ ] **Step 1: Write the failing sink test**

Create `cmd/wsitools/dzi_lossless_sink_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

// fakeDZISink records WriteTile calls.
type fakeDZISink struct {
	calls map[[3]int][]byte
}

func newFakeDZISink() *fakeDZISink { return &fakeDZISink{calls: map[[3]int][]byte{}} }
func (f *fakeDZISink) WriteTile(level, col, row int, body []byte) error {
	cp := make([]byte, len(body))
	copy(cp, body)
	f.calls[[3]int{level, col, row}] = cp
	return nil
}

// fakeTileReader serves a fixed "verbatim" byte slice for any tile.
type fakeTileReader struct{ tile []byte; maxSize int }

func (f *fakeTileReader) TileMaxSize() int { return f.maxSize }
func (f *fakeTileReader) TileInto(tx, ty int, dst []byte) (int, error) {
	n := copy(dst, f.tile)
	return n, nil
}

func TestLosslessDZISink_InteriorVerbatim_EdgeEngine(t *testing.T) {
	inner := newFakeDZISink()
	verbatim := []byte("VERBATIM-SOURCE-TILE")
	// 300x300 image, tile 256 → grid 2x2; tile (0,0) interior, (1,0)/(0,1)/(1,1) edge.
	s := &losslessDZISink{
		inner: inner, src: &fakeTileReader{tile: verbatim, maxSize: 64},
		baseW: 300, baseH: 300, tileSize: 256,
	}
	enc := []byte("ENGINE-ENCODED")
	// engine level 0 (base):
	_ = s.WriteTile(0, 0, 0, enc) // interior → verbatim
	_ = s.WriteTile(0, 1, 0, enc) // edge → engine
	// a lower level (engine level 1): always engine
	_ = s.WriteTile(1, 0, 0, enc)

	if got := inner.calls[[3]int{0, 0, 0}]; !bytes.Equal(got, verbatim) {
		t.Errorf("interior base tile: got %q want verbatim", got)
	}
	if got := inner.calls[[3]int{0, 1, 0}]; !bytes.Equal(got, enc) {
		t.Errorf("edge base tile: got %q want engine bytes", got)
	}
	if got := inner.calls[[3]int{1, 0, 0}]; !bytes.Equal(got, enc) {
		t.Errorf("lower level: got %q want engine bytes", got)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`losslessDZISink` undefined)

Run: `go test ./cmd/wsitools/ -run TestLosslessDZISink`

- [ ] **Step 3: Implement the sink**

In `cmd/wsitools/convert_dzi.go`, add (near `dziWriterSink`):

```go
// losslessTileReader is the subset of opentile's *Level the lossless sink needs:
// verbatim compressed-tile reads.
type losslessTileReader interface {
	TileMaxSize() int
	TileInto(tx, ty int, dst []byte) (int, error)
}

// losslessDZISink wraps a retile.TileSink. For the engine's finest level (level 0
// = DZI base = native) it substitutes the verbatim source tile for INTERIOR tiles
// (a complete standalone JPEG from src.TileInto), giving byte-identical interior
// base tiles with no re-encode. Edge base tiles and all lower levels pass the
// engine's encoded bytes through unchanged (edges need the trimmed remainder
// dims; lower levels are the regenerated descent).
type losslessDZISink struct {
	inner    retile.TileSink
	src      losslessTileReader
	baseW    int
	baseH    int
	tileSize int
}

func (s *losslessDZISink) WriteTile(level, col, row int, encoded []byte) error {
	if level == 0 {
		tw, th := dzi.EdgeTileDims(s.baseW, s.baseH, s.tileSize, col, row)
		if tw == s.tileSize && th == s.tileSize { // interior tile
			buf := make([]byte, s.src.TileMaxSize())
			n, err := s.src.TileInto(col, row, buf)
			if err != nil {
				return fmt.Errorf("lossless: read source tile (%d,%d): %w", col, row, err)
			}
			return s.inner.WriteTile(level, col, row, buf[:n])
		}
	}
	return s.inner.WriteTile(level, col, row, encoded)
}
```
(Confirm `retile.TileSink` is the sink interface `dziWriterSink` implements, and
`dziWriterSink` satisfies `losslessDZISink.inner`. Import `retile`/`dzi` as needed —
both already imported in this file.)

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./cmd/wsitools/ -run TestLosslessDZISink`

- [ ] **Step 5: Wire into `emitDZIPyramid`**

Change `emitDZIPyramid`'s signature to accept a lossless flag + the source L0:
```go
func emitDZIPyramid(ctx context.Context, slide *opentile.Slide, w dziTileSink, cfg dzi.Config, srcRegion opentile.Region, lossless bool, srcL0 losslessTileReader) error {
```
Just before `retile.Run`, build the sink:
```go
	var sink retile.TileSink = newDZIWriterSink(w, len(levels))
	if lossless {
		sink = &losslessDZISink{inner: sink, src: srcL0, baseW: cfg.Width, baseH: cfg.Height, tileSize: cfg.TileSize}
	}
```
and pass `sink` to `retile.Run` (replace the inline `Sink: newDZIWriterSink(...)`).
Update both call sites (`runConvertDZI`, `runConvertSZI`) to pass `cvLossless` and
`l0` (the opentile `*Level`, which satisfies `losslessTileReader` via its
`TileInto`/`TileMaxSize`). For the non-lossless case `srcL0` is unused — pass `l0`
anyway (harmless) or `nil`.

- [ ] **Step 6: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'LosslessDZI|DZI|Dzi|Convert' -count=1`
Expected: PASS. `gofmt -l` → clean.

- [ ] **Step 7: Commit**

```bash
git add cmd/wsitools/convert_dzi.go cmd/wsitools/convert_szi.go cmd/wsitools/dzi_lossless_sink_test.go
git commit -m "$(cat <<'EOF'
feat(dzi): losslessDZISink — verbatim interior base tiles

emitDZIPyramid takes a lossless flag + source L0; when set, the sink
substitutes the verbatim source JPEG tile (TileInto = complete standalone
tile) for interior base tiles, leaving edges + lower levels as the engine
regenerates them. Byte-identical interior base, no generational loss.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`.

- [ ] **Step 2: lossless DZI runs + auto-configures + notice**

```bash
./bin/wsitools convert --to dzi --lossless -o /tmp/1b.dzi sample_files/svs/CMU-1-Small-Region.svs
grep -o 'TileSize="[0-9]*" *Overlap="[0-9]*"\|Overlap="[0-9]*" *TileSize="[0-9]*"' /tmp/1b.dzi
```
Expected: prints the "lossless: base tiles copied verbatim (tile-size 240, overlap
0)…" notice; manifest TileSize=240, Overlap=0 (CMU-1 source tile is 240).

- [ ] **Step 3: interior base tile is byte-identical to the source**

```bash
# Interior base tile (1,1) at the deepest level dir; compare to the source's tile.
# The base level dir is the highest-numbered _files/<N>/ dir.
python3 - <<'PY'
import glob, os, subprocess, sys
files = "/tmp/1b_files"
lvls = sorted(int(os.path.basename(d)) for d in glob.glob(files+"/*"))
base = str(lvls[-1])
tile = f"{files}/{base}/1_1.jpeg"  # col=1,row=1 interior
d = open(tile,'rb').read()
assert d[:2]==b'\xff\xd8' and d[-2:]==b'\xff\xd9', "not a complete JPEG"
print("OK base interior tile is a complete JPEG,", len(d), "bytes:", tile)
PY
```
Expected: prints OK (a complete standalone JPEG). (A stronger check — comparing to
`l0.TileInto(1,1)` — is the Task-2 unit test's job + a Go integration test below.)

- [ ] **Step 3b: Go integration test for byte-identity** (add under
`-tags integration`, controller runs with fixtures):
A test that opens CMU-1, reads `l0.TileInto(1,1)`, opens the generated DZI base
tile file `1_1.jpeg`, and asserts the bytes are equal. (Lossy DZI would differ.)

- [ ] **Step 4: guards**

```bash
./bin/wsitools convert --to dzi --lossless --factor 2 -o /tmp/x.dzi sample_files/svs/CMU-1-Small-Region.svs 2>&1 | grep -i "cannot be combined"
./bin/wsitools convert --to dzi --lossless -o /tmp/x.dzi sample_files/svs/JP2K-33003-1.svs 2>&1 | grep -i "requires a JPEG source"
./bin/wsitools convert --to tiff --lossless -o /tmp/x.tiff sample_files/svs/CMU-1-Small-Region.svs 2>&1 | grep -i "only supported with --to dzi"
./bin/wsitools convert --to dzi --lossless --dzi-overlap 1 -o /tmp/x.dzi sample_files/svs/CMU-1-Small-Region.svs 2>&1 | grep -i "overlap 0"
```
Expected: each prints its guard error.

- [ ] **Step 5: lossy DZI unchanged**

```bash
./bin/wsitools convert --to dzi -o /tmp/1b-lossy.dzi sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
```
Expected: succeeds with the default grid (tile 256, overlap 1) — unchanged.

- [ ] **Step 6: Clean up** `/tmp/1b* /tmp/x.*`.

---

## Self-review

**Spec coverage:** `--lossless` flag (Task 1); jpeg-source/factor/rect/geometry
validation via `losslessDZIConfig` (Task 1); `losslessDZISink` verbatim-interior
substitution (Task 2); full integration incl. byte-identity + guards (Task 3).
Notice printed. SZI mirrors DZI (shared helper + shared `emitDZIPyramid`).

**Placeholder scan:** the two "confirm the opentile Level tile-size accessor"
notes are bounded look-ups (`l0.TileSize.W` vs `.TileSize().X`), not placeholders.

**Type consistency:** `losslessDZIConfig(losslessDZIInputs) (losslessDZIResolved,
error)`; `losslessDZISink{inner retile.TileSink, src losslessTileReader, baseW,
baseH, tileSize int}`; `emitDZIPyramid(…, lossless bool, srcL0 losslessTileReader)`
— both call sites updated.

## Boundaries

**In Phase 1b:** lossless `--to dzi|szi` (full-slide, jpeg source). **Deferred:**
the base-level engine-skip efficiency optimization (the engine still re-encodes
interior base tiles that the sink discards); `--lossless --rect`; lossless for the
TIFF family via `convert` (use `crop --lossless`); Phase 2 conformance gate.
