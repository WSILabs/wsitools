# SP3a — DICOM via the retile engine — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** the streaming retile engine (SP1 + SP2 M1–M5, merged). SP3a is the first SP3 (convergence) sub-project.
**Prior specs:** `docs/superpowers/specs/2026-06-18-retile-engine-sp2*.md`.

## Goal

Route **lossy DICOM transforms** — `convert --to dicom --factor`, `downsample
<dicom>`, `crop <dicom>` (default) — through the `internal/retile` engine instead
of the `derivedsource` reduced-raster path. Concrete wins: **JP2K / HTJ2K derived
levels** for DICOM (today JPEG-baseline only), bounded memory, and it unlocks
deleting the JPEG-baseline raster machinery.

## Background — the push/pull mismatch

Today: the driver materializes a reduced/cropped L0 raster
(`downscale.Materialize{Reduced,Cropped}L0`), wraps it as a `source.Source` via
`derivedsource.FromReducedL0` (whose `rasterLevel` lazily **JPEG-baseline**
re-encodes tiles on `TileInto`, via a worker pool), and hands it to
`dicomwriter.WritePyramid(src, opts, newWriter)`, which **pulls** frames in
TILED_FULL order. The retile engine **pushes** encoded tiles to a `TileSink`
out-of-order. SP3a bridges push→pull with a **disk spool**.

## 1. The spool bridge (approved: approach A)

Two new units in `internal/dicomwriter` (or a small shared package):

- **`spoolTileSink`** — implements `retile.TileSink`. The engine pushes
  `WriteTile(level, col, row, frame)`; the sink appends each frame to a per-level
  spool file and records its `(offset, len)` in a per-level index (`tilesX×tilesY`
  slots). Bounded memory (disk-backed), like `cogwsiwriter`'s spool-and-finalize.
  Reuse/extract `cogwsiwriter`'s spool primitive (`openSpool`/`Append`) into a
  shared helper, or write a small focused one.
- **`spoolSource`** — implements `source.Source` over the finished spool. `Levels()`
  returns `spoolLevel`s whose `TileInto(x,y,dst)` reads the **verbatim** spooled
  frame (no re-encode — the engine already encoded). It carries the
  **scale-adjusted `Metadata`** (MPP×factor / crop offset), the source `Format`,
  and the `Associated` images, so `dicomwriter` reads PixelSpacing/associated
  exactly as it does from `derivedsource` today. `Compression()` reports the
  codec's `source.Compression` (JPEG/JPEG2000/HTJ2K) so the writer picks the right
  transfer-syntax UID.

`dicomwriter.WritePyramid` is **unchanged** — it still pulls from a `source.Source`.

## 2. The encoder — self-contained DICOM frames (NOT abbreviated)

**Critical:** DICOM has no TIFF-style shared-tables mechanism (no tag-347 /
`LevelHeader`). Every frame must be a **complete, self-contained codestream**. So
the DICOM engine path must NOT use the TIFF family's abbreviated `codecTileEncoder`
(which emits table-less JPEG tiles). It needs a **standalone-frame encoder**:
- **JPEG:** `jpeg.Encoder.EncodeStandalone` (full SOI+DQT+DHT+SOS+EOI per frame) —
  the same call M1's DZI `dziStandaloneEncoder` and today's `rasterLevel` already
  use.
- **JP2K / HTJ2K:** `codec.Encoder.EncodeTile` already returns a complete J2K
  codestream (J2K has no abbreviated/shared-tables form), so it is DICOM-ready as-is.

