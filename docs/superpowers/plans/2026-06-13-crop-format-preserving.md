# Format-preserving `crop` (Phase 2a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `crop` writes its output back to the source's own TIFF-family container (svs/tiff/ome-tiff/cog-wsi), re-encode mode, by routing the cropped L0 through per-format emitters that mirror `downsampleTo{Format}`.

**Architecture:** Split `runCrop` into a format-agnostic front-end (open → detect target → validate → lossless-guard → dispatch) and per-format emitters. SVS keeps its existing body (extracted into `cropEmitSVS`, unchanged). New `cropToTIFF`/`cropToOMETIFF`/`cropToCOGWSI` each ≈ their `downsampleTo{Format}` fed a pre-materialized **cropped** L0 (exact extent) with **preserved** MPP/magnification. cog-wsi needs a from-raster `buildPyramidFromRasterCOGWSI` extraction.

**Tech Stack:** Go, cobra, `internal/tiff/streamwriter` (svs/tiff/ome-tiff), `internal/tiff/cogwsiwriter` (cog-wsi), `internal/downscale`.

**Spec:** `docs/superpowers/specs/2026-06-13-crop-format-preserving-design.md`
**Templates to mirror (read them):** `downsampleToTIFF` / `downsampleToOMETIFF` / `downsampleToCOGWSI` / `buildPyramidCOGWSI` in `cmd/wsitools/convert_factor.go`.

---

## File Structure

| File | Action |
|---|---|
| `cmd/wsitools/convert_factor.go` | Modify: extract `buildPyramidFromRasterCOGWSI` from `buildPyramidCOGWSI` |
| `cmd/wsitools/crop.go` | Modify: `runCrop` front-end + dispatch; extract `cropEmitSVS` |
| `cmd/wsitools/crop_formats.go` (new) | `cropToTIFF`, `cropToOMETIFF`, `cropToCOGWSI` |
| `tests/integration/crop_test.go` | Modify: format-preserving matrix + lossless-non-svs guard |

Reused: `MaterializeCroppedL0`, `buildPyramidFromRaster`, `cropPyramidLevels`, `validateCropBounds`, `downsampleTargetForFormat`, `writeOneAssociated`, `faithfulCOGWSISpecOT`, `SyntheticOMEDescriptionWithMag`, `omeAssocName`, `OMEAssoc`, `halveRaster`, `encodeAndWriteLevelCOGWSI`, `parseBigTIFFFlag`.

---

## Task 1: Extract `buildPyramidFromRasterCOGWSI`

**Files:** `cmd/wsitools/convert_factor.go` (modify `buildPyramidCOGWSI`, ~lines 832-880).

Mirror the `buildPyramidFromRaster` extraction: pull the in-memory-raster loop out of `buildPyramidCOGWSI` so `crop` can feed it a cropped L0. Behaviour-preserving for `downsample`.

- [ ] **Step 1: Read** `buildPyramidCOGWSI` in full (the materialize + per-level loop using `encodeAndWriteLevelCOGWSI` + the inter-level halve, which currently inlines the even-up + `otresample.ImageInto(Box)`).

- [ ] **Step 2: Add `buildPyramidFromRasterCOGWSI`** (place directly below `buildPyramidCOGWSI`):

```go
// buildPyramidFromRasterCOGWSI encodes an in-memory RGB888 L0 raster into a
// cogwsiwriter pyramid, box-halving between levels via halveRaster. nLevels is
// the total level count (L0 included). Shared by buildPyramidCOGWSI (downsample)
// and cropToCOGWSI.
func buildPyramidFromRasterCOGWSI(ctx context.Context, w *cogwsiwriter.Writer, l0 []byte, l0W, l0H, nLevels, quality int) error {
	currentRaster := l0
	currentW, currentH := l0W, l0H
	for outLvl := 0; outLvl < nLevels; outLvl++ {
		if err := encodeAndWriteLevelCOGWSI(ctx, w, currentRaster, currentW, currentH, quality, outLvl == 0); err != nil {
			return fmt.Errorf("level %d: %w", outLvl, err)
		}
		if outLvl < nLevels-1 {
			var herr error
			currentRaster, currentW, currentH, herr = halveRaster(currentRaster, currentW, currentH)
			if herr != nil {
				return fmt.Errorf("Box halving level %d→%d: %w", outLvl, outLvl+1, herr)
			}
			if currentW == 0 || currentH == 0 {
				break
			}
		}
	}
	return nil
}
```

