# `convert --factor` Scaling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `--factor`/`--target-mag` scaling to `convert` for svs/tiff/ome-tiff/cog-wsi via a shared `internal/downscale` engine, and reimplement `downsample` as a thin alias over it.

**Architecture:** Extract `downsample`'s codec-agnostic reduce-then-rebuild core into `internal/downscale`. `convert --factor` materializes a reduced L0 + fresh pyramid and routes levels through the existing per-target writers (streamwriter / cogwsiwriter) with per-target metadata scaling (MPP ×N, mag ÷N; Aperio ImageDescription for SVS, OME-XML PhysicalSize for OME-TIFF). `downsample` delegates to `convert --to svs --factor`.

**Tech Stack:** Go, opentile-go v0.35.0, cobra. Integration tests gated by `WSI_TOOLS_TESTDIR`; fixtures `svs/CMU-1-Small-Region.svs` (JPEG), `svs/JP2K-33003-1.svs` (JP2K).

**Spec:** `docs/superpowers/specs/2026-06-05-convert-factor-scaling-design.md`

---

## File structure

| File | Responsibility |
|---|---|
| `internal/downscale/downscale.go` (new) | reduction core: `DecodeReducedTile`, `BoxHalve`, `MaterializeReducedL0`; pure Go, no cobra/CLI |
| `internal/downscale/downscale_test.go` (new) | unit tests for the reduction primitives |
| `cmd/wsitools/downsample.go` | first: delegate reduction to `internal/downscale`; last: become a thin alias → `convert --to svs --factor` |
| `cmd/wsitools/convert.go` | add `--factor`/`--target-mag` flags + validation/dispatch |
| `cmd/wsitools/convert_factor.go` (new) | the reduce-then-rebuild path for TIFF-family targets + per-target metadata scaling |
| `cmd/wsitools/convert_factor_test.go` (new) | per-target `--factor` integration tests + SVS parity |
| `CHANGELOG.md`, `README.md` | document the new capability + downsample-as-alias |

---

## Task 1: Extract the reduction core into `internal/downscale`

Pure refactor — no behavior change. Verified by existing `downsample` tests still passing.

**Files:**
- Create: `internal/downscale/downscale.go`
- Modify: `cmd/wsitools/downsample.go`
- Test: `internal/downscale/downscale_test.go`

- [ ] **Step 1: Create the package by moving the reduction primitives**

Move these from `cmd/wsitools/downsample.go` into `internal/downscale/downscale.go`, exporting them (capitalize): `decodeReducedTile` → `DecodeReducedTile`, `downsampleByPowerOf2` → `BoxHalve`, and the per-tile materialize loop from `materializeOutputL0` → a new exported `MaterializeReducedL0(ctx, src *opentile.Slide, srcL0 opentile.Level, outL0 []byte, outW, outH, factor int) error`. Keep signatures identical except the rename + export. Imports move with them (`opentile`, `otdecoder`, `errors`). The package doc comment: `// Package downscale reduces a WSI source by an integer power-of-2 factor: codec-domain scaled decode where the codec supports it, else full-decode + box-halve.`

- [ ] **Step 2: Update `downsample.go` to call the package**

In `cmd/wsitools/downsample.go`, replace the now-moved functions' call sites with `downscale.MaterializeReducedL0(...)`, `downscale.BoxHalve(...)`. Add import `"github.com/wsilabs/wsitools/internal/downscale"`. Remove the moved function bodies. `materializeOutputL0` becomes a thin wrapper calling `downscale.MaterializeReducedL0` (or is inlined at its call site).

- [ ] **Step 3: Add a unit test for the package**

```go
// internal/downscale/downscale_test.go
package downscale

import "testing"

func TestBoxHalveDims(t *testing.T) {
	// 256x256 RGB → factor 4 → 64x64.
	src := make([]byte, 256*256*3)
	pix, w, h, err := BoxHalve(src, 256, 256, 4)
	if err != nil {
		t.Fatal(err)
	}
	if w != 64 || h != 64 || len(pix) != 64*64*3 {
		t.Fatalf("got %dx%d len=%d, want 64x64 len=%d", w, h, len(pix), 64*64*3)
	}
}
```

