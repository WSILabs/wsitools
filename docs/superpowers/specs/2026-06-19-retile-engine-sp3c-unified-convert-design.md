# SP3c — unified-transform `convert` (re-based on the retile engine) — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** the streaming retile engine (SP1 + SP2 M1–M5 + SP3a, all merged). SP3c
is the CLI-convergence sub-project.
**Supersedes:** the *implementation* approach of
`docs/superpowers/specs/2026-06-14-unified-transform-convert-design.md`. That doc
established the **shape** (one-pass `convert`, single-axis aliases, revived
`transcode`, a conformance gate) and that shape is unchanged. But it was written
*before* the retile engine, so its pipeline section (the `materializeWorkingL0`
raster front-end; merging `downsampleTo*`/`cropTo*` into raster-based
`transformTo*`) is stale. SP2/SP3a already converged that machinery at the engine
layer. This doc re-bases the design onto the engine and re-scopes it into phases.

## Goal

Surface, at the CLI, the composition the retile engine already performs
internally: make `convert` a **single-pass transform** that composes container
change (`--to`), crop (`--rect`), downsample (`--factor`/`--target-mag`), and
codec change (`--codec`/`--quality`) in **one decode→rebuild pass**, while keeping
`crop` / `downsample` / `transcode` as focused single-axis aliases over the same
dispatch.

## Why this is now small: the engine already composes the axes

The composition primitive exists today. The engine core

```
buildEnginePyramid(ctx, slide, w, srcRegion opentile.Region, outL0 opentile.Size, quality, workers, postL0Hook)
```

takes `srcRegion` and `outL0` as **independent** arguments:

- `downsample` / `convert --factor` call it (via the thin `buildPyramid` wrapper)
  with `srcRegion = full L0`, `outL0 = L0/factor`.
- `crop` calls it with `srcRegion = rect`, `outL0 = rect.Size`.

So **crop + downsample in one pass is literally
`buildEnginePyramid(rect, rect.Size/factor)`** — the engine composes them now;
nothing at the CLI exposes it. The same is true of `runDICOMEngine` (DICOM) and of
`emitDZIPyramid` → `retile.Run` (DZI/SZI, which already pass `SrcRegion` + `OutL0`
and already honor `--factor`). SP3c is therefore **CLI surfacing + dispatch
convergence**, not new engine work.

## Mental model: one axis per verb, `convert` is the union

| Axis | Single-op alias | Headline flag(s) | Status |
|---|---|---|---|
| space | `crop` | `--rect X,Y,W,H` (or `--x/--y/--w/--h`) | Phase 1 |
| resolution | `downsample` | `--factor N` / `--target-mag M` | Phase 1 |
| codec | `transcode` (revived) | `--codec C` / `--quality Q` | Phase 1 |
| container + **all** | `convert` | `--to FMT` + the above | Phase 1 |
| orientation | `rotate` / `flip` | `--rotate` / `--flip` | deferred (slotted) |
| pyramid structure | `retile` | `--tile-size` / `--levels` | deferred (slotted) |

`transcode` re-encodes tiles to a different codec **in the same container, same
geometry** (e.g. `transcode --codec avif slide.tiff`). It was folded into `convert`
historically only because `convert` was then the sole transform verb; with the
engine's select-octave transcode path (M4) it earns its own focused identity again.

## CLI model (Phase 1)

**`convert` — the unified one-pass transform:**
```
convert [--to FMT] [--rect X,Y,W,H | --x/--y/--w/--h] [--factor N | --target-mag M] \
        [--codec C [--quality Q]] [-o OUT] [-f] [--workers/--jobs N] \
        [--no-associated] [--tile-order …] [--bigtiff …] \
        [--dzi-tile-size …] [--dzi-overlap …] INPUT
```
- **`--to` is optional.** Omitted ⇒ output container = source format (the
  format-preserving case falls out for free, reusing the source-format detection
  `crop`/`downsample` already do). `--to` still accepts
  `cog-wsi|svs|tiff|ome-tiff|dzi|szi|dicom|bif`.
- **`--rect` is new on `convert`.** Any subset of {rect, factor, codec, to}
  composes in one decode→rebuild. Headline:
  `convert --to dicom --rect 0,0,8192,8192 --factor 2 -o out in`.
- Bare `convert in out` (same format, no transform) = the existing lossless
  tile-copy passthrough.

**Single-axis aliases — thin desugar, format-preserving, no `--to`:**

| Alias | desugars to | exposes |
|---|---|---|
| `crop --rect X,Y,W,H [--lossless]` | `convert --rect …` | space axis + universal flags |
| `downsample --factor N \| --target-mag M` | `convert --factor …` | resolution axis + universal flags |
| `transcode --codec C [--quality Q]` | `convert --codec …` | codec axis + universal flags |

