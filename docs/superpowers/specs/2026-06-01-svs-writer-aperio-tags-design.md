# SVS writer Aperio-conformance tags (sub-project #2, phase 1) — design

**Date:** 2026-06-01
**Status:** approved, ready for planning
**Scope:** `convert --to svs` only. tiff / ome-tiff / cog-wsi / downsample
targets are out of scope and unchanged.

## Background

Sub-project #2 is per-target-format metadata conformance: each target
format owns the set of TIFF tags it is *defined* to carry, and the writer
populates them. Phase 1 covers the **SVS writer** specifically.

An audit of the SVS sample fixtures (recorded in
`memory/project_metadata_fidelity_direction.md`) split them by producer:

- **Genuine Aperio** (`CMU-1-Small-Region.svs`, `CMU-1.svs`,
  `JP2K-33003-1.svs`) — L0 emits `ImageDepth` (32997, always),
  `YCbCrSubSampling` (530, for JPEG-compressed data), `ICCProfile` (34675,
  sometimes). Notably **no** resolution tags (282/283/296): genuine Aperio
  carries MPP only inside the Aperio `ImageDescription` string.
- **Grundium** (Aperio-compatible: `scan_617_*`, `scan_620_*`,
  `svs_40x_bigtiff.svs`) — additionally emits `Orientation` (274),
  resolution tags (282/283/296), `PageNumber` (297),
  `ReferenceBlackWhite` (532), and emits **no** ICC.

