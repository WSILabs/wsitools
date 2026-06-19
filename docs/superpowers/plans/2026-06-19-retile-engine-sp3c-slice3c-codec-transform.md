# SP3c Slice 3c — `--codec` on the transform path — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Checkbox (`- [ ]`) steps.

**Goal:** Let `--codec` compose with `--rect`/`--factor` — `convert --rect … --codec
avif`, `convert --factor 2 --codec jpeg2000`, etc. — by making the crop/downsample
engine builders codec-configurable (they already use `codecTileEncoder`, just
hardcode jpeg).

**Architecture:** `buildEnginePyramid`/`buildEnginePyramidCOGWSI` gain `(fac
codec.EncoderFactory, knobs map[string]string)`; the encoder + `Compression` tag
become codec-derived (exactly as `transcodeLevel` already does). Existing callers
pass the jpeg factory ⇒ byte-identical default. The crop/downsample emitters
resolve `--codec`/`--quality` → `(fac, knobs)` via the existing `codec.Lookup` +
`parseQualityKnobs`. `runDICOMEngine` already takes a `codecName` — pass the real
one. `validateRectCombo` drops the `--codec` rejection. A geometry change always
re-encodes, so there is **no lossless-source fork** here (that stays on the
pure-transcode path).

**Spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-slice3c-codec-transform-design.md`.

**Branch:** `feat/retile-engine-sp3c-3c` (stacked on `-2`; off main@da4e80f +
DZI-rect + transcode).

**Key code facts (verified):**
- `buildEnginePyramid` (`downsample.go:247`) builds the encoder with
  `jpegcodec.Factory{}.NewEncoder(...)`, `tables := enc.LevelHeader()`, and a
  `streamwriter.LevelSpec` with `Compression: tiff.CompressionJPEG, JPEGTables:
  tables`. It drives `runEngineRetile(..., &codecTileEncoder{enc: enc}, ...)`.
- `transcodeLevel` (`convert_tiff.go:484`) is the codec-aware template:
  `Compression: enc.TIFFCompressionTag()`, `JPEGTables: enc.LevelHeader()`,
  `Photometric: 2`, `BitsPerSample: [8,8,8]`.
- `parseQualityKnobs(quality string) (map[string]string, error)` (`convert_tiff.go:437`)
  → knobs (default `{"q":"85"}`); `codec.Lookup(name) (codec.EncoderFactory, error)`.

---

### Task 1: `buildEnginePyramid`/`buildEnginePyramidCOGWSI` codec-configurable

**Files:**
- Modify: `cmd/wsitools/downsample.go` (`buildEnginePyramid`, and the thin
  `buildPyramid` wrapper that calls it)
- Modify: `cmd/wsitools/convert_factor.go` (`buildPyramidCOGWSI` /
  `buildEnginePyramidCOGWSI` — confirm exact names)
- Modify: all current `buildEnginePyramid`/`buildEnginePyramidCOGWSI` call sites
  (crop emitters in `crop_formats.go`, `cropEmitSVS` in `crop.go`) to pass the jpeg
  factory + knobs (byte-identical default)
- Test: `cmd/wsitools/build_engine_codec_test.go`

This is a behavior-preserving refactor when callers pass jpeg.

- [ ] **Step 1: Write a test pinning the jpeg default + a non-jpeg compression tag**

Create `cmd/wsitools/build_engine_codec_test.go`:

```go
package main

import (
	"strconv"
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
)

