# Format-preserving `crop` (Phase 2a: re-encode for the TIFF family) — design spec

**Date:** 2026-06-13
**Status:** approved (brainstorming) — ready for implementation plan
**Builds on:** shipped SVS crop (`crop` re-encode + `crop --lossless`) and the
`downsample` per-format dispatch.
**Specs of record:** `2026-06-13-svs-crop-design.md`, `…-lossless-design.md`.

---

## Goal

Make `crop` **format-preserving**: crop any TIFF-family WSI and write the result
back to its **own container** (SVS→SVS, OME-TIFF→OME-TIFF, generic-TIFF→generic-TIFF,
cog-wsi→cog-wsi), exactly the way `downsample` is format-preserving. **Phase 2a
scope: the RE-ENCODE mode only** for the three new TIFF formats. The shipped SVS
paths (both modes) are unchanged.

This is the natural extension: crop's re-encode pipeline (decode region → rebuild
pyramid) is structurally identical to `downsample` (decode reduced → rebuild
pyramid), and `downsample` already has working per-format writers
(`downsampleTo{SVS,TIFF,COGWSI,OMETIFF}`) behind `dispatchDownsampleByTarget`.
Crop reuses that machinery, swapping "materialize **reduced** L0" for
"materialize **cropped** L0."

## Decisions locked in brainstorming

- **Format-preserving, no `--to` flag.** `crop` detects the source container and
  writes the same one. Cross-format conversion stays `convert`'s job.
- **Re-encode only in 2a.** Lossless-verbatim for non-SVS is **Phase 2b**
  (tiff/ome-tiff can reuse `writeLosslessL0` on the shared `streamwriter`;
  cog-wsi needs a `cogwsiwriter` verbatim path). `--lossless` continues to
  require SVS and errors clearly on a non-SVS source until 2b.
