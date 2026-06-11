# DICOM-WSI writer — Phase 0 spike — design

> Status: **approved design** (brainstormed 2026-06-11). Next: writing-plans.
> Branch off `main`; never implement on `main`.
> First phase of the `convert --to dicom` target (scoping:
> `docs/notes/2026-06-03-dicom-writer-scoping.md`). P0 de-risks the WSM IOD; P1+
> get their own spec/plan cycles.

## Goal

Prove a **conformant** DICOM VL Whole Slide Microscopy Image (WSM; SOP class
`1.2.840.10008.5.1.4.1.1.77.1.6`) can be emitted from wsitools. `convert --to
dicom -o out.dcm <input.dcm>` reads **one pyramid level** of a DICOM source via
opentile-go and writes it back as a **single** WSM VOLUME instance, copying the
JPEG frames **verbatim** (no decode/re-encode). Success = passes an external IOD
validator with zero errors AND round-trips through opentile-go.

The spike answers the open question from scoping: *is the Go port a few hundred
lines or a swamp?* It exercises the full write path (IOD assembly + encapsulated
multi-frame + UID/geometry) on conformant input, isolating the JPEG-colorspace
reconciliation (deferred to P1).

## Background

- **Library:** `github.com/suyashkumar/dicom` (already an indirect dep via
  opentile-go's DICOM reader) handles the byte machinery — datasets/elements,
  explicit-VR, file-meta, and **encapsulated multi-frame PixelData** (verified:
  `write.go` emits a Basic Offset Table + one fragment per frame from
  `PixelDataInfo{IsEncapsulated:true, Frames, Offsets}`; `frame.EncapsulatedFrame{Data []byte}`
  holds compressed JPEG bytes). We provide the elements + the compressed frames;
  the library frames the encapsulation.
- **Golden template:** `sample_files/dicom/scan_621_grundium_dicom/` — a real
  Grundium WSM set. The VOLUME instances show the minimal conformant attribute
  set: **anonymous identity** (empty `PatientName`/DOB/Sex; `PatientID`=scan
  name; empty container/specimen sequences), **TILED_FULL** (no Per-Frame
  Functional Groups — positions implicit), `YBR_FULL_422` JPEG, one
  `OpticalPathSequence`, `PixelSpacing` in **Shared** Functional Groups. P0
  mirrors this attribute set.
- **No clean-room needed:** WSM IOD policy is ported from permissively-licensed
  references — `wsidicomizer`/`wsidicom` (Sectra, Apache-2.0; same lineage as
  opentile), `highdicom` (MIT), `wsi2dcm` (Apache-2.0) — with the Grundium
  instance as ground truth.

## Scope (decided)

**In:**
- `convert --to dicom -o out.dcm <input>` where `<input>` is a **DICOM** source.
- Emit **one** WSM **VOLUME** instance for **one** pyramid level (default L0; a
  `--level N` flag selects the level for the spike).
- **TILED_FULL**, single optical path, single focal plane, brightfield RGB,
  **JPEG-Baseline tile-copy** (frames copied verbatim).
- Anonymous identity; generated UIDs under `2.25.<uuid>`.
- Validation harness (see below).

**Out (P1+ / later specs):**
- Full pyramid / multi-instance sets / one-instance-per-level; label/overview
  instances; Concatenations.
- Non-DICOM sources (SVS/TIFF → JPEG-colorspace/APP14 reconciliation).
- TILED_SPARSE; re-encode/transfer-syntax conversion; fluorescence /
  multi-channel / z-stack.

## Architecture

### One focused package: `internal/dicomwriter`
P0 is a single pure-Go package + a cmd file. (The scoping note's 4-package split
— dataset / IOD / geometry / encapsulation — is deferred until P1+ justifies it.
YAGNI for a spike.) The package exposes one entry point:

```go
// WriteVolumeInstance writes a single WSM VOLUME instance for src level `level`
// to w, copying JPEG frames verbatim. src must be a DICOM, TILED_FULL,
// JPEG-encapsulated brightfield source.
func WriteVolumeInstance(w io.Writer, src source.Source, level int, opts Options) error
```

Internally three concerns (functions, not yet packages):
1. **IOD assembly** — build the WSM `dicom.Dataset` (element list), mirroring the
   Grundium golden attribute set, with values derived from the source.
2. **Frame encapsulation** — source raw tiles → encapsulated `PixelData` element
   in TILED_FULL frame order.
3. **UID generation** — Study/Series/SOP/FrameOfReference UIDs.

### IOD assembly (the ported policy)
Build a `dicom.Dataset{Elements: []*dicom.Element{...}}` via `dicom.NewElement(t,
data)` (and the WSM-specific tags via `tag.Tag{group,elem}` where no named const
exists; the library resolves VR from its dictionary). The minimal conformant
module set, with P0 values:

| Module / attribute | P0 value (source-derived unless noted) |
|---|---|
| File meta: MediaStorageSOPClassUID, SOPInstanceUID, TransferSyntaxUID | WSM SOP class; generated SOP UID; `1.2.840.10008.1.2.4.50` (JPEG Baseline) |
| SOP Common: SOPClassUID/SOPInstanceUID | as above |
| Patient: Name/ID/DOB/Sex | empty (Type-2) / `PatientID` from source or `"WSITOOLS"`; anonymous |
| General Study: StudyInstanceUID, StudyID, dates | generated UID; minimal |
| General/WSM Series: Modality `SM`, SeriesInstanceUID, SeriesNumber | generated |
| Frame of Reference: FrameOfReferenceUID | generated |
| General Equipment: Manufacturer/Model/Software | `wsitools` / Version |
| Image Pixel: SamplesPerPixel 3, PhotometricInterpretation, PlanarConfig 0, Rows/Cols (=tile dims), Bits 8/8/7, PixelRepresentation 0 | from source tile geometry + JPEG (`YBR_FULL_422`) |
| WSM Image: ImageType `DERIVED\PRIMARY\VOLUME\NONE`, NumberOfFrames, ImagedVolume*, TotalPixelMatrixColumns/Rows, TotalPixelMatrixOriginSequence, ImageOrientationSlide `0\1\0\1\0\0`, DimensionOrganizationType `TILED_FULL`, FocusMethod `AUTO`, ExtendedDepthOfField `NO`, SpecimenLabelInImage `NO` | from source dims + MPP; constants where structural |
| Multi-frame Functional Groups: SharedFunctionalGroupsSequence → PixelMeasuresSequence (PixelSpacing) + WholeSlideMicroscopyImageFrameTypeSequence | PixelSpacing from MPP (mm); FrameType `DERIVED\PRIMARY\VOLUME\NONE` |
| Optical Path: OpticalPathSequence (one path, brightfield illuminator/color codes, ObjectiveLensNA), NumberOfOpticalPaths 1, TotalPixelMatrixFocalPlanes 1 | NA from source mag if available; coded constants |
| Specimen: ContainerIdentifier, SpecimenDescriptionSequence (Type `Microscope slide` SCT 433466003) | minimal coded |
| Acquisition Context: AcquisitionContextSequence | empty (Type-2) |
| LossyImageCompression `01`, LossyImageCompressionRatio/Method | from source (JPEG) |
| Lossy compression, ICC profile | carry ICC if source exposes it |

(The exact required-attribute list is finalized against `dciodvfy` during
implementation — the validator IS the spec for conformance.)

### Frame encapsulation (tile-copy)
For the chosen level: iterate the tile grid in **TILED_FULL order** (row-major,
column fastest: `frameIndex = ty*gridX + tx`), reading each tile's **raw
compressed JPEG** via `lvl.TileInto(tx,ty,buf)` (opentile returns the
encapsulated frame bytes for a DICOM source — no decode). Build
`PixelDataInfo{IsEncapsulated:true, Frames:[]*frame.Frame{{EncapsulatedData:
frame.EncapsulatedFrame{Data: tileBytes}}, ...}, Offsets:[...]}` and emit it as
the `PixelData` element. `NumberOfFrames = gridX*gridY`. (Confirm the exact
`PixelDataInfo`/`NewElement(tag.PixelData, …)` construction against
`suyashkumar/dicom` during implementation.)

### UID generation
Generate Study/Series/SOP/FrameOfReference UIDs as `2.25.<128-bit-uuid-as-int>`
(the DICOM-blessed UUID-derived form; no registered org root needed for a spike).

### CLI
`runConvert` (`convert.go`) gains `case "dicom": return runConvertDICOM(cmd,
input, start)`. `runConvertDICOM` opens the source, validates it's DICOM +
TILED_FULL + JPEG-encapsulated (else a clear error), resolves `--level` (default
0), opens `-o` for write, calls `dicomwriter.WriteVolumeInstance`. Output is a
single `.dcm` file.

## Data flow

```
convert --to dicom --level N -o out.dcm input.dcm
  source.Open(input.dcm)                 (opentile-go DICOM reader)
  validate: DICOM + TILED_FULL + JPEG-encapsulated   (else error)
  lvl = src.Levels()[N]
  ds = assembleWSMDataset(src, lvl, generated UIDs)   (mirror Grundium golden)
  ds.PixelData = encapsulate(lvl tiles in TILED_FULL order)   (verbatim copy)
  dicom.Write(out.dcm, ds)               (suyashkumar/dicom: preamble+meta+ds)
```

## Error handling

| Condition | Behavior |
|---|---|
| source not DICOM | error: `--to dicom requires a DICOM source (P0)` |
| source not TILED_FULL / not JPEG-encapsulated | error naming the limitation (P0 scope) |
| `--level` out of range | error |
| multi-optical-path / multi-focal-plane source | error (P0 single-plane) |
| write/encapsulation failure | error; no partial output |

## Validation harness

1. **opentile-go read-back (automatable, Go test):** `source.Open(out.dcm)`
   succeeds; reports a level with matching dimensions/tile geometry; the level's
   raw tiles are **byte-identical** to the source level's (verifies tile-copy).
2. **External IOD validator (separate step, needs a tool):** `dciodvfy out.dcm`
   (install `dicom3tools` via brew) → **0 errors**; cross-check with
   `pip install highdicom` + pydicom parse. Run as a `make dicom-validate` target
   / CI step, not a Go unit test.
3. **Golden diff:** `dcmdump` our output vs the source Grundium instance —
   geometry/photometric/structural attrs match; UIDs + identity differ by design;
   frame bytes identical.
4. **Viewer interop (manual stretch):** open in OHIF/Slim.

## File structure

| Path | Responsibility |
|---|---|
| `internal/dicomwriter/dicomwriter.go` (new) | `WriteVolumeInstance`, IOD assembly, encapsulation, UID gen |
| `internal/dicomwriter/dicomwriter_test.go` (new) | gated: emit from Grundium `pyr04` → opentile read-back + byte-identical tiles + attrs present |
| `cmd/wsitools/convert_dicom.go` (new) | `runConvertDICOM` + `--level` flag |
| `cmd/wsitools/convert.go` (modify) | `case "dicom"` dispatch |
| `Makefile` (modify) | `dicom-validate` target (dciodvfy/highdicom) |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md` | document P0 (single VOLUME instance, DICOM→DICOM only) |

## Testing

`make test` (`-race`); integration gated by `WSI_TOOLS_TESTDIR`.

- **Gated Go test** on `sample_files/dicom/scan_621_grundium_dicom` level `pyr04`
  (4096×4096, 64 frames — small, fast): emit → `source.Open` the output →
  assert format DICOM, one level with matching dims/tile size/grid, **raw tiles
  byte-identical** to the source level, and the required attributes parse back
  (re-read via `suyashkumar/dicom`, assert SOPClassUID = WSM, NumberOfFrames,
  TotalPixelMatrix dims, DimensionOrganizationType `TILED_FULL`).
- **`make dicom-validate`** (manual/CI, needs `dciodvfy`): emit a fixture
  instance and run the external validator → 0 errors. Documented as the
  conformance gate.

## Out of scope / next phases

P1: full pyramid (instance per level), non-DICOM sources + JPEG-colorspace/APP14
reconciliation, conformant Patient/Study/Specimen population, ICC carry,
MPP/orientation from non-DICOM sources. P2: TILED_SPARSE, label/overview
instances, Concatenations. P3: fluorescence / multi-channel / z-stack. Each gets
its own spec.

## Open questions

None blocking. The exact `suyashkumar/dicom` `PixelDataInfo`/`NewElement`
construction and the precise `dciodvfy`-required attribute set are confirmed
during implementation (the validator is the conformance spec).
