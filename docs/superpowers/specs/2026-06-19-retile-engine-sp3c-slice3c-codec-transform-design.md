# SP3c Slice 3c — `--codec` on the transform path — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** SP3c unified-transform convert (Phase 1). Slices 1, 2, 3a, 3b(+SVS,+DZI),
4 are shipped/merged (3a/3b on main@da4e80f; DZI-rect + transcode on
`feat/retile-engine-sp3c-2`). This is the last Phase-1 slice.
**Umbrella spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-unified-convert-design.md`.

## Goal

Allow `--codec` to compose with `--rect` and/or `--factor`: `convert --rect …
--codec avif`, `convert --factor 2 --codec jpeg2000`, `convert --rect … --factor 2
--codec htj2k`, and the `crop`/`downsample` aliases likewise. Today the
crop/downsample engine path is **jpeg-only** and `validateRectCombo` hard-rejects
`--rect --codec`; `convert --factor --codec` already partially works via the
factor dispatch but should share one codec-configurable engine path.

## Key insight: the engine is already codec-capable; the emitters hardcode jpeg

`buildEnginePyramid` (the crop/downsample TIFF-family engine driver) already drives
`runEngineRetile` with a **`codecTileEncoder`** — the same generic
`codec.Encoder → retile.TileEncoder` adapter the transcode path uses. It just
hardcodes two things:

1. the **encoder**: `jpegcodec.Factory{}.NewEncoder(...)`; and
2. the **`streamwriter.LevelSpec`**: `Compression: tiff.CompressionJPEG`,
   `Photometric: 2`, `SamplesPerPixel: 3`, `BitsPerSample: [8,8,8]`,
   `JPEGTables: enc.LevelHeader()`.

The **transcode path** (`transcodeLevel` in `convert_tiff.go`) already builds the
**codec-aware** equivalent: `Compression` from `enc.TIFFCompressionTag()`,
`JPEGTables`/tables from `enc.LevelHeader()`, chroma subsampling via
`encoderChromaSubsampling`, photometric per codec. So 3c is: **make
`buildEnginePyramid` (and `buildEnginePyramidCOGWSI`) codec-configurable by reusing
the transcode path's codec-aware LevelSpec construction**, then thread `--codec`
through the crop/downsample emitters. `runDICOMEngine` **already takes a
`codecName`** (from SP3a) — the DICOM emitters just pass the real codec instead of
the hardcoded `"jpeg"`.

## No lossless-source fork here (unlike pure transcode)

The M4 finding — that the engine's ScaledStrips read is not byte-identical, so a
**lossless-codec transcode at identical geometry** must use the per-level
`transcodePyramid` instead of the engine — applies **only to pure transcode** (no
`--rect`, no `--factor`), which stays on the existing path. A `--rect`/`--factor`
operation is **inherently a re-encode** (crop decodes the region; downsample
box-reduces): there is no source-byte-preservation expectation. So a reversible
codec (`--codec jpeg2000 --quality reversible=true`) combined with `--rect`/`--factor`
is correct through the engine — it losslessly encodes the *transformed* pixels.
**3c therefore needs no lossless special-casing**; it only handles the
geometry-change + codec case.

## Routing

- **Pure transcode** (`convert --codec X` / `transcode --codec X`, no rect/factor):
  unchanged — the existing `transcodePyramid` select-octave path (preserves source
  levels; lossless-codec → per-level). NOT touched by 3c.
- **Geometry change + codec** (`--rect` and/or `--factor`, with `--codec`):
  the crop/downsample engine emitters, now codec-configurable. octave-floored
  (consistent with 3a/3b).
- `validateRectCombo` drops the `--codec` rejection. (For dzi/szi, `--codec` is
  already the tile-format selector — out of scope here; `--rect --to dzi --codec
  jpeg|png` already works via Slice 3b-DZI.)

## Components

| Unit | Responsibility | Source |
|---|---|---|
| `engineLevelEncoder(fac, knobs, dims…) → (codecTileEncoder, levelSpecFields)` | codec-aware encoder + the LevelSpec fields (Compression, tables, photometric, subsampling) for a streamwriter level | **extract** from `transcodeLevel`'s existing logic (`encoderChromaSubsampling`, `TIFFCompressionTag`, `LevelHeader`) so transcode and the engine share ONE codec→levelspec mapping |
| `buildEnginePyramid` / `buildEnginePyramidCOGWSI` | gain `(fac codec.EncoderFactory, knobs map[string]string)`; default = jpeg (existing callers pass jpeg ⇒ byte-identical to today) | `downsample.go` / `convert_factor.go` |
| crop emitters (`cropTo{TIFF,OMETIFF,COGWSI}`) + downsample emitters | resolve `--codec` → `(fac, knobs)`; pass to the engine builders; set the writer's container/codec metadata (ImageDescription codec= field) | `crop_formats.go`, `convert_factor.go` |
| `cropToDICOM` / `downsampleToDICOM` | pass the resolved `codecName` to `runDICOMEngine` (instead of `"jpeg"`) | `crop_formats.go`, `convert_factor.go` |
| `runConvert` rect block + `validateRectCombo` | drop `--codec` rejection; resolve `--codec`/`--quality` and thread through `runCrop` (which gains codec params) | `convert.go`, `crop.go` |
| quality knobs | `convert`'s string `--quality` → codec knobs (the transcode path already parses these — reuse) | existing knob parsing |

## Scope & conformance

- **In:** `--codec` with `--rect`/`--factor` for tiff/ome-tiff/cog-wsi/dicom; the
  shared codec-aware engine LevelSpec; codec threaded to `runDICOMEngine`; guard
  dropped; `crop`/`downsample` aliases gain `--codec`.
- **SVS:** `--codec` into an SVS container with `--rect`/`--factor` produces a
  non-Aperio TIFF when codec ∉ {jpeg, jpeg2000} — same conformance question as the
  umbrella spec's Phase-2 table. 3c keeps the **existing ad-hoc per-driver codec
  checks** (e.g. SVS already restricts its codec set); full conformance gating is
  **Phase 2**. Where an ad-hoc check doesn't exist, 3c errors for the clearly
  non-conformant SVS+exotic-codec combos and defers the nuanced cases to Phase 2.
- **DICOM:** codec limited to what `newDicomFrameEncoder` supports (jpeg,
  jpeg2000, htj2k) — already enforced there; an unsupported `--codec` for `--to
  dicom` errors as it does today.
- **Out:** pure-transcode path changes; dzi/szi (their `--codec` is the tile
  format); the Phase-2 conformance table; the crop-vs-downsample `--quality`
  default unification (separate item).

## Testing

- **Composition:** `convert --rect … --codec jpeg2000`, `convert --factor 2
  --codec avif`, `convert --rect … --factor 2 --codec htj2k` over tiff/ome-tiff/
  cog-wsi → output re-detects, tiles are the requested codec (decode round-trip via
  opentile), dims/levels correct.
- **DICOM:** `convert --to dicom --rect … --factor 2 --codec jpeg2000` →
  `dciodvfy` 0 errors, JP2K transfer syntax.
- **jpeg default unchanged:** `convert --rect … --factor 2` (no `--codec`) is
  pixel-identical to the 3b output (the jpeg default path must be byte-for-byte the
  same — the engine builders default to jpeg).
- **Shared LevelSpec parity:** a transcode and a factor-1 full-rect crop at the
  same `--codec` produce the same per-level Compression/photometric/subsampling
  tags (the extracted helper is the single source of truth).
- **Guard removed:** `convert --rect --codec X` no longer errors; full `-race`.

## Risks

- **The codec-aware LevelSpec extraction** is the delicate part: `transcodeLevel`
  threads subsampling/photometric/compression-tag per codec; the extracted helper
  must reproduce it exactly so transcode output is unchanged. Mitigation: extract
  with transcode's existing tests green first (refactor under test), then point the
  engine builders at it.
- **cog-wsi LevelSpec** differs from streamwriter's (cogwsiwriter has its own
  spec type) — the codec mapping must be applied in both writers' idioms.
