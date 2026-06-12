# DICOM-WSI writer — Phase 2: associated images as separate instances — Design

**Status:** approved design, pre-plan.
**Date:** 2026-06-12
**Predecessors (all shipped to `main`):** Phase 0 (DICOM→DICOM), Phase 1 slice 1 (non-DICOM SVS), slice 2 (full pyramid, multi-instance Series), slice 3 (JPEG 2000).

## Goal

Emit the slide's **associated images** (label, macro, overview, thumbnail) as
separate single-frame DICOM WSM instances in the **same Series** as the pyramid
VOLUME instances. Today `convert --to dicom` emits only the resolution pyramid;
the associated images are dropped. This completes the slide → DICOM picture.

## Locked scope decisions (from brainstorming)

1. **Each associated image → one single-frame WSM instance** in the same Study/
   Series/FrameOfReference as the pyramid (shared UIDs, continuing InstanceNumber),
   per the Grundium golden.
2. **Type → `ImageType[2]` flavor:** `label`→`LABEL`, `overview`→`OVERVIEW`,
   `macro`→`OVERVIEW` (no DICOM MACRO flavor), `thumbnail`→`THUMBNAIL`.
3. **Default-on in full-pyramid mode**, skipped by the existing `--no-associated`
   flag. `--level N` (single-instance) mode emits **no** associated images.
