# SP2 — streaming retile engine + sinks — design

**Date:** 2026-06-18
**Status:** Approved design, ready for implementation plan.
**Parent:** `docs/superpowers/specs/2026-06-18-retiling-engine-design.md` (SP2 of SP1/SP2/SP3).

## Goal

Build the `internal/retile` streaming engine (generalizing the DZI descent) and a
`TileSink` interface with per-container sinks, then **route every decode-path
`convert` internal through it** — stitched-source convert, downsample, transcode,
lossy crop — replacing the per-tile re-encode paths and the raster L0 front-ends.
Verbatim tile-copy (no decode) stays separate. The **CLI convergence** (formal
aliases, `validate()` capability table, `--rect` on `convert`, deleting dead
raster code) is **SP3** — SP2 swaps the engine in *under* the existing commands.

**Standalone value:** fixes BIF-source convert (BIF → cog-wsi/svs/tiff/ome-tiff
now *works*, retiring the hard-error guard) and the L0-in-RAM problem (survey C5 —
`--factor` becomes streaming).

## Relationship to SP1 and the overarching spec

Architecture is fixed by the parent spec: two paths (verbatim | streaming
descent), opentile owns READ (`ScaledStrips`), encode stays in wsitools
(`internal/codec`). **SP1 (opentile-go#71) LANDED — v0.48.0 ships
`Level.Overlapping`**, now exposed on wsitools' `source.Level` as `Overlapping()
bool` and consumed by the stitch guard (the heuristic is retired). SP2 uses
`lvl.Overlapping()` as the path-selection predicate.

## The engine — `internal/retile`

```go
// Run executes one streaming retile pass: ScaledStrips → level-builder chain
// (downsample in-pipeline) → encode pool → sink. One L0 decode, bounded memory.
func Run(ctx context.Context, spec Spec) error

type Spec struct {
    Slide     *opentile.Slide
    SrcRegion opentile.Region  // L0-coord source rect (full slide, or a crop)
    OutL0     opentile.Size    // output L0 dims (= SrcRegion.Size/factor); == SrcRegion.Size for no downsample
    Levels    []LevelSpec      // output pyramid levels (L0 first), precomputed by ComputeLevels
    Kernel    resample.Kernel  // L0 resample kernel (Box default; Lanczos3; Nearest at identity scale)
    Encoder   codec.Encoder    // tile encoder (internal/codec)
    Sink      TileSink
    Workers   int
}

type LevelSpec struct {
    Index               int
    Width, Height       int    // level pixel dims
    Cols, Rows          int    // tile grid
    TileW, TileH        int
    Overlap             int    // 0 for TIFF-family/cog-wsi; 1 for DZI
}

// TileSink receives encoded output tiles. The engine emits tiles for multiple
// levels INTERLEAVED (as bands flow top-to-bottom, each band emits L0 tiles and
// feeds the downsample chain that emits coarser-level tiles). Each level's tiles
// arrive in the sink's required order via the writer's per-level reorder buffer,
// so interleaving across levels is fine.
type TileSink interface {
    WriteTile(level, col, row int, encoded []byte) error
}

// ComputeLevels derives the output pyramid from OutL0 + tiling + ratio/count.
// Shared by the engine and the convert drivers (which AddLevel on the writer).
func ComputeLevels(outL0 opentile.Size, tileW, tileH, overlap, levelRatio, levelCount int) []LevelSpec
```

**The descent core generalizes `convert_dzi_descent.go`'s `levelBuilder`:** the
DZI-specific bits become parameters — the level math (`dzi.MaxLevel/LevelDims/
GridDims`) → `Spec.Levels`/`ComputeLevels`; the hardcoded `jpeg.New` → `Spec.
Encoder`; `dziTileSink` → `Spec.Sink`; the kernel → `Spec.Kernel`. The pipeline
shape — `ScaledStrips → linked levelBuilders → encoder pool → sink drainer` — is
reused intact (including its proven concurrency: a worker pool on an `encodeJobs`
channel, a serialized `sinkDrainer`). `Overlap` flows into `assembleTile` (which
already handles DZI's 1px overlap; 0 is the no-overlap case).

**What is reused vs rewritten:** the strip-feed loop, the rolling-band buffer
(`feed`/`acceptDownsampled`/`flush`), `boxDownsample2x`, `assembleTile`, the
encoder/sink worker wiring — **reused** (moved into `internal/retile`, made
parameter-driven). New: `Spec`/`LevelSpec`/`TileSink`/`ComputeLevels`, the
non-octave/arbitrary-tile-size level math, and pluggable encoder/kernel/overlap.

## Sinks (push model)

`dzi`/`cogwsi`/`streamwriter` writers are push (`AddLevel`+`WriteTile`/
`WriteTileAtIndex`); a thin sink routes `WriteTile(level,…)` to the right level
handle. The **convert driver owns** the writer setup — it `AddLevel`s every level
(from `ComputeLevels`), emits associated images + metadata via the *existing*
per-format logic, wraps the level handles in a sink, calls `retile.Run`, then
closes. **The engine produces only pyramid tiles**; associated images
(label/macro/thumbnail/overview), MPP/mag/ICC/ImageDescription/OME-XML, the SVS
thumbnail-at-IFD-1 placement, and classification tags stay where they are today
(in the writer/driver). This is the key boundary that keeps the per-format
fidelity work *reused, not rewritten*.

| Sink | Writer | Notes |
|---|---|---|
| `dziSink` / `sziSink` | `dzi.Writer` / `szi.Writer` | refactor the existing `dziTileSink`; associated already emitted as PNG sidecars |
| `cogwsiSink` | `cogwsiwriter` (`AddLevel`→`LevelHandle`) | driver keeps the WSIImageType/associated/ICC emission |
| `streamwriterSink` | `streamwriter` (svs/tiff/ome-tiff) | driver keeps `emitSVSThumbnailAtL0`, `writeAssociatedImages`, Aperio/OME L0 desc |

**Deferred: the BIF *sink*** (engine→BIF). `bifwriter.WritePyramid` is *pull*
(`PyramidLevel{Src TileSource}`), so a BIF sink needs a bifwriter push refactor.
Not required for the BIF-*source* fix (source is read via `ScaledStrips`; targets
are the push writers above), so it slots to a later SP. Until then `--to bif` from
a non-overlapping source keeps its current path; from an overlapping source the
guard stands.

## Routing (the SP2 convergence — engine swapped in under existing commands)

The overlap predicate + the spec axes select the path. For each decode-path
target, the driver builds a `retile.Spec` and calls `Run`; the per-format
associated/metadata emission is unchanged.

| Path | `SrcRegion` | `OutL0` | `Levels` | replaces |
|---|---|---|---|---|
| **stitched-source** (BIF→tiled) | full | `Size.Size` | match-source ratios | the guard / broken per-tile copy |
| **downsample** `--factor N` | full | `Size/N` | match-source ratios | `runConvertFactor` raster L0 (C5) |
| **transcode** (codec, same geom) | full | `Size.Size` | match-source ratios | `transcodePyramid` per-tile re-encode |
| **lossy crop** `--rect` | rect | `rect.Size` | derived | crop re-encode raster |

**Match-source default:** for stitched/downsample/transcode, `ComputeLevels` uses
the **source's** level ratios/count (only pixels change, never pyramid shape).
`--tile-size`/`--level-ratio`/`--levels` (SP3 CLI) override.

**Verbatim tile-copy stays separate** (no decode): same codec, same geometry,
**non-overlapping** source (heuristic), carrying target. The verbatim path is
unchanged in SP2; only its *gate* consults the overlap predicate.

## Implementation milestones (sequenced; each independently verifiable)

1. **Engine + `TileSink` + DZI/SZI refactor** — move the descent into
   `internal/retile`, parameterize it, and re-point `runConvertDZI`/`SZI` at it.
   **Zero behavior change**; gated by **pixel-identical DZI/SZI parity** vs the
   current output (`hash --mode pixel` + byte-compare the manifest/tiles).
2. **cogwsi + streamwriter sinks + stitched-source routing** — BIF→cog-wsi/svs/
   tiff/ome-tiff now produces a correct stitched pyramid; retire the guard for
   those targets. Verified against the BIF fixtures (read-back via opentile,
   associated preserved, dims = stitched).
3. **downsample → engine** — `--factor`/`--target-mag` for the TIFF-family +
   cog-wsi route through `Run`; `runConvertFactor`'s raster L0 path is bypassed
   (deletion is SP3). Verified: output pixel-equivalent to today; **bounded
   memory** on a large slide (C5).
4. **transcode → engine** — same-geometry re-encode routes through `Run` with
   match-source levels. Verified: level shape preserved, only codec changes.
5. **lossy crop → engine** — `crop` (non-`--lossless`) routes through
   `Run(SrcRegion=rect)`. Lossless crop stays verbatim. Verified vs today.

Each milestone is its own commit(s) behind its own tests; a regression in one
doesn't block the others.

## Error handling

`retile.Run` propagates the first error from any stage (strip read / encode /
sink) and cancels the context (the descent already does this). Drivers keep
their atomic output (temp→rename / `.tmp` for BIF-family; dir-atomic for DICOM —
unaffected). Overlap-guard message stays for any path not yet routed (e.g. BIF
sink).

## Testing

- **DZI/SZI parity** (milestone 1) — pixel-identical to current output; the
  strongest safety net (the engine must be a faithful generalization).
- **Stitched-source correctness** (m2) — BIF→cog-wsi/svs read back via opentile
  with stitched dims + associated images intact; the previously-crashing/guarded
  path now succeeds.
- **Downsample equivalence + memory** (m3) — pixel-equivalent to the raster path;
  a large-slide `--factor` stays bounded (no OOM) — the C5 guard.
- **Transcode shape** (m4) — same level count/ratios as source; codec changed.
- **Crop equivalence** (m5) — lossy crop pixel-equivalent to today; lossless crop
  still byte-identical (unchanged path).
- **Engine unit tests** — `ComputeLevels` for octave/non-octave/arbitrary tile
  size + overlap; the band/downsample chain on a synthetic source.
- Existing convert/associated/format suites stay green throughout (the driver
  reorg must preserve associated + metadata emission).

## Component summary

| Unit | Responsibility | Depends on |
|---|---|---|
| `internal/retile.Run` + descent | streaming `ScaledStrips`→bands→downsample chain→encode pool→sink | opentile `ScaledStrips`, `internal/codec`, `resample`, `TileSink` |
| `internal/retile.ComputeLevels` | output pyramid geometry (shared by engine + drivers) | — |
| `TileSink` + per-container sinks | route encoded pyramid tiles to a writer's level handles | dzi / szi / cogwsiwriter / streamwriter |
| convert drivers (reorged) | writer setup + associated + metadata + build `Spec` + `Run` + close | retile engine, existing writers, overlap heuristic |
| overlap predicate | `source.Level.Overlapping()` (opentile-go #71 / v0.48.0) | source.Level |

## Deferred

- **BIF sink** (engine→BIF) — needs a bifwriter push refactor; later SP.
- **SP3 — CLI convergence** — crop/downsample/transcode as formal aliases,
  `validate()` capability table, `--rect` on `convert`, delete the dead raster
  code (`runConvertFactor` raster path, `crop_formats` raster).
- **Orientation** (rotate/flip), **`--preserve-levels`** — parent-spec slots.
- **DICOM via the engine** (a `dicomTileSink`) — DICOM keeps `derivedsource`.
