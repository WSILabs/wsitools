package dicomwriter

import (
	"testing"

	otdecoder "github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

// TestDescriptorFromInspect covers the codec-domain → DICOM mapping that replaced
// the hand-rolled jpegmeta/jp2kmeta parsers: ColorEncoding (+ JPEG subsampling)
// → PhotometricInterpretation, and source compression (+ Lossless) → transfer
// syntax / lossy attributes.
func TestDescriptorFromInspect(t *testing.T) {
	type want struct {
		ts    string
		photo string
		spp   int
		lossy bool
	}
	cases := []struct {
		name string
		info otdecoder.CodestreamInfo
		comp source.Compression
		want want
	}{
		{"jpeg ycbcr 4:2:2", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessNo, ColorEncoding: otdecoder.ColorYCbCr, ChromaSubsampling: otdecoder.Subsampling422}, source.CompressionJPEG, want{jpegBaselineTS, "YBR_FULL_422", 3, true}},
		{"jpeg ycbcr 4:2:0", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessNo, ColorEncoding: otdecoder.ColorYCbCr, ChromaSubsampling: otdecoder.Subsampling420}, source.CompressionJPEG, want{jpegBaselineTS, "YBR_FULL_422", 3, true}},
		{"jpeg ycbcr 4:4:4", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessNo, ColorEncoding: otdecoder.ColorYCbCr, ChromaSubsampling: otdecoder.Subsampling444}, source.CompressionJPEG, want{jpegBaselineTS, "YBR_FULL", 3, true}},
		{"jpeg rgb (aperio app14)", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessNo, ColorEncoding: otdecoder.ColorRGB}, source.CompressionJPEG, want{jpegBaselineTS, "RGB", 3, true}},
		{"jpeg gray", otdecoder.CodestreamInfo{Components: 1, BitDepth: 8, Lossless: otdecoder.LosslessNo, ColorEncoding: otdecoder.ColorGrayscale}, source.CompressionJPEG, want{jpegBaselineTS, "MONOCHROME2", 1, true}},

		{"jp2k rgb no-mct lossy", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessNo, ColorEncoding: otdecoder.ColorRGB}, source.CompressionJPEG2000, want{jp2kTS, "RGB", 3, true}},
		{"jp2k ybr_rct lossless", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessYes, ColorEncoding: otdecoder.ColorYBRRCT}, source.CompressionJPEG2000, want{jp2kLosslessTS, "YBR_RCT", 3, false}},
		{"jp2k ybr_ict lossy", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessNo, ColorEncoding: otdecoder.ColorYBRICT}, source.CompressionJPEG2000, want{jp2kTS, "YBR_ICT", 3, true}},
		{"jp2k gray lossless", otdecoder.CodestreamInfo{Components: 1, BitDepth: 8, Lossless: otdecoder.LosslessYes, ColorEncoding: otdecoder.ColorGrayscale}, source.CompressionJPEG2000, want{jp2kLosslessTS, "MONOCHROME2", 1, false}},

		{"htj2k ybr_rct lossless", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessYes, ColorEncoding: otdecoder.ColorYBRRCT}, source.CompressionHTJ2K, want{htj2kLosslessTS, "YBR_RCT", 3, false}},
		{"htj2k ybr_ict lossy", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessNo, ColorEncoding: otdecoder.ColorYBRICT}, source.CompressionHTJ2K, want{htj2kTS, "YBR_ICT", 3, true}},

		{"jxl rgb unknown-lossless", otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, Lossless: otdecoder.LosslessUnknown, ColorEncoding: otdecoder.ColorRGB}, source.CompressionJPEGXL, want{jpegxlTS, "RGB", 3, true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			desc, err := descriptorFromInspect(c.info, c.comp, nil, 1.0)
			if err != nil {
				t.Fatalf("descriptorFromInspect: %v", err)
			}
			if desc.TransferSyntax != c.want.ts {
				t.Errorf("TransferSyntax = %s, want %s", desc.TransferSyntax, c.want.ts)
			}
			if desc.Photometric != c.want.photo {
				t.Errorf("Photometric = %s, want %s", desc.Photometric, c.want.photo)
			}
			if desc.SamplesPerPixel != c.want.spp {
				t.Errorf("SamplesPerPixel = %d, want %d", desc.SamplesPerPixel, c.want.spp)
			}
			if desc.Lossy != c.want.lossy {
				t.Errorf("Lossy = %v, want %v", desc.Lossy, c.want.lossy)
			}
		})
	}
}

func TestDescriptorFromInspect_Errors(t *testing.T) {
	// Non-8-bit is unsupported (>8-bit is a later slice).
	if _, err := descriptorFromInspect(otdecoder.CodestreamInfo{Components: 3, BitDepth: 12, ColorEncoding: otdecoder.ColorRGB}, source.CompressionJPEG2000, nil, 1); err == nil {
		t.Error("expected error for 12-bit, got nil")
	}
	// Unknown color encoding (e.g. 4-component CMYK) has no DICOM photometric.
	if _, err := descriptorFromInspect(otdecoder.CodestreamInfo{Components: 4, BitDepth: 8, ColorEncoding: otdecoder.ColorUnknown}, source.CompressionJPEG, nil, 1); err == nil {
		t.Error("expected error for unknown color encoding, got nil")
	}
	// A codec we don't frame-copy.
	if _, err := descriptorFromInspect(otdecoder.CodestreamInfo{Components: 3, BitDepth: 8, ColorEncoding: otdecoder.ColorRGB}, source.CompressionLZW, nil, 1); err == nil {
		t.Error("expected error for unsupported codec, got nil")
	}
}
