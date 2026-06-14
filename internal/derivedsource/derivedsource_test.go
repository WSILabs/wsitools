//go:build !nocgo

package derivedsource

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func TestRasterLevel_TileIntoEncodesDecodableJPEG(t *testing.T) {
	// 4×4 solid mid-gray raster, one 4-px tile.
	w, h, ts := 4, 4, 4
	raster := make([]byte, w*h*3)
	for i := range raster {
		raster[i] = 128
	}
	l := &rasterLevel{raster: raster, w: w, h: h, tileSize: ts, quality: 90, index: 0}

	if l.Compression() != source.CompressionJPEG {
		t.Errorf("Compression = %v, want JPEG", l.Compression())
	}
	if got := l.Size(); got != (image.Point{X: 4, Y: 4}) {
		t.Errorf("Size = %v, want 4×4", got)
	}
	if got := l.Grid(); got != (image.Point{X: 1, Y: 1}) {
		t.Errorf("Grid = %v, want 1×1", got)
	}

	dst := make([]byte, l.TileMaxSize())
	n, err := l.TileInto(0, 0, dst)
	if err != nil {
		t.Fatalf("TileInto: %v", err)
	}
	// The frame must be a self-contained JPEG — decodable by the stdlib decoder
	// (which has no shared-tables mechanism), proving it is NOT abbreviated.
	img, err := jpeg.Decode(bytes.NewReader(dst[:n]))
	if err != nil {
		t.Fatalf("TileInto produced non-decodable output (must be self-contained JPEG for DICOM): %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 4 || b.Dy() != 4 {
		t.Errorf("decoded dims = %d×%d, want 4×4", b.Dx(), b.Dy())
	}
	// Center pixel ≈ 128 (JPEG is lossy; allow tolerance).
	r, _, _, _ := img.At(2, 2).RGBA()
	r8 := r >> 8
	if r8 < 118 || r8 > 138 {
		t.Errorf("center R = %d, want ≈128", r8)
	}
}
