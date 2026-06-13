# `wsitools crop --lossless` — design spec

**Date:** 2026-06-13
**Status:** approved (brainstorming) — ready for implementation plan
**Builds on:** the shipped `wsitools crop` (Strategy A, full re-encode) —
`docs/superpowers/specs/2026-06-13-svs-crop-design.md`
**Background analysis:** `docs/aperio-svs-crop-analysis.md` (Strategy B in the
option table)

---

## Goal

Add a `--lossless` mode to `wsitools crop` that produces a cropped pyramidal SVS
whose **full-resolution (L0) tiles are byte-identical to the source** — zero
re-encode of the original pixel data. This is Strategy **B** from the analysis:
snap the crop to the L0 tile grid so output tiles map 1:1 onto source tiles and
copy them verbatim.

The shipped default (`crop` without `--lossless`) stays Strategy A (full
re-encode, exact requested extent, one JPEG generation). `--lossless` trades
*exact extent* for *zero loss*: the output is the tile-aligned bounding box of
the requested rect (up to ~255 px larger per edge).

## Why this shape (decisions locked in brainstorming)

- **Extent = tile-aligned superset (B), not exact (C).** Verbatim tile copy is
  only possible when the origin snaps *down* to a tile boundary (an unaligned
  origin shifts the whole output grid so every output tile straddles 2–4 source
  tiles). Snapping the extent *up* too makes every output tile full → every tile
  copies verbatim. Strategy C (exact extent, edge re-encode) is out of scope.
- **L0 verbatim, lower levels rebuilt.** A single L0-tile-aligned origin is
  generally *not* tile-aligned at L1/L2 (the grid origin halves each level), so
  full-pyramid passthrough needs per-level snapping or a bounds concept — both
  out of scope. Instead: copy L0 tiles byte-identical (the full-resolution data
  — the thing "lossless" must protect), and rebuild the lower overview levels by
  downsample + re-encode (they are derived previews; the scanner produced them by
  downsampling anyway). "Lossless" here means **the original full-resolution
  pixels are preserved exactly.**

## Out of scope (unchanged from the v1 crop)

- Strategy C (exact-extent edge re-encode).
- Full-pyramid passthrough / the bounds-in-opentile-go concept.
- Non-SVS sources/targets; `--codec`; rewriting `Left`/`Top`.

---

## CLI

One new flag on the existing command:

```
wsitools crop --lossless [--rect X,Y,W,H | --x --y --w --h] -o OUT.svs  [-f] [--no-associated]  INPUT.svs
```

- `--lossless` (bool, default false): enable verbatim-L0 tile-snap crop.
- Coordinates remain **L0 pixel space**; the requested rect is snapped (see
  Geometry). The command **prints the actual snapped rect** so the user knows
  the real output bounds.
- `--quality` still applies to the **rebuilt lower levels and the regenerated
  thumbnail** (default: source Q). It does **not** affect L0 (copied verbatim).
- `--tile-order`, `--bigtiff`, `--workers`, `-f`, `--no-associated` behave as in
  the existing command. `--lossless` is incompatible only with nothing — it's
  orthogonal.

**Interaction with the existing path:** in `runCrop`, branch on `lossless`. The
shipped Strategy-A body is unchanged; the lossless body is a sibling path that
reuses most helpers.

---

## Geometry — snap to the tile-aligned bounding box

Given the requested rect `(x, y, w, h)` in L0 coords and the source L0 tile size
`tile` (`srcL0.TileSize.W`/`.H` — square for SVS), and L0 dims `baseW, baseH`:

```
snapX = (x / tile) * tile                       // origin snapped DOWN
snapY = (y / tile) * tile
endX  = min(ceilDiv(x+w, tile) * tile, baseW)    // far edge snapped UP, clamped
endY  = min(ceilDiv(y+h, tile) * tile, baseH)
snapW = endX - snapX
snapH = endY - snapY
```