// engineLevelCompression is a thin helper: the Compression tag buildEnginePyramid
// will write for a given codec. Asserts jpeg→7 (CompressionJPEG) and that a
// J2K-family codec reports a different tag, proving the spec is codec-derived.
func TestEngineCompressionTagIsCodecDerived(t *testing.T) {
	jenc, err := jpegcodec.Factory{}.NewEncoder(codec.LevelGeometry{TileWidth: 256, TileHeight: 256, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: map[string]string{"q": "85"}})
	if err != nil {
		t.Fatal(err)
	}
	defer jenc.Close()
	if jenc.TIFFCompressionTag() != 7 { // tiff.CompressionJPEG == 7
		t.Fatalf("jpeg TIFFCompressionTag = %d, want 7", jenc.TIFFCompressionTag())
	}

	fac, err := codec.Lookup("jpeg2000")
	if err != nil {
		t.Skip("jpeg2000 codec not built")
	}
	j2k, err := fac.NewEncoder(codec.LevelGeometry{TileWidth: 256, TileHeight: 256, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: map[string]string{"q": "85"}})
	if err != nil {
		t.Fatal(err)
	}
	defer j2k.Close()
	if j2k.TIFFCompressionTag() == 7 {
		t.Fatalf("jpeg2000 TIFFCompressionTag must differ from jpeg(7), got %d", j2k.TIFFCompressionTag())
	}
	_ = strconv.Itoa // keep import tidy if unused
}
```

- [ ] **Step 2: Run — expect PASS** (documents the codec interface; no code change yet)

Run: `go test ./cmd/wsitools/ -run TestEngineCompressionTagIsCodecDerived`

- [ ] **Step 3: Parameterize `buildEnginePyramid`**

In `cmd/wsitools/downsample.go`, change the signature:

```go
func buildEnginePyramid(ctx context.Context, slide *opentile.Slide, w *streamwriter.Writer, srcRegion opentile.Region, outL0 opentile.Size, fac codec.EncoderFactory, knobs map[string]string, workers int, postL0Hook func() error) error {
```
Replace the encoder construction:
```go
	enc, err := fac.NewEncoder(codec.LevelGeometry{
		TileWidth: outputTileSize, TileHeight: outputTileSize, PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: knobs})
```
In `specFor`, change `Compression: tiff.CompressionJPEG` → `Compression:
enc.TIFFCompressionTag()`. Leave `JPEGTables: tables` (already
`enc.LevelHeader()`), `Photometric: 2`, `SamplesPerPixel: 3`, `BitsPerSample:
[8,8,8]` unchanged (matches `transcodeLevel`). (The `quality int` param is gone —
quality now lives in `knobs["q"]`.)

- [ ] **Step 4: Update the thin `buildPyramid` wrapper + the factor default**

The `buildPyramid(ctx, src, w, factor, quality, workers, hook)` wrapper (the
downsample/`--factor` entry) must now build the jpeg factory + knobs and call the
new signature:
```go
func buildPyramid(ctx context.Context, slide *opentile.Slide, w *streamwriter.Writer, factor, quality, workers int, postL0Hook func() error) error {
	srcW, srcH := /* existing L0 dims logic */
	outL0 := opentile.Size{W: srcW / factor, H: srcH / factor}
	srcRegion := opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: opentile.Size{W: srcW, H: srcH}}
	return buildEnginePyramid(ctx, slide, w, srcRegion, outL0, jpegcodec.Factory{}, map[string]string{"q": strconv.Itoa(quality)}, workers, postL0Hook)
}
```
(Adapt to the wrapper's actual current body — it already computes srcRegion/outL0;
just inject the jpeg factory + knobs. Keep its external signature so its callers
are unchanged.)

- [ ] **Step 5: Update the crop-emitter call sites to pass jpeg**

In `crop_formats.go` (`cropToTIFF`/`cropToOMETIFF`) and `crop.go` (`cropEmitSVS`),
the lossy `buildEnginePyramid(...)` calls now pass `jpegcodec.Factory{},
map[string]string{"q": strconv.Itoa(p.quality)}` in place of the `p.quality`
positional arg. (cog-wsi: same for `buildEnginePyramidCOGWSI` — Step 6.) Import
`jpegcodec` + `strconv` where needed. This is the byte-identical default.

- [ ] **Step 6: Same treatment for `buildEnginePyramidCOGWSI`**

Read `buildEnginePyramidCOGWSI` (in `convert_factor.go`) and apply the identical
change: add `(fac, knobs)`, build the encoder from `fac`, derive the cogwsiwriter
level spec's Compression field from `enc.TIFFCompressionTag()` (in cogwsiwriter's
spec idiom — confirm the field name), update its thin `buildPyramidCOGWSI` wrapper
+ `cropToCOGWSI` caller to pass jpeg.

- [ ] **Step 7: Build + test (jpeg default unchanged)**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'Crop|crop|Convert|Downsample|EngineCompression' -count=1`
Expected: PASS. `gofmt -l` → clean. (The jpeg default must produce identical
output — verified at the integration gate, Task 4.)

- [ ] **Step 8: Commit**

