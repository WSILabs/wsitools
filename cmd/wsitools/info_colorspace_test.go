package main

import (
	"testing"

	"github.com/wsilabs/wsitools/cmd/wsitools/quality"

	otdecoder "github.com/wsilabs/opentile-go/decoder"
)

// TestEffectiveColorspace pins the codec-domain ColorEncoding -> effective
// (decoded) colorspace mapping shared by validate's #44 check and info. The
// load-bearing case is that an MCT (ICT/RCT) JPEG 2000 codestream reports "RGB"
// — the transform is inverted on decode — not YCbCr.
func TestEffectiveColorspace(t *testing.T) {
	cases := []struct {
		in   otdecoder.ColorEncoding
		want string
	}{
		{otdecoder.ColorRGB, "RGB"},
		{otdecoder.ColorYBRICT, "RGB"}, // MCT inverted on decode
		{otdecoder.ColorYBRRCT, "RGB"}, // MCT inverted on decode
		{otdecoder.ColorYCbCr, "YCbCr"},
		{otdecoder.ColorGrayscale, "grayscale"},
		{otdecoder.ColorUnknown, ""}, // ambiguous — don't guess
	}
	for _, c := range cases {
		if got := effectiveColorspace(c.in); got != c.want {
			t.Errorf("effectiveColorspace(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatQualityColorspacePrefix checks that a known colorspace is prefixed
// to the quality summary, and that a blank colorspace leaves the summary
// untouched (no stray separator).
func TestFormatQualityColorspacePrefix(t *testing.T) {
	cases := []struct {
		name string
		q    quality.Info
		want string
	}{
		{
			name: "jpeg RGB 8-bit with Q + chroma",
			q:    quality.Info{Codec: "JPEG", QualityEstimate: 93, ChromaSubsampling: "4:2:0", Colorspace: "RGB", BitDepth: 8},
			want: "RGB 8-bit 4:2:0  Q≈93",
		},
		{
			name: "jp2k RGB 8-bit lossy with layers + chroma",
			q:    quality.Info{Codec: "JPEG 2000", LayerCount: 1, Colorspace: "RGB", BitDepth: 8, ChromaSubsampling: "4:4:4"},
			want: "RGB 8-bit 4:4:4  lossy, 1 layers",
		},
		{
			name: "16-bit jp2k grayscale reversible",
			q:    quality.Info{Codec: "JPEG 2000", Lossless: true, LayerCount: 1, Colorspace: "grayscale", BitDepth: 16},
			want: "grayscale 16-bit  lossless, 1 layers",
		},
		{
			name: "no facts leaves body unprefixed",
			q:    quality.Info{Codec: "JPEG", QualityEstimate: 77},
			want: "Q≈77",
		},
		{
			name: "chroma only (no colorspace/bitdepth)",
			q:    quality.Info{Codec: "JPEG", QualityEstimate: 77, ChromaSubsampling: "4:4:4"},
			want: "4:4:4  Q≈77",
		},
	}
	for _, c := range cases {
		if got := formatQuality(&c.q); got != c.want {
			t.Errorf("%s: formatQuality = %q, want %q", c.name, got, c.want)
		}
	}
}