- [ ] **Step 4: Run — package test + downsample regression (no behavior change)**

Run: `cd /Volumes/Ext/GitHub/wsitools && make build && go test ./internal/downscale/ && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'Downsample'`
Expected: all PASS (downsample output unchanged — it just calls the extracted code). (cgo build; ignore duplicate-library linker warnings; `make build` after each change since integration tests use `./bin/wsitools`.)

- [ ] **Step 5: Commit**

```bash
git add internal/downscale/ cmd/wsitools/downsample.go
git commit -m "refactor(downsample): extract reduction core to internal/downscale"
```

---

## Task 2: `convert --factor`/`--target-mag` flags + validation

**Files:**
- Modify: `cmd/wsitools/convert.go`
- Test: `cmd/wsitools/convert_factor_test.go` (new)

- [ ] **Step 1: Write failing tests for flag validation**

```go
// cmd/wsitools/convert_factor_test.go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --factor with dzi/szi is rejected.
func TestConvertFactorRejectsDZI(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.dzi")
	o, err := runBin(bin, "convert", "--to", "dzi", "--factor", "2", "-f", "-o", out, src)
	if err == nil || !strings.Contains(string(o), "factor") {
		t.Fatalf("expected --factor/dzi rejection, got err=%v\n%s", err, o)
	}
}

// invalid factor rejected.
func TestConvertFactorRejectsBadValue(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.svs")
	o, err := runBin(bin, "convert", "--to", "svs", "--factor", "3", "-f", "-o", out, src)
	if err == nil || !strings.Contains(string(o), "2,4,8,16") {
		t.Fatalf("expected invalid-factor rejection, got err=%v\n%s", err, o)
	}
}
```

- [ ] **Step 2: Run — confirm FAIL**

