# SP2 — Milestone 2 — sinks + stitched-source routing — design

**Date:** 2026-06-18
**Status:** Approved design, ready for implementation plan.
**Parent:** `docs/superpowers/specs/2026-06-18-retile-engine-sp2-design.md` (M2 of M1–M5).
**Builds on:** M1 (merge `9ef7027`) — `internal/retile` engine (`Run`/`Spec`/`LevelSpec`/`TileEncoder`/`TileSink`/`ComputeLevels`), proven byte-identical on DZI/SZI.

## Goal

Drive two non-DZI writers through the M1 engine and route **overlapping/stitched
sources** (Ventana BIF) to it, so `convert --to cog-wsi|svs|tiff|ome-tiff` from a
stitched source produces a correct re-tiled pyramid — retiring the hard-error
overlap guard for those four targets.

**Standalone value:** fixes the BIF-source convert bug (currently guarded with a
hard error). The motivating defect: opentile v0.46+ reports a stitched
`Level.Size` (compacted hull) that disagrees with the raw overlapping tile
`Grid`, so the per-tile copy/re-encode convert paths crash / panic / mis-place.
The engine reads via `ScaledStrips`, which composites the stitched image
correctly.

## Scope boundary (what M2 is and is NOT)

**In M2:** the `streamwriterSink` + `cogwsiSink`; the `Overlapping()`→engine
routing for `{cog-wsi, svs, tiff, ome-tiff}`; retiring the guard for those four.

**NOT in M2 (deferred, own milestones):**
- Non-overlapping `transcode`/`downsample`/`crop` through the engine — M3/M4/M5.
  M2 adds the engine branch *alongside* the existing per-tile path, gated purely
  on `Overlapping()`. Non-overlapping sources keep today's verbatim/
  `transcodePyramid` path untouched.
- **Match-source pyramid shape** — M4 (transcode), where it matters (non-overlapping,
  same-pyramid-different-codec). M2's stitched output is a re-derived octave
  pyramid (see Output pyramid).
- **BIF sink** (engine→BIF) — needs a `bifwriter` push refactor; `--to bif` from
  an overlapping source keeps the guard.
- **DICOM** — stays on the `derivedsource` path; `--to dicom` from an overlapping
  source keeps the guard.

## Two axes (settled in design review)

The output is defined along two independent axes:

1. **Pyramid shape** = how many output levels and their dims. **M2 = octave,
   floored:** each level is half the previous (L0, L0/2, L0/4, …), stopping after
   the first level whose smaller dimension is ≤ the output tile size (a normal WSI
   pyramid bottom, not a 1×1 tail). The source's own (often non-power-of-2) level
   ratios are **not** preserved — the stitched source is being recomposited from
   L0 anyway, so its level structure isn't sacred.

2. **Resampling** = how the downsampled pixels are computed. **The derived
   (coarser) levels are box-averaged on uncompressed RGB inside the engine**
   (`boxDownsample2x`), NOT via codec-domain scaled decode. Rationale: the engine
   decodes L0 **once** (via `ScaledStrips`) and reduces the RGB it already holds —
   cheap, and box 2× reduction is quality-equivalent to libvips (audited). Asking
   the codec to scale-decode each level instead means re-decoding L0 per level —
   the per-level-`ScaledStrips` approach that was **7× slower** (the v0.17
   failure). The only codec-domain scaling that could apply is opentile's
   `ScaledStrips` top read when output L0 ≠ source L0; **for M2 that read is
   identity** (stitched hull → same-size output), so it only stitches.

## Routing

`runConvert` (cmd/wsitools/convert.go) already calls `guardStitchedSource(input,
cvTo)` before the dispatch switch. M2 changes that gate from "error on overlap" to
"route on overlap":

- Detect overlap once via `source.Level.Overlapping()` on any level (the existing
  guard predicate).
- **Overlapping + target ∈ {cog-wsi, svs, tiff, ome-tiff}** → the **engine path**
  (this milestone).
- **Overlapping + target ∈ {dicom, bif, dzi, szi}** → unchanged: dzi/szi already
  composite via their own descent (exempt); dicom/bif keep the guard error.
