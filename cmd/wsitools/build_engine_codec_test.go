package main

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
)

// TestEngineCompressionTagIsCodecDerived asserts the codec interface buildEnginePyramid
// now relies on: jpeg→7 (CompressionJPEG), and a J2K-family codec reports a different tag.
func TestEngineCompressionTagIsCodecDerived(t *testing.T) {
	jenc, err := jpegcodec.Factory{}.NewEncoder(codec.LevelGeometry{TileWidth: 256, TileHeight: 256, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: map[string]string{"q": "85"}})
	if err != nil {
		t.Fatal(err)
	}
	defer jenc.Close()
	if jenc.TIFFCompressionTag() != 7 {
		t.Fatalf("jpeg TIFFCompressionTag = %d, want 7", jenc.TIFFCompressionTag())
	}
	fac, err := codec.Lookup("jpeg2000")
	if err != nil {
		t.Skip("jpeg2000 not built")
	}
	j2k, err := fac.NewEncoder(codec.LevelGeometry{TileWidth: 256, TileHeight: 256, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: map[string]string{"q": "85"}})
	if err != nil {
		t.Fatal(err)
	}
	defer j2k.Close()
	if j2k.TIFFCompressionTag() == 7 {
		t.Fatalf("jpeg2000 tag must differ from jpeg(7), got %d", j2k.TIFFCompressionTag())
	}
}
