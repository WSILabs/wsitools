# Codec-agnostic `downsample` Reduction — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `downsample`'s per-codec `switch` in `materializeOutputL0` with a single codec-agnostic reduction (try `Decode(Scale: factor)`, fall back to full-decode + box on `ErrUnsupportedScale`), fixing the factor-16-on-JPEG bug and gaining AVIF/WebP/HTJ2K source support.

**Architecture:** opentile-go v0.33.0 lets JP2K/HTJ2K honor `DecodeOptions.Scale`; JPEG already does (1/2/4/8). A new `decodeReducedTile` helper tries scaled decode and falls back to `downsampleByPowerOf2` (box) only on `ErrUnsupportedScale`. The decoder is resolved generically via `opentile.CompressionToTIFFTag` + `decoder.GetByCompressionTag` (covers all registered codecs).

**Tech Stack:** Go, opentile-go v0.33.0, cgo codec decoders. Integration tests gated by `WSI_TOOLS_TESTDIR`; fixtures `svs/CMU-1-Small-Region.svs` (JPEG), `svs/JP2K-33003-1.svs` (JP2K).

**Spec:** `docs/superpowers/specs/2026-06-04-downsample-codec-agnostic-design.md`

---

## File structure

| File | Responsibility |
|---|---|
| `cmd/wsitools/downsample.go` | new `decodeReducedTile` helper; `materializeOutputL0` uses it + generic decoder resolution (replaces the per-codec switch) |
| `cmd/wsitools/downsample_codec_test.go` (new) | factor-16-JPEG regression, JP2K dims, factor 2/4/8 unchanged, new-format coverage |
| `CHANGELOG.md` | document the behavior changes |

---

## Task 1: Codec-agnostic tile reduction

**Files:**
- Modify: `cmd/wsitools/downsample.go` — add `decodeReducedTile`; rewrite the decoder resolution (~542–543) and the per-tile `switch` (~576–619) in `materializeOutputL0`.
- Test: `cmd/wsitools/downsample_codec_test.go` (new).

- [ ] **Step 1: Write the failing regression test**

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// downsample --factor 16 on a JPEG source currently fails at runtime
// (decoder/jpeg: scale=16). With the codec-agnostic fallback it must succeed.
func TestDownsampleFactor16JPEG(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "ds16.svs")
	if cmdOut, err := runBin(bin, "downsample", "--factor", "16", "--quiet", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample --factor 16: %v\n%s", err, cmdOut)
	}
	if cmdOut, err := runBin(bin, "info", out); err != nil {
		t.Fatalf("info on output: %v\n%s", err, cmdOut)
	}
}
```
(`stripedBinary`, `testDir`, `runBin` already exist in the package — reuse.)

- [ ] **Step 2: Run, confirm FAIL**

Run: `cd /Volumes/Ext/GitHub/wsitools && make build && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestDownsampleFactor16JPEG -v`
Expected: FAIL — `build pyramid: decode JPEG tile (0,0): decoder/jpeg: scale=16 (want 1,2,4,8)`.
(Integration tests run the pre-built `./bin/wsitools`; `make build` after every code change. cgo build — ignore harmless duplicate-library linker warnings.)

- [ ] **Step 3: Add the `decodeReducedTile` helper**

Add to `cmd/wsitools/downsample.go` (near `downsampleByPowerOf2`):

```go
// decodeReducedTile decodes one source tile's compressed bytes reduced by
// `factor`, preferring codec-domain scaled decode (DecodeOptions.Scale) and
// falling back to a full decode + box-halving only when the codec cannot
// scale-decode (ErrUnsupportedScale). Returns packed RGB and its actual dims.
func decodeReducedTile(fac otdecoder.Factory, compressed []byte, srcTileW, srcTileH, factor int) (pix []byte, w, h int, err error) {
	dec := fac.New()
	defer dec.Close()
	img, derr := dec.Decode(compressed, otdecoder.DecodeOptions{Scale: factor, Format: otdecoder.PixelFormatRGB})
	if derr == nil {
		return img.Pix, img.Width, img.Height, nil
	}
	if !errors.Is(derr, otdecoder.ErrUnsupportedScale) {
		return nil, 0, 0, derr
	}
	// Codec can't scale-decode at this factor: full decode + box-halve.
	full, ferr := dec.Decode(compressed, otdecoder.DecodeOptions{Scale: 1, Format: otdecoder.PixelFormatRGB})
	if ferr != nil {
		return nil, 0, 0, ferr
	}
	return downsampleByPowerOf2(full.Pix, srcTileW, srcTileH, factor)
}
```
Add `"errors"` to the import block in downsample.go (it is not currently imported).

- [ ] **Step 4: Rewrite decoder resolution + the per-tile switch in `materializeOutputL0`**

Replace the two-decoder resolution (currently):
```go
	jpegFac, jpegOK := otdecoder.Get("jpeg")
	jp2kFac, jp2kOK := otdecoder.Get("jpeg2000")
```
with a single generic resolution:
```go
	fac, ok := otdecoder.GetByCompressionTag(opentile.CompressionToTIFFTag(srcCompression))
	if !ok {
		return fmt.Errorf("no decoder registered for source compression %s", srcCompression)
	}
```

Replace the per-tile `switch srcCompression { … }` block (the JPEG/JP2K/default arms producing `decoded, decW, decH`) with:
```go
			decoded, decW, decH, err := decodeReducedTile(fac, compressed, srcTileW, srcTileH, factor)
			if err != nil {
				return fmt.Errorf("decode tile (%d,%d): %w", tx, ty, err)
			}
