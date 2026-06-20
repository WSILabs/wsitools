# SP3c — finish the codec story — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Checkbox (`- [ ]`) steps.

**Goal:** (1) **unify SVS into the shared crop/downsample path** (remove the bespoke
positional `cropEmitSVS`) so SVS gains jpeg+jpeg2000 like every other container;
(2) every pixel re-encode defaults to the codec standard (jpeg 90), never source-Q;
(3) `--quality reversible=true` works through `--factor`.

**Spec:** `docs/superpowers/specs/2026-06-19-sp3c-codec-story-finish-design.md`.

**Branch:** `feat/sp3c-codec-story-finish` (off main@33c23f2).

---

### Task 1: re-encode quality default → codec standard (#2 + #3) — SHIPPED

Commits `00157ec` + `814a023`. `parseQualityKnobs` default 85→90; `cropEmitSVS` drops
source-Q for the codec default; `runConvertFactor` parses `--quality` via
`parseQualityKnobs` (not `Sscanf`) so `reversible=true` flows through `--factor`.

---

### Task 2: Aperio codec descriptor + builders take a codec

**Files:** `cmd/wsitools/svs_imagedesc.go`, `cmd/wsitools/svs_imagedesc_test.go`, +
all `BuildCropImageDescription`/`SyntheticAperioDescription` callers.

Foundational, no behavior change (callers pass `"jpeg"` → descriptor stays
`JPEG/RGB`).

- [ ] **Step 1: Descriptor helpers + test**

Add to `svs_imagedesc.go` (ensure `strings` imported):

```go
// aperioCodecDescriptor maps an output codec to the Aperio geometry-line codestream
// descriptor: jpeg → "JPEG/RGB", jpeg2000 → "J2K/YUV16" (per Aperio's J2K SVS, e.g.
// JP2K-33003-1.svs). Only the conformant SVS codecs are handled.
func aperioCodecDescriptor(codec string) string {
	if codec == "jpeg2000" {
		return "J2K/YUV16"
	}
	return "JPEG/RGB"
}

// setAperioCodecDescriptor rewrites the codestream descriptor token on a parsed
// Aperio description's geometry line (after MutateForDownsample, when the output
// codec differs from the source's).
func setAperioCodecDescriptor(d *AperioDescription, codec string) {
	want := aperioCodecDescriptor(codec)
	for _, old := range []string{"JPEG/RGB", "J2K/YUV16"} {
		if strings.Contains(d.GeometryLine, old) {
			d.GeometryLine = strings.Replace(d.GeometryLine, old, want, 1)
			return
		}
	}
}
```

Test (`svs_imagedesc_test.go`):

```go
func TestAperioCodecDescriptor(t *testing.T) {
	if aperioCodecDescriptor("jpeg") != "JPEG/RGB" || aperioCodecDescriptor("jpeg2000") != "J2K/YUV16" {
		t.Fatal("descriptor mapping wrong")
	}
	d := &AperioDescription{GeometryLine: "1000x1000 (256x256) JPEG/RGB Q=90"}
	setAperioCodecDescriptor(d, "jpeg2000")
	if !strings.Contains(d.GeometryLine, "J2K/YUV16") || strings.Contains(d.GeometryLine, "JPEG/RGB") {
		t.Fatalf("not rewritten: %q", d.GeometryLine)
	}
}
```

- [ ] **Step 2: Run — FAIL → implement → PASS**

`go test ./cmd/wsitools/ -run TestAperioCodecDescriptor`

- [ ] **Step 3: `BuildCropImageDescription` + `SyntheticAperioDescription` take `codec string`**

Add a trailing `codec string` param to both; replace the hardcoded `JPEG/RGB` with
`aperioCodecDescriptor(codec)`. Update every caller (`grep -rn
"BuildCropImageDescription\|SyntheticAperioDescription" cmd/wsitools/`) to pass the
codec — `"jpeg"` at the current jpeg call sites (Tasks 3–4 pass the real codec).

