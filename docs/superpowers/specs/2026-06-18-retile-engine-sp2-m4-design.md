# SP2 — Milestone 4 — transcode via the engine (select-octave) — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** `docs/superpowers/specs/2026-06-18-retile-engine-sp2-design.md` (M4 of M1–M5).
**Builds on:** M1 (engine), M2 (sinks + per-container shaping), M3 (engine hardening). Merges `9ef7027`/`39281de`/`00f9e2c`.

## Goal

Route **non-overlapping TIFF-family transcode** (`convert --to svs|tiff|ome-tiff
--codec X`, same geometry, re-encode only) through the `internal/retile` engine,
preserving the source's pyramid structure while decoding L0 only once.

## The core problem (and the resolution)

Transcode means "same pyramid, different codec." But the engine's box-2× descent
emits a full **octave** pyramid, while real WSI sources are **non-octave** — the
fixture survey shows Aperio/NDPI classically use 4× steps (CMU-1.svs 46000→11500→
2875 = 4×,4×; CMU-1.ndpi 4×,4×,4×; scan_617 4×,2×,2×). Octave-floored output (M2/
M3's rule) would balloon a 3-level source into a 5+-level pyramid with dims the
source never had — that is not a transcode.

**Resolution — select-octave (user-approved):** those non-octave ratios are all
powers of 2, so every source level is reachable by repeated box-halving from L0.
The engine decodes L0 once, box-2× derives the octave chain down to the deepest
source level, and **encodes/emits ONLY the octaves matching a source level**,
computing the intermediate octaves solely to feed the reduction chain. Single
decode; source structure preserved; derived levels carry one fewer lossy
generation than re-encoding the source's already-lossy reduced levels.

## Level construction (select-octave)

Identity geometry: `OutL0 = SourceL0`, `SrcRegion =` full source L0, `Kernel =
resample.Nearest` (no resampling on the top read).

For source levels `srcLevels[0..M-1]` (L0 = srcLevels[0]):
- Each source level *i*'s octave = `round(log2(srcL0.W / srcLevels[i].W))`.
- The deepest octave `D` = the octave of the smallest source level.
- Build octave LevelSpecs `0..D` (box-derived ceil-halved dims).
- An octave `k` is **emitted** iff some source level maps to it; otherwise
  `Intermediate=true` (computed for reduction, not encoded).
- Each **emitted** level carries:
  - `Width/Height` = the **box-derived** dims at that octave (≈ source, ±1px on
    odd-dim levels — accepted);
  - `TileW/TileH` = the **matching source level's tile size** (faithful transcode,
    mirroring today's `transcodeLevel` which uses `lvl.TileSize()`);
  - `Index` = its **emit position** (0,1,2…), so the sink's `handles[]` is sized
    to M and output metadata (`WSILevelIndex`/`Count`, `NewSubfileType`) is
    correct.
- **Intermediate** levels carry box-derived dims + a default tile size (e.g. 256,
  used only for internal strip batching); `Index` is unused.

A new helper `transcodeOctaveLevels(srcLevels) ([]retile.LevelSpec, ok bool)`
builds this list and returns `ok=false` if any source level does not map to a
clean power-of-2 octave (→ fallback, §Fallback).

## Engine enhancement: `LevelSpec.Intermediate`

Add `Intermediate bool` to `retile.LevelSpec`. **Zero-value `false` = emit**, so
M1/M2/M3 (and `ComputeLevels`/`octaveLevelSpecsFor`, which never set it) are
unchanged. In the descent, gate ONLY the `emitRow` calls (in both `feed` and
`flush`) on `!Intermediate`; the buffer rotation + `boxDownsample2x(strip) →
child.acceptDownsampled` and the `child.flush()` cascade must ALWAYS run so deeper
emitted levels are still produced. Sketch:
```go
// feed():        if lb.cur != nil && !lb.spec.Intermediate { lb.emitRow(lb.rowIndex); lb.rowIndex++ }
//                ... boxDownsample2x(strip) → child ALWAYS runs ...
// flush() tail:  if lb.cur != nil && lb.rowIndex < lb.rows && !lb.spec.Intermediate { lb.emitRow(lb.rowIndex); lb.rowIndex++ }
//                ... child.flush() ALWAYS runs ...
```
So an intermediate octave costs a cheap in-RAM box reduction, never the (cgo)
encode, and never enqueues an encodeJob on any path. This is the single, reusable
engine change.

NOTE for the implementer: an intermediate level whose `Cols/Rows` are 0 or whose
`Index` collides with an emitted level must be harmless — since `emitRow` is the
only consumer of `Index`/`Cols`/`Rows` and it never runs for intermediates, set
intermediate `Index` to anything (e.g. -1) and leave `Cols/Rows` unused.

## Reuse + fallback

`convertTranscodeTIFF` reuses M2's `convertStitchedTIFF` machinery wholesale — one
shared `codecTileEncoder` (from `fac`/`knobs`), `streamwriterSink`, the per-
container `LevelSpec` shaping (`Compression`/`Photometric`/`SamplesPerPixel`/
`BitsPerSample`/`JPEGTables`/`NewSubfileType(emitIndex)`/`WSIImageType=Pyramid`,
L0 `ImageDescription` ExtraTags for svs/ome-tiff via the **preserved** source
`srcImageDesc`), the L0→thumbnail→L1 AddLevel ordering (`emitSVSThumbnailAtL0`),
and `writeAssociatedImages`. The **only** difference from `convertStitchedTIFF` is
the level list (`transcodeOctaveLevels` instead of `octaveLevelSpecsFor`) and the
identity `OutL0`/Nearest kernel. Factor out the shared body if it reduces
duplication cleanly; otherwise a focused new function is fine.

**Fallback:** `runConvertTIFFReencode`'s non-overlapping branch calls
`transcodeOctaveLevels(src.Levels())`; if `ok`, run `convertTranscodeTIFF`; else
fall back to the existing `transcodePyramid` (per-level, exact dims) — safe for
any exotic non-power-of-2 source. `transcodePyramid`/`transcodeLevel` are **kept**
(SP3 may delete if the fallback proves dead).

## Routing summary (`runConvertTIFFReencode`)

```
overlapping(src)                       → convertStitchedTIFF      (M2, octave-floored)
!overlapping, transcodeOctaveLevels ok → convertTranscodeTIFF     (M4, select-octave)   ← NEW
!overlapping, not ok                   → transcodePyramid          (fallback, per-level)
```
`--factor` is handled earlier (M3) and never reaches here.

## Error handling

The M3 engine hardening (encode-error propagation + real-error-not-cancel) and the
M2 unconditional-`finish()` discipline carry over (M4 reuses those sinks/driver
shape). Atomic temp→rename unchanged.

## Testing

- **Structure preservation (primary):** transcode an SVS (jpeg→jpeg2000 AND
  jpeg→jpeg) and an OME-TIFF; read back via opentile; assert output level **count
  == source count** and **ratios == source ratios** (dims equal source, or ±1px on
  odd levels); MPP/mag/ICC + L0 ImageDescription preserved; SVS thumbnail at IFD 1;
  associated images intact; validate clean.
- **Codec actually changed:** output L0 compression == the requested `--codec`
  (e.g. jpeg2000), not the source's.
- **Pixel-equivalence:** L0 region of the engine transcode vs the source ≈ codec
  round-trip noise (mean small).
- **`Intermediate` engine unit test (internal/retile):** a 3-octave descent with
  the middle level `Intermediate=true` → the sink receives tiles for exactly the
  2 emitted levels (finest + coarsest), with correct dims; the middle level's
  pixels still feed the coarsest (verify the coarsest content is the box-reduction
  of the finest, i.e. the chain ran).
- **`transcodeOctaveLevels` unit test:** power-of-2 source (4×,4×) → ok, octaves
  {0,2,4}, emit at those, intermediates {1,3} marked; a non-power-of-2 source
  (e.g. ratio 3) → `ok=false`.
- **Fallback path:** confirm a non-power-of-2 source still transcodes (via
  `transcodePyramid`) and validates.
- Existing transcode/convert/associated suites green; full `-race`.

## Component summary

| Unit | Responsibility | Depends on |
|---|---|---|
| `transcodeOctaveLevels` | source levels → select-octave LevelSpec list (emit at source octaves, Intermediate elsewhere) + ok flag | retile.LevelSpec |
| `convertTranscodeTIFF` | identity OutL0 + select-octave levels + per-container shaping + sink + Run (reuses M2 machinery) | retile.Run, streamwriterSink, codecTileEncoder, existing helpers |
| `retile.LevelSpec.Intermediate` + descent gate | compute-but-don't-encode intermediate octaves | — |
| routing (runConvertTIFFReencode) | overlapping→M2, ok→M4, else→transcodePyramid | sourceIsOverlapping, transcodeOctaveLevels |

## Deferred (post-M4)

- M5 lossy crop → engine.
- SP3: delete `transcodePyramid` if the fallback is provably dead; CLI convergence
  (aliases, `validate()` capability table, `--rect`); delete the bypassed raster
  code; BIF sink; DICOM-via-engine.
- cog-wsi transcode (no re-encode path exists today; out of scope).
- Stitched-source transcode stays on M2's octave-floored path (accepted asymmetry —
  stitched sources are recomposited, so source structure isn't meaningfully
  preservable).