- **Associated images: pass through** (matching each format's `downsample`
  writer) in 2a. **MUST-ADDRESS follow-up:** a passed-through thumbnail still
  renders the *whole slide*, which is wrong for a crop (the exact staleness we
  fixed for SVS via `regenCropThumbnail`). This is a tracked limitation to fix,
  NOT a permanent design choice — see [Must-address follow-ups](#must-address-follow-ups).

---

## CLI

No new flags. Existing `crop` flags apply. Behaviour by source format:

| Source format | `crop` (re-encode) | `crop --lossless` |
|---|---|---|
| SVS | ✅ (shipped) | ✅ (shipped) |
| generic-TIFF | ✅ **2a** | ⛔ until 2b (clear error) |
| OME-TIFF | ✅ **2a** | ⛔ until 2b |
| cog-wsi | ✅ **2a** | ⛔ until 2b |
| other (NDPI, SCN, DICOM, …) | ⛔ clear "unsupported source format" error | ⛔ |

`crop --lossless <non-svs>` must fail fast with a message like
`--lossless currently supports SVS sources only (other containers: Phase 2b)`.

---

## Architecture

Mirror `downsample`'s dispatch. Split `runCrop` into a **format-agnostic
front-end** + a **per-format dispatch**.

### Front-end (`runCrop`, format-agnostic)

1. Open source; resolve the target via `downsampleTargetForFormat(src.Format())`
   (existing helper: svs/ome-tiff/tiff/cog-wsi). Unknown → error.
2. Validate the rect against L0 bounds (`validateCropBounds`, existing).
3. Resolve quality/workers; path/force checks (existing).
4. If `--lossless` and target != svs → the "Phase 2b" error.
5. **Materialize the cropped L0 raster once** (`MaterializeCroppedL0` — already
   format-agnostic; reads any `*opentile.Level` via the codec registry).
6. Dispatch to the per-format emitter with the in-memory cropped L0.

> Re-encode crop is **exact-extent**: the output L0 dims equal the requested
> `w×h` (no tile-snap — snapping is a lossless-only concern). So the front-end
> uses the requested rect directly (the `ex,ey,ew,eh` effective-rect machinery
> only snaps in the SVS lossless branch).

### Per-format dispatch (`dispatchCropByTarget`, mirrors `dispatchDownsampleByTarget`)

```
target := downsampleTargetForFormat(src.Format())
switch target {
case "svs":      → existing SVS crop emission (BuildCropImageDescription + thumbnail regen + label/macro)
case "tiff":     → cropToTIFF
case "ome-tiff": → cropToOMETIFF
case "cog-wsi":  → cropToCOGWSI
}
```

Each emitter receives the opened `src`, the cropped L0 raster + dims, the rect,
quality/workers/order/bigtiff/force/noAssociated, and builds the output container.

### Per-format emitters (each ≈ its `downsampleTo{Format}` with cropped L0)

- **cropToTIFF** — `streamwriter.Create({FormatName:"tiff", ImageDescription:
  wsi-tools provenance, MPPX/Y, Magnification, ICC})`, then
  `buildPyramidFromRaster(croppedL0, ew, eh, nLevels, …, nil)`, then associated
  passthrough via `writeOneAssociated` loop, then `Close`. Identical to
  `downsampleToTIFF` except: L0 = cropped (not reduced); MPP/mag **preserved**
  (crop keeps resolution — no `*factor` scaling); `nLevels` from
  `cropPyramidLevels(ew, eh, outputTileSize)`.
- **cropToOMETIFF** — same as `cropToTIFF` but with OME-TIFF options: OME-XML
  `ImageDescription` (via `ome_imagedesc` / the OME associated enumeration) with
  the **cropped** dims, SubIFD pyramid layout. MPP/mag preserved.
- **cropToCOGWSI** — `cogwsiwriter.Create({Metadata:…})`, then a **from-raster**
  pyramid build (see below), then associated, then `Close`. MPP/mag preserved.

### Required helper extraction: `buildPyramidFromRasterCOGWSI`

`buildPyramidCOGWSI(ctx, src, w, factor, …)` today materializes the reduced L0
internally then loops `encodeAndWriteLevelCOGWSI` + the inter-level halve.
Extract the in-memory-raster loop into:

```go
func buildPyramidFromRasterCOGWSI(ctx context.Context, w *cogwsiwriter.Writer, l0 []byte, l0W, l0H, nLevels, quality int) error
```

mirroring the `buildPyramidFromRaster` extraction (Phase-1/lossless work) — and
reuse the shared `halveRaster` helper for the inter-level reduction.
`buildPyramidCOGWSI` becomes: materialize reduced L0 → call the from-raster
builder. **Behaviour-preserving for `downsample` — guarded by the downsample
integration regression test.** `cropToCOGWSI` calls the from-raster builder with
the cropped L0 and `cropPyramidLevels`-derived level count.

### Reuse strategy (the main implementation decision — resolve in the plan)

The three streamwriter formats (svs/tiff/ome-tiff) share the shape
"cropped L0 → `streamwriter.Create(format opts)` → `buildPyramidFromRaster` →
associated → close." Two ways to realize it, for the plan/architect to choose:

- **(A) Shared streamwriter-crop core** parameterized by `streamwriter.Options`
  builder + a metadata/associated policy; svs/tiff/ome-tiff become thin wrappers.
  DRY-est; the SVS thumbnail-interleave (postL0Hook) is the policy that differs.
- **(B) Parallel `cropTo{Format}` emitters** mirroring the `downsampleTo{Format}`
  bodies. More duplication, zero risk to shipped paths.

Recommendation: lean (A) for tiff/ome-tiff (they're nearly identical and have no
thumbnail interleave), keep the SVS emitter as-is, cog-wsi separate. The plan
settles it; any refactor of shared helpers is guarded by the `downsample`
integration tests.

---

## Metadata (exact-extent crop preserves resolution)

- **MPP + magnification preserved unchanged** (crop is a spatial subset at full
  res). Each format's existing metadata path already carries these; crop passes
  them through without the `*factor`/`/factor` scaling that `downsample` applies.
- **tiff:** wsi-tools provenance `ImageDescription` (the generictiff reader reads
  `mpp=`/`mag=` from it). A crop note is optional, not required.
- **ome-tiff:** OME-XML with the cropped pixel dimensions; MPP/mag preserved.
- **cog-wsi:** cog-wsi `Metadata` with MPP/mag preserved.
- No Aperio geometry token for non-SVS (those formats don't carry one).

---

## Associated images

Pass through via each format's existing mechanism (the `writeOneAssociated`
loop for streamwriter formats; the cog-wsi associated path). `--no-associated`
skips them. **The stale-thumbnail problem is documented as a must-address
follow-up** (below), not silently shipped as final.

---

## Testing (local-only — large fixtures, not in CI)

Extend `tests/integration/crop_test.go` with a re-encode, format-preserving
matrix over the available fixtures:

| Source | Fixture | Notes |
|---|---|---|
| generic-TIFF | `CMU-1.tiff` | 240px tiles |
| OME-TIFF | `Leica-1.ome.tiff` | 512px tiles |
| cog-wsi | `CMU-1_cog-wsi.tiff` | 256px tiles |

For each (skip-if-missing): run `crop --rect <in-bounds> -o out.<ext> <src>`,
re-open with opentile, and assert:
- `Format()` **preserved** (== source format),
- output L0 dims == the **exact requested** `w×h`,
- MPP/Magnification preserved (== source),
- ≥1 level; associated present (count matches, where the source had any).

Also: a unit/CLI test that `crop --lossless <non-svs-fixture>` fails with the
Phase-2b error. And the regression guard: `downsample` integration tests stay
green after the `buildPyramidCOGWSI` extraction.

---

## Out of scope (2a)

- **Lossless-verbatim for non-SVS → Phase 2b.**
- `--to` cross-format crop (stays `convert`'s domain).
- Non-TIFF containers (NDPI has no writer; DICOM crop is a separate effort).
- Per-format thumbnail regeneration (see must-address).

## Must-address follow-ups

1. **Stale thumbnail for non-SVS crops (MUST FIX).** Passing the source thumbnail
   through means a cropped non-SVS file shows a whole-slide preview. SVS already
   regenerates it (`regenCropThumbnail`). The fix is per-format thumbnail
   regeneration from the decoded cropped L0 (each writer emits associated
   differently). Scheduled for Phase 2b or a dedicated follow-up; tracked so it
   is not forgotten.
2. **Phase 2b: lossless-verbatim per format** (tiff/ome-tiff via `writeLosslessL0`;
   cog-wsi via a new `cogwsiwriter` verbatim path).

## Risks / open items (resolve in the plan)

1. **Reuse strategy (A vs B)** — settle shared-core vs parallel emitters for the
   streamwriter formats.
2. **`buildPyramidCOGWSI` extraction must not change `downsample` cog-wsi output**
   — guard with the downsample integration regression test.
3. **OME-TIFF SubIFD layout from a cropped L0** — confirm `downsampleToOMETIFF`'s
   SubIFD/OME-XML path works unchanged when fed a cropped (vs reduced) L0; the
   only differences are dims + (preserved) MPP/mag.
4. **Novel-codec generic-TIFF sources** (avif/jxl/htj2k `*-out.tiff`) — re-encode
   requires decoding those tiles; confirm the decoder is registered, else the
   crop fails with a clear "no decoder" error (acceptable — out of the core test
   matrix; note it).
