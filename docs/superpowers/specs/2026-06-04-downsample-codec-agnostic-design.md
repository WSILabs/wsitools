# Codec-agnostic `downsample` primary reduction — design

> Status: **approved design** (brainstormed 2026-06-04). Next: writing-plans.
> Branch off `main`; never implement on `main`.

## Goal

Make `downsample`'s primary source→output-L0 reduction codec-agnostic: try a
**codec-domain scaled decode** (`DecodeOptions.Scale = factor`), and fall back to
**full-decode + box** only when the codec can't scale-decode. This replaces the
hardcoded `switch srcCompression` in `materializeOutputL0`, leveraging
opentile-go v0.33.0's scaled JP2K/HTJ2K decode (#10/#12).

## Background — current state and a latent bug

`cmd/wsitools/downsample.go` `materializeOutputL0` dispatches per source
compression (~lines 576–619):

- **JPEG** → `Decode(Scale: factor)` (libjpeg IDCT fast-scale). The opentile-go
  JPEG decoder only supports `Scale ∈ {1,2,4,8}`.
- **JP2K** → full-decode (`Scale: 1`) + chained box (`downsampleByPowerOf2`).
- **default** (HTJ2K/AVIF/WebP/raw) → `unsupported compression` error.

`--factor` is validated to `{2,4,8,16}`. **Latent bug confirmed empirically:**
`downsample --factor 16` on a JPEG source fails at runtime —
`decoder/jpeg: scale=16 (want 1,2,4,8)` — because 16 exceeds libjpeg's fast-scale
cap and there is no fallback. So a validated, accepted flag value is broken for
the most common source type.

This refactor was unblocked by opentile-go v0.33.0 (JP2K + HTJ2K now honor
`Scale`); see [[codec-domain-scaled-decode-direction-2026-06-03]].

## Design

### Unified per-tile reduction

In `materializeOutputL0`, replace the `switch srcCompression { … }` with one
helper applied per source tile:

```
decodeReducedTile(fac, compressed, srcTileW, srcTileH, factor) -> (pix, w, h, err):
  1. img, err := fac.New().Decode(compressed, {Scale: factor, Format: RGB})
  2. if errors.Is(err, decoder.ErrUnsupportedScale):
        full, err := fac.New().Decode(compressed, {Scale: 1, Format: RGB})
        pix, w, h, err = downsampleByPowerOf2(full.Pix, srcTileW, srcTileH, factor)  // box fallback
        return
  3. if err != nil: return err
  4. return img.Pix, img.Width, img.Height, nil   // use ACTUAL decoded dims
```

Use the decoded image's **actual** `Width/Height` for `decW/decH` (both the
scaled-decode and the box fallback yield `ceil(src/factor)`), feeding the
existing `validDecW/validDecH` + `pasteIntoRaster` logic unchanged.

### Generic decoder resolution (this is what enables new formats)

Resolve the decoder for the source tile's compression generically via
opentile-go's `decoder.GetByCompressionTag(tag uint16)` — which covers every
registered codec (jpeg, jpeg2000, htj2k, avif, webp, …) — instead of the
hardcoded `Get("jpeg")`/`Get("jpeg2000")`. Map the source level's compression to
its TIFF compression tag (the codebase already has `compressionTagFor` for the
`source.Compression` enum; `materializeOutputL0` currently works with
opentile-go's `opentile.Compression` from `srcL0.Compression` — bridge via the
existing `mapOpentileCompression` or resolve the tag directly). If no decoder is
registered for the compression, return a clear `no decoder for source
compression <c>` error (as today).

**DRY cleanup (in scope, since we're here):** three near-duplicate
compression→decoder helpers exist (`pickDecoder` in convert_tiff.go,
`pickDecoderForCompression` in hash.go, downsample's inline `Get(...)` calls).
Consolidate to one generic tag-based resolver if it's a clean change; do not
chase unrelated refactoring beyond that.

### Why per-tile scaled decode is correct (no seams)

Codec-domain reduction (JPEG IDCT, JP2K/HTJ2K wavelet resolution) is
self-contained within a single tile's codestream — no cross-tile dependency — so
per-tile decoding is seam-free. (This is exactly why codec-domain beat the
earlier spatial-Lanczos idea, which had tile-boundary seams.)

### Scope boundary

Only the **primary reduction** (`materializeOutputL0`) changes. The output
pyramid **cascade** (sublevels, `downsampleByPowerOf2` calls in the level loop)
stays box — correct for mipmap generation and libvips `dzsave` parity. The
`convert --to dzi|szi` paths are untouched.

## Behavior changes (→ CHANGELOG, minor version bump)

1. **JP2K output pixels change** — box-average → v0.33.0 wavelet resolution
   decode (sharper, faster; not byte-identical to prior releases). Flag
   prominently; this is the accepted point of the change.
2. **`--factor 16` on JPEG now works** — was a runtime error; the box fallback
   handles it.
3. **AVIF / WebP / HTJ2K sources now downsample** — were `unsupported
   compression` errors. HTJ2K via scaled decode; AVIF/WebP via the box fallback.

## Error handling

- No registered decoder for the source compression → clear error (unchanged
  behavior, generalized message).
- Decode fails for reasons other than `ErrUnsupportedScale` → surface the
  per-tile error as today (no silent fallback that masks a real decode failure —
  only `ErrUnsupportedScale` triggers the box fallback).

## Isolation

Contained to `materializeOutputL0` + the new `decodeReducedTile` helper (and the
consolidated decoder resolver). Reuses the existing `downsampleByPowerOf2`. No
new package.

## Testing (TDD)

- **Regression for the fix:** `downsample --factor 16` on a JPEG SVS
  (`CMU-1-Small-Region.svs`) now exits 0 and produces the expected output
  dimensions (`ceil(src/16)`). (This test fails on `main` today.)
- **JPEG factor 2/4/8 unchanged:** still succeeds (fast-scale path).
- **JP2K downsample:** succeeds with correct dims on a JP2K source
  (`JP2K-33003-1.svs`); assert dimensions + non-degenerate output. Do NOT assert
  byte-identity to prior box output (it changed by design). Optionally assert the
  output differs from a box-rendered reference to prove the new path is taken.
- **New-format coverage:** for any of HTJ2K/AVIF/WebP with an available fixture,
  assert `downsample` now succeeds; skip if no fixture. (At minimum, document
  which were exercised vs skipped — no silent gaps.)
- **Fallback path:** assert a source whose decoder returns `ErrUnsupportedScale`
  (e.g. JPEG at factor 16) routes through the box fallback (covered by the
  factor-16 test).
- Heavy `-race` suites run uncontended / `-timeout 30m` (per CLAUDE.md).

## Out of scope

- Optimizing the fallback (e.g. fast-scale to the codec's max then box the
  residual) — simple full-decode + box is correct; optimize later if factor-16
  JPEG perf matters.
- The output-pyramid cascade and `convert --to dzi|szi` (stay box).
- WebP/JXL **encoder**-side or opentile-go #11 umbrella items.

## Open questions (resolved during brainstorming)

- *JP2K output-pixel change?* → accepted; CHANGELOG + minor bump.
- *Scope?* → fully codec-agnostic (try-Scale + box fallback), not JP2K-only.
- *Factor-16 fallback?* → simple full-decode + box (no optimization).
