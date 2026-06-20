# SP3c — finish the codec story — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** SP3c (Phase 1 + 1b + 2 merged, main@33c23f2). These are the three
"finish the codec story" follow-ons deferred from Phase 1/2.

## Goal

Make the SVS path fully codec-uniform and the `--quality` default consistent:

1. **SVS crop/downsample emitter codecs** — `convert --to svs --rect|--factor
   --codec jpeg2000` writes a conformant J2K SVS (today the SVS crop/downsample
   emitters are jpeg-only; the SVS *transcode* path already does jpeg2000).
2. **`--quality` default unification** — every pixel re-encode (crop, downsample,
   transcode) defaults to the **target codec's standard default** (jpeg 90), never
   the source quality; verbatim/lossless paths preserve the source exactly.
3. **reversible knob through `--factor`** — `convert --factor --codec jpeg2000
   --quality reversible=true` (lossless-J2K downsample) instead of erroring.

## 1. SVS crop/downsample emitter codecs (jpeg + jpeg2000)

Aperio encodes the codec in the ImageDescription geometry line:
`… (256x256) JPEG/RGB Q=70 …` (jpeg) vs `… (256x256) J2K/YUV16 Q=70 …` (jpeg2000)
— measured on `JP2K-33003-1.svs`.

- **Thread the codec into the SVS emitters.** `cropEmitSVS` (`crop.go`) and
  `downsampleToSVS` (`convert_factor.go`) gain the resolved codec (`fac`, `knobs`,
  `codecName`) and pass it to `buildEnginePyramid` (already codec-configurable
  since 3c) instead of the hardcoded jpeg factory.
- **Codestream descriptor.** A helper `aperioCodecDescriptor(codec) → "JPEG/RGB" |
  "J2K/YUV16"` feeds the Aperio description builders:
  - `BuildCropImageDescription` (crop) takes the codec → uses the descriptor (it
    hardcodes `JPEG/RGB` at `svs_imagedesc.go:161`).
  - `SyntheticAperioDescription` (non-SVS→SVS) takes the codec (hardcodes
    `JPEG/RGB` at `:180`).
  - SVS-source downsample uses `MutateForDownsample` on the *source* desc, which
    keeps the source's descriptor — correct for **same-codec** downsample; when the
    downsample **changes** the codec (jpeg→jpeg2000), the descriptor must be
    rewritten to `J2K/YUV16` (a small `setAperioCodecDescriptor(desc, codec)` on the
    geometry line).
- **Guard.** The SVS jpeg-only guards (`crop.go:214`, `convert_factor.go:84`) widen
  from `{jpeg}` to **`{jpeg, jpeg2000}`** — the conformant + emitter-capable SVS
  set. Non-conformant SVS codecs (avif/webp/htj2k/jpegxl) still error with the
  redirect (use `--to tiff`, or the SVS transcode path via `--allow-nonconformant`).
  Source the set from `containerCapabilities("svs").conformant` (Phase-2 table) so
  there's one source of truth.

## 2. `--quality` default rule: re-encode ⇒ codec standard default

**If the pixels are re-encoded, the default quality is the target codec's standard
default — never the source quality, even for a same-codec re-encode.** Verbatim
paths (tile-copy, lossless crop) preserve the source bytes exactly and have no
quality knob.

| Operation | Default `--quality` |
|---|---|
| tile-copy (`convert --to X` no `--codec`) / `crop --lossless` | n/a — bytes preserved |
| lossy crop (re-encode) | codec default **(SVS crop today: source-Q — changes)** |
| downsample (re-encode) | codec default (already ~90) |
| transcode (re-encode) | codec default **(today 85 — changes)** |

The standard jpeg default is **90** (the dominant value already in the tree:
downsample, non-SVS crop; matches the 2026-06-14 "jpeg 90" intent). The exact
number isn't load-bearing (≈85–90 all fine); the point is **one** standard default
per codec, applied to every re-encode.

This **removes** the source-codec-sameness logic — there is no `defaultQuality(...)`
helper keyed on source vs output codec. Concretely:
- `cropEmitSVS` **stops** reading `desc.Quality()` for its default — it uses the
  codec default (90 for jpeg) like every other re-encode.
