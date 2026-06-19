# SP2 — Milestone 5 — lossy crop via the engine — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** `docs/superpowers/specs/2026-06-18-retile-engine-sp2-design.md` (M5 of M1–M5 — the last SP2 transform path).
**Builds on:** M1–M4 (merges `9ef7027`/`39281de`/`00f9e2c`/`f9323dd`).

## Goal

Route **lossy `crop`** (svs/tiff/ome-tiff/cog-wsi) through the `internal/retile`
engine, replacing the cropped-L0-in-RAM raster build. Mechanically this is M3
(downsample) with `SrcRegion =` the crop rect instead of the full L0.

**Standalone value:** kills the cropped-L0 raster allocation (`make([]byte,
ew*eh*3)` via `downscale.MaterializeCroppedL0`) — a C5-like win on large crops —
and completes the SP2 convergence (all from-scratch transform paths on one engine).

## Mechanism — M3 with a sub-region

Crop is identity-scale of a sub-rectangle. For the engine:
- `SrcRegion = opentile.Region{Origin:{ex,ey}, Size:{ew,eh}}` (the effective crop
  rect; the lossy path uses the exact requested rect, not the tile-snapped one).
- `OutL0 = {ew,eh}` (identity — no downscale).
- `Kernel = resample.Nearest` (identity read; `ScaledStrips` crops/composites the
  arbitrary region and yields strips, then the engine box-2× derives the pyramid).