- [ ] **Step 3: Re-point `buildPyramidCOGWSI`** to materialize then delegate. Replace its body after the `outL0`/`MaterializeReducedL0` block with:

```go
	// (after MaterializeReducedL0 fills outL0 of size outW×outH)
	return buildPyramidFromRasterCOGWSI(ctx, w, outL0, outW, outH, nLevels, quality)
```

(Remove the now-duplicated per-level loop from `buildPyramidCOGWSI`. Keep its `srcLevels`/`outW`/`outH`/`nLevels`/`rasterBytes`/`MaterializeReducedL0` prologue.) Confirm the inter-level halve in the original used the same even-up + Box as `halveRaster` — it does (identical to the `buildPyramidFromRaster` extraction).

- [ ] **Step 4: Build + vet + cog-wsi unit tests.**

Run: `go build ./...` (clean), `go vet ./cmd/wsitools/` (clean), `go test ./cmd/wsitools/ -run 'COGWSI|Pyramid|Downsample' -count=1` (PASS).

- [ ] **Step 5: Commit.**

```bash
git add cmd/wsitools/convert_factor.go
git commit -m "refactor(convert): extract buildPyramidFromRasterCOGWSI for crop reuse"
```

- [ ] **Step 6: Controller runs the downsample regression guard** (implementer does NOT).

`WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestDownsample -count=1 -timeout 30m` → PASS (cog-wsi downsample output unchanged).

---

## Task 2: `runCrop` front-end + dispatch + `cropEmitSVS` + `cropToTIFF` + `cropToOMETIFF`

**Files:** `cmd/wsitools/crop.go` (refactor `runCrop`, extract `cropEmitSVS`), `cmd/wsitools/crop_formats.go` (new: `cropToTIFF`, `cropToOMETIFF`).

### Step 1: Extract `cropEmitSVS` (mechanical, behaviour-preserving)

In `crop.go`, the current `runCrop` body (everything from `srcL0 := src.Levels()[0]` through `wtr.Close()` + the final `fmt.Printf`) is the SVS emission. Move that body verbatim into:

```go
// cropEmitSVS is the SVS crop emission (re-encode + --lossless). Extracted from
// runCrop unchanged; the front-end dispatches here for SVS sources.
func cropEmitSVS(ctx context.Context, src *opentile.Slide, input, output string, x, y, w, h, quality, workers int, order tileorder.OrderStrategy, bigtiffFlag string, noAssociated, lossless bool, start time.Time) error {
	srcL0 := src.Levels()[0]
	baseW, baseH := srcL0.Size.W, srcL0.Size.H
	// ... the existing SVS body, verbatim ...
}
```

The body needs `src` (opened), `input` (for `source.ReadSourceImageDescription`), and the resolved `order`. Adjust the head: it already had `srcL0`/`baseW`/`baseH`/`rawDesc`/`desc`/quality-default/effective-rect/`BuildCropImageDescription`/`streamwriter.Create`/materialize/branch/associated/close — keep ALL of it. Remove the now-front-end-owned pieces (the `opentile.OpenFile`, `src.Format()` guard, path/force checks, `tileorder.ByName`) since the front-end does them and passes `src`+`order` in.

### Step 2: Rewrite `runCrop` as the front-end + dispatch