- **Non-overlapping** → unchanged: the existing per-tile driver path.

The engine path is a new function (working name `convertStitchedSource`) invoked
from `runConvertTIFF`/`runConvertCOGWSI` when overlap is detected (each driver
branches: overlapping → engine path; else existing per-tile path). It owns: open
slide, build the octave `LevelSpec` list, `AddLevel` each level on the writer with
per-container shaping, wrap the level handles in the matching sink (streamwriter
or cogwsi), build the `TileEncoder`, call `retile.Run`, then emit associated
images + metadata via the **existing** per-format helpers, then close atomically.
The per-container differences (which writer, which sink, which metadata/associated
helpers) are small enough to keep as two thin driver branches that share the
octave-level + encoder + `retile.Run` core via a helper.

## Output pyramid construction

- `OutL0 = lvl0.Size` (the stitched hull). `SrcRegion = {Origin:{0,0}, Size:OutL0}`.
  Identity scale → `Kernel = resample.Nearest` (ScaledStrips composites the
  overlapping tiles; no resampling on the top read).
- **Tile size** = source L0 tile size (`lvl0.TileSize`); **overlap = 0**.
- **Levels** = `retile.ComputeLevels(OutL0, tileW, tileH, 0 /*overlap*/, 2
  /*ratio*/, flooredLevelCount(OutL0, tileSize))`.
- `flooredLevelCount(size, tile)` = the number of octave levels from native down
  to (and including) the first level whose `min(w,h) ≤ tile`; always ≥ 1. New
  small helper (cmd/wsitools), unit-tested. (Distinct from M1's `dziOctaveCount`,
  which floors at 1×1.)

## The two sinks

Both are `retile.TileSink` implementations in `cmd/wsitools` (driver glue, like
`dziWriterSink`). The engine emits tiles for all levels **interleaved** and, within
a level, **out of grid order** (the encoder pool finishes out of order). Each sink
satisfies its writer's ordering contract.

### `streamwriterSink` (svs / tiff / ome-tiff)
`streamwriter.LevelHandle` already has a reorder buffer and the
`CloseInput`/`NextReady`/`WriteTileAtIndex` drain protocol. The sink:
- Holds `[]*streamwriter.LevelHandle` (one per octave level, from `AddLevel`).
- `WriteTile(level,col,row,body)` → `handles[level].WriteTile(uint32(col),
  uint32(row), body)`.
- Starts **one ordered-drain goroutine per level** at construction (the exact
  pattern in `transcodeLevel`: loop `NextReady`→`WriteTileAtIndex`, `Abort` on
  error). On engine completion: `CloseInput` every handle, wait all drains,
  surface the first drain error.

No new ordering machinery — reuses the writer's reorder buffer + the proven drain.

### `cogwsiSink` (cog-wsi)
`cogwsiwriter.LevelHandle.WriteTile(tx,ty)` requires **strict row-major from
(0,0)**, no reorder buffer. cogwsiwriter stays **unchanged**; the sink adds a
**per-level row-major reorder buffer**:
- Per level: a `nextIdx` (row-major emission index) + a `map[idx][]byte` holding
  out-of-order tiles.
- `WriteTile(level,col,row,body)`: compute `idx = row*cols + col`; if `idx ==
  nextIdx`, write through (`handle.WriteTile`) and drain any contiguous buffered
  successors, advancing `nextIdx`; else stash in the holding map.
- Memory is bounded: the engine emits each level roughly row-major, so the holding
  set stays ~O(workers). Concurrency: the engine's `sinkDrainer` is single-
  goroutine, so the sink needs no internal locking for the reorder state (one
  caller). Document that assumption.
- A small, focused type with its own unit test (out-of-order in → strict
  row-major `handle.WriteTile` out).

## Encoder + per-container shaping (reuse, not rewrite)

