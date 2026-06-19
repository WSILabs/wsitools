# SP2 — Milestone 3 — downsample via the engine — design

**Date:** 2026-06-18
**Status:** Approved design, ready for implementation plan.
**Parent:** `docs/superpowers/specs/2026-06-18-retile-engine-sp2-design.md` (M3 of M1–M5).
**Builds on:** M1 (engine, merge `9ef7027`) + M2 (sinks + stitched-source routing, merge `39281de`).

## Goal

Route `convert --factor`/`--target-mag` (downsample) for the TIFF-family
(svs/tiff/ome-tiff) + cog-wsi through the `internal/retile` engine, replacing
`runConvertFactor`'s raster-L0-in-RAM build. The standalone `downsample` command
shares the same per-target bodies, so it converts too.

**Standalone value:** kills survey **C5** — today `downsampleToSVS`/etc. allocate a
full reduced-L0 raster (`make([]byte, outW*outH*3)` via
`downscale.MaterializeReducedL0`), ~18 GB on a large slide reduced by a small
factor. The streaming engine holds only rolling band buffers (hundreds of MB,
flat as slides grow).

## The core insight — factor is nearly free

The engine already does downscale: set `Spec.OutL0 = SourceL0 / factor`,
`Spec.SrcRegion =` the full source L0 rect, `Spec.Kernel = resample.Box`.
`ScaledStrips` scales L0→OutL0 in one pass (area-averaging); the engine then
box-2× derives the octave-floored pyramid below OutL0. So the factor reduces to:
`outW = srcW/factor`, `outH = srcH/factor`, plus the Box kernel. `octaveLevelSpecsFor`
and both M2 sinks work unchanged.

**Pixel-equivalence (the oracle):** this is the SAME algorithm the raster path
uses — `MaterializeReducedL0` is `ScaledStrips` L0→reduced with Box, then
`FromReducedL0` box-halves. The engine: `ScaledStrips` L0→OutL0 with Box, then
`boxDownsample2x` descent. So engine `--factor` output is **pixel-equivalent** to
today's (expect mean per-channel diff ~0–1/255; not guaranteed bit-identical
because the two box implementations may round differently), just streamed.

## Output pyramid shape (settled)

