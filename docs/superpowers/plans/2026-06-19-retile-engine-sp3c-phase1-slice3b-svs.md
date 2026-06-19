# SP3c Phase 1 — Slice 3b-SVS: SVS crop+downsample (Aperio chain mutation) — Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Enable `convert --to svs --rect ... --factor N` (and SVS-default) by making
`cropEmitSVS` factor-aware: box-reduce the cropped region to rect/factor and scale
the magnification/resolution — including the `AppMag`/`MPP` tokens inside the
crop description's appended Aperio provenance chain (the user's chosen
"mutate the chain" approach).

**Why the chain:** `BuildCropImageDescription` appends the source Aperio
description verbatim (`...Q=q;<source-chain>`). The chain holds `|AppMag = N`
(and sometimes `|MPP = M`) — opentile reads magnification from there. For a
*downsampled* crop those are stale; we must scale them (`AppMag ÷ factor`, `MPP ×
factor`). Pixel-dimension tokens (the geometry `WxH` and `OriginalWidth/Height`)
are **untouched** — they are provenance, not resolution. Reference:
`sample_files/svs/CMU-2_cropped_..._imagescope.svs` shows the canonical crop format
(`<base> [x,y WxH] (tile) JPEG/RGB Q=q;<chain>|AppMag = 20|...`).

**Scope:** `convert --to svs --rect --factor` + the SVS-default path. Lossy only
(`--lossless` + factor stays a contradiction). factor 1 must remain byte-identical
to today's plain SVS crop.

**Branch:** `feat/retile-engine-sp3c` (Slices 1/2/3a/3b landed). Continue here.

---

### Task 1: Aperio resolution-token scaler

**Files:**
- Modify: `cmd/wsitools/svs_imagedesc.go` (add `scaleAperioResolutionTokens`)
- Test: `cmd/wsitools/svs_imagedesc_test.go` (add cases)

- [ ] **Step 1: Write the failing test**

Append to `cmd/wsitools/svs_imagedesc_test.go`:

```go
func TestScaleAperioResolutionTokens(t *testing.T) {
	// factor 1 = identity.
	in := "78000x30462 [0,0 78000x30462] (256x256) JPEG/RGB Q=30|AppMag = 20|MPP = 0.499|OriginalWidth = 78000"
	if got := scaleAperioResolutionTokens(in, 1); got != in {
		t.Fatalf("factor 1 must be identity:\n got %q", got)
	}
	// factor 2: AppMag halves (20->10), MPP doubles (0.499->0.998); dims untouched.
	got := scaleAperioResolutionTokens(in, 2)
	if !strings.Contains(got, "AppMag = 10") {
		t.Errorf("AppMag not halved: %q", got)
	}
	if !strings.Contains(got, "MPP = 0.998") {
		t.Errorf("MPP not doubled: %q", got)
	}
	if !strings.Contains(got, "78000x30462") || !strings.Contains(got, "OriginalWidth = 78000") {
		t.Errorf("pixel dims must be untouched: %q", got)
	}
}

func TestScaleAperioResolutionTokens_NoMPP(t *testing.T) {
	// CMU-2 style: AppMag present, no MPP token. Must scale AppMag, not crash.
	in := "27836x25633 [46492,3599 27836x25633] (256x256) JPEG/RGB Q=30|AppMag = 20|StripeWidth = 2040"
	got := scaleAperioResolutionTokens(in, 2)
	if !strings.Contains(got, "AppMag = 10") {
		t.Errorf("AppMag not halved: %q", got)
	}
}
```

(Confirm `strings` is imported in the test file; add if missing.)

- [ ] **Step 2: Run — expect FAIL** (`scaleAperioResolutionTokens` undefined)

Run: `go test ./cmd/wsitools/ -run TestScaleAperioResolutionTokens`

- [ ] **Step 3: Implement the scaler**

Add to `cmd/wsitools/svs_imagedesc.go` (it already imports `fmt`, `strconv`,
`strings`; add `regexp`):

