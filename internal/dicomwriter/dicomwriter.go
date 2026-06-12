package dicomwriter

import (
	"fmt"
	"io"

	"github.com/suyashkumar/dicom"

	"github.com/wsilabs/wsitools/internal/source"
)

// Options is reserved for future write-side knobs (P0/P1: empty).
type Options struct{}

// sharedUIDs are the UIDs shared by every instance in a pyramid Series: the
// Study, Series, FrameOfReference, and DimensionOrganization. Each instance still
// gets its own SOPInstanceUID.
type sharedUIDs struct {
	Study, Series, FrameOfReference, DimensionOrg string
}

// newSharedUIDs generates a fresh set of series-level UIDs (one per Study /
// Series / FrameOfReference / DimensionOrganization).
func newSharedUIDs() sharedUIDs {
	return sharedUIDs{
		Study:            NewUID(),
		Series:           NewUID(),
		FrameOfReference: NewUID(),
		DimensionOrg:     NewUID(),
	}
}

// WriteVolumeInstance emits ONE conformant DICOM WSM VOLUME instance for src
// level `level` to w, copying the source's compressed JPEG tiles verbatim. The
// source's selected level must carry JPEG-baseline tiles (DICOM sources always
// do; non-DICOM sources are codec-gated in buildDescriptor).
func WriteVolumeInstance(w io.Writer, src source.Source, level int, _ Options) error {
	return writeInstance(w, src, level, newSharedUIDs())
}

// WritePyramid emits the full resolution pyramid as a multi-instance Series: one
// WSM VOLUME instance per source level, all sharing the Study/Series/
// FrameOfReference/DimensionOrganization UIDs. newWriter supplies the destination
// writer for each level (0-based); WritePyramid closes each writer after writing.
func WritePyramid(src source.Source, _ Options, newWriter func(level int) (io.WriteCloser, error)) error {
	shared := newSharedUIDs()
	for level := range src.Levels() {
		w, err := newWriter(level)
		if err != nil {
			return fmt.Errorf("open writer for level %d: %w", level, err)
		}
		werr := writeInstance(w, src, level, shared)
		cerr := w.Close()
		if werr != nil {
			return fmt.Errorf("write level %d: %w", level, werr)
		}
		if cerr != nil {
			return fmt.Errorf("close level %d: %w", level, cerr)
		}
	}
	return nil
}

// writeInstance assembles + writes one WSM VOLUME instance for src level `level`
// to w, using the supplied shared UIDs and a fresh SOPInstanceUID. The level's
// InstanceNumber (level+1) is emitted by assembleWSMDataset.
func writeInstance(w io.Writer, src source.Source, level int, shared sharedUIDs) error {
	if level < 0 || level >= len(src.Levels()) {
		return fmt.Errorf("level %d out of range (0..%d)", level, len(src.Levels())-1)
	}
	// Encapsulate first: the compressed byte total feeds the lossy compression
	// ratio (LossyImageCompressionRatio is Type 1C, required when
	// LossyImageCompression is "01").
	pd, compressedBytes, err := encapsulatePixelData(src, level)
	if err != nil {
		return err
	}
	lvl := src.Levels()[level]
	tileSize := lvl.TileSize()
	grid := lvl.Grid()
	uncompressed := int64(grid.X) * int64(grid.Y) * int64(tileSize.X) * int64(tileSize.Y) * 3
	lossyRatio := 1.0
	if compressedBytes > 0 {
		lossyRatio = float64(uncompressed) / float64(compressedBytes)
	}

	desc, err := buildDescriptor(src, level, lossyRatio)
	if err != nil {
		return err
	}

	uids := UIDSet{
		SOP:              NewUID(),
		Study:            shared.Study,
		Series:           shared.Series,
		FrameOfReference: shared.FrameOfReference,
		DimensionOrg:     shared.DimensionOrg,
	}
	ds, err := assembleWSMDataset(src, level, uids, desc)
	if err != nil {
		return err
	}
	ds.Elements = append(ds.Elements, pd) // PixelData last
	return dicom.Write(w, ds)
}

// buildDescriptor derives the codec/colorspace-dependent attributes for src
// level `level`. DICOM sources reuse P0's fixed values (preserving byte-identical
// output); non-DICOM sources are gated to JPEG-baseline tiles and their
// PhotometricInterpretation is derived by inspecting the first tile's markers.
// ICC is carried from the source, or synthesized as sRGB when absent.
func buildDescriptor(src source.Source, level int, lossyRatio float64) (ImageDescriptor, error) {
	md := src.Metadata()
	icc := md.ICCProfile
	if len(icc) == 0 {
		icc = srgbICCProfile
	}

	if src.Format() == "dicom" {
		// P0 path: Grundium-mirrored values, unchanged.
		return ImageDescriptor{
			Photometric: "YBR_FULL_422",
			ImageType:   []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
			ICCProfile:  icc,
			LossyRatio:  lossyRatio,
		}, nil
	}

	lvl := src.Levels()[level]
	if lvl.Compression() != source.CompressionJPEG {
		return ImageDescriptor{}, fmt.Errorf(
			"--to dicom: level %d is %s; Phase 1 supports JPEG-baseline tile-copy only",
			level, lvl.Compression())
	}
	buf := make([]byte, lvl.TileMaxSize())
	n, err := lvl.TileInto(0, 0, buf)
	if err != nil {
		return ImageDescriptor{}, fmt.Errorf("read tile (0,0) for colorspace probe: %w", err)
	}
	info, err := Inspect(buf[:n])
	if err != nil {
		return ImageDescriptor{}, fmt.Errorf("inspect source JPEG: %w", err)
	}
	photo, err := Photometric(info)
	if err != nil {
		return ImageDescriptor{}, fmt.Errorf("derive photometric from source JPEG: %w", err)
	}
	// Level 0 of a non-DICOM slide is the native acquisition (ORIGINAL); reduced
	// levels are downsampled (DERIVED / RESAMPLED).
	imageType := []string{"ORIGINAL", "PRIMARY", "VOLUME", "NONE"}
	if level > 0 {
		imageType = []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"}
	}
	return ImageDescriptor{
		Photometric: photo,
		ImageType:   imageType,
		ICCProfile:  icc,
		LossyRatio:  lossyRatio,
	}, nil
}