- [ ] **Step 4: Build + test**

`go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'Aperio|Crop|Convert' -count=1` → PASS (jpeg callers byte-identical). `gofmt -l` clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/svs_imagedesc.go cmd/wsitools/svs_imagedesc_test.go cmd/wsitools/crop.go cmd/wsitools/convert_factor.go
git commit -m "$(cat <<'EOF'
feat(svs): Aperio codec descriptor (JPEG/RGB <-> J2K/YUV16)

Description builders take a codec and emit the matching geometry-line
codestream descriptor; jpeg callers unchanged. Wired to real codecs next.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Unify SVS into the crop dispatch — `cropToSVS(p cropEmitParams)`

**Files:** `cmd/wsitools/crop.go` (remove the SVS bypass + `cropEmitSVS`),
`cmd/wsitools/crop_formats.go` (add `cropToSVS`), tests.

`cropEmitSVS` (`crop.go:283`) is a positional-args emitter called via an early
`if target == "svs"` bypass in `runCrop` (`crop.go:218`). Fold it into a
`cropEmitParams` peer. Its body already matches `cropToTIFF` except for the Aperio
ImageDescription + MPP-from-Aperio-desc; and it has a **duplicate** lossless snap
that `runCrop` already does (`p.stx0`/`p.sty0`/`p.outTilesX`/`p.outTilesY`).

- [ ] **Step 1: Add `cropToSVS(p cropEmitParams)` in `crop_formats.go`**

Mirror `cropToTIFF(p)` (same lossless/lossy/associated structure, using
`p.fac`/`p.knobs` for the lossy engine, `p.stx0…` for lossless), with these
SVS-specific bits (lifted from `cropEmitSVS`):
```go
	rawDesc, _ := source.ReadSourceImageDescription(p.input)
	desc, derr := ParseImageDescription(rawDesc)
	if derr != nil {
		return fmt.Errorf("parse source ImageDescription: %w", derr)
	}
	cropDesc := BuildCropImageDescription(rawDesc, p.src.Levels()[0].Size.W, p.src.Levels()[0].Size.H,
		p.ex, p.ey, p.l0W, p.l0H, outputTileSize, outputTileSize, p.quality, p.codecName)
	cropDesc = scaleAperioResolutionTokens(cropDesc, p.factor)
	outMPP := desc.MPP * float64(p.factor)
	outMag := desc.AppMag / float64(p.factor)
```
and the writer Options use `FormatName: "svs"`, `ImageDescription: cropDesc`,
`MPPX/MPPY: outMPP`, `Magnification: outMag` (the SVS shape). Everything else —
`streamwriterBigTIFF(p.bigtiffFlag, p.outW, p.outH)`, the lossless branch
(`writeLosslessL0` + `regenCropThumbnail` + `buildPyramidFromRaster`), the lossy
branch (`buildEnginePyramid(p.ctx, p.src, w, rect, {p.outW,p.outH}, p.fac, p.knobs,
workers, postL0Hook)`), the associated loop (label/macro via `writeOneAssociated`,
thumbnail via the hook/regen) — copies `cropToTIFF`'s structure. (The base dims for
`BuildCropImageDescription` are the source L0 dims; the snap origin is `p.ex,p.ey`.)

NOTE: study `cropToTIFF` + the existing `cropEmitSVS` side by side; `cropToSVS` is
their union. Use `p.outW/p.outH` for output geometry (factor-aware, from 3b),
`p.l0W/p.l0H` as the source rect, `p.ex/p.ey` as the rect origin.

- [ ] **Step 2: Route svs through `cropEmitParams`; delete the bypass + `cropEmitSVS`**

In `runCrop` (`crop.go`):
- delete the `if target == "svs" { return cropEmitSVS(...) }` early-return
  (`crop.go:217-218`), so svs falls through to the `cropEmitParams` construction.
