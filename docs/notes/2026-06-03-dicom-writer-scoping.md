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
