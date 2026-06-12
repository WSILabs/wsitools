# DICOM-WSI writer — Phase 2 (associated images as separate instances) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Emit the slide's associated images (label/macro/overview/thumbnail) as single-frame DICOM WSM instances in the same Series as the pyramid, default-on in full-pyramid mode (`--no-associated` to skip).

**Architecture:** Generalize `assembleWSMDataset` to a pure builder over a per-instance `instanceSpec` (geometry + flavor + spatial + codec/color); the level path and a new `writeAssociated` both build a spec. `WritePyramid`'s factory is keyed by name (`level-0`, `label`, …) and emits the associated images (shared UIDs, continuing InstanceNumber) after the levels.

**Tech Stack:** Go, `github.com/suyashkumar/dicom` v1.1.0, opentile-go, `dciodvfy` (dicom3tools).

**Spec:** `docs/superpowers/specs/2026-06-12-dicom-writer-phase2-associated-images-design.md`

**Branch:** create `feat/dicom-writer-phase2-associated` off `main`. Never implement on `main`.

**Verified facts (probed this session):**
- Grundium golden associated instances share Study/Series/FrameOfReference UID with the pyramid; `ImageType` = `DERIVED\PRIMARY\{LABEL|OVERVIEW|THUMBNAIL}\{NONE|RESAMPLED}`; NumberOfFrames 1; Rows/Columns = image dims; SpecimenLabelInImage YES (label/overview) / NO (thumbnail).
- `source.Source.Associated() []source.AssociatedImage`; `AssociatedImage` has `Type() string` (`label`/`macro`/`thumbnail`/`overview`), `Size() image.Point`, `Bytes() ([]byte, error)`, `Compression() source.Compression`.
- Current `internal/dicomwriter/dicomwriter.go`: `WriteVolumeInstance(w,src,level,_Options)`, `WritePyramid(src,_Options, newWriter func(level int)(io.WriteCloser,error))`, `writeInstance(w,src,level,shared)`, `buildDescriptor(src,level,lossyRatio) (ImageDescriptor,error)`. `ImageDescriptor` carries `TransferSyntax/Photometric/SamplesPerPixel/ImageType/ICCProfile/Lossy/LossyMethod/LossyRatio`. `encapsulatePixelData(src,level) (*dicom.Element,int64,error)`.
- Current `dataset.go`: `assembleWSMDataset(src,level,uids,desc ImageDescriptor)` computes geometry from `src.Levels()[level]` + L0 (downsample spatial). `formatDS` helper (≤16-char DS) exists. `jpegBaselineTS`/`jp2kLosslessTS`/`jp2kTS` consts. Constants `wsmSOPClassUID`, `writerSoftware`, `srgbICCProfile`.
- `cmd/wsitools/convert_dicom.go`: `writeDICOMPyramid` factory `func(level int)(io.WriteCloser,error)` → `level-<n>.dcm`; global `cvNoAssociated` (the `--no-associated` flag) exists.

---

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `internal/dicomwriter/dataset.go` | modify | `ImageDescriptor`→codec/color only; new `instanceSpec`; `assembleWSMDataset(src,uids,spec)`; `levelSpatial` helper |
| `internal/dicomwriter/dicomwriter.go` | modify | `codecColor` (factored probe); `buildDescriptor` (no ImageType); `writeInstance` builds a spec; `writeAssociated`; `WritePyramid` factory-by-name + `Options{Associated}` |
| `internal/dicomwriter/dataset_test.go` | modify | call-site updates to `assembleWSMDataset(src,uids,spec)` |
| `internal/dicomwriter/dicomwriter_test.go` | modify | `WritePyramid` factory-by-name signature |
| `internal/dicomwriter/associated_test.go` | new | `writeAssociated` + pyramid-with-associated unit tests |
| `cmd/wsitools/convert_dicom.go` | modify | factory-by-name; `Options{Associated:!cvNoAssociated}` |
| `cmd/wsitools/convert_dicom_test.go` | modify | associated-instances CLI integration + `--no-associated` |
| `Makefile` | modify | `dicom-validate` validates the associated `<type>.dcm` |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md` | modify | document the slice |

---

## Task 1: Generalize the assembler to `instanceSpec` (refactor; no behavior change)

**Files:** modify `internal/dicomwriter/dataset.go`, `internal/dicomwriter/dicomwriter.go`, `internal/dicomwriter/dataset_test.go`. One commit (interlocking signatures). The success bar is: **the existing level/pyramid tests + dciodvfy stay green** — the emitted level instances are unchanged.

- [ ] **Step 1: `dataset.go` — add `image` import, redefine `ImageDescriptor` (codec/color only), add `instanceSpec` + `levelSpatial`.**

Add `"image"` to the import block. Replace the `ImageDescriptor` struct (and its doc comment) with:

```go
// ImageDescriptor carries the codec/colorspace attributes derived from a tile's
// codestream (independent of geometry or image flavor).
type ImageDescriptor struct {
	TransferSyntax  string // 1.2.840.10008.1.2.4.{50|90|91}
	Photometric     string // RGB | YBR_FULL_422 | YBR_FULL | YBR_ICT | YBR_RCT | MONOCHROME2
	SamplesPerPixel int    // 1 or 3
	ICCProfile      []byte // carried or synthesized; non-empty for color
	Lossy           bool   // LossyImageCompression "01" (true) vs "00" (false)
	LossyMethod     string // "ISO_10918_1" | "ISO_15444_1" (empty when lossless)
	LossyRatio      float64
}

// instanceSpec is everything that varies per WSM instance (pyramid level or
// associated image). assembleWSMDataset is a pure builder over src's shared slide
// metadata + this spec.
type instanceSpec struct {
	Size      image.Point // TotalPixelMatrix (X=cols, Y=rows)
	TileSize  image.Point // Rows=Y, Columns=X (the frame size)
	NumFrames int

	ImageType            []string // 4 elements; [2] = VOLUME|LABEL|OVERVIEW|THUMBNAIL
	SpecimenLabelInImage string   // "YES" | "NO"
	InstanceNumber       int

	PixelSpacingX, PixelSpacingY float64 // mm
	ImagedVolumeW, ImagedVolumeH float64 // mm

	ImageDescriptor // embedded codec/color (promotes TransferSyntax/Photometric/…)
}

