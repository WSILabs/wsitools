package jpeg

import (
	"bytes"
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/decoder"
)

// TestJPEGEncoderRoundTrip verifies encode→decode fidelity using libjpeg-turbo
// on the decode side. The Go stdlib image/jpeg decoder is NOT used here because
// it does not honor the Adobe APP14 marker that this encoder writes — stdlib
// always applies the YCbCr→RGB inverse transform regardless of marker, which
// hue-rotates output that's stored as raw RGB. libjpeg-turbo's tjDecompress2
// (and openslide / QuPath / libvips) honors APP14 and reproduces the source
// pixels.
func TestJPEGEncoderRoundTrip(t *testing.T) {
	// 256x256 RGB tile with a gradient.
	rgb := make([]byte, 256*256*3)
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			off := (y*256 + x) * 3
			rgb[off+0] = byte(x)
			rgb[off+1] = byte(y)
			rgb[off+2] = 128
		}
	}

	fac := Factory{}
	enc, err := fac.NewEncoder(codec.LevelGeometry{
		TileWidth: 256, TileHeight: 256,
		PixelFormat: codec.PixelFormatRGB8,
		ColorSpace:  codec.ColorSpace{Name: "sRGB"},
	}, codec.Quality{Knobs: map[string]string{"q": "85"}})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	tile, err := enc.EncodeTile(rgb, 256, 256, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	if enc.TIFFCompressionTag() != 7 {
		t.Errorf("TIFFCompressionTag: got %d, want 7", enc.TIFFCompressionTag())
	}

	tables := enc.LevelHeader()
	if len(tables) == 0 {
		t.Fatal("LevelHeader: empty")
	}
	// Splice tables + tile into a self-contained JPEG, then decode with
	// libjpeg-turbo's tjDecompress2 (which honors APP14).
	spliced := spliceForDecode(tables, tile)
	if spliced == nil {
		t.Fatal("splice produced nil")
	}
	dec := decoder.NewJPEG()
	out, err := dec.DecodeTile(spliced, nil, 1, 1)
	if err != nil {
		t.Fatalf("DecodeTile: %v", err)
	}
	if got, want := len(out), 256*256*3; got != want {
		t.Errorf("decoded length: got %d, want %d", got, want)
	}
	// Spot-check pixel (10, 20) — RGB888-packed at offset (20*256 + 10)*3.
	off := (20*256 + 10) * 3
	gotR, gotG, gotB := int(out[off]), int(out[off+1]), int(out[off+2])
	// JPEG drift tolerance at q=85: lossy compression on a smooth gradient
	// should be tight (≤8 per channel).
	if abs(gotR-10) > 8 || abs(gotG-20) > 8 || abs(gotB-128) > 8 {
		t.Errorf("pixel (10,20) round-trip drift too large: got R=%d G=%d B=%d, want ~(10, 20, 128)", gotR, gotG, gotB)
	}
}

// spliceForDecode joins a tables-only JPEG (SOI + DQT + DHT + EOI) with an
// abbreviated tile (SOI + APP14 + SOF + SOS + entropy + EOI) into a
// self-contained JPEG by dropping the tables' EOI and the tile's SOI.
func spliceForDecode(tables, tile []byte) []byte {
	if !bytes.HasSuffix(tables, []byte{0xFF, 0xD9}) || !bytes.HasPrefix(tile, []byte{0xFF, 0xD8}) {
		return nil
	}
	out := make([]byte, 0, len(tables)+len(tile)-4)
	out = append(out, tables[:len(tables)-2]...) // tables minus EOI
	out = append(out, tile[2:]...)               // tile minus SOI
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
