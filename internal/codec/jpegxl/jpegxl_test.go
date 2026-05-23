//go:build !nojxl

package jpegxl

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

func TestJPEGXLEncoderRoundTrip(t *testing.T) {
	rgb := make([]byte, 256*256*3)
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			off := (y*256 + x) * 3
			rgb[off+0] = byte(x)
			rgb[off+1] = byte(y)
			rgb[off+2] = 128
		}
	}

	enc, err := (Factory{}).NewEncoder(codec.LevelGeometry{
		TileWidth: 256, TileHeight: 256,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": "85"}})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	tile, err := enc.EncodeTile(rgb, 256, 256, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	if len(tile) == 0 {
		t.Fatal("encoded tile is empty")
	}
	if enc.TIFFCompressionTag() != 50002 {
		t.Errorf("TIFFCompressionTag: got %d, want 50002 (Adobe-allocated draft for JPEG-XL)", enc.TIFFCompressionTag())
	}

	// JPEG-XL codestream signature checks: FF 0A (codestream) OR an "JXL "
	// FourCC at bytes 4-7 (boxed container).
	if len(tile) < 12 {
		t.Fatal("tile too short for signature check")
	}
	hasJxlSignature := (tile[0] == 0xFF && tile[1] == 0x0A) ||
		(string(tile[4:8]) == "JXL ")
	if !hasJxlSignature {
		end := 16
		if end > len(tile) {
			end = len(tile)
		}
		t.Errorf("tile bytes don't start with a recognised JPEG-XL signature: % X", tile[:end])
	}
}
