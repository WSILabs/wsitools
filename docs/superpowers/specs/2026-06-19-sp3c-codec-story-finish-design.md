# SP3c — finish the codec story — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** SP3c (Phase 1 + 1b + 2 merged, main@33c23f2). These are the three
"finish the codec story" follow-ons deferred from Phase 1/2.

## Goal

Make the SVS path fully codec-uniform and the `--quality` default consistent:

1. **Unify SVS into the shared crop/downsample path** — remove the bespoke
   positional `cropEmitSVS` special case (fold it into `cropToSVS(p
   cropEmitParams)`), so SVS gains codec support like every other container.
   `convert --to svs --rect|--factor --codec jpeg2000` then writes a conformant
   J2K SVS (today the SVS crop/downsample emitters are jpeg-only).
2. **`--quality` default unification** — every pixel re-encode (crop, downsample,
   transcode) defaults to the **target codec's standard default** (jpeg 90), never
   the source quality; verbatim/lossless paths preserve the source exactly.
3. **reversible knob through `--factor`** — `convert --factor --codec jpeg2000
   --quality reversible=true` (lossless-J2K downsample) instead of erroring.

## 1. Unify SVS into the shared crop/downsample path (and gain jpeg2000)

An **SVS *is* a generic TIFF** — same tiles, pyramid, writer, associated images —
that differs only in its **ImageDescription** (the Aperio header + geometry line
+ provenance chain) and detection. Yet the crop path special-cases it:
`cropTo{TIFF,OMETIFF,COGWSI,DICOM}` share the `cropEmitParams` dispatch, but
`cropEmitSVS` is a separate **positional-args** function that `runCrop` calls via an
early `if target == "svs"` **bypass** — it predates the `cropEmitParams` refactor
and was never folded in. That's why it didn't get the 3c codec/quality plumbing for
free. Fixing #1 by threading codec into the bespoke emitter would *entrench* the
special case; instead we **remove it**.

- **Fold `cropEmitSVS` → `cropToSVS(p cropEmitParams)`** — a peer of `cropToTIFF` /
  `cropToOMETIFF` in the dispatch switch (the transcode path already proves the
  TIFF family unifies; this brings the crop path in line). It uses `p.fac`/`p.knobs`
  (codec, for free), `p.stx0`/`p.sty0`/… (runCrop's snap — dropping `cropEmitSVS`'s
  **duplicate** internal snap), `p.outW`/`p.outH`, etc., exactly like its peers. The
  *only* SVS-specific code is **building the Aperio ImageDescription** (rawDesc →
  `BuildCropImageDescription` + `scaleAperioResolutionTokens` + MPP-from-Aperio-desc).
- `runCrop`: delete the `if target == "svs" { cropEmitSVS(...) }` bypass; let svs
  flow into the `cropEmitParams` construction and `switch target { … case "svs":
  cropToSVS(p) }`. **Delete `cropEmitSVS`.**
- **Downsample side:** `downsampleToSVS` is already a per-format peer of
  `downsampleToTIFF` (which takes `codecName`); it just needs the same codec
  threading (no special-case to remove there) — add `codecName`, resolve `fac`/
  `knobs` via `resolveTransformCodec`, pass to `buildPyramid`.

**Codestream descriptor (the only genuinely SVS-specific new code).** Aperio
encodes the codec in the geometry line: `… (256x256) JPEG/RGB Q=70 …` (jpeg) vs
`… (256x256) J2K/YUV16 Q=70 …` (jpeg2000) — measured on `JP2K-33003-1.svs`. (opentile
decodes via the TIFF Compression tag, so this is fidelity, not readability.) A
helper `aperioCodecDescriptor(codec) → "JPEG/RGB" | "J2K/YUV16"` feeds:
- `BuildCropImageDescription` (takes the codec; hardcodes `JPEG/RGB` at
  `svs_imagedesc.go:161`).
- `SyntheticAperioDescription` (non-SVS→SVS downsample; hardcodes `JPEG/RGB` at
  `:180`).
- SVS-source downsample: after `MutateForDownsample` (keeps the source descriptor —
  correct for same-codec), `setAperioCodecDescriptor(&desc, codec)` rewrites the
  token when the codec changes (jpeg→jpeg2000).

**Guard.** The SVS guards (`crop.go:214`, `convert_factor.go:84`) widen `{jpeg}` →
**`{jpeg, jpeg2000}`** (the conformant + emitter-capable set, sourced from
`containerCapabilities("svs").conformant`). Non-conformant SVS codecs (avif/etc.)
still error with the redirect (transcode-only).

**Outcome:** SVS is no longer a special case — it's a `cropEmitParams` peer like
every other container, codec/quality fall out automatically, and the duplicate snap
+ positional bypass are gone.

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
| `cropToSVS(p cropEmitParams)` (replaces `cropEmitSVS`) | a `cropEmitParams` peer in the crop switch; Aperio desc is the only SVS-specific part; `p.fac`/`p.knobs` give codec for free; drops the duplicate snap + positional bypass | `crop.go`, `crop_formats.go` |
| `downsampleToSVS` | thread `codecName` (parity with `downsampleToTIFF`); codec-correct Aperio desc | `convert_factor.go` |
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