```go
func runCrop(ctx context.Context, input, output string, x, y, w, h, quality, workers int, tileOrderName, bigtiffFlag string, force, noAssociated, lossless bool, start time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if output == "" {
		return fmt.Errorf("--output is required")
	}
	if workers == 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if !force {
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("output exists (use --force to overwrite): %s", output)
		}
	}
	absIn, _ := filepath.Abs(input)
	absOut, _ := filepath.Abs(output)
	if absIn == absOut {
		return fmt.Errorf("input and output paths are the same")
	}

	src, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	target, ok := downsampleTargetForFormat(string(src.Format()))
	if !ok {
		return fmt.Errorf("crop: unsupported source format %q (supported: svs, ome-tiff, tiff, cog-wsi)", src.Format())
	}

	srcL0 := src.Levels()[0]
	baseW, baseH := srcL0.Size.W, srcL0.Size.H
	if err := validateCropBounds(x, y, w, h, baseW, baseH); err != nil {
		return err
	}
	if lossless && target != "svs" {
		return fmt.Errorf("crop --lossless currently supports SVS sources only (other containers: Phase 2b)")
	}
	order, err := tileorder.ByName(tileOrderName)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	if target == "svs" {
		return cropEmitSVS(ctx, src, input, output, x, y, w, h, quality, workers, order, bigtiffFlag, noAssociated, lossless, start)
	}

	// Non-SVS: exact-extent re-encode. Default quality 90 (non-SVS sources carry
	// no Aperio Q to inherit).
	q := quality
	if q == 0 {
		q = 90
	}
	if q < 1 || q > 100 {
		return fmt.Errorf("--quality must be in [1,100], got %d", q)
	}
	rasterBytes := int64(w) * int64(h) * 3
	if rasterBytes < 0 {
		return fmt.Errorf("cropped L0 raster size overflows int64")
	}
	outL0 := make([]byte, rasterBytes)
	if err := downscale.MaterializeCroppedL0(ctx, srcL0, outL0, x, y, w, h); err != nil {
		return fmt.Errorf("materialize cropped L0: %w", err)
	}
	nLevels := cropPyramidLevels(w, h, outputTileSize)

	switch target {
	case "tiff":
		return cropToTIFF(ctx, src, input, output, outL0, w, h, nLevels, q, workers, order, bigtiffFlag, noAssociated, start)
	case "ome-tiff":
		return cropToOMETIFF(ctx, src, input, output, outL0, w, h, nLevels, q, workers, order, bigtiffFlag, noAssociated, start)
	case "cog-wsi":
		return cropToCOGWSI(ctx, src, input, output, outL0, w, h, nLevels, q, workers, order, bigtiffFlag, noAssociated, start)
	default:
		return fmt.Errorf("crop: target %q not implemented", target)
	}
}
```

> All three emitters take `input string` (after `src`) so `cropSourceScale` can read
> the source ImageDescription. `cropToCOGWSI` is implemented in Task 3 — for Task 2
> to compile, add a temporary stub in `crop_formats.go` with the EXACT signature:
> `func cropToCOGWSI(ctx context.Context, src *opentile.Slide, input, output string, l0 []byte, l0W, l0H, nLevels, quality, workers int, order tileorder.OrderStrategy, bigtiffFlag string, noAssociated bool, start time.Time) error { return fmt.Errorf("crop cog-wsi: implemented in the next task") }`
> Task 3 replaces the stub.

### Step 3: Implement `cropToTIFF` + `cropToOMETIFF` in `crop_formats.go`

Helper to resolve source MPP/mag (preserved — no scaling), mirroring the downsample writers' resolution but without `*factor`:

```go
package main

import (
	"context"
	"fmt"
	"os"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// cropSourceScale returns the source MPP (X,Y) and magnification, preferring the
// Aperio ImageDescription, else opentile metadata. Crop preserves resolution, so
// these are emitted unchanged (no factor scaling).
func cropSourceScale(input string, src *opentile.Slide) (mppX, mppY, mag float64) {
	rawDesc, _ := source.ReadSourceImageDescription(input)
	if desc, err := ParseImageDescription(rawDesc); err == nil {
		return desc.MPP, desc.MPP, desc.AppMag
	}
	md := src.Metadata()
	return md.MPP.X, md.MPP.Y, md.Magnification
}
```

