# DICOM derived-pyramid adapter (A1) — design

**Date:** 2026-06-14
**Status:** Approved design, ready for implementation plan.
**Survey item:** A1 from `docs/format-debt-survey-2026-06-13.md` — "DICOM
transform dead-end: no `downsample`, no `crop`, no `--factor` into DICOM."

## Goal

Make DICOM a first-class **transform target** so a reduced or cropped pyramid
can be written as a DICOM-WSM series — closing the gap where DICOM is read-only
for transforms. One new adapter feeds the existing, dciodvfy-validated
`dicomwriter.WritePyramid` from a *derived* pyramid instead of only a verbatim
frame-copy of an existing DICOM source.

## Background: the seam we're plugging into

`dicomwriter.WritePyramid(src source.Source, opts, newWriter)` already reads
**compressed** JPEG/JPEG-2000 tiles verbatim from `src.Levels()[i].TileInto`,
and `buildDescriptor` already probes a *non-DICOM* source's codec correctly
(deriving photometric/transfer-syntax from the actual frame bytes). So
converting an **existing** pyramid to DICOM already works — that is the shipped
SVS→DICOM full-pyramid path.

The only thing missing for downsample/crop-into-DICOM is a `source.Source`
whose levels present a **derived** (reduced or cropped) pyramid, JPEG-encoding
tiles on demand. That adapter is the keystone; once it exists, wiring the
callers is mechanical and the writer is untouched (save one small `Options`
flag).

## Scope

In scope (all three entry points, both modes):

1. `convert --to dicom --factor N` (any source → reduced DICOM pyramid).
2. `downsample --factor N <dicom>` (format-preserving DICOM → DICOM).
3. `crop <dicom>` — default (re-encode) **and** `--lossless` (verbatim L0
   frame-copy of the tile-snapped region + rebuilt lower levels).

Out of scope (deferred, with rationale):

- **JPEG-2000 / HTJ2K re-encode.** No JP2K or HTJ2K *encoder* exists
  (survey B1/B4); re-encoded levels are JPEG-baseline. JP2K has effectively no
  role in modern WSI; HTJ2K-encode is real future work that drops into the same
  seam (see Codec decisions).
- **`crop --lossless` for downsample/`--factor`.** Resampling is inherently
  lossy, so "lossless" applies to crop only.
- **DICOM crop thumbnail regen.** A cropped slide's passed-through
  thumbnail/overview still depict the whole slide; regenerating them for the
  cropped region is deferred (mirrors how associated passthrough was staged for
  the TIFF family). Associated images are carried through verbatim.
- **Uncompressed-native re-encode option.** The codebase could emit lossless
  Explicit-VR-LE native RGB, but at ~10–20× size; not worth a flag for A1.

## Architecture

Approach **A** (chosen over a second writer entry point or a narrower
frame-provider interface): a synthesized `source.Source` over the derived
pyramid. It leaves the validated writer's frame-emission and descriptor code
untouched; the two level kinds map exactly onto re-encode vs. lossless.

### New package `internal/derivedsource`

A `source.Source` implementation over a derived pyramid, with two level kinds:

- **`rasterLevel`** — holds an RGB raster (`[]byte`, tightly packed `W*H*3`)
  for that level, plus `tileSize` and JPEG `quality`. `TileInto(x,y,dst)`
  extracts the tile sub-rect (zero-padded at right/bottom edges) and
  JPEG-baseline-encodes it via the libjpeg-turbo encoder; `Compression()` →
  `source.CompressionJPEG`. Used for every re-encoded level. The encoder is the
  single codec seam — a future HTJ2K encoder drops in here without touching the
  adapter or writer.
- **`passthroughLevel`** — wraps a source `Level` plus a tile offset
  `(offX, offY)` and the output grid. `TileInto(x,y,dst)` returns the source's
  **verbatim** compressed frame at `(x+offX, y+offY)`; `Compression()` →
  the source's compression. `Size()`/`Grid()` report the snapped output
  geometry; `TileSize()` is the source tile size. Used only for a lossless
  crop's L0.

