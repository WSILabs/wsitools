package dicomwriter

import (
	"fmt"
	"time"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/wsilabs/wsitools/internal/source"
)

// writerSoftware is the value emitted for Manufacturer / SoftwareVersions.
// dicomwriter is an internal/ package and cannot import package main (the
// CLI's version.go), so it carries its own identity constant.
const writerSoftware = "wsitools"

// Transfer syntax + SOP class for the WSM VOLUME instance we emit.
const (
	wsmSOPClassUID = "1.2.840.10008.5.1.4.1.1.77.1.6" // VLWholeSlideMicroscopyImageStorage
	jpegBaselineTS = "1.2.840.10008.1.2.4.50"         // JPEG Baseline (Process 1)
)

// UIDSet holds the generated UIDs for one instance. The caller populates these
// via NewUID() so the writer stays deterministic-input / no-hidden-state.
type UIDSet struct {
	SOP, Study, Series, FrameOfReference, DimensionOrg string
}

// assembleWSMDataset builds the WSM IOD element list (everything except
// PixelData) for src level `level`, mirroring the Grundium golden template
// (sample_files/dicom/scan_621_grundium_dicom/scan_621__pyr04.dcm).
//
// SEQUENCE (SQ) CONSTRUCTION — verified against suyashkumar/dicom v1.1.0:
//
//	dicom.NewElement(seqTag, [][]*dicom.Element{ {item0elems...}, {item1elems...} })
//
// NewValue's [][]*Element branch wraps each inner slice as a SequenceItemValue.
// Nesting works the same: an item element can itself be a NewElement(SQ, [][]...).
// An EMPTY sequence is dicom.NewElement(seqTag, [][]*dicom.Element{}). The
// library resolves VR from its tag dictionary, so coded-sequence items just
// carry CodeValue/CodingSchemeDesignator/CodeMeaning string elements.
//
// Most geometry is DERIVED from the source level/metadata; structural constants
// (ImageType, Modality, photometric/bit-depth fields, orientation, TILED_FULL,
// anonymous identity, coded-sequence codes) are MIRRORED from the golden.
// ImageDescriptor carries the codec/colorspace-dependent attributes that vary by
// source. The caller (WriteVolumeInstance) derives these — probing a non-DICOM
// source's JPEG, or using P0's fixed values for a DICOM source — so the assembler
// stays a pure dataset builder.
type ImageDescriptor struct {
	Photometric string   // PhotometricInterpretation: RGB | YBR_FULL_422 | YBR_FULL | MONOCHROME2
	ImageType   []string // ImageType + FrameType value (4 elements)
	ICCProfile  []byte   // carried or synthesized; non-empty for color
	LossyRatio  float64  // LossyImageCompressionRatio
}

