package png

import (
	"bytes"
	stdpng "image/png"
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

func TestPNGEncoderRoundTrip(t *testing.T) {
	const w, h = 64, 48
	rgb := make([]byte, w*h*3)
	for i := range rgb {
		rgb[i] = byte(i * 7)
	}
	enc, err := (Factory{}).NewEncoder(
		codec.LevelGeometry{TileWidth: w, TileHeight: h, PixelFormat: codec.PixelFormatRGB8},
		codec.Quality{},
	)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	tile, err := enc.EncodeTile(rgb, w, h, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	if len(tile) < 8 || string(tile[1:4]) != "PNG" {
		t.Fatalf("not a PNG: % X", tile)
	}
	img, err := stdpng.Decode(bytes.NewReader(tile))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("dims: got %v, want %dx%d", img.Bounds(), w, h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			i := y*w*3 + x*3
			if byte(r>>8) != rgb[i] || byte(g>>8) != rgb[i+1] || byte(b>>8) != rgb[i+2] {
				t.Fatalf("pixel (%d,%d) mismatch: got %d,%d,%d want %d,%d,%d",
					x, y, byte(r>>8), byte(g>>8), byte(b>>8), rgb[i], rgb[i+1], rgb[i+2])
			}
		}
	}
}

func TestPNGRegisteredAndNotTIFF(t *testing.T) {
	fac, err := codec.Lookup("png")
	if err != nil {
		t.Fatalf("png not registered: %v", err)
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{}, codec.Quality{})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	if got := enc.TIFFCompressionTag(); got != 0 {
		t.Errorf("TIFFCompressionTag = %d, want 0 (PNG is not a TIFF tile codec)", got)
	}
	if hdr := enc.LevelHeader(); hdr != nil {
		t.Errorf("LevelHeader = %v, want nil (PNG tiles are self-contained)", hdr)
	}
}