// levelSpatial returns PixelSpacing (mm) and the constant ImagedVolume extent (mm)
// for an image of pixel size `size` viewed as a downsample of an L0 of size `l0`
// at base MPP (µm/px). Shared by pyramid levels and associated images. MPP 0 →
// spacing/extent 0.
func levelSpatial(l0, size image.Point, mppX, mppY float64) (psX, psY, imgW, imgH float64) {
	dsX, dsY := 1.0, 1.0
	if size.X > 0 {
		dsX = float64(l0.X) / float64(size.X)
	}
	if size.Y > 0 {
		dsY = float64(l0.Y) / float64(size.Y)
	}
	psX = mppX * dsX / 1000.0
	psY = mppY * dsY / 1000.0
	imgW = float64(l0.X) * mppX / 1000.0
	imgH = float64(l0.Y) * mppY / 1000.0
	return psX, psY, imgW, imgH
}
```

- [ ] **Step 2: `dataset.go` — replace the `assembleWSMDataset` function** (from `func assembleWSMDataset(...` through its closing `}`) with this version. It drops `level`, the range check, and all geometry/spatial computation; it reads those from `spec`. Everything else (boilerplate, ordering, Type-1C omissions) is identical.

```go
func assembleWSMDataset(src source.Source, uids UIDSet, spec instanceSpec) (dicom.Dataset, error) {
	md := src.Metadata()

	now := time.Now().UTC()
	acq := md.AcquisitionDateTime
	if acq.IsZero() {
		acq = now
	}
	contentDA := now.Format("20060102")
	contentTM := now.Format("150405")
	acqDT := acq.Format("20060102150405")

	serial := md.SerialNumber
	if serial == "" {
		serial = writerSoftware
	}

	ratioStr := fmt.Sprintf("%.4g", spec.LossyRatio)
	lossyFlag := "00"
	if spec.Lossy {
		lossyFlag = "01"
	}

	var firstErr error
	mk := func(t tag.Tag, v any) *dicom.Element {
		e, err := dicom.NewElement(t, v)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("NewElement(%v): %w", t, err)
		}
		return e
	}
	codeItem := func(value, scheme, meaning string) []*dicom.Element {
		return []*dicom.Element{
			mk(tag.CodeValue, []string{value}),
			mk(tag.CodingSchemeDesignator, []string{scheme}),
			mk(tag.CodeMeaning, []string{meaning}),
		}
	}

	manufacturer := md.Make
	if manufacturer == "" {
		manufacturer = writerSoftware
	}
	model := md.Model
	if model == "" {
		model = writerSoftware
	}

	opticalPathItem := []*dicom.Element{
		mk(tag.IlluminationTypeCodeSequence, [][]*dicom.Element{
			codeItem("111744", "DCM", "Brightfield illumination"),
		}),
	}
	if len(spec.ICCProfile) > 0 {
		opticalPathItem = append(opticalPathItem, mk(tag.ICCProfile, spec.ICCProfile))
	}
	opticalPathItem = append(opticalPathItem,
		mk(tag.OpticalPathIdentifier, []string{"0"}),
		mk(tag.IlluminationColorCodeSequence, [][]*dicom.Element{
			codeItem("414298005", "SCT", "Full Spectrum"),
		}),
	)

	elems := []*dicom.Element{
		// ---- File-meta (group 0002) ----
		mk(tag.FileMetaInformationVersion, []byte{0x00, 0x01}),
		mk(tag.MediaStorageSOPClassUID, []string{wsmSOPClassUID}),
		mk(tag.MediaStorageSOPInstanceUID, []string{uids.SOP}),
		mk(tag.TransferSyntaxUID, []string{spec.TransferSyntax}),
		mk(tag.ImplementationClassUID, []string{NewUID()}),

		// ---- General / SOP Common (group 0008) ----
		mk(tag.ImageType, spec.ImageType),
		mk(tag.SOPClassUID, []string{wsmSOPClassUID}),
		mk(tag.SOPInstanceUID, []string{uids.SOP}),
		mk(tag.StudyDate, []string{contentDA}),
		mk(tag.ContentDate, []string{contentDA}),
		mk(tag.AcquisitionDateTime, []string{acqDT}),
		mk(tag.StudyTime, []string{contentTM}),
		mk(tag.ContentTime, []string{contentTM}),
		mk(tag.AccessionNumber, []string{}),
		mk(tag.Modality, []string{"SM"}),
		mk(tag.Manufacturer, []string{manufacturer}),
		mk(tag.ReferringPhysicianName, []string{}),
		mk(tag.ManufacturerModelName, []string{model}),
		mk(tag.VolumetricProperties, []string{"VOLUME"}),

		// ---- Patient (group 0010) — anonymous identity ----
		mk(tag.PatientName, []string{}),
		mk(tag.PatientID, []string{"WSITOOLS"}),
		mk(tag.PatientBirthDate, []string{}),
		mk(tag.PatientSex, []string{}),

		// ---- Equipment (group 0018) ----
		mk(tag.DeviceSerialNumber, []string{serial}),
		mk(tag.SoftwareVersions, []string{writerSoftware}),

		// ---- General Study / Series / FrameOfReference (group 0020) ----
		mk(tag.StudyInstanceUID, []string{uids.Study}),
		mk(tag.SeriesInstanceUID, []string{uids.Series}),
		mk(tag.StudyID, []string{}),
		mk(tag.SeriesNumber, []string{"1"}),
		mk(tag.InstanceNumber, []string{strconv.Itoa(spec.InstanceNumber)}),
		mk(tag.FrameOfReferenceUID, []string{uids.FrameOfReference}),
		mk(tag.PositionReferenceIndicator, []string{"SLIDE_CORNER"}),

		mk(tag.DimensionOrganizationSequence, [][]*dicom.Element{
			{mk(tag.DimensionOrganizationUID, []string{uids.DimensionOrg})},
		}),
		mk(tag.DimensionOrganizationType, []string{"TILED_FULL"}),

		// ---- Image Pixel / WSM frame descriptors (group 0028) ----
		mk(tag.SamplesPerPixel, []int{spec.SamplesPerPixel}),
		mk(tag.PhotometricInterpretation, []string{spec.Photometric}),
		mk(tag.PlanarConfiguration, []int{0}),
		mk(tag.NumberOfFrames, []string{strconv.Itoa(spec.NumFrames)}),
		mk(tag.Rows, []int{spec.TileSize.Y}),
		mk(tag.Columns, []int{spec.TileSize.X}),
		mk(tag.BitsAllocated, []int{8}),
		mk(tag.BitsStored, []int{8}),
		mk(tag.HighBit, []int{7}),
		mk(tag.PixelRepresentation, []int{0}),
		mk(tag.BurnedInAnnotation, []string{"NO"}),
		mk(tag.LossyImageCompression, []string{lossyFlag}),
		mk(tag.LossyImageCompressionRatio, []string{ratioStr}),
		mk(tag.LossyImageCompressionMethod, []string{spec.LossyMethod}),

		// ---- Specimen (group 0040) ----
		mk(tag.ContainerIdentifier, []string{"WSITOOLS"}),
		mk(tag.IssuerOfTheContainerIdentifierSequence, [][]*dicom.Element{}),
		mk(tag.ContainerTypeCodeSequence, [][]*dicom.Element{
			codeItem("433466003", "SCT", "Microscope slide"),
		}),
		mk(tag.AcquisitionContextSequence, [][]*dicom.Element{}),
		mk(tag.SpecimenDescriptionSequence, [][]*dicom.Element{
			{
				mk(tag.SpecimenIdentifier, []string{"WSITOOLS"}),
				mk(tag.SpecimenUID, []string{NewUID()}),
				mk(tag.IssuerOfTheSpecimenIdentifierSequence, [][]*dicom.Element{}),
				mk(tag.SpecimenPreparationSequence, [][]*dicom.Element{}),
			},
		}),

		// ---- Whole Slide Microscopy Image (group 0048) ----
		mk(tag.ImagedVolumeWidth, []float64{spec.ImagedVolumeW}),
		mk(tag.ImagedVolumeHeight, []float64{spec.ImagedVolumeH}),
		mk(tag.ImagedVolumeDepth, []float64{1}),
		mk(tag.TotalPixelMatrixColumns, []int{spec.Size.X}),
		mk(tag.TotalPixelMatrixRows, []int{spec.Size.Y}),
		mk(tag.TotalPixelMatrixOriginSequence, [][]*dicom.Element{
			{
				mk(tag.XOffsetInSlideCoordinateSystem, []string{"0.0"}),
				mk(tag.YOffsetInSlideCoordinateSystem, []string{"0.0"}),
				mk(tag.ZOffsetInSlideCoordinateSystem, []string{"0.0"}),
			},
		}),
		mk(tag.SpecimenLabelInImage, []string{spec.SpecimenLabelInImage}),
		mk(tag.FocusMethod, []string{"AUTO"}),
		mk(tag.ExtendedDepthOfField, []string{"NO"}),
		mk(tag.ImageOrientationSlide, []string{"0", "1", "0", "1", "0", "0"}),
		mk(tag.OpticalPathSequence, [][]*dicom.Element{opticalPathItem}),
		mk(tag.NumberOfOpticalPaths, []int{1}),
		mk(tag.TotalPixelMatrixFocalPlanes, []int{1}),

		// ---- Shared Functional Groups (group 5200) ----
		mk(tag.SharedFunctionalGroupsSequence, [][]*dicom.Element{
			{
				mk(tag.PixelMeasuresSequence, [][]*dicom.Element{
					{
						mk(tag.SliceThickness, []string{"0.001"}),
						mk(tag.PixelSpacing, []string{
							formatDS(spec.PixelSpacingY),
							formatDS(spec.PixelSpacingX),
						}),
					},
				}),
				mk(tag.WholeSlideMicroscopyImageFrameTypeSequence, [][]*dicom.Element{
					{mk(tag.FrameType, spec.ImageType)},
				}),
			},
		}),
	}

	if firstErr != nil {
		return dicom.Dataset{}, firstErr
	}
	// Type 1C omissions: LossyImageCompressionRatio + Method only when "01";
	// PlanarConfiguration only when SamplesPerPixel > 1.
	mono := spec.SamplesPerPixel == 1
	if !spec.Lossy || mono {
		kept := elems[:0]
		for _, e := range elems {
			if !spec.Lossy && (e.Tag == tag.LossyImageCompressionRatio || e.Tag == tag.LossyImageCompressionMethod) {
				continue
			}
			if mono && e.Tag == tag.PlanarConfiguration {
				continue
			}
			kept = append(kept, e)
		}
		elems = kept
	}
	return dicom.Dataset{Elements: elems}, nil
}
```

Confirm `strconv` is imported in dataset.go (it is — `formatDS` uses it).

- [ ] **Step 3: `dicomwriter.go` — replace `buildDescriptor` with `codecColor` + a thinner `buildDescriptor`** (ImageType moves out; codec probe factored to take raw bytes). Replace the entire current `buildDescriptor` function with:

```go
// codecColor derives the codec/colorspace attributes from a tile/frame's
// codestream bytes. Gated to JPEG-baseline + JPEG 2000.
func codecColor(tile []byte, comp source.Compression, icc []byte, lossyRatio float64) (ImageDescriptor, error) {
	desc := ImageDescriptor{ICCProfile: icc, LossyRatio: lossyRatio}
	switch comp {
	case source.CompressionJPEG:
		info, err := Inspect(tile)
		if err != nil {
			return ImageDescriptor{}, fmt.Errorf("inspect source JPEG: %w", err)
		}
		photo, err := Photometric(info)
		if err != nil {
			return ImageDescriptor{}, fmt.Errorf("derive photometric from source JPEG: %w", err)
		}
		desc.TransferSyntax = jpegBaselineTS
		desc.Photometric = photo
		desc.SamplesPerPixel = info.Components
		desc.Lossy = true
		desc.LossyMethod = "ISO_10918_1"
	case source.CompressionJPEG2000:
		info, err := InspectJP2K(tile)
		if err != nil {
			return ImageDescriptor{}, fmt.Errorf("inspect source JPEG 2000: %w", err)
		}
		photo, err := PhotometricJP2K(info)
		if err != nil {
			return ImageDescriptor{}, fmt.Errorf("derive photometric from source JPEG 2000: %w", err)
		}
		desc.Photometric = photo
		desc.SamplesPerPixel = info.Components
		if info.Reversible {
			desc.TransferSyntax = jp2kLosslessTS
			desc.Lossy = false
			desc.LossyMethod = ""
		} else {
			desc.TransferSyntax = jp2kTS
			desc.Lossy = true
			desc.LossyMethod = "ISO_15444_1"
		}
	default:
		return ImageDescriptor{}, fmt.Errorf("unsupported codec %s (JPEG-baseline or JPEG 2000 only)", comp)
	}
	return desc, nil
}

