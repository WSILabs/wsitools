# Unified-Transform `convert` — Plan 1 (engine convergence + `convert --rect`)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Converge the duplicate `downsampleTo*` (`convert_factor.go`) and `cropTo*` (`crop_formats.go`) per-format emitters into one shared `transformTo*` family fed by a shared `MaterializeWorkingL0` front-end, and wire `convert --to X --rect … [--factor N]` so crop + downsample + container fuse into **one decode→rebuild pass**.

**Architecture:** A `transformParams` struct carries an already-materialized working L0 raster (cropped and/or reduced) + final metadata + flags (lossless, regenThumb, resampled). Each `transformTo<F>(p transformParams)` rebuilds the pyramid in its container — the body is today's `cropTo<F>` generalized so metadata comes from `p` (not recomputed) and thumbnail-regen is conditional. The three front-ends (`runCrop`, `runConvertFactor`/downsample, and the new `convert --rect` path) each materialize their working L0 + compute their nLevels/metadata, then call the *same* emitter. This is **Plan 1 of 2**; Plan 2 adds `transcode`, `--to`-defaults-to-source, the conformance-validation layer, arbitrary `--codec` on the transform path, and the lossless-auto-passthrough generalization (see the spec).

**Tech Stack:** Go; `internal/downscale` (raster ops); `internal/tiff/streamwriter` + `cogwsiwriter` + `internal/derivedsource`+`dicomwriter` (rebuild); cobra CLI.

**Spec:** `docs/superpowers/specs/2026-06-14-unified-transform-convert-design.md`

**Scope note (Plan 1):** codec on the transform path stays as today (JPEG re-encode for re-encoded levels; verbatim tiles for `crop --lossless`). Arbitrary `--codec` with `--rect`/`--factor`, the `transcode` alias, `--to`-optional, and the full conformance validator are **Plan 2**. `--rect` on `convert` therefore still **requires `--to`** in Plan 1.

---

## File structure

| File | Responsibility | Action |
|---|---|---|
| `internal/downscale/transform.go` | `MaterializeWorkingL0` — crop∘downsample the source L0 into the working raster | Create |
| `internal/downscale/transform_test.go` | unit test for the composition | Create |
| `cmd/wsitools/transform.go` | `transformParams` struct + `dispatchTransform` + shared helpers (moved from crop_formats.go) | Create |
| `cmd/wsitools/crop_formats.go` | `cropTo*` → `transformTo*` (generalized); `cropEmitParams` → `transformParams` | Modify |
| `cmd/wsitools/crop.go` | `runCrop` populates `transformParams` (new metadata/flag fields) + calls `dispatchTransform` | Modify |
| `cmd/wsitools/convert_factor.go` | `downsampleTo*` deleted; `dispatchDownsampleByTarget` builds `transformParams` + calls `dispatchTransform` | Modify |
| `cmd/wsitools/convert.go` | `runConvert` accepts `--rect`; routes crop[/+factor] to a new `runConvertTransform` | Modify |
| `tests/integration/transform_test.go` | composition matrix, alias parity, one-pass equivalence, lossless oracle | Create |
| `README.md`, `CHANGELOG.md` | `convert --rect` docs | Modify |

---

## Task 1: `downscale.MaterializeWorkingL0`

**Files:**
- Create: `internal/downscale/transform.go`
- Create: `internal/downscale/transform_test.go`

`MaterializeWorkingL0` composes the two existing front-ends: it crops the source L0 to a rect (or the whole L0), then box-reduces by `factor`. It reuses `MaterializeCroppedL0` (which already decodes the source L0 region into an RGB raster) and `BoxHalve` (factor>1).

- [ ] **Step 1: Write the failing test**

Create `internal/downscale/transform_test.go`:

```go
package downscale

import (
	"context"
	"testing"
)

// MaterializeWorkingL0 with no source slide is hard to unit-test directly, so we
// test the pure compose logic via the exported helper on a synthetic raster:
// crop a region of a known raster, then box-reduce, and check dims + a pixel.
func TestComposeCropReduce(t *testing.T) {
	// 8x8 raster, R channel = x, so we can verify the crop offset survives.
	w, h := 8, 8
	raster := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := (y*w + x) * 3
			raster[o] = byte(x * 16)
		}
	}
	// Crop the right half (x∈[4,8), y∈[0,8)) → 4x8, then reduce by 2 → 2x4.
	cropped := make([]byte, 4*8*3)
	PasteSubRect(cropped, 4, 8, 0, 0, raster, w, 4, 0, 4, 8)
	out, ow, oh, err := BoxHalve(cropped, 4, 8, 2)
	if err != nil {
		t.Fatalf("BoxHalve: %v", err)
	}
	if ow != 2 || oh != 4 {
		t.Fatalf("reduced dims = %dx%d, want 2x4", ow, oh)
	}
	// Top-left reduced pixel averages source x∈{4,5} → R≈(64+80)/2=72.
	if r := out[0]; r < 60 || r > 84 {
		t.Errorf("reduced[0].R = %d, want ~72 (crop offset preserved)", r)
	}
	_ = context.Background()
}
```

