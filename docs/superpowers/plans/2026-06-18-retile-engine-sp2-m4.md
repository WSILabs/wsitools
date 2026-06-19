# Streaming Retile Engine (SP2) — Milestone 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route non-overlapping TIFF-family transcode (`convert --to svs|tiff|ome-tiff --codec X`, same geometry, re-encode only) through the `internal/retile` engine via **select-octave** level emission — decode L0 once, box-derive the octave chain, encode/emit only the octaves matching a source level, preserving source pyramid structure.

**Architecture:** A new `retile.LevelSpec.Intermediate` flag lets the descent compute intermediate octaves (for box-reduction) without encoding them. `transcodeOctaveLevels` maps a power-of-2 source pyramid to that octave chain (emit at source octaves, Intermediate elsewhere). `convertTranscodeTIFF` reuses M2's `convertStitchedTIFF` machinery (encoder/sink/per-container shaping/thumbnail/associated), differing only in the level list (select-octave) and identity geometry. Non-power-of-2 sources fall back to the existing per-level `transcodePyramid`.

**Tech Stack:** Go; `internal/retile` (M1–M3); the M2 sinks/encoder; `internal/codec`; opentile-go.

**Scope:** M4 = the `Intermediate` engine flag + `transcodeOctaveLevels` + `convertTranscodeTIFF` + routing. `transcodePyramid`/`transcodeLevel` stay (fallback). cog-wsi (no transcode path), overlapping transcode (M2), `--factor` (M3) unchanged. Per `docs/superpowers/specs/2026-06-18-retile-engine-sp2-m4-design.md`.

---

## Key facts (ground truth)

- **Descent (`internal/retile/level.go`):** `feed` (line 62) does `if lb.cur != nil { lb.emitRow(lb.rowIndex); lb.rowIndex++ }` then ALWAYS `if lb.child != nil { lb.child.acceptDownsampled(boxDownsample2x(strip)) }`. `flush` (line 104) does an accum-finalize (`feed(short)`), a final buffer rotation, then `if lb.cur != nil && lb.rowIndex < lb.spec.Rows { lb.emitRow(lb.rowIndex); lb.rowIndex++ }`, then ALWAYS `if lb.child != nil { lb.child.flush() }`. `emitRow` (line 133) enqueues encodeJobs with `level: lb.spec.Index`.
- **`retile.LevelSpec`** (level.go): `{Index, Width, Height, Cols, Rows, TileW, TileH, Overlap int}`. Built by `ComputeLevels`/`octaveLevelSpecsFor`; the engine's `Run` builds the builder chain from `spec.Levels` finest-first, chaining `builders[i-1].child = builders[i]`.
- **`convertStitchedTIFF`** (convert_stitched.go, M2): builds one `codecTileEncoder` from `fac`/`knobs`; `specFor(i)` builds a `streamwriter.LevelSpec` (Compression=`enc.TIFFCompressionTag()`, Photometric 2, SPP 3, BitsPerSample 8/8/8, JPEGTables=`enc.LevelHeader()`, NewSubfileType=`newSubfileTypeForLevel(i, container)`, WSIImageType=`tiff.WSIImageTypePyramid`, and L0 gets `ExtraTags = buildL0ImageDescriptionTag(srcImageDesc)` for svs/ome-tiff); `AddLevel(L0)` → `emitSVSThumbnailAtL0(src,w,0,…)` → `AddLevel(L1..)`; `newStreamwriterSink(handles)`; `retile.Run(Spec{Slide, SrcRegion=full l0, OutL0, Levels, Kernel: resample.Nearest, Encoder, Sink, Workers})`; unconditional `sink.finish()` (prefer runErr); `writeAssociatedImages(src,w,container,omeSynthetic,plan)`.
- **`runConvertTIFFReencode`** branch (convert_tiff.go:347): `if sourceIsOverlapping(src) { convertStitchedTIFF(...) } else { transcodePyramid(...); if cvNoAssociated false: writeAssociatedImages(...) }`. It holds `slide` (from `source.OpenWithSlide`), `src`, `w`, `resolvedContainer`, `srcImageDesc`, `omeSynthetic`, `fac`, `knobs`, `workers`.
- **opentile slide levels:** `slide.Pyramid(0).Levels[i]` has `.Size` (opentile.Size{W,H}) and `.TileSize` (opentile.Size{W,H}).
- Imports: `retile`=`internal/retile`, `opentile`=`github.com/wsilabs/opentile-go`, `resample`=`github.com/wsilabs/opentile-go/resample`, `codec`=`internal/codec`, `tiff`=`internal/tiff`, `streamwriter`=`internal/tiff/streamwriter`.