Crop is **jpeg-only / format-preserving** (quality-based, no `--codec`), so the
engine path uses the jpeg `codecTileEncoder` (like M3's `buildPyramid`). No
codec-lossless probing (unlike M4) — crop has an explicit `--lossless` flag.

## Level count — unify on `flooredLevelCount` (standardization)

Crop currently uses `cropPyramidLevels` (stops while `w/2 ≥ tile`), which yields
one fewer, larger bottom level than `flooredLevelCount` (M2/M3's rule, stops at
`min(w,h) ≤ tile`). **M5 unifies crop on `flooredLevelCount`** so every from-scratch
engine path (M2 stitched, M3 downsample, M5 crop) shares one level-count rule and
crop gets the more complete/conventional pyramid (a smaller bottom overview level).

To keep crop **internally** consistent, the `flooredLevelCount` switch applies to
BOTH crop paths: the lossy (engine) path builds levels via
`octaveLevelSpecsFor(Size{ew,eh}, outputTileSize)` (which uses `flooredLevelCount`),
and the `--lossless` (raster) path's lower-level rebuild switches its `nLevels`
from `cropPyramidLevels` to `flooredLevelCount(ew,eh,outputTileSize)`. `cropPyramidLevels`
is removed (or becomes an alias) once both paths use the unified rule.

This is a one-time, minor change to crop's output (≈1 extra small level); accepted
in favor of standardization + output quality.

## Lossy → engine, lossless → unchanged (flag-gated)

The `--lossless` flag (`cropLossless`) is the explicit signal:
- **`--lossless` (Strategy B):** byte-identical verbatim L0 tile copy
  (`writeLosslessL0`) + raster-rebuilt lower levels — stays on the existing path
  (byte-exact; the engine's `ScaledStrips` read is NOT byte-identical anyway, per
  the M4 finding). Only its `nLevels` changes to `flooredLevelCount` (above).
- **default lossy (Strategy A):** routes through the engine (this milestone).

DICOM crop (`cropToDICOM`) stays on the `derivedsource` path (no engine sink for
DICOM yet — deferred). The raster builders (`buildPyramidFromRaster*`,
`MaterializeCroppedL0`, `halveRaster`) stay (lossless crop still uses them).

## The one new piece — streaming thumbnail

Today `regenCropThumbnail(wtr, outL0, ew, eh, quality)` downscales the full decoded
crop raster. The engine streams (no raster), so M5 regenerates the crop thumbnail
from a **separate small region read**: `Pyramid(0).ScaledStrips(rect, thumbDims,
…, WithStripKernel(Box))` (or a single `ReadRegion`-equivalent) decoding the crop
rect downscaled to thumbnail dimensions → encode JPEG → emit at IFD 1 (svs, between
L0 and L1) / the format's associated slot. It is a second decode, but tiny
(~1024 px) relative to the crop. Compute thumbnail dims the same way
`regenCropThumbnail` does today (read its sizing logic; reproduce it on the small
read so the thumbnail matches the current geometry). Associated label/macro copy is
unchanged.

A small helper `streamCropThumbnail(slide, rect, thumbW, thumbH, quality) ([]byte,
error)` produces the encoded thumbnail; the per-format driver writes it where it
writes associated images today.

## Reuse — generalize the M3 engine helper to a sub-region

M3's `runDownsampleEngine(ctx, slide, srcL0, outL0, levels, enc, sink, workers)`
hardcodes `SrcRegion = {origin 0,0, size srcL0}` and `Kernel = Box`. Generalize it
to take a `srcRegion opentile.Region` and DERIVE the kernel — **`Nearest` when
`outL0 == srcRegion.Size` (identity: crop, or factor-1), else `Box` (downscale:
downsample)**. Rename to `runEngineRetile(ctx, slide, srcRegion opentile.Region,
outL0 opentile.Size, levels, enc, sink, workers)`. Update M3's two callers
(`buildPyramid`, `buildPyramidCOGWSI`) to pass `opentile.Region{Origin:{0,0},
Size:srcL0}`. Then:
- downsample: `srcRegion =` full L0, `outL0 =` L0/factor → Box.
- crop: `srcRegion =` rect, `outL0 =` rect size → Nearest.

The progress bar + unconditional `finish()` + `countingSink` carry over unchanged.

## Per-format drivers

Each lossy crop emit path (`cropEmitSVS` for svs; `cropToTIFF`/`cropToOMETIFF`/
`cropToCOGWSI` for the rest) keeps its writer setup + crop `ImageDescription`
(`BuildCropImageDescription`) + MPP/mag/ICC + label/macro associated emission, and
swaps ONLY the `MaterializeCroppedL0` + `buildPyramidFromRaster` block for: build
`octaveLevelSpecsFor` levels → `AddLevel` each (per-container shaping, reusing M2/M3
spec logic) → wrap a sink → `runEngineRetile(srcRegion=rect, outL0=rect)` →
`streamCropThumbnail` at IFD 1 (svs). The lossy path no longer materializes the
cropped raster.

To avoid up-front raster materialization, `runCrop`/`cropEmitSVS` must NOT
`MaterializeCroppedL0` on the lossy path; materialize only on the `--lossless`
path (which needs it for the thumbnail + lower-level rebuild). Thread the slide +
rect to the emit functions instead of the pre-materialized `cropEmitParams.l0`.

## Error handling

The M3/M4 engine hardening (encode-error propagation; real-error-not-cancel) and
the unconditional-`finish()` discipline carry over via `runEngineRetile`. Atomic
temp→rename / `wtr.Abort()` on error unchanged.

## Testing

- **Lossy crop (svs/tiff/ome-tiff/cog-wsi):** crop a rect, read back via opentile;
  assert L0 dims == rect (ew×eh), level count == `flooredLevelCount(ew,eh,tile)`,
  thumbnail present (svs: at IFD 1), MPP/mag/ICC + crop `ImageDescription`
  preserved, validate clean.
- **Pixel-equivalence:** the cropped L0 region vs the source's same region ≈ JPEG
  re-encode noise (mean small) — confirms the rect read is correctly placed.
- **`--lossless` crop still byte-identical** (unchanged verbatim L0 path) — a
  regression guard; only its level count may grow by the unified `flooredLevelCount`.
- **Bounded memory:** a large lossy crop no longer allocates `ew*eh*3`.
- **Level-count unification:** a unit test that crop's level count now equals
  `flooredLevelCount` (e.g. the 1000×800/tile-256 case → 3 levels, was 2).
- Existing crop suite (CMU-2 parity, byte-identity matrix) stays green; full `-race`.

## Component summary

| Unit | Responsibility | Depends on |
|---|---|---|
| `runEngineRetile` (generalized) | engine pass over a `srcRegion` (kernel derived) | retile.Run, sinks, encoder |
| lossy `cropEmit*` (reorged) | writer + crop metadata + associated (unchanged) + levels + sink + engine + streaming thumbnail | runEngineRetile, octaveLevelSpecsFor, streamCropThumbnail |
| `streamCropThumbnail` | small ScaledStrips read of the rect → thumbnail JPEG | opentile ScaledStrips, jpeg |
| crop level count | unified on `flooredLevelCount` (lossy + lossless) | — |

## Deferred (post-M5 → SP3)

- Delete the now-bypassed lossy-crop raster path + all bypassed raster code
  (`buildPyramidFromRaster*`, `MaterializeReducedL0`/`MaterializeCroppedL0` once
  lossless crop is the only remaining user, etc.).
- CLI convergence: crop/downsample/transcode as formal aliases, `validate()`
  capability table, `--rect` on `convert`.
- BIF sink (engine→BIF); DICOM-via-engine (crop + factor).
- M5 keeps lossless crop on the raster path; an engine-based lossless crop would
  need a byte-exact engine read (not available).
