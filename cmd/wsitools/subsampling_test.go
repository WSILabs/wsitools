package main

import (
	"testing"

	qualityjpeg "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"
	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
)

// TestJPEGEncoderSubsamplingKnob: the encoder honors the "subsampling" knob,
// producing JPEGs whose SOF sampling factors match (444→1,1; 422→2,1; 420→2,2),
// and defaults to 4:2:0 when the knob is absent.
func TestJPEGEncoderSubsamplingKnob(t *testing.T) {
	cases := []struct {
		knob  string
		wantH uint16
		wantV uint16
	}{
		{"444", 1, 1},
		{"422", 2, 1},
		{"420", 2, 2},
		{"", 2, 2}, // default
	}
	rgb := make([]byte, 64*64*3)
	for _, c := range cases {
		knobs := map[string]string{"q": "85"}
		if c.knob != "" {
			knobs["subsampling"] = c.knob
		}
		enc, err := jpegcodec.New(codec.LevelGeometry{TileWidth: 64, TileHeight: 64, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: knobs})
		if err != nil {
			t.Fatalf("subsampling %q: New: %v", c.knob, err)
		}
		out, err := enc.EncodeStandalone(rgb, 64, 64)
		enc.Close()
		if err != nil {
			t.Fatalf("subsampling %q: encode: %v", c.knob, err)
		}
		h, v, ok := qualityjpeg.LumaSampling(out)
		if !ok {
			t.Fatalf("subsampling %q: could not parse SOF", c.knob)
		}
		if h != c.wantH || v != c.wantV {
			t.Errorf("subsampling %q: luma sampling = (%d,%d), want (%d,%d)", c.knob, h, v, c.wantH, c.wantV)
		}
	}
}

// TestJPEGEncoderRejectsBadSubsampling: an unknown subsampling value errors.
func TestJPEGEncoderRejectsBadSubsampling(t *testing.T) {
	_, err := jpegcodec.New(codec.LevelGeometry{TileWidth: 16, TileHeight: 16, PixelFormat: codec.PixelFormatRGB8},
		codec.Quality{Knobs: map[string]string{"subsampling": "411"}})
	if err == nil {
		t.Error("expected error for unknown subsampling 411, got nil")
	}
}