> Add `"github.com/wsilabs/wsitools/internal/source"` to the imports (used by `cropSourceScale`). Adjust the import list to exactly what compiles.

`cropToTIFF` (mirror `downsampleToTIFF`, fed the cropped raster, preserved mag):

```go
// cropToTIFF writes the cropped L0 + rebuilt pyramid as a generic tiled TIFF.
// Mirrors downsampleToTIFF but with an exact-extent cropped L0 and preserved
// MPP/magnification (crop keeps resolution).
func cropToTIFF(ctx context.Context, src *opentile.Slide, input, output string, l0 []byte, l0W, l0H, nLevels, quality, workers int, order tileorder.OrderStrategy, bigtiffFlag string, noAssociated bool, start time.Time) error {
	mppX, mppY, mag := cropSourceScale(input, src)
	bigtiffMode := streamwriterBigTIFF(bigtiffFlag, l0W, l0H)

	imageDesc := fmt.Sprintf("wsi-tools/%s crop source=%s codec=jpeg mpp=%v mag=%vx", Version, src.Format(), mppX, mag)
	w, err := streamwriter.Create(output, streamwriter.Options{
		BigTIFF:          bigtiffMode,
		ImageDescription: imageDesc,
		ToolsVersion:     Version,
		SourceFormat:     string(src.Format()),
		FormatName:       "tiff",
		AcceptedOrders:   acceptedOrdersForFormat("tiff"),
		DefaultOrder:     order,
		MPPX:             mppX,
		MPPY:             mppY,
		Magnification:    mag,
		ICCProfile:       src.ICCProfile(),
	})
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			w.Abort()
		}
	}()
	if err := buildPyramidFromRaster(ctx, w, l0, l0W, l0H, nLevels, quality, workers, nil); err != nil {
		return fmt.Errorf("build pyramid: %w", err)
	}
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if err := writeOneAssociated(w, a); err != nil {
				return fmt.Errorf("write associated %s: %w", a.Type(), err)
			}
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	closed = true
	reportWrote(output, start)
	return nil
}
```

`cropToOMETIFF` (mirror `downsampleToOMETIFF`: OME-XML with **cropped** dims, `SubResolutionPyramid:true`, `SampleFormat:1`, OME-filtered associated):

```go
// cropToOMETIFF writes the cropped L0 + rebuilt pyramid as a conformant OME-TIFF.
func cropToOMETIFF(ctx context.Context, src *opentile.Slide, input, output string, l0 []byte, l0W, l0H, nLevels, quality, workers int, order tileorder.OrderStrategy, bigtiffFlag string, noAssociated bool, start time.Time) error {
	mppX, mppY, mag := cropSourceScale(input, src)
	bigtiffMode := streamwriterBigTIFF(bigtiffFlag, l0W, l0H)

	var omeAssocs []OMEAssoc
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if name := omeAssocName(string(a.Type())); name != "" {
				omeAssocs = append(omeAssocs, OMEAssoc{Name: name, W: uint32(a.Size().W), H: uint32(a.Size().H)})
			}
		}
	}
	omeXML := SyntheticOMEDescriptionWithMag(uint32(l0W), uint32(l0H), mppX, mppY, mag, "Image", string(src.Format()), omeAssocs)

	w, err := streamwriter.Create(output, streamwriter.Options{
		BigTIFF:              bigtiffMode,
		ImageDescription:     omeXML,
		ToolsVersion:         Version,
		SourceFormat:         string(src.Format()),
		FormatName:           "ome-tiff",
		AcceptedOrders:       acceptedOrdersForFormat("ome-tiff"),
		DefaultOrder:         order,
		MPPX:                 mppX,
		MPPY:                 mppY,
		Magnification:        mag,
		ICCProfile:           src.ICCProfile(),
		SubResolutionPyramid: true,
		SampleFormat:         1,
	})
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			w.Abort()
		}
	}()
	if err := buildPyramidFromRaster(ctx, w, l0, l0W, l0H, nLevels, quality, workers, nil); err != nil {
		return fmt.Errorf("build pyramid: %w", err)
	}
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if omeAssocName(string(a.Type())) == "" {
				continue
			}
			if err := writeOneAssociated(w, a); err != nil {
				return fmt.Errorf("write associated %s: %w", a.Type(), err)
			}
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	closed = true
	reportWrote(output, start)
	return nil
}
```