(This validates the crop-then-reduce *composition math* with the existing `PasteSubRect`/`BoxHalve` primitives, which is what `MaterializeWorkingL0` wires to a real slide.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/downscale/ -run TestComposeCropReduce -count=1`
Expected: PASS or FAIL depending only on the math — if it FAILs, the helper signatures differ; read `internal/downscale/crop.go` + `downscale.go` and fix the test to match `PasteSubRect`/`BoxHalve`'s real signatures before proceeding. (This step exists to lock the primitives the implementation uses.)

- [ ] **Step 3: Implement `MaterializeWorkingL0`**

Create `internal/downscale/transform.go`:

```go
package downscale

import (
	"context"
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
)

// MaterializeWorkingL0 decodes the source level-0 into the working RGB888 raster
// for a transform: it crops to (cropX,cropY,cropW,cropH) — pass the full level
// dims for "no crop" — then box-reduces by factor (1 = no reduction). Returns
// the raster and its dimensions. This is the shared front-end for crop,
// downsample, and combined crop+downsample (convert --rect [--factor]).
func MaterializeWorkingL0(ctx context.Context, srcL0 *opentile.Level, cropX, cropY, cropW, cropH, factor int) (raster []byte, outW, outH int, err error) {
	if cropW <= 0 || cropH <= 0 {
		return nil, 0, 0, fmt.Errorf("degenerate crop %dx%d", cropW, cropH)
	}
	cropBytes := int64(cropW) * int64(cropH) * 3
	if cropBytes < 0 {
		return nil, 0, 0, fmt.Errorf("crop raster size overflows int64")
	}
	cropped := make([]byte, cropBytes)
	if err := MaterializeCroppedL0(ctx, srcL0, cropped, cropX, cropY, cropW, cropH); err != nil {
		return nil, 0, 0, fmt.Errorf("materialize cropped L0: %w", err)
	}
	if factor <= 1 {
		return cropped, cropW, cropH, nil
	}
	reduced, rw, rh, err := BoxHalve(cropped, cropW, cropH, factor)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("box-reduce by %d: %w", factor, err)
	}
	return reduced, rw, rh, nil
}
```

- [ ] **Step 4: Run tests + build**

Run: `go test ./internal/downscale/ -count=1 && go build ./...`
Expected: PASS; build clean (ignore the `ld: warning` linker noise).

- [ ] **Step 5: Commit**

```bash
git add internal/downscale/transform.go internal/downscale/transform_test.go
git commit -m "feat(downscale): MaterializeWorkingL0 (crop∘downsample front-end)"
```

---

## Task 2: `transformParams` struct + rename `cropEmitParams`

**Files:**
- Create: `cmd/wsitools/transform.go`
- Modify: `cmd/wsitools/crop_formats.go` (remove the `cropEmitParams` type def — moves to transform.go)
- Modify: `cmd/wsitools/crop.go` (the `cropEmitParams{…}` literal in `runCrop`)

Rename `cropEmitParams` → `transformParams` and add the fields the converged emitters need from the front-end: final metadata (`mppX,mppY,mag`), `resampled` (DICOM ImageType), and `regenThumb` (regenerate vs pass through the thumbnail).

- [ ] **Step 1: Create `cmd/wsitools/transform.go` with the struct + dispatch**

```go
package main

