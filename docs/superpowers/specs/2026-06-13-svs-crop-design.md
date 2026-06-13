# `wsitools crop` — design spec

**Date:** 2026-06-13
**Status:** approved (brainstorming) — ready for implementation plan
**Background analysis:** `docs/aperio-svs-crop-analysis.md` (reverse-engineering of
how Aperio ImageScope crops an SVS)

---

## Goal

Add a `wsitools crop` command that produces a **new pyramidal WSI of a
rectangular region** of a source slide. Unlike `region` (which extracts a flat
PNG of one level) and `downsample` (which reduces the full extent), `crop`
preserves the source's resolution/magnification and rebuilds a full tiled
pyramid for the cropped sub-region, with the output anchored at pixel origin
`(0,0)`.

**v1 scope: SVS in → SVS out, full re-encode (Strategy A).**

## Why Strategy A (full re-encode)

The analysis documents three strategies (A re-encode / B tile-snap-lossless /
C snap+edge-reencode). v1 ships **A** because it:

- reproduces what ImageScope itself does (exact requested extent, clean `(0,0)`
  origin), so existing Aperio users get familiar behaviour;
- works for any crop origin (no tile-alignment constraint) and any source codec;
- reuses the existing `convert`/`downsample` decode→pyramid machinery almost
  verbatim;
- has a **parity oracle**: `CMU-2.svs` vs
  `CMU-2_cropped_46492_3599_27836_25633_imagescope.svs` lets the test suite
  verify wsitools' crop decodes to within one JPEG generation of ImageScope's.

A is lossy by exactly one JPEG re-encode generation (~0.5 mean / ~10 max abs
diff per channel — the analysis measured this). The lossless modes (B/C) are
explicitly **out of scope for v1** and would arrive later behind a `--lossless`
flag.

---

## CLI

```
wsitools crop [--rect X,Y,W,H | --x N --y N --w N --h N] -o OUT.svs \
              [--quality N] [--workers N] [--tile-order row-major|hilbert|morton] \
              [--bigtiff auto|on|off] [-f|--force] [--no-associated]  INPUT.svs
```

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `--rect` | `X,Y,W,H` | — | Crop rectangle in **L0 pixel coordinates** |
| `--x` `--y` `--w` `--h` | int | — | Same rectangle, component form (mutually exclusive with `--rect`) |
| `-o` `--output` | path | — (required) | Output SVS path |
| `--quality` | int | **source Q** | JPEG quality for re-encoded tiles. If unset, defaults to the source's parsed `Q=` value (so the parity oracle holds); falls back to `30` if the source has none. |
| `--workers` | int | NumCPU | Encode worker count |
| `--tile-order` | enum | `row-major` | Tile-ordering strategy (passthrough to writer; matches `convert`/`downsample`) |
| `--bigtiff` | enum | `auto` | BigTIFF promotion mode |
| `-f` `--force` | bool | false | Overwrite existing output |
| `--no-associated` | bool | false | Skip label/macro/thumbnail/overview |

**Coordinate semantics:**

- The rectangle is interpreted in **level-0 (base) pixel space**. There is no
  `--level` flag — a pyramidal crop is always defined on the base image.