Small shared helpers (add to `crop_formats.go`):

```go
// streamwriterBigTIFF resolves the BigTIFF mode for streamwriter formats from the
// flag; "auto" promotes when the uncompressed L0 raster exceeds 4 GiB (a crop is
// usually well under, like the SVS path).
func streamwriterBigTIFF(flag string, w, h int) tiff.BigTIFFMode {
	switch flag {
	case "on":
		return tiff.BigTIFFOn
	case "off":
		return tiff.BigTIFFOff
	default:
		if int64(w)*int64(h)*3 > (int64(4) << 30) {
			return tiff.BigTIFFOn
		}
		return tiff.BigTIFFOff
	}
}

// reportWrote prints the standard "wrote <path> (<size>) in <elapsed>" line.
func reportWrote(output string, start time.Time) {
	var sz string
	if fi, err := os.Stat(output); err == nil {
		sz = formatBytes(fi.Size())
	}
	fmt.Printf("wrote %s (%s) in %s\n", output, sz, time.Since(start).Round(time.Millisecond))
}
```

> Imports for `crop_formats.go`: `context`, `errors` (Task 3), `fmt`, `log/slog`
> (Task 3), `os`, `time`, `opentile`, `internal/source`, `internal/tiff`,
> `internal/tiff/streamwriter`, `internal/tiff/cogwsiwriter` (Task 3),
> `internal/tiff/tileorder`. Trim to exactly what compiles per task.

### Step 4: Build + vet + existing SVS crop unit tests

Run: `go build ./...` clean; `go vet ./cmd/wsitools/` clean.
Run: `go test ./cmd/wsitools/ -run 'Crop|Snap|Halve|Thumb|Quality|BuildCrop|Rect' -count=1` → PASS (SVS extraction behaviour-preserving).
Run: `go run ./cmd/wsitools crop --help` (unchanged flags).

- [ ] **Step 5 (controller): SVS crop integration regression** — confirm the `cropEmitSVS` extraction didn't change SVS output:
`WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run 'TestCrop_CMU2ParityOracle|TestCropLossless_ByteIdentity|TestCrop_SmallRegionTileAligned' -count=1 -timeout 30m` → PASS.

- [ ] **Step 6: Commit.**

```bash
git add cmd/wsitools/crop.go cmd/wsitools/crop_formats.go
git commit -m "feat(crop): format-preserving dispatch + cropToTIFF/cropToOMETIFF (re-encode)"
```

---

## Task 3: `cropToCOGWSI`

**Files:** `cmd/wsitools/crop_formats.go` (replace the Task-2 stub).

Mirror `downsampleToCOGWSI` fed the cropped L0 via `buildPyramidFromRasterCOGWSI`.