import (
	"context"
	"fmt"
	"time"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// transformParams carries an already-materialized working L0 raster (cropped
// and/or downsampled by the front-end) plus the metadata and flags the per-
// format transformTo* emitters need to rebuild the output pyramid. It is the
// shared currency of the crop, downsample, and convert --rect front-ends.
type transformParams struct {
	ctx          context.Context
	src          *opentile.Slide
	srcL0        *opentile.Level
	input        string
	output       string
	l0           []byte // working L0 raster (RGB888)
	l0W, l0H     int
	nLevels      int
	quality      int
	workers      int
	order        tileorder.OrderStrategy
	bigtiffFlag  string
	noAssociated bool

	// Lossless verbatim L0 (crop --lossless only). When lossless, l0 is the
	// snapped-region raster and srcL0/stx0/sty0/outTilesX/outTilesY drive the
	// verbatim tile copy.
	lossless             bool
	stx0, sty0           int
	outTilesX, outTilesY int

	// Final output metadata (front-end computes: crop preserves source MPP/mag;
	// downsample scales MPP ×factor / mag ÷factor).
	mppX, mppY, mag float64
	// resampled is true when a downsample factor>1 was applied (DICOM ImageType
	// RESAMPLED vs NONE). regenThumb is true when a crop rect was applied (regen
	// the thumbnail from the crop) vs false for a pure downsample (pass through).
	resampled  bool
	regenThumb bool

	start time.Time
}

// dispatchTransform rebuilds the working L0 into the target container.
func dispatchTransform(target string, p transformParams) error {
	switch target {
	case "svs":
		return transformToSVS(p)
	case "tiff":
		return transformToTIFF(p)
	case "ome-tiff":
		return transformToOMETIFF(p)
	case "cog-wsi":
		return transformToCOGWSI(p)
	case "dicom":
		return transformToDICOM(p)
	default:
		return fmt.Errorf("transform: target %q not implemented", target)
	}
}
```

- [ ] **Step 2: Remove the old `cropEmitParams` type from `crop_formats.go`**

Delete the `type cropEmitParams struct { … }` definition in `cmd/wsitools/crop_formats.go` (it now lives in transform.go as `transformParams`). Leave the `cropTo*` functions for now (Tasks 3–7 rename + generalize them).

- [ ] **Step 3: Update `runCrop` to build `transformParams`**

In `cmd/wsitools/crop.go`, the `runCrop` function builds a `cropEmitParams{…}` literal and dispatches via `switch target { case "tiff": return cropToTIFF(p) … }`. Replace the literal type name with `transformParams` and add the new fields. The crop metadata comes from `cropSourceScale(input, src)` (crop preserves source scale), `resampled` is `false` (crop doesn't downsample), and `regenThumb` is `true` (crop always regenerates its thumbnail):

```go
	mppX, mppY, mag := cropSourceScale(input, src)
	p := transformParams{
		ctx: ctx, src: src, srcL0: srcL0, input: input, output: output,
		l0: outL0, l0W: ew, l0H: eh, nLevels: nLevels, quality: q, workers: workers,
		order: order, bigtiffFlag: bigtiffFlag, noAssociated: noAssociated,
		lossless: lossless, stx0: stx0, sty0: sty0, outTilesX: outTilesX, outTilesY: outTilesY,
		mppX: mppX, mppY: mppY, mag: mag, resampled: false, regenThumb: true,
		start: start,
	}
	return dispatchTransform(target, p)
```

(Read the current `runCrop` `p := cropEmitParams{…}` + `switch target` block and replace both with the above. The `cropEmitSVS` special-case for the SVS target is handled in Task 3.)

- [ ] **Step 4: Build (will fail until Tasks 3–7 rename the emitters)**

Run: `go build ./cmd/wsitools 2>&1 | head`
Expected: compile errors referencing `cropToTIFF`/`transformToTIFF` etc. — that's fine; the emitter rename happens next. Confirm the errors are *only* the missing `transformTo*` functions (not a `transformParams` field typo).

- [ ] **Step 5: Commit (WIP — compiles after Task 7)**

Do **not** commit a non-compiling tree. Instead, proceed through Tasks 3–7 and commit the struct + the first emitter together. (Mark this task done once Task 3 compiles.) If your workflow requires a commit per task, combine Tasks 2+3 into one commit.

---

## Task 3: `transformToSVS` (+ fold in `cropEmitSVS` / `downsampleToSVS`)

**Files:**
- Modify: `cmd/wsitools/crop_formats.go` (or move SVS emitter to transform.go)
- Modify: `cmd/wsitools/crop.go` (SVS crop currently routes to `cropEmitSVS`)
- Modify: `cmd/wsitools/convert_factor.go` (`downsampleToSVS` → route through `transformToSVS`)

SVS is the most involved: the current `cropEmitSVS` (in crop.go) takes explicit args (not the params struct) and `downsampleToSVS` builds the Aperio `ImageDescription`. The converged `transformToSVS(p transformParams)` rebuilds an SVS from `p.l0`, mutating/synthesizing the Aperio description for the working dims + `p.mag`/`p.mppX`.

- [ ] **Step 1: Read the three current SVS paths**

Read `cropEmitSVS` (`cmd/wsitools/crop.go`), `downsampleToSVS` (`cmd/wsitools/convert_factor.go`), and `cropToTIFF` (`cmd/wsitools/crop_formats.go`, the params-struct template). `transformToSVS` = the `cropToTIFF` *structure* (params-struct, lossless branch, conditional thumbnail) with SVS specifics: the Aperio `ImageDescription` (mutated from source for SVS sources via `desc.MutateForDownsample` when downsampling, else `SyntheticAperioDescription`/`BuildCropImageDescription`), `FormatName: "svs"`, and `ImageDepth: 1` + `YCbCrSubSampling` L0 tags.

- [ ] **Step 2: Write `transformToSVS`**

Add `transformToSVS(p transformParams) error` modeled on `cropToTIFF` (Task 4's template) but emitting SVS. Key SVS-specific points to carry over from the existing SVS emitters:
- ImageDescription: for a crop (regenThumb && !resampled) reuse `cropEmitSVS`'s `BuildCropImageDescription`; for a downsample (resampled) reuse `downsampleToSVS`'s mutate/synthesize path; both feed `streamwriter.Options.ImageDescription`. Compute it from `p.src`'s source description + `p.l0W/p.l0H` + `p.mag`/`p.mppX`.
- `streamwriter.Options{FormatName: "svs", AcceptedOrders: acceptedOrdersForFormat("svs"), MPPX: p.mppX, MPPY: p.mppY, Magnification: p.mag, ICCProfile: p.src.ICCProfile(), ImageDepth: 1, …}` plus the JPEG `YCbCrSubSampling` L0 tag when the source L0 is JPEG (mirror `convert_tiff.go:166-178`).
- Pyramid rebuild + associated: identical to `cropToTIFF` (lossless branch via `writeLosslessL0`; else `buildPyramidFromRaster`; thumbnail conditional on `p.regenThumb`).

Because this function is long and SVS-specific, copy `cropEmitSVS`'s SVS setup verbatim into the new structure rather than re-deriving it. Provide the complete function (no abbreviation) — the implementer should produce a full `transformToSVS` that compiles.

- [ ] **Step 3: Route the SVS front-ends through it**

- `runCrop`: the `if target == "svs" { return cropEmitSVS(…) }` branch becomes part of the unified `dispatchTransform` (SVS is now a normal `case "svs"`). Delete the special-case; `runCrop` already builds `transformParams` (Task 2) and calls `dispatchTransform`, which now has `transformToSVS`.
- `downsampleToSVS`: delete it; in `dispatchDownsampleByTarget` the `case "svs"` builds `transformParams` (see Task 8) and calls `transformToSVS`. (Task 8 wires this; for now just ensure `transformToSVS` exists and `cropEmitSVS` is removed/inlined.)

- [ ] **Step 4: Build + the SVS crop/downsample integration tests**

Run:
```
go build ./... && \
WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./cmd/wsitools/ -run 'Crop|Downsample|ConvertFactor' -count=1 2>&1 | grep -vE 'ld: warning' | tail
```
Expected: the existing SVS crop + downsample + convert-factor-SVS tests still PASS (behavior-preserving). If `TestConvertFactorSVSParity` (pixel parity vs standalone downsample) fails, the SVS metadata/level-count path drifted — reconcile `transformToSVS`'s ImageDescription + nLevels with the old `downsampleToSVS`.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/transform.go cmd/wsitools/crop_formats.go cmd/wsitools/crop.go cmd/wsitools/convert_factor.go
git commit -m "refactor(transform): transformParams + transformToSVS (fold cropEmitSVS + downsampleToSVS)"
```

---

## Task 4: `transformToTIFF`

**Files:**
- Modify: `cmd/wsitools/crop_formats.go`
- Modify: `cmd/wsitools/convert_factor.go` (`downsampleToTIFF` → route through it)

- [ ] **Step 1: Generalize `cropToTIFF` → `transformToTIFF`**

Rename `cropToTIFF(p cropEmitParams)` → `transformToTIFF(p transformParams)` and make two changes to its body:

(a) Replace the metadata line:
```go
	mppX, mppY, mag := cropSourceScale(p.input, p.src)
```
with the params-supplied metadata:
```go
	mppX, mppY, mag := p.mppX, p.mppY, p.mag
```

(b) Make the thumbnail conditional on `p.regenThumb` (crop regenerates; downsample passes through). The associated-images loop becomes:
```go
	if !p.noAssociated {
		for _, a := range p.src.AssociatedImages() {
			if p.regenThumb && a.Type() == opentile.AssociatedThumbnail {
				if err := regenCropThumbnail(w, p.l0, p.l0W, p.l0H, p.quality); err != nil {
					return fmt.Errorf("regenerate thumbnail: %w", err)
				}
				continue
			}
			if err := writeOneAssociated(w, a); err != nil {
				return fmt.Errorf("write associated %s: %w", a.Type(), err)
			}
		}
	}
```

Everything else (writer Options, lossless branch, `buildPyramidFromRaster`) is unchanged. The `imageDesc` provenance string already reads `p.src.Format()`/`mag`/`mppX` — leave it.

- [ ] **Step 2: Delete `downsampleToTIFF`, route through `transformToTIFF`**

In `convert_factor.go`, delete `downsampleToTIFF`. (Task 8 makes `dispatchDownsampleByTarget`'s `case "tiff"` build `transformParams` + call `transformToTIFF`.)

- [ ] **Step 3: Build + tests**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./cmd/wsitools/ -run 'ConvertFactorTIFF|Crop' -count=1 2>&1 | grep -vE 'ld: warning' | tail`
Expected: PASS — TIFF downsample (`TestConvertFactorTIFFTargets`) + TIFF crop unchanged.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/crop_formats.go cmd/wsitools/convert_factor.go
git commit -m "refactor(transform): transformToTIFF (fold cropToTIFF + downsampleToTIFF)"
```

---

## Task 5: `transformToOMETIFF`

**Files:**
- Modify: `cmd/wsitools/crop_formats.go`
- Modify: `cmd/wsitools/convert_factor.go` (`downsampleToOMETIFF` → route through it)

- [ ] **Step 1: Generalize `cropToOMETIFF` → `transformToOMETIFF`**

Rename `cropToOMETIFF(p cropEmitParams)` → `transformToOMETIFF(p transformParams)`. Same two edits as Task 4:

(a) `mppX, mppY, mag := cropSourceScale(p.input, p.src)` → `mppX, mppY, mag := p.mppX, p.mppY, p.mag`.

(b) In the OME associated loop, gate the thumbnail regen on `p.regenThumb`:
```go
			if p.regenThumb && a.Type() == opentile.AssociatedThumbnail {
				if err := regenCropThumbnail(w, p.l0, p.l0W, p.l0H, p.quality); err != nil {
					return fmt.Errorf("regenerate thumbnail: %w", err)
				}
				continue
			}
```

**Caveat (read carefully):** the OME emitter pre-computes `omeAssocs` (the OME-XML Image list) and overrides the thumbnail dims to `thumbDims(p.l0W,p.l0H,…)` so the XML matches the written IFD. That override is only correct when the thumbnail is regenerated. Guard it the same way: only substitute the regenerated dims `if p.regenThumb && a.Type() == opentile.AssociatedThumbnail`; otherwise use the source thumbnail's real `a.Size()`. Update the `omeAssocs` construction loop accordingly.

- [ ] **Step 2: Delete `downsampleToOMETIFF`, route through `transformToOMETIFF`** (Task 8 wires the dispatch).

- [ ] **Step 3: Build + tests**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./cmd/wsitools/ -run 'OMETIFF|Crop' -count=1 2>&1 | grep -vE 'ld: warning' | tail`
Expected: PASS — OME downsample (`TestConvertFactorOMETIFF`/`TestDownsamplePreservesOMETIFF`) + OME crop unchanged.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/crop_formats.go cmd/wsitools/convert_factor.go
git commit -m "refactor(transform): transformToOMETIFF (fold cropToOMETIFF + downsampleToOMETIFF)"
```

---

## Task 6: `transformToCOGWSI`

**Files:**
- Modify: `cmd/wsitools/crop_formats.go`
- Modify: `cmd/wsitools/convert_factor.go` (`downsampleToCOGWSI` → route through it)

- [ ] **Step 1: Generalize `cropToCOGWSI` → `transformToCOGWSI`**

Read the current `cropToCOGWSI` (it uses `cogwsiwriter.Create` + `buildPyramidFromRasterCOGWSI` + `faithfulCOGWSISpecOT` for associated). Rename to `transformToCOGWSI(p transformParams)` and apply the same two changes: metadata from `p` (the cog-wsi emitter sets `Metadata{MPPX,MPPY,Magnification,…}` — source these from `p.mppX/p.mppY/p.mag`); and gate the thumbnail regen (`regenCropThumbnailCOGWSI`) on `p.regenThumb`.

- [ ] **Step 2: Delete `downsampleToCOGWSI`, route through `transformToCOGWSI`** (Task 8 wires the dispatch).

- [ ] **Step 3: Build + tests**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./cmd/wsitools/ -run 'COGWSI|cog' -count=1 2>&1 | grep -vE 'ld: warning' | tail`
Expected: PASS — cog-wsi downsample (`TestConvertFactorTIFFTargets/cog-wsi`, `TestDownsamplePreservesCOGWSI`) + cog-wsi crop unchanged.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/crop_formats.go cmd/wsitools/convert_factor.go
git commit -m "refactor(transform): transformToCOGWSI (fold cropToCOGWSI + downsampleToCOGWSI)"
```

---

## Task 7: `transformToDICOM`

**Files:**
- Modify: `cmd/wsitools/crop_formats.go`
- Modify: `cmd/wsitools/convert_factor.go` (`downsampleToDICOM` → route through it)

The DICOM emitter is special: it builds a `derivedsource` (raster levels) and chooses `L0ImageType` (RESAMPLED for downsample, NONE for crop). The current `cropToDICOM` uses `NONE`; `downsampleToDICOM` uses `RESAMPLED`. The converged `transformToDICOM` picks based on `p.resampled`.

- [ ] **Step 1: Generalize `cropToDICOM` → `transformToDICOM`**

Rename `cropToDICOM(p cropEmitParams)` → `transformToDICOM(p transformParams)`. Changes:

(a) Metadata: the current `cropToDICOM` reads `src.Metadata()` (preserve). Replace with `p`'s metadata — set the derived source's `source.Metadata` MPP/mag from `p.mppX/p.mppY/p.mag` so a downsample's scaled values flow through. (Read how `cropToDICOM` builds `md`; substitute `p.mppX/p.mppY/p.mag`.)

(b) Thumbnail: the current `cropToDICOM` calls `regenCropThumbnailAssoc(assoc, p.l0, …)` unconditionally. Gate it on `p.regenThumb`:
```go
	} else if p.regenThumb {
		var rerr error
		assoc, rerr = regenCropThumbnailAssoc(assoc, p.l0, p.l0W, p.l0H, p.quality)
		if rerr != nil {
			return fmt.Errorf("regenerate crop thumbnail: %w", rerr)
		}
	}
```

(c) `L0ImageType`: choose by `p.resampled`:
```go
	l0ImageType := []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"}
	if p.resampled {
		l0ImageType = []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"}
	}
	// … emitDICOM(ds, dicomwriter.Options{Associated: !p.noAssociated, L0ImageType: l0ImageType}, p.output, cropForce)
```

Keep the lossless guard + `WithLosslessL0`/`FromReducedL0` branch as-is (lossless is crop-only, `p.resampled` is false there).

- [ ] **Step 2: Delete `downsampleToDICOM`, route through `transformToDICOM`** (Task 8 wires the dispatch).

- [ ] **Step 3: Build + integration tests + dciodvfy gate**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run 'DICOM|CropDICOM' -count=1 2>&1 | grep -vE 'ld: warning' | tail`
Expected: PASS. **Controller:** produce a `downsample --factor 2 <dicom>` and a `crop <dicom>` output and run `dciodvfy` (0 errors) — confirm downsample levels are still `RESAMPLED` and crop levels `NONE`.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/crop_formats.go cmd/wsitools/convert_factor.go
git commit -m "refactor(transform): transformToDICOM (fold cropToDICOM + downsampleToDICOM)"
```

---

## Task 8: Retarget `dispatchDownsampleByTarget` through the converged emitters

**Files:**
- Modify: `cmd/wsitools/convert_factor.go`

The `downsampleTo*` functions are gone (Tasks 3–7). `dispatchDownsampleByTarget` must now: open the source, materialize the **reduced** working L0 via `MaterializeWorkingL0` (no crop, factor>1), compute scaled metadata + `nLevels = len(slide.Levels())`, build `transformParams{resampled: true, regenThumb: false, …}`, and call `dispatchTransform(target, p)`.

- [ ] **Step 1: Rewrite `dispatchDownsampleByTarget`'s body**

Replace the per-target switch (which called `downsampleToSVS` etc.) with a single shared body that builds `transformParams` and dispatches. Open via `source.OpenWithSlide` (for DICOM ambiguity + both handles), resolve factor/target-mag, `MaterializeWorkingL0(ctx, slide.Levels()[0], 0,0, L0W, L0H, factor)`, scale metadata (`mppX*=factor; mag/=factor`), `nLevels = len(slide.Levels())`, then:

```go
	p := transformParams{
		ctx: ctx, src: slide, srcL0: slide.Levels()[0], input: input, output: output,
		l0: l0, l0W: outW, l0H: outH, nLevels: len(slide.Levels()),
		quality: quality, workers: workers, order: order, bigtiffFlag: bigtiffFlag,
		noAssociated: noAssociated,
		lossless: false,
		mppX: mppX, mppY: mppY, mag: mag, resampled: true, regenThumb: false,
		start: time.Now(),
	}
	return dispatchTransform(target, p)
```

(Read the existing `downsampleToSVS`/`downsampleToTIFF` for the exact factor-resolution + metadata + writer-order setup; lift it into this shared body. `order` comes from `tileorder.ByName(tileOrderName)`.)

**Subtlety:** the SVS Aperio ImageDescription needs the source description — `transformToSVS` reads `p.src` + builds it (Task 3), so the shared body just needs `p.src`/`p.mag`/`p.mppX` correct.

- [ ] **Step 2: Build + the full downsample/convert-factor suite**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./cmd/wsitools/ -run 'Downsample|ConvertFactor|Scale' -count=1 -timeout 20m 2>&1 | grep -vE 'ld: warning' | tail`
Expected: PASS — all five targets' downsample/convert-factor behavior preserved (`TestConvertFactorSVSParity`, `TestConvertFactorTIFFTargets`, `TestConvertFactorOMETIFF`, `TestConvertFactorSVSFromNonSVS`, `TestDownsamplePreserves*`).

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/convert_factor.go
git commit -m "refactor(convert): downsample routes through the converged transformTo* emitters"
```

---

## Task 9: `convert --rect` — the new one-pass front-end

**Files:**
- Modify: `cmd/wsitools/convert.go` (add `--rect` flag; route to `runConvertTransform`)
- Create: `cmd/wsitools/convert_transform.go` (the `runConvertTransform` front-end)
- Test: `tests/integration/transform_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `tests/integration/transform_test.go`:

```go
//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runT(bin string, args ...string) (string, error) {
	out, err := exec.Command(bin, args...).CombinedOutput()
	return string(out), err
}

// convert --to <fmt> --rect ... [--factor N] fuses crop + (downsample) + container.
func TestConvertRect_CrossFormat(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skip("no svs fixture")
	}
	// crop a 1024x1024 region of the SVS into a generic-TIFF.
	out := filepath.Join(t.TempDir(), "crop.tiff")
	if o, err := runT(bin, "convert", "--to", "tiff", "--rect", "0,0,1024,1024", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to tiff --rect: %v\n%s", err, o)
	}
	info, _ := runT(bin, "info", out)
	if !contains(info, "Format:  generic-tiff") {
		t.Errorf("expected generic-tiff output:\n%s", info)
	}
	if !contains(info, "1024 × 1024") {
		t.Errorf("expected 1024×1024 L0 (the crop):\n%s", info)
	}
}