Aliases surface **only their axis flag + universal flags** (`-o`, `-f`,
`--quality`, `--workers/--jobs`, `--no-associated`, `--tile-order`, `--bigtiff`).
They have **no `--to`** and no other-axis flags — that is what keeps them "one axis
only." `--lossless` appears only on `crop` (the sole alias whose op can be
verbatim). The alias and `convert` build the **same** `transformSpec` and call the
**same** dispatch, so they are provably one code path.

`crop` keeps `--x/--y/--w/--h` as alternates to `--rect` (both exist today).

## Three corrections to the 2026-06-14 boundaries (DZI/SZI)

The 2026-06-14 doc excluded DZI/SZI from the transform axes because they were on a
separate pyramid-descent generator. **M1 re-pointed DZI/SZI onto the retile
engine** (`emitDZIPyramid` → `retile.Run`), so those exclusions are stale and are
lifted here:

1. **`--factor`/`--target-mag` already works for DZI/SZI** (`convert_dzi.go`
   `resolveFactor`/`reducedDims`; `OutL0 = cfg.Width/Height`). No change needed —
   just no longer described as excluded.
2. **`--rect` is included for DZI/SZI.** `emitDZIPyramid` already passes
   `SrcRegion` to `retile.Run` (hardcoded to full L0); setting `SrcRegion = rect`
   is the identical one-line change every other target gets. `convert --to dzi
   --rect …` (deep-zoom of a cropped region) is a useful, well-defined operation.
3. **`--codec` is unified across all targets**; `--dzi-format` becomes a
   deprecated back-compat alias. Choosing a tile codec is one concept; DZI/SZI just
   have a different *valid set* (`{jpeg, png}` — what browser deep-zoom viewers
   render) than WSI containers (`{jpeg, jpeg2000, htj2k, avif, webp, jxl}`).
   - **PNG is promoted to a first-class codec** in Phase 1: a new
     `internal/codec/png` subpackage with a `codec.EncoderFactory` registered via
     `init()` (encode-only, lossless RGB888, stdlib `image/png`), so `--codec png`
     is a real registry value like every other codec. The DZI/SZI encoder uses the
     registered codec instead of the inline stdlib path in
     `newDZIStandaloneEncoder` (one encode path, not two).
   - **Gating (the safety the promotion relies on):** `internal/codec` is
     **encode-side**; decode/read-back is opentile's job, which does not read
     PNG-compressed TIFF tiles — so PNG is conformant **only for `dzi|szi`** (the
     sole containers whose tiles we never read back through opentile). In Phase 1
     the existing **ad-hoc per-driver codec checks** must permit `--codec png` for
     `dzi|szi` only and reject it for every WSI container (png-in-TIFF is
     writable-but-nonconformant — Phase 2 table territory — and is **not** enabled
     in Phase 1). Phase 2's `containerCapabilities` table formalizes the same
     constraint.

DZI/SZI diverge from the other targets on exactly two points now: their codec
*valid set* (`--dzi-format`/`--codec` = jpeg|png) and `--lossless`, which is
deferred to **Phase 1b** (see below). They remain unreachable from the
`crop`/`downsample`/`transcode` aliases (aliases are format-preserving; you cannot
"preserve" a deep-zoom source).

## Internal convergence (the Phase 1 refactor)

One `transformSpec` built by `runConvert` and by each alias wrapper:

```
transformSpec{
    srcRegion  opentile.Region  // rect, or full L0 when no --rect
    outL0      opentile.Size     // srcRegion.Size/factor, or srcRegion.Size when no downsample
    codec      string            // --codec, or source codec when absent
    quality    int
    target     string            // --to, or source format when absent
    lossless   bool              // crop --lossless / auto-passthrough only
    losslessSource *…            // optional source-level handle for the verbatim branch
    // universal: workers, tileOrder, bigtiff, noAssociated, dzi cfg
}
```

The current split — `convert_factor.go`'s
`downsampleTo{SVS,TIFF,OMETIFF,COGWSI,DICOM}` and `crop_formats.go`'s
`cropTo{TIFF,OMETIFF,COGWSI,DICOM}` (+ `cropEmitSVS`) — **merge into one
`transformTo{…}` per target**, each computing `srcRegion`/`outL0` from the spec and
calling the engine core both families already call
(`buildEnginePyramid`/`buildEnginePyramidCOGWSI`/`runDICOMEngine`;
`emitDZIPyramid` for dzi/szi). DICOM is already partway converged — `runDICOMEngine`
is shared by `downsampleToDICOM` and `cropToDICOM` today.

**Combined metadata:** crop preserves MPP/mag; downsample scales MPP×factor,
mag÷factor; output L0 dims = `(cropW/factor)×(cropH/factor)`. (Same arithmetic the
two paths already do separately.)