- `runConvertFactor`'s int default stays 90.
- `parseQualityKnobs`' default aligns **85 → 90** so the transcode path
  (`runConvertTIFFReencode`) and the knob fallback also land on 90.

Two behavior changes, both toward one uniform re-encode default:
- **SVS crop: source-Q → 90.**
- **transcode / codec-knob default: 85 → 90.**

**Bonus consistency win:** this fixes the crop-vs-downsample quality mismatch noted
in Slice 3b — a full-rect crop and a plain downsample now both encode at 90 and
produce pixel-identical output.

Implementation: `cropEmitSVS` already reads source-Q when `quality == 0`
(`desc.Quality()`); `downsampleToSVS` adopts the same — when `--quality` is absent
and the output codec equals the source codec, use `desc.Quality()` (else 90). The
non-SVS downsample emitters keep 90.

## 3. reversible knob through `--factor`

`runConvertFactor` (`convert_factor.go:106`) validates `--quality` with
`fmt.Sscanf(cvQuality, "%d", &quality)`, which **rejects** any non-integer
(`reversible=true`) before the codec ever sees it. Replace it: parse `cvQuality`
with `parseQualityKnobs` (the same parser the transcode path uses) to derive the
integer `q` for the fallback/SVS-jpeg path, and let the full `cvQuality` string flow
to `resolveTransformCodec` (which the downsample emitters already call and which
parses the knobs). Then `--quality reversible=true` is honored: `q` falls back to
the codec default, `reversible=true` reaches the J2K encoder.

(Note: `resolveTransformCodec(codecName, cvQuality, fallbackQ)` already seeds knobs
with `fallbackQ` and overrides from `cvQuality` when non-empty — so the only blocker
is the up-front `Sscanf`. After it's removed, the lossless-J2K downsample works.)

## Components

| Unit | Responsibility | Source |
|---|---|---|
| `aperioCodecDescriptor` / `setAperioCodecDescriptor` | jpeg→`JPEG/RGB`, jpeg2000→`J2K/YUV16`; set the descriptor on a geometry line | `svs_imagedesc.go` |
| `cropEmitSVS` / `downsampleToSVS` | accept the codec; pass `fac`/`knobs` to the engine; emit the codec-correct Aperio desc; widen guard to jpeg\|jpeg2000; default quality = codec default (NOT source-Q) | `crop.go`, `convert_factor.go` |
| `parseQualityKnobs` default | 85 → 90 (the standard re-encode default) | `convert_tiff.go` |
| `runConvertFactor` quality parse | `parseQualityKnobs` instead of `Sscanf`; int default 90 | `convert_factor.go` |

## Testing

- **J2K SVS round-trip:** `convert --to svs --factor 2 --codec jpeg2000` and
  `convert --to svs --rect … --codec jpeg2000` produce a file that re-detects as
  **svs** with **jpeg2000** tiles and the geometry line says `J2K/YUV16`; pixels
  decode (`hash --mode pixel`).
- **Guard:** `--to svs --rect --codec avif` still errors (use --to tiff); jpeg +
  jpeg2000 pass.
- **Quality unification:** `crop <svs>` (no --quality) now emits at the codec
  default (Q≈90), NOT the source Q — assert the output's `Q=` token is the standard
  default, not the source's. `downsample <svs>` likewise. A **transcode**
  (`convert --to svs --codec jpeg`) is also the codec default. **Crop ≡ downsample
  pixel parity:** a full-rect `crop` and a plain `downsample` of the same SVS now
  produce identical pixel hashes (both at 90) — the Slice-3b mismatch is gone.
- **reversible knob:** `convert --to tiff --factor 2 --codec jpeg2000 --quality
  reversible=true` succeeds (no "must be an integer" error); the J2K tiles are
  reversible (lossless) — pixel round-trip is exact vs the box-reduced source.
- **jpeg default unchanged:** all no-`--codec` / non-SVS jpeg paths byte-identical
  (the quality rule only fires for SVS-same-codec crop/downsample).
- Full `-race`.

## Boundaries / deferred

**In scope:** SVS crop/downsample for jpeg + jpeg2000 (the conformant set); the
operation-based `--quality` default; the reversible-knob fix.

**Deferred:** non-conformant SVS codecs via the crop/downsample emitters (stay
transcode-only); the `validate(spec)` unification; bit-depth/colorspace; the other
slotted axes.