// crop + downsample in one convert pass: 1024 crop, factor 2 → 512 L0.
func TestConvertRect_PlusFactor(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skip("no svs fixture")
	}
	out := filepath.Join(t.TempDir(), "cropdown.svs")
	if o, err := runT(bin, "convert", "--to", "svs", "--rect", "0,0,1024,1024", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --rect --factor: %v\n%s", err, o)
	}
	info, _ := runT(bin, "info", out)
	if !contains(info, "512 × 512") {
		t.Errorf("expected 512×512 L0 (1024 crop / factor 2):\n%s", info)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (func() bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
})() }
```

(Confirm `buildOnce`/`testdir` are the existing integration helpers; if a `contains`/`runT` already exists in the package, reuse it instead of redefining.)

- [ ] **Step 2: Run to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run TestConvertRect -count=1`
Expected: FAIL — `convert` rejects `--rect` (unknown flag).

- [ ] **Step 3: Add the `--rect` flag + routing**

In `cmd/wsitools/convert.go`: register `convertCmd.Flags().StringVar(&cvRect, "rect", "", "crop rectangle X,Y,W,H (level-0 coords); fuses with --factor/--codec in one pass")` and declare `var cvRect string`. In `runConvert`, before the `switch cvTo`, add:

```go
	if cvRect != "" {
		if cvTo == "" {
			return fmt.Errorf("--rect requires --to (cross-format crop); for a format-preserving crop use `wsitools crop`")
		}
		if cvTo == "dzi" || cvTo == "szi" {
			return fmt.Errorf("--rect not supported for --to %s", cvTo)
		}
		return runConvertTransform(cmd, input, cvTo, start)
	}
```