// buildDescriptor derives the codec/color attributes for src level `level`. DICOM
// sources reuse P0's fixed JPEG-baseline values; non-DICOM levels probe the tile.
func buildDescriptor(src source.Source, level int, lossyRatio float64) (ImageDescriptor, error) {
	md := src.Metadata()
	icc := md.ICCProfile
	if len(icc) == 0 {
		icc = srgbICCProfile
	}
	if src.Format() == "dicom" {
		return ImageDescriptor{
			TransferSyntax:  jpegBaselineTS,
			Photometric:     "YBR_FULL_422",
			SamplesPerPixel: 3,
			ICCProfile:      icc,
			Lossy:           true,
			LossyMethod:     "ISO_10918_1",
			LossyRatio:      lossyRatio,
		}, nil
	}
	lvl := src.Levels()[level]
	comp := lvl.Compression()
	if comp != source.CompressionJPEG && comp != source.CompressionJPEG2000 {
		return ImageDescriptor{}, fmt.Errorf(
			"--to dicom: level %d is %s; Phase 1 supports JPEG-baseline or JPEG 2000 tile-copy only",
			level, comp)
	}
	buf := make([]byte, lvl.TileMaxSize())
	n, err := lvl.TileInto(0, 0, buf)
	if err != nil {
		return ImageDescriptor{}, fmt.Errorf("read tile (0,0) for codec probe: %w", err)
	}
	return codecColor(buf[:n], comp, icc, lossyRatio)
}
```

- [ ] **Step 4: `dicomwriter.go` — replace `writeInstance`** to build an `instanceSpec` (geometry from the level, ImageType computed here, spatial via `levelSpatial`):

```go
// writeInstance assembles + writes one WSM VOLUME instance for src level `level`
// (InstanceNumber level+1) to w, using the shared UIDs and a fresh SOPInstanceUID.
func writeInstance(w io.Writer, src source.Source, level int, shared sharedUIDs) error {
	if level < 0 || level >= len(src.Levels()) {
		return fmt.Errorf("level %d out of range (0..%d)", level, len(src.Levels())-1)
	}
	pd, compressedBytes, err := encapsulatePixelData(src, level)
	if err != nil {
		return err
	}
	lvl := src.Levels()[level]
	size := lvl.Size()
	tileSize := lvl.TileSize()
	grid := lvl.Grid()
	numFrames := grid.X * grid.Y
	uncompressed := int64(numFrames) * int64(tileSize.X) * int64(tileSize.Y) * 3
	lossyRatio := 1.0
	if compressedBytes > 0 {
		lossyRatio = float64(uncompressed) / float64(compressedBytes)
	}

	desc, err := buildDescriptor(src, level, lossyRatio)
	if err != nil {
		return err
	}

	// ImageType: a DICOM source re-emission is DERIVED at every level (P0); a
	// non-DICOM level 0 is the native acquisition (ORIGINAL), reduced levels DERIVED.
	var imageType []string
	switch {
	case src.Format() == "dicom":
		imageType = []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"}
	case level == 0:
		imageType = []string{"ORIGINAL", "PRIMARY", "VOLUME", "NONE"}
	default:
		imageType = []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"}
	}

	md := src.Metadata()
	mppX, mppY := md.MPPX, md.MPPY
	if mppX == 0 {
		mppX = md.MPP
	}
	if mppY == 0 {
		mppY = md.MPP
	}
	psX, psY, imgW, imgH := levelSpatial(src.Levels()[0].Size(), size, mppX, mppY)

	spec := instanceSpec{
		Size:                 size,
		TileSize:             tileSize,
		NumFrames:            numFrames,
		ImageType:            imageType,
		SpecimenLabelInImage: "NO",
		InstanceNumber:       level + 1,
		PixelSpacingX:        psX,
		PixelSpacingY:        psY,
		ImagedVolumeW:        imgW,
		ImagedVolumeH:        imgH,
		ImageDescriptor:      desc,
	}

	uids := UIDSet{
		SOP:              NewUID(),
		Study:            shared.Study,
		Series:           shared.Series,
		FrameOfReference: shared.FrameOfReference,
		DimensionOrg:     shared.DimensionOrg,
	}
	ds, err := assembleWSMDataset(src, uids, spec)
	if err != nil {
		return err
	}
	ds.Elements = append(ds.Elements, pd)
	return dicom.Write(w, ds)
}
```

- [ ] **Step 5: Update `dataset_test.go` call sites** — every `assembleWSMDataset(src, level, uids, ImageDescriptor{...})` becomes `assembleWSMDataset(src, uids, instanceSpec{...})`. There are THREE call sites (`TestAssembleWSMDataset`, `TestAssembleWSMDatasetLosslessOmitsLossyTags`, `TestAssembleWSMDatasetMonoOmitsPlanarConfiguration`) plus `TestPerLevelSpatialMetadata` (which builds a `desc` var and calls it twice). Convert each: move the codec/color fields into an embedded `ImageDescriptor`, and add the geometry/flavor/spatial fields the test needs.

For `TestAssembleWSMDataset` — it asserts SOPClass/Modality/TILED_FULL/NumberOfFrames(grid product)/TotalPixelMatrixColumns(size.X)/Rows(tileSize.Y)/Columns/SamplesPerPixel. Build the spec from the level it already opens (`lvl := src.Levels()[level]`):
```go
	lvl := src.Levels()[level]
	grid := lvl.Grid()
	ds, err := assembleWSMDataset(src, uids, instanceSpec{
		Size:                 lvl.Size(),
		TileSize:             lvl.TileSize(),
		NumFrames:            grid.X * grid.Y,
		ImageType:            []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		SpecimenLabelInImage: "NO",
		InstanceNumber:       1,
		PixelSpacingX:        0.000251, PixelSpacingY: 0.000251,
		ImagedVolumeW:        16.0, ImagedVolumeH: 16.0,
		ImageDescriptor: ImageDescriptor{
			TransferSyntax: jpegBaselineTS, Photometric: "YBR_FULL_422", SamplesPerPixel: 3,
			ICCProfile: src.Metadata().ICCProfile, Lossy: true, LossyMethod: "ISO_10918_1", LossyRatio: 10.0,
		},
	})