The snapped rect is the tile-aligned bounding box of the request. It is a
**superset** of the requested rect (extends in all four directions to the
enclosing tile box; ≤ `tile-1` px extra per side; exact if the request was
already aligned). Source tile-coordinate origin of the copy block:
`stx0 = snapX / tile`, `sty0 = snapY / tile`; output tile grid is
`outTilesX = snapW / tile` × `outTilesY = snapH / tile` (snapW/snapH are exact
multiples of `tile` except where clamped at the image edge, where the last
output tile is a partial edge tile whose *source* tile is also partial — still a
1:1 verbatim copy).

`validateCropBounds(x, y, w, h, baseW, baseH)` (existing) runs on the **requested**
rect before snapping. The snapped rect is within bounds by construction (clamped).

> Tile size assumption: SVS L0 tiles are square (256, or 240 on older Aperio).
> Use `srcL0.TileSize.W` for X and `.H` for Y independently (don't assume 256).

---

## Architecture

### Module touch-points

| File | Change |
|---|---|
| `cmd/wsitools/crop.go` | Add `--lossless` flag; branch `runCrop` into Strategy-A vs lossless paths; add `snapRectToTiles` + `runCropLossless` (or inline branch) |
| `cmd/wsitools/crop_lossless.go` (new) | `writeLosslessL0` (verbatim L0 tile-block copy) + the lossless orchestration body |
| `cmd/wsitools/crop_test.go` | Unit tests for `snapRectToTiles` |
| `tests/integration/crop_test.go` | Lossless byte-identity integration test |

Reused unchanged: `MaterializeCroppedL0`, `buildPyramidFromRaster`,
`BuildCropImageDescription`, `regenCropThumbnail`, `writeOneAssociated`,
`cropPyramidLevels`, the streamwriter `Options`/`AddLevel`/`WriteTile`.

### Lossless data flow (`runCropLossless`)

```
INPUT.svs  → open, require FormatSVS; parse ImageDescription; resolve quality
  │  validateCropBounds(requested rect)
  │  snapRectToTiles → (snapX, snapY, snapW, snapH, stx0, sty0, outTilesX, outTilesY)
  ▼
[1] MaterializeCroppedL0(ctx, srcL0, raster[snapW*snapH*3], snapX, snapY, snapW, snapH)
  │       (decode the snapped region — needed for thumbnail + lower levels)
  ▼
[2] streamwriter.Create(OUT.svs, {ImageDescription: BuildCropImageDescription(.. snapped rect ..), MPP, Mag, ICC, BigTIFF, order})
  ▼
[3] writeLosslessL0(w, srcL0, stx0, sty0, outTilesX, outTilesY, snapW, snapH)
  │       AddLevel{ImageWidth:snapW, ImageHeight:snapH, TileSize:srcTile,
  │                Compression: srcL0.Compression→tag, JPEGTables: srcL0.TilePrefix(),
  │                Photometric:2, SamplesPerPixel:3, BitsPerSample:[8,8,8],
  │                NewSubfileType:0, WSIImageType:pyramid}
  │       for oy,ox: TileInto(stx0+ox, sty0+oy) → WriteTile(ox, oy, bytes)  // verbatim
  ▼
[4] regenCropThumbnail(w, raster, snapW, snapH, quality)        // between L0 and L1
  ▼
[5] if nLevels > 1:
  │     l1, l1W, l1H := halveRaster(raster, snapW, snapH)        // SHARED helper
  │     buildPyramidFromRaster(ctx, w, l1, l1W, l1H, nLevels-1, quality, workers, nil)
  ▼
[6] writeOneAssociated(label); writeOneAssociated(macro/overview)   (unless --no-associated)
  ▼
  w.Close()
```

`nLevels := cropPyramidLevels(snapW, snapH, tile)`. When `nLevels == 1` (crop ≤
~1 tile), steps [5] emit nothing — just verbatim L0 + thumbnail.

### Component spec: `writeLosslessL0`

```go
// writeLosslessL0 emits pyramid level 0 by copying a contiguous block of source
// L0 tiles verbatim (byte-identical compressed bytes), propagating the source
// level's shared codec prefix (JPEG tables, tag 347). The crop origin must be
// tile-aligned so output tile (ox,oy) maps 1:1 to source tile (stx0+ox, sty0+oy).
func writeLosslessL0(w *streamwriter.Writer, srcL0 *opentile.Level, stx0, sty0, outTilesX, outTilesY, outW, outH int) error
```

Implementation:
- `tables := srcL0.TilePrefix()` (tag 347 JPEG tables; nil for codecs without a
  prefix — propagate as-is).
- `compTag := tiff-tag for srcL0.Compression` (use `opentile.CompressionToTIFFTag(srcL0.Compression)`,
  cast to the streamwriter's `Compression uint16` field — same value JPEG=7).
- `AddLevel(streamwriter.LevelSpec{ ImageWidth: outW, ImageHeight: outH,
  TileWidth: srcL0.TileSize.W, TileHeight: srcL0.TileSize.H, Compression: compTag,
  Photometric: 2, SamplesPerPixel: 3, BitsPerSample: []uint16{8,8,8},
  JPEGTables: tables, NewSubfileType: 0, WSIImageType: tiff.WSIImageTypePyramid })`.
- Drive tiles in the writer's order. Reuse the same `LevelHandle` +
  `NextReady` drain pattern as `encodeAndWriteLevel` (so non-row-major
  `--tile-order` still works), but the "process" step is a pure copy instead of
  an encode:
  - source: row-major `(ox, oy)` over `outTilesX × outTilesY`; for each, read
    `srcL0.TileInto(stx0+ox, sty0+oy, buf)` into a fresh buffer and emit
    `pipeline.Tile{X: ox, Y: oy, Bytes: copyOf(buf[:n])}`.
  - process: identity (return the tile unchanged).
  - sink: `lh.WriteTile(t.X, t.Y, t.Bytes)`.
  Use a per-tile fresh buffer (not one shared `tileBuf`) because the pipeline may
  hold several tiles concurrently — or run a simple synchronous row-major loop if
  the order is row-major and skip the pipeline. **Decide in the plan**: simplest
  correct form is a synchronous loop (`AddLevel`; for oy,ox: `TileInto`→fresh
  copy→`lh.WriteTile`; `CloseInput`; drain `NextReady`), since verbatim copy is
  cheap and ordering is trivial. Mirror `encodeAndWriteLevel`'s drain/abort
  structure for correctness.

> Photometric/SamplesPerPixel/BitsPerSample are set to Aperio's RGB-JPEG
> convention (2 / 3 / [8,8,8]) — identical to what the shipped `encodeAndWriteLevel`
> writes for SVS pyramid levels, and what the parity oracle validated. The
> verbatim tiles are the same colorspace as the source (Aperio RGB+APP14), so
> these tags are correct. (If a future non-RGB SVS surfaces, read the source
> IFD's photometric instead — note it; not needed for v1.)

### Metadata / thumbnail / associated

- `BuildCropImageDescription(rawDesc, baseW, baseH, snapX, snapY, snapW, snapH, tile, tile, quality)`
  — the geometry token records the **snapped** rect (must equal actual output
  dims). Provenance chain + appended OriginalWidth/Height as before.
- Thumbnail: `regenCropThumbnail(w, raster, snapW, snapH, quality)` — emitted
  between L0 and L1 (call it after `writeLosslessL0`, before
  `buildPyramidFromRaster`; pass `nil` postL0Hook to the latter).
- label / macro / overview / ICC: passthrough as in Strategy A.

---

## Testing

### Unit (`cmd/wsitools/crop_test.go`)

`TestSnapRectToTiles`:
- Unaligned rect `(100, 80, 300, 300)` with `tile=256`, base `4096×4096`
  → `snapX=0, snapY=0, snapW=512, snapH=512` (bbox of [100,400)×[80,380) =
  [0,512)×[0,512)), `stx0=0, sty0=0, outTilesX=2, outTilesY=2`.
- Already-aligned rect `(256, 512, 512, 256)` → snapped == requested,
  `stx0=1, sty0=2, outTilesX=2, outTilesY=1`.
- Edge clamp: rect near the right/bottom image edge → `endX`/`endY` clamped to
  `baseW`/`baseH`, last output tile partial.

### Integration — byte-identity (`tests/integration/crop_test.go`, gated)

`TestCropLossless_ByteIdentity` (use `CMU-1-Small-Region.svs`, a CI fixture):
1. Open source; pick an **unaligned** rect (e.g. `--rect 300,200,400,400`).
2. Run `crop --lossless --rect 300,200,400,400 -o out.svs <src>`.
3. Re-open `out.svs`. Assert:
   - `Format()==svs`; output L0 dims == the snapped bbox (compute expected snap
     in the test from the source tile size).
   - **For every output L0 tile, `out.Tile(ox,oy)` bytes == `src.Tile(stx0+ox,
     sty0+oy)` bytes** (byte-identical verbatim copy — the core lossless
     guarantee). Also `out.TilePrefix()` == `src.TilePrefix()`.
   - MPP/Magnification preserved; thumbnail + label/overview present (if source
     has them).
4. A second case on a **tile-aligned** rect → snapped == requested (no slop),
   same byte-identity assertion.

> This is a *stronger* assertion than the Strategy-A parity oracle: exact byte
> equality of L0 tiles, not a pixel-diff threshold. It directly proves zero
> re-encode of the full-resolution data.

### Regression

The existing Strategy-A crop tests and downsample tests must stay green
(`--lossless` is an additive branch; the default path is untouched).

---

## Risks / implementation notes (resolve in the plan)

1. **`writeLosslessL0` tile-write mechanism.** Confirm whether a synchronous
   row-major `AddLevel`→`WriteTile`→`CloseInput`→drain loop is sufficient, or
   whether the streamwriter requires the `NextReady` async drain even for
   in-order writes (mirror `encodeAndWriteLevel`). Pick the simplest correct
   form; verbatim copy needs no worker pool.
2. **Buffer lifetime.** `TileInto` reuses the caller buffer; each `WriteTile`
   must receive a stable copy (the streamwriter may defer the write). Use a
   fresh slice per tile (or confirm `WriteTile` copies internally).
3. **Compression-tag mapping.** `srcL0.Compression` is an `opentile.Compression`
   enum; the streamwriter `LevelSpec.Compression` is a TIFF `uint16` tag. Map via
   `opentile.CompressionToTIFFTag` (already used in `internal/downscale`). For
   JPEG this is 7; verify J2K maps to the tag the SVS reader recognizes (defer
   J2K validation — JPEG is the v1 target; if `TilePrefix()` is nil for J2K the
   copy still works).
4. **Decode redundancy.** The lossless path both decodes the L0 region
   (`MaterializeCroppedL0`, for thumbnail + lower levels) and reads the raw tiles
   again (`writeLosslessL0`). Two reads of the same source tiles; acceptable
   (decode dominates; raw read is cheap). Note it; do not prematurely fuse.
5. **`buildPyramidFromRaster` from L1 — halve must match.** The L0→L1 halve done
   before calling `buildPyramidFromRaster` MUST be byte-identical to that
   function's own inter-level halve, or the rebuilt L1 differs from what the
   Strategy-A path would produce. **Extract the inter-level halve** (the
   even-dimension `cropRaster` + `otresample.ImageInto(Box)` step currently
   inlined in `buildPyramidFromRaster`'s loop, downsample.go) into a shared
   helper `halveRaster(raster []byte, w, h int) (out []byte, outW, outH int)` and
   call it from BOTH `buildPyramidFromRaster`'s loop and the lossless path. DRY +
   guaranteed-matching. Then feed `buildPyramidFromRaster` the halved raster with
   `nLevels-1` and `nil` postL0Hook (thumbnail already emitted). Level count:
   verbatim L0 (1) + rebuilt (`nLevels-1`) = `nLevels` =
   `cropPyramidLevels(snapW,snapH,tile)`. (Refactoring `buildPyramidFromRaster` to
   use `halveRaster` must not change downsample output — guard with the existing
   downsample integration regression test.)