- **TIFF-family `TileEncoder`**: a thin adapter wrapping `codec.Encoder` (built via
  the `--codec`/`--quality` factory, with `LevelGeometry{TileWidth,TileHeight}` =
  the output tile size). `EncodeTile(rgb,w,h)` → `codec.Encoder.EncodeTile(rgb,w,
  h,nil)` (the **abbreviated** body). The level's `JPEGTables` = `enc.LevelHeader()`
  (TIFF tag 347). One encoder shared across all levels + the engine's worker pool
  (concurrency-safe, as `transcodeLevel`'s pipeline already shares one). Lives in
  cmd/wsitools (working name `codecTileEncoder`).
- **Per-container `LevelSpec` shaping stays in the driver**, mirroring
  `transcodeLevel`: `Compression`/`Photometric`/`BitsPerSample`/`SamplesPerPixel`,
  `JPEGTables`, `NewSubfileType` (`newSubfileTypeForLevel`), `WSIImageType=Pyramid`
  + level index/count, and the L0 `ImageDescription` ExtraTag for svs/ome-tiff
  (`buildL0ImageDescriptionTag`). `pyramidLevelCount` = the octave level count.
- **Metadata + associated images reuse existing helpers unchanged**: MPP/mag/ICC
  via the writer's `addL0Metadata`; associated (label/overview/macro/thumbnail)
  via `writeAssociatedImages`; SVS thumbnail-at-IFD-1 via `emitSVSThumbnailAtL0`
  (only when container == svs). The engine produces **only pyramid tiles**;
  everything else is the driver's existing per-format logic. This is the boundary
  that keeps the per-format fidelity work reused, not rewritten.

## Error handling

`retile.Run` propagates the first stage error and cancels the context (M1). Each
sink `Abort`s its drain(s) on a write error and surfaces the first error after the
run. Drivers keep their atomic output (temp→rename for streamwriter family;
cogwsiwriter's spool-and-finalize). On any engine error the partial output is
discarded (no rename / finalize). The overlap-guard message stays for the targets
M2 does not route (dicom/bif).

## Testing

- **Stitched-source correctness (primary):** for each BIF fixture, `convert --to
  {cog-wsi,svs,tiff,ome-tiff}`, then **read back via opentile** and assert: L0 dims
  = stitched hull; level count = `flooredLevelCount`; each level's tiles decode;
  associated images present and decodable; (svs) thumbnail at IFD 1. The
  previously-guarded path now succeeds.
- **`cogwsiSink` reorder unit test:** feed `WriteTile` out of grid order across ≥2
  interleaved levels; assert the wrapped writer receives strictly row-major
  `(0,0),(1,0),…` per level, contents intact.
- **`flooredLevelCount` unit test:** octave/odd-dim/min-dim-≤-tile cases; always
  ≥ 1.
- **`streamwriterSink` integration:** covered by the stitched-source read-back;
  optionally a focused test that the per-level drain emits all tiles.
- **Guard regression:** non-routed overlapping targets (dicom/bif) still error;
  non-overlapping sources still take the existing path (existing convert/associated
  suites stay green).
- Full `-race` (cmd/wsitools heavy — `-timeout 30m`).

## Component summary

| Unit | Responsibility | Depends on |
|---|---|---|
| routing (convert.go) | `Overlapping()` → engine path for the 4 targets; guard for dicom/bif | source.Level.Overlapping |
| `convertStitched…` driver | slide open + octave LevelSpec + per-container shaping + sink + encoder + `retile.Run` + associated/metadata + close | retile.Run, streamwriter/cogwsiwriter, existing helpers |
| `streamwriterSink` | route tiles to per-level handles; run per-level ordered drains | streamwriter.LevelHandle |
| `cogwsiSink` | route tiles; per-level row-major reorder buffer | cogwsiwriter.LevelHandle |
| `codecTileEncoder` | codec.Encoder → abbreviated `TileEncoder.EncodeTile` | internal/codec |
| `flooredLevelCount` | octave depth flooring at tile size | — |

## Deferred (post-M2)

- M3 downsample → engine (C5 bounded memory); M4 transcode → engine (+ match-source
  shape); M5 lossy crop → engine.
- BIF sink (bifwriter push refactor); DICOM via engine; SP3 CLI convergence.