```
(Adjust the existing `wantFrames`/`size`/`tileSize` assertions in that test to read from `lvl` as before — they already compute from `src.Levels()[level]`, so they still pass since the spec mirrors the level's geometry.)

For `TestAssembleWSMDatasetLosslessOmitsLossyTags` and `…MonoOmitsPlanarConfiguration`: same shape — wrap the codec/color fields in `ImageDescriptor{…}`, and add `Size`/`TileSize`/`NumFrames: 1`/`ImageType`/`SpecimenLabelInImage`/`InstanceNumber: 1`/spatial. Use the source's L0 geometry, e.g. `lvl := src.Levels()[0]; Size: lvl.Size(), TileSize: lvl.TileSize(), NumFrames: lvl.Grid().X*lvl.Grid().Y`. Keep the lossless test's `Lossy: false`/`TransferSyntax: jp2kLosslessTS` inside the embedded `ImageDescriptor`, and the mono test's `SamplesPerPixel: 1`/`Photometric: "MONOCHROME2"` inside it.

For `TestPerLevelSpatialMetadata`: it asserts cross-level PixelSpacing scaling + constant ImagedVolume. Since the spatial values now come from the spec (not computed in the assembler), this test must compute them via `levelSpatial` and pass them in — i.e. it now tests `levelSpatial` + the assembler's emission together. Replace the `desc :=` + two `assembleWSMDataset(src, N, uids, desc)` calls with a helper that builds a per-level spec:
```go
	specFor := func(level int) instanceSpec {
		lvl := src.Levels()[level]
		md := src.Metadata()
		mppX, mppY := md.MPPX, md.MPPY
		if mppX == 0 { mppX = md.MPP }
		if mppY == 0 { mppY = md.MPP }
		psX, psY, w, h := levelSpatial(src.Levels()[0].Size(), lvl.Size(), mppX, mppY)
		g := lvl.Grid()
		return instanceSpec{
			Size: lvl.Size(), TileSize: lvl.TileSize(), NumFrames: g.X * g.Y,
			ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"}, SpecimenLabelInImage: "NO",
			InstanceNumber: level + 1, PixelSpacingX: psX, PixelSpacingY: psY, ImagedVolumeW: w, ImagedVolumeH: h,
			ImageDescriptor: ImageDescriptor{TransferSyntax: jpegBaselineTS, Photometric: "YBR_FULL_422", SamplesPerPixel: 3, ICCProfile: src.Metadata().ICCProfile, Lossy: true, LossyMethod: "ISO_10918_1", LossyRatio: 10.0},
		}
	}
	ds0, err := assembleWSMDataset(src, uids, specFor(0))
	...
	dsN, err := assembleWSMDataset(src, uids, specFor(last))