The `derived` struct implements `source.Source`:

- `Format()` → the **original** source format string (so `buildDescriptor`
  takes its codec-probe branch, not the DICOM-verbatim branch).
- `Levels()` → the kind-mixed level list (L0 first).
- `Metadata()` → factor-scaled metadata (see Metadata decisions).
- `Associated()` → the source's associated images, passed straight through.
- `SourceImageDescription()` → `""`.
- `Close()` → no-op (the underlying source is owned/closed by the caller).

Constructors (final signatures settled in the plan), conceptually:

- `FromReducedL0(l0 []byte, w, h, nLevels, tileSize, quality int, md source.Metadata, assoc []source.AssociatedImage) source.Source`
  — builds `nLevels` raster levels by box-halving L0. Serves downsample and
  `convert --factor` and the **re-encode** crop (with a cropped L0).
- `WithLosslessL0(srcL0 source.Level, offX, offY, gridW, gridH int, lowerL0Raster []byte, w, h, nLevels, tileSize, quality int, md, assoc) source.Source`
  — L0 = passthrough over `srcL0`; lower levels = raster levels box-halved from
  the decoded snapped region. Serves `crop --lossless`.

### Shared raster helper

Box-halving (`halveRaster`) and tile extraction currently live in
`cmd/wsitools`. Move them into `internal/downscale` so the existing streamwriter
path and the new adapter share one implementation. (Targeted refactor — the
streamwriter callers switch to the moved symbols; behavior unchanged.)

### One writer change

