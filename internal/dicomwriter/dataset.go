package dicomwriter

import (
	"fmt"
	"image"
	"strconv"
	"time"

	"github.com/WSILabs/dicom"
	"github.com/WSILabs/dicom/pkg/tag"

	"github.com/wsilabs/wsitools/internal/source"
)

// formatDS formats a float64 as a DICOM DS (Decimal String) value guaranteed to
// fit the VR's 16-character limit. It uses the shortest round-tripping form, then
// reduces significant digits until it fits — non-integer per-level downsample
// ratios (e.g. an SVS whose level sizes aren't exact powers of two) can otherwise
// yield 20+ char PixelSpacing values that dciodvfy rejects.
func formatDS(v float64) string {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if len(s) <= 16 {
		return s
	}
	for prec := 15; prec >= 1; prec-- {
		if s = strconv.FormatFloat(v, 'g', prec, 64); len(s) <= 16 {
			return s
		}
	}
	return s[:16]
}

// writerSoftware is the value emitted for Manufacturer / SoftwareVersions.
// dicomwriter is an internal/ package and cannot import package main (the
// CLI's version.go), so it carries its own identity constant.
const writerSoftware = "wsitools"

// Transfer syntax + SOP class for the WSM VOLUME instance we emit.
const (
	wsmSOPClassUID  = "1.2.840.10008.5.1.4.1.1.77.1.6" // VLWholeSlideMicroscopyImageStorage
	jpegBaselineTS  = "1.2.840.10008.1.2.4.50"         // JPEG Baseline (Process 1)
	jp2kLosslessTS  = "1.2.840.10008.1.2.4.90"         // JPEG 2000 (Lossless Only)
	jp2kTS          = "1.2.840.10008.1.2.4.91"         // JPEG 2000 Image Compression
	htj2kLosslessTS = "1.2.840.10008.1.2.4.201"        // High-Throughput JPEG 2000 (Lossless Only)
	htj2kTS         = "1.2.840.10008.1.2.4.203"        // High-Throughput JPEG 2000 Image Compression
)

// UIDSet holds the generated UIDs for one instance. The caller populates these
// via NewUID() so the writer stays deterministic-input / no-hidden-state.
type UIDSet struct {
	SOP, Study, Series, FrameOfReference, DimensionOrg string
}

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

	// mk wraps NewElement, accumulating the first error.
	var firstErr error
	mk := func(t tag.Tag, v any) *dicom.Element {
		e, err := dicom.NewElement(t, v)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("NewElement(%v): %w", t, err)
		}
		return e
	}
	// codeItem builds a coded-concept item (CodeValue/Designator/Meaning).
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
		// Dates/times (tag-ordered: StudyDate 0020, ContentDate 0023,
		// AcquisitionDateTime 002A, StudyTime 0030, ContentTime 0033).
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

		// DimensionOrganization (TILED_FULL).
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

		// ---- Slide Label (group 2200) — required when the image contains the
		// label (SpecimenLabelInImage == "YES", i.e. LABEL/OVERVIEW flavors). Both
		// Type 2: present-but-empty (anonymous re-emission). Filtered out below for
		// instances that do not contain the label.
		mk(tag.LabelText, []string{}),
		mk(tag.BarcodeValue, []string{}),

		// ---- Shared Functional Groups (group 5200) ----
		mk(tag.SharedFunctionalGroupsSequence, [][]*dicom.Element{
			{
				mk(tag.PixelMeasuresSequence, [][]*dicom.Element{
					{
						mk(tag.SliceThickness, []string{"0.001"}),
						// PixelSpacing is row\col == Y\X (DS, ≤16 chars).
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
	// Conditional omissions: LossyImageCompressionRatio + Method are Type 1C (only
	// when "01"); PlanarConfiguration is Type 1C (only when SamplesPerPixel > 1);
	// the SlideLabel module (LabelText + BarcodeValue) is required only when the
	// image contains the label (SpecimenLabelInImage == "YES").
	mono := spec.SamplesPerPixel == 1
	hasLabel := spec.SpecimenLabelInImage == "YES"
	if !spec.Lossy || mono || !hasLabel {
		kept := elems[:0]
		for _, e := range elems {
			if !spec.Lossy && (e.Tag == tag.LossyImageCompressionRatio || e.Tag == tag.LossyImageCompressionMethod) {
				continue
			}
			if mono && e.Tag == tag.PlanarConfiguration {
				continue
			}
			if !hasLabel && (e.Tag == tag.LabelText || e.Tag == tag.BarcodeValue) {
				continue
			}
			kept = append(kept, e)
		}
		elems = kept
	}
	return dicom.Dataset{Elements: elems}, nil
}
