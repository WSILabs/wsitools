//go:build !nojp2k

package jp2k

import (
	"testing"

	otdecoder "github.com/wsilabs/opentile-go/decoder"
	_ "github.com/wsilabs/opentile-go/decoder/all" // register the jpeg2000 decoder
	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/tiff"
)

// gradientRGB builds a w×h RGB888 tile with a deterministic gradient so the
// round-trip can detect transposition / channel-swap as well as gross loss.
func gradientRGB(w, h int) []byte {
	pix := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := (y*w + x) * 3
			pix[o] = byte(x)         // R ramps in x
			pix[o+1] = byte(y)       // G ramps in y
			pix[o+2] = byte((x + y)) // B
		}
	}
	return pix
}

func TestFactoryRegisteredAndTag(t *testing.T) {
	f, err := codec.Lookup("jpeg2000")
	if err != nil {
		t.Fatalf("codec.Lookup(jpeg2000): %v", err)
	}
	enc, err := f.NewEncoder(codec.LevelGeometry{TileWidth: 64, TileHeight: 64, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if got := enc.TIFFCompressionTag(); got != tiff.CompressionJPEG2000 {
		t.Errorf("TIFFCompressionTag = %d, want %d", got, tiff.CompressionJPEG2000)
	}
}

// TestLosslessRoundTripByteExact: reversible=true must round-trip pixel-exact.
func TestLosslessRoundTripByteExact(t *testing.T) {
	const w, h = 64, 48
	src := gradientRGB(w, h)
	enc, err := Factory{}.NewEncoder(
		codec.LevelGeometry{TileWidth: w, TileHeight: h, PixelFormat: codec.PixelFormatRGB8},
		codec.Quality{Knobs: map[string]string{"reversible": "true"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	j2k, err := enc.EncodeTile(src, w, h, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	if len(j2k) < 4 || j2k[0] != 0xFF || j2k[1] != 0x4F {
		t.Fatalf("output is not a J2K codestream (missing SOC FF4F): % x", j2k[:min(8, len(j2k))])
	}

	// opentile-go#53 (fixed in v0.45.1): the JPEG 2000 decoder now honors the
	// codestream MCT/colorspace, so our RGB+MCT output round-trips byte-exact
	// instead of being force-converted as YCbCr.
	fac, ok := otdecoder.Get("jpeg2000")
	if !ok {
		t.Skip("jpeg2000 decoder not registered")
	}
	dec := fac.New()
	defer dec.Close()
	img, err := dec.Decode(j2k, otdecoder.DecodeOptions{Format: otdecoder.PixelFormatRGB})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Width != w || img.Height != h {
		t.Fatalf("decoded dims = %d×%d, want %d×%d", img.Width, img.Height, w, h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w*3; x++ {
			got := img.Pix[y*img.Stride+x]
			want := src[y*w*3+x]
			if got != want {
				t.Fatalf("lossless mismatch at row %d col %d: got %d want %d", y, x, got, want)
			}
		}
	}
}

// TestLossyProducesDecodableOutput: default (lossy) encode must produce a
// decodable J2K of the right dims (pixels are approximate, not asserted).
func TestLossyProducesDecodableOutput(t *testing.T) {
	const w, h = 64, 64
	enc, _ := Factory{}.NewEncoder(
		codec.LevelGeometry{TileWidth: w, TileHeight: h, PixelFormat: codec.PixelFormatRGB8},
		codec.Quality{Knobs: map[string]string{"q": "80"}},
	)
	j2k, err := enc.EncodeTile(gradientRGB(w, h), w, h, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	fac, ok := otdecoder.Get("jpeg2000")
	if !ok {
		t.Skip("jpeg2000 decoder not registered")
	}
	dec := fac.New()
	defer dec.Close()
	img, err := dec.Decode(j2k, otdecoder.DecodeOptions{Format: otdecoder.PixelFormatRGB})
	if err != nil {
		t.Fatalf("decode lossy: %v", err)
	}
	if img.Width != w || img.Height != h {
		t.Errorf("decoded dims = %d×%d, want %d×%d", img.Width, img.Height, w, h)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