```go
// aperioResTokenRE matches an Aperio "AppMag = <n>" or "MPP = <n>" resolution
// token. The key is anchored to a left boundary (start, '|', ';', or space) so
// it never matches inside another key (e.g. a hypothetical "FooMPP"). The numeric
// value is captured for scaling.
var aperioResTokenRE = regexp.MustCompile(`(^|[|;\s])(AppMag|MPP)(\s*=\s*)([0-9.]+)`)

// scaleAperioResolutionTokens scales every AppMag (×appMagMul) and MPP
// (×mppMul) value in an Aperio description so a downsampled crop reports the
// correct magnification/resolution. Pixel-dimension tokens (geometry "WxH",
// OriginalWidth/Height) are left untouched. factor 1 is the identity.
func scaleAperioResolutionTokens(desc string, factor int) string {
	if factor <= 1 {
		return desc
	}
	f := float64(factor)
	return aperioResTokenRE.ReplaceAllStringFunc(desc, func(m string) string {
		sub := aperioResTokenRE.FindStringSubmatch(m)
		// sub[1]=boundary, sub[2]=key, sub[3]="=", sub[4]=number
		v, err := strconv.ParseFloat(sub[4], 64)
		if err != nil {
			return m
		}
		switch sub[2] {
		case "AppMag":
			v = v / f
		case "MPP":
			v = v * f
		}
		return sub[1] + sub[2] + sub[3] + formatAperioFloat(v)
	})
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./cmd/wsitools/ -run TestScaleAperioResolutionTokens`

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/svs_imagedesc.go cmd/wsitools/svs_imagedesc_test.go
git commit -m "$(cat <<'EOF'
feat(svs): scale AppMag/MPP tokens in an Aperio description

scaleAperioResolutionTokens scales AppMag÷factor / MPP×factor wherever
they appear (anchored to a key boundary), leaving pixel-dimension tokens
untouched. Backs SVS crop+downsample's provenance-chain mutation.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `cropEmitSVS` factor support; drop the SVS guards

**Files:**
- Modify: `cmd/wsitools/crop.go` (`cropEmitSVS` gains `factor`; the `runCrop`
  `factor!=1 && target=="svs"` guard removed; the `cropEmitSVS` call passes
  `factor`)
- Modify: `cmd/wsitools/convert.go` (`validateRectCombo` drops the `to=="svs"`
  factor rejection)
- Test: extend `cmd/wsitools/convert_rect_test.go`

- [ ] **Step 1: Add `factor` to `cropEmitSVS` and apply it**

In `cmd/wsitools/crop.go`, change `cropEmitSVS`'s signature to take `factor int`
(insert after `quality, workers int` — match the existing param list):

```go
func cropEmitSVS(ctx context.Context, src *opentile.Slide, input, output string, x, y, w, h, quality, workers, factor int, order tileorder.OrderStrategy, bigtiffFlag string, noAssociated, lossless bool, start time.Time) error {
```

Inside, after the effective rect (`ex,ey,ew,eh`) is resolved and BEFORE building
the writer, compute output dims and scale the description + metadata:

```go
	outW, outH := outDimsForFactor(ew, eh, factor)
	cropDesc = scaleAperioResolutionTokens(cropDesc, factor)
	outMPP := desc.MPP * float64(factor)
	outMag := desc.AppMag / float64(factor)
```

Then in the `streamwriter.Create(... Options{...})`, replace `MPPX: desc.MPP,
MPPY: desc.MPP, Magnification: desc.AppMag` with `MPPX: outMPP, MPPY: outMPP,
Magnification: outMag`. (At factor 1 these equal the source values — identity.)

Replace the lossy-branch engine call's `OutL0` and the `nLevels`:
```go
	nLevels := flooredLevelCount(outW, outH, outputTileSize)
	...
	// lossy (else) branch:
	if err := buildEnginePyramid(ctx, src, wtr, rect, opentile.Size{W: outW, H: outH}, quality, workers, postL0Hook); err != nil {
```
`rect` (SrcRegion) stays `{ex,ey,ew,eh}`. The lossless branch is unreachable with
factor>1 (guarded in runCrop) — leave it using `ew,eh` (factor 1 ⇒ outW==ew).
NOTE: `cropDesc` is currently assigned with `:=`; change its declaration so the
`scaleAperioResolutionTokens` reassignment compiles (use `=` after the initial
`cropDesc := BuildCropImageDescription(...)`, or wrap as shown).

- [ ] **Step 2: Drop the SVS factor guard in `runCrop`; pass `factor` to `cropEmitSVS`**

In `runCrop`, delete the guard:
```go
	if factor != 1 && target == "svs" {
		return fmt.Errorf("crop+downsample to SVS is not yet supported; use --to tiff|ome-tiff|cog-wsi|dicom")
	}
```
and update the `cropEmitSVS(...)` call to pass `factor` (in the new position):
```go
		return cropEmitSVS(ctx, src, input, output, x, y, w, h, quality, workers, factor, order, bigtiffFlag, noAssociated, lossless, start)
```
Keep the `factor != 1 && lossless` guard (lossless+downsample is still a
contradiction).

