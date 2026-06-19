# SP3c Phase 1 — Slice 3b: `convert --rect --factor` (non-SVS) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `convert --rect --factor N` crop **and** downsample in one pass for
the non-SVS containers (tiff/ome-tiff/cog-wsi/dicom), by threading a `factor`
through the crop emitters (output L0 = rect/factor, metadata scaled MPP×factor /
mag÷factor).

**Architecture:** The crop emitters already stream `srcRegion = rect` → `OutL0 =
rect` through the engine (`buildEnginePyramid(rect, {ew,eh})`). Adding `factor`
makes `OutL0 = {ew/factor, eh/factor}` — the engine box-reduces from the rect to
the smaller output. `cropEmitParams` gains `factor`, `outW`, `outH`; the lossy
branch of each non-SVS emitter uses `outW/outH` for writer geometry + `OutL0` and
scales MPP/mag by `factor`. `factor=1` reproduces today's plain-crop output
exactly (×1 is identity), so existing crop behavior is preserved.

**Scope / deferred:**
- **In:** `convert --rect --factor|--target-mag` for **tiff/ome-tiff/cog-wsi/
  dicom**, lossy, jpeg.
- **Deferred (own slices):** **SVS** crop+downsample — `BuildCropImageDescription`
  appends the source desc *verbatim* (stale MPP/AppMag for a downsampled output);
  the provenance approach (verbatim-stale vs synthetic-correct vs mutate-chain) is
  a design decision, not mechanical. **DZI/SZI `--rect`** (engine `SrcRegion=rect`
  into `emitDZIPyramid`). **`--codec` on the transform path** (Slice 3c). The
  `downsampleTo*`/`cropTo*` full source-merge is not required here (crop emitters
  just gain factor).
- `convert --rect --factor --to svs` (and SVS-default) keeps **hard-erroring** with
  a pointer to use `--to tiff` etc.

**Tech Stack:** Go, the retile engine (`buildEnginePyramid`/`buildEnginePyramidCOGWSI`/
`runDICOMEngine`).

**Spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-unified-convert-design.md`.

**Branch:** `feat/retile-engine-sp3c` (Slices 1/2/3a landed). Continue here.

---

### Task 1: Thread `factor` through the non-SVS crop emitters

**Files:**
- Modify: `cmd/wsitools/crop_formats.go` (`cropEmitParams` + `cropToTIFF`/
  `cropToOMETIFF`/`cropToCOGWSI`/`cropToDICOM` lossy branches)
- Modify: `cmd/wsitools/crop.go` (`runCrop` gains a `factor` param; computes
  `outW/outH/nLevels`; rejects `factor>1` for the SVS target)
- Test: `cmd/wsitools/crop_factor_test.go`

`cropEmitParams` currently carries `l0W/l0H` = the **source rect** dims (= `ew,eh`)
and uses them for both `SrcRegion` and `OutL0` (factor 1). Add `factor int`, `outW
int`, `outH int`. The lossy emitter branch uses `outW/outH` for `OutL0`, writer
geometry, and scaled metadata; `SrcRegion` stays `{ex,ey,l0W,l0H}`.

- [ ] **Step 1: Write a failing unit test for the scale helper**

Create `cmd/wsitools/crop_factor_test.go`:

```go
package main

import "testing"

func TestScaleMPPMag(t *testing.T) {
	// factor 1 = identity; factor 2 = MPP doubles, mag halves.
	mx, my, mag := scaleMPPMag(0.25, 0.25, 40, 1)
	if mx != 0.25 || my != 0.25 || mag != 40 {
		t.Fatalf("factor 1 must be identity, got %v,%v,%v", mx, my, mag)
	}
	mx, my, mag = scaleMPPMag(0.25, 0.25, 40, 2)
	if mx != 0.5 || my != 0.5 || mag != 20 {
		t.Fatalf("factor 2: got %v,%v,%v want 0.5,0.5,20", mx, my, mag)
	}
}