```go
// cropToCOGWSI writes the cropped L0 + rebuilt pyramid as a COG-WSI TIFF.
func cropToCOGWSI(ctx context.Context, src *opentile.Slide, input, output string, l0 []byte, l0W, l0H, nLevels, quality, workers int, order tileorder.OrderStrategy, bigtiffFlag string, noAssociated bool, start time.Time) error {
	mppX, mppY, mag := cropSourceScale(input, src)

	bigTIFFMode, err := parseBigTIFFFlag(bigtiffFlag)
	if err != nil {
		if bigtiffFlag == "" {
			bigTIFFMode = cogwsiwriter.BigTIFFAuto
		} else {
			return err
		}
	}

	w, err := cogwsiwriter.Create(output, cogwsiwriter.Options{
		BigTIFF:      bigTIFFMode,
		ToolsVersion: Version,
		DefaultOrder: order,
		Metadata: cogwsiwriter.Metadata{
			MPPX:            mppX,
			MPPY:            mppY,
			Magnification:   mag,
			ICCProfile:      src.ICCProfile(),
			SourceFormat:    string(src.Format()),
			SourceImageDesc: fmt.Sprintf("wsitools/%s crop source=%s", Version, src.Format()),
		},
	})
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	aborted := false
	defer func() {
		if aborted {
			w.Abort()
		}
	}()

	if err := buildPyramidFromRasterCOGWSI(ctx, w, l0, l0W, l0H, nLevels, quality); err != nil {
		aborted = true
		return fmt.Errorf("build pyramid: %w", err)
	}
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			spec, err := faithfulCOGWSISpecOT(a)
			if err != nil {
				if errors.Is(err, errSkipAssociated) {
					slog.Warn("skipping associated image", "type", a.Type(), "reason", err)
					continue
				}
				aborted = true
				return err
			}
			if err := w.AddAssociated(spec); err != nil {
				aborted = true
				return fmt.Errorf("add associated %s: %w", a.Type(), err)
			}
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	reportWrote(output, start)
	return nil
}
```

Add imports to `crop_formats.go`: `"errors"`, `"log/slog"`, `"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"`. The `runCrop` cog-wsi dispatch already passes `input` (Task 2). Confirm `errSkipAssociated`, `faithfulCOGWSISpecOT`, `parseBigTIFFFlag`, `cogwsiwriter.BigTIFFAuto` resolve (all used by `downsampleToCOGWSI`).

- [ ] **Build + vet + commit.**

Run: `go build ./...` clean; `go vet ./cmd/wsitools/` clean; `go test ./cmd/wsitools/ -run Crop -count=1` PASS.

```bash
git add cmd/wsitools/crop_formats.go cmd/wsitools/crop.go
git commit -m "feat(crop): cropToCOGWSI (format-preserving cog-wsi re-encode)"
```

---

## Task 4: Integration tests + lossless-non-SVS guard

**Files:** `tests/integration/crop_test.go`.

- [ ] **Step 1: Format-preserving re-encode matrix.** Add:

```go
// TestCrop_FormatPreserving verifies crop writes the output back to the source's
// own container (re-encode) for the TIFF family, with exact extent + preserved
// MPP/mag. Local-only (large fixtures).
func TestCrop_FormatPreserving(t *testing.T) {
	td := testdir(t)
	bin := buildOnce(t)
	cases := []struct {
		file, ext  string
		x, y, w, h int
		wantFormat opentile.Format
	}{
		{"generic-tiff/CMU-1.tiff", "tiff", 500, 500, 2000, 2000, opentile.FormatGenericTIFF},
		{"ome-tiff/Leica-1.ome.tiff", "ome.tiff", 500, 500, 2000, 2000, opentile.FormatOMETIFF},
		{"cog-wsi/CMU-1_cog-wsi.tiff", "tiff", 500, 500, 2000, 2000, opentile.FormatCOGWSI},
	}
	for _, c := range cases {
		c := c
		t.Run(c.file, func(t *testing.T) {
			src := filepath.Join(td, c.file)
			if _, err := os.Stat(src); err != nil {
				t.Skipf("fixture missing: %s", src)
			}
			srcTlr, err := opentile.OpenFile(src)
			if err != nil {
				t.Fatalf("open src: %v", err)
			}
			sb := srcTlr.Levels()[0].Size
			srcMD := srcTlr.Metadata()
			srcTlr.Close()
			if c.x+c.w > sb.W || c.y+c.h > sb.H {
				t.Skipf("rect exceeds source %dx%d", sb.W, sb.H)
			}
			out := filepath.Join(t.TempDir(), "crop."+c.ext)
			cmd := exec.Command(bin, "crop", "--rect",
				fmt.Sprintf("%d,%d,%d,%d", c.x, c.y, c.w, c.h), "-o", out, src)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("crop: %v\n%s", err, b)
			}
			outTlr, err := opentile.OpenFile(out)
			if err != nil {
				t.Fatalf("open out: %v", err)
			}
			defer outTlr.Close()
			if outTlr.Format() != c.wantFormat {
				t.Errorf("format = %v, want preserved %v", outTlr.Format(), c.wantFormat)
			}
			l0 := outTlr.Levels()[0]
			if l0.Size.W != c.w || l0.Size.H != c.h {
				t.Errorf("L0 = %dx%d, want exact %dx%d", l0.Size.W, l0.Size.H, c.w, c.h)
			}
			md := outTlr.Metadata()
			if srcMD.MPP.X != 0 && md.MPP.X != srcMD.MPP.X {
				t.Errorf("MPP.X changed: got %v, want %v", md.MPP.X, srcMD.MPP.X)
			}
			if srcMD.Magnification != 0 && md.Magnification != srcMD.Magnification {
				t.Errorf("Magnification changed: got %v, want %v", md.Magnification, srcMD.Magnification)
			}
		})
	}
}
```

