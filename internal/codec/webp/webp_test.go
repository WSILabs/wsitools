//go:build !nowebp

package webp

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

func TestWebPEncoderRoundTrip(t *testing.T) {
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
	if enc.TIFFCompressionTag() != 50001 {
		t.Errorf("TIFFCompressionTag: got %d, want 50001", enc.TIFFCompressionTag())
	}
	// WebP RIFF: bytes 0-3 = "RIFF", bytes 8-11 = "WEBP".
	if len(tile) < 12 || string(tile[0:4]) != "RIFF" || string(tile[8:12]) != "WEBP" {
		end := 16
		if end > len(tile) {
			end = len(tile)
		}
		t.Errorf("not a valid WebP RIFF: % X", tile[:end])
	}
}

func TestWebPEncoder_Lossless(t *testing.T) {
	rgb := make([]byte, 64*64*3)
	for i := range rgb {
		rgb[i] = byte(i)
	}
	enc, err := (Factory{}).NewEncoder(codec.LevelGeometry{
		TileWidth: 64, TileHeight: 64,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"lossless": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	tile, err := enc.EncodeTile(rgb, 64, 64, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tile) == 0 {
		t.Fatal("empty lossless tile")
	}
}