**Level-spec choice (a correctness fork the implementer must preserve):** the
engine already uses two level strategies, and `transformTo*` must pick by whether
geometry changes:
- **Pure transcode / re-container** (no `--rect`, no `--factor`/`--target-mag`) →
  **select-octave** (M4): emit only source-matching octaves with ±2px tolerance,
  preserving the source's exact level count and ratios (`transcodeOctaveLevels` /
  `LevelSpec.Intermediate`). This is what the `transcode` alias and a bare
  re-container hit. **But only for a *lossy* codec:** a **lossless-codec** transcode
  (e.g. `--codec jpeg2000 --quality reversible=true`, codec-owned `IsLossless()`)
  must stay byte-exact, and the engine read is **not** pixel-identical (the M4/M5
  finding) — so it routes to the per-level `transcodePyramid` path, **not** the
  engine. `transformTo*` must probe `encoderIsLossless` and branch accordingly, as
  the convert transcode path does today.
- **Geometry change** (`--rect` and/or `--factor`) → **octave-floored** from the
  new L0 (M2/M3/M5; `octaveLevelSpecsFor` / `flooredLevelCount`). The output level
  count is derived from the post-transform L0, not `len(slide.Levels())` — the same
  approved trade-off as M3.

**Dispatch:** `runConvert` parses flags → builds `transformSpec` → (Phase 2:
`validate(spec)`) → `transformTo<target>`. The `crop`/`downsample`/`transcode`
aliases build the same spec (their one axis; `--to` = source) and call the same
dispatch.