- add `case "svs": return cropToSVS(p)` to the dispatch `switch target` (`crop.go:266`).
- **delete the `cropEmitSVS` function** (and any now-unused imports it alone used,
  e.g. if `tiff`/`snapRectToTiles` usage drops — let the compiler guide).
- the SVS guard (`crop.go:214`) widens (Task 4 shares the message); for now keep it
  rejecting non-jpeg (Task 4 widens to jpeg|jpeg2000). Actually widen it here:
  `if target == "svs" && p? …` — the guard runs before the rect/param block, so it
  uses `codecName` (the resolved `cvCodec`); change `!= "jpeg"` to `!= "jpeg" &&
  != "jpeg2000"` with message `"SVS crop/downsample supports jpeg or jpeg2000; use
  --to tiff for %s"`.

- [ ] **Step 3: Byte-identity guard test for jpeg SVS crop**

The critical invariant: **jpeg SVS crop output is unchanged** by the refactor. Add a
controller-run note (Task 5) comparing `convert --to svs --rect …` pixel-hash before
vs after — OR a unit test if a fixture-free one is feasible. (At minimum: existing
SVS crop tests still pass.)

- [ ] **Step 4: Build + test**

`go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'Crop|crop|Convert|SVS' -count=1` → PASS. `gofmt -l` clean. Fix any test that referenced `cropEmitSVS` directly.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor(crop): unify SVS into cropEmitParams (cropToSVS peer)

Fold the bespoke positional cropEmitSVS into cropToSVS(p cropEmitParams),
a peer of cropToTIFF/OMETIFF in the dispatch switch. Removes the SVS
early-bypass + the duplicate lossless snap; SVS now gets fac/knobs (codec)
and the codec descriptor for free. jpeg SVS crop output unchanged. Guard
widened to jpeg|jpeg2000.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: `downsampleToSVS` codec threading (parity with `downsampleToTIFF`)

**Files:** `cmd/wsitools/convert_factor.go`, tests.

`downsampleToSVS` is already a peer of `downsampleToTIFF` (which takes `codecName`);
bring it to parity.

- [ ] **Step 1: thread `codecName`**

Add a `codecName string` param to `downsampleToSVS`; pass it from
`dispatchDownsampleByTarget`'s svs arm (it already receives `codecName`). Inside:
- `fac, knobs, resolvedCodec, err := resolveTransformCodec(codecName, cvQuality, quality)`.
- pass `fac, knobs` to `buildPyramid(...)` (replacing `jpegcodec.Factory{},
  {"q":Itoa(quality)}`).
- SVS-source path: after `desc.MutateForDownsample(...)` add
  `setAperioCodecDescriptor(&desc, resolvedCodec)`.
- non-SVS path: pass `resolvedCodec` to `SyntheticAperioDescription(...)`.

- [ ] **Step 2: widen the downsample SVS guard**

`convert_factor.go:84` (in `dispatchDownsampleByTarget`): `if codecName != "" &&
codecName != "jpeg" && codecName != "jpeg2000" { return fmt.Errorf("SVS
crop/downsample supports jpeg or jpeg2000; use --to tiff for %s", codecName) }`.

- [ ] **Step 3: Build + test**

`go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'Downsample|Convert|SVS' -count=1` → PASS. `gofmt -l` clean.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/convert_factor.go
git commit -m "$(cat <<'EOF'
feat(svs): downsampleToSVS threads --codec (jpeg2000)