func assembleWSMDataset(src source.Source, level int, uids UIDSet, desc ImageDescriptor) (dicom.Dataset, error) {
	if level < 0 || level >= len(src.Levels()) {
		return dicom.Dataset{}, fmt.Errorf("level %d out of range (0..%d)", level, len(src.Levels())-1)
	}
	lvl := src.Levels()[level]
	md := src.Metadata()

	// Date/time attributes. The opentile DICOM reader does not currently surface
	// the source's acquisition timestamp, so ContentDate/Time (Type 1, "when this
	// instance was created") use now; AcquisitionDateTime (Type 1) uses the source
	// value when present, else now. StudyDate/Time (Type 2) mirror the content
	// date/time so a DICOMDIR can index the instance (StudyID stays empty — we
	// have no real study identifier for an anonymous re-emission).
	now := time.Now().UTC()
	acq := md.AcquisitionDateTime
	if acq.IsZero() {
		acq = now
	}
	contentDA := now.Format("20060102")
	contentTM := now.Format("150405")
	acqDT := acq.Format("20060102150405")

	// DeviceSerialNumber is Type 1 (must be non-empty). Carry the source serial
	// when available, else identify the synthesizing writer.
	serial := md.SerialNumber
	if serial == "" {
		serial = writerSoftware
	}

	// LossyImageCompressionRatio (Type 1C, DS, ≤16 chars) — %.4g keeps it compact.
	ratioStr := fmt.Sprintf("%.4g", desc.LossyRatio)

	grid := lvl.Grid()
	size := lvl.Size()
	tileSize := lvl.TileSize()
	numFrames := grid.X * grid.Y

	// PixelSpacing in mm = MPP(µm) / 1000. Fall back to 0 when unknown; the
	// golden carries a real value, so prefer per-axis when available.
	mppX, mppY := md.MPPX, md.MPPY
	if mppX == 0 {
		mppX = md.MPP
	}
	if mppY == 0 {
		mppY = md.MPP
	}
	psX := mppX / 1000.0 // mm/px, column spacing
	psY := mppY / 1000.0 // mm/px, row spacing

	// ImagedVolume dimensions (mm) = matrix pixels × pixel spacing.
	imagedW := float64(size.X) * psX
	imagedH := float64(size.Y) * psY

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

	// OpticalPath item (tag-ordered within the item). ICCProfile (0028,2000) is
	// Type 1C — required for color images; placed after IlluminationTypeCodeSequence
	// 0022,0016 and before OpticalPathIdentifier 0048,0106. desc.ICCProfile is the
	// source's embedded profile, or a synthesized sRGB profile when the source has
	// none (buildDescriptor), so it is non-empty for color and the Type 1C
	// requirement is always satisfied; the guard below tolerates an empty profile
	// defensively.
	opticalPathItem := []*dicom.Element{
		mk(tag.IlluminationTypeCodeSequence, [][]*dicom.Element{
			codeItem("111744", "DCM", "Brightfield illumination"),
		}),
	}
	if len(desc.ICCProfile) > 0 {
		opticalPathItem = append(opticalPathItem, mk(tag.ICCProfile, desc.ICCProfile))
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
		mk(tag.TransferSyntaxUID, []string{jpegBaselineTS}),
		mk(tag.ImplementationClassUID, []string{NewUID()}),

		// ---- General / SOP Common (group 0008) ----
		mk(tag.ImageType, desc.ImageType),
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
		mk(tag.InstanceNumber, []string{fmt.Sprintf("%d", level+1)}),
		mk(tag.FrameOfReferenceUID, []string{uids.FrameOfReference}),
		mk(tag.PositionReferenceIndicator, []string{"SLIDE_CORNER"}),

		// DimensionOrganization (TILED_FULL).
		mk(tag.DimensionOrganizationSequence, [][]*dicom.Element{
			{mk(tag.DimensionOrganizationUID, []string{uids.DimensionOrg})},
		}),
		mk(tag.DimensionOrganizationType, []string{"TILED_FULL"}),

		// ---- Image Pixel / WSM frame descriptors (group 0028) ----
		mk(tag.SamplesPerPixel, []int{3}),
		mk(tag.PhotometricInterpretation, []string{desc.Photometric}),
		mk(tag.PlanarConfiguration, []int{0}),
		mk(tag.NumberOfFrames, []string{fmt.Sprintf("%d", numFrames)}),
		mk(tag.Rows, []int{tileSize.Y}),
		mk(tag.Columns, []int{tileSize.X}),
		mk(tag.BitsAllocated, []int{8}),
		mk(tag.BitsStored, []int{8}),
		mk(tag.HighBit, []int{7}),
		mk(tag.PixelRepresentation, []int{0}),
		mk(tag.BurnedInAnnotation, []string{"NO"}),
		mk(tag.LossyImageCompression, []string{"01"}),
		mk(tag.LossyImageCompressionRatio, []string{ratioStr}),
		mk(tag.LossyImageCompressionMethod, []string{"ISO_10918_1"}),

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
		mk(tag.ImagedVolumeWidth, []float64{imagedW}),
		mk(tag.ImagedVolumeHeight, []float64{imagedH}),
		mk(tag.ImagedVolumeDepth, []float64{1}),
		mk(tag.TotalPixelMatrixColumns, []int{size.X}),
		mk(tag.TotalPixelMatrixRows, []int{size.Y}),
		mk(tag.TotalPixelMatrixOriginSequence, [][]*dicom.Element{
			{
				mk(tag.XOffsetInSlideCoordinateSystem, []string{"0.0"}),
				mk(tag.YOffsetInSlideCoordinateSystem, []string{"0.0"}),
				mk(tag.ZOffsetInSlideCoordinateSystem, []string{"0.0"}),
			},
		}),
		mk(tag.SpecimenLabelInImage, []string{"NO"}),
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
						// PixelSpacing is row\col == Y\X (DS).
						mk(tag.PixelSpacing, []string{
							fmt.Sprintf("%g", psY),
							fmt.Sprintf("%g", psX),
						}),
					},
				}),
				mk(tag.WholeSlideMicroscopyImageFrameTypeSequence, [][]*dicom.Element{
					{mk(tag.FrameType, desc.ImageType)},
				}),
			},
		}),
	}

	if firstErr != nil {
		return dicom.Dataset{}, firstErr
	}
	return dicom.Dataset{Elements: elems}, nil
}
