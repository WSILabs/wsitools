# `convert --factor` scaling (TIFF-family) + `downsample` as alias — design

> Status: **approved design** (brainstormed 2026-06-05). Next: writing-plans.
> Branch off `main`; never implement on `main`.

## Goal

Bring downsampling/scaling to `convert` so any TIFF-family target can be emitted
at a reduced resolution with correctly-scaled metadata, and reimplement the
standalone `downsample` command as a thin alias over the same engine.

## Background

`convert` and `downsample` use different pyramid models:
- **`convert` (today)** — *preserve-levels*: emits output level k from source
  level k at the same dimensions (`Scale: 1`, no reduction).
- **`downsample`** — *reduce-then-rebuild*: reduces source L0 to L0/N (the
  codec-agnostic `decodeReducedTile`: try `Scale: factor`, else full-decode +
  box), then builds a fresh pyramid; SVS-source → SVS-out only.

`convert --factor` = route the reduce-then-rebuild model through `convert`'s
per-target writers (with per-format metadata scaling). The reduction core is
shared so `downsample` becomes a thin alias rather than duplicated logic. This
is the roadmap's "unify downsample into convert --factor" item, TIFF-family
first; dzi/szi deferred.

## Scope (decided)

- Targets: **`svs`, `tiff`, `ome-tiff`, `cog-wsi`**. `dzi`/`szi` deferred
  (they're already pyramids; base-reduction is a separate wrinkle).
- **`downsample` is kept** as a thin alias for `convert --to svs --factor N`
  (NOT removed). Behavior unchanged → non-breaking.

## CLI surface

- `convert --factor N` where `N ∈ {2,4,8,16}` (matches the codec scaled-decode
  set), and `convert --target-mag M` (derive N from source AppMag, `N =
  round(AppMag/M)`, must be a valid power-of-2 — absorbed from `downsample`).
- `--factor`/`--target-mag` valid only with `--to {svs,tiff,ome-tiff,cog-wsi}`.
  With `dzi`/`szi` → error: `--factor not supported for --to dzi|szi (yet)`.
- `--factor 1` or absent → today's preserve-levels behavior, unchanged.
- `--factor > 1` **forces re-encode** (a resized base can't be tile-copied):
  when `--codec` is absent it defaults to `jpeg`. (Tile-copy + `--factor` is not
  a valid combination; document it.)
- `downsample` keeps its current flags (`--factor`, `--target-mag`, `--quality`,
  `--jobs`, `--tile-order`, `--max-memory`) and UX; its `RunE` resolves them to
  the shared engine with target = svs. SVS-source → SVS-out, exactly as today.

## Reduction model

With `--factor`, output L0 = `ceil(srcL0 / N)` via the codec-agnostic
`decodeReducedTile` (per source tile: try `Scale: factor`; on
`ErrUnsupportedScale` full-decode + `downsampleByPowerOf2` box). A fresh pyramid
is then built from that base by the box cascade. Output tile dimensions come
from the actual reduced raster (`ceil(src/N)`), consistent across the scaled and
box-fallback paths.

## Metadata scaling (per target)

Scale once at the boundary: `MPP ×N`, `Magnification ÷N`. These feed the
resolution tags (282/283) + WSI MPP/mag private tags for **all** targets. Plus
format-specific embedded descriptions:
- **svs** — Aperio `ImageDescription` via the existing `MutateForDownsample`
  (AppMag ÷N, MPP ×N, geometry + OriginalWidth/Height to the reduced dims).
- **ome-tiff** — OME-XML `PhysicalSizeX`/`PhysicalSizeY` ×N (and the L0
  dimensions in the OME-XML reflect the reduced size).
- **tiff / cog-wsi** — scaled `md` only (no embedded description to rewrite).

MPP is symmetric (Aperio convention); per-axis MPPX≠MPPY scaling is out of scope
(noted in roadmap).

## Code organization

- New **`internal/downscale`** package holds the reduction + cascade extracted
  from `cmd/wsitools/downsample.go`: `decodeReducedTile`, `downsampleByPowerOf2`,
  the materialize-output-L0 logic, and the metadata-scaling helpers. Pure Go;
  one clear responsibility (reduce a source to a target factor + emit levels).
- `cmd/wsitools/convert_tiff.go` (`runConvertTIFF`) gains a `factor` path: when
  `factor > 1`, materialize the reduced L0, scale metadata, build the pyramid via
  `internal/downscale`, and write each level through the existing target writer
  (streamwriter for svs/tiff/ome-tiff; cogwsiwriter for cog-wsi). `factor == 1`
  keeps the current preserve-levels path.
- `cmd/wsitools/downsample.go` becomes a thin command: parse its flags →
  delegate to the shared engine with target = svs. Duplicate reduction code
  removed.
- Associated images: carried verbatim (not pyramid-scaled), as both commands do
  today.

## Error handling

- `--factor` with an unsupported target (dzi/szi) → clear error.
- Invalid factor (not in {2,4,8,16}) / `--target-mag` that doesn't resolve to a
  valid power-of-2 → clear error (same messages as today's `downsample`).
- Reduced output degenerate (factor too large for the slide) → clear error
  (preserve `downsample`'s existing check).

## Testing (TDD)

- **Parity:** `convert --to svs --factor N` produces **pixel-equivalent** output
  (`hash --mode pixel`, not file SHA — the tile writer is nondeterministic) to
  today's `downsample --factor N` on an SVS fixture; the `downsample` alias must
  not regress SVS downsampling.
- **New targets:** `convert --to {tiff,ome-tiff,cog-wsi} --factor 4` → assert
  output L0 dims = `ceil(src/4)`, reads back, and **MPP ×4 / magnification ÷4**
  are present (info shows scaled values; for ome-tiff assert OME-XML
  PhysicalSize scaled).
- **`--target-mag`** resolves to the right factor.
- **Errors:** `--factor` with `--to dzi` rejected; bad factor rejected.
- Heavy `-race` suites run uncontended / `-timeout 30m` (per CLAUDE.md).

## Out of scope

- `dzi`/`szi --factor` (follow-up: base-reduced DeepZoom).
- Per-axis (MPPX≠MPPY) scaling.
- The constant-memory dzi/szi cascade (separate roadmap item).

## Open questions (resolved during brainstorming)

- *Scope?* → TIFF-family (svs/tiff/ome-tiff/cog-wsi) first; dzi/szi deferred.
- *downsample fate?* → kept as a thin alias for `convert --to svs --factor`
  (non-breaking); shared `internal/downscale` engine.
- *Reduction source?* → resample from source L0 (any power-of-2 N), reusing the
  codec-agnostic `decodeReducedTile`.
- *Tile-copy + factor?* → invalid; `--factor` forces re-encode (default jpeg).