Parity with downsampleToTIFF: resolve fac/knobs from --codec, emit the
codec-correct Aperio descriptor, widen the guard to jpeg|jpeg2000.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`. `SRC=sample_files/svs/CMU-1-Small-Region.svs`

- [ ] **Step 2: jpeg SVS crop/downsample UNCHANGED (byte-identity)** — the refactor
must not regress jpeg SVS. (If a pre-refactor binary was stashed, diff pixel hashes;
else verify against the known behavior + that crop≡downsample at q90.)
```bash
./bin/wsitools convert --to svs --rect 0,0,2048,2048 -o /tmp/cs-svs-j.svs "$SRC"
./bin/wsitools crop --rect 0,0,2048,2048 -o /tmp/cs-svs-crop.svs "$SRC"
./bin/wsitools hash --mode pixel /tmp/cs-svs-j.svs
./bin/wsitools hash --mode pixel /tmp/cs-svs-crop.svs   # convert --rect ≡ crop (identical)
```

- [ ] **Step 3: J2K SVS via crop + downsample**
```bash
./bin/wsitools convert --to svs --rect 0,0,2048,2048 --codec jpeg2000 -o /tmp/cs-crop.svs "$SRC"
./bin/wsitools info /tmp/cs-crop.svs | grep -iE "Format|jpeg2000"
./bin/wsitools convert --to svs --factor 2 --codec jpeg2000 -o /tmp/cs-ds.svs "$SRC"
./bin/wsitools dump-ifds --raw /tmp/cs-ds.svs 2>/dev/null | grep -i ImageDescription | head -1 | grep -o "J2K/YUV16"
./bin/wsitools hash --mode pixel /tmp/cs-ds.svs >/dev/null && echo "J2K SVS reads back"
```
Expected: re-detect as svs/jpeg2000; description `J2K/YUV16`; hashes OK.

- [ ] **Step 4: guard + quality + reversible**
```bash
./bin/wsitools convert --to svs --rect 0,0,512,512 --codec avif -o /tmp/x.svs "$SRC" 2>&1 | grep -i "jpeg or jpeg2000"
# crop ≡ downsample pixel parity at q90 (the Slice-3b mismatch is gone):
./bin/wsitools convert --to tiff --factor 2 -o /tmp/cs-A.tiff "$SRC"; ./bin/wsitools convert --to tiff --rect 0,0,2220,2967 --factor 2 -o /tmp/cs-B.tiff "$SRC"
./bin/wsitools hash --mode pixel /tmp/cs-A.tiff; ./bin/wsitools hash --mode pixel /tmp/cs-B.tiff  # MATCH
./bin/wsitools convert --to tiff --factor 2 --codec jpeg2000 --quality reversible=true -o /tmp/cs-rev.tiff "$SRC" 2>&1 | tail -1; echo "exit=$?"
```

- [ ] **Step 5: Clean up** `/tmp/cs-* /tmp/x.svs`.

---

## Self-review

**Spec coverage:** #1 unification (Task 2 descriptor + Task 3 `cropToSVS` peer + Task
4 downsample parity); #2/#3 quality (Task 1, shipped). The byte-identity of jpeg SVS
crop is the key invariant (Task 3 Step 3 / Task 5 Step 2).

**Placeholder scan:** the `cropToSVS` body is specified by reference to its two
parents (`cropToTIFF` + the read `cropEmitSVS` body) — the implementer composes them;
the SVS-specific lines are spelled out. Not a placeholder, but the highest-judgment
task (study both, byte-identical jpeg result).

**Type consistency:** `aperioCodecDescriptor(string) string`,
`setAperioCodecDescriptor(*AperioDescription, string)`; `BuildCropImageDescription` +
`SyntheticAperioDescription` gain trailing `codec string`; `cropToSVS(p
cropEmitParams) error`; `downsampleToSVS` gains `codecName string`.

## Boundaries

**In scope:** unify SVS into the shared crop dispatch; SVS jpeg+jpeg2000;
descriptor; downsample parity; quality unification (shipped). **Deferred:**
non-conformant SVS codecs via crop/downsample (transcode-only); deduplicating
`cropToTIFF`/`cropToOMETIFF`/`cropToSVS` into one shared streamwriter core (a
separate, larger refactor); bit-depth/colorspace.
