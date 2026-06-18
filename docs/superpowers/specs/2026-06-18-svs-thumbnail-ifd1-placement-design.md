# SVS thumbnail IFD-1 placement — design

**Date:** 2026-06-18
**Status:** approved (brainstorm)

## Problem

opentile classifies the SVS thumbnail **positionally**: per `formats/svs/series.go`,
*"Page 1 is the Thumbnail iff non-tiled."* The thumbnail must be the second IFD
(index 1), between L0 and the first reduced pyramid level — the canonical Aperio
layout `L0, thumbnail, L1…Ln, label, macro/overview`.

The wsitools streamwriter emits IFDs in `AddLevel`/`AddStripped` **call order**
(`w.imgs` append order), and `writeAssociatedImages` appends every associated
image **after all pyramid levels**. On a **single-level** SVS this is harmless —
the thumbnail still lands at IFD 1 because there are no reduced levels after L0.
On a **multi-level** SVS the tiled levels occupy pages 1..N and the thumbnail is
stranded at the tail, where opentile cannot classify it. The overview is lost as
collateral damage (three trailing non-tiled pages confuse the "second trailing
page" label/macro heuristic).

### Observed impact (multi-level SVS)

- `convert --to svs` (both tile-copy and `--codec` re-encode paths) **silently
  drops the thumbnail and overview**. Verified on `239551.svs`: source exposes
  thumbnail+label+overview; output exposes only `label`.
- `associated thumbnail replace`/`remove` on a multi-level SVS cannot be served
  by the rebuild fallback, because the rebuild (which goes through the same
  writer) re-strands the thumbnail. (This is the deferred C4 #2 parity item.)

This is uncaught by CI because the only SVS fixtures in CI are single-level
(`CMU-1-Small-Region.svs`, the `590_crop` exports), where tail-placement happens
to work.

## Goal

Make the SVS write path emit the thumbnail at IFD 1 on multi-level slides. This:

1. Fixes `convert --to svs` of a multi-level slide (thumbnail + overview
   preserved), for both the tile-copy and `--codec` re-encode paths.
2. Unblocks `associated thumbnail replace`/`remove` on multi-level SVS via the
   rebuild fallback (closes C4 #2).

## Non-goals

- No change to generic-TIFF (its `WSIImageType` private tag is
  position-independent; tail-placement is fine) or OME-TIFF (IFD order is tied to
  `<Image>` positions in the synthetic OME-XML — interleaving would desync).
- No streamwriter changes. The fix lives entirely in `cmd/wsitools/convert_tiff.go`
  plus re-adding the SVS rebuild wrapper.
- Byte-for-byte identity with a real Aperio file is not a goal; pixel-identity of
  the pyramid (verbatim tile-copy) and correct associated classification are.

## Approach

IFD order equals `w.imgs` append order (`AddLevel` at `levelhandle.go:99`,
`AddStripped` at `stripped.go:76` both append to the same slice; `Close →
closeFlatLayout` emits in that order). The pyramid-index computation
(`writer.go:209-223`) counts only entries with `levelSpec != nil &&
WSIImageType == WSIImageTypePyramid`, so interleaving a stripped (associated)
entry between levels does **not** disturb pyramid numbering. SVS uses the flat
layout (`subResPyramid` is off). Therefore the thumbnail can be emitted between
L0 and L1 with no writer change.

### Components — `cmd/wsitools/convert_tiff.go`

1. **`emitOneAssociated`** — extracted from the per-image body of
   `writeAssociatedImages`:

   ```
   emitOneAssociated(src, w, a, container, omeSynthetic, plan) (emitted bool, err error)
   ```

   Encapsulates: the plan decision for image `a` (skip on `plan.remove`;
   substitute `plan.spec` on `plan.replace`; otherwise `faithfulStrippedSpec(a)`),
   the SVS/OME `NewSubfileType`/`ExtraTags`/`WSIImageType` stamping, the
   `errSkipAssociated` warn-and-skip, and the `AddStripped` call. Returns whether
   an IFD was emitted (for the upsert bookkeeping `writeAssociatedImages` already
   does). `writeAssociatedImages` is rewritten to loop over `src.Associated()`
   calling `emitOneAssociated`, preserving its current behavior exactly for
   non-SVS and single-level SVS.

2. **Thumbnail-at-IFD-1 injection** in both level loops. After L0 is fully
   drained (`CloseInput` + `<-drainErr`), if `container == "svs"`, emit the
   thumbnail via `emitOneAssociated` for the `src.Associated()` entry of type
   `"thumbnail"` (respecting the plan):

   - `writeTIFFTileCopy` (tile-copy path; backs `convert --to svs` tile-copy and
     `rebuildSVS`).
   - `transcodePyramid` (re-encode path; backs `convert --to svs --codec`).

   A shared helper `emitSVSThumbnailAtL0(src, w, lvlIndex, container, omeSynthetic, plan)`
   does the no-op guard (`container=="svs" && lvlIndex==0`), finds the thumbnail
   in `src.Associated()` (or the `plan.replace=="thumbnail"` upsert spec), and
   calls `emitOneAssociated`. Returns whether it emitted, recorded so the tail
   pass skips it.

3. **Tail-pass skip.** `writeAssociatedImages` skips `a.Type()=="thumbnail"` when
   `container=="svs"` (it was already emitted at IFD 1, or intentionally
   dropped). The skip is unconditional for SVS thumbnail: on a single-level SVS
   the IFD-1 injection still runs after L0 (there is no L1), so the thumbnail is
   emitted exactly once either way. The existing **upsert block** (`if
   plan.replace != "" && !replaced …`) must likewise not emit a thumbnail when
   `container=="svs"` — the IFD-1 injection owns the SVS thumbnail upsert. Concretely:
   `emitSVSThumbnailAtL0` returns whether it emitted, and that result seeds the
   `replaced` flag for a `plan.replace=="thumbnail"` so the tail upsert is a no-op.

### Plan handling at IFD 1

| Plan | IFD-1 thumbnail |
|---|---|
| `dropAll` (`--no-associated`) | nothing |
| `remove == "thumbnail"` | nothing |
| `replace == "thumbnail"` | `plan.spec` (the new image), incl. upsert when absent in source |
| none / other | `faithfulStrippedSpec(thumbnail)` (verbatim copy) |

### Rebuild wiring — `cmd/wsitools/associated.go` + `associated_rebuild_svs.go`

Re-add `rebuildSVS` (mirrors `rebuildGenericTIFF` via the shared `baseRebuildOpts`
+ `finalizeRebuild` helpers, with `container="svs"`, `ImageDepth=1`,
`YCbCrSubSampling` from the L0 JPEG tile, and the Aperio L0 description from
`src.SourceImageDescription()`). Wire the SVS `edit.ErrUnexpectedLayout` branch in
both `runAssociatedRemoveFor` and `runAssociatedReplaceFor` to `rebuildSVS`
instead of returning `ErrUnsupportedAssoc`. Because the shared writer now places
the thumbnail at IFD 1, the rebuilt thumbnail is classified correctly.

## Data flow

```
convert --to svs (tile-copy):
  writeTIFFTileCopy
    AddLevel(L0) + drain
    [svs] emitSVSThumbnailAtL0 -> emitOneAssociated(thumbnail)   <-- IFD 1
    AddLevel(L1..Ln) + drain
    writeAssociatedImages -> emitOneAssociated(label, macro, overview)  [skips thumbnail for svs]

convert --to svs --codec:
  transcodePyramid
    (same: emit thumbnail after L0)
  writeAssociatedImages (skips svs thumbnail)

associated thumbnail replace/remove (multi-level svs):
  splice -> ErrUnexpectedLayout -> rebuildSVS -> finalizeRebuild -> writeTIFFTileCopy (plan)
```

## Error handling

- Unchanged atomic temp→rename in `finalizeRebuild` and the convert writers.
- A thumbnail whose bytes/spec can't be built still surfaces `errSkipAssociated`
  → `slog.Warn` + skip (no IFD), exactly as today; the pyramid still writes.
- `rebuildSVS` aborts the writer and removes the temp on any error.

## Testing

**Fixture:** add `239551.svs` (owner's own scan; CC0/CC-BY in `wsi-fixtures`) to
the `svs` fixture set + `.github/fixtures.sha256`. 25 MB, JPEG-tiled, 3 pyramid
levels, thumbnail+label+overview — closes debt item D3 ("no multi-level SVS
fixture"). CI pulls it via the existing `svs.tar`.

**Integration tests** (gated on the multi-level fixture; skip-if-absent):

- `convert --to svs` of the multi-level slide → output exposes thumbnail +
  label + overview (regression: currently only label); pyramid pixel-identical
  (`hash --mode pixel`).
- `convert --to svs --codec jpeg` of the multi-level slide → same associated
  preservation.
- `associated thumbnail replace` on the multi-level slide → succeeds; reading the
  output through opentile returns the replacement thumbnail (not the original);
  pyramid pixel-identical; label/overview intact.
- `associated thumbnail remove` on the multi-level slide → succeeds; thumbnail
  gone; label/overview intact.

**Unit test** (no fixture; runs in CI regardless): a minimal in-memory fake
`source.Source` with two tiled levels + a `"thumbnail"` associated image →
`writeTIFFTileCopy(container="svs")` into a temp file → re-parse the IFD chain
(via `internal/tiff/edit.Parse` or `dump-ifds`) and assert: IFD 1 is the
non-tiled thumbnail, IFDs 0/2/… are the tiled pyramid levels in order.

**Existing single-level coverage** (`TestSVSOverviewReplaceWorks`,
`TestSVSThumbnailReplaceSingleLevel`, the convert single-level tests) must stay
green — the IFD-1 injection is a no-op there (no L1 follows).

## Risks

- **Both convert paths must be covered.** Missing `transcodePyramid` would leave
  `--codec` re-encode broken. The integration test for `--codec jpeg` guards
  this.
- **OME/generic-TIFF must be untouched.** The injection is guarded on
  `container=="svs"`; the OME-XML `<Image>`↔IFD lockstep in `omeAssociatedSpecs`
  is unaffected because the thumbnail skip is SVS-only.
- **Upsert ordering.** A `replace thumbnail` on a slide with no existing
  thumbnail must still land at IFD 1, not the tail. Covered by the IFD-1
  injection consulting `plan.replace` directly.