```
The existing `imagedVolumeWidth`/`pixelSpacingYX` assertions then verify `levelSpatial` produced co-registered values (constant extent, downsample-scaled spacing) — same guarantee as before, now end-to-end through the spec.

- [ ] **Step 6: Build + regression (zero behavior change)**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ ./cmd/wsitools/ -run 'DICOM|Dicom|WSM|Assemble|Encapsulat|NewUID|ConvertDICOM|Inspect|SRGB|PixelRoundTrip|PerLevel|WritePyramid|JP2K|Lossless|Mono|FormatDS|WriteVolumeInstance' -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l internal/dicomwriter/dataset.go internal/dicomwriter/dataset_test.go internal/dicomwriter/dicomwriter.go
go vet ./internal/dicomwriter/ ./cmd/wsitools/
```
Expected: ALL PASS (the level/pyramid/JP2K/round-trip tests are unchanged in behavior — the spec reproduces the prior emitted attributes). gofmt empty; vet clean.

- [ ] **Step 7: Commit**
```bash
git add internal/dicomwriter/dataset.go internal/dicomwriter/dataset_test.go internal/dicomwriter/dicomwriter.go
git commit -m "refactor(dicomwriter): instanceSpec assembler (generalize for non-level instances)

assembleWSMDataset is now a pure builder over an instanceSpec (geometry +
flavor + spatial + codec/color); writeInstance builds the spec from a level.
codecColor factors the codec probe to work on raw bytes. No behavior change to
emitted level instances.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `writeAssociated` + `WritePyramid` associated emission

**Files:** modify `internal/dicomwriter/dicomwriter.go`, `internal/dicomwriter/dicomwriter_test.go`, `cmd/wsitools/convert_dicom.go`; create `internal/dicomwriter/associated_test.go`. (`WritePyramid`'s factory signature changes → the CLI + the pyramid unit test update in the same commit so the build stays green.)

- [ ] **Step 1: `dicomwriter.go` — add `Options{Associated}`, `writeAssociated`, a skip sentinel, and the encapsulate-one-frame helper; change `WritePyramid`.** Add imports `"errors"`, `"log/slog"`, and `"github.com/suyashkumar/dicom/pkg/frame"` + `"github.com/suyashkumar/dicom/pkg/tag"` (for the single-frame PixelData element — mirror `encapsulate.go`'s hand-built element). Do NOT add `"image"` — `a.Size()` is passed through without naming `image.Point`, so importing it would be an unused-import error. Replace `type Options struct{}` and `WritePyramid` with:

```go
// Options controls the DICOM write. Associated enables emitting the slide's
// associated images (label/overview/thumbnail/…) as separate instances.
type Options struct {
	Associated bool
}

// errSkipAssociated marks an associated image that can't be tile-copied (e.g. an
// unsupported codec); WritePyramid logs and skips it rather than failing.
var errSkipAssociated = errors.New("associated image skipped")

// WritePyramid emits the full resolution pyramid (one WSM VOLUME instance per
// level) and, when opts.Associated, the slide's associated images — all sharing
// the Study/Series/FrameOfReference/DimensionOrganization UIDs, with InstanceNumber
// continuing across levels then associated images. newWriter supplies a writer per
// instance, keyed by a name ("level-0", "label", …); WritePyramid closes each.
func WritePyramid(src source.Source, opts Options, newWriter func(name string) (io.WriteCloser, error)) error {
	shared := newSharedUIDs()
	levels := src.Levels()
	for level := range levels {
		name := fmt.Sprintf("level-%d", level)
		w, err := newWriter(name)
		if err != nil {
			return fmt.Errorf("open writer for %s: %w", name, err)
		}
		werr := writeInstance(w, src, level, shared)
		cerr := w.Close()
		if werr != nil {
			return fmt.Errorf("write %s: %w", name, werr)
		}
		if cerr != nil {
			return fmt.Errorf("close %s: %w", name, cerr)
		}
	}
	if !opts.Associated {
		return nil
	}
	instanceNumber := len(levels) + 1
	for _, a := range src.Associated() {
		name := a.Type()
		w, err := newWriter(name)
		if err != nil {
			return fmt.Errorf("open writer for %s: %w", name, err)
		}
		werr := writeAssociated(w, src, a, shared, instanceNumber)
		cerr := w.Close()
		if werr != nil {
			if errors.Is(werr, errSkipAssociated) {
				slog.Warn("skipping associated image", "type", name, "reason", werr)
				continue
			}
			return fmt.Errorf("write associated %s: %w", name, werr)
		}
		if cerr != nil {
			return fmt.Errorf("close associated %s: %w", name, cerr)
		}
		instanceNumber++
	}
	return nil
}

// associatedFlavor maps a source associated-image type to its DICOM ImageType[2]
// flavor and [3] value, plus SpecimenLabelInImage.
func associatedFlavor(t string) (imageType []string, specimenLabel string) {
	switch t {
	case "label":
		return []string{"DERIVED", "PRIMARY", "LABEL", "NONE"}, "YES"
	case "thumbnail":
		return []string{"DERIVED", "PRIMARY", "THUMBNAIL", "RESAMPLED"}, "NO"
	default: // overview, macro, and any other → OVERVIEW
		return []string{"DERIVED", "PRIMARY", "OVERVIEW", "NONE"}, "YES"
	}
}