`dicomwriter.Options` gains `L0ImageType []string`. When non-nil, `writeInstance`
stamps it as L0's `ImageType` instead of the default `ORIGINAL/PRIMARY/VOLUME/
NONE` — because a reduced/cropped L0 is no longer the native acquisition. Lower
levels are unchanged (the existing `DERIVED/PRIMARY/VOLUME/RESAMPLED`, correct
since they are box-halved from L0 in every mode). A single `bool` is
insufficient because the L0 `[3]` value differs by operation (see below), so the
caller passes the exact 4-tuple:

- downsample / `--factor`: `{DERIVED, PRIMARY, VOLUME, RESAMPLED}` — L0 is
  resampled.
- crop (both modes): `{DERIVED, PRIMARY, VOLUME, NONE}` — L0 is a spatial subset
  at the source resolution, not resampled (lossless even copies the original
  frame bytes verbatim).

This is the only change to the writer; frame emission and `buildDescriptor` are
untouched.

### Shared emit helper

`emitDICOM(src source.Source, opts dicomwriter.Options, outDir string, force bool) error`
lifts `convert_dicom.go`'s temp-dir → `WritePyramid` → atomic-rename flow.
`convert_dicom.go`'s `writeDICOMPyramid` is refactored to call it; the three new
entry points call it too.

## Data flow

All paths end at `emitDICOM`; they differ only in how they build `derived`.

### ① `convert --to dicom --factor N` and `downsample --factor N <dicom>`

Shared body `downsampleToDICOM`:

1. `source.Open(input)`; resolve/validate factor `{2,4,8,16}` (or
   `--target-mag` against source magnification).
2. `downscale.MaterializeReducedL0` → reduced-L0 RGB raster.
3. `derivedsource.FromReducedL0(...)` — `nLevels` raster levels (reduced L0 +
   box-halved lowers), `tileSize = 256`, `quality` from `--quality`,
   factor-scaled metadata, associated passthrough.
4. `emitDICOM(..., Options{Associated: !noAssociated, L0ImageType: {DERIVED,PRIMARY,VOLUME,RESAMPLED}}, outDir)`.

### ② `crop <dicom>` default (re-encode)

`cropToDICOM` (re-encode branch):

1. `source.Open`; `validateCropBounds`.
2. `MaterializeCroppedL0` → cropped-L0 raster (exact extent).
3. `derivedsource.FromReducedL0(...)` with the cropped raster (raster levels:
   cropped L0 + halved lowers).
4. `emitDICOM(..., Options{L0ImageType: {DERIVED,PRIMARY,VOLUME,NONE}}, outDir)`.

### ③ `crop --lossless <dicom>`

`cropToDICOM` (lossless branch):

1. `snapRectToTiles` → tile-aligned rect; derive tile offset `(offX, offY)` and
   output grid `gridW×gridH`.
2. L0 = passthrough level over the source L0 (verbatim frames; `Compression()`
   = source's → `.50` for JPEG, `.90/.91` for JP2K), reporting the snapped
   dims/grid and mapping output tile `(x,y) → (x+offX, y+offY)`.
3. Lower levels = raster levels — decode the snapped region once, box-halve,
   JPEG-baseline encode.
4. `derivedsource.WithLosslessL0(...)`; `emitDICOM(..., Options{L0ImageType: {DERIVED,PRIMARY,VOLUME,NONE}})`.

**Ordering invariant (verify in plan, do not change):** `encapsulatePixelData`
iterates tiles row-major (DICOM `TILED_FULL` frame order); the passthrough
mapping preserves it because `TileInto` is pulled in `(ty, tx)` order.

## CLI surface, output layout & target detection

**DICOM output is always a directory** of `level-<n>.dcm`, as the shipped
`convert --to dicom` pyramid already does. For a DICOM target, `downsample`/
`crop` treat `-o` as the pyramid **directory** name (single-file output is for
the TIFF family); a `.dcm`-looking `-o` is still the directory name.

| Command | Detection | Routes to |
|---|---|---|
| `convert --to dicom --factor N` | `--to dicom` + `--factor`/`--target-mag` set | new `dicom` case in `dispatchDownsampleByTarget` → `downsampleToDICOM` |
| `downsample --factor N <dicom>` | `downsampleTargetForFormat("dicom") → ("dicom", true)` (new mapping) | same `downsampleToDICOM` |
| `crop <dicom>` (± `--lossless`) | source format is `dicom` | new `cropToDICOM` (re-encode + lossless branches) |

Decisions baked in:

- `--factor` with `--to dicom` always emits the **full reduced pyramid**;
  `--factor` + `--level` is rejected as contradictory (`--level` stays the
  single-instance, factor-free escape hatch).
- Re-encoded levels use `tileSize = 256` (matches the cogwsi/streamwriter
  downsample paths); a lossless passthrough L0 keeps the **source tile size**
  verbatim. Mixed tile sizes across instances are legal in DICOM.
- `--quality` feeds the JPEG encoder (default 90, as the other targets);
  `--no-associated` and `--force` behave as on the existing DICOM/TIFF paths.

## Error handling

- Source open / unsupported format, factor validation `{2,4,8,16}`,
  `--target-mag` resolution, `validateCropBounds`, and degenerate output dims
  (factor-too-large → 0×0) reuse the existing helpers and messages.
- JPEG-encode errors (raster level) and source-frame read errors (passthrough
  level) propagate.
- **Lossless guard:** the passthrough path requires source frames in a
  DICOM-copyable codec (JPEG / JPEG 2000). A DICOM source's L0 always satisfies
  this, but we still check and fail loud
  (`--lossless into DICOM needs JPEG or JPEG 2000 source frames; got <comp>`)
  rather than emit an invalid transfer syntax.
- **Atomicity:** `emitDICOM` writes into a temp sibling dir and renames into
  place; any failure `RemoveAll`s the temp dir, so a failed run leaves no
  partial pyramid. The C1 fix (skipped associated leaves no stray file) is
  already in `WritePyramid`.

## Metadata / provenance

- **ImageType** — L0 via `Options.L0ImageType`: `RESAMPLED` for downsample/
  `--factor`, `NONE` for crop (spatial subset, not resampled); lower levels keep
  the writer's existing `DERIVED/.../RESAMPLED`. No level is stamped `ORIGINAL`.
- **Scaled metadata:** downsample/`--factor` → `MPP × factor`, `mag ÷ factor`;
  **crop preserves** L0 MPP/mag (re-encode at the same resolution). The writer's
  `levelSpatial` derives PixelSpacing/ImagedVolume per-level from
  `Levels()[0].Size()` + base MPP, so it stays correct given the adapter's
  `Metadata()`.
- **Associated images** are carried through verbatim (the writer handles
  tile-copyable → encapsulated vs. decode → native RGB). Crop-thumbnail
  staleness is the documented deferral above.

## Codec decisions

- Re-encoded DICOM frames are **JPEG-baseline** — the only DICOM-valid encoder
  in the tree. This is forced, not chosen: there is no JP2K/HTJ2K encoder.
- A **JP2K source**, when re-encoded (any downsample / non-lossless crop), is
  **downgraded to lossy JPEG-baseline**. A *lossless* crop preserves L0 verbatim
  (stays `.90/.91` via the passthrough level) but rebuilds lower levels as
  JPEG-baseline → a **mixed-codec pyramid** across instances (legal in DICOM;
  each instance carries its own TransferSyntax).
- The encoder is a single seam in `rasterLevel`; an HTJ2K encoder (survey B4)
  drops in later without touching the adapter or writer.

## Testing

### Unit — `internal/derivedsource`

- `rasterLevel.TileInto` → valid JPEG decodable back to the correct tile
  sub-rect (dims + pixel spot-check); right/bottom edge padding handled.
- `passthroughLevel.TileInto` → **byte-identical** to the source's `TileInto`
  at `(x+offX, y+offY)` — the core lossless guarantee, at the unit level.
- `derived` satisfies `source.Source`: level count, per-kind `Compression()`,
  per-level `Size`/`Grid`, scaled `Metadata()`, associated passthrough.
- Box-halving helper keeps its test when it moves to `internal/downscale`.

### Integration — `tests/integration` (`//go:build integration`, fixture-gated)