ICC (34675) already ships (phase 1 of sub-project #1). Comparing genuine
Aperio against what the wsitools SVS writer currently emits, the remaining
genuine-Aperio L0 gap is exactly two tags: **`ImageDepth` (32997)** and
**`YCbCrSubSampling` (530)**. This spec adds only those two.

The Grundium-only tags are explicitly **not** added — they are not part of
genuine-Aperio conformance. (Note: wsitools' scale-metadata feature already
*generates* resolution tags 282/283/296 from MPP, which genuine Aperio does
not. That is a deliberate readability deviation, left as-is here; the
strict-mimic-vs-readable question is out of scope for this phase.)

## Key finding that de-risks YCbCrSubSampling

Genuine Aperio CMU-1.svs L0 sets `PhotometricInterpretation = RGB (2)`
*together with* `YCbCrSubSampling = [2,2]`. The wsitools SVS writer
**already** emits `Photometric = 2` (RGB) on tile-copy and re-encode paths.

With new-style JPEG-in-TIFF (`Compression = 7`) and `Photometric = 2`, the
JPEG codestream owns its color transform and decodes to RGB; the TIFF
`YCbCrSubSampling` tag is **informational** and is ignored by conformant
decoders (it is only load-bearing under `Photometric = 6` / YCbCr). So
emitting 530 cannot cause a color misrender. It exists for Aperio
look-alike fidelity and as an honest record of the underlying JPEG chroma
sampling.

## Requirements

### R1 — ImageDepth (32997)

On the **L0 IFD** of `convert --to svs` output, emit `ImageDepth` = `1`,
type LONG, count 1. Always (wsitools only produces 2D output). Independent
of codec.

### R2 — YCbCrSubSampling (530)

On the **L0 IFD** of `convert --to svs` output, emit `YCbCrSubSampling`,
type SHORT, count 2, **only when the output L0 compression is JPEG**
(`tiff.CompressionJPEG`, 7). For any other output codec (JPEG 2000, AVIF,
WebP, JPEG-XL, HTJ2K, …) **do not** emit 530 — it is meaningless outside
JPEG.

The emitted value MUST match the actual chroma subsampling of the JPEG
bytes written to the file ("match what we are writing"):

- **Tile-copy path** (verbatim source JPEG tiles): parse the subsampling
  from an actual source JPEG tile's SOF marker.
- **Re-encode-to-JPEG path** (wsitools' own JPEG encoder): the encoder is
  fixed at YCbCr 4:2:0, so the value is `[2,2]`.

`YCbCrSubSampling` is `[ChromaSubsampleHoriz, ChromaSubsampleVert]`, which
equals the luma (component 0) sampling factors in the JPEG SOF: 4:2:0 →
`[2,2]`, 4:2:2 → `[2,1]`, 4:4:4 → `[1,1]`.

### R3 — Placement and gating

Both tags are L0-only and SVS-container-only. The streamwriter stays
format-agnostic; the SVS-conformance policy (which tags, when) lives in the
`convert --to svs` caller, mirroring how ICC / MPP / ImageDescription are
already routed.

### R4 — dump-ifds visibility

`32997 → "ImageDepth"` is added to the `internal/tiff/tagnames.go`
dictionary so `dump-ifds --raw` shows it by name instead of `unknown`.
`530 → "YCbCrSubSampling"` already exists there.

## Design

### Components

1. **`internal/tiff/tags.go`** — add two constants:
   - `TagImageDepth uint16 = 32997`
   - `TagYCbCrSubSampling uint16 = 530`

2. **`internal/tiff/tagnames.go`** — add `32997: "ImageDepth"` to the
   name dictionary.

3. **JPEG SOF subsampling helper** — a small pure-Go function that scans a
   JPEG bytestream for the SOF marker (`0xFFC0`–`0xFFCF`, excluding
   `0xFFC4`/`0xFFC8`/`0xFFCC`) and returns the component-0 (luma) horizontal
   and vertical sampling factors as `[2]uint16`, plus an `ok` bool
   (`ok=false` if no SOF / malformed). This is the "match what we wrote"
   primitive for the tile-copy path. Location: a new file under
   `internal/codec/jpeg/` (or `internal/tiff/`), wherever a tile's bytes are
   reachable without a cgo decode — header parsing only, no libjpeg call.
   (Final home decided in the plan; it must be importable by the convert
   command without an import cycle.)

4. **`internal/tiff/streamwriter`** — `Options` gains generic, emit-if-set
   fields:
   - `ImageDepth uint32` — when non-zero, L0 emits `TagImageDepth` LONG = value.
   - `YCbCrSubSampling []uint16` — when len == 2, L0 emits
     `TagYCbCrSubSampling` SHORT[2].
   These are emitted in the existing L0-metadata path (`addL0Metadata`),
   alongside ICC. The writer does no SVS-specific gating — it only emits
   what the caller set.

5. **`cmd/wsitools/convert_tiff.go`** — when the target container is `svs`:
   - Set `Options.ImageDepth = 1`.
   - If output L0 compression is JPEG:
     - tile-copy: read one source L0 JPEG tile, run the SOF helper, set
       `Options.YCbCrSubSampling` to the parsed factors. If parsing fails,
       omit 530 (do not guess).
     - re-encode to JPEG: set `Options.YCbCrSubSampling = []uint16{2, 2}`.
   - Otherwise leave `YCbCrSubSampling` nil (omit 530).

### Data flow

`convert --to svs` → determines container == svs and output codec → sets
`streamwriter.Options.{ImageDepth, YCbCrSubSampling}` → `streamwriter`
emits the two tags on L0 in `addL0Metadata` → bytes land in the file.

No change to cog-wsi (`convert --to cog-wsi` uses a different writer and is
out of scope) or to the tiff/ome-tiff targets (the caller only sets these
Options when container == svs).

### Why the writer stays generic

`ImageDepth` and `YCbCrSubSampling` are ordinary TIFF tags, not
SVS-private. Keeping them as plain emit-if-set Options means the
streamwriter has no `if format == "svs"` branch; the SVS policy is one
place (the convert caller). This matches the existing ICC/MPP routing and
keeps the writer easy to reason about.

## Testing

1. **Unit — SOF helper:** table test over hand-built minimal JPEG headers
   (4:2:0 → `[2,2]`, 4:2:2 → `[2,1]`, 4:4:4 → `[1,1]`) plus a malformed /
   no-SOF input → `ok=false`.

2. **Unit — streamwriter emit-if-set:** extend the existing
   `streamwriter` tag tests (same shape as `icc_test.go`): given
   `Options.ImageDepth = 1` and `Options.YCbCrSubSampling = [2,2]`, L0
   reports `32997` and `530`; given the zero values, neither tag appears.

3. **Integration — genuine Aperio round-trip:** `convert --to svs` on a
   genuine-Aperio JPEG fixture (`CMU-1-Small-Region.svs`); assert L0 has
   `ImageDepth = 1` and `YCbCrSubSampling` equal to the source's value
   (CMU-1 family = `[2,2]`), via `dump-ifds --raw`.

4. **Integration — non-JPEG omits 530:** `convert --to svs --codec <a
   non-JPEG codec>` (or a JP2K source on the tile-copy path) → L0 has
   `ImageDepth = 1` but **no** `530`.

5. **Integration — re-encode to JPEG:** `convert --to svs --codec jpeg`
   (re-encode path) → L0 `YCbCrSubSampling = [2,2]`.

6. **Regression — pixels unchanged:** existing pixel-equivalence checks
   (`hash --mode pixel`) still pass; adding informational L0 tags must not
   perturb decoded pixels.

## Documentation

Extend `docs/tiff-tags.md` with a new section, **"SVS writer tag
profile"**, capturing:

- The genuine-Aperio vs Grundium L0 tag-set split (the audit finding).
- Exactly which L0 tags the wsitools SVS writer emits and why:
  `ImageDepth` (32997, always = 1), `YCbCrSubSampling` (530, JPEG output
  only, matches the written JPEG chroma sampling), `ICCProfile` (34675,
  when the source has one), plus the generated MPP/resolution/WSI tags from
  scale-metadata.
- The explicit non-goals: Grundium-only tags (274/297/532, and 282/283/296
  beyond what scale-metadata already generates) are not emitted for Aperio
  conformance.
- A note that `YCbCrSubSampling` is informational under `Photometric = 2`
  (RGB) — present for Aperio look-alike fidelity, ignored by decoders.

## Out of scope / follow-ups

- Strict Aperio byte-mimic vs maximally-readable SVS (affects whether the
  scale-metadata resolution tags 282/283/296 stay on SVS output). Tracked
  separately.
- cog-wsi / tiff / ome-tiff conformance profiles (later phases of
  sub-project #2).
- **Revisit/confirm YCbCrSubSampling behavior in conversion** (tracked as a
  todo): verify across both code paths that the emitted 530 matches the
  JPEG bytes actually written, and that non-JPEG output omits it.
