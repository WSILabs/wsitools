# SP3c Phase 1b — lossless DZI/SZI — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** SP3c Phase 1 (merged, main@a81f694). Phase 1b is the first deferred
follow-on.
**Umbrella spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-unified-convert-design.md`
(the "Phase 1b" boundary paragraph).

## Goal

Make `--lossless` valid for `convert --to dzi|szi`: copy the source's stored JPEG
tiles into the Deep Zoom **base level** verbatim (no decode/re-encode → no
generational loss), regenerating the edges and the lower pyramid levels. Mirrors
the existing lossless semantics of `crop`/`downsample` ("base byte-identical, lower
levels regenerated").

## Why this is now simple: opentile assembles complete tiles

`opentile.Level.Tile(tx,ty)` / `TileInto` (`ImageRawTile`) returns the **complete,
self-contained** compressed tile — a standalone JPEG for a JPEG source. (The
`TilePrefix()`+`TileBodyInto()` pair is the documented "splice-prefix optimization
family" for shared-prefix containers like TIFF's tag-347; we don't need it.) So a
verbatim DZI base tile is just `srcL0.TileInto(col,row)` written to
`dzi.WriteTile(maxLevel, col, row, bytes)` — lossless (stored DCT coefficients,
already assembled into a complete JPEG; no re-quantization).

## Mechanism: a lossless sink substitutes interior base tiles

The DZI engine path (`emitDZIPyramid` → `retile.Run` → `dziWriterSink`) decodes the
source and re-encodes every tile of every level. For lossless we change **only the
sink** when `--lossless`:

- The engine numbers levels finest-first; **engine level 0 = DZI base = native**
  (`dziWriterSink` maps engine k → DZI level `nLevels-1-k`).
- A `losslessDZISink` wraps `dziWriterSink`. On `WriteTile(level, col, row,
  encoded)`:
  - **engine level 0 (base) AND interior tile** (`dzi.EdgeTileDims(baseW, baseH,
    tileSize, col, row) == (tileSize, tileSize)`): write the **verbatim** source
    tile (`srcL0.TileInto(col,row)`), discarding the engine's `encoded`.
  - **base edge tile, or any lower level**: write the engine's `encoded`
    (regenerated — edge tiles get the correct trimmed dims; lower levels are the
    box-reduced descent).

The engine runs otherwise unchanged. **Guarantee:** interior base tiles are
byte-identical to the source's stored tiles; edges + lower levels are regenerated.
This is slightly weaker than `crop --lossless`'s full-L0 byte-identity (edges are
re-encoded) — the printed notice must say so.

**Known v1 inefficiency (documented, not a correctness issue):** the engine still
decodes + re-encodes the base interior tiles before the sink discards them. A
future optimization can skip the base level in the engine and write it in a
dedicated verbatim pass. v1 favors reusing `emitDZIPyramid` wholesale for
correctness.

## Geometry: verbatim copy requires a tile-grid match

Verbatim base tiles only align when the **DZI tile grid == the source L0 tile
grid**: `--dzi-tile-size` = the source L0 tile size, `--dzi-overlap` = 0 (overlap
re-cuts tiles → would force a re-encode). So `--lossless` **auto-configures**
`cfg.TileSize = srcL0.TileSize`, `cfg.Overlap = 0`, and prints a notice. If the
user **explicitly** set `--dzi-tile-size`/`--dzi-overlap` to conflicting values,
**error** (don't silently override an explicit flag) telling them lossless fixes
the geometry.

## Guards

- **jpeg source only.** A J2K/HTJ2K (or other non-JPEG) source cannot be copied
  verbatim into a Deep Zoom tile (a browser deep-zoom viewer renders only
  jpeg/png). If `srcL0.Compression() != JPEG`, error: "lossless DZI requires a JPEG
  source; got <codec>".
- **`--lossless` + `--factor`/`--target-mag` = contradiction** → hard error (can't
  copy tiles verbatim *and* downsample). (Lower DZI levels are always regenerated;
  the *base* is what's verbatim, and a downsample changes the base.)
- **`--lossless` only with `--to dzi|szi`** in Phase 1b. For other `--to`, error and
  point at `crop --lossless` (the TIFF-family lossless path). `convert --lossless`
  without `--rect`/transform for the TIFF family is out of scope here.
- **png `--codec`/`--dzi-format` + `--lossless`** → error (PNG is a re-encode; a
  JPEG source can't be verbatim-copied as PNG). Lossless implies the source codec
  (jpeg).

## CLI

```
convert --to dzi --lossless -o out.dzi slide.svs
```
- New `--lossless` flag on `convert` (bool). Phase-1b-scoped to dzi/szi.
- Prints: `lossless: base tiles copied verbatim (tile-size N, overlap 0); edges +
  lower levels regenerated`.

## Components

| Unit | Responsibility | Source |
|---|---|---|
| `--lossless` flag on `convert` | new bool flag; validated in `runConvert` | `convert.go` |
| lossless validation + auto-config | jpeg-source check, factor contradiction, tile-size/overlap auto-set + notice (or conflict error) | `runConvertDZI`/`runConvertSZI` (`convert_dzi.go`/`convert_szi.go`) |
| `losslessDZISink` | wraps `dziWriterSink`; substitutes verbatim `srcL0.TileInto` for interior base tiles | `convert_dzi.go` |
| `emitDZIPyramid` lossless wiring | accept a sink (or a `lossless` flag) so the lossless sink is used; otherwise unchanged | `convert_dzi.go` |

## Testing

- **Interior base tile byte-identity:** open the source and the output DZI; for an
  interior base tile, `srcL0.TileInto(c,r)` == the DZI base tile file bytes.
- **Output is a valid DZI:** opens via opentile's DZI reader (or decode each base
  tile as a standalone JPEG); dims/levels correct.
- **Edge tiles correct:** right/bottom edge base tiles decode to the trimmed
  remainder dims (not padded), and are valid JPEGs.
- **Lower levels present + valid:** the full descent down to 1×1 exists.
- **jpeg-source guard:** a JP2K SVS source (`JP2K-33003-1.svs`) → clear error.
- **factor contradiction:** `--lossless --factor 2` → error.
- **geometry auto-config notice** + the explicit-conflict error.
- **lossy DZI unchanged:** `convert --to dzi` (no `--lossless`) output is
  unchanged.
- SZI mirrors DZI.

## Boundaries / deferred

**In Phase 1b:** lossless `--to dzi|szi` (jpeg source), the verbatim-interior sink,
geometry auto-config + guards.

**Deferred:** the base-level engine-skip efficiency optimization; lossless for the
TIFF family via `convert` (use `crop --lossless`); Phase 2 conformance gate.
