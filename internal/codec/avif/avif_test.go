//go:build !noavif

package avif

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

func TestAVIFEncoderRoundTrip(t *testing.T) {
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
	if enc.TIFFCompressionTag() != 60001 {
		t.Errorf("TIFFCompressionTag: got %d, want 60001", enc.TIFFCompressionTag())
	}
	// AVIF: ISOBMFF with 'ftyp' box at bytes 4-7.
	if len(tile) < 12 || string(tile[4:8]) != "ftyp" {
		end := 16
		if end > len(tile) {
			end = len(tile)
		}
		t.Errorf("not a valid AVIF (no ftyp box): % X", tile[:end])
	}
}
