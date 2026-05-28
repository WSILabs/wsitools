//go:build !nocgo

package jpeg

import (
	"bytes"
	"image"
	stdjpeg "image/jpeg"
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

func makeRGBPattern(w, h int) []byte {
	rgb := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w*3 + x*3
			rgb[i+0] = byte((x * 255) / w)
			rgb[i+1] = byte((y * 255) / h)
			rgb[i+2] = 128
		}
	}
	return rgb
}

func quality85() codec.Quality {
	return codec.Quality{Knobs: map[string]string{"q": "85"}}
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestEncodeStandaloneDecodesViaStdlib(t *testing.T) {
	enc, err := New(codec.LevelGeometry{TileWidth: 64, TileHeight: 64}, quality85())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer enc.Close()

	rgb := makeRGBPattern(64, 64)
	body, err := enc.EncodeStandalone(rgb, 64, 64)
	if err != nil {
		t.Fatalf("EncodeStandalone: %v", err)
	}

	if len(body) < 4 || body[0] != 0xFF || body[1] != 0xD8 {
		t.Fatalf("not a JPEG SOI prefix: %X", body[:4])
	}
	if body[len(body)-2] != 0xFF || body[len(body)-1] != 0xD9 {
		t.Errorf("not a JPEG EOI suffix")
	}

	// Must NOT contain APP14 marker.
	for i := 0; i+1 < len(body); i++ {
		if body[i] == 0xFF && body[i+1] == 0xEE {
			t.Errorf("vanilla output should not contain APP14 marker (offset %d)", i)
			break
		}
	}

	img, err := stdjpeg.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("stdlib decode: %v", err)
	}
	if img.Bounds() != image.Rect(0, 0, 64, 64) {
		t.Errorf("decoded bounds: %v, want 64x64", img.Bounds())
	}

	// Sample center pixel — should be approximately the encoded value.
	r, g, b, _ := img.At(32, 32).RGBA()
	rd, gd, bd := byte(r>>8), byte(g>>8), byte(b>>8)
	if absInt(int(rd)-127) > 20 || absInt(int(gd)-127) > 20 || absInt(int(bd)-128) > 20 {
		t.Errorf("center pixel decoded as (%d,%d,%d); expected near (127,127,128)", rd, gd, bd)
	}
}

func TestEncodeTileAbbreviatedRoundTrip(t *testing.T) {
	enc, err := New(codec.LevelGeometry{TileWidth: 32, TileHeight: 32}, quality85())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer enc.Close()

	rgb := makeRGBPattern(32, 32)
	tileBody, err := enc.EncodeTile(rgb, 32, 32, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}

	tables := enc.LevelHeader()
	if len(tables) < 4 {
		t.Fatalf("LevelHeader empty: %d bytes", len(tables))
	}
	// Build SOI + (tables sans SOI/EOI) + (tile sans SOI) for standalone-decodable bytes.
	spliced := append([]byte{0xFF, 0xD8}, tables[2:len(tables)-2]...)
	spliced = append(spliced, tileBody[2:]...)

	img, err := stdjpeg.Decode(bytes.NewReader(spliced))
	if err != nil {
		t.Fatalf("stdlib decode of spliced: %v", err)
	}
	if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
		t.Errorf("decoded dims: %v, want 32x32", img.Bounds())
	}
}

func TestFactoryRegistered(t *testing.T) {
	fac, err := codec.Lookup("jpeg")
	if err != nil {
		t.Fatalf("codec.Lookup(jpeg): %v", err)
	}
	if fac.Name() != "jpeg" {
		t.Errorf("factory Name: %q, want jpeg", fac.Name())
	}
}