// writeAssociated emits one associated image as a single-frame WSM instance.
func writeAssociated(w io.Writer, src source.Source, a source.AssociatedImage, shared sharedUIDs, instanceNumber int) error {
	comp := a.Compression()
	if comp != source.CompressionJPEG && comp != source.CompressionJPEG2000 {
		return fmt.Errorf("%w: %s codec %s", errSkipAssociated, a.Type(), comp)
	}
	body, err := a.Bytes()
	if err != nil {
		return fmt.Errorf("%w: %s bytes: %v", errSkipAssociated, a.Type(), err)
	}
	md := src.Metadata()
	icc := md.ICCProfile
	if len(icc) == 0 {
		icc = srgbICCProfile
	}
	uncompressed := int64(a.Size().X) * int64(a.Size().Y) * 3
	lossyRatio := 1.0
	if len(body) > 0 {
		lossyRatio = float64(uncompressed) / float64(len(body))
	}
	desc, err := codecColor(body, comp, icc, lossyRatio)
	if err != nil {
		return fmt.Errorf("%w: %s codec probe: %v", errSkipAssociated, a.Type(), err)
	}

	imageType, specimenLabel := associatedFlavor(a.Type())
	mppX, mppY := md.MPPX, md.MPPY
	if mppX == 0 {
		mppX = md.MPP
	}
	if mppY == 0 {
		mppY = md.MPP
	}
	psX, psY, imgW, imgH := levelSpatial(src.Levels()[0].Size(), a.Size(), mppX, mppY)

	spec := instanceSpec{
		Size:                 a.Size(),
		TileSize:             a.Size(), // single frame = whole image
		NumFrames:            1,
		ImageType:            imageType,
		SpecimenLabelInImage: specimenLabel,
		InstanceNumber:       instanceNumber,
		PixelSpacingX:        psX,
		PixelSpacingY:        psY,
		ImagedVolumeW:        imgW,
		ImagedVolumeH:        imgH,
		ImageDescriptor:      desc,
	}

	uids := UIDSet{
		SOP:              NewUID(),
		Study:            shared.Study,
		Series:           shared.Series,
		FrameOfReference: shared.FrameOfReference,
		DimensionOrg:     shared.DimensionOrg,
	}
	ds, err := assembleWSMDataset(src, uids, spec)
	if err != nil {
		return err
	}
	pd, err := encapsulateOneFrame(body)
	if err != nil {
		return err
	}
	ds.Elements = append(ds.Elements, pd)
	return dicom.Write(w, ds)
}
```

- [ ] **Step 2: `dicomwriter.go` — add `encapsulateOneFrame`** (a single-frame version of `encapsulatePixelData`, reusing the odd-length pad). Add it (e.g. at the end of the file):

```go
// encapsulateOneFrame builds an encapsulated single-frame PixelData element from
// one compressed image (an associated image's whole-image codestream). Mirrors
// encapsulatePixelData's hand-built OB/undefined-length element; odd-length frames
// are padded to even per DICOM's fragment rule.
func encapsulateOneFrame(body []byte) (*dicom.Element, error) {
	data := append([]byte(nil), body...)
	if len(data)%2 != 0 {
		data = append(data, 0x00)
	}
	pdValue, err := dicom.NewValue(dicom.PixelDataInfo{
		IsEncapsulated: true,
		Offsets:        []uint32{0},
		Frames:         []*frame.Frame{{Encapsulated: true, EncapsulatedData: frame.EncapsulatedFrame{Data: data}}},
	})
	if err != nil {
		return nil, fmt.Errorf("build associated PixelData value: %w", err)
	}
	return &dicom.Element{
		Tag:                    tag.PixelData,
		ValueRepresentation:    tag.VRPixelData,
		RawValueRepresentation: "OB",
		ValueLength:            tag.VLUndefinedLength,
		Value:                  pdValue,
	}, nil
}
```
(Confirm the import block now has `errors`, `fmt`, `io`, `log/slog`, `github.com/suyashkumar/dicom`, `github.com/suyashkumar/dicom/pkg/frame`, `github.com/suyashkumar/dicom/pkg/tag`, `github.com/wsilabs/wsitools/internal/source`. `image` is NOT needed in dicomwriter.go — `a.Size()` returns `image.Point` but is only passed through; if the compiler wants it, add it.)

- [ ] **Step 3: Update `cmd/wsitools/convert_dicom.go`** — the factory key is now a name, and pass `Associated`. In `writeDICOMPyramid`, change the factory + the `WritePyramid` call:
```go
	factory := func(name string) (io.WriteCloser, error) {
		return os.Create(filepath.Join(tmp, name+".dcm"))
	}
	if err := dicomwriter.WritePyramid(src, dicomwriter.Options{Associated: !cvNoAssociated}, factory); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("write DICOM pyramid: %w", err)
	}
```
The instance count for the report is no longer just `len(src.Levels())`; count the actual `.dcm` files written:
```go
	entries, _ := os.ReadDir(cvOutput)
	n := 0
	var total int64
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".dcm" {
			n++
			if info, err := e.Info(); err == nil {
				total += info.Size()
			}
		}
	}
```
(Replace the existing `n := len(src.Levels())` + ReadDir loop with this; keep the slog/Printf reporting using `n` + `total`.)

- [ ] **Step 4: Update `dicomwriter_test.go` `TestWritePyramid`** — the factory signature changed from `func(level int)` to `func(name string)`. Update the test's factory + buffer keying to use the name:
```go
	bufs := map[string]*bytes.Buffer{}
	factory := func(name string) (io.WriteCloser, error) {
		b := &bytes.Buffer{}
		bufs[name] = b
		return nopWriteCloser{b}, nil
	}
	if err := WritePyramid(src, Options{}, factory); err != nil {
		t.Fatalf("WritePyramid: %v", err)
	}
```
Then iterate levels by name `fmt.Sprintf("level-%d", level)` to fetch each buffer (`bufs[fmt.Sprintf("level-%d", level)]`), keeping the existing shared-UID / distinct-SOP / InstanceNumber / TotalPixelMatrixColumns assertions. (Ensure `fmt` is imported in the test file.)

- [ ] **Step 5: Create `internal/dicomwriter/associated_test.go`** — unit tests for `writeAssociated` + pyramid-with-associated. Gated on the Grundium DICOM fixture (which has label/overview/thumbnail associated images).

```go
package dicomwriter

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/wsilabs/wsitools/internal/source"
)