Run: `make build && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'ConvertFactorRejects' -v`
Expected: FAIL (flags don't exist; `--factor` is currently an unknown flag).

- [ ] **Step 3: Add the flags + validation**

In `cmd/wsitools/convert.go`, add package vars `cvFactor int` and `cvTargetMag int`, register flags in `init()`:
```go
	convertCmd.Flags().IntVar(&cvFactor, "factor", 1, "downsample factor for svs|tiff|ome-tiff|cog-wsi (1=no scaling; one of {2,4,8,16})")
	convertCmd.Flags().IntVar(&cvTargetMag, "target-mag", 0, "alternative to --factor: derive factor from source AppMag")
```
In `runConvert` (before the `switch cvTo`), add validation:
```go
	if cvFactor != 1 || cvTargetMag != 0 {
		if cvTo == "dzi" || cvTo == "szi" {
			return fmt.Errorf("--factor/--target-mag not supported for --to %s (yet)", cvTo)
		}
	}
```
Reuse the existing `isValidFactor` (from downsample.go — it lives in package main, so it is in scope) inside the convert-factor path; the bad-value message must contain `2,4,8,16` (it already does in `isValidFactor`'s error). The factor resolution (incl. `--target-mag`) happens in Task 3's `runConvertFactor`; here only gate dzi/szi up front.

- [ ] **Step 4: Run — confirm dzi rejection passes; bad-value will pass once Task 3 wires factor resolution**

Run: `make build && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'ConvertFactorRejectsDZI' -v`
Expected: PASS. (`TestConvertFactorRejectsBadValue` passes after Task 3.)

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/convert_factor_test.go
git commit -m "feat(convert): add --factor/--target-mag flags + dzi/szi guard"
```

---

## Task 3: `convert --to svs --factor` (reduce-then-rebuild + SVS metadata) + SVS parity

**Files:**
- Create: `cmd/wsitools/convert_factor.go`
- Modify: `cmd/wsitools/convert_tiff.go` (`runConvertTIFF` dispatch), `cmd/wsitools/convert.go`
- Test: `cmd/wsitools/convert_factor_test.go`

- [ ] **Step 1: Write the parity + dims test**

```go
// SVS downsample via convert must match the standalone downsample (pixel-equal).
func TestConvertFactorSVSParity(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	a := filepath.Join(t.TempDir(), "a.svs")
	b := filepath.Join(t.TempDir(), "b.svs")
	if o, err := runBin(bin, "downsample", "--factor", "4", "--quiet", "-f", "-o", a, src); err != nil {
		t.Fatalf("downsample: %v\n%s", err, o)
	}
	if o, err := runBin(bin, "convert", "--to", "svs", "--factor", "4", "-f", "-o", b, src); err != nil {
		t.Fatalf("convert --factor: %v\n%s", err, o)
	}
	ha, _ := runBin(bin, "hash", "--mode", "pixel", a)
	hb, _ := runBin(bin, "hash", "--mode", "pixel", b)
	if pixelDigest(ha) == "" || pixelDigest(ha) != pixelDigest(hb) {
		t.Errorf("pixel hash mismatch:\n a=%s\n b=%s", ha, hb)
	}
	// Metadata scaled: factor-4 of a 40x slide → ~10x, MPP ×4.
	info, _ := runBin(bin, "info", b)
	if !strings.Contains(string(info), "Magnification: 10x") {
		t.Errorf("expected Magnification 10x in:\n%s", info)
	}
}
```
(`pixelDigest` already exists in `dicom_fidelity_test.go`.)

- [ ] **Step 2: Run — confirm FAIL**

Run: `make build && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestConvertFactorSVSParity -v`
Expected: FAIL (`--factor` is a no-op for convert today — output is full-res, not reduced).

- [ ] **Step 3: Implement `runConvertFactor` (svs path)**

Create `cmd/wsitools/convert_factor.go` with `runConvertFactor(cmd *cobra.Command, input, target string, factor int, start time.Time) error`. It mirrors `downsample.go`'s flow but parameterized by `target`:
1. Resolve `factor` (from `cvFactor`, or `cvTargetMag` via the same `isValidFactor`/AppMag logic as `downsample.go:152-166`); error on invalid (message contains `2,4,8,16`).
2. `source.Open(input)`; get `srcL0 := src` opentile L0; compute `outW, outH = ceil(srcL0.Size / factor)`.
3. Allocate `outL0` raster; `downscale.MaterializeReducedL0(ctx, slide, srcL0, outL0, outW, outH, factor)`.
4. Build the scaled metadata: copy `src.Metadata()`, set `MPP *= factor`, `Magnification /= factor`. For `target == "svs"`, parse the Aperio `ImageDescription` and call `MutateForDownsample(factor, outW, outH)` (the existing `AperioDescription` path from `downsample.go`).
5. Create the target writer via the **same** `streamwriter.Options` setup `downsample.go` uses (FormatName from `resolveContainer(src.Format(), "", target)`; MPPX/MPPY/Magnification = scaled values; ImageDescription = scaled desc; ICCProfile carried), and build the output pyramid from `outL0` via the box cascade (reuse `downsample.go`'s `buildPyramid`/`encodeAndWriteLevel`, which write reduced sublevels through the streamwriter). Associated images carried verbatim.

> This is the same engine `downsample` already runs for SVS; the work is parameterizing the writer/container by `target` and moving the metadata-scaling so `convert` can call it. Keep `buildPyramid`/`encodeAndWriteLevel` shared (they already live in `downsample.go`, package main — callable from `convert_factor.go`). Default codec is jpeg (the encoder `downsample` uses).

In `runConvertTIFF` (`convert_tiff.go:31`), at the top after `src` checks: `if cvFactor != 1 || cvTargetMag != 0 { return runConvertFactor(cmd, input, target, cvFactor, start) }`.

- [ ] **Step 4: Run — confirm PASS (parity + bad-value)**

Run: `make build && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'ConvertFactorSVSParity|ConvertFactorRejectsBadValue' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/convert_factor.go cmd/wsitools/convert_tiff.go cmd/wsitools/convert.go cmd/wsitools/convert_factor_test.go
git commit -m "feat(convert): --to svs --factor (reduce-then-rebuild, scaled MPP/mag)"
```

---

## Task 4: tiff + cog-wsi targets

**Files:** `cmd/wsitools/convert_factor.go`, test.

- [ ] **Step 1: Write tests**

```go
func TestConvertFactorTIFFTargets(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	for _, tgt := range []struct{ to, ext string }{{"tiff", "tiff"}, {"cog-wsi", "cog.tiff"}} {
		out := filepath.Join(t.TempDir(), "o."+tgt.ext)
		if o, err := runBin(bin, "convert", "--to", tgt.to, "--factor", "4", "-f", "-o", out, src); err != nil {
			t.Fatalf("%s --factor 4: %v\n%s", tgt.to, err, o)
		}
		info, _ := runBin(bin, "info", out)
		if !strings.Contains(string(info), "Magnification: 10x") {
			t.Errorf("%s: expected 10x in info:\n%s", tgt.to, info)
		}
	}
}
```

- [ ] **Step 2: Run — confirm FAIL** (`make build && ... -run TestConvertFactorTIFFTargets -v`) — expected: FAIL or unsupported-target error for non-svs in `runConvertFactor`.

- [ ] **Step 3: Implement** — in `runConvertFactor`, allow `target ∈ {svs,tiff,ome-tiff,cog-wsi}`. For `tiff`/`cog-wsi` the metadata path is the scaled `md` only (no embedded description to mutate). For `cog-wsi`, route the reduced pyramid + associated images through the `cogwsiwriter` (mirror `runConvertCogWSI`'s writer setup, but feed the reduced L0/pyramid from `internal/downscale` instead of source levels). For `tiff`, the streamwriter path with `FormatName=tiff`.

- [ ] **Step 4: Run — PASS** (`-run TestConvertFactorTIFFTargets -v`).

- [ ] **Step 5: Commit** — `feat(convert): --factor for tiff + cog-wsi targets`.

---

## Task 5: ome-tiff target (scale OME-XML PhysicalSize)

**Files:** `cmd/wsitools/convert_factor.go`, `cmd/wsitools/convert_tiff.go` (OME-XML build), test.

- [ ] **Step 1: Write test**

```go
func TestConvertFactorOMETIFF(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.ome.tiff")
	if o, err := runBin(bin, "convert", "--to", "ome-tiff", "--factor", "4", "-f", "-o", out, src); err != nil {
		t.Fatalf("ome-tiff --factor 4: %v\n%s", err, o)
	}
	info, _ := runBin(bin, "info", out)
	if !strings.Contains(string(info), "Magnification: 10x") {
		t.Errorf("expected 10x in info:\n%s", info)
	}
}
```

- [ ] **Step 2: Run — confirm FAIL** (`-run TestConvertFactorOMETIFF -v`).

- [ ] **Step 3: Implement** — in the OME-XML generation path used by `runConvertFactor` for `target=="ome-tiff"`, scale `PhysicalSizeX`/`PhysicalSizeY` by `factor` and use the reduced L0 dims. Locate the OME-XML builder used by `runConvertTIFFReencode` (`md.MPP`-fed `<Pixels PhysicalSizeX=...>`); pass the scaled `md.MPP` (already `×factor`) so the OME-XML carries the reduced resolution.

- [ ] **Step 4: Run — PASS** (`-run TestConvertFactorOMETIFF -v`). Confirm the output reads back as ome-tiff with the scaled pyramid.

- [ ] **Step 5: Commit** — `feat(convert): --factor for ome-tiff (scale OME-XML PhysicalSize)`.

---

## Task 6: `downsample` becomes a thin alias

**Files:** `cmd/wsitools/downsample.go`, test.

- [ ] **Step 1: Write the alias-parity test**

```go
func TestDownsampleAliasEqualsConvert(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	a := filepath.Join(t.TempDir(), "a.svs")
	b := filepath.Join(t.TempDir(), "b.svs")
	if o, err := runBin(bin, "downsample", "--factor", "2", "--quiet", "-f", "-o", a, src); err != nil {
		t.Fatalf("downsample: %v\n%s", err, o)
	}
	if o, err := runBin(bin, "convert", "--to", "svs", "--factor", "2", "-f", "-o", b, src); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	if pixelDigest(mustHash(t, bin, a)) != pixelDigest(mustHash(t, bin, b)) {
		t.Error("downsample alias output != convert --to svs --factor output")
	}
}

func mustHash(t *testing.T, bin, path string) []byte {
	t.Helper()
	o, err := runBin(bin, "hash", "--mode", "pixel", path)
	if err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return o
}
```

- [ ] **Step 2: Run — should already PASS** (both run the same engine after Task 3). If it passes, the alias is effectively already in place via shared code; proceed to make `downsample`'s `RunE` *delegate* to keep one path.

- [ ] **Step 3: Make `downsample` delegate**

Reimplement `downsample.go`'s `RunE` to: map its flags (`--factor`/`--target-mag`/`--quality`/`--jobs`→workers/`--tile-order`/`--max-memory`) onto the convert-factor engine with `target="svs"`, calling `runConvertFactor(cmd, input, "svs", dsFactor, start)` (or a shared lower-level entry that both commands call). Remove `downsample.go`'s now-duplicate pyramid-materialize/write code that Task 3 already centralized; keep the command, its flags, and help text. The SVS-only restriction stays (it's the SVS shortcut).

- [ ] **Step 4: Run — PASS** (`-run 'Downsample|DownsampleAlias' -v`) — downsample behavior unchanged.

- [ ] **Step 5: Commit** — `refactor(downsample): delegate to convert --to svs --factor (thin alias)`.

---

## Task 7: docs

**Files:** `CHANGELOG.md`, `README.md`.

- [ ] **Step 1: CHANGELOG** — under `## [Unreleased] ### Added`:
```markdown
- `convert --factor N` / `--target-mag M` — downsample while converting, for
  `--to svs|tiff|ome-tiff|cog-wsi`, with correctly-scaled MPP (×N) and
  magnification (÷N). `dzi`/`szi` not yet supported. `downsample` is now a thin
  alias for `convert --to svs --factor N` (behavior unchanged).
```

- [ ] **Step 2: README** — in the format×command matrix, `downsample` column note / `convert (to)` footnote: mention `--factor` is available for the TIFF-family targets; downsample is the SVS shortcut. Update the `downsample` usage line to note it aliases `convert --to svs --factor`.

- [ ] **Step 3: Commit** — `docs: convert --factor + downsample alias`.

---

## Self-review

**Spec coverage:**
- shared `internal/downscale` engine → Task 1 ✓
- `--factor`/`--target-mag` flags + dzi/szi guard + force-jpeg → Tasks 2-3 ✓
- reduce-then-rebuild from L0 → Task 3 (via `MaterializeReducedL0`) ✓
- per-target metadata scaling: svs Aperio → Task 3; tiff/cog-wsi scaled md → Task 4; ome-tiff PhysicalSize → Task 5 ✓
- downsample as non-breaking alias → Task 6 ✓
- {2,4,8,16} + target-mag parity → Task 3 resolution ✓
- tests (parity, per-target dims+metadata, errors) → Tasks 2-6 ✓
- docs → Task 7 ✓

**Placeholders:** the move/extract steps reference exact existing functions (`materializeOutputL0`, `downsampleByPowerOf2`, `buildPyramid`, `MutateForDownsample`, `isValidFactor`) rather than reproducing them — these already exist in `cmd/wsitools/downsample.go`; the engineer relocates/reuses them, which is concrete. New logic (flags, factor dispatch, metadata scaling, OME-XML PhysicalSize) has concrete code or precise integration points + tests.

**Type consistency:** `MaterializeReducedL0`/`BoxHalve`/`DecodeReducedTile` exported names used consistently; `runConvertFactor(cmd, input, target, factor, start)` signature consistent across Tasks 3-6; `pixelDigest` reused from existing tests.

**Sequencing:** Task 1 (extract) → 2 (flags) → 3 (svs path, the core) → 4/5 (more targets) → 6 (alias) → 7 (docs). Each task ships independently and keeps the suite green.