- [ ] **Step 4: Implement `runConvertTransform`**

Create `cmd/wsitools/convert_transform.go` — the front-end that parses the rect, materializes the cropped (+ optionally reduced) working L0 via `MaterializeWorkingL0`, computes nLevels + metadata, builds `transformParams{regenThumb: true, resampled: cvFactor>1, lossless: false}`, and calls `dispatchTransform(target, p)`. Model the rect parse on `runCrop`'s (`parseCropRect`/`validateCropBounds`), the factor resolution on `runConvertFactor`, the metadata on: crop preserves source MPP/mag, `--factor` scales them. Resolve `target` from `cvTo` (svs/tiff/ome-tiff/cog-wsi/dicom). Provide the complete function.

Key body:
```go
func runConvertTransform(cmd *cobra.Command, input, target string, start time.Time) error {
	// parse + validate rect (X,Y,W,H), resolve quality/workers/order/factor,
	// open source (opentile.OpenFile; or source.OpenWithSlide for dicom),
	// x,y,w,h := the rect; factor := cvFactor (validate {1,2,4,8,16}),
	// l0, ow, oh, err := downscale.MaterializeWorkingL0(ctx, srcL0, x, y, w, h, factor)
	// nLevels := cropPyramidLevels(ow, oh, outputTileSize)
	// mppX,mppY,mag := cropSourceScale(input, src); if factor>1 { mppX*=factor; mppY*=factor; mag/=factor }
	// p := transformParams{… l0, l0W:ow, l0H:oh, nLevels, regenThumb:true, resampled: factor>1, lossless:false …}
	// return dispatchTransform(target, p)
}
```

