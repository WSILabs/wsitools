# Streaming retile engine — design

**Date:** 2026-06-18
**Status:** Approved design, ready for decomposition + implementation plans.

## Goal

One **streaming** decode→retile→encode engine that reads a source pyramid (any
tile geometry, any codec, possibly overlapping/stitched like BIF) and produces a
target pyramid with a different tile geometry (e.g. 240×240 → 512×512) and/or
codec, in a single pass that **never materializes L0 in RAM**. It generalizes the
existing DZI pyramid-descent generator so the *same* engine feeds every tiled
container writer (cog-wsi / svs / tiff / ome-tiff / bif / dzi / szi), not just
DZI/SZI.

## Relationship to the 2026-06-14 unified-transform design

This **revises the engine** of `docs/superpowers/specs/2026-06-14-unified-transform-convert-design.md`.
That design's CLI model, single-axis aliases, capability table, and `validate(spec)`
gating are **kept**. What changes: its **raster `materializeWorkingL0`** front-end
(decode the whole cropped/reduced L0 into one RGB buffer, then rebuild) is
**replaced** by the streaming descent below. The raster approach's ~18 GB L0 peak
(survey item C5) is the can that design left unbuilt; streaming retires it. The
2026-06-14 doc's "dzi/szi excluded from the raster pipeline" caveat goes away —
dzi/szi run on the *same* engine as everyone else.

## Two paths, one router

`validate(spec)` chooses exactly one path per the single test **"does this decode?"**:

| Path | Eligible when | Per output tile |
|---|---|---|
| **Verbatim tile-copy** (no decode) | same codec, same geometry, **non-overlapping** source, crop tile-aligned-or-absent, target carries the codec | byte-copy one source tile |
| **Streaming descent** (decode → derive → encode) | *anything that decodes* — re-codec (transcode), downsample, retile, lossy crop, **or an overlapping/stitched source** | bands → downsample chain → re-tile → encode |

The verbatim path is the only one that must **not** decode; it stays separate.
Everything else — including same-size transcode — goes through the one descent.
(Transcode through the descent is competitive-or-better than a per-tile re-encode:
it decodes **L0 only** (~1× L0) and box-reduces the rest, vs ~1.33× L0 decoded by
decoding every level; and the derived reduced levels carry one *fewer* lossy
generation, with downsampling attenuating L0's JPEG artifacts. The only thing a
per-tile pass preserves is the source's exact reduced-level *pixels* — a weak
requirement, since transcode already re-encodes. A future `--preserve-levels`
flag can re-route to a per-tile pass; not v1.)

## The streaming descent engine (the core deliverable)

A new format-agnostic `internal/retile` package, generalizing
`cmd/wsitools/convert_dzi_descent.go`'s `levelBuilder`:

```
opentile: ScaledStrips(cropRegion, scaledL0Size, bandHeight)   ← read+decode+stitch
  → descent: rolling band buffer per level
             box/lanczos downsample chain → ALL output levels from ONE L0 pass
             slice each band into target tiles (tileW×tileH ± overlap)
  → wsitools encode(rgb,w,h) → bytes        ← codec interface (parallel workers)
  → TileSink.WriteTile(level,col,row,bytes) ← per-container placement
```

- **Input:** a `RetileSpec{ source, cropRegion?, outL0Size, rebuild RebuildSpec,
  encoder Encoder, sink TileSink, kernel, jobs }`.
- **Crop** = `cropRegion` arg; **downsample** = `outL0Size < cropRegion`;
  **retile** = `rebuild.TileW×TileH`; **re-codec** = `encoder`. All compose in the
  single pass.
- **Memory:** peak ≈ `bandHeight × width × 3 × (a few buffers)` — hundreds of MB,
  flat as slides grow; no full-L0 raster.
- **Parallelism:** encode runs on a worker pool (as the DZI path does today);
  decode is sequential through `ScaledStrips` (decode-once by access order — no
  redundant decode, no LRU needed).

## The three seams (clean boundaries)

