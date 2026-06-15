# Unified-transform `convert` — design

**Date:** 2026-06-14
**Status:** Approved design, ready for implementation plan.

## Goal

Make `convert` a **single-pass transform pipeline** that can compose container
change, codec change, crop, and downsample in **one decode→transform→encode
pass**, while keeping `crop` / `downsample` / `transcode` as simple
**single-axis convenience aliases**. The motivating property: chaining the
verbs (`crop | downsample | transcode`) decodes and re-encodes the pixels once
per stage — N× the time and N× the generational quality loss — whereas the
unified path decodes the cropped region once and rebuilds the pyramid once.

## Mental model: one axis per verb, `convert` is the union

Every WSI transform moves the image along one of a few **axes**. Each axis gets
a focused verb; `convert` is the swiss-army union (and also covers the plain
container case, ImageMagick-style — `convert`/`magick` is the established name
for the do-everything imaging tool).

| Axis | Single-op alias | Headline flag(s) | Status |
|---|---|---|---|
| space | `crop` | `--rect X,Y,W,H` | v1 |
| resolution | `downsample` | `--factor N` / `--target-mag M` | v1 |
| codec | `transcode` (revived) | `--codec C` / `--quality Q` | v1 |
| container + **all** | `convert` | `--to FMT` + the above | v1 |
| orientation | `rotate` / `flip` | `--rotate` / `--flip` | **deferred** (slotted) |
| pyramid structure | `retile` | `--tile-size` / `--level-ratio` / `--levels` | **deferred** (slotted) |

`transcode` re-encodes tiles to a different codec **in the same container** with
the **same geometry** (e.g. `transcode --codec avif slide.tiff`). It was folded
into `convert` historically only because `convert` was then the sole transform
verb; in this model it earns its own focused identity again.

## Scope

**In scope (v1) — unify what already exists:**
- `convert` composes crop + downsample + codec + container in one pass; `--to`
  is optional (omitted ⇒ output container = source format).
- `crop`, `downsample` survive as single-axis aliases (backward-compatible);
  `transcode` is added as the codec-axis alias.
- The lossless tile-copy fast path is preserved and generalized.
- A conformance + combo **validation layer** gates invalid combinations.

**Deferred but slotted (designed-in, fixed defaults in v1):**
- **Orientation** (`rotate`/`flip`): a no-op stage in the pipeline; net-new
  raster-rotation capability + geometry/MPP-axis handling lands later.
- **Pyramid structure** (`--tile-size` / `--level-ratio` / `--levels`, and a
  future `retile` alias): the rebuild stage takes a `RebuildSpec{tileSize,
  levelRatio, levelCount}`; v1 ships fixed defaults (256 / 2× octave / derived
  level count). Seams already exist (`outputTileSize` is one constant;
  `BoxHalve` already takes an arbitrary factor; level count is a computed int).

**Out of scope (not slotted in v1):** de-identification (label/PHI strip during
convert), metadata-policy flags (ICC/vendor preserve/strip), bit-depth /
colorspace conversion (16→8, RGB→grayscale), multi-dimensional (Z/C) selection.

## CLI model

**`convert` — the unified one-pass transform:**
```
convert [--to FMT] [--rect X,Y,W,H] [--factor N | --target-mag M] \
        [--codec C [--quality Q]] [--lossless] [--allow-nonconformant] \
        [-o OUT] [-f] [--jobs N] [--no-associated] [--tile-order …] [--bigtiff …] INPUT
```
- `--to` optional ⇒ source format (format-preserving falls out for free).
- Any subset of the axes (crop, downsample, codec, container) composes in one
  pass. Headline: `convert --to dicom --rect 0,0,8192,8192 --factor 2 -o out in`.
- Bare `convert in out` (same format, no transform) = lossless tile-copy
  passthrough (a normalize/validate copy).

**Single-axis aliases — thin desugar, format-preserving, no `--to`:**