func openGrundium(t *testing.T) source.Source {
	t.Helper()
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no dicom fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func TestWriteAssociated(t *testing.T) {
	src := openGrundium(t)
	defer src.Close()
	assoc := src.Associated()
	if len(assoc) == 0 {
		t.Skip("fixture has no associated images")
	}
	shared := newSharedUIDs()
	flavors := map[string]string{"label": "LABEL", "overview": "OVERVIEW", "macro": "OVERVIEW", "thumbnail": "THUMBNAIL"}
	for i, a := range assoc {
		var buf bytes.Buffer
		if err := writeAssociated(&buf, src, a, shared, 100+i); err != nil {
			t.Fatalf("writeAssociated(%s): %v", a.Type(), err)
		}
		ds, err := dicom.Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
		if err != nil {
			t.Fatalf("parse %s: %v", a.Type(), err)
		}
		it, err := ds.FindElementByTag(tag.ImageType)
		if err != nil {
			t.Fatalf("%s ImageType: %v", a.Type(), err)
		}
		got := it.Value.GetValue().([]string)
		if len(got) < 3 || got[2] != flavors[a.Type()] {
			t.Errorf("%s ImageType[2] = %v, want %s", a.Type(), got, flavors[a.Type()])
		}
		if nf := firstStrA(t, ds, tag.NumberOfFrames); nf != "1" {
			t.Errorf("%s NumberOfFrames = %q, want 1", a.Type(), nf)
		}
		if s := firstStrA(t, ds, tag.SeriesInstanceUID); s != shared.Series {
			t.Errorf("%s SeriesInstanceUID = %q, want shared %q", a.Type(), s, shared.Series)
		}
		if fr := firstStrA(t, ds, tag.FrameOfReferenceUID); fr != shared.FrameOfReference {
			t.Errorf("%s FrameOfReferenceUID not shared", a.Type())
		}
	}
}

func TestWritePyramidWithAssociated(t *testing.T) {
	src := openGrundium(t)
	defer src.Close()
	if len(src.Associated()) == 0 {
		t.Skip("fixture has no associated images")
	}
	bufs := map[string]*bytes.Buffer{}
	factory := func(name string) (io.WriteCloser, error) {
		b := &bytes.Buffer{}
		bufs[name] = b
		return nopWriteCloser{b}, nil
	}
	if err := WritePyramid(src, Options{Associated: true}, factory); err != nil {
		t.Fatalf("WritePyramid: %v", err)
	}
	// Levels present.
	for level := range src.Levels() {
		if bufs[fmt.Sprintf("level-%d", level)] == nil {
			t.Errorf("missing level-%d", level)
		}
	}
	// Associated present + shared Series + unique contiguous InstanceNumbers.
	var series string
	seen := map[string]bool{}
	insts := map[int]bool{}
	for name, b := range bufs {
		ds, err := dicom.Parse(bytes.NewReader(b.Bytes()), int64(b.Len()), nil)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		s := firstStrA(t, ds, tag.SeriesInstanceUID)
		if series == "" {
			series = s
		} else if s != series {
			t.Errorf("%s SeriesInstanceUID %q != %q", name, s, series)
		}
		sop := firstStrA(t, ds, tag.SOPInstanceUID)
		if seen[sop] {
			t.Errorf("duplicate SOPInstanceUID at %s", name)
		}
		seen[sop] = true
		inst, _ := strconv.Atoi(firstStrA(t, ds, tag.InstanceNumber))
		if insts[inst] {
			t.Errorf("duplicate InstanceNumber %d at %s", inst, name)
		}
		insts[inst] = true
	}
	for _, a := range src.Associated() {
		if bufs[a.Type()] == nil {
			t.Errorf("missing associated %s.dcm", a.Type())
		}
	}
}

func firstStrA(t *testing.T, ds dicom.Dataset, tg tag.Tag) string {
	t.Helper()
	e, err := ds.FindElementByTag(tg)
	if err != nil {
		t.Fatalf("missing %v: %v", tg, err)
	}
	return e.Value.GetValue().([]string)[0]
}
```
(If the Grundium associated images turn out to be uncompressed/non-JPEG and `writeAssociated` skips them, the test will see fewer instances — adjust the asserts to tolerate skips, OR rely on the SVS fixtures which carry JPEG associated images. During implementation, run and confirm which fixture's associated images are JPEG; the Grundium golden's were JPEGBaseline.)

- [ ] **Step 6: Build, test, commit**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ ./cmd/wsitools/ -run 'DICOM|Assemble|ConvertDICOM|WritePyramid|Associated|PerLevel|JP2K|Lossless|Mono|PixelRoundTrip|WriteVolumeInstance' -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l internal/dicomwriter/ cmd/wsitools/convert_dicom.go
go vet ./internal/dicomwriter/ ./cmd/wsitools/
```
Expected: PASS (new associated tests + the regression set; the CLI compiles with the name-keyed factory). gofmt empty; vet clean.
```bash
git add internal/dicomwriter/dicomwriter.go internal/dicomwriter/dicomwriter_test.go internal/dicomwriter/associated_test.go cmd/wsitools/convert_dicom.go
git commit -m "feat(dicomwriter): emit associated images as separate WSM instances

WritePyramid now also emits the slide's associated images (label/overview/
thumbnail/macro→overview) as single-frame instances sharing the Series, after
the levels; --no-associated skips them. Unsupported-codec associated images are
logged and skipped. Factory is keyed by instance name.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: CLI integration test

**Files:** modify `cmd/wsitools/convert_dicom_test.go`.

- [ ] **Step 1: Add the test** — append. It runs `convert --to dicom -o <dir>` on a fixture with associated images and asserts the `<type>.dcm` files appear; and `--no-associated` produces only levels. Use the Grundium DICOM dir (multi-level + associated) — but its full pyramid writes a 311MB L0; instead, prefer a smaller fixture. **CMU-1-Small-Region.svs** is single-level and carries a `thumbnail` associated image (JPEG) — small + fast. Confirm during impl with `./bin/wsitools info sample_files/svs/CMU-1-Small-Region.svs` (it lists associated images).

```go
func TestConvertDICOMPyramidAssociated(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	svs := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skip("no CMU SVS fixture")
	}
	src, err := source.Open(svs)
	if err != nil {
		t.Fatal(err)
	}
	assoc := src.Associated()
	src.Close()
	if len(assoc) == 0 {
		t.Skip("fixture has no associated images")
	}

	out := filepath.Join(t.TempDir(), "pyr")
	convertCmd.Flags().Lookup("level").Changed = false
	cvOutput, cvForce, cvNoAssociated = "", false, false
	rootCmd.SetArgs([]string{"convert", "--to", "dicom", "-o", out, svs})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		convertCmd.Flags().Lookup("level").Changed = false
		cvNoAssociated = false
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("convert --to dicom: %v", err)
	}
	for _, a := range assoc {
		p := filepath.Join(out, a.Type()+".dcm")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing associated %s.dcm: %v", a.Type(), err)
			continue
		}
		s, err := source.Open(p)
		if err != nil {
			t.Errorf("source.Open(%s): %v", p, err)
			continue
		}
		if s.Format() != "dicom" {
			t.Errorf("%s Format = %q, want dicom", p, s.Format())
		}
		s.Close()
	}

	// --no-associated: only level-<n>.dcm, no associated files.
	out2 := filepath.Join(t.TempDir(), "pyr2")
	convertCmd.Flags().Lookup("level").Changed = false
	cvOutput, cvForce, cvNoAssociated = "", false, true
	rootCmd.SetArgs([]string{"convert", "--to", "dicom", "--no-associated", "-o", out2, svs})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("convert --no-associated: %v", err)
	}
	for _, a := range assoc {
		if _, err := os.Stat(filepath.Join(out2, a.Type()+".dcm")); err == nil {
			t.Errorf("--no-associated still wrote %s.dcm", a.Type())
		}
	}
}
```
(Confirm `--no-associated` is registered on `convertCmd` and bound to `cvNoAssociated`. If the flag name differs, use the actual one.)

- [ ] **Step 2: Run + clean + commit**
```bash
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run 'TestConvertDICOM' -v -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l cmd/wsitools/convert_dicom_test.go
go vet ./cmd/wsitools/
git add cmd/wsitools/convert_dicom_test.go
git commit -m "test(convert): --to dicom emits associated instances; --no-associated skips

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: dciodvfy on the associated instances (the de-risk)