4. **Verbatim codec tile-copy** of the whole associated image as one frame (JPEG
   or JPEG 2000, via the same codec probe). Non-JPEG/JP2K associated images are
   **skipped with a logged warning** (not fail-fast — they're auxiliary).
5. **Architecture:** generalize the assembler to a pure builder over a per-
   instance `instanceSpec`; both the pyramid-level and associated-image callers
   build a spec. (`ImageDescriptor` is folded into `instanceSpec`.)

## Verified facts (probed this session)

- Grundium golden has `label`/`overview`/`thumbnail` DICOM instances: same
  `SOPClassUID` (VLWholeSlideMicroscopyImage) and **same SeriesInstanceUID /
  StudyInstanceUID / FrameOfReferenceUID** as the pyramid; `ImageType` =
  `DERIVED\PRIMARY\{LABEL|OVERVIEW|THUMBNAIL}\{NONE|RESAMPLED}`; `NumberOfFrames`
  1; `Rows`/`Columns` = the image dims (single frame = whole image);
  `TotalPixelMatrixColumns/Rows` = same; full DimensionOrganization (TILED_FULL),
  OpticalPathSequence, SharedFunctionalGroupsSequence + PixelSpacing (label
  0.026 mm), ImagedVolume; `SpecimenLabelInImage` = `YES` (label, overview) /
  `NO` (thumbnail); `InstanceNumber` continues the series (label 2, overview 3,
  thumbnail 4 — interleaved with the pyramid levels).
- `source.Source.Associated() []AssociatedImage`; `AssociatedImage` exposes
  `Type() string` (`label`/`macro`/`thumbnail`/`overview`), `Size() image.Point`,
  `Bytes() ([]byte, error)` (the whole compressed image), `Compression()
  source.Compression`.
- The SVS fixtures carry `label`/`macro`/`thumbnail` associated images (JPEG);
  the Grundium DICOM source carries `label`/`overview`/`thumbnail`.

## Architecture

`assembleWSMDataset` is generalized: it no longer reads `src.Levels()[level]`.
It takes `src` (shared slide metadata: patient/study/equipment/specimen/dates/
make-model/MPP) plus an `instanceSpec` carrying everything instance-specific. Two
callers build a spec: `writeInstance` (a pyramid level) and the new
`writeAssociated` (one associated image). A shared `levelSpatial` helper computes
PixelSpacing/ImagedVolume identically for both (downsample relative to L0).

### Component 1 — `instanceSpec` (replaces the geometry-from-level + `ImageDescriptor`)

```go
type instanceSpec struct {
	// geometry
	Size      image.Point // TotalPixelMatrix (cols=X, rows=Y)
	TileSize  image.Point // Rows=Y, Columns=X (the frame size)
	NumFrames int

	// flavor
	ImageType            []string // 4 elements; [2] = VOLUME|LABEL|OVERVIEW|THUMBNAIL
	SpecimenLabelInImage string   // "YES" | "NO"
	InstanceNumber       int

	// spatial (mm; computed by the caller, DS-formatted in the assembler)
	PixelSpacingX, PixelSpacingY float64
	ImagedVolumeW, ImagedVolumeH float64

	// codec / color (formerly ImageDescriptor)
	TransferSyntax  string
	Photometric     string
	SamplesPerPixel int
	ICCProfile      []byte
	Lossy           bool
	LossyMethod     string
	LossyRatio      float64
}
```

`assembleWSMDataset(src source.Source, uids UIDSet, spec instanceSpec) (dicom.Dataset, error)`:
- Shared metadata (group 0008/0010/0018/0020 identity, dates, specimen, optical
  path, equipment) read from `src.Metadata()` as today.
- `ImageType`/`FrameType` ← `spec.ImageType`; `SamplesPerPixel` ← `spec.SamplesPerPixel`;
  `PhotometricInterpretation` ← `spec.Photometric`; `TransferSyntaxUID` ←
  `spec.TransferSyntax`; `InstanceNumber` ← `spec.InstanceNumber`;
  `TotalPixelMatrixColumns/Rows` ← `spec.Size`; `Rows/Columns` ← `spec.TileSize`;
  `NumberOfFrames` ← `spec.NumFrames`; `SpecimenLabelInImage` ← `spec.SpecimenLabelInImage`;
  `PixelSpacing` ← `formatDS(spec.PixelSpacingY)\formatDS(spec.PixelSpacingX)`;
  `ImagedVolumeWidth/Height` ← `spec.ImagedVolumeW/H`.
- `ImageOrientationSlide` stays the standard `0\1\0\1\0\0` for all instances.
- The Type-1C omissions (lossy ratio/method when `!Lossy`; PlanarConfiguration
  when `SamplesPerPixel==1`) are unchanged.

### Component 2 — `levelSpatial` helper + the callers

```go
// levelSpatial returns PixelSpacing (mm) and ImagedVolume extent (mm) for an
// image of pixel size `size` that is a downsampled view of an L0 of pixel size
// `l0` at base MPP (µm/px). Reused for pyramid levels and associated images.
func levelSpatial(l0, size image.Point, mppX, mppY float64) (psX, psY, imgW, imgH float64)
```
(downsample = `l0/size`, `psX = mppX*dsX/1000`, `imgW = l0.X*mppX/1000` — the
constant L0 extent; 0 when MPP unknown.)

- **`writeInstance(w, src, level, shared, instanceNumber)`** (pyramid level):
  builds a spec from `lvl := src.Levels()[level]` — Size/TileSize/NumFrames from
  the level, ImageType per level (`ORIGINAL…NONE` @0 else `DERIVED…RESAMPLED`),
  SpecimenLabelInImage `NO`, spatial via `levelSpatial(L0,lvl.Size,…)`, codec/
  color via `buildDescriptor`, LossyRatio from the compressed byte total.
- **`writeAssociated(w, src, a, shared, instanceNumber)`** (new): for one
  `AssociatedImage` — gate `a.Compression()` to JPEG/JP2K (else return a sentinel
  "skip" error); `bytes := a.Bytes()`; probe codec/photometric via the same logic
  `buildDescriptor` uses (factored so both call it on raw bytes); Size=TileSize=
  `a.Size()`, NumFrames=1; ImageType `{DERIVED,PRIMARY,<flavor>,<NONE|RESAMPLED>}`
  (RESAMPLED for thumbnail, NONE otherwise); SpecimenLabelInImage `YES` for
  label/overview, `NO` for thumbnail; spatial via `levelSpatial(L0,a.Size,…)`;
  encapsulate `bytes` as **one** frame (even-length pad reused). LossyRatio from
  the single frame's byte count.

`buildDescriptor`'s codec-probe + photometric/transfer-syntax derivation is
factored into a helper `codecColor(tileBytes, comp source.Compression) (…, error)`
so `writeAssociated` reuses it without a `Level`.

### Component 3 — `WritePyramid` extension + CLI

`WritePyramid(src, opts Options, newWriter func(name string) (io.WriteCloser, error))`:
the factory key changes from `level int` to a `name string` (e.g. `"level-0"`,
`"label"`, `"overview"`). `Options` gains `Associated bool`. After the N levels
(InstanceNumber 1..N), if `opts.Associated`, iterate `src.Associated()`; for each,
call `writeAssociated` with the **same `sharedUIDs`** and InstanceNumber N+1, N+2,
…, to writer name = the associated `Type()`. A `writeAssociated` "skip" sentinel
(unsupported codec) is logged (`slog.Warn`) and the image skipped; the pyramid
still completes.

CLI (`cmd/wsitools/convert_dicom.go`): the pyramid factory creates
`<tmp>/<name>.dcm`; pass `Options{Associated: !cvNoAssociated}`. `--level N`
(single-instance) is unchanged (no associated). `WriteVolumeInstance` (single
instance) keeps its signature (it wraps `writeInstance`).

## Error handling

| Condition | Behavior |
|---|---|
| associated image codec not JPEG/JP2K | skip with `slog.Warn` (pyramid still valid) |
| `a.Bytes()` / encapsulation / codec-probe error on an associated image | skip with `slog.Warn` |
| a pyramid **level** fails | fail-fast (unchanged — levels are essential) |
| `--no-associated` | emit no associated images |

## Testing

1. **`writeAssociated` unit** (gated, Grundium DICOM + an SVS fixture with
   associated images): emit each associated image to an in-memory buffer; parse
   back and assert `ImageType[2]` flavor (LABEL/OVERVIEW/THUMBNAIL), shared
   SeriesInstanceUID + FrameOfReferenceUID with a pyramid instance, NumberOfFrames
   1, SpecimenLabelInImage per type, SamplesPerPixel/Photometric sane.
2. **`WritePyramid` + associated unit:** emit a full pyramid with `Associated:
   true` from the Grundium source; assert the named instances (`label`, `overview`,
   `thumbnail`) exist, all share the pyramid's Series/FrameOfReference UID, and
   InstanceNumbers are unique and contiguous across levels + associated.
3. **dciodvfy** (`make dicom-validate`): the full-pyramid emit now also writes
   `<type>.dcm`; validate **every** file (levels + associated) → 0 errors each.
   Run on the Grundium pyramid (associated present) — confirms associated-instance
   conformance.
4. **CLI integration:** `convert --to dicom -o <dir>` on a fixture with associated
   images produces `<type>.dcm` alongside `level-<n>.dcm`; each `source.Open`s as
   `dicom`. `--no-associated` produces only `level-<n>.dcm`.
5. **Regression:** the single-instance, full-pyramid, JPEG/JP2K, and P0 paths stay
   green / structurally unchanged (the `instanceSpec` refactor reproduces the
   level instances' prior emitted attributes — verified by the existing
   dciodvfy/round-trip/PerLevel tests).

## Success criteria

- `convert --to dicom -o <dir> <slide-with-associated>` emits the pyramid
  `level-<n>.dcm` **plus** `label.dcm`/`overview.dcm`/`thumbnail.dcm` (as present),
  every file passing `dciodvfy` (0 errors), all sharing the Series/FrameOfReference
  UID, with correct `ImageType` flavors and InstanceNumbers.
- `--no-associated` and `--level N` emit no associated images.
- Pyramid-level output is structurally unchanged; non-JPEG/JP2K associated images
  are skipped with a warning, not a failure.

## Out of scope (later)

- The golden's rotated label `ImageOrientationSlide` (`1\0\0\0\-1\0`) — we emit
  the standard orientation.
- Faithful label PixelSpacing (we emit a slide-derived nominal value).
- HTJ2K / 16-bit associated images; `.jp2`-boxed inputs.
- The pre-existing P0 DICOM-source codec-mislabeling bug (HTJ2K DICOM source).

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `internal/dicomwriter/dataset.go` | modify | `instanceSpec`; `assembleWSMDataset(src,uids,spec)`; `levelSpatial` helper |
| `internal/dicomwriter/dicomwriter.go` | modify | `writeInstance` builds a spec; `writeAssociated`; `codecColor` helper; `WritePyramid` factory-by-name + `Associated` opt |
| `internal/dicomwriter/dicomwriter.go` (Options) | modify | `Options{Associated bool}` |
| `internal/dicomwriter/associated_test.go` | new | `writeAssociated` + pyramid-with-associated unit tests |
| `internal/dicomwriter/dataset_test.go` | modify | update call sites to `assembleWSMDataset(src,uids,spec)` |
| `internal/dicomwriter/dicomwriter_test.go` | modify | `WritePyramid` factory-by-name signature |
| `cmd/wsitools/convert_dicom.go` | modify | factory-by-name; `Options{Associated:!cvNoAssociated}` |
| `cmd/wsitools/convert_dicom_test.go` | modify | associated-instances CLI integration + `--no-associated` |
| `Makefile` | modify | `dicom-validate` validates the associated `<type>.dcm` too |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md` | modify | document the slice |
