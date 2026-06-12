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
		// Reachable once writeAssociated calls codecColor directly on associated
		// frame bytes (buildDescriptor pre-checks the level codec before calling).
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