```bash
git add cmd/wsitools/downsample.go cmd/wsitools/convert_factor.go cmd/wsitools/crop_formats.go cmd/wsitools/crop.go cmd/wsitools/build_engine_codec_test.go
git commit -m "$(cat <<'EOF'
refactor(engine): buildEnginePyramid takes a codec factory (default jpeg)

buildEnginePyramid/buildEnginePyramidCOGWSI gain (fac, knobs); the encoder
and the level Compression tag are codec-derived (as transcodeLevel does).
All current callers pass the jpeg factory => byte-identical. Threads the
real codec in the next task.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Thread `--codec` through the crop/downsample emitters + DICOM

**Files:**
- Modify: `cmd/wsitools/crop_formats.go` (`cropEmitParams` + emitters),
  `cmd/wsitools/crop.go` (`runCrop` resolves codec), `cmd/wsitools/convert_factor.go`
  (downsample emitters + `downsampleToDICOM`)
- Test: extend `cmd/wsitools/crop_factor_test.go` (a codec-resolution unit test)

- [ ] **Step 1: Add a codec resolver + test**

Add a helper (e.g. in `crop.go` or a small `codec_resolve.go`):

```go
// resolveTransformCodec maps --codec/--quality to an encoder factory + knobs for
// the crop/downsample engine path. Empty codec ⇒ jpeg at the given quality.
func resolveTransformCodec(codecName, quality string, fallbackQ int) (codec.EncoderFactory, map[string]string, error) {
	if codecName == "" {
		return jpegcodec.Factory{}, map[string]string{"q": strconv.Itoa(fallbackQ)}, nil
	}
	knobs, err := parseQualityKnobs(quality)
	if err != nil {
		return nil, nil, err
	}
	fac, err := codec.Lookup(codecName)
	if err != nil {
		return nil, nil, err
	}
	return fac, knobs, nil
}
```
Unit test: empty → jpeg factory + {"q": fallback}; "jpeg2000" → its factory; bad
codec → error.

- [ ] **Step 2: `cropEmitParams` carries `fac`/`knobs`; emitters pass them**

Add `fac codec.EncoderFactory` + `knobs map[string]string` to `cropEmitParams`.
The lossy branches of `cropToTIFF`/`cropToOMETIFF`/`cropToCOGWSI` pass `p.fac,
p.knobs` to the engine builder (replacing the jpeg-default from Task 1 Step 5).
`cropToDICOM` lossy passes the codec **name** to `runDICOMEngine` (it already takes
`codecName`): add a `codecName string` field to `cropEmitParams` and use it instead
of `"jpeg"`. The L0 `ImageDescription` `codec=` field should reflect the real codec.

- [ ] **Step 3: `runCrop` resolves the codec and fills the params**

`runCrop` gains `codecName, quality string` params (or reads the cv globals — match
how convert passes them). It calls `resolveTransformCodec(codecName, quality,
qFallback)` and sets `p.fac/p.knobs/p.codecName`. For the SVS target, keep the
existing SVS codec restriction (svs only writes jpeg/jpeg2000 conformantly — error
on others, or defer to the existing ad-hoc check).

- [ ] **Step 4: downsample emitters + `downsampleToDICOM`**

`downsampleTo{SVS,TIFF,OMETIFF,COGWSI}` already call `buildPyramid`/
`buildPyramidCOGWSI` (jpeg). Give them a codec path too: resolve `(fac, knobs)` and
call `buildEnginePyramid`/`buildEnginePyramidCOGWSI` directly with the codec (or
extend the thin wrappers to take `(fac, knobs)`). `downsampleToDICOM` passes the
real `codecName` to `runDICOMEngine` (it already threads `codecName` from SP3a —
confirm and wire `cvCodec`).

- [ ] **Step 5: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'Crop|Convert|Downsample|Codec' -count=1`
Expected: PASS. `gofmt -l` → clean.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat(transform): thread --codec through crop/downsample emitters

cropEmitParams carries (fac, knobs, codecName); the lossy emitters encode
with the chosen codec; cropToDICOM/downsampleToDICOM pass the real codec
to runDICOMEngine. resolveTransformCodec maps --codec/--quality → factory.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Drop the `--codec` guard; route `--rect --codec`

**Files:**
- Modify: `cmd/wsitools/convert.go` (`validateRectCombo`; the rect block threads
  `cvCodec`/`cvQuality` to `runCrop`)
- Test: extend `cmd/wsitools/convert_rect_test.go`

- [ ] **Step 1: Update tests**

`TestConvertRectComboGuards` no longer expects `rect+codec` to error; move it to
`TestConvertRectComboAllowed`:
```go
	if err := validateRectCombo(true, 1, 0, "avif", "tiff"); err != nil {
		t.Fatalf("rect+codec is now allowed, got %v", err)
	}
```

