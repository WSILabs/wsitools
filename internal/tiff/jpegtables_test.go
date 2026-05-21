package tiff

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestExtractJPEGTables(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			im.SetRGBA(x, y, color.RGBA{R: byte(x * 32), G: byte(y * 32), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, im, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}

	tables, err := ExtractJPEGTables(buf.Bytes())
	if err != nil {
		t.Fatalf("ExtractJPEGTables: %v", err)
	}
	if !bytes.HasPrefix(tables, []byte{0xFF, 0xD8}) {
		t.Errorf("missing SOI prefix")
	}
	if !bytes.Contains(tables, []byte{0xFF, 0xDB}) {
		t.Errorf("missing DQT")
	}
	if !bytes.Contains(tables, []byte{0xFF, 0xC4}) {
		t.Errorf("missing DHT")
	}
	if bytes.Contains(tables, []byte{0xFF, 0xDA}) {
		t.Errorf("tables-only JPEG should not contain SOS")
	}
	if !bytes.HasSuffix(tables, []byte{0xFF, 0xD9}) {
		t.Errorf("missing EOI suffix")
	}
}

func TestStripJPEGTables(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	jpeg.Encode(&buf, im, &jpeg.Options{Quality: 80})

	abbrev, err := StripJPEGTables(buf.Bytes())
	if err != nil {
		t.Fatalf("StripJPEGTables: %v", err)
	}
	if bytes.Contains(abbrev, []byte{0xFF, 0xDB}) {
		t.Errorf("abbreviated tile should not contain DQT")
	}
	if bytes.Contains(abbrev, []byte{0xFF, 0xC4}) {
		t.Errorf("abbreviated tile should not contain DHT")
	}
	if !bytes.Contains(abbrev, []byte{0xFF, 0xDA}) {
		t.Errorf("abbreviated tile should still contain SOS")
	}
}
