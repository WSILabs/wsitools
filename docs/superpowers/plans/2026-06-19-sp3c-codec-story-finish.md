# SP3c — finish the codec story — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Checkbox (`- [ ]`) steps.

**Goal:** (1) SVS crop/downsample write jpeg + jpeg2000; (2) every pixel re-encode
defaults to the codec standard (jpeg 90), never source-Q; (3) `--quality
reversible=true` works through the `--factor` path.

**Spec:** `docs/superpowers/specs/2026-06-19-sp3c-codec-story-finish-design.md`.

**Branch:** `feat/sp3c-codec-story-finish` (off main@33c23f2).

**Confirmed code state:**
- `parseQualityKnobs` (`convert_tiff.go:438`): `knobs := map[string]string{"q":"85"}`.
- `runConvertFactor` (`convert_factor.go:106`): `quality := 90; if cvQuality != ""
  { Sscanf("%d") }`.
- `cropEmitSVS` (`crop.go:284`): no codec params; `if quality == 0 { desc.Quality()
  … }` (source-Q); calls `BuildCropImageDescription`; called from `crop.go:218`
  without fac/knobs.
- `downsampleToSVS` (`convert_factor.go:139`): `buildPyramid(…, jpegcodec.Factory{},
  {"q":Itoa(quality)}, …)` (jpeg); desc via `MutateForDownsample` (SVS source) or
  `SyntheticAperioDescription` (non-SVS).
- `BuildCropImageDescription(srcDesc, baseW, baseH, x, y, cropW, cropH, tileW,
  tileH, quality int)` (`svs_imagedesc.go:155`) hardcodes `JPEG/RGB`;
  `SyntheticAperioDescription(l0W,l0H,tileW,tileH uint32, quality int, mpp,appMag
  float64, srcSoftware string)` (`:175`) hardcodes `JPEG/RGB`.
- SVS jpeg-only guards: `crop.go:214`, `convert_factor.go:84` (in
  `dispatchDownsampleByTarget`), sourced (Phase 2) from svs conformant set.

---

### Task 1: re-encode quality default → codec standard (#2) + reversible knob (#3)

**Files:** `cmd/wsitools/convert_tiff.go` (parseQualityKnobs), `cmd/wsitools/crop.go`
(cropEmitSVS default), `cmd/wsitools/convert_factor.go` (runConvertFactor parse),
tests.

- [ ] **Step 1: Failing test for #3 (reversible knob through --factor)**

The cleanest unit-testable seam is `parseQualityKnobs`. Add to
`cmd/wsitools/convert_tiff_test.go` (or a quality test file):

```go
func TestParseQualityKnobs_DefaultAndReversible(t *testing.T) {
	// default q is now 90 (was 85)
	k, err := parseQualityKnobs("")
	if err != nil || k["q"] != "90" {
		t.Fatalf("default: q=%q err=%v (want q=90)", k["q"], err)
	}
	// k=v knobs (reversible) parse without erroring; q falls back to default
	k, err = parseQualityKnobs("reversible=true")
	if err != nil || k["reversible"] != "true" || k["q"] != "90" {
		t.Fatalf("reversible: %v err=%v", k, err)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (default is 85)

Run: `go test ./cmd/wsitools/ -run TestParseQualityKnobs_DefaultAndReversible`

- [ ] **Step 3: parseQualityKnobs default 85 → 90**

In `convert_tiff.go:438` change `{"q": "85"}` → `{"q": "90"}`.

- [ ] **Step 4: cropEmitSVS default → codec default (drop source-Q)**

In `crop.go` `cropEmitSVS`, replace the `if quality == 0 { if q,ok :=
desc.Quality(); ok { quality = q } else { quality = 30 } }` block with:
```go
	if quality == 0 {
		quality = 90 // re-encode uses the codec standard default, not source-Q
	}
```
(`desc` is still parsed for MPP/AppMag/provenance — only the *quality* default
changes. Keep the `desc, err := ParseImageDescription(...)` and its other uses.)

- [ ] **Step 5: #3 — runConvertFactor parses knobs instead of Sscanf**

In `convert_factor.go` `runConvertFactor`, replace:
```go
	quality := 90
	if cvQuality != "" {
		if _, err := fmt.Sscanf(cvQuality, "%d", &quality); err != nil {
			return fmt.Errorf("--quality %q: must be an integer 1..100", cvQuality)
		}
	}
	if quality < 1 || quality > 100 {
		return fmt.Errorf("--quality must be 1..100")
	}
```
with:
```go
	// Derive the int quality (fallback for the SVS-jpeg path) from the knob
	// parser, so non-integer --quality (e.g. reversible=true for J2K) is honored;
	// the full cvQuality string flows to resolveTransformCodec in the emitters.
	knobs, err := parseQualityKnobs(cvQuality)
	if err != nil {
		return err
	}
	quality, _ := strconv.Atoi(knobs["q"]) // parseQualityKnobs range-checks q