(Write it fully, mirroring `runCrop` for the source-open + rect plumbing and `downsampleToDICOM` for the dicom-source open.)

- [ ] **Step 5: Run the integration test + build**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run TestConvertRect -count=1 2>&1 | grep -vE 'ld: warning' | tail`
Expected: PASS — cross-format crop + crop+factor fusion. `go build ./...` clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/convert_transform.go tests/integration/transform_test.go
git commit -m "feat(convert): --rect fuses crop + downsample + container in one pass"
```

---

## Task 10: Convergence regression + parity tests

**Files:**
- Modify: `tests/integration/transform_test.go`

- [ ] **Step 1: Add alias-parity + lossless-preserved tests**

Append tests asserting the refactor is behavior-preserving:

```go
// crop alias produces the same pixels as convert --rect --to <source-format>.
func TestCropAliasParity(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skip("no svs fixture")
	}
	a := filepath.Join(t.TempDir(), "a.svs")
	b := filepath.Join(t.TempDir(), "b.svs")
	if o, err := runT(bin, "crop", "--rect", "0,0,1024,1024", "-f", "-o", a, src); err != nil {
		t.Fatalf("crop: %v\n%s", err, o)
	}
	if o, err := runT(bin, "convert", "--to", "svs", "--rect", "0,0,1024,1024", "-f", "-o", b, src); err != nil {
		t.Fatalf("convert --rect: %v\n%s", err, o)
	}
	ha, _ := runT(bin, "hash", "--mode", "pixel", a)
	hb, _ := runT(bin, "hash", "--mode", "pixel", b)
	if pixelDigest(ha) == "" || pixelDigest(ha) != pixelDigest(hb) {
		t.Errorf("crop vs convert --rect pixel mismatch:\n a=%s\n b=%s", ha, hb)
	}
}
```

