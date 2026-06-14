# Format-preserving lossless `crop` (Phase 2b) — design spec

**Date:** 2026-06-13
**Status:** approved (brainstorming) — ready for implementation plan
**Builds on:** SVS `crop --lossless` (`2026-06-13-svs-crop-lossless-design.md`) and
Phase 2a format-preserving re-encode (`2026-06-13-crop-format-preserving-design.md`).

---

## Goal

Extend `crop --lossless` (byte-identical L0 tile copy) to the non-SVS TIFF family:
**generic-TIFF, OME-TIFF, and cog-wsi.** Output stays format-preserving (same
container as the source); the full-resolution L0 tiles are copied byte-identical,
lower pyramid levels are rebuilt. Drops the current `--lossless`-non-SVS guard.

**Scope: all three formats in this phase** (decision locked).

## Why this is mostly reuse

The SVS lossless emission (in `cropEmitSVS`) is already the template:
**snap rect → verbatim L0 copy → regenerate thumbnail → halve → rebuild lower
levels from L1.** The pieces generalize:

- `snapRectToTiles` is format-agnostic (uses the source tile size).
- `writeLosslessL0(w *streamwriter.Writer, …)` works for **tiff + ome-tiff**
  (both streamwriter-backed) **unchanged**.
- `halveRaster` + `buildPyramidFromRaster` (streamwriter) / `buildPyramidFromRasterCOGWSI`
  (cog-wsi) rebuild the lower levels.
- `regenCropThumbnail` / `regenCropThumbnailCOGWSI` (Phase-2a) regenerate the thumbnail.

The **only substantial new code** is `writeLosslessL0COGWSI` (verbatim L0 tile
copy into a `cogwsiwriter`), feasible because `cogwsiwriter.LevelSpec` already
carries `Compression`, `JPEGTables`, `Photometric` (so abbreviated source tiles +
shared tables re-emit faithfully).

---

## Architecture

### Front-end (`runCrop`) — generalize snap+materialize to all lossless targets

Today the front-end errors on `--lossless` for non-SVS and materializes an
**exact-extent** L0 for non-SVS re-encode. Change:

1. Remove the `lossless && target != "svs"` guard.
2. For SVS → `cropEmitSVS` (unchanged; it does its own snap+lossless).
3. For non-SVS, compute the effective rect:
   ```
   ex,ey,ew,eh := x,y,w,h
   var stx0,sty0,outTilesX,outTilesY int
   if lossless {
       ex,ey,ew,eh, stx0,sty0,outTilesX,outTilesY = snapRectToTiles(x,y,w,h, srcL0.TileSize.W, srcL0.TileSize.H, baseW, baseH)
       print snapped rect if it changed
   }
   materialize outL0 at (ex,ey,ew,eh)            // snapped raster for lossless; exact for re-encode
   nLevels := cropPyramidLevels(ew,eh, outputTileSize)
   ```
4. Dispatch to `cropToTIFF`/`cropToOMETIFF`/`cropToCOGWSI` with the **mode +
   snap coords**: add params `lossless bool, srcL0 *opentile.Level, stx0, sty0,
   outTilesX, outTilesY int` to each emitter.

> Quality default: for lossless, L0 is copied verbatim (quality irrelevant to L0);
> the rebuilt lower levels + thumbnail still use the resolved quality (90 default
> for non-SVS). No change needed.

### Per-format emitters — branch only the pyramid emission

The re-encode vs lossless difference is **only the pyramid build**; the writer
setup, metadata, and the associated loop (incl. Phase-2a in-place thumbnail
regen) are shared.

**`cropToTIFF` / `cropToOMETIFF`** (streamwriter):
```
create streamwriter (tiff / ome-tiff Options; ome-tiff OME-XML uses ew×eh = snapped dims)
if lossless:
    writeLosslessL0(w, srcL0, stx0, sty0, outTilesX, outTilesY, ew, eh)
    if nLevels > 1:
        l1,l1W,l1H := halveRaster(l0, ew, eh)
        buildPyramidFromRaster(w, l1, l1W, l1H, nLevels-1, quality, workers, nil)
else:
    buildPyramidFromRaster(w, l0, ew, eh, nLevels, quality, workers, nil)
<shared associated loop — in-place thumbnail regen + passthrough>
close
```

**`cropToCOGWSI`** (cogwsiwriter): same shape with `writeLosslessL0COGWSI` +
`buildPyramidFromRasterCOGWSI(w, l1, …, nLevels-1, quality)`.

### New: `writeLosslessL0COGWSI`

```go
func writeLosslessL0COGWSI(w *cogwsiwriter.Writer, srcL0 *opentile.Level, stx0, sty0, outTilesX, outTilesY, outW, outH int) error
```

