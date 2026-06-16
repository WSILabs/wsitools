package dicomwriter

import (
	"fmt"

	otdecoder "github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

// codecinspect.go maps a source codestream's codec-domain metadata — obtained
// from opentile-go's header-only decoder.CodestreamInspector (GH opentile-go#41)
// — onto the DICOM codec/colour attributes for a verbatim frame-copy. It
// replaces wsitools' former hand-rolled jpegmeta/jp2kmeta marker parsers: the
// upstream inspector already parses the JPEG SOF / J2K SIZ+COD / HTJ2K / JXL
// headers (and exposes ColorEncoding + ChromaSubsampling + Lossless), so the
// only wsitools-specific logic left is the codec→DICOM vocabulary mapping.

// inspectorFor returns the registered decoder that can inspect a codestream of
// the given source compression's header, or ok=false for codecs we don't
// frame-copy into DICOM (or a nocgo build whose stub factory can't inspect).
func inspectorFor(comp source.Compression) (otdecoder.CodestreamInspector, bool) {
	var name string
	switch comp {
	case source.CompressionJPEG:
		name = "jpeg"
	case source.CompressionJPEG2000:
		name = "jpeg2000"
	case source.CompressionHTJ2K:
		name = "htj2k"
	case source.CompressionJPEGXL:
		name = "jpegxl"
	default:
		return nil, false
	}
	fac, ok := otdecoder.Get(name)
	if !ok {
		return nil, false
	}
	insp, ok := fac.(otdecoder.CodestreamInspector)
	return insp, ok
}

// photometricForInspect maps the inspector's codec-domain ColorEncoding (and, for
// JPEG luma/chroma, its ChromaSubsampling) onto the DICOM PhotometricInterpretation.
func photometricForInspect(info otdecoder.CodestreamInfo) (string, error) {
	switch info.ColorEncoding {
	case otdecoder.ColorGrayscale:
		return "MONOCHROME2", nil
	case otdecoder.ColorRGB:
		return "RGB", nil
	case otdecoder.ColorYCbCr:
		// JPEG luma/chroma: DICOM uses YBR_FULL_422 when the chroma is
		// subsampled (the usual WSI case — e.g. Aperio 4:2:2) and YBR_FULL when
		// it is not. SubsamplingNone is grayscale (handled above), so anything
		// other than 4:4:4 is treated as subsampled.
		if info.ChromaSubsampling == otdecoder.Subsampling444 {
			return "YBR_FULL", nil
		}
		return "YBR_FULL_422", nil
	case otdecoder.ColorYBRICT:
		return "YBR_ICT", nil
	case otdecoder.ColorYBRRCT:
		return "YBR_RCT", nil
	default:
		return "", fmt.Errorf("dicomwriter: unsupported color encoding %q (components=%d)",
			info.ColorEncoding, info.Components)
	}
}

// descriptorFromInspect builds the DICOM ImageDescriptor for a verbatim
// frame-copy of a codestream with the given inspector metadata and source
// compression. The transfer syntax + lossy attributes are codec-specific; the
// photometric / samples-per-pixel come from the inspector.
func descriptorFromInspect(info otdecoder.CodestreamInfo, comp source.Compression, icc []byte, lossyRatio float64) (ImageDescriptor, error) {
	if info.BitDepth != 8 {
		return ImageDescriptor{}, fmt.Errorf("dicomwriter: unsupported bit depth %d for %s frame-copy (want 8)", info.BitDepth, comp)
	}
	photo, err := photometricForInspect(info)
	if err != nil {
		return ImageDescriptor{}, err
	}
	desc := ImageDescriptor{
		Photometric:     photo,
		SamplesPerPixel: info.Components,
		ICCProfile:      icc,
		LossyRatio:      lossyRatio,
	}
	switch comp {
	case source.CompressionJPEG:
		desc.TransferSyntax = jpegBaselineTS
		desc.Lossy = true
		desc.LossyMethod = "ISO_10918_1"
	case source.CompressionJPEG2000:
		if info.Lossless == otdecoder.LosslessYes {
			desc.TransferSyntax = jp2kLosslessTS
		} else {
			desc.TransferSyntax = jp2kTS
			desc.Lossy = true
			desc.LossyMethod = "ISO_15444_1"
		}
	case source.CompressionHTJ2K:
		if info.Lossless == otdecoder.LosslessYes {
			desc.TransferSyntax = htj2kLosslessTS
		} else {
			desc.TransferSyntax = htj2kTS
			desc.Lossy = true
			desc.LossyMethod = "ISO_15444_15"
		}
	case source.CompressionJPEGXL:
		// JPEG XL exposes no header-only reversibility flag (Lossless ==
		// LosslessUnknown), so use the general JPEG XL transfer syntax (…4.112),
		// which is valid for both lossy and lossless, and mark lossy
		// conservatively (over-claiming lossy is safer than mislabeling a lossy
		// frame as lossless). UNTESTED: no JPEG XL source fixture exists yet.
		desc.TransferSyntax = jpegxlTS
		desc.Lossy = true
		desc.LossyMethod = "ISO_18181_1"
	default:
		return ImageDescriptor{}, fmt.Errorf("unsupported codec %s (JPEG-baseline, JPEG 2000, HTJ2K, or JPEG XL only)", comp)
	}
	return desc, nil
}