---

## Task 1: Engine — `LevelSpec.Intermediate` (compute, don't encode)

**Files:**
- Modify: `internal/retile/level.go`
- Test: `internal/retile/level_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/retile/level_test.go`:

```go
func TestLevelBuilderIntermediateSkipsEmitButReduces(t *testing.T) {
	// L2 (512, emit) → L1 (256, INTERMEDIATE) → L0 (128, emit); tile 256, overlap 0.
	// The middle level must enqueue ZERO encodeJobs but still feed the coarsest.
	jobs := make(chan encodeJob, 32)
	ctx := context.Background()
	coarsest := &levelBuilder{spec: LevelSpec{Index: 0, Width: 128, Height: 128, Cols: 1, Rows: 1, TileW: 256, TileH: 256}, jobs: jobs, ctx: ctx}
	mid := &levelBuilder{spec: LevelSpec{Index: -1, Width: 256, Height: 256, Cols: 1, Rows: 1, TileW: 256, TileH: 256, Intermediate: true}, child: coarsest, jobs: jobs, ctx: ctx}
	top := &levelBuilder{spec: LevelSpec{Index: 1, Width: 512, Height: 512, Cols: 2, Rows: 2, TileW: 256, TileH: 256}, child: mid, jobs: jobs, ctx: ctx}

	top.feed(makeRGB(512, 256, 1))
	top.feed(makeRGB(512, 256, 2))
	top.flush()
	close(jobs)

	counts := map[int]int{}
	for j := range jobs {
		counts[j.level]++
	}
	if counts[1] != 4 {
		t.Errorf("top (emit) tiles = %d, want 4", counts[1])
	}
	if counts[-1] != 0 {
		t.Errorf("intermediate level emitted %d tiles, want 0", counts[-1])
	}
	if counts[0] != 1 {
		t.Errorf("coarsest (emit, fed through the intermediate) tiles = %d, want 1 (chain must still run)", counts[0])
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/retile/ -run TestLevelBuilderIntermediateSkipsEmitButReduces -v`
Expected: FAIL — `unknown field 'Intermediate' in struct literal` (compile error).

- [ ] **Step 3: Add the field + gate the emits**

In `internal/retile/level.go`, add `Intermediate bool` to `LevelSpec` (with a doc comment), and gate the two `emitRow` call sites:

`LevelSpec`:
```go
type LevelSpec struct {
	Index         int
	Width, Height int // level pixel dims (Height is set for sinks — e.g. TIFF IFDs; the engine derives content height from Rows/TileH and the strip)
	Cols, Rows    int // tile grid
	TileW, TileH  int
	Overlap       int // 0 for TIFF-family/cog-wsi; 1 for DZI
	// Intermediate marks a level the descent computes ONLY to feed the box-
	// reduction chain (a non-source octave in a select-octave transcode). Its
	// strips are box-reduced to the child, but it never encodes/emits tiles.
	// Zero-value false = emit (all M1/M2/M3 callers).
	Intermediate bool
}
```

`feed` — gate the emit, keep the reduction:
```go
func (lb *levelBuilder) feed(strip *RGBImage) {
	lb.prev = lb.cur
	lb.cur = lb.next
	lb.next = strip
	if lb.cur != nil && !lb.spec.Intermediate {
		lb.emitRow(lb.rowIndex)
		lb.rowIndex++
	}
	if lb.child != nil {
		lb.child.acceptDownsampled(boxDownsample2x(strip))
	}
}
```

`flush` — gate the final-row emit, keep the cascade:
```go
	lb.prev = lb.cur
	lb.cur = lb.next
	lb.next = nil
	if lb.cur != nil && lb.rowIndex < lb.spec.Rows && !lb.spec.Intermediate {
		lb.emitRow(lb.rowIndex)
		lb.rowIndex++
	}
	if lb.child != nil {
		lb.child.flush()
	}
```