Mirrors `writeLosslessL0` but for `cogwsiwriter`:
- `w.AddLevel(cogwsiwriter.LevelSpec{ImageWidth: outW, ImageHeight: outH,
  TileWidth/Height: srcL0.TileSize, Compression: CompressionToTIFFTag(srcL0.Compression),
  Photometric: levelPhotometric(srcL0), SamplesPerPixel: 3, BitsPerSample: {8,8,8},
  JPEGTables: levelJPEGTables(srcL0), IsL0: true})`.
- For each output tile (ox,oy) in **row-major** order (cogwsiwriter `WriteTile`
  enforces strict row-major — see `encodeAndWriteLevelCOGWSI`): read the source
  **abbreviated tile body** (`srcL0.TileBodyInto`, NOT `Tile` — see the SVS
  lossless spec on why; fresh slice per tile) and `lh.WriteTile(ox, oy, body)`.
- Reuse the existing `levelPhotometric` / `levelJPEGTables` helpers (from
  `crop_lossless.go`).

> Confirm the `cogwsiwriter` `AddLevel`→`LevelHandle`→`WriteTile`/`CloseInput`
> drain protocol against `encodeAndWriteLevelCOGWSI` (it writes serially
> row-major; no concurrent drain needed). Match it exactly.

### Reuse vs duplication (resolve in the plan)

The streamwriter lossless pyramid emission (`writeLosslessL0` + halve +
`buildPyramidFromRaster(L1)`) now appears in `cropEmitSVS` AND the new
`cropToTIFF`/`cropToOMETIFF`. Optional DRY: extract a shared
`emitLosslessPyramidStreamwriter(w, srcL0, l0, snapCoords, ew, eh, nLevels, quality, workers)`
used by all three streamwriter formats. The plan/architect decides extract-vs-
inline; if extracted from `cropEmitSVS`, the SVS byte-identity/parity tests guard it.

---

## Testing (local-only — large fixtures, not in CI)

Extend the byte-identity guarantee to the non-SVS family. Add
`TestCropLossless_FormatPreserving` (or parametrize the existing
`assertLosslessByteIdentity` helper to take an output extension + expected
format, and reuse it):

| Source | Fixture | Asserts |
|---|---|---|
| generic-TIFF | `generic-tiff/CMU-1.tiff` (240px tiles) | output L0 tiles **byte-identical** to source, format preserved, MPP/mag |
| OME-TIFF | `ome-tiff/Leica-1.ome.tiff` (512px tiles) | same |
| cog-wsi | `cog-wsi/CMU-1_cog-wsi.tiff` | same (exercises `writeLosslessL0COGWSI`) |

The byte-identity check: snapped L0 dims = tile-aligned bbox; every output L0 tile
== the corresponding source tile; `TilePrefix` equal; lower levels present;
thumbnail regenerated to crop aspect (Phase-2a).

> The existing `assertLosslessByteIdentity` opens the output as SVS implicitly via
> `Tile()`/`TilePrefix()` on `*Level` — those are format-agnostic on `*opentile.Level`,
> so the helper generalizes; it just needs the output path/ext per format.

Regression: SVS `--lossless` byte-identity + the Phase-2a re-encode matrix stay green.

---

## Out of scope

- Non-TIFF containers (NDPI has no writer; DICOM lossless crop is separate).
- Strategy C (exact-extent edge re-encode).
- Cross-format `--to`.

## Risks / open items (resolve in the plan)

1. **`cogwsiwriter` verbatim-tile protocol** — confirm `AddLevel`/`WriteTile`/
   `CloseInput` usage against `encodeAndWriteLevelCOGWSI`; verify a level with
   `JPEGTables` + a non-zero `Compression` tag round-trips (opentile re-reads the
   cog-wsi output and the tiles decode).
2. **J2K source lossless** (JPEG2000 fixtures): verbatim J2K tiles into tiff/ome-tiff/
   cog-wsi — `TilePrefix` is nil (no JPEGTables), `Compression` = 33003. Confirm
   each writer emits the right tag and opentile re-reads it. (SVS lossless already
   proved the J2K nil-prefix path; the test matrix should include a J2K case if a
   non-SVS J2K fixture exists — `cog-wsi/JP2K-33003-1_cog-wsi.tiff` does.)
3. **Emitter signature growth** — six new params per emitter; consider a small
   options struct if it reads poorly (plan decision).
4. **OME-XML dims for lossless** — `ew×eh` are the snapped dims; the OME-XML and
   thumbnail dims must use them (they already flow from `l0W/l0H = ew/eh`).
