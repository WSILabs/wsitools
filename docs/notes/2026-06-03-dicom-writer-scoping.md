# DICOM-WSI writer — rough scoping (pre-spec)

> Status: **scoping draft**, not a committed design. A full brainstorm → spec →
> plan cycle precedes implementation. This captures the shape, the build
> decision, the license basis, and the phasing so the to-do entry is grounded.

## Goal

A `convert --to dicom` target that emits a **DICOM VL Whole Slide Microscopy
Image** (Supplement 145; SOP class `1.2.840.10008.5.1.4.1.1.77.1.6`). Output is
a *set* of SOP Instances (typically one per pyramid level, grouped in a
Series within a Study), not a single file — plus optionally label/overview as
separate instances.

## Build decision: pure-Go, porting wsi2dcm's IOD logic

- **Library:** `github.com/suyashkumar/dicom` (already an indirect dep via
  opentile-go's DICOM reader) for low-level dataset/element/encapsulation writes.
- **The hard part (WSM IOD assembly) is PORTED, not derived from spec.** Both
  wsitools and **wsi2dcm** (`GoogleCloudPlatform/wsi-to-dicom-converter`) are
  **Apache-2.0**, so its source can be directly translated C++→Go — no
  clean-room. Light obligations: retain Google's copyright header on ported
  portions, propagate its `NOTICE` attributions, mark changed files.
- **highdicom** (Python, MIT) is a second freely-portable reference — the
  clearest high-level model of the WSM attribute structure.
- **Port the source, never link the binary.** We transcribe wsi2dcm's *DICOM
  half* and re-wire the plumbing (its DCMTK calls → `suyashkumar/dicom`; its
  OpenSlide input → our `internal/source` + opentile-go metadata). This avoids
  every heavy/copyleft runtime dep wsi2dcm carries — OpenSlide (LGPL-2.1, the
  dependency opentile-go exists to replace), OpenCV, Boost, Abseil, DCMTK.

## Architectural constraint (non-negotiable)

This is still significant work, and the failure mode to avoid is grafting in a
large foreign-shaped C++-style codebase. Port the *logic and values*, but in
**wsitools idioms**:

- Pure-Go writer (CLAUDE.md: "writers are pure Go; cgo only inside codec
  wrappers"). No new cgo, no C++ shim.
- Small, focused, independently-testable packages — mirror how
  `internal/tiff/streamwriter` / `cogwsiwriter` are organized. Candidate split:
  - dataset/element emission (thin layer over `suyashkumar/dicom`),
  - WSM IOD attribute assembly (the ported policy: which attrs, required
    modules),
  - slide-geometry mapping (MPP/dims/origin/orientation → DICOM attributes),
  - frame encapsulation + offset table (tiles → encapsulated multi-frame).
- Reuse existing infra: `internal/source` (read + metadata), `internal/pipeline`
  (tile fan-out), the **tile-copy fast path** (wrap existing compressed JPEG
  tiles as encapsulated frames — no re-encode when the transfer syntax matches),
  `internal/cliout`, ICC/scale metadata already pulled by the source layer.

## What reuses vs. what's net-new

| Reuse (cheap) | Net-new (the work) |
|---|---|
| source pyramid geometry + metadata (MPP, mag, ICC) | WSM IOD attribute assembly (ported from wsi2dcm) |
| compressed JPEG tile bytes → encapsulated frames (no re-encode) | encapsulated multi-frame PixelData + offset table |
| `internal/pipeline` worker pool | slide-coordinate geometry mapping |
| `convert --to` CLI wiring pattern | UID generation (Study/Series/SOP/FrameOfRef) |
| ICC carry (already done for TIFF targets) | synthetic-but-conformant Patient/Study/Specimen |
|  | validation harness (read-back + external validator + viewer) |

## Phasing

- **Phase 0 — spike (de-risk the IOD):** emit ONE `TILED_FULL`, single-level,
  brightfield-RGB instance with minimal-but-conformant metadata. Validate via
  `dcmtk` `dciodvfy`/`dcmdump`, round-trip through opentile-go's DICOM reader,
  and open in one viewer (e.g. OHIF/Slim). Decides whether the Go port is "a few
  hundred lines" or a swamp before further investment.
- **Phase 1:** full pyramid (instance per level), JPEG tile-copy reuse,
  synthetic-conformant Patient/Study/Specimen, ICC carry, MPP/orientation from
  source.
- **Phase 2:** `TILED_SPARSE`, label/overview as separate instances,
  Concatenations (split oversized levels), richer specimen/staining metadata.
- **Phase 3:** multi-channel / z-stack (fluorescence) — multiple optical paths
  and focal planes; large, separate.

## Phase 0 outcome (2026-06-11)

**DONE — and tractable (NOT a swamp).** `convert --to dicom -o out.dcm --level N
<input.dcm>` emits ONE conformant WSM **VOLUME** instance from a **DICOM source**:
source JPEG frames copied **verbatim** (byte-identical), re-encapsulated as
TILED_FULL multi-frame PixelData; one level per invocation (`--level`, default
`0`). `dciodvfy` (dicom3tools) reports **0 errors** on both L0 (65536²,
16384 frames) and reduced L2 instances — the sole residual is a benign "Study ID"
DICOMDIR warning (anonymous re-emission has no study identifier). The dataset
mirrors a real Grundium WSM golden; output round-trips through opentile-go
(`source.Open` → `Format: dicom`, frames byte-identical). Built on
`github.com/suyashkumar/dicom` **v1.1.0** (pure Go, now a direct dep).

### `suyashkumar/dicom` v1.1.0 construction patterns de-risked

These are the non-obvious library mechanics the spike pinned down — they're the
"port" gotchas Phase 1 inherits:

- **Encapsulated PixelData must be a HAND-BUILT `*dicom.Element`** — not via
  `dicom.NewElement`. Construct it directly: `Tag` = PixelData,
  `ValueRepresentation` = `tag.VRPixelData`, `RawValueRepresentation` = `"OB"`,
  `ValueLength` = `tag.VLUndefinedLength`, `Value` =
  `dicom.NewValue(dicom.PixelDataInfo{...})`. Calling
  `dicom.NewElement(tag.PixelData, info)` **SIGSEGVs** (it forces the OW/native
  branch).
- **SQ construction:** `dicom.NewElement(seqTag, [][]*dicom.Element{ {item-elems...} })`;
  an empty sequence is `[][]*dicom.Element{}`.
- **The library does NOT sort elements on write** — emit them in **ascending tag
  order manually** or the output is non-conformant.
- **JPEG Baseline transfer syntax has no exported const** — use the literal
  `"1.2.840.10008.1.2.4.50"`. `NumberOfFrames` VR is **IS**, so its value is a
  `[]string`, not an int.
- **The 8 WSM IOD attributes `dciodvfy` required:** `StudyDate`/`StudyTime`,
  `ContentDate`/`ContentTime`, `AcquisitionDateTime`, `DeviceSerialNumber`,
  `LossyImageCompressionRatio`, and `ICCProfile` (in the Optical Path sequence).

**Known P0 limitation:** a source lacking an embedded ICC profile would
reintroduce a Type 1C conformance gap (`ICCProfile` in Optical Path) — fine for
P0 (DICOM input carries ICC in practice).

**Conclusion:** the Go port is "a few hundred lines," not a swamp. **Phase 1
(full pyramid + non-DICOM sources + colorspace reconciliation) is the clear next
step.**

## Phase 1 — slice 1 outcome (2026-06-11)

**DONE — non-DICOM single-level SHIPPED.** `convert --to dicom --level N
<input.svs>` emits ONE conformant WSM **VOLUME** instance from a **non-DICOM
source** for one pyramid level. The source level's JPEG-baseline tiles are
copied **verbatim** (no decode/re-encode); non-JPEG codecs (JPEG 2000 etc.)
error clearly (`Phase 1 supports JPEG-baseline tile-copy only`).

### Key de-risk result: dciodvfy accepts RGB photometric with JPEG Baseline

The central uncertainty going into the slice was whether DICOM's photometric
expectations could be reconciled with the Aperio APP14/RGB colorspace quirk
(an open question logged below). The answer: **dciodvfy reports 0 errors on an
RGB-`PhotometricInterpretation` instance carrying JPEG Baseline frames.** No
forced YCbCr re-encode is needed; the verbatim Aperio RGB tiles are emitted
as-is with `PhotometricInterpretation = RGB`. This is the result that unblocks
the whole non-DICOM path.

`PhotometricInterpretation` is **marker-driven**: the writer probes the first
tile's JPEG markers (Adobe APP14 ColorTransform + chroma subsampling) and sets
**RGB** for the Aperio APP14 raw-RGB variant, `YBR_FULL_422` for subsampled
YCbCr, `YBR_FULL` for 4:4:4 YCbCr. The marker probe rejects non-8-bit precision.

### CMU-1-Small-Region.svs findings (the probe target)

- **Adobe APP14, ColorTransform = 0 → raw RGB** (the classic Aperio variant).
- **4:4:4** (no chroma subsampling).
- **Marker order: SOF appears BEFORE APP14** — the probe must not assume APP14
  precedes the frame header; it scans for both.
- **No embedded ICC profile** — so the **sRGB-synthesis** path is the one
  exercised on the CI fixture. ICC carried-or-synthesized closes the P0 Type 1C
  `ICCProfile` gap: source ICC is carried when present, a canonical sRGB profile
  is embedded when absent.

### Fixes / mechanics this slice pinned down

- **Odd-length frame even-length padding.** A source JPEG frame of odd byte
  length gets a trailing `0x00` pad to satisfy DICOM's even-length
  encapsulated-fragment rule. Pixel content is unchanged — decoders stop at EOI.
- **Pixel round-trip safety net.** Beyond `dciodvfy` (structural), a test
  decodes the emitted DICOM honoring its `PhotometricInterpretation` and compares
  to the source's decode — confirms **byte-identical RGB**, i.e. the colorspace
  is correct, not just structurally valid. `make dicom-validate` now runs both
  the DICOM→DICOM and SVS→DICOM paths; DICOM→DICOM output is byte-identical to P0.

### Phase-1 limitations / remaining slices

- **Single level per invocation** — **full pyramid** (one instance per level in
  a Series) is the next slice, not yet built.
- **JPEG-baseline only** — JPEG 2000 / other codecs are a later slice. Caveat:
  the gate keys on the *codec* (JPEG); a *progressive*-JPEG source would pass the
  gate but is not baseline (not expected for WSI tiles, not separately rejected —
  the marker probe does reject non-8-bit precision).
- **ImageType ORIGINAL on level 0.** Level 0 of a non-DICOM source is labelled
  `ImageType ORIGINAL` (assumes the level is the native acquisition); a source
  whose level 0 is itself a derivative would be mislabelled — no general way to
  know provenance from the pyramid alone.
- **Identity is anonymous/synthetic** (carried over from Phase 0).

## Open questions for the eventual spec

- Transfer syntax policy: tile-copy (reuse source JPEG) vs. re-encode; which
  syntaxes to emit; how to reconcile the Aperio APP14/colorspace quirk with
  DICOM's photometric expectations (`YBR_FULL_422`).
- UID root: org-registered vs. generated; how Frame of Reference is assigned.
- Identity/provenance defaults when the source slide is anonymous.
- One-instance-per-level vs. when Concatenation becomes mandatory (instance size
  limits).

## Validation harness (itself real work)

1. opentile-go DICOM **read-back** (we already have the reader — round-trip).
2. External validator: `dcmtk` `dciodvfy` (or pydicom + highdicom checks).
3. Viewer interop (the real bar): OHIF/Slim or similar.

## References (read, per the no-guessing rule)

- wsi2dcm source (Apache-2.0) — the portable C++ blueprint.
- highdicom `hd.wsi` (MIT) — clearest WSM IOD structure.
- DICOM Part 3 (IOD modules), Part 5 (encapsulation), Supplement 145.
- opentile-go `formats/dicom/*` — our existing read model (TILED_FULL/SPARSE,
  encapsulated frame walking, PixelSpacing→MPP).
