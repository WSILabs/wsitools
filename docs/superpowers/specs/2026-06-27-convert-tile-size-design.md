# `convert --tile-size` — Design

**Date:** 2026-06-27
**Status:** Approved (brainstorming) — ready for implementation plan
**Branch:** `feat/convert-tile-size` (stacked on `fix/reencode-jpeg-photometric`, which it
shares convert/tiling code with; that colour fix should land first or merge together).

## Problem

`wsitools convert` has no general output-tile-size control. The only tile-size flag is
`--dzi-tile-size` (default 256), used solely by `--to dzi|szi`. For the TIFF family the
output tile size is decided implicitly and inconsistently:

- Verbatim tile-copy: preserves the source tiling (correct — can't change losslessly).
- Lossy same-geometry transcode (`--codec`) and stitched BIF re-encode: preserve the
  source tile size (`l0.TileSize` / `lvl.TileSize`).
- `--factor`, `downsample`, `--to ife`: **hardcode `outputTileSize = 256`** — wrong; a
  re-encode should default to the source tiling, not silently force 256.

When re-encoding, the tiles are rebuilt anyway, so the tile size should be user-
configurable, with a sensible default.

## Goal

A single `--tile-size N` flag that **replaces** `--dzi-tile-size`, governs output tiling
for every raster/re-encode target, and **defaults to the source's tile size** when unset
(fixing the hardcoded-256 paths). Governing principle (the user's): *honor the default +
any override, and error clearly on a disallowed or not-yet-supported combination.*

## Flag

- `--tile-size N` (int, default `0` = "unset"). `cvDZITileSize` → `cvTileSize`;
  `--dzi-tile-size` is **removed** (pre-1.0; breaking changes are expected per
  `CHANGELOG`/README). `--dzi-overlap` is unchanged.
- Validation when set: `N > 0`. (DZI/SZI impose no specific allowed sizes — the writers
  require only `TileSize > 0`, `internal/dzi/writer.go:49`; so there is nothing stricter
  to enforce.) A future tightening (e.g. multiple-of-8 for JPEG MCU cleanliness) is out
  of scope; `N > 0` is the only gate.

## Default resolution (unset)

A shared helper:

```go
// resolveTileSize returns the output tile edge: the user's --tile-size when >0,
// else the source level-0 tile width, else 256 when the source has no usable
// square tile geometry.
func resolveTileSize(srcL0TileW int, flag int) int {
    if flag > 0 {
        return flag
    }
    if srcL0TileW > 0 {
        return srcL0TileW
    }
    return 256
}
```

Applied uniformly so unset → match source for: svs/tiff/ome-tiff/cog-wsi (re-encode,
`--factor`, `downsample`), `--to ife`, and `--to dzi|szi`. This replaces the
`outputTileSize = 256` constant and the bare `l0.TileSize.W` reads in the re-encode
paths. DZI/SZI's old hardcoded-256 default becomes match-source (uniform rule); the
lossless-DZI resolver (`losslessDZIConfig`) keeps adjusting for verbatim base tiles, just
sourcing its `reqTileSize`/`userSetTileSize` from the unified flag.

## Per-target / per-path behavior

| Situation | Behavior |
|---|---|
| svs/tiff/ome-tiff/cog-wsi, ife, dzi/szi — re-encode/raster | retile to the resolved size |
| `--to dicom` | **honored** — re-tile in `internal/derivedsource`; DICOM `Rows`/`Columns` follow the tile size |
| `--to bif` | **error**: `--tile-size is not supported for --to bif` (verbatim DP-200 vendor layout; no re-tiling) |
| Tile-copyable input, `--tile-size` == source tile size | no-op; stays a lossless tile-copy |
| Tile-copyable input, `--tile-size` ≠ source, **no `--codec`** | auto re-encode; **codec defaults to the source's own codec** (see below) |
| …same but source codec has no wsitools encoder (LZW/Deflate/None) | **error**: re-encode required but no encoder for the source codec; pass `--codec`. (Tracked: file a wsitools issue to add LZW/uncompressed encode targets.) |

### Forced-re-encode codec default = source codec

When `--tile-size` differs from the source tiling on an otherwise tile-copyable input and
the user gave no `--codec`, retiling requires a decode + re-encode. The re-encode
**preserves the source compression family**: the default codec is the source's own codec,
not jpeg. `source.Compression.String()` already yields the codec-registry names
(`"jpeg"`, `"jpeg2000"`, `"jpegxl"`, `"avif"`, `"webp"`, `"htj2k"`), so the default is
`codec.Lookup(src.L0.Compression().String())`:

- Found → use it (JPEG source → JPEG, JPEG 2000 source → JPEG 2000, …).
- Not found (LZW/Deflate/None/Iris/PNG-as-tiles, i.e. no lossy/​tile encoder) → **error**
  asking for an explicit `--codec`.

An explicit `--codec` always overrides this default.

This forced-re-encode path is detected by making `tileCopyEligible`
(`convert_shared.go:52`) return false when `--tile-size` is set and differs from the
source tile size, so the existing dispatch routes to the re-encode path; the re-encode
path resolves its codec from `--codec` or the source-codec default above.

## Components / files

| File | Change |
|---|---|
| `cmd/wsitools/convert.go` | rename flag `dzi-tile-size`→`tile-size`, var `cvDZITileSize`→`cvTileSize`, default `0`; help text |
| `cmd/wsitools/convert_shared.go` | `resolveTileSize` helper; `tileCopyEligible` gains a "tile-size forces re-encode" rule; source-codec-default resolution helper (`reencodeCodecFor(src, codecFlag)`) |
| `cmd/wsitools/convert_tiff.go` | re-encode paths use `resolveTileSize`; route the forced-re-encode codec default; BIF/DICOM guards live in dispatch |
| `cmd/wsitools/convert_stitched.go` | `tile := resolveTileSize(l0.TileSize.W, cvTileSize)` (was `l0.TileSize.W`) |
| `cmd/wsitools/convert_factor.go`, `downsample.go`, `convert_ife.go` | replace `outputTileSize` (256) with `resolveTileSize(...)` |
| `cmd/wsitools/convert_dzi.go`, `convert_szi.go` | use `cvTileSize`/`resolveTileSize`; thread into `losslessDZIConfig` inputs |
| `cmd/wsitools/convert_bif.go` (or dispatch) | error if `cvTileSize > 0` |
| DICOM path (`convert_factor.go` DICOM branch / `internal/derivedsource`) | re-tile to the resolved size; set frame `Rows`/`Columns` accordingly |
| `cmd/wsitools/convert_dzi.go`/`szi` flag refs, tests, help, README | flag rename fallout |

`outputTileSize = 256` (downsample.go:49) is removed; 256 survives only as the
`resolveTileSize` fallback.

## DICOM specifics

DICOM imposes no tile-size restriction — `dataset.go:232-233` writes `Rows`/`Columns`
straight from `spec.TileSize`. Today both DICOM sub-paths inherit the source tiling
(`WritePyramid` copies frames; `derivedsource` re-encodes at `src.TileSize()`). To honor
`--tile-size`, the `derivedsource` transform path must **re-tile**: decode the source and
regroup pixels into resolved-size frames before re-encoding, and the writer's
`spec.TileSize` (→ `Rows`/`Columns`) follows. The verbatim `WritePyramid` (no transform)
path stays source-tiled; `--tile-size` on a no-transform `--to dicom` therefore implies a
re-tiling transform (routes through `derivedsource`).

## Errors (the governing principle, made concrete)

- `--tile-size 0` or negative → error: must be positive.
- `--to bif --tile-size N` → error: unsupported for bif.
- `--tile-size` ≠ source on a tile-copyable input whose source codec has no encoder, no
  `--codec` → error: re-encode needed, pass `--codec`.
- Everything else: honor.

## Testing

- **Unit:** `resolveTileSize` (flag>0, source-match, fallback); `reencodeCodecFor`
  (jpeg→jpeg, jp2k→jp2k, LZW→error); `tileCopyEligible` with `--tile-size` differing.
- **CLI/integration (CMU + fixtures):**
  - `--to svs --tile-size 512` on a 256/240-tiled source → output L0 `TileWidth`==512.
  - unset on `--factor`/`downsample`/`--to ife` → output tile == source tile (regression
    for the hardcoded-256 bug).
  - `--to svs --tile-size <same as source>` → stays a lossless tile-copy (byte-identical
    pyramid via `hash --mode pixel` / tag check), no re-encode.
  - `--to svs --tile-size 512` (no `--codec`) on a JPEG source → re-encodes JPEG (codec
    preserved), L0 tile 512.
  - `--to bif --tile-size 512` → error.
  - `--to dicom --tile-size 512` → DICOM `Rows`/`Columns`==512.
  - dzi/szi `--tile-size 512` → `.dzi` manifest TileSize 512 (replaces `--dzi-tile-size`
    coverage).

## Decisions locked in brainstorming

- One `--tile-size` flag **replaces** `--dzi-tile-size`.
- Unset default = **match source tile size**, fallback 256 (uniform, incl. DZI/SZI).
- Forced-re-encode default codec = **source's own codec**; error if no encoder for it
  (and **file a wsitools issue** to add LZW/uncompressed encode targets).
- `--to dicom`: **honor** (re-tile via derivedsource).
- `--to bif`: **error** (vendor format).
- DZI/SZI: no specific allowed sizes → `N > 0` is the only validation.

## Related cleanup: remove the `--jobs` alias

`--jobs` is a redundant alias of `--workers`, present on `convert`, `crop`,
`downsample`, and `transcode` (and reversed on `downsample`, where `--jobs` is the
primary and `--workers` the alias). Reconciled by `resolveWorkers`
(`cmd/wsitools/workers.go`). It bloats the (already long) option list for no benefit.

**Change:** `--workers` becomes the single canonical worker flag on every command;
`--jobs` is **removed** everywhere (breaking; pre-1.0). `resolveWorkers` and
`workers.go` are deleted — with one flag there is nothing to reconcile; each command
reads its `--workers` value directly.

| File | Change |
|---|---|
| `cmd/wsitools/convert.go` | drop `cvJobs` + `--jobs`; `cvWorkers` used directly (remove the `resolveWorkers` call) |
| `cmd/wsitools/crop.go` | drop `cropJobs` + `--jobs`; `cropWorkers` directly |
| `cmd/wsitools/transcode.go` | drop `cvJobs` + `--jobs` |
| `cmd/wsitools/downsample.go` | make `--workers` the primary (default `runtime.NumCPU()`, preserving today's effective default); drop `--jobs`/`dsJobs` |
| `cmd/wsitools/workers.go` | **delete** (`resolveWorkers` no longer needed) |
| tests / help text | update any `--jobs` references |

Defaults are preserved: `convert`/`crop`/`transcode` keep `--workers` default `0`
(GOMAXPROCS); `downsample` keeps the effective `NumCPU` default on `--workers`.

## Out of scope / follow-ups

- A wsitools issue to add LZW/uncompressed (and Deflate) as **encode** targets, so
  re-tiling a lossless-source without `--codec` can preserve it instead of erroring.
- Stricter `--tile-size` validation (multiple-of-8, max bound).
- BIF tile-size support (would require a re-tiling BIF writer; format-constrained).