```
Delete the now-unused `jpegFac/jpegOK/jp2kFac/jp2kOK` references. Update the `materializeOutputL0` doc comment (~line 538) to describe the codec-agnostic behavior ("scaled decode where the codec supports it, else full-decode + box"). `srcCompression` is `opentile.Compression` (from `srcL0.Compression`); `opentile` and `otdecoder` are already imported.

- [ ] **Step 5: Run the regression test, confirm PASS**

Run: `make build && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestDownsampleFactor16JPEG -v`
Expected: PASS.

- [ ] **Step 6: Add the behavior-coverage tests**

Append to `cmd/wsitools/downsample_codec_test.go`:

```go
// JPEG fast-scale path (factors 2/4/8) is unchanged and still works.
func TestDownsampleJPEGFastScaleStillWorks(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	for _, f := range []string{"2", "4", "8"} {
		out := filepath.Join(t.TempDir(), "ds"+f+".svs")
		if o, err := runBin(bin, "downsample", "--factor", f, "--quiet", "-f", "-o", out, src); err != nil {
			t.Fatalf("downsample --factor %s: %v\n%s", f, err, o)
		}
	}
}

// JP2K source now downsamples via scaled (resolution) decode. Output pixels
// differ from the old box path by design; assert it succeeds and reads back.
func TestDownsampleJP2KSource(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "ds_jp2k.svs")
	if o, err := runBin(bin, "downsample", "--factor", "4", "--quiet", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample JP2K --factor 4: %v\n%s", o, err)
	}
	if o, err := runBin(bin, "info", out); err != nil {
		t.Fatalf("info on JP2K downsample output: %v\n%s", err, o)
	}
}
```

> New-format (HTJ2K/AVIF/WebP) source coverage: there is no tiled HTJ2K/AVIF/WebP *source* fixture in the pool (those are write-side codecs), so no integration test is added for them here. The factor-16 test already exercises the `ErrUnsupportedScale` → box fallback path. If a tiled AVIF/WebP/HTJ2K source fixture is added later, add a success test then. (Documented gap — not a silent omission.)

- [ ] **Step 7: Run the full downsample test set, confirm green**

Run: `make build && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'Downsample' -v`
Expected: all PASS (incl. any pre-existing downsample tests — no regression).

- [ ] **Step 8: Commit**

```bash
git add cmd/wsitools/downsample.go cmd/wsitools/downsample_codec_test.go
git commit -m "feat(downsample): codec-agnostic primary reduction (scaled decode + box fallback)

materializeOutputL0 now tries Decode(Scale: factor) and falls back to
full-decode + box only on ErrUnsupportedScale, resolving the decoder generically
via GetByCompressionTag. Fixes --factor 16 on JPEG (was a runtime error), gains
AVIF/WebP/HTJ2K source support, and routes JP2K through v0.33.0 resolution decode
(faster + sharper; output pixels change vs the old box path).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Document the behavior changes

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add a CHANGELOG entry** under the current unreleased / `0.21.0` section (match the file's existing heading style). Content:

```markdown
### Changed
- `downsample` primary reduction is now codec-agnostic: it uses codec-domain
  scaled decode (`DecodeOptions.Scale`) where the source codec supports it and
  falls back to full-decode + box otherwise.
  - **JP2K sources now decode via wavelet resolution-reduction** (opentile-go
    v0.33.0) instead of full-decode + box — faster and sharper, but **output
    pixels are no longer byte-identical** to prior releases for JP2K sources.
  - **Fixes** `downsample --factor 16` on JPEG sources (previously errored with
    `scale=16 (want 1,2,4,8)`).
  - **Adds** `downsample` support for AVIF / WebP / HTJ2K sources (previously
    `unsupported compression`).
```
(Verify the exact heading the file uses for in-progress changes; place the entry there.)

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): codec-agnostic downsample (JP2K output change, factor-16 fix, new source formats)"
```

> Version constant is `0.21.0-dev` (pre-release), so the minor bump is already in place — the change ships in 0.21.0. No `version.go` edit needed.

---

## Optional Task 3 (skip if not clean): consolidate decoder pickers

The codebase has near-duplicate compression→decoder helpers (`pickDecoder` in convert_tiff.go, `pickDecoderForCompression` in hash.go) keyed on `source.Compression`, plus the new `GetByCompressionTag` path keyed on `opentile.Compression`. Consolidating across the two enum types is **not clean** (different enums, different call sites), so per the spec's "do not chase unrelated refactoring," **skip this** unless a genuinely clean single helper emerges. Do not block Task 1/2 on it.

---

## Self-review

**Spec coverage:**
- Unified per-tile reduction (try Scale → box fallback) → Task 1 Steps 3-4 ✓
- Generic decoder resolution (`GetByCompressionTag`) → Task 1 Step 4 ✓
- Use actual decoded dims → `decodeReducedTile` returns `img.Width/Height` ✓
- factor-16 fix → Task 1 Step 1 test ✓
- JP2K output-change accepted + documented → Task 2 ✓
- new-format support → covered by generic resolution + fallback; integration test gap documented (no source fixture) ✓
- cascade/convert untouched → only `materializeOutputL0` changes ✓
- DRY cleanup → Optional Task 3 (skip if not clean), per spec ✓

**Placeholder scan:** none — all code is concrete. The new-format test "gap" is an explicit, justified documentation note (no tiled AVIF/WebP/HTJ2K source fixture exists), not a hand-wave.

**Type consistency:** `decodeReducedTile(fac otdecoder.Factory, compressed []byte, srcTileW, srcTileH, factor int) (pix []byte, w, h int, err error)` matches its call site; `downsampleByPowerOf2` already returns `([]byte, int, int, error)`; `otdecoder.GetByCompressionTag` and `opentile.CompressionToTIFFTag` signatures confirmed against v0.33.0.
