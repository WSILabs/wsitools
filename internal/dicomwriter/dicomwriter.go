package dicomwriter

import (
	"fmt"
	"io"

	"github.com/suyashkumar/dicom"

	"github.com/wsilabs/wsitools/internal/source"
)

// Options is reserved for future write-side knobs (P0/P1: empty).
type Options struct{}

// WriteVolumeInstance emits ONE conformant DICOM WSM VOLUME instance for src
// level `level` to w, copying the source's compressed JPEG tiles verbatim.
// The source's selected level must carry JPEG-baseline tiles (DICOM sources
// always do; non-DICOM sources are codec-gated in buildDescriptor).
func WriteVolumeInstance(w io.Writer, src source.Source, level int, _ Options) error {
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
		Study:            NewUID(),
		Series:           NewUID(),
		FrameOfReference: NewUID(),
		DimensionOrg:     NewUID(),
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
