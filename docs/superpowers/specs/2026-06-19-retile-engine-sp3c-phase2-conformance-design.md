# SP3c Phase 2 — conformance gate — design

**Date:** 2026-06-19
**Status:** Approved design, ready for implementation plan.
**Parent:** SP3c (Phase 1 + Phase 1b merged, main@e089125).
**Umbrella spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-unified-convert-design.md`
("Conformance & validation" section).

## Goal

Replace the **five scattered ad-hoc codec×container checks** with **one capability
table** (single source of truth) + a `validateCodec` gate, add an
`--allow-nonconformant` escape hatch, and surface the valid codecs per `--to` in
help text. The gate makes every "codec X into container Y" decision consistent and
documented, and lets power users deliberately write valid-but-non-readable outputs.

## The five checks being consolidated

| Location | Current ad-hoc rule |
|---|---|
| `convert.go:142` | `--codec png` only valid with `--to dzi\|szi` |
| `dzi_format.go:22` | DZI/SZI tiles must be jpeg or png |
| `crop.go:214`, `convert_factor.go:84` | SVS emitters are jpeg-only → "use --to tiff" |
| `dicom_engine.go:184` | DICOM codec ∈ {jpeg, jpeg2000, htj2k} |
| `convert_tiff.go:69` | `--codec` required when no tile-copy path |

All become calls into the gate. (The DZI/SZI tile-format selection stays in
`resolveDZIFormat`, but its valid-set is sourced from the table.)

## Three-tier model

For a (container, codec) pair, the gate classifies into one tier:

1. **conformant** — wsitools writes it **and** the bytes are readable as that
   format (round-trip verified). → proceed silently.
2. **nonconformant** — wsitools can write the bytes, but they are **not** readable
   as the claimed format (our reader / standard readers can't open them). →
   **error by default**; `--allow-nonconformant` writes it anyway **and still
   prints a warning**.
3. **unsupported** — wsitools **cannot** produce it: no encoder, no container slot,
   or the emitter is codec-limited. → **hard error, no override**, with a redirect
   to a container that can.

## Capability table (VERIFIED by round-trip on CMU-1-Small-Region)

`containerCapabilities(container) → {conformant []codec, nonconformant []codec}`
(everything else ⇒ unsupported). The values below are the **measured** result of
writing each (container, codec) and re-reading via `hash --mode pixel` (decodes
every tile = readable):

| Container | conformant (verified) | nonconformant (verified) | unsupported ⇒ redirect |
|---|---|---|---|
| tiff (generic) | jpeg, jpeg2000, htj2k, avif, webp | **jpegxl** (writes, no read-back) | — |
| svs | jpeg, **jpeg2000** | htj2k, avif, webp, jpegxl | — |
| ome-tiff | jpeg, **jpeg2000** | htj2k, avif, webp, jpegxl | — |
| cog-wsi | jpeg, jpeg2000, htj2k, avif, webp, jpegxl | — | — |
| dicom | jpeg, jpeg2000, htj2k | — | avif, webp, jpegxl → "no DICOM transfer syntax; use jpeg/jpeg2000/htj2k" |
| dzi, szi | jpeg, png | — | everything else → "Deep Zoom tiles are jpeg or png" |
| bif | jpeg (verbatim copy only) | — | re-encode to bif unsupported |

Surprises the round-trip caught (why verification mattered): **jpegxl-in-generic-
TIFF does NOT read back** (nonconformant, not conformant as the 2026-06-14 sketch
assumed); **jpeg2000-in-OME-TIFF DOES** (conformant); **jpeg2000-SVS is conformant**
(a valid J2K Aperio file — SVS is NOT jpeg-only).

### Path-dependence (important model correction)

Codec support is **path-dependent** for SVS and DICOM:
- **SVS** — the *transcode* path (`convert --to svs --codec X`, no `--rect`/`--factor`)
  is codec-capable: it writes conformant jpeg2000-SVS and nonconformant
  avif/webp/htj2k/jpegxl. But the *crop/downsample* SVS emitter (`cropEmitSVS`/
  `downsampleToSVS`) is **jpeg-only**.
- **DICOM** — the *transform* path (`--factor`/`--rect`) writes jpeg/jpeg2000/htj2k;
  the *verbatim* path (`convert --to dicom` no transform) preserves the source
  codec (effectively jpeg-only — can't switch codec without re-encoding).

The **gate (table) classifies codec×container CONFORMANCE** (the best-achievable
tier). The **path-specific emitter limits stay as their own checks** — they are a
narrower "this transform path can't produce codec X for container Y" constraint,
not a conformance question:
- `cropEmitSVS`/`downsampleToSVS` keep their jpeg-only guard (`crop.go:214`,
  `convert_factor.go:84`) — so `convert --to svs --rect … --codec jpeg2000` still
  errors with "SVS crop keeps jpeg tiles" (the deferred SVS-crop-emitter codec work).
- the verbatim-DICOM jpeg-only constraint stays in its path.

So the gate **consolidates the conformance rules** (`convert.go:142` png→dzi/szi;
`dzi_format.go:22` dzi jpeg/png; the codec×container tiers; the DICOM
no-transfer-syntax set) and **adds the nonconformant tier + `--allow-nonconformant`**;
the two path-specific emitter limits remain where they are.

**OME-TIFF non-jpeg (htj2k/avif/webp/jpegxl)** and the new nonconformant entries
(svs/tiff/ome non-readable codecs) are the real use for `--allow-nonconformant`:
today (post-3c) `convert --to ome-tiff --codec avif` writes **silently**; Phase 2
makes it **error by default** ("valid bytes, but this tool's reader can't open them
as ome-tiff; pass --allow-nonconformant"), and write+warn under the flag.
(Behavior change, called out.)

## `validateCodec`

```
validateCodec(container, codec string, allowNonconformant bool) (warning string, err error)
```
- codec ∈ conformant → `("", nil)`.
- codec ∈ nonconformant → allowNonconformant ? `(warn, nil)` : `("", error)`.
- else (unsupported) → `("", error)` with the container's redirect.

Called from `runConvert` (and the alias verbs) **before dispatch**, after `--to`
and `--codec` are resolved. Returned `warning` is printed (non-fatal); `err` aborts
before any I/O. `--allow-nonconformant` is a **distinct bool flag** (never overload
`--force`, which is overwrite-output).

## Lossless / contradiction checks (already exist — stay, or fold in)

The non-codec contradiction checks (`--lossless` + `--factor`, lossless geometry,
`--rect` bounds) are already enforced in `losslessDZIConfig` / `validateRectCombo`
/ `runCrop`. Phase 2 does **not** move these; it owns only the **codec×container**
gate. (A future cleanup could route all of them through one `validate(spec)`.)

## Help text

`--to`'s and `--codec`'s flag usage, and an error's redirect, are generated from
the table so they never drift. Minimum: when `validateCodec` errors, the message
lists the container's conformant codecs.

## Components

| Unit | Responsibility | Source |
|---|---|---|
| `containerCapabilities(container) → caps` | the table: conformant + nonconformant codec sets per container | new `cmd/wsitools/capabilities.go` |
| `validateCodec(container, codec, allow) → (warn, err)` | three-tier classification + messages | `capabilities.go` |
| `--allow-nonconformant` flag | bool on `convert` (+ `transcode`) | `convert.go` / `transcode.go` |
| gate call in `runConvert` | resolve container+codec → `validateCodec` → print warn / return err | `convert.go` |
| ad-hoc check removal | replace the 5 scattered checks with the gate (or source their valid-set from the table) | the 5 files above |

## Forward-looking: opentile delegation

The table is the kind of format authority `opentile-go` (the read source-of-truth)
may come to own. Structure `containerCapabilities` as the single lookup so that, if
opentile ships a validator/capability API, wsitools **delegates to / reconciles
with** it instead of maintaining a parallel table. Per the opentile-go boundary,
any reader-side conformance authority is filed upstream and implemented there;
wsitools consumes it. (No upstream dependency in this phase — just the seam.)

## Testing

- **Table round-trip verification (the populating task):** for each (container,
  codec), write a tiny pyramid and attempt to re-open + decode via opentile;
  classify conformant vs nonconformant from the actual result. The committed table
  reflects verified reality, not assumption.
- **Gate unit tests:** `validateCodec` returns the right tier/err for representative
  pairs (tiff+avif ok; ome-tiff+avif error→flag→warn; svs+avif unsupported error;
  dicom+avif unsupported; dzi+avif unsupported; png+tiff unsupported).
- **`--allow-nonconformant`:** `convert --to ome-tiff --codec avif` errors;
  `… --allow-nonconformant` writes + warns; the output is the avif-OME bytes.
- **Consolidation parity:** the 5 previously-scattered errors still fire (same
  cases), now via the gate, with consistent wording. Existing tests for those cases
  pass (update wording assertions as needed).
- **No-codec / jpeg-default unchanged:** the gate is a no-op for conformant
  defaults; full `-race`.

## Boundaries / deferred

**In Phase 2:** the capability table, `validateCodec`, `--allow-nonconformant`,
consolidation of the 5 checks, OME-non-jpeg gating, help text, the opentile seam.

**Deferred:** making the SVS **crop/downsample** emitter codec-configurable (so
`convert --to svs --rect/--factor --codec jpeg2000` works — the SVS *transcode*
path already does jpeg2000); the verbatim-DICOM codec constraint; the
`validate(spec)` unification of the lossless/contradiction checks; an actual
opentile-go capability API (file/consume when it exists).