- `--rect` and `--x/--y/--w/--h` are mutually exclusive; exactly one form must
  be supplied (reuse `region.go`'s existing rules).
- `W` and `H` must be positive.

**Bounds validation (hard errors):**

- `X >= 0`, `Y >= 0`
- `X + W <= L0.Width`, `Y + H <= L0.Height`

If the rect extends past the L0 bounds, fail with a message naming the offending
edge and the L0 dimensions. (v1 does **not** clamp — an out-of-bounds crop is a
user error, not a silent truncation.)

**Path validation:** mirror `downsampleToSVS` — input must exist; output must
not exist unless `--force`; input and output absolute paths must differ.

---

## Architecture

### Module touch-points

| File | Responsibility | Change |
|---|---|---|
| `cmd/wsitools/coords.go` (new) | Shared `--rect` / `--x/--y/--w/--h` parsing + resolution | Lift `parseRect`/`resolveRect` out of `region.go` |
| `cmd/wsitools/region.go` | (existing) flat-PNG region extract | Use the shared `coords` helpers (behaviour unchanged) |
| `cmd/wsitools/crop.go` (new) | `crop` cobra command + `runCrop` orchestration | New |
| `internal/downscale/crop.go` (new) | `MaterializeCroppedL0` — tile-by-tile region decode + offset paste | New |
| `cmd/wsitools/downsample.go` | `buildPyramid` halving loop | Refactor: extract `buildPyramidFromRaster` (in-memory L0 → pyramid); `buildPyramid` materialises then delegates |
| `cmd/wsitools/svs_imagedesc.go` | Aperio `ImageDescription` mutation | Add `MutateForCrop` |
| `cmd/wsitools/crop_thumbnail.go` (new) | Regenerate the thumbnail from the cropped raster | New |

### Data flow (`runCrop`)

```
INPUT.svs
  │  opentile.OpenFile → require FormatSVS
  │  source.ReadSourceImageDescription → ParseImageDescription
  │  resolveRect (L0 coords) → validate against L0 bounds
  ▼
[1] MaterializeCroppedL0(ctx, srcL0, outL0[cropW*cropH*3], cropX, cropY, cropW, cropH)
  │      decode each source-L0 tile overlapping the crop, paste overlap → outL0
  ▼
[2] desc.MutateForCrop(baseW, baseH, cropX, cropY, cropW, cropH, tileW, tileH, quality)
  │      prepend geometry line + provenance chain + fresh OriginalWidth/Height
  ▼
[3] streamwriter.Create(OUT.svs, {ImageDescription: desc.Encode(), MPP, Mag, ICC, …})
  ▼
[4] buildPyramidFromRaster(ctx, w, outL0, cropW, cropH, quality, workers, postL0Hook)
  │      L0 = outL0; each level: encode 256×256 JPEG tiles; box-halve → next level
  │      stop when min(levelW, levelH) < tileSize
  │      postL0Hook (after L0) → write regenerated thumbnail
  ▼
[5] writeOneAssociated(label); writeOneAssociated(macro/overview)   (unless --no-associated)
  ▼
  w.Close() → OUT.svs
```

---

## Component specs

### 1. `downscale.MaterializeCroppedL0`

```go
// MaterializeCroppedL0 decodes the source-L0 region [cropX, cropX+cropW) ×
// [cropY, cropY+cropH) and writes it, anchored at (0,0), into outL0 (an
// RGB888 raster of size cropW*cropH*3). It decodes only the source tiles that
// overlap the crop and pastes each tile's overlapping sub-rect — memory-bounded
// like MaterializeReducedL0, but offset (no scaling).
func MaterializeCroppedL0(ctx context.Context, srcL0 *opentile.Level, outL0 []byte, cropX, cropY, cropW, cropH int) error
```

Algorithm (mirrors `MaterializeReducedL0` structure):

- Resolve the decoder factory for `srcL0.Compression`.
- Compute the source tile range overlapping the crop:
  `tx0 = cropX / srcTileW … tx1 = (cropX+cropW-1) / srcTileW` (inclusive),
  same for `ty`.
- For each overlapping source tile `(tx, ty)`:
  - `TileInto` → decode full tile to RGB (`DecodeReducedTile` with `factor=1`,
    or a direct full decode — factor=1 yields the unscaled tile).
  - The tile covers source rect `[sx0, sx1) × [sy0, sy1)` (clamped to source
    bounds). Intersect with the crop rect to get the copy region
    `[ix0, ix1) × [iy0, iy1)` in **source-pixel** space.
  - Source-tile-local offset of the copy region: `(ix0 - sx0, iy0 - sy0)`.
  - Destination offset in `outL0`: `(ix0 - cropX, iy0 - cropY)`.
  - Paste `validW = ix1-ix0`, `validH = iy1-iy0` from the decoded tile (at its
    local offset) into `outL0` at the destination offset. (Extend
    `PasteIntoRaster` or add a `PasteSubRect` that takes a source offset — see
    note below.)

**`PasteIntoRaster` gap:** the existing `PasteIntoRaster` copies from the
**top-left** of the source tile. Cropping needs a copy that starts at an
**interior** offset within the source tile (when the crop origin is mid-tile).
Add:

```go
// PasteSubRect copies a validW×validH region starting at (srcX, srcY) within
// the source RGB raster (stride srcStrideW*3) into dst at (dx, dy).
func PasteSubRect(dst []byte, dstW, dstH, dx, dy int, src []byte, srcStrideW, srcX, srcY, validW, validH int)
```

`PasteIntoRaster` becomes `PasteSubRect(..., srcX:0, srcY:0, ...)`.

### 2. `buildPyramidFromRaster` (refactor of `buildPyramid`)

Extract the in-memory-raster pyramid loop (current `downsample.go:291–366`) into:

```go
// buildPyramidFromRaster encodes an in-memory RGB888 L0 raster into a tiled
// JPEG pyramid via the streamwriter, box-halving between levels. It stops when
// the next level's min dimension would fall below the output tile size.
// postL0Hook runs immediately after L0 (used to interleave the thumbnail IFD).
func buildPyramidFromRaster(ctx context.Context, w *streamwriter.Writer, l0 []byte, l0W, l0H, quality, workers int, postL0Hook func() error) error
```

`buildPyramid` (the `downsample` path) keeps its current outer responsibilities
(progress-bar total, `MaterializeReducedL0`) and then calls
`buildPyramidFromRaster`. Behaviour for `downsample` must be **unchanged**.

**Level-count policy:** halve while `min(levelW, levelH) >= outputTileSize`
(`256`). Concretely: emit L0, then keep emitting halved levels until the level
just emitted has `min(w,h) < 2*outputTileSize` would make the *next* halve
smaller than a tile — i.e. stop when `levelW/2 < outputTileSize ||
levelH/2 < outputTileSize`. This yields a pyramid whose smallest level is ≥ one
tile and < two tiles on its short side. For the CMU-2 parity crop
(`27836×25633`) this produces L0/L1/L2 (matching ImageScope's 3 levels in
count; exact downsample spacing need not match — the oracle checks L0 pixels,
not level spacing).

> Note: this differs from `downsample`'s "one level per source level" policy.
> `crop` derives depth from the crop dimensions because the source pyramid's
> level count reflects the whole-slide extent, not the crop's. The refactored
> `buildPyramidFromRaster` therefore takes no source-level count; it computes
> depth from `l0W/l0H` using the policy above. (`buildPyramid` for `downsample`
> passes its raster in and the same dimension-driven policy applies — verify in
> a test that `downsample` output level counts are unchanged for the CMU
> fixtures; if the policy would change them, gate the old behaviour behind the
> caller. See Risks.)

### 3. `AperioDescription.MutateForCrop`

```go
// MutateForCrop rewrites the parsed Aperio ImageDescription for a crop, per the
// ImageScope recipe (see docs/aperio-svs-crop-analysis.md):
//   - prepend a geometry line: "<baseW>x<baseH> [x,y WxH] (tileW x tileH) JPEG/RGB Q=q;"
//   - append the source description verbatim after the ';' (provenance chain)
//   - append fresh OriginalWidth/OriginalHeight = the pre-crop base dims
//   - keep MPP, AppMag, ImageID, Filename, and scanner fields unchanged
//   - leave Left/Top unchanged (Aperio does the same; cosmetic for OpenSlide/QuPath)
func (d *AperioDescription) MutateForCrop(baseW, baseH, x, y, cropW, cropH, tileW, tileH, quality int)
```

**Exact output structure** (per the analysis "Cropped" example,
`docs/aperio-svs-crop-analysis.md:134–139`):

```
Aperio Image Library v<VER>
<baseW>x<baseH> [<x>,<y> <cropW>x<cropH>] (<tileW>x<tileH>) JPEG/RGB Q=<q>;<SOURCE-DESCRIPTION-VERBATIM>|OriginalWidth = <baseW>|OriginalHeight = <baseH>
```

i.e. the function:

1. Emits a header line `Aperio Image Library v<VER>`. **`VER` must keep the
   literal `Aperio Image Library v` prefix** — opentile-go's SVS classifier
   keys on it, so dropping it would make the output unrecognisable as SVS.
   Use the wsitools version (e.g. `Aperio Image Library v<Version>`); the plan
   may instead mirror the source's `VER` if the wsitools string trips the SVS
   version parser — verify against opentile-go during planning.
2. Emits the geometry line
   `<baseW>x<baseH> [x,y cropWxcropH] (tileW x tileH) JPEG/RGB Q=q` terminated
   by `;`.
3. Appends the **entire source `ImageDescription` verbatim** immediately after
   the `;` (this carries the original `Aperio Image Library v10.0.51` header +
   its geometry + its full field block — the provenance chain).
4. Appends a fresh `OriginalWidth = <baseW>` / `OriginalHeight = <baseH>` pair
   (the pre-crop base dims) after the chain.

`MPP`, `AppMag`, `ImageID`, `Filename`, scanner fields, and `Left`/`Top` are all
inherited unchanged because the source block is copied verbatim — `MutateForCrop`
adds the geometry framing around it, it does **not** rewrite individual source
fields. Model the prepend/append text handling on `MutateForDownsample`.

### 4. Thumbnail regeneration (`crop_thumbnail.go`)

```go
// regenCropThumbnail renders a thumbnail JPEG from the cropped L0 raster at the
// crop's aspect ratio (longest side ≈ thumbLongSide) and writes it as the
// thumbnail associated IFD via the streamwriter (NewSubfileType bits per the
// thumbnail convention used by writeOneAssociated / downsample).
func regenCropThumbnail(w *streamwriter.Writer, l0 []byte, l0W, l0H int) error
```

- Target longest side: `thumbLongSide = 1024` (matches the order of magnitude of
  ImageScope's regenerated thumbnail; exact dims need not match the oracle).
- Compute thumb dims preserving aspect: if `l0W >= l0H`,
  `tw = 1024, th = round(1024 * l0H / l0W)`, else swap.
- Box-downscale the L0 raster to `tw×th` (reuse `otresample.ImageInto` with
  `Box`, as the pyramid loop does).
- Encode as a single JPEG (baseline, the thumbnail is a whole image, not tiled)
  and add via the streamwriter's associated/thumbnail path. Follow exactly how
  `downsample`'s `writeOneAssociated` + the thumbnail IFD-ordering convention
  (`downsample.go:316`, `534`) emit a thumbnail so opentile-go classifies it
  correctly (`WSIImageTypeThumbnail`, `NewSubfileType=0` for thumbnail per
  `downsample.go:520–536`).
- Emitted via `postL0Hook` so it lands between L0 and L1 (Aperio IFD order).

> Implementation note: confirm whether the streamwriter exposes a
> single-image (non-tiled) associated-write path the way `writeOneAssociated`
> does for passthrough images. If `writeOneAssociated` only handles
> passthrough (copying existing compressed bytes), add a sibling that takes
> freshly-encoded JPEG bytes + dims + type and writes the IFD. Inspect
> `writeOneAssociated` (`downsample.go:522`) and the streamwriter associated API
> before the plan locks the signature.

### 5. Associated passthrough

- `label` and `macro`/`overview` pass through **verbatim** using the existing
  faithful-copy path (`writeOneAssociated`), exactly as `downsampleToSVS` does.
- Segregation/IFD ordering: thumbnail between L0 and L1 (via `postL0Hook`);
  label then macro/overview after the pyramid — identical to `downsampleToSVS`.
- `--no-associated` skips all associated images (no thumbnail regeneration
  either).

### 6. ICC + writer options

Pass `src.ICCProfile()`, `desc.MPP` (X and Y), `desc.AppMag` into
`streamwriter.Options`, exactly as `downsampleToSVS`. BigTIFF prediction: reuse
`predictBigTIFFNeeded` against the cropped L0 dims (a crop is smaller than the
source, so BigTIFF is rarely needed, but keep the `auto`/`on`/`off` flag).

---

## Testing

### Unit tests

- **`downscale.MaterializeCroppedL0`** (`internal/downscale/crop_test.go`):
  build a synthetic 2-tile × 2-tile source level with a known per-pixel pattern
  (e.g. `pix = f(x,y)`), crop a mid-tile-origin rect spanning all four tiles,
  assert every output pixel equals the source pattern at `(cropX+ox, cropY+oy)`.
  Covers the interior-offset paste (the `PasteSubRect` path).
- **`PasteSubRect`** (`internal/downscale/crop_test.go`): direct test of the
  sub-rect copy with a non-zero source offset.
- **`MutateForCrop`** (`cmd/wsitools/crop_imagedesc_test.go`): feed the CMU-2
  original `ImageDescription`, crop `[46492,3599 27836×25633]`, assert the
  output string (a) begins with the geometry line
  `78000x30462 [46492,3599 27836x25633] (256x256) JPEG/RGB Q=30;`, (b) contains
  the original `Aperio Image Library v10.0.51` block verbatim in the chain,
  (c) ends with the appended `OriginalWidth = 78000`/`OriginalHeight = 30462`,
  (d) preserves `MPP = 0.4990`, `AppMag = 20`, `ImageID = 1004487`, and leaves
  `Left`/`Top` unchanged.
- **Rect bounds rejection** (`cmd/wsitools/crop_test.go`): `resolveRect` +
  bounds-check unit — out-of-bounds rect (e.g. `X+W > L0W`) returns an error
  naming the edge; negative origin rejected; `--rect` and `--x/--y/--w/--h`
  together rejected.

### Integration test — parity oracle (gated by `WSI_TOOLS_TESTDIR`)

`tests/integration/crop_test.go` (build tag `integration`):

1. Skip unless both `CMU-2.svs` and
   `CMU-2_cropped_46492_3599_27836_25633_imagescope.svs` exist in
   `$WSI_TOOLS_TESTDIR/svs`.
2. Run `crop --rect 46492,3599,27836,25633 -o out.svs CMU-2.svs`.
3. Re-open `out.svs` and the ImageScope crop with opentile-go. Assert:
   - `Format() == svs`
   - `out` L0 size `== 27836×25633` (exact requested extent)
   - `out` MPP `== source MPP` (0.4990) and AppMag `== 20`
   - thumbnail present with aspect ≈ `27836/25633` (within 1%)
   - `label` and `overview` associated images present
4. **Pixel parity:** decode several interior regions from both `out.svs` and the
   ImageScope crop (same crop-local coordinates) and compute mean/max abs diff
   per channel. Assert `mean <= ~1.0` and `max <= ~12` (one JPEG generation;
   the analysis measured ~0.2–0.6 mean / 8–10 max — allow headroom for a
   different libjpeg build/quality rounding). Use `hash --mode pixel` semantics
   or a direct decoded-region diff helper.

### Tile-aligned edge case (integration or smaller fixture)

Crop on a tile-aligned origin (`X,Y` multiples of 256) on
`CMU-1-Small-Region.svs`; assert output opens, dims exact, level count ≥ 1.
(No lossless assertion — v1 re-encodes regardless of alignment.)

### Regression guard

`downsample` integration tests must stay green after the `buildPyramid`
refactor (level counts + L0 dims unchanged for the CMU fixtures).

---

## Out of scope (v1)

- **Lossless modes B/C** (tile-snap / snap+edge-reencode) — future `--lossless`.
- **Non-SVS targets** (`--to tiff|ome-tiff|cog-wsi`) — the re-encode pipeline is
  format-generic, but the metadata recipe is SVS-specific; defer.
- **`--codec` override** — v1 always re-encodes as JPEG/RGB (matches the source
  and the oracle). `--quality` is exposed; codec selection is not.
- **Rewriting `Left`/`Top`** to the crop's physical origin — left stale, as
  Aperio does (the analysis shows OpenSlide/QuPath ignore them).
- **Annotation/coordinate round-trip back to the parent slide** — no SVS field
  the dominant consumers read carries it; would need a sidecar.

---

## Risks / open implementation questions (resolve during planning, not now)

1. **`buildPyramid` refactor changing `downsample` output.** The dimension-driven
   level-count policy must not alter `downsample`'s existing output level counts.
   If it would, keep `downsample`'s "one level per source level" behaviour by
   passing the desired level count into `buildPyramidFromRaster` (or keep two
   small loop callers). Decide in the plan after reading the current counts the
   integration tests assert.
2. **Streamwriter thumbnail-write API.** Confirm whether a freshly-encoded
   (non-passthrough) thumbnail can be written through the existing associated
   path or needs a new sibling helper (see §4 note).
3. **Memory.** Cropped L0 raster for the CMU-2 parity crop is
   `27836×25633×3 ≈ 2.1 GB`; box-halving adds a transient `~0.5 GB`. Within the
   existing `GOMEMLIMIT` envelope (`convert` peaks higher), but the plan should
   note it and rely on `MaterializeCroppedL0` being tile-streamed (not a
   whole-source decode).