(The accum-finalize at the top of `flush` calls `lb.feed(short)`, which already carries the `!Intermediate` gate — no change needed there.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/retile/ -run TestLevelBuilderIntermediateSkipsEmitButReduces -v` → PASS.
Run: `go test -race ./internal/retile/ 2>&1 | grep -v 'duplicate librar' | tail -2` → all PASS (existing cascade/parity tests unaffected — they never set Intermediate, so default false = emit).

- [ ] **Step 5: Commit**

```bash
git add internal/retile/level.go internal/retile/level_test.go
git commit -m "feat(retile): LevelSpec.Intermediate — compute a level for reduction without encoding it"
```

---

## Task 2: `transcodeOctaveLevels` — map a power-of-2 source pyramid to select-octave levels

**Files:**
- Create: `cmd/wsitools/transcode_levels.go`
- Create: `cmd/wsitools/transcode_levels_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/transcode_levels_test.go`:

```go
package main

import "testing"

func TestTranscodeOctaveLevels_PowerOfTwo(t *testing.T) {
	// CMU-1-like: L0 46000×40000 (tile 256), L1 = /4 (octave 2), L2 = /16 (octave 4).
	src := []srcLevelDims{
		{W: 46000, H: 40000, TileW: 256, TileH: 256},
		{W: 11500, H: 10000, TileW: 256, TileH: 256},
		{W: 2875, H: 2500, TileW: 256, TileH: 256},
	}
	levels, ok := transcodeOctaveLevels(src)
	if !ok {
		t.Fatal("expected ok for power-of-2 source")
	}
	// Octaves 0..4 = 5 levels in the chain.
	if len(levels) != 5 {
		t.Fatalf("chain length = %d, want 5 (octaves 0..4)", len(levels))
	}
	// Emit at octaves 0,2,4; intermediate at 1,3.
	emit := map[int]bool{}
	for k, l := range levels {
		if l.Intermediate {
			if k != 1 && k != 3 {
				t.Errorf("octave %d marked Intermediate, expected only 1,3", k)
			}
		} else {
			emit[k] = true
		}
	}
	if !emit[0] || !emit[2] || !emit[4] {
		t.Errorf("emitted octaves = %v, want {0,2,4}", emit)
	}
	// Emit indices are contiguous 0,1,2 in finest-first order.
	if levels[0].Index != 0 || levels[2].Index != 1 || levels[4].Index != 2 {
		t.Errorf("emit indices = [%d,%d,%d], want [0,1,2]", levels[0].Index, levels[2].Index, levels[4].Index)
	}
	// Box-derived dims: octave 0 = 46000×40000; octave 2 = ceil-halve twice.
	if levels[0].Width != 46000 || levels[0].Height != 40000 {
		t.Errorf("L0 = %d×%d, want 46000×40000", levels[0].Width, levels[0].Height)
	}
	if levels[2].Width != 11500 || levels[2].Height != 10000 {
		t.Errorf("octave2 = %d×%d, want 11500×10000", levels[2].Width, levels[2].Height)
	}
	// Emitted levels carry the source tile size; emitted grid is set; intermediate dims set.
	if levels[2].TileW != 256 || levels[2].Cols != (11500+255)/256 {
		t.Errorf("octave2 tile/grid wrong: TileW=%d Cols=%d", levels[2].TileW, levels[2].Cols)
	}
}

func TestTranscodeOctaveLevels_NonPowerOfTwo(t *testing.T) {
	// Ratio 3 between L0 and L1 → not a clean octave → ok=false.
	src := []srcLevelDims{
		{W: 9000, H: 9000, TileW: 256, TileH: 256},
		{W: 3000, H: 3000, TileW: 256, TileH: 256},
	}
	if _, ok := transcodeOctaveLevels(src); ok {
		t.Error("expected ok=false for ratio-3 source")
	}
}

func TestTranscodeOctaveLevels_SingleLevel(t *testing.T) {
	src := []srcLevelDims{{W: 1000, H: 800, TileW: 256, TileH: 256}}
	levels, ok := transcodeOctaveLevels(src)
	if !ok || len(levels) != 1 || levels[0].Intermediate || levels[0].Index != 0 {
		t.Errorf("single-level: ok=%v levels=%d %+v", ok, len(levels), levels)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestTranscodeOctaveLevels -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: srcLevelDims`, `undefined: transcodeOctaveLevels`.

- [ ] **Step 3: Implement `cmd/wsitools/transcode_levels.go`**

```go
package main

import (
	"math"

	"github.com/wsilabs/wsitools/internal/retile"
)

// srcLevelDims is the minimal per-source-level geometry transcodeOctaveLevels
// needs (decoupled from source.Level for testability).
type srcLevelDims struct{ W, H, TileW, TileH int }

// transcodeOctaveLevels maps a source pyramid to the select-octave LevelSpec
// chain for a same-geometry transcode: octaves 0..D (D = the deepest source
// level's octave), box-derived dims, EMITTING only the octaves that match a
// source level (Index = emit position, source tile size) and marking the rest
// Intermediate. Returns ok=false if any source level's ratio to L0 is not a
// clean power of 2 (caller falls back to the per-level transcode path).
//
// Levels are finest-first (octave 0 = L0 = Levels[0]).
func transcodeOctaveLevels(src []srcLevelDims) ([]retile.LevelSpec, bool) {
	if len(src) == 0 {
		return nil, false
	}
	l0 := src[0]
	// octaveOf[k] = the source level (by index into src) that sits at octave k.
	octaveOf := map[int]srcLevelDims{}
	deepest := 0
	for _, s := range src {
		if s.W <= 0 || l0.W <= 0 || s.TileW <= 0 || s.TileH <= 0 {
			return nil, false // zero/degenerate geometry → fall back to per-level
		}
		ratio := float64(l0.W) / float64(s.W)
		k := int(math.Round(math.Log2(ratio)))
		if k < 0 {
			return nil, false
		}
		// Verify the ratio is a clean power of 2 in BOTH dimensions: box-halving
		// L0 k times must reproduce the source dims (±0; the source level dim is
		// the authority for the match test, output uses box-derived below).
		if ceilHalve(l0.W, k) != s.W || ceilHalve(l0.H, k) != s.H {
			return nil, false
		}
		if _, dup := octaveOf[k]; dup {
			return nil, false // two source levels at the same octave — malformed
		}
		octaveOf[k] = s
		if k > deepest {
			deepest = k
		}
	}

	levels := make([]retile.LevelSpec, 0, deepest+1)
	emitIdx := 0
	for k := 0; k <= deepest; k++ {
		w := ceilHalve(l0.W, k)
		h := ceilHalve(l0.H, k)
		if s, isEmit := octaveOf[k]; isEmit {
			cols := (w + s.TileW - 1) / s.TileW
			rows := (h + s.TileH - 1) / s.TileH
			levels = append(levels, retile.LevelSpec{
				Index: emitIdx, Width: w, Height: h,
				Cols: cols, Rows: rows, TileW: s.TileW, TileH: s.TileH,
				Overlap: 0, Intermediate: false,
			})
			emitIdx++
		} else {
			// Intermediate: computed for reduction only. Index/Cols/Rows unused;
			// TileH governs the internal accumulator strip height.
			levels = append(levels, retile.LevelSpec{
				Index: -1, Width: w, Height: h,
				Cols: 0, Rows: 0, TileW: 256, TileH: 256,
				Overlap: 0, Intermediate: true,
			})
		}
	}
	return levels, true
}

// ceilHalve halves v (ceil) n times: ceilHalve(v,0)=v.
func ceilHalve(v, n int) int {
	for i := 0; i < n; i++ {
		v = (v + 1) / 2
	}
	return v
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestTranscodeOctaveLevels -v 2>&1 | grep -v 'duplicate librar'` → all 3 PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/transcode_levels.go cmd/wsitools/transcode_levels_test.go
git commit -m "feat(convert): transcodeOctaveLevels — select-octave mapping for transcode"
```

---

## Task 3: `convertTranscodeTIFF` + routing

**Files:**
- Modify: `cmd/wsitools/convert_stitched.go` (add `convertTranscodeTIFF`)
- Modify: `cmd/wsitools/convert_tiff.go` (route the non-overlapping branch)
- Test: controller-run integration

- [ ] **Step 1: Add `convertTranscodeTIFF` to `cmd/wsitools/convert_stitched.go`**

It mirrors `convertStitchedTIFF` but (a) takes the precomputed select-octave `levels`, (b) AddLevels handles ONLY for emitted levels (indexed by emit position), (c) passes the FULL `levels` chain to `retile.Run`, (d) identity `OutL0` = source L0.

```go
// convertTranscodeTIFF re-encodes a non-overlapping source to a new codec while
// preserving its pyramid structure (select-octave): the engine decodes L0 once,
// box-derives the octave chain, and encodes ONLY the octaves matching a source
// level (the Intermediate ones feed reduction). `levels` is the full octave chain
// from transcodeOctaveLevels (finest-first); emitted levels carry contiguous
// Index 0..M-1 + their source tile size.
func convertTranscodeTIFF(ctx context.Context, slide *opentile.Slide, src source.Source, w *streamwriter.Writer, container, srcImageDesc string, plan omeEditPlan, omeSynthetic bool, workers int, fac codec.EncoderFactory, knobs map[string]string, levels []retile.LevelSpec) error {
	l0 := slide.Pyramid(0).Levels[0]

	enc, err := fac.NewEncoder(codec.LevelGeometry{TileWidth: levels[0].TileW, TileHeight: levels[0].TileH, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: knobs})
	if err != nil {
		return err
	}
	defer enc.Close()

	// Collect emitted levels (finest-first); their Index is the emit position.
	var emitted []retile.LevelSpec
	for _, ls := range levels {
		if !ls.Intermediate {
			emitted = append(emitted, ls)
		}
	}

	swSpec := func(ls retile.LevelSpec) streamwriter.LevelSpec {
		spec := streamwriter.LevelSpec{
			ImageWidth: uint32(ls.Width), ImageHeight: uint32(ls.Height),
			TileWidth: uint32(ls.TileW), TileHeight: uint32(ls.TileH),
			Compression: enc.TIFFCompressionTag(), Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			JPEGTables:     enc.LevelHeader(),
			NewSubfileType: newSubfileTypeForLevel(ls.Index, container),
			WSIImageType:   tiff.WSIImageTypePyramid,
		}
		if ls.Index == 0 && srcImageDesc != "" && (container == "svs" || container == "ome-tiff") {
			spec.ExtraTags = buildL0ImageDescriptionTag(srcImageDesc)
		}
		return spec
	}

	handles := make([]*streamwriter.LevelHandle, len(emitted))
	h0, err := w.AddLevel(swSpec(emitted[0]))
	if err != nil {
		return fmt.Errorf("add level 0: %w", err)
	}
	handles[0] = h0
	// SVS thumbnail at IFD 1 (no-op unless container==svs) — between L0 and L1.
	if _, err := emitSVSThumbnailAtL0(src, w, 0, container, omeSynthetic, plan); err != nil {
		return err
	}
	for e := 1; e < len(emitted); e++ {
		h, err := w.AddLevel(swSpec(emitted[e]))
		if err != nil {
			return fmt.Errorf("add level %d: %w", e, err)
		}
		handles[e] = h
	}

	sink := newStreamwriterSink(handles)
	runErr := retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: l0.Size},
		OutL0:     l0.Size, // identity: transcode is same geometry
		Levels:    levels,  // FULL octave chain (emit + intermediate)
		Kernel:    resample.Nearest,
		Encoder:   &codecTileEncoder{enc: enc},
		Sink:      sink,
		Workers:   workers,
	})
	if ferr := sink.finish(); ferr != nil && runErr == nil {
		runErr = ferr
	}
	if runErr != nil {
		return runErr
	}
	return writeAssociatedImages(src, w, container, omeSynthetic, plan)
}

// srcLevelDimsFromSlide extracts srcLevelDims from the opentile slide levels.
func srcLevelDimsFromSlide(slide *opentile.Slide) []srcLevelDims {
	lv := slide.Pyramid(0).Levels
	out := make([]srcLevelDims, len(lv))
	for i, l := range lv {
		out[i] = srcLevelDims{W: l.Size.W, H: l.Size.H, TileW: l.TileSize.W, TileH: l.TileSize.H}
	}
	return out
}
```

VERIFY: `slide.Pyramid(0).Levels[i].TileSize` is `opentile.Size{W,H}`. Zero/degenerate tile geometry is already handled in Task 2 (`transcodeOctaveLevels` returns ok=false when any `s.TileW/TileH ≤ 0`), so such sources fall back to `transcodePyramid`.

- [ ] **Step 2: Route the non-overlapping branch in `runConvertTIFFReencode`**

In `cmd/wsitools/convert_tiff.go`, the branch at ~line 347 currently is:
```go
	if sourceIsOverlapping(src) {
		if err := convertStitchedTIFF(...); err != nil { ... }
	} else {
		if err := transcodePyramid(...); err != nil { ... }
		if !cvNoAssociated { writeAssociatedImages(...) }
	}
```
Insert the select-octave branch between the overlapping and the fallback (match the EXISTING error-handling — `w.Abort()` + return, per M2/M3):
```go
	if sourceIsOverlapping(src) {
		// ... existing convertStitchedTIFF call (unchanged) ...
	} else if levels, ok := transcodeOctaveLevels(srcLevelDimsFromSlide(slide)); ok {
		// Select-octave transcode through the engine (emits associated itself).
		if err := convertTranscodeTIFF(cmd.Context(), slide, src, w, resolvedContainer, srcImageDesc, omeEditPlan{dropAll: cvNoAssociated}, omeSynthetic, workers, fac, knobs, levels); err != nil {
			w.Abort()
			return err
		}
	} else {
		// Fallback: per-level transcode (exotic non-power-of-2 source). UNCHANGED.
		if err := transcodePyramid(...); err != nil { ... }
		if !cvNoAssociated { writeAssociatedImages(...) }
	}
```
Read the real code around line 347-365 and preserve its EXACT variable names + error handling (`w.Abort()`/return, the `cvNoAssociated` gate on the fallback's writeAssociatedImages). The overlapping arm and the fallback arm must be byte-for-byte unchanged; only the new `else if` is inserted. `convertTranscodeTIFF` calls `writeAssociatedImages` itself, so do NOT add a separate associated call in the new arm.

- [ ] **Step 3: Build + unit suite**

Run: `go build ./... 2>&1 | grep -v 'duplicate librar'` → clean.
Run: `go test ./cmd/wsitools/ -run 'TestTranscodeOctaveLevels|TestCountingSink|TestFlooredLevelCount|TestCodecTileEncoder|TestCogwsiReorder|TestStreamwriterSink' 2>&1 | grep -v 'duplicate librar' | tail -3` → PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/convert_stitched.go cmd/wsitools/convert_tiff.go
git commit -m "feat(convert): route non-overlapping transcode through the engine (select-octave)"
```

- [ ] **Step 5: CONTROLLER integration (transcode → structure preserved + codec changed)**

```bash
make build
FIX=$(pwd)/sample_files/svs/CMU-1.svs   # 3-level, 4×,4× (non-octave power-of-2)
echo "=== source structure ==="; ./bin/wsitools info "$FIX" 2>&1 | grep -v 'duplicate librar' | grep -E 'L[0-9]| MPP|Magnif'
echo "=== transcode jpeg→jpeg2000 ==="
./bin/wsitools convert "$FIX" --to svs --codec jpeg2000 -o /tmp/m4-jp2k.svs -f 2>&1 | grep -v 'duplicate librar'
./bin/wsitools info /tmp/m4-jp2k.svs 2>&1 | grep -v 'duplicate librar' | grep -E 'L[0-9]| MPP|Magnif|jpeg2000|thumbnail'
./bin/wsitools validate /tmp/m4-jp2k.svs 2>&1 | grep -v 'duplicate librar' | tail -1
echo "=== transcode jpeg→jpeg (same family, re-encode) ==="
./bin/wsitools convert "$FIX" --to svs --codec jpeg -o /tmp/m4-jpg.svs -f 2>&1 | grep -v 'duplicate librar'
./bin/wsitools info /tmp/m4-jpg.svs 2>&1 | grep -v 'duplicate librar' | grep -E 'L[0-9]'
```
Expected: output level COUNT == source count (3), RATIOS == source (4×,4×), dims == source (or ±1px); L0 compression == jpeg2000 (codec changed); thumbnail at IFD 1; MPP/mag preserved; validate clean. The level structure must MATCH the source (NOT octave-floored to 5 levels).

---

## Task 4: Verification — pixel-equivalence + fallback + race (controller)

**Files:** none (verification only).

- [ ] **Step 1: Structure-preservation assertion (vs source)**

```bash
FIX=$(pwd)/sample_files/svs/CMU-1.svs
echo "source levels:"; ./bin/wsitools info "$FIX" 2>&1 | grep -E '  L[0-9]+ ' | awk '{print $1,$2"x"$3}'
echo "transcoded levels:"; ./bin/wsitools info /tmp/m4-jp2k.svs 2>&1 | grep -E '  L[0-9]+ ' | awk '{print $1,$2"x"$3}'
```
Expected: same number of levels, dims equal (or ±1px on odd levels), L0 identical. NOT a 5-level octave pyramid.

- [ ] **Step 2: Pixel-equivalence (L0)**

```bash
for s in "$FIX:src" "/tmp/m4-jpg.svs:new"; do f=${s%%:*}; n=${s##*:}; ./bin/wsitools region "$f" --rect 5000,5000,512,512 --level 0 -o /tmp/m4-$n.png -f 2>&1 | grep -v 'duplicate librar'; done
python3 - <<'PY' 2>/dev/null || echo "(PIL unavailable)"
from PIL import Image, ImageChops, ImageStat
a=Image.open("/tmp/m4-src.png").convert("RGB"); b=Image.open("/tmp/m4-new.png").convert("RGB")
print("L0 mean abs diff:", [round(x,2) for x in ImageStat.Stat(ImageChops.difference(a,b)).mean])
PY
```
Expected: small mean (jpeg→jpeg round-trip noise on L0; the engine reads L0 at identity, so L0 ≈ source re-encoded).

- [ ] **Step 3: Fallback path (non-power-of-2 source, if one exists)**

Most fixtures are power-of-2. If a non-power-of-2 source is available, transcode it and confirm it succeeds (via `transcodePyramid` fallback) + validates. If none exists, the `TestTranscodeOctaveLevels_NonPowerOfTwo` unit test covers the `ok=false` branch; note that the controller fallback path is exercised only by unit test absent a fixture.

- [ ] **Step 4: Full race suite**

```bash
WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -race -count=1 -timeout 30m ./internal/retile/ ./cmd/wsitools/ 2>&1 | grep -v 'duplicate librar' | tail -4
```
Expected: all PASS.

---

## Final verification + finish

Dispatch a final code reviewer over `main..HEAD`, then use **superpowers:finishing-a-development-branch**. Branch: `feat/retile-engine-m4` off `main`.

**M4 acceptance:**
- `retile.LevelSpec.Intermediate` gates emit in both `feed` and `flush`; box-reduction cascades; existing engine tests unaffected.
- Non-overlapping TIFF-family transcode routes through the engine (select-octave); output level count + ratios == source (dims ±1px); codec changed; metadata + associated + SVS thumbnail-at-IFD-1 preserved; validate clean.
- Non-power-of-2 (or zero-tile) source falls back to `transcodePyramid`.
- Full `-race` green.

---

## Self-Review

**Spec coverage:**
- Select-octave levels (emit at source octaves, box-derived dims, source tile size, Intermediate elsewhere) → Task 2 (`transcodeOctaveLevels`). ✓
- `LevelSpec.Intermediate` gating emitRow in feed+flush, box-reduce always cascades → Task 1. ✓
- `convertTranscodeTIFF` reuses M2 machinery (encoder/sink/shaping/thumbnail/associated/srcImageDesc), full chain to Run, handles for emitted only → Task 3. ✓
- Routing: overlapping→M2, ok→M4, else→transcodePyramid fallback → Task 3 Step 2. ✓
- Testing: structure preservation, codec change, pixel-equiv, fallback, Intermediate unit, octave-mapping unit → Tasks 1/2/4. ✓

**Placeholder scan:** none. The "read the real code around line 347 and match names/error-handling" and the zero-tile guard "add if not covered" are explicit verification steps, not placeholders.

**Type consistency:** `srcLevelDims{W,H,TileW,TileH}`, `transcodeOctaveLevels([]srcLevelDims) ([]retile.LevelSpec, bool)`, `retile.LevelSpec.Intermediate`, `convertTranscodeTIFF(..., levels []retile.LevelSpec)`, `srcLevelDimsFromSlide`, `ceilHalve` — consistent across tasks; `convertTranscodeTIFF` mirrors `convertStitchedTIFF`'s signature + the M2 sink/encoder names. The engine's `emitRow` reads `lb.spec.Index` (emit index for emitted levels). ✓

**Risk:** the ±1px box-derived-vs-source dim on odd levels (accepted in the design). And the `Intermediate` field defaulting false — verified backward-compatible (all existing specs come from ComputeLevels/octaveLevelSpecsFor, which don't set it; hand-built test specs default false = emit). Flagged in Task 1 Step 4.
