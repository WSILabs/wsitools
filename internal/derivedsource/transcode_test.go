//go:build !nocgo

package derivedsource

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

// solidLevel is a source.Level whose DecodedTile returns a solid-gray RGB tile,
// so transcodeLevel's decode→JPEG-re-encode path is testable without a real codec
// source.
type solidLevel struct {
	tile image.Point
	val  byte
}

func (l *solidLevel) Index() int                      { return 0 }
func (l *solidLevel) Size() image.Point               { return l.tile }
func (l *solidLevel) TileSize() image.Point           { return l.tile }
func (l *solidLevel) Grid() image.Point               { return image.Point{X: 1, Y: 1} }
func (l *solidLevel) Compression() source.Compression { return source.CompressionNone }
func (l *solidLevel) TileMaxSize() int                { return l.tile.X * l.tile.Y * 3 }
func (l *solidLevel) TileInto(x, y int, dst []byte) (int, error) {
	return 0, nil // unused: transcodeLevel reads via DecodedTile
}
func (l *solidLevel) DecodedTile(x, y int) (*decoder.Image, error) {
	pix := make([]byte, l.tile.X*l.tile.Y*3)
	for i := range pix {
		pix[i] = l.val
	}
	return &decoder.Image{Width: l.tile.X, Height: l.tile.Y, Stride: l.tile.X * 3, Format: decoder.PixelFormatRGB, Pix: pix}, nil
}

func TestTranscodeLevel_TileIntoEncodesDecodableJPEG(t *testing.T) {
	const ts = 16
	src := &solidLevel{tile: image.Point{X: ts, Y: ts}, val: 128}
	l := &transcodeLevel{src: src, quality: 90, index: 0, tileW: ts, tileH: ts}

	if l.Compression() != source.CompressionJPEG {
		t.Errorf("Compression = %v, want JPEG", l.Compression())
	}
	dst := make([]byte, l.TileMaxSize())
	n, err := l.TileInto(0, 0, dst)
	if err != nil {
		t.Fatalf("TileInto: %v", err)
	}
	// Must be a self-contained JPEG (decodable by the stdlib decoder).
	img, err := jpeg.Decode(bytes.NewReader(dst[:n]))
	if err != nil {
		t.Fatalf("TileInto produced non-decodable JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != ts || b.Dy() != ts {
		t.Errorf("decoded dims = %d×%d, want %d×%d", b.Dx(), b.Dy(), ts, ts)
	}
	r, _, _, _ := img.At(ts/2, ts/2).RGBA()
	if r8 := r >> 8; r8 < 118 || r8 > 138 {
		t.Errorf("center R = %d, want ≈128", r8)
	}
}

// fakeSource is a minimal source.Source over a fixed level list.
type fakeSource struct {
	levels []source.Level
	md     source.Metadata
}

func (s *fakeSource) Format() string                       { return "tiff" }
func (s *fakeSource) Levels() []source.Level               { return s.levels }
func (s *fakeSource) Associated() []source.AssociatedImage { return nil }
func (s *fakeSource) Metadata() source.Metadata            { return s.md }
func (s *fakeSource) SourceImageDescription() string       { return "" }
func (s *fakeSource) Close() error                         { return nil }

func TestTranscodeToJPEG_WrapsEveryLevel(t *testing.T) {
	src := &fakeSource{
		levels: []source.Level{
			&solidLevel{tile: image.Point{X: 16, Y: 16}, val: 64},
			&solidLevel{tile: image.Point{X: 8, Y: 8}, val: 64},
		},
		md: source.Metadata{Magnification: 20},
	}
	got := TranscodeToJPEG(src, 90, 2)
	if got.Format() != "tiff" {
		t.Errorf("Format = %q, want tiff (source's)", got.Format())
	}
	if n := len(got.Levels()); n != 2 {
		t.Fatalf("Levels = %d, want 2", n)
	}
	for i, lv := range got.Levels() {
		if lv.Compression() != source.CompressionJPEG {
			t.Errorf("level %d Compression = %v, want JPEG", i, lv.Compression())
		}
	}
	if got.Metadata().Magnification != 20 {
		t.Errorf("Magnification = %v, want 20 (passed through)", got.Metadata().Magnification)
	}
}