func TestCropOutDims(t *testing.T) {
	// outDimsForFactor floors source/factor (matches engine + flooredLevelCount).
	w, h := outDimsForFactor(2049, 1025, 2)
	if w != 1024 || h != 512 {
		t.Fatalf("got %dx%d want 1024x512", w, h)
	}
	w, h = outDimsForFactor(2048, 1024, 1)
	if w != 2048 || h != 1024 {
		t.Fatalf("factor 1 identity, got %dx%d", w, h)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`scaleMPPMag`/`outDimsForFactor` undefined)

Run: `go test ./cmd/wsitools/ -run 'ScaleMPPMag|CropOutDims'`

- [ ] **Step 3: Add the helpers**

In `cmd/wsitools/crop_formats.go` (top, after imports):

```go
// scaleMPPMag scales resolution metadata for a downsample by `factor`: MPP grows,
// magnification shrinks. factor 1 is the identity (plain crop preserves
// resolution), so the same call serves both crop and crop+downsample.
func scaleMPPMag(mppX, mppY, mag float64, factor int) (float64, float64, float64) {
	f := float64(factor)
	return mppX * f, mppY * f, mag / f
}

// outDimsForFactor floors source dims by factor (matches the engine's box-reduce
// and flooredLevelCount). factor 1 returns the dims unchanged.
func outDimsForFactor(w, h, factor int) (int, int) {
	return w / factor, h / factor
}
```

- [ ] **Step 4: Add fields to `cropEmitParams`**

In `cmd/wsitools/crop_formats.go`, add to the struct (after `force bool`):

```go
	factor       int
	outW, outH   int
```

- [ ] **Step 5: Use `outW/outH` + scaled metadata in each non-SVS lossy branch**

For `cropToTIFF`, `cropToOMETIFF`, `cropToCOGWSI` — in each, the **lossy** (`else`)
branch and the writer setup change as follows (the lossless branch is untouched;
lossless never has factor>1):

1. Scale metadata right after `cropSourceScale`:
   ```go
   mppX, mppY, mag := cropSourceScale(p.input, p.src)
   mppX, mppY, mag = scaleMPPMag(mppX, mppY, mag, p.factor)
   ```
2. Use `p.outW/p.outH` (not `p.l0W/p.l0H`) for: the `streamwriterBigTIFF`/BigTIFF
   prediction call, the writer geometry, and `cropToOMETIFF`'s `thumbDims` +
   `SyntheticOMEDescriptionWithMag(uint32(p.outW), uint32(p.outH), ...)`.
3. The lossy engine call changes its `OutL0` from `{p.l0W,p.l0H}` to
   `{p.outW,p.outH}` while `SrcRegion` stays the rect:
   ```go
   rect := opentile.Region{Origin: opentile.Point{X: p.ex, Y: p.ey}, Size: opentile.Size{W: p.l0W, H: p.l0H}}
   ...
   if err := buildEnginePyramid(p.ctx, p.src, w, rect, opentile.Size{W: p.outW, H: p.outH}, p.quality, p.workers, postL0Hook); err != nil {
   ```
   (cog-wsi uses `buildEnginePyramidCOGWSI` — same `OutL0` change.)
4. The `imageDesc`/`SourceImageDesc` strings that interpolate mpp/mag now use the
   scaled values (they already read the `mppX/mag` locals — no extra change once
   step 1 scales them). The thumbnail `streamCropThumbnail(p.src, rect, p.l0W,
   p.l0H, p.quality)` stays keyed on the **rect** (it renders a small downscale of
   the source region; aspect is preserved) — do NOT change its args.

For `cropToDICOM` lossy branch: scale the DICOM metadata before
`runDICOMEngine`. After `md := src.Metadata()`:
```go
   md.MPP.X, md.MPP.Y, md.Magnification = scaleMPPMag(md.MPP.X, md.MPP.Y, md.Magnification, p.factor)
```
and the lossy `runDICOMEngine(p.ctx, p.src, rect, opentile.Size{W: p.outW, H: p.outH}, ...)` (OutL0 = outW×outH; SrcRegion rect unchanged). The lossless branch (passthrough) is untouched.

- [ ] **Step 6: Compute `factor/outW/outH/nLevels` in `runCrop`; guard SVS**

In `cmd/wsitools/crop.go`, give `runCrop` a `factor int` param (insert before
`target string`):
```go
func runCrop(ctx context.Context, input, output string, x, y, w, h, quality, workers, factor int, tileOrderName, bigtiffFlag string, force, noAssociated, lossless bool, target string, start time.Time) error {
```
Validate + apply:
```go
	if factor < 1 {
		factor = 1
	}
	if factor != 1 && lossless {
		return fmt.Errorf("--lossless cannot be combined with downsampling")
	}
	if factor != 1 && target == "svs" {
		return fmt.Errorf("crop+downsample to SVS is not yet supported; use --to tiff|ome-tiff|cog-wsi|dicom")
	}
```
Compute output dims + nLevels from the **reduced** extent (replace the existing
`nLevels := flooredLevelCount(ew, eh, outputTileSize)` line):
```go
	outW, outH := outDimsForFactor(ew, eh, factor)
	if outW <= 0 || outH <= 0 {
		return fmt.Errorf("--factor %d too large for crop extent %dx%d", factor, ew, eh)
	}
	nLevels := flooredLevelCount(outW, outH, outputTileSize)
```
Add `factor: factor, outW: outW, outH: outH,` to the `cropEmitParams` literal.

Update both `runCrop` call sites to pass a `factor`:
- crop `RunE` (crop.go): pass `1` (crop never downsamples). Find the call and insert
  `1` in the new `factor` position (after `workers`).
- (the convert call site is added in Task 2.)

The `cropEmitSVS` dispatch (`if target == "svs"`) is reached only when `factor==1`
(guarded above), so `cropEmitSVS` is unchanged.

- [ ] **Step 7: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ScaleMPPMag|CropOutDims|Crop|crop'`
Expected: PASS (plain crop unchanged — factor 1 is identity). `gofmt -l` edited files → clean.

- [ ] **Step 8: Commit**

```bash
git add cmd/wsitools/crop.go cmd/wsitools/crop_formats.go cmd/wsitools/crop_factor_test.go
git commit -m "$(cat <<'EOF'
feat(crop): thread factor through non-SVS crop emitters (crop+downsample)

cropEmitParams gains factor/outW/outH; the lossy tiff/ome-tiff/cog-wsi/
dicom branches box-reduce the cropped region to rect/factor and scale
MPP×factor / mag÷factor. factor 1 is identity (plain crop unchanged).
SVS crop+downsample guarded off (Aperio provenance is a separate slice).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Wire `convert --rect --factor`

**Files:**
- Modify: `cmd/wsitools/convert.go` (`validateRectCombo` allows factor for non-SVS
  non-dzi targets; `runConvert` resolves the factor and passes it to `runCrop`)
- Test: extend `cmd/wsitools/convert_rect_test.go`

- [ ] **Step 1: Update the combo-guard test**

In `cmd/wsitools/convert_rect_test.go`, change `validateRectCombo`'s contract: it
now ALLOWS `--rect --factor` for non-svs/non-dzi targets, still REJECTS for svs and
dzi/szi, and still rejects `--codec`. Replace `TestConvertRectComboGuards`/
`TestConvertRectComboAllowed` with:

```go
func TestConvertRectComboGuards(t *testing.T) {
	cases := []struct {
		name, codec, to, wantSub string
		rectSet                   bool
		factor, targetMag         int
	}{
		{"rect+factor+svs", "", "svs", "crop+downsample to SVS", true, 2, 0},
		{"rect+targetmag+svs", "", "svs", "crop+downsample to SVS", true, 1, 20},
		{"rect+factor+dzi", "", "dzi", "--rect with --to dzi", true, 2, 0},
		{"rect+codec", "avif", "tiff", "--rect with --codec", true, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRectCombo(c.rectSet, c.factor, c.targetMag, c.codec, c.to)
			if err == nil || !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("err=%v want substring %q", err, c.wantSub)
			}
		})
	}
}