| Alias | desugars to | exposes |
|---|---|---|
| `crop --rect X,Y,W,H [--lossless]` | `convert --rect …` | space axis + universal flags |
| `downsample --factor N \| --target-mag M` | `convert --factor …` | resolution axis + universal flags |
| `transcode --codec C [--quality Q]` | `convert --codec …` | codec axis + universal flags |

Aliases surface **only their axis flag + universal flags** (`-o`, `-f`,
`--quality`, `--jobs`, `--no-associated`, `--tile-order`, `--bigtiff`). They
deliberately have **no `--to`** and no other-axis flags — that is what keeps them
"one axis only." To combine axes you reach for `convert`. The alias and `convert`
build the *same* internal spec and call the *same* dispatch, so they are provably
one code path.

Two universal flags are conditional on the axis: **`--lossless`** appears only on
`crop` (the sole alias whose op can be verbatim), and **`--allow-nonconformant`**
appears only on `convert` and `transcode` (the codec/container-changing verbs —
`crop`/`downsample` are format-preserving same-codec, hence always conformant and
never tier-(c)).

`crop` keeps `--x/--y/--w/--h` as alternates to `--rect` (both exist today).

## Pipeline & engine

**Fixed-order pipeline (every command is a subset):**
```
read(any format)
  → [crop]        rect @ L0 coords      → downscale.MaterializeCroppedL0
  → [orient]      rotate/flip            → DEFERRED slot (no-op in v1)
  → [downsample]  factor / target-mag    → downscale box-reduce
  → re-encode     codec + quality + RebuildSpec (slots; fixed in v1)
  → container     --to → per-format emitter
  → write
```
Crop and downsample both operate on the decoded L0 raster, which is why they
fuse: decode the cropped region once, apply crop→downsample to the working
raster, rebuild once.

**Components (clear boundaries):**

1. **`materializeWorkingL0(source, rect?, factor?) → (raster, dims, scaledMeta)`**
   — the shared front-end that *replaces* the two separate front-ends used today
   (`MaterializeCroppedL0` for crop, `MaterializeReducedL0` for downsample). It
   crops the L0 region, then box-reduces by `factor`, and computes combined
   metadata: **crop preserves resolution; downsample scales MPP ×factor / mag
   ÷factor**; output L0 dims = `(cropW/factor)×(cropH/factor)`. The orient slot
   sits here (no-op in v1). Lives in `internal/downscale`.

2. **`transformTo{SVS,TIFF,OMETIFF,COGWSI,DICOM}(spec)`** — the **converged
   emitters**: today's `downsampleTo*` (`convert_factor.go`) + `cropTo*`
   (`crop_formats.go`) pairs **merge into one per-format function** taking a
   `transformSpec{workingL0, dims, meta, codec, quality, rebuildSpec, lossless,
   losslessSource?, target, …}`. They rebuild the pyramid via the existing
   machinery (`buildPyramidFromRaster` for streamwriter formats;
   `internal/derivedsource` → `dicomwriter.WritePyramid` for DICOM). New module,
   e.g. `cmd/wsitools/transform.go` + per-format `transformTo*`.

3. **Lossless branch** — when eligible the emitter copies *source* tiles
   verbatim instead of using the working raster, so the spec carries an optional
   source-level handle (already the shape of `cropEmitParams`).

4. **Dispatch** — `runConvert` parses flags → builds `transformSpec` →
   `validate(spec)` → calls `transformTo<target>`. The `crop`/`downsample`/
   `transcode` aliases build the same spec (their one axis + `--to`=source) and
   call the same dispatch.

**One-pass guarantee:** `convert --rect --factor --codec --to` does exactly one
decode (cropped L0 region) + one encode (output pyramid) — no temp files, no
generational re-encoding.

**Out of the raster pipeline:** `dzi`/`szi` stay on their pyramid-descent
generator (excluded from `--rect`/`--factor`, as today). `convert --to dzi`
routes there unchanged; `crop`/`downsample`/`transcode` aliases do not target
dzi/szi.