**Files:** modify `Makefile`. The controller runs `/tmp/dciodvfy`.

- [ ] **Step 1: Extend `make dicom-validate`** — the Grundium full-pyramid block already emits into `$$DIR/pyr/`; it now also writes `label.dcm`/`overview.dcm`/`thumbnail.dcm` there. Change its glob from `pyr/level-*.dcm` to `pyr/*.dcm` so every instance (levels + associated) is validated. Find the Grundium block's loop:
```make
		for L in "$$DIR"/pyr/level-*.dcm; do \
```
Replace with:
```make
		for L in "$$DIR"/pyr/*.dcm; do \
```
(Leave the SVS single-instance and JP2K-pyramid blocks as they are; the JP2K block's `level-*.dcm` glob can also become `*.dcm` to validate its associated images — make the same change there.)

- [ ] **Step 2: Run (CONTROLLER step — needs dciodvfy)**
```bash
go build -o bin/wsitools ./cmd/wsitools 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" make dicom-validate DCIODVFY=/tmp/dciodvfy 2>&1 | grep -vE "duplicate lib|level=INFO" | grep -E "=== dciodvfy|^Error"
```
Expected: **0 Errors** across the Grundium pyramid levels + its `label`/`overview`/`thumbnail` instances, the SVS instance, and the JP2K pyramid (+ its associated). The benign per-instance Study-ID DICOMDIR warning is acceptable. Count `^Error` → must be 0.

- [ ] **Step 3: If an associated instance errors, fix and re-run**

Associated instances flow through the same `assembleWSMDataset`, so a gap would likely be: a flavor's required attribute, or the single-frame geometry (Rows/Columns vs TotalPixelMatrix), or PixelSpacing for the associated. Read every dciodvfy Error, fix in `dataset.go`/`writeAssociated`, re-run to 0. **If an associated flavor proves structurally non-conformant in a way the slice can't resolve, STOP and report.**

- [ ] **Step 4: Commit**
```bash
git add Makefile
git commit -m "feat(dicomwriter): validate associated instances in make dicom-validate

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Docs

**Files:** `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md`.

- [ ] **Step 1: Update docs**
- **CHANGELOG.md** `## [Unreleased]`: augment the `convert --to dicom` entry — the full-pyramid output now also includes the slide's **associated images** (label/overview/thumbnail/macro→overview) as single-frame WSM instances in the same Series (`<dir>/<type>.dcm`), default-on, skipped by `--no-associated`; unsupported-codec associated images are skipped with a warning. dciodvfy 0 errors including the associated instances.
- **README.md**: update the `convert --to dicom` bullet + footnote ⁶ — full-pyramid mode also emits associated images; `--no-associated` to skip; `--level N` single-instance emits none.
- **docs/roadmap.md**: add a `✅ DONE (2026-06-12): Phase 2 — associated images` sub-bullet; the DICOM writer's remaining items are now HTJ2K / 16-bit / the DICOM-source codec-awareness bug.
- **docs/notes/2026-06-03-dicom-writer-scoping.md**: add a `## Phase 2 outcome (2026-06-12)` section: associated images as same-Series single-frame instances (golden-confirmed model); the `instanceSpec` assembler generalization; `associatedFlavor` mapping (macro→OVERVIEW); skip-with-warning for unsupported codecs; dciodvfy 0 errors incl. associated. Remaining: HTJ2K, 16-bit, the P0 DICOM-source codec-mislabel bug.

- [ ] **Step 2: Verify + commit**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
git add README.md CHANGELOG.md docs/roadmap.md docs/notes/2026-06-03-dicom-writer-scoping.md
git commit -m "docs: DICOM-WSI writer Phase 2 (associated images as separate instances)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final review

Dispatch a final reviewer (focus: the `instanceSpec` refactor preserves level/pyramid output; associated instances share the Series with correct ImageType flavors + SpecimenLabelInImage + single-frame geometry; skip-with-warning for unsupported codecs; dciodvfy 0 errors incl. associated; `--no-associated` honored; no scope creep). Then use `superpowers:finishing-a-development-branch`.

## Self-review notes (author)

- **Spec coverage:** instanceSpec refactor + levelSpatial + codecColor (T1); writeAssociated + WritePyramid associated emission + factory-by-name + Options.Associated + CLI (T2); CLI integration + --no-associated (T3); dciodvfy de-risk incl. associated (T4); docs (T5). Flavor mapping (associatedFlavor: label→LABEL, overview/macro→OVERVIEW, thumbnail→THUMBNAIL) = T2. Same-Series shared UIDs + continuing InstanceNumber = T2. Skip-with-warning = T2.
- **Type consistency:** `instanceSpec` (embeds `ImageDescriptor`), `assembleWSMDataset(src,uids,spec)`, `levelSpatial`, `codecColor(tile,comp,icc,ratio)`, `buildDescriptor(src,level,ratio)`, `writeAssociated(w,src,a,shared,instNum)`, `encapsulateOneFrame(body)`, `WritePyramid(src,opts,newWriter func(name string)…)`, `Options{Associated bool}`, `associatedFlavor(t)` consistent across tasks.
- **Regression safety:** T1 changes zero emitted level bytes (spec reproduces the prior values); verified by the existing dciodvfy/round-trip/PerLevel/Lossless/Mono tests. ImageType logic for DICOM sources (DERIVED at every level) preserved in writeInstance.
- **Scope:** associated images as same-Series single-frame instances + the enabling refactor; HTJ2K/16-bit/.jp2-box/DICOM-source-codec-bug explicitly deferred. T4 carries the STOP-and-report signal.
