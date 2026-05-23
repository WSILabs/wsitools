//go:build !nohtj2k

package htj2k

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

func TestHTJ2KEncoderRoundTrip(t *testing.T) {
	rgb := make([]byte, 256*256*3)
	for i := range rgb {
		rgb[i] = byte(i)
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
	if enc.TIFFCompressionTag() != 60003 {
		t.Errorf("TIFFCompressionTag: got %d, want 60003", enc.TIFFCompressionTag())
	}
	// J2K codestream signature: SOC marker = 0xFF 0x4F.
	if len(tile) < 2 || tile[0] != 0xFF || tile[1] != 0x4F {
		end := 8
		if end > len(tile) {
			end = len(tile)
		}
		t.Errorf("not a J2K codestream (no SOC marker): % X", tile[:end])
	}
}