| Seam | Owner | Interface |
|---|---|---|
| **Read** | **opentile** | `Pyramid.ScaledStrips(src Region, out Size, stripHeight) *StripIterator` — exists; decodes + stitches + scales into RGB bands. **New:** a "tiles overlap / stitched" signal on `Level` (e.g. `Overlapping() bool`) **and** `Grid()`/`Size()` consistency, so the verbatim gate can bail correctly and the BIF Grid/Size break is fixed at the contract level. Read-side → filed as an opentile issue (SP1). |
| **Encode** | **wsitools** | `internal/codec.Encoder.EncodeTile(rgb []byte, w, h int, dst []byte) ([]byte, error)` — exists. The engine is codec-agnostic; `RetileSpec` carries the resolved encoder. **Stays in wsitools** (encode is a write concern with container-write policy; the codec cgo libs are shared system libraries, so no real duplication with opentile's decoders). |
| **Place** | **wsitools** | new `TileSink{ Begin(levels []LevelSpec) error; WriteTile(level, col, row int, encoded []byte) error; Close() error }` — one per container, each owning its tile order / classification / overlap / IFD-or-frame layout (the per-format concerns from the format-writer audit). |

`TileSink` implementations: `dzi` (refactor the existing sink), `cogwsi`,
`streamwriter` (svs/tiff/ome-tiff), `bif`. Each wraps the existing writer; the
engine never learns container specifics. The SVS thumbnail-at-IFD-1 rule, BIF
row-major order, cog-wsi WSIImageType tagging, OME-XML lockstep — all live behind
the sink, not in the engine.

## Level structure & transcode semantics

`RebuildSpec{ TileW, TileH, Overlap, LevelRatio, LevelCount }` drives the rebuild.
Defaults are **"match source"** so a plain transcode changes pixels (re-encode)
but **not the pyramid shape**: `LevelRatio`/`LevelCount` default to the source's
own level scales (derive output levels at the source's downsample factors), and
`TileW×TileH` default to the source's tile size. The user opts into a different
structure with `--tile-size` / `--level-ratio` / `--levels`. (This makes the
"derive levels" default safe — only the pixels change on transcode, which is
inherent to re-encoding, never the level count/ratios unless asked.)

Downsample kernel: default **box**, **lanczos3** available (`--kernel`), per the
completed DZI-kernel audit (libvips parity).

## What it subsumes / replaces

- **DZI/SZI convert** → engine + `dziTileSink` (the descent *becomes* the shared engine).
- **`convert --to {cog-wsi,svs,tiff,ome-tiff,bif}`** with re-encode / downsample / retile / stitched source.
- **`downsample --factor`** → retile, same tile size, scaled `outL0Size`.
- **lossy `crop`** → `ScaledStrips(cropRegion)`.
- **`transcode`** (revived) → same geometry, different encoder, match-source levels.
- **new `--tile-size`** (the retile axis) → target `TileW×TileH`.
- Deletes the raster `materializeWorkingL0` approach from the 2026-06-14 design.
- **Fixes** the BIF-source convert break (stitched → descent) and survey C5
  (no L0 in RAM) as falling-out.

## What stays separate / deferred

- **Verbatim lossless tile-copy** — separate no-decode fast path, gated on a
  non-overlapping source (the opentile signal) + matching codec/geometry + a
  carrying target.
- **DICOM** — its `derivedsource`→`dicomwriter` path works today and carries heavy
  WSM conformance (TILED_FULL frames, multi-instance Series, dciodvfy). A
  `dicomTileSink` (engine tiles → DICOM frames) is a natural future fit but
  **deferred**; v1 targets the TIFF-family + dzi/szi sinks. DICOM keeps its path.
- **Orientation** (rotate/flip) — the one axis awkward in pure streaming; a
  deferred pipeline slot that falls back to a raster path *only when invoked*, so
  the common case stays streaming.
- **`--preserve-levels`** (byte-faithful reduced levels via a per-tile pass) —
  deferred; not the default.

## Validation & errors

Reuse the 2026-06-14 **capability table + `validate(spec)`** verbatim — it is
path-agnostic and already classifies target exclusions, logical contradictions,
conformance tiers, and the lossless regime; here it additionally selects
verbatim-vs-descent via the source-overlap signal. Fail fast before any I/O;
atomic temp→rename per container; decode/encode/write errors propagate with
context. `--allow-nonconformant` downgrades a tier-(c) error to a warning.

## Testing

- **DZI parity** — DZI via the shared engine is **pixel-identical** to today's DZI
  descent (proves the generalization is faithful; the safest first milestone).
- **Engine ≡ old per-tile path** — for a non-stitched source, descent output is
  pixel-equivalent to the current per-tile re-encode (`hash --mode pixel`).
- **BIF-source convert** — now succeeds to every tiled target (the motivating bug;
  was "tile out of order").
- **Retile** — 240→512 (and 256→512) produce correct geometry and re-detect.
- **Transcode keeps level shape** — `transcode --codec X` yields the same level
  count/ratios as the source, only the codec differs.
- **Composition matrix** — {crop?, factor?, tile-size?, codec?, --to} cross-product
  re-detects with expected dims/MPP/mag.
- **Memory** — a large-slide convert stays bounded (doesn't OOM), guarding the
  streaming property.
- **Verbatim preserved** — same-codec same-geometry non-overlapping convert is a
  byte-identical tile-copy (no decode).

## Component summary

| Unit | Responsibility | Depends on |
|---|---|---|
| `runConvert` + alias wrappers | parse → `transformSpec` → `validate` → route (verbatim \| descent) → run | cobra, validate, retile engine |
| `validate(spec)` + capability table | conformance/contradiction/lossless gating + path selection | opentile overlap signal |
| `internal/retile` engine | streaming `ScaledStrips` → band buffers → downsample chain → re-tile → encode → sink | opentile `ScaledStrips`, `internal/codec`, `TileSink`, downsample kernels |
| `TileSink` + per-container sinks | place encoded tiles per container (order/classification/overlap) | streamwriter / cogwsiwriter / bifwriter / dzi |
| verbatim tile-copy path | byte-copy eligible source tiles | opentile overlap signal |

## Decomposition into sub-projects

Too large for one plan. Three independently shippable sub-projects:

- **SP1 — opentile (upstream, read-side):** the `Level.Overlapping()` signal +
  `Grid()`/`Size()` consistency. Small; it's the **unblock** for the verbatim gate
  and the BIF fix. Filed as an opentile issue; you implement upstream.
- **SP2 — wsitools, the engine:** `internal/retile` + `TileSink` + per-container
  sinks, generalizing the DZI descent. **Prove it by routing DZI/SZI through it
  first** (pixel-identical parity), then add cog-wsi/svs/tiff/bif sinks. Standalone
  value: fixes BIF-source convert + C5.
- **SP3 — wsitools, the convergence:** wire `convert`/`transcode`/`downsample`/
  `crop` onto the engine + `validate`, retire the raster path and the per-tile
  re-encode paths (this is where the unified-transform CLI lands). Builds on the
  2026-06-14 CLI/validation design.

Each sub-project gets its own spec → plan → implementation cycle. SP2 is the core;
SP1 unblocks its verbatim gate; SP3 lands the user-facing unification.

## Operational note — the pending opentile v0.47.0 bump

The v0.47.0 bump (BIF stitching) is on branch `chore/opentile-go-v0.47.0` and
**breaks convert-from-BIF** to tile-copy targets ("tile out of order") because the
per-tile path can't read overlapping tiles. Until SP2 lands, the bump needs one of:
(a) **hold** the bump; (b) **interim re-route** — send stitched/overlapping
sources through the existing DZI-style descent (or a thin pre-SP2 descent) for
tiled targets; (c) **hard-error** stitched-source → tile-copy-target with a clear
message and ship the bump (BIF *write* and non-BIF paths are unaffected). This is
a sequencing decision, resolved when SP1/SP2 are scheduled, independent of this
spec.

## Deferred / future (slots reserved)

- **Orientation** (`rotate`/`flip`) — raster-fallback slot, invoked-only.
- **`--preserve-levels`** — per-tile faithful-reduced-levels pass.
- **DICOM sink** — engine tiles → WSM frames.
- **Upstream conformance validator** from opentile-go — consume when available
  (per the 2026-06-14 design's forward-looking note).