**Refactor footprint:** `convert_factor.go` + `crop_formats.go` converge into
the transform module; the alias commands become thin wrappers. Well-tested code
— guarded by the existing integration suite plus new composition tests.

## Lossless regime

`--lossless` (verbatim tile-copy) is eligible **only** when *all* hold: no
downsample; no codec change (`--codec` absent or = source); no re-tile (trivially
true in v1); crop is tile-aligned (or absent); and the target container can carry
the source codec verbatim. So lossless = *{tile-aligned crop and/or re-container,
same codec, no resample/re-tile}*.

**Three behaviors (generalizing today's semantics):**
1. **Auto-lossless passthrough** — a pure re-container with no spatial/codec
   change (`convert --to tiff slide.svs`) does a verbatim tile-copy by default.
2. **`--lossless` (explicit)** — forces verbatim and **snaps a crop rect to the
   source tile grid** (output is a tile-aligned superset, with the existing
   printed notice). Combined with any disqualifier (`--factor`, different
   `--codec`, deferred `--tile-size`) ⇒ **hard contradiction error**, no override
   (it is impossible to honor, not merely non-conformant).
3. **Default crop (no `--lossless`)** — re-encodes the **exact extent** (lossy),
   preserving the deliberate asymmetry: default crop = precise rect; `--lossless`
   = tile-aligned superset with byte-identical L0.

## Conformance & validation

Codecs and containers are not freely interchangeable. A single **capability
table** (one source of truth, also feeding `--to X` help text) classifies each
container's codec support:

| Container | Conformant | Writable-but-non-conformant | No encoder / no slot |
|---|---|---|---|
| generic-TIFF | jpeg, jpeg2000, lzw, deflate, avif, webp, jxl, htj2k | — | — |
| SVS (Aperio) | jpeg, jpeg2000 | avif/webp/jxl/htj2k (a TIFF, but not an Aperio file readers open) | — |
| OME-TIFF | jpeg | non-jpeg → valid OME bytes, **our** reader can't read back | — |
| COG-WSI | jpeg (+ our codec set) | — | — |
| DICOM | jpeg-baseline (today) | — | avif/webp/jxl (no transfer syntax); jpeg2000/htj2k (valid syntax, **no encoder** — frame-copy only) |
| DZI/SZI | jpeg, png | — | everything else |

**`validate(spec)`** runs before dispatch, in priority order:
- **(a) Target exclusions** — `--rect`/`--factor` with `dzi`/`szi` → hard error.
- **(b) Logical contradictions** — `--lossless` + `--factor`/different-`--codec`/
  `--tile-size` → hard error (no override).
- **(c) Conformance tier** — for the requested codec×container:
  - **impossible** (no encoder / no slot) → hard error, no override;
  - **non-conformant** (writable but unreadable-as-that-format) → error by
    default, allowed with **`--allow-nonconformant`** which writes it **and still
    warns**;
  - **valid-but-limited** (OME-TIFF + non-jpeg) → warn, do not block.
- **(d) Lossless classification** — {auto-passthrough, lossless-eligible,
  lossless-error, re-encode}, routing the emitter.

Every error names the offending flags and the fix (e.g. "SVS uses jpeg/jpeg2000;
`--codec avif` produces a non-Aperio TIFF — use `--to tiff`, or
`--allow-nonconformant`").

**`--allow-nonconformant`** is a distinct flag — never overload `--force` (which
means "overwrite the output file").

**Upstream validator (forward-looking):** this capability matrix is exactly the
kind of format authority `opentile-go` (the reader / format source-of-truth) may
come to own. The validation layer should be structured around a single
`containerCapabilities(format) → {conformant, nonconformant, unsupported}`
lookup so that, if opentile-go ships a validator, wsitools can **delegate to /
reconcile with** it rather than permanently maintain a parallel table. Per the
opentile-go boundary, any reader-side conformance authority is filed upstream and
implemented there; wsitools consumes it.

## Flags, compatibility & deprecations

- **Purely additive / backward-compatible.** Nothing removed: `crop` /
  `downsample` keep working (now aliases), `convert --to X --factor N`
  unchanged, `transcode` revived, `convert` gains `--rect` + `--allow-nonconformant`.
- **`--jobs` ⇄ `--workers`** reconciled as aliases of each other everywhere
  (document `--jobs`; keep `--workers` working). No breakage.
- **`--quality`** default = re-encode at source quality for SVS→SVS same-codec,
  else the codec default (jpeg 90). One rule across all paths.
- **Internal convergence only** (`downsampleTo*` + `cropTo*` → `transformTo*`)
  is invisible to users.

## Error handling

All combination errors flow through `validate(spec)` (above) before any I/O, so a
bad invocation fails fast with a pointed message and no partial output. Runtime
errors (decode/encode/write) propagate from `materializeWorkingL0` /
`transformTo*` with context; the DICOM emitter keeps its temp-dir → atomic-rename
(no partial pyramid). `--allow-nonconformant` downgrades a tier-(c) error to a
warning.

## Testing

- **Composition matrix** — `convert` over the cross-product of {crop?, factor?,
  codec?, --to format} on a CC0 fixture; assert the output re-detects, has the
  expected dims/MPP/mag, and (for DICOM) dciodvfy = 0 errors.
- **Alias ≡ convert parity** — `crop --rect R` produces pixel-identical output to
  `convert --rect R` (same code path); likewise `downsample`/`transcode`. Use the
  `hash --mode pixel` oracle (file bytes may be nondeterministic).
- **One-pass equivalence** — fused `convert --rect --factor` is pixel-equivalent
  to sequential crop-then-downsample, confirming the fused path is correct (and
  is a single decode/encode).
- **Lossless oracle preserved** — `crop --lossless` still byte-identical L0
  (existing oracle); auto-lossless passthrough byte-identical to a verbatim copy.
- **Conformance gatekeeping** — impossible combos hard-error; non-conformant
  error by default and succeed-with-warning under `--allow-nonconformant`;
  valid-but-limited warns; contradictions (`--lossless --factor`) hard-error.
- **Backward-compat** — existing `crop` / `downsample` / `convert --to X
  [--factor N]` invocations produce unchanged output.

## Component summary

| Unit | Responsibility | Depends on |
|---|---|---|
| `runConvert` + alias wrappers | parse → build `transformSpec` → validate → dispatch | cobra, validate, transform module |
| `validate(spec)` + capability table | conformance/contradiction/lossless gating (consumes an upstream validator if available) | — |
| `downscale.materializeWorkingL0` | crop∘downsample the source L0 → working raster + scaled meta | opentile decode, BoxHalve |
| `transformTo{SVS,TIFF,OMETIFF,COGWSI,DICOM}` | rebuild pyramid in target container/codec (lossless branch when eligible) | streamwriter / cogwsiwriter / derivedsource+dicomwriter |

## Deferred / future (slots reserved)

- **Orientation** stage (`rotate`/`flip`) — net-new raster rotation + MPP-axis /
  geometry handling; the pipeline slot is a no-op in v1.
- **Pyramid structure** — `RebuildSpec{tileSize, levelRatio, levelCount}` on the
  rebuild stage; flags `--tile-size` / `--level-ratio` / `--levels` and a
  `retile` alias later. Re-encode-regime only (never with `--lossless`); enforce
  MCU/16 + container limits when exposed.
- **De-identification** (`--deidentify` / label strip during convert),
  **metadata policy** (ICC/vendor preserve/strip), **bit-depth/colorspace**,
  **multi-dim (Z/C) selection** — separate axes, separate future specs.
- **Upstream conformance validator** from opentile-go — consume when available.
