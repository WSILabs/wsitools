package dicomwriter

import (
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/frame"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

// Options controls the DICOM write. Associated enables emitting the slide's
// associated images (label/overview/thumbnail/…) as separate instances.
type Options struct {
	Associated bool
}

// errSkipAssociated marks an associated image that can't be tile-copied (e.g. an
// unsupported codec); WritePyramid logs and skips it rather than failing.
var errSkipAssociated = errors.New("associated image skipped")

// associatedSupported reports whether an associated image's codec can be
// tile-copied into DICOM (the single source of truth for the codec gate).
func associatedSupported(c source.Compression) bool {
	return c == source.CompressionJPEG || c == source.CompressionJPEG2000
}

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
// A tile-copyable codec (JPEG / JPEG 2000) is stored verbatim-encapsulated; any
// other codec (e.g. an LZW label — not a DICOM transfer syntax) is decoded via
// opentile and stored as uncompressed native RGB (lossless).
func writeAssociated(w io.Writer, src source.Source, a source.AssociatedImage, shared sharedUIDs, instanceNumber int) error {
	md := src.Metadata()
	icc := md.ICCProfile
	if len(icc) == 0 {
		icc = srgbICCProfile
	}
	imageType, specimenLabel := associatedFlavor(a.Type())
	mppX, mppY := baseMPP(md)

	spec := instanceSpec{
		Size:                 a.Size(),
		TileSize:             a.Size(), // single frame = whole image
		NumFrames:            1,
		ImageType:            imageType,
		SpecimenLabelInImage: specimenLabel,
		InstanceNumber:       instanceNumber,
	}

	var pd *dicom.Element
	if associatedSupported(a.Compression()) {
		// Verbatim encapsulated tile-copy (JPEG / JPEG 2000).
		body, err := a.Bytes()
		if err != nil {
			return fmt.Errorf("%w: %s bytes: %v", errSkipAssociated, a.Type(), err)
		}
		uncompressed := int64(a.Size().X) * int64(a.Size().Y) * 3
		lossyRatio := 1.0
		if len(body) > 0 {
			lossyRatio = float64(uncompressed) / float64(len(body))
		}
		// codecColor probes the image bytes directly (codec-driven) — associated
		// images always derive their photometric from the codestream, never the
		// DICOM-source hardcode that buildDescriptor applies to pyramid levels.
		desc, err := codecColor(body, a.Compression(), icc, lossyRatio)
		if err != nil {
			return fmt.Errorf("%w: %s codec probe: %v", errSkipAssociated, a.Type(), err)
		}
		spec.ImageDescriptor = desc
		if pd, err = encapsulateOneFrame(body); err != nil {
			return err
		}
	} else {
		// Decode (opentile owns codec/predictor) → store uncompressed native RGB.
		di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
		if err != nil {
			return fmt.Errorf("%w: %s decode: %v", errSkipAssociated, a.Type(), err)
		}
		spec.ImageDescriptor = ImageDescriptor{
			TransferSyntax:  explicitVRLE,
			Photometric:     "RGB",
			SamplesPerPixel: 3,
			ICCProfile:      icc,
			Lossy:           false,
			LossyMethod:     "",
			LossyRatio:      1.0,
		}
		// Decoded image dimensions are authoritative for geometry.
		spec.Size = image.Point{X: di.Width, Y: di.Height}
		spec.TileSize = spec.Size
		if pd, err = nativePixelData(tightRGB(di), di.Height, di.Width, 3); err != nil {
			return err
		}
	}

	// Spatial attributes derive from the final spec.Size (authoritative for the
	// decoded branch; identical to a.Size() for the encapsulated branch).
	spec.PixelSpacingX, spec.PixelSpacingY, spec.ImagedVolumeW, spec.ImagedVolumeH =
		levelSpatial(src.Levels()[0].Size(), spec.Size, mppX, mppY)

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

// tightRGB returns a tightly-packed Height*Width*3 RGB buffer from a decoder.Image,
// stripping any row stride padding (decoder may over-allocate Stride for SIMD).
func tightRGB(di *decoder.Image) []byte {
	rowBytes := di.Width * 3
	if di.Stride == rowBytes {
		return di.Pix[:di.Height*rowBytes]
	}
	out := make([]byte, di.Height*rowBytes)
	for y := 0; y < di.Height; y++ {
		copy(out[y*rowBytes:(y+1)*rowBytes], di.Pix[y*di.Stride:y*di.Stride+rowBytes])
	}
	return out
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
	mppX, mppY := baseMPP(md)
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

// baseMPP returns the source's base (level-0) microns-per-pixel with the per-axis
// → symmetric fallback.
func baseMPP(md source.Metadata) (mppX, mppY float64) {
	mppX, mppY = md.MPPX, md.MPPY
	if mppX == 0 {
		mppX = md.MPP
	}
	if mppY == 0 {
		mppY = md.MPP
	}
	return mppX, mppY
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
