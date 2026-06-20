# Per-codec default quality — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** SP3c "finish the codec story" (merged, main@8a49511). Follow-on fix.

## Problem

The "finish the codec story" work set the absent-`--quality` default to `q=90` for
**every** codec (`parseQualityKnobs` default + `resolveTransformCodec` seed). But
`q` (1–100) is a JPEG-ism, and each codec maps it differently — so forcing `q=90`
**overrides each codec's own sensible default**:

| codec | own default (q absent) | what forced `q=90` does |
|---|---|---|
| jpeg / jpeg2000 / htj2k / avif / webp | **85** | bumps to 90 (minor) |
| jpegxl | **distance 1.0** ("visually lossless") | `15·(1−0.9)` = **distance 1.5** — *more* lossy than its default |

JPEG-XL is the clear bug (90 makes it worse than its default). The right model is a
**per-codec default**, not one forced number.

## Fix: a per-codec default-knobs map

A single source of truth in wsitools:

```
codecDefaultKnobs(codec) → map[string]string
  jpeg | jpeg2000 | htj2k | avif | webp → {"q": "85"}
  jpegxl                                → {"distance": "1.0"}
  png                                   → {}            // lossless, no quality
  (unknown)                            → {"q": "85"}
```

Values **start from the codecs' existing defaults** (all 85; jxl distance-1.0), so
this is byte-identical to "let each encoder default" — but it's explicit and tunable
in one place. (Future per-codec tuning lives here, not scattered across encoders.)

## Wiring

- **`resolveTransformCodec(codecName, quality, fallbackQ)`** (`codec_resolve.go`):
  when `quality == ""`, seed `knobs = codecDefaultKnobs(resolvedCodecName)` instead
  of `{"q": fallbackQ}`. When `quality != ""`, the user's `parseQualityKnobs(quality)`
  overrides (unchanged). `fallbackQ` is no longer the default source — but the int
  it represents is still needed downstream (Aperio `Q=` token), so:
- **`qFromKnobs(knobs) int`** — returns `knobs["q"]` (validated 1–100) or **85** when
  absent (jxl/png). Used wherever an int quality is needed for metadata (the Aperio
  geometry-line `Q=` token, the `quality` int threaded through `runConvertFactor` /
  the SVS emitters).
- **`parseQualityKnobs` default `90 → 85`** (`convert_tiff.go:441`): revert the
  bump, so a user `--quality reversible=true` (knobs without `q`) seeds `q=85`
  (consistent with the codec defaults; `q` is ignored for reversible anyway).
- **Callers** (`runConvertFactor`, `cropToSVS`/`downsampleToSVS` quality default,
  the transcode path's int): derive the int quality via `qFromKnobs` (default 85),
  not a hardcoded 90.

## Behavior change (vs main@8a49511)

- Every re-encode's absent-`--quality` default: **q90 → q85** for the q-codecs
  (jpeg/jp2k/htj2k/avif/webp); **jxl: q-mapped-1.5 → native distance-1.0**.
- The recent byte-identity baselines (jpeg SVS crop/downsample at q90) move to q85 —
  still internally consistent (crop ≡ downsample, convert --rect ≡ crop), just at
  85. Verify those equalities still hold at the new default.
- An explicit `--quality N` is unchanged (the user's number wins).

## Testing

- **Default knobs:** `codecDefaultKnobs("jpeg")=={"q":"85"}`,
  `codecDefaultKnobs("jpegxl")=={"distance":"1.0"}`, `("png")=={}`.
- **resolveTransformCodec absent-quality:** `("avif","",90)` → knobs `{"q":"85"}`
  (NOT 90); `("jpegxl","",90)` → `{"distance":"1.0"}`; `("jpeg","85-ish")` explicit
  still honored.
- **Integration:** `convert --to tiff --codec jpegxl` (no --quality) writes a JXL at
  distance-1.0 (decodes; not the more-lossy 1.5). `convert --to tiff --factor 2`
  (jpeg, no --quality) is now q85 (`info` shows Q≈83, not Q≈88). `convert --to svs
  --factor 2` likewise q85. crop ≡ downsample pixel parity holds (both q85).
  `--quality 95` still overrides.
- `qFromKnobs`: `{"q":"70"}`→70, `{"distance":"1.0"}`→85, `{}`→85.
- jpeg2000 reversible (`--quality reversible=true`) still lossless; full `-race`.

## Boundaries

**In scope:** the per-codec default map + `qFromKnobs`, the `resolveTransformCodec`
seed, the `parseQualityKnobs` 90→85 revert, caller int-quality derivation.
**Deferred:** per-codec *non-default* tuning (the map is the seam); exposing codec
knobs (distance/effort/speed) as first-class CLI flags.