func TestConvertRectComboAllowed(t *testing.T) {
	// crop+downsample into a non-SVS container is now allowed.
	if err := validateRectCombo(true, 2, 0, "", "tiff"); err != nil {
		t.Fatalf("rect+factor+tiff should be allowed, got %v", err)
	}
	if err := validateRectCombo(true, 1, 0, "", "dicom"); err != nil {
		t.Fatalf("plain rect+dicom should be allowed, got %v", err)
	}
	if err := validateRectCombo(false, 2, 0, "avif", "dzi"); err != nil {
		t.Fatalf("no rect = always allowed, got %v", err)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (guard still rejects rect+factor universally)

Run: `go test ./cmd/wsitools/ -run TestConvertRectCombo`

- [ ] **Step 3: Update `validateRectCombo`**

Replace the factor guard in `validateRectCombo` (convert.go) so factor is allowed
except for the SVS and dzi/szi targets:

```go
func validateRectCombo(rectSet bool, factor, targetMag int, codec, to string) error {
	if !rectSet {
		return nil
	}
	if codec != "" {
		return fmt.Errorf("--rect with --codec is not yet supported")
	}
	if to == "dzi" || to == "szi" {
		return fmt.Errorf("--rect with --to %s is not yet supported", to)
	}
	if (factor != 1 || targetMag != 0) && to == "svs" {
		return fmt.Errorf("crop+downsample to SVS is not yet supported; use --to tiff|ome-tiff|cog-wsi|dicom")
	}
	return nil
}
```

- [ ] **Step 4: Resolve the factor and pass it to `runCrop`**

In `runConvert`'s rect block, resolve the effective factor from `--factor`/
`--target-mag` (reuse the existing `resolveFactor` helper used by dzi — it takes
`(src source.Source, input string, factor, targetMag int)`; open the source to pass
it, OR reuse the already-validated `cvFactor` when `--target-mag` is unset). Use
this minimal form that matches dzi's pattern:

```go
		f, err := opentile.OpenFile(input)
		if err != nil {
			return fmt.Errorf("open source: %w", err)
		}
		factor, ferr := resolveFactor(source.FromSlide(f, input), input, cvFactor, cvTargetMag)
		_ = f.Close()
		if ferr != nil {
			return ferr
		}
		rx, ry, rw, rh, err := resolveRectValues(cmd, cvRect, cvRectX, cvRectY, cvRectW, cvRectH)
		if err != nil {
			return err
		}
		workers := cvWorkers
		return runCrop(cmd.Context(), input, cvOutput, rx, ry, rw, rh,
			qualityIntForConvert(), workers, factor, cvTileOrder, cvBigTIFFFlag, cvForce, cvNoAssociated, false, cvTo, start)
```

(Confirm `resolveFactor`'s exact signature in `convert_dzi.go` and the
`source.FromSlide` helper before wiring; if `resolveFactor` takes `*opentile.Slide`
instead of a `source.Source`, pass `f` directly. Match reality.)

- [ ] **Step 5: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ConvertRect|Convert|Crop'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/convert_rect_test.go
git commit -m "$(cat <<'EOF'
feat(convert): --rect --factor crops and downsamples in one pass

validateRectCombo now allows --rect with --factor/--target-mag for
non-SVS containers; runConvert resolves the factor and threads it to the
crop emitters. SVS crop+downsample and dzi/szi rect still guarded.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`.

- [ ] **Step 2: crop+downsample in one pass**

```bash
./bin/wsitools convert --to tiff --rect 0,0,4096,4096 --factor 2 -o /tmp/sp3c-s3b.tiff sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools info /tmp/sp3c-s3b.tiff
```
Expected: L0 = 2048×2048 (4096/2); MPP doubled, magnification halved vs the source.

- [ ] **Step 3: one-pass ≡ crop-then-downsample (pixel parity)**

```bash
./bin/wsitools crop --rect 0,0,4096,4096 -o /tmp/sp3b-crop.tiff sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools downsample --factor 2 -o /tmp/sp3b-seq.tiff /tmp/sp3b-crop.tiff
./bin/wsitools convert --to tiff --rect 0,0,4096,4096 --factor 2 -o /tmp/sp3b-fused.tiff sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools hash --mode pixel /tmp/sp3b-seq.tiff
./bin/wsitools hash --mode pixel /tmp/sp3b-fused.tiff
```
Expected: pixel hashes match (fused == sequential), confirming the fused path is
correct. (Box-reduce of a cropped region == crop then box-reduce.)

- [ ] **Step 4: dicom crop+downsample + SVS guard**

```bash
./bin/wsitools convert --to dicom --rect 0,0,4096,4096 --factor 2 -o /tmp/sp3b-dcm sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
./bin/wsitools convert --to svs --rect 0,0,4096,4096 --factor 2 -o /tmp/x.svs sample_files/svs/CMU-1-Small-Region.svs 2>&1 | grep -i "not yet supported"
```
Expected: dicom succeeds; svs errors with the pointer.

- [ ] **Step 5: Clean up** `/tmp/sp3b-*` + `/tmp/sp3c-s3b*`.

---

## Self-review

**Spec coverage (Slice 3b scope):** `convert --rect --factor` for non-SVS targets
(Task 1 emitters + Task 2 wiring); metadata scaled (Task 1 `scaleMPPMag`); output
dims floored (Task 1 `outDimsForFactor`); factor 1 = identity (preserves crop).
SVS crop+downsample + dzi/szi rect explicitly deferred + guarded. ✓

**Placeholder scan:** none — the one signature to confirm (`resolveFactor`) is
flagged with "match reality" and a fallback.

**Type consistency:** `runCrop` gains `factor int` (after `workers`, before
`tileOrderName`); both call sites updated (crop passes 1; convert passes resolved
factor). `cropEmitParams` gains `factor/outW/outH int`. `scaleMPPMag(mppX,mppY,mag
float64, factor int)`, `outDimsForFactor(w,h,factor int) (int,int)`,
`validateRectCombo(rectSet bool, factor, targetMag int, codec, to string) error`.

## Boundaries

**In Slice 3b:** `convert --rect --factor|--target-mag` for tiff/ome-tiff/cog-wsi/
dicom; metadata scaling; factor-1 identity preserved.

**Deferred:** SVS crop+downsample (Aperio provenance design), DZI/SZI `--rect`,
`--codec` on the transform path (3c), the `transformTo*` source merge.