- `convert --to dicom --factor 2` from **SVS** (CMU-1-Small-Region, *in CI*) →
  pyramid dir, reduced `TotalPixelMatrix` dims, `ImageType` DERIVED/RESAMPLED,
  **dciodvfy 0 errors** per instance. (Real CI coverage now that D1 runs the
  integration suite.)
- `convert --to dicom --factor 2` from a **DICOM** source, and
  `downsample --factor 2 <dicom>` → same assertions (local-only until D2 lands
  the DICOM CI fixture).
- `crop <dicom>` (re-encode) → crop-extent `TotalPixelMatrix`, dciodvfy clean.
- `crop --lossless <dicom>` → **oracle:** read back L0 frame `(0,0)` and assert
  byte-equality with the source frame at `(offX, offY)`; lower levels present;
  dciodvfy clean. JP2K variant (3DHISTECH-JP2K) confirms `.90/.91` passthrough
  L0 + JPEG lowers.

**Conformance gate:** the controller runs `dciodvfy` on emitted instances (the
gate P1/P2 used). **Determinism:** frame order is row-major (`TILED_FULL`),
JPEG encode is deterministic; where file-hash isn't stable, use the
`hash --mode pixel` oracle.

## Component summary (isolation & boundaries)

| Unit | Responsibility | Depends on |
|---|---|---|
| `internal/derivedsource` | present a derived pyramid as `source.Source` (raster + passthrough levels) | `internal/source`, `internal/codec/jpeg`, `internal/downscale` |
| `internal/downscale` (extended) | own box-halving + tile extraction (shared) | — |
| `dicomwriter.Options.L0ImageType` | override L0 ImageType for transformed pyramids (RESAMPLED vs NONE) | — |
| `emitDICOM` (cmd/wsitools) | temp-dir → WritePyramid → atomic-rename | `dicomwriter` |
| `downsampleToDICOM` / `cropToDICOM` | build the derived source per operation, call `emitDICOM` | `derivedsource`, `downscale`, `emitDICOM` |
