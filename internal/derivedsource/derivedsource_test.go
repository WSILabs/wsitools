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

func TestFromReducedL0_SourceShape(t *testing.T) {
	// 8×8 L0 raster, 4-px tiles, 2 levels (8×8 then 4×4).
	w, h := 8, 8
	raster := make([]byte, w*h*3)
	for i := range raster {
		raster[i] = 64
	}
	md := source.Metadata{MPPX: 1.0, MPPY: 1.0, MPP: 1.0, Magnification: 10}
	src, err := FromReducedL0(raster, w, h, 2 /*nLevels*/, 4 /*tileSize*/, 90, "svs", md, nil)
	if err != nil {
		t.Fatalf("FromReducedL0: %v", err)
	}
	defer src.Close()

	if src.Format() != "svs" {
		t.Errorf("Format = %q, want svs", src.Format())
	}
	if src.SourceImageDescription() != "" {
		t.Errorf("SourceImageDescription = %q, want empty", src.SourceImageDescription())
	}
	lv := src.Levels()
	if len(lv) != 2 {
		t.Fatalf("Levels = %d, want 2", len(lv))
	}
	if lv[0].Size() != (image.Point{X: 8, Y: 8}) {
		t.Errorf("L0 size = %v, want 8×8", lv[0].Size())
	}
	if lv[1].Size() != (image.Point{X: 4, Y: 4}) {
		t.Errorf("L1 size = %v, want 4×4 (box-halved)", lv[1].Size())
	}
	if lv[0].Compression() != source.CompressionJPEG {
		t.Errorf("L0 compression = %v, want JPEG", lv[0].Compression())
	}
	if src.Metadata().Magnification != 10 {
		t.Errorf("Magnification = %v, want 10 (passed through)", src.Metadata().Magnification)
	}
}

func TestFromReducedL0_RejectsZeroLevels(t *testing.T) {
	raster := make([]byte, 8*8*3)
	if _, err := FromReducedL0(raster, 8, 8, 0, 4, 90, "svs", source.Metadata{}, nil); err == nil {
		t.Fatal("expected error for nLevels=0, got nil")
	}
}

func TestFromReducedL0_TruncatesWhenRasterDegenerates(t *testing.T) {
	// 1×1 raster: a box-halve to 0×0 must stop the loop, so asking for 4 levels
	// yields just the single L0.
	raster := make([]byte, 1*1*3)
	src, err := FromReducedL0(raster, 1, 1, 4, 4, 90, "svs", source.Metadata{}, nil)
	if err != nil {
		t.Fatalf("FromReducedL0: %v", err)
	}
	defer src.Close()
	if n := len(src.Levels()); n != 1 {
		t.Errorf("Levels = %d, want 1 (truncated at degenerate dim)", n)
	}
}