**Octave-floored** (reuse M2's `flooredLevelCount`): from the reduced L0
(`outW×outH`), emit octave levels stopping at the first whose `min(w,h) ≤ tile`.
Consistent with M2. This MAY differ in level *count* from today's raster path,
which emits exactly `len(source.Levels())` octave levels from the reduced L0 — the
octave-floored count is a complete pyramid for the reduced image, not a
source-count echo. (User-approved trade-off.)

## Routing & scope

`runConvertFactor` → `dispatchDownsampleByTarget` → per-target
`downsampleToSVS`/`downsampleToTIFF`/`downsampleToOMETIFF`/`downsampleToCOGWSI`
(`cmd/wsitools/convert_factor.go`). These bodies are **shared with the standalone
`downsample` command** (`runDownsample`), so one change covers both surfaces.

M3 swaps **only the raster-materialize + raster-pyramid-build** inside each
`downsampleToX` for the streaming engine. Everything else stays:
- factor / `--target-mag` resolution + `{2,4,8,16}` validation;
- **downsample metadata mutation** — `desc.MutateForDownsample(factor,…)` (SVS) /
  `SyntheticAperioDescription` / OME equivalent → adjusted MPP×factor, mag÷factor,
  and the L0 Aperio/OME ImageDescription;
- BigTIFF prediction; writer creation (`streamwriter.Create` / `cogwsiwriter.Create`)
  with the mutated metadata;
- the existing associated-image segregation (thumbnail between L0 and L1,
  label/macro/overview at the end).

**dicom** `--factor` stays on the `derivedsource` path (no engine sink for DICOM
yet — deferred). **The raster code** (`downscale.MaterializeReducedL0`,
`derivedsource.FromReducedL0`, `buildPyramidFromRasterCOGWSI`,
`encodeAndWriteLevelCOGWSI`, `extractTileFromRaster`) is **bypassed, not deleted**
— SP3 removes the now-dead path.

## Shared engine helper

The four targets share the build-levels + AddLevel + sink + `retile.Run(Box)` +
`finish()` boilerplate. Extract one helper in `cmd/wsitools` (working name
`runDownsampleEngine`) that takes the open `*opentile.Slide`, the source-L0 size,
the output L0 size, the tile size, the `retile.TileEncoder`, the `retile.TileSink`
(already wrapping the writer's level handles), the level specs, and `workers`; it
builds the `retile.Spec` (`SrcRegion` = full source L0, `OutL0` = reduced,
`Kernel = Box`), calls `retile.Run`, then `sink.finish()` (unconditional, per the
M2 fix), returning the first error. Each `downsampleToX` keeps its own writer +
per-level `AddLevel` shaping (svs/cog-wsi differ) + associated emission and calls
this helper for the pixel pyramid. Reuses M2's `streamwriterSink`, `cogwsiSink`,
`codecTileEncoder`, `octaveLevelSpecsFor` verbatim.

NOTE the source must be opened as a `*opentile.Slide` for `ScaledStrips`. The
current `downsampleToSVS` opens `opentile.OpenFile(input)` directly (it already
holds a `*opentile.Slide`), so the slide is in hand — no `OpenWithSlide` switch
needed; thread the existing slide into the helper.

## Fold-in: M1 engine hardening (encoderWorker error propagation)

The M2-review follow-up: `internal/retile.encoderWorker` (encode.go) silently
`return`s on an `EncodeTile` error, so a failed encode becomes a missing tile (the
level's drain then sees a permanent gap), not an error. M3 drives real
downscale+re-encode through the engine, so harden it: on encode error, record the
first error and `cancel()` the run (mirroring the strip-read error path in
`Run`). `Run` returns that error; the sink `finish()` still joins cleanly (the M2
`reorderBuffer.NextReady` close+gap termination handles the missing tiles).
Implementation: give `encoderWorker` access to a shared error sink + the cancel
func (or route through the existing `ctx` cancel + a `sync.Once` error capture in
`Run`). Unit test: a `TileEncoder` that errors on the Nth `EncodeTile` → `Run`
returns that error, no hang, within a timeout.

## Error handling

`retile.Run` first-error propagation + the M2 unconditional-`finish()` discipline
carry over to `runDownsampleEngine`. Drivers keep their atomic output
(temp→rename / spool-finalize) and `w.Abort()` on error. The encoderWorker fix
closes the silent-data-loss gap.

## Testing

- **Pixel-equivalence (primary):** for a fixture (e.g. CMU-1) at `--factor 2` and
  `4`, compare the engine output against the pre-engine raster output
  region-by-region (`region` command or `hash --mode pixel`); expect mean
  per-channel diff ~0–1/255. Run for both a TIFF-family target and cog-wsi.
- **Bounded memory (the C5 proof):** controller-run — `--factor` a large slide and
  assert peak RSS does NOT scale with L0 area (the raster path allocates
  `outW*outH*3`; the engine stays flat). Compare RSS before/after the swap on the
  same large input.
- **encoderWorker error propagation (unit):** `internal/retile` — Nth-tile encode
  error surfaces from `Run` without hanging.
- **Metadata + associated preserved:** output MPP = srcMPP×factor, mag =
  srcMag÷factor; Aperio/OME L0 description re-detects; thumbnail at IFD 1 (svs);
  label/macro present. Read back via opentile.
- **`downsample` command parity:** the standalone `downsample` (sharing the bodies)
  still produces a valid format-preserving pyramid.
- Existing convert/downsample suites stay green; full `-race`.

## Component summary

| Unit | Responsibility | Depends on |
|---|---|---|
| `downsampleToX` (reorged) | factor/metadata/writer/associated (unchanged) + build levels + call engine helper | runDownsampleEngine, existing helpers |
| `runDownsampleEngine` | build retile.Spec (Box, OutL0=L0/factor) + Run + finish | retile.Run, sinks, encoder |
| `internal/retile.encoderWorker`/`Run` | propagate encode error + cancel (hardening) | — |
| (reused from M2) `streamwriterSink`, `cogwsiSink`, `codecTileEncoder`, `octaveLevelSpecsFor` | unchanged | — |

## Deferred (post-M3)

- Delete the raster path (`MaterializeReducedL0` etc.) — SP3.
- M4 transcode → engine (+ match-source shape); M5 lossy crop → engine.
- DICOM `--factor` via the engine (stays `derivedsource`); BIF sink; SP3 CLI
  convergence (formal aliases, `validate()` capability table, `--rect`).