(Reuse the existing `pixelDigest` helper from `convert_factor_test.go`'s package if shared; otherwise extract a small digest parser. If `crop --lossless` byte-identity has an existing oracle in `crop_test.go`, no new lossless test is needed here — the existing one guards it through the converged emitter.)

- [ ] **Step 2: Run the integration suite (no regressions)**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/... -count=1 -timeout 30m 2>&1 | grep -vE 'ld: warning' | tail`
Expected: PASS — the crop parity/byte-identity oracles, the format-preserving matrix, the new `--rect` tests, and the DICOM transform tests all green through the converged engine.

- [ ] **Step 3: Commit**

```bash
git add tests/integration/transform_test.go
git commit -m "test(transform): alias parity for the converged emitters"
```

---

## Task 11: Docs

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: README**

In the `convert` section, note `--rect`: "`convert --to FMT --rect X,Y,W,H [--factor N]` crops (and optionally downsamples) into a *different* container in one decode→encode pass — the cross-format / fused counterpart to the format-preserving `crop` / `downsample` aliases." Update the `convert` flag list. (Defer the broader `transcode`/`--to`-optional narrative to Plan 2.)

- [ ] **Step 2: CHANGELOG `[Unreleased]` → Added**

```markdown
- **`convert --rect X,Y,W,H`** — crop (optionally `+ --factor`) into a different
  container in **one decode→rebuild pass**, instead of piping `crop | downsample
  | convert` (which re-encodes the pixels each stage). Internally the
  `downsampleTo*` / `cropTo*` per-format emitters converged into one
  `transformTo*` family fed by `downscale.MaterializeWorkingL0`. `crop` /
  `downsample` are unchanged. (Unified-transform Plan 1; `transcode`, `--to`
  default, and the conformance validator land in Plan 2.)
```

- [ ] **Step 3: Build + commit**

```bash
go build ./... && git add README.md CHANGELOG.md
git commit -m "docs(convert): document --rect one-pass crop+downsample+container (Plan 1)"
```

---

## Final verification (controller, after all tasks)

- [ ] `go build ./...` + `go build -tags nocgo ./...` + `go vet ./...` clean.
- [ ] Full unit suite green (heavy `-race cmd/wsitools` with `-timeout 30m`).
- [ ] Integration suite green (`-tags integration`, `-timeout 30m`) — crop oracles, downsample regression, format-preserving matrix, DICOM transform, new `--rect` + parity tests.
- [ ] **dciodvfy gate:** `downsample --factor 2 <dicom>` levels RESAMPLED, `crop <dicom>` levels NONE, both 0 errors — confirming the DICOM convergence preserved ImageType semantics.
- [ ] **No dead code:** `downsampleTo{SVS,TIFF,OMETIFF,COGWSI,DICOM}`, `cropEmitSVS`, and the old `cropTo*` names are gone (grep returns nothing); `cropEmitParams` is gone.
- [ ] Dispatch a final code reviewer over the whole branch (focus: behavior-preservation of every format's crop + downsample; the metadata/nLevels/ImageType deltas; lossless still byte-identical; `--rect` requires `--to`).
- [ ] Use `superpowers:finishing-a-development-branch`.
```