- [ ] **Step 2: Lossless-non-SVS guard** (fast — uses the small generic-tiff fixture; no fixture-size concern, but keep build-tagged for consistency):

```go
// TestCropLossless_RejectsNonSVS confirms --lossless errors clearly on a non-SVS
// source until Phase 2b.
func TestCropLossless_RejectsNonSVS(t *testing.T) {
	td := testdir(t)
	bin := buildOnce(t)
	src := filepath.Join(td, "generic-tiff", "CMU-1-Small-Region.stripped.tiff")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	out := filepath.Join(t.TempDir(), "x.tiff")
	b, err := exec.Command(bin, "crop", "--lossless", "--rect", "0,0,256,256", "-o", out, src).CombinedOutput()
	if err == nil {
		t.Fatalf("expected error for --lossless on non-SVS, got success:\n%s", b)
	}
	if !strings.Contains(string(b), "SVS sources only") {
		t.Errorf("error should mention SVS-only; got:\n%s", b)
	}
}
```

Add `"strings"` to the test imports if not present.

- [ ] **Step 3 (controller): run the new + regression integration suites.**

`WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run 'TestCrop_FormatPreserving|TestCropLossless_RejectsNonSVS|TestDownsample' -count=1 -timeout 30m -v`
Expected: format-preserving cases PASS (format preserved, exact extent, MPP/mag), lossless-guard PASS, downsample regression PASS.

- [ ] **Step 4: Commit.**

```bash
git add tests/integration/crop_test.go
git commit -m "test(crop): format-preserving re-encode matrix + lossless-non-SVS guard"
```

---

## Final verification (controller)

- [ ] `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -race ./internal/... ./cmd/wsitools/ -count=1 -timeout 30m` green.
- [ ] `go vet ./...` clean; `gofmt -l` clean for the touched files.
- [ ] Integration: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run 'TestCrop|TestDownsample' -count=1 -timeout 30m` green.
- [ ] Final whole-branch code review, then superpowers:finishing-a-development-branch.

## Notes / risks

- **`cropEmitSVS` extraction is behaviour-preserving** — guarded by the SVS crop integration tests (parity oracle + byte-identity + smoke + variants).
- **`buildPyramidFromRasterCOGWSI` extraction** must not change downsample cog-wsi output — Task 1 Step 6 regression guard.
- **Novel-codec generic-TIFF sources** (avif/jxl/htj2k `*-out.tiff`): re-encode requires a registered decoder; if absent the crop fails with a clear "no decoder" error. Out of the core matrix; not tested here.
- **Stale thumbnail (MUST-ADDRESS, deferred):** associated passthrough means a cropped non-SVS thumbnail shows the whole slide. Tracked in the spec's must-address follow-ups + memory; Phase 2b or a dedicated task.
- **MERGE-VERIFY:** after merging, grep the merged files for the new emitters + re-run a key test on the merged HEAD (per the detached-HEAD lesson).
