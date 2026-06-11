package dicomwriter

import (
	"fmt"

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
func assembleWSMDataset(src source.Source, level int, uids UIDSet) (dicom.Dataset, error) {
	if level < 0 || level >= len(src.Levels()) {
		return dicom.Dataset{}, fmt.Errorf("level %d out of range (0..%d)", level, len(src.Levels())-1)
	}
	lvl := src.Levels()[level]
	md := src.Metadata()

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

	elems := []*dicom.Element{
		// ---- File-meta (group 0002) ----
		mk(tag.FileMetaInformationVersion, []byte{0x00, 0x01}),
		mk(tag.MediaStorageSOPClassUID, []string{wsmSOPClassUID}),
		mk(tag.MediaStorageSOPInstanceUID, []string{uids.SOP}),
		mk(tag.TransferSyntaxUID, []string{jpegBaselineTS}),
		mk(tag.ImplementationClassUID, []string{NewUID()}),

		// ---- General / SOP Common (group 0008) ----
		// ImageType: VOLUME image; RESAMPLED for derived pyramid levels.
		mk(tag.ImageType, []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"}),
		mk(tag.SOPClassUID, []string{wsmSOPClassUID}),
		mk(tag.SOPInstanceUID, []string{uids.SOP}),
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
		mk(tag.PhotometricInterpretation, []string{"YBR_FULL_422"}),
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
		mk(tag.OpticalPathSequence, [][]*dicom.Element{
			{
				mk(tag.IlluminationTypeCodeSequence, [][]*dicom.Element{
					codeItem("111744", "DCM", "Brightfield illumination"),
				}),
				mk(tag.OpticalPathIdentifier, []string{"0"}),
				mk(tag.IlluminationColorCodeSequence, [][]*dicom.Element{
					codeItem("414298005", "SCT", "Full Spectrum"),
				}),
			},
		}),
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
					{mk(tag.FrameType, []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"})},
				}),
			},
		}),
	}

	if firstErr != nil {
		return dicom.Dataset{}, firstErr
	}
	return dicom.Dataset{Elements: elems}, nil
}