**One-pass guarantee:** `convert --rect --factor --codec --to` does exactly one
decode (cropped L0 region through the engine's ScaledStrips read) + one encode
(output pyramid) — no temp files, no generational re-encoding.

## Lossless regime (Phase 1)

Unchanged from today; `--lossless` (verbatim compressed-tile copy) is eligible
**only** when all hold: no downsample; no codec change; crop is tile-aligned (or
absent); the target container can carry the source codec verbatim. The engine read
is **not** byte-exact (the M4/M5 finding), so the lossless branch stays on the
verbatim copy path (`cropEmitSVS`'s `writeLosslessL0`, `derivedsource.WithLosslessL0`
for DICOM), **not** the engine.

Three behaviors (as today): auto-lossless passthrough (pure re-container);
explicit `--lossless` on `crop` (snaps the rect to the tile grid → tile-aligned
superset, byte-identical L0, with the printed notice); default crop (re-encodes the
exact extent, lossy). `--lossless` + any disqualifier (`--factor`, different
`--codec`) ⇒ hard contradiction error (Phase 2 gives it the consolidated message;
Phase 1 keeps today's per-path errors).

**Lossless DZI/SZI is Phase 1b** (below), not Phase 1.

## `--jobs` ⇄ `--workers` reconciliation (Phase 1)

`downsample` exposes `--jobs`; `convert`/`crop` expose `--workers`. Reconcile as
aliases of each other everywhere (both work; document one). Purely additive, no
breakage. Small standardization win that belongs with the alias convergence.

## Scope

**Phase 1 (this spec):**
- `convert`: `--rect`, optional `--to`, compose any subset of {rect, factor, codec,
  to} in one pass.
- `transformTo{SVS,TIFF,OMETIFF,COGWSI,DICOM,DZI,SZI}` convergence over the engine
  (merge `downsampleTo*` + `cropTo*`).
- `--codec` unified across all targets; `--dzi-format` deprecated alias; DZI/SZI in
  `--rect`.
- **PNG promoted to a first-class codec** (`internal/codec/png`, registered via
  `init()`); DZI/SZI encoder re-pointed onto it; `--codec png` gated to `dzi|szi`
  only (ad-hoc checks in Phase 1; Phase 2 table thereafter).
- `crop`/`downsample` as thin aliases; `transcode` revived as the codec alias.
- `--jobs`/`--workers` reconciliation.
- Lossless regime unchanged (verbatim branch off the engine), **except** DZI
  lossless (Phase 1b).

**Phase 1b (own spec):** lossless `--to dzi|szi`. `--lossless` becomes valid for
DZI/SZI: copy the source JPEG tiles into the DZI **base level** verbatim
(consistent with the existing "base byte-identical, lower levels regenerated"
lossless semantics of `crop`/`downsample`), regenerate the reduced levels.
Constraints, stated up front:
- **jpeg-source only** — J2K/HTJ2K isn't a Deep Zoom tile format (a browser viewer
  can't read it), so those sources cannot be lossless-to-DZI.
- **geometry-gated** — requires `--dzi-overlap 0` and `--dzi-tile-size` = the
  source's stored tile size; otherwise base tiles are re-cut → re-encode.
- **edge tiles re-encoded** — source tiles are padded to full tile size; DZI edge
  tiles must be trimmed to the true remainder dims, so the right/bottom edge
  column/row decode→trim→re-encode. The guarantee is therefore "byte-identical
  interior, regenerated edges + lower levels" — slightly weaker than `crop
  --lossless`'s full-L0 byte-identity; the help text/notice must say so.
- needs a new passthrough path in the DZI sink (accept pre-encoded source bytes,
  like `crop`'s `writeLosslessL0`) + the edge-recut logic.
- `--lossless --factor` stays a hard contradiction.

**Phase 2 (own spec):** the conformance/capability gate — a single
`containerCapabilities(format) → {conformant, nonconformant, unsupported}` table
(source of truth, also feeds `--to`/`--codec` help text), `--allow-nonconformant`,
contradiction errors, and `validate(spec)` run before dispatch. Phase 1 relies on
the existing ad-hoc per-driver codec checks until then. (Forward-looking: this
matrix is the kind of format authority `opentile-go` may come to own; structure it
so wsitools can delegate to/reconcile with an upstream validator. Per the
opentile-go boundary, reader-side conformance authority is filed upstream and
implemented there.)

**Deferred / slotted (unchanged from 2026-06-14):** orientation (`rotate`/`flip`),
pyramid structure (`--tile-size`/`--levels`/`retile`), de-identification,
metadata-policy flags, bit-depth/colorspace, multi-dimensional (Z/C) selection.

## Components

| Unit | Responsibility | Depends on |
|---|---|---|
| `runConvert` + `crop`/`downsample`/`transcode` wrappers | parse → build `transformSpec` → dispatch (validate in P2) | cobra, transform module |
| `transformTo{SVS,TIFF,OMETIFF,COGWSI,DICOM,DZI,SZI}` | compute srcRegion/outL0 + metadata, rebuild via the engine core; verbatim branch when lossless-eligible | `buildEnginePyramid*`, `runDICOMEngine`, `emitDZIPyramid`, streamwriter/cogwsiwriter/derivedsource |
| `transformSpec` | the one struct both `convert` and aliases build | — |
| `internal/codec/png` | first-class PNG encoder (lossless RGB888), registered via `init()`; backs `--codec png` and the DZI/SZI PNG path | `internal/codec`, `image/png` |

## Error handling

Phase 1 keeps today's per-path errors (each driver validates its own codec/target
support; `--rect`/`--factor` bounds checks; `--lossless` contradictions). The DICOM
emitter keeps its temp-dir → atomic-rename. Phase 2 consolidates these into
`validate(spec)` (fail fast, before any I/O, pointed messages). No partial output
on a bad invocation.

## Testing (Phase 1)

- **Composition matrix** — `convert` over the cross-product of {rect?, factor?,
  codec?, --to format} on a CC0 fixture; assert the output re-detects, has the
  expected dims/MPP/mag, and (for DICOM) `dciodvfy` = 0 errors.
- **Alias ≡ convert parity** — `crop --rect R` is pixel-identical to `convert
  --rect R` (same code path); likewise `downsample`/`transcode`. Use the `hash
  --mode pixel` oracle (file bytes may be nondeterministic — see the
  pipeline-nondeterminism note).
- **One-pass equivalence** — fused `convert --rect --factor` is pixel-equivalent to
  sequential crop-then-downsample, confirming the fused path is correct and is a
  single decode/encode.
- **DZI/SZI `--rect`** — `convert --to dzi --rect R` produces a deep-zoom of the
  region with the expected base dims; `--codec`/`--dzi-format` alias parity.
- **PNG codec** — `internal/codec/png` round-trips a known RGB raster (encode →
  stdlib decode → pixel-identical); `convert --to dzi --codec png` emits PNG tiles
  byte-identical to the pre-promotion stdlib path (no output regression); `--codec
  png --to tiff` is rejected in Phase 1.
- **`--to`-optional** — `convert in out` (no `--to`) preserves the source format;
  `transcode --codec C` preserves container + geometry, changes codec.
- **Lossless oracle preserved** — `crop --lossless` still byte-identical L0;
  auto-lossless passthrough byte-identical to a verbatim copy.
- **Backward-compat** — existing `crop` / `downsample` / `convert --to X [--factor
  N]` invocations produce unchanged output; full `-race`.

## Boundaries / deferred

**In Phase 1:** unified `convert` (`--rect`, optional `--to`, compose axes),
`transformTo*` convergence over the engine, `--codec` unification + DZI/SZI in
`--rect`, PNG promoted to a first-class codec (gated to `dzi|szi`), revived
`transcode`, alias convergence, `--jobs`/`--workers` reconciliation.

**Deferred:** Phase 1b (lossless DZI/SZI), Phase 2 (conformance gate), and all the
2026-06-14 slotted axes (orientation, pyramid structure, de-id, metadata policy,
bit-depth/colorspace, multi-dim).