- [ ] **Step 2: Run — expect FAIL** (guard still rejects rect+codec)

Run: `go test ./cmd/wsitools/ -run TestConvertRectCombo`

- [ ] **Step 3: Drop the `--codec` rejection in `validateRectCombo`**

Remove the `if codec != "" { return … }` block. `validateRectCombo` may now be a
no-op for the crop targets (factor/targetMag are supported, codec is supported);
keep it as the seam for future combo guards, or inline-remove the call if it adds
nothing — implementer's judgment.

- [ ] **Step 4: Thread `--codec`/`--quality` into the rect block's `runCrop` call**

In `runConvert`'s rect block, pass `cvCodec, cvQuality` to `runCrop` (the new
params from Task 2 Step 3). The factor resolution stays.

- [ ] **Step 5: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ConvertRect|Convert|Crop' -count=1`
Expected: PASS. `gofmt -l` → clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/convert_rect_test.go
git commit -m "$(cat <<'EOF'
feat(convert): --rect/--factor compose with --codec (one pass)

validateRectCombo no longer rejects --rect --codec; runConvert threads
the codec to the crop/downsample engine path. crop+downsample+transcode
+ container change now compose in a single decode/rebuild.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`.

- [ ] **Step 2: jpeg default unchanged (regression)**

```bash
./bin/wsitools convert --to tiff --rect 0,0,2048,2048 --factor 2 -o /tmp/3c-jpeg.tiff sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools hash --mode pixel /tmp/3c-jpeg.tiff
```
Expected: pixel hash matches the Slice-3b output for the same command (no `--codec`
⇒ jpeg default ⇒ byte-identical). Compare against a pre-3c build if unsure.

- [ ] **Step 3: rect/factor + codec compose**

```bash
./bin/wsitools convert --to tiff --rect 0,0,2048,2048 --factor 2 --codec jpeg2000 -o /tmp/3c-j2k.tiff sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools info /tmp/3c-j2k.tiff   # L0 1024x1024, tiles jpeg2000
./bin/wsitools convert --factor 2 --codec avif -o /tmp/3c-avif.tiff sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
```
Expected: jpeg2000 output re-detects with J2K tiles at 1024²; avif downsample
succeeds.

- [ ] **Step 4: DICOM rect+factor+codec**

```bash
./bin/wsitools convert --to dicom --rect 0,0,2048,2048 --factor 2 --codec jpeg2000 -o /tmp/3c-dcm sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
```
Expected: succeeds; frames are JP2K (dciodvfy 0 errors if available).

- [ ] **Step 5: guard removed**

```bash
./bin/wsitools convert --to tiff --rect 0,0,512,512 --codec webp -o /tmp/3c-webp.tiff sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
```
Expected: succeeds (no "not yet supported").

- [ ] **Step 6: Clean up** `/tmp/3c-*`.

---

## Self-review

**Spec coverage:** codec-configurable `buildEnginePyramid`/COGWSI (Task 1);
`--codec` threaded through crop/downsample emitters + DICOM (Task 2); guard dropped
+ routing (Task 3); jpeg default byte-identical (Task 1 + Task 4 Step 2); no
lossless fork (geometry change always re-encodes — design §"No lossless-source
fork"). SVS/exotic-codec conformance left to existing ad-hoc checks + Phase 2.

**Placeholder scan:** the two "confirm the field name / exact names" notes
(`buildEnginePyramidCOGWSI`, the cogwsiwriter Compression field) are bounded
look-ups, not placeholders — the change pattern is fully specified.

**Type consistency:** `buildEnginePyramid(…, fac codec.EncoderFactory, knobs
map[string]string, workers int, hook)`; `resolveTransformCodec(codecName, quality
string, fallbackQ int) (codec.EncoderFactory, map[string]string, error)`;
`cropEmitParams` += `fac`, `knobs`, `codecName`; `runCrop` += `codecName, quality
string`.

## Boundaries

**In Slice 3c:** `--codec` with `--rect`/`--factor` for tiff/ome-tiff/cog-wsi/dicom.
**Deferred:** Phase 2 conformance table (the nuanced SVS/OME non-jpeg cases), the
`--quality` default unification, dzi/szi (their `--codec` is the tile format).
This completes SP3c Phase 1.