```
(Confirm `strconv` is imported; `err` may need declaring — adapt so it compiles.
`cvQuality == ""` → parseQualityKnobs returns the default q=90.)

- [ ] **Step 6: Build + test; update changed assertions**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ParseQualityKnobs|Convert|Crop|Downsample|Codec' -count=1`
Expected: some existing tests asserting q≈83/85 output or SVS source-Q now see q=90
— update those assertions (they reflect the intended new default). Report which.
`gofmt -l` → clean.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat(quality): re-encode defaults to codec standard (90), not source-Q

parseQualityKnobs default 85→90; cropEmitSVS drops source-Q for the codec
default; runConvertFactor parses --quality via parseQualityKnobs (not
Sscanf) so reversible=true (lossless J2K) works through --factor. Every
pixel re-encode now lands on one standard codec default.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: SVS crop/downsample emitter codecs (jpeg + jpeg2000) (#1)

**Files:** `cmd/wsitools/svs_imagedesc.go` (descriptor helper + builders),
`cmd/wsitools/crop.go` (cropEmitSVS + runCrop call + guard), `cmd/wsitools/convert_factor.go`
(downsampleToSVS + guard), tests.

- [ ] **Step 1: Descriptor helper + test**

Add to `svs_imagedesc.go`:
```go
// aperioCodecDescriptor maps an output codec to the Aperio geometry-line codestream
// descriptor: jpeg → "JPEG/RGB", jpeg2000 → "J2K/YUV16" (per Aperio's own J2K SVS,
// e.g. JP2K-33003-1.svs). Only the conformant SVS codecs are handled.
func aperioCodecDescriptor(codec string) string {
	if codec == "jpeg2000" {
		return "J2K/YUV16"
	}
	return "JPEG/RGB"
}

// setAperioCodecDescriptor rewrites the codestream descriptor token on a parsed
// Aperio description's geometry line (used after MutateForDownsample when the
// output codec differs from the source's). It replaces a leading "JPEG/RGB" or
// "J2K/YUV16" token with the target codec's.
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

- [ ] **Step 2: `BuildCropImageDescription` + `SyntheticAperioDescription` take a codec**

Add a `codec string` parameter to both. In `BuildCropImageDescription`, replace the
hardcoded `JPEG/RGB` in the `fmt.Fprintf(... "JPEG/RGB Q=%d;" ...)` with
`aperioCodecDescriptor(codec)`. In `SyntheticAperioDescription`, replace the
`JPEG/RGB` in its `geom := fmt.Sprintf(...)` likewise. Update all existing callers
to pass the codec (jpeg for the current jpeg-only call sites).

- [ ] **Step 3: Thread the codec into `cropEmitSVS`**

`cropEmitSVS` signature gains `fac codec.EncoderFactory, knobs map[string]string,
codecName string` (after `workers`). Inside:
- pass `fac, knobs` to its `buildEnginePyramid(...)` call (replacing
  `jpegcodec.Factory{}, {"q":Itoa(quality)}`).
- pass `codecName` to `BuildCropImageDescription(rawDesc, …, quality, codecName)`.
- the writer's `streamwriter.Options` codec/compression are derived by the engine;
  no change needed beyond the desc.
In `runCrop` (`crop.go:218`), pass the already-resolved `fac, knobs, resolvedCodec`
(the same values it puts in `cropEmitParams`) to `cropEmitSVS`.

- [ ] **Step 4: Thread the codec into `downsampleToSVS`**

`downsampleToSVS` gains a `codecName string` param (threaded from
`dispatchDownsampleByTarget`'s svs arm — it already receives `codecName`). Inside:
- `fac, knobs, resolvedCodec, err := resolveTransformCodec(codecName, cvQuality, quality)`.
- pass `fac, knobs` to `buildPyramid(...)` (replacing the jpeg literal).
- SVS-source path: after `desc.MutateForDownsample(...)`, call
  `setAperioCodecDescriptor(&desc, resolvedCodec)` (rewrites JPEG/RGB→J2K/YUV16 when
  the codec changed; no-op for same-codec jpeg).
- non-SVS path: pass `resolvedCodec` to `SyntheticAperioDescription(...)`.

- [ ] **Step 5: Widen the SVS guards jpeg → jpeg|jpeg2000**

`crop.go:214` and `convert_factor.go:84` currently reject any non-jpeg codec for
svs. Widen the accepted set to `{jpeg, jpeg2000}` (the emitter-capable conformant
set). The simplest: `if codecName != "" && codecName != "jpeg" && codecName !=
"jpeg2000" { return fmt.Errorf("SVS crop/downsample supports jpeg or jpeg2000; use
--to tiff for %s", codecName) }`. (Non-conformant SVS codecs still error here;
avif-SVS remains transcode-only.)

- [ ] **Step 6: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'Aperio|Crop|Downsample|Convert|SVS' -count=1`
Expected: PASS. `gofmt -l` → clean.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat(svs): crop/downsample write jpeg2000 (conformant J2K SVS)

cropEmitSVS/downsampleToSVS thread the resolved codec into the engine and
emit the codec-correct Aperio descriptor (JPEG/RGB ↔ J2K/YUV16); the SVS
guard widens to jpeg|jpeg2000. convert --to svs --rect|--factor --codec
jpeg2000 now produces a conformant J2K SVS. avif/etc. stay transcode-only.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`. `SRC=sample_files/svs/CMU-1-Small-Region.svs`

- [ ] **Step 2: J2K SVS via crop + downsample**
```bash
./bin/wsitools convert --to svs --rect 0,0,2048,2048 --codec jpeg2000 -o /tmp/cs-crop.svs "$SRC"
./bin/wsitools info /tmp/cs-crop.svs | grep -iE "Format|jpeg2000"
./bin/wsitools convert --to svs --factor 2 --codec jpeg2000 -o /tmp/cs-ds.svs "$SRC"
./bin/wsitools info /tmp/cs-ds.svs | grep -iE "Format|jpeg2000"
# geometry descriptor:
./bin/wsitools dump-ifds --raw /tmp/cs-ds.svs 2>/dev/null | grep -i ImageDescription | head -1 | grep -o "J2K/YUV16"
# reads back:
./bin/wsitools hash --mode pixel /tmp/cs-ds.svs >/dev/null && echo "J2K SVS reads back"
```
Expected: both re-detect as svs / jpeg2000; description says `J2K/YUV16`; hashes OK.

- [ ] **Step 3: SVS guard (avif still rejected)**
```bash
./bin/wsitools convert --to svs --rect 0,0,512,512 --codec avif -o /tmp/x.svs "$SRC" 2>&1 | grep -i "jpeg or jpeg2000"
```

- [ ] **Step 4: #2 quality — crop ≡ downsample at codec default**
```bash
./bin/wsitools convert --to tiff --factor 2 -o /tmp/cs-A.tiff "$SRC"
./bin/wsitools convert --to tiff --rect 0,0,2220,2967 --factor 2 -o /tmp/cs-B.tiff "$SRC"
./bin/wsitools hash --mode pixel /tmp/cs-A.tiff
./bin/wsitools hash --mode pixel /tmp/cs-B.tiff   # must MATCH now (both q90)
# crop SVS no longer source-Q:
./bin/wsitools convert --to svs --rect 0,0,2048,2048 -o /tmp/cs-cropQ.svs "$SRC"
./bin/wsitools info /tmp/cs-cropQ.svs | grep -iE "Q≈9|jpeg"   # Q≈88 (q90), not Q≈26 source
```

- [ ] **Step 5: #3 reversible J2K downsample**
```bash
./bin/wsitools convert --to tiff --factor 2 --codec jpeg2000 --quality reversible=true -o /tmp/cs-rev.tiff "$SRC" 2>&1 | tail -1; echo "exit=$?"
./bin/wsitools info /tmp/cs-rev.tiff | grep -iE "jpeg2000|reversible|lossless"
```
Expected: succeeds (no "must be an integer"); J2K tiles reversible/lossless.

- [ ] **Step 6: Clean up** `/tmp/cs-* /tmp/x.svs`.

---

## Self-review

**Spec coverage:** #1 (SVS jpeg|jpeg2000 + J2K/YUV16 descriptor + widened guard,
Task 2); #2 (re-encode → codec default 90, drop source-Q, parseQualityKnobs 85→90,
Task 1); #3 (parseQualityKnobs instead of Sscanf, Task 1). Crop≡downsample parity +
reversible-J2K + guard in Task 3.

**Placeholder scan:** none — the "update changed assertions" step is expected
fallout of an intended default change, not a placeholder.

**Type consistency:** `aperioCodecDescriptor(string) string`,
`setAperioCodecDescriptor(*AperioDescription, string)`; `BuildCropImageDescription`
+ `SyntheticAperioDescription` gain a trailing `codec string`; `cropEmitSVS` gains
`fac codec.EncoderFactory, knobs map[string]string, codecName string`;
`downsampleToSVS` gains `codecName string`.

## Boundaries

**In scope:** SVS jpeg+jpeg2000 crop/downsample; uniform re-encode quality default;
the reversible-knob fix. **Deferred:** non-conformant SVS codecs via crop/downsample
(transcode-only); per-codec *distinct* default values (one number 90 for now);
bit-depth/colorspace.