A `dicomFrameEncoder` adapter (implements `retile.TileEncoder`) selects per codec:
standalone JPEG, or pass-through `EncodeTile` for J2K-family. Default codec
**JPEG** (matches today's output, no surprise); `--codec jpeg2000|htj2k` enables
the J2K-family frames — the payoff. (Whether the DICOM transform commands already
accept `--codec` is wired up here.)

## 3. Scope & routing (mirrors M4/M5)

- **Lossy** DICOM transforms → engine + spool:
  - `downsampleToDICOM` (`convert --to dicom --factor`, `downsample <dicom>`):
    `SrcRegion =` full L0, `OutL0 = L0/factor`, octave-floored
    (`octaveLevelSpecsFor` / `flooredLevelCount`), Box kernel, `dicomFrameEncoder`,
    `spoolTileSink` → `spoolSource` → `dicomwriter.WritePyramid`.
  - `cropToDICOM` default: `SrcRegion =` the crop rect, `OutL0 =` rect, Nearest.
- **Lossless** DICOM crop (`crop <dicom> --lossless`) stays on
  `derivedsource.WithLosslessL0` (`passthroughLevel`, verbatim L0 frames) — the
  engine read isn't byte-exact (the M4/M5 finding). `passthroughLevel` survives.
- `convert --to dicom` (verbatim WSM copy, no transform) is **unchanged**.
- Metadata scale-adjustment (MPP×factor, mag÷factor) moves from `FromReducedL0`'s
  caller into the `spoolSource` construction (same values).
- Octave-floored output (consistent with M2/M3/M5). The output level count differs
  from today's `len(slide.Levels())`-based count — same approved trade-off as M3.

## 4. Cleanup unlocked

Once the lossy DICOM path is on the engine, the JPEG-baseline raster machinery is
dead and deletable: `derivedsource.rasterLevel`, `FromReducedL0`,
`TranscodeToJPEG` (if unused), and the **lossy** uses of `MaterializeReducedL0` /
`MaterializeCroppedL0`. KEEP: `derivedsource.passthroughLevel` +
`WithLosslessL0` (DICOM lossless crop) and the TIFF-family lossless-crop raster
builders (`buildPyramidFromRaster*` — used by TIFF/cog-wsi lossless crop, NOT a
DICOM concern). Do the deletion as the final SP3a step, gated on the engine path
passing `dciodvfy`. (Audit each symbol's callers before removing.)

## 5. Components

| Unit | Responsibility | Depends on |
|---|---|---|
| `spoolTileSink` | engine push → per-level disk spool + offset index | retile.TileSink, spool primitive |
| `spoolSource` | `source.Source` over the spool (verbatim frames + adjusted md + associated + codec compression) | source.Source/Level, spool |
| `dicomFrameEncoder` | self-contained frames per codec (standalone JPEG / J2K passthrough) | internal/codec, internal/codec/jpeg |
| DICOM drivers (reorged) | lossy `downsampleToDICOM`/`cropToDICOM` → octave levels + engine + spool + `WritePyramid`; lossless crop unchanged | retile.Run, runEngineRetile-style, dicomwriter |

## 6. Error handling

`retile.Run` first-error propagation (M3 hardening) + the spool sink's finish
(flush + close spool files) surface errors before `WritePyramid` runs. Atomic
output: `dicomwriter.WritePyramid` already writes to a temp dir → rename; the spool
is scratch (temp dir, cleaned up). On engine error, discard the spool + don't call
`WritePyramid`.

## 7. Testing

- **DICOM downsample/crop via engine:** read back via the opentile DICOM reader —
  octave-floored level dims, MPP×factor / crop offset, associated preserved;
  **`dciodvfy` 0 errors** on every instance (the existing DICOM conformance gate;
  `dciodvfy` is the dclunie precompiled macexe, not brew).
- **`--codec jpeg2000` derived levels:** output frames are JP2K with the correct
  transfer-syntax UID and decode correctly (round-trip via opentile); HTJ2K
  likewise if wired.
- **Lossless DICOM crop still byte-exact** (unchanged `passthrough` path) — guard.
- **Bounded memory:** a large DICOM downsample no longer allocates the
  `outW*outH*3` reduced raster (the spool is disk-backed).
- Existing DICOM suite (dciodvfy, round-trip) + the JP2K/HTJ2K fixtures green; full
  `-race`.

## 8. Boundaries / deferred

**In SP3a:** spool bridge (`spoolTileSink` + `spoolSource`), lossy DICOM transforms
via the engine, self-contained `dicomFrameEncoder`, `--codec` for DICOM transforms,
and deleting the now-dead JPEG-baseline raster path.

**Deferred:** SP3b (BIF sink), SP3c (CLI convergence). Refactoring `dicomwriter`
itself to push (bridge approach C) — only if the spool proves limiting. The
TIFF-family lossless-crop raster builders stay (separate concern).