- [ ] **Step 3: Drop the SVS rejection in `validateRectCombo`**

In `cmd/wsitools/convert.go`, remove the block:
```go
	if (factor != 1 || targetMag != 0) && to == "svs" {
		return fmt.Errorf("crop+downsample to SVS is not yet supported; use --to tiff|ome-tiff|cog-wsi|dicom")
	}
```

- [ ] **Step 4: Update the combo tests**

In `cmd/wsitools/convert_rect_test.go`, the `rect+factor+svs` and
`rect+targetmag+svs` rows in `TestConvertRectComboGuards` now must NOT error —
move them to `TestConvertRectComboAllowed`:

```go
	if err := validateRectCombo(true, 2, 0, "", "svs"); err != nil {
		t.Fatalf("rect+factor+svs is now allowed, got %v", err)
	}
```
Remove the two SVS rows from the guards table (keep dzi/szi + codec rows). Keep the
existing `factor=1+svs` allowed assertion.

- [ ] **Step 5: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ConvertRect|Crop|crop|Aperio'`
Expected: PASS. `gofmt -l` edited files → clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/crop.go cmd/wsitools/convert.go cmd/wsitools/convert_rect_test.go
git commit -m "$(cat <<'EOF'
feat(convert): SVS crop+downsample (scaled Aperio chain + metadata)

cropEmitSVS gains factor: box-reduces the cropped region to rect/factor,
scales MPPX/Mag and the AppMag/MPP tokens in the crop description's
provenance chain. Drops the SVS crop+downsample guards. factor 1 is
byte-identical to plain SVS crop.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`.

- [ ] **Step 2: SVS crop+downsample, read-back magnification**

```bash
./bin/wsitools convert --to svs --rect 0,0,2048,2048 --factor 2 -o /tmp/sp3bsvs.svs sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools info /tmp/sp3bsvs.svs
```
Expected: L0 = 1024×1024; **Magnification 10x** (halved from 20x), **MPP ~0.998**
(doubled) — read back through opentile from the mutated Aperio chain.

- [ ] **Step 3: full-rect SVS crop+downsample ≡ plain downsample (pixel parity)**

```bash
./bin/wsitools convert --to svs --factor 2 -o /tmp/sp3bsvs-A.svs sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools convert --to svs --rect 0,0,2220,2967 --factor 2 -o /tmp/sp3bsvs-B.svs sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools hash --mode pixel /tmp/sp3bsvs-A.svs
./bin/wsitools hash --mode pixel /tmp/sp3bsvs-B.svs
```
Expected: pixel hashes match (full-rect crop+downsample == plain downsample).

- [ ] **Step 4: plain SVS crop (factor 1) byte-unchanged**

```bash
./bin/wsitools convert --to svs --rect 0,0,2048,2048 -o /tmp/sp3bsvs-c1.svs sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools crop --rect 0,0,2048,2048 -o /tmp/sp3bsvs-crop.svs sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools hash --mode pixel /tmp/sp3bsvs-c1.svs
./bin/wsitools hash --mode pixel /tmp/sp3bsvs-crop.svs
```
Expected: identical (factor-1 convert --rect ≡ crop).

- [ ] **Step 5: Clean up** `/tmp/sp3bsvs*`.

---

## Self-review

**Spec coverage:** `scaleAperioResolutionTokens` (Task 1) scales AppMag/MPP in the
chain, dims untouched; `cropEmitSVS` factor support (Task 2) box-reduces + scales
metadata + mutates the desc; guards dropped (runCrop + validateRectCombo); factor-1
identity preserved (×1 / ÷1, `factor<=1` early return in the scaler).

**Placeholder scan:** none. **Type consistency:** `cropEmitSVS` gains `factor int`
(after `workers`); call site updated. `scaleAperioResolutionTokens(desc string,
factor int) string`. `outDimsForFactor` (Slice 3b) reused.

**Read-back oracle:** Task 3 Step 2 verifies opentile reads the *scaled* AppMag/MPP
from the mutated chain — the whole point of the chain mutation.

## Boundaries

**In:** SVS crop+downsample via chain mutation. **Deferred:** DZI/SZI `--rect`,
`--codec` on the transform path (3c), `transcode` revival (Slice 4).
