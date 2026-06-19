package main

import (
	"bytes"
	"image"
	stdjpeg "image/jpeg"
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	otdecoder "github.com/wsilabs/opentile-go/decoder"
	_ "github.com/wsilabs/opentile-go/decoder/all"
	_ "github.com/wsilabs/opentile-go/formats/all"
	"github.com/wsilabs/wsitools/internal/source"
)

// fakeAssoc is a minimal source.AssociatedImage for testing the thumbnail-regen
// list transform.
type fakeAssoc struct{ typ string }

func (f fakeAssoc) Type() string                     { return f.typ }
func (f fakeAssoc) Size() image.Point                { return image.Point{X: 10, Y: 10} }
func (f fakeAssoc) Compression() source.Compression  { return source.CompressionJPEG }
func (f fakeAssoc) Bytes() ([]byte, error)           { return []byte("orig"), nil }
func (f fakeAssoc) Decode(otdecoder.DecodeOptions) (*otdecoder.Image, error) {
	return nil, nil
}
func (f fakeAssoc) Source() (opentile.AssociatedEncoding, bool) {
	return opentile.AssociatedEncoding{}, false
}
func (f fakeAssoc) IFDOffset() (int64, bool) { return 0, false }

// regenCropThumbnailAssoc replaces the thumbnail in the list with one rendered
// from the crop L0; everything else passes through.
func TestRegenCropThumbnailAssoc(t *testing.T) {
	w, h := 2048, 1024
	l0 := make([]byte, w*h*3)
	for i := range l0 {
		l0[i] = 100
	}
	in := []source.AssociatedImage{fakeAssoc{typ: "label"}, fakeAssoc{typ: "thumbnail"}}
	out, err := regenCropThumbnailAssoc(in, l0, w, h, 90)
	if err != nil {
		t.Fatalf("regenCropThumbnailAssoc: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	// Label passes through unchanged.
	if out[0].Type() != "label" {
		t.Errorf("out[0] type = %q, want label (passthrough)", out[0].Type())
	}
	// Thumbnail replaced by a synthetic croppedThumbnail rendered from the crop.
	th, ok := out[1].(*croppedThumbnail)
	if !ok {
		t.Fatalf("out[1] = %T, want *croppedThumbnail", out[1])
	}
	if th.Type() != "thumbnail" || th.Compression() != source.CompressionJPEG {
		t.Errorf("regen type/comp = %q/%v, want thumbnail/JPEG", th.Type(), th.Compression())
	}
	wantW, wantH := thumbDims(w, h, thumbLongSide) // 1024×512
	if th.Size() != (image.Point{X: wantW, Y: wantH}) {
		t.Errorf("regen size = %v, want %dx%d", th.Size(), wantW, wantH)
	}
	jb, _ := th.Bytes()
	img, err := stdjpeg.Decode(bytes.NewReader(jb))
	if err != nil {
		t.Fatalf("regen thumbnail bytes not a decodable JPEG: %v", err)
	}
	if img.Bounds().Dx() != wantW || img.Bounds().Dy() != wantH {
		t.Errorf("decoded regen dims = %v, want %dx%d", img.Bounds(), wantW, wantH)
	}
}

func TestRenderCropThumbnail(t *testing.T) {
	l0 := make([]byte, 2000*1000*3)
	for i := range l0 {
		l0[i] = byte(i)
	}
	jpegBytes, tw, th, err := renderCropThumbnail(l0, 2000, 1000, 80)
	if err != nil {
		t.Fatalf("renderCropThumbnail: %v", err)
	}
	if tw != 1024 || th != 512 {
		t.Errorf("dims = %dx%d, want 1024x512", tw, th)
	}
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xFF || jpegBytes[1] != 0xD8 {
		t.Errorf("not a JPEG (no SOI marker), %d bytes", len(jpegBytes))
	}
}

func TestThumbDims(t *testing.T) {
	// Landscape: longest side → 1024.
	w, h := thumbDims(27836, 25633, 1024)
	if w != 1024 {
		t.Errorf("landscape W = %d, want 1024", w)
	}
	if h != 943 { // round(1024 * 25633/27836) = 943
		t.Errorf("landscape H = %d, want 943", h)
	}
	// Portrait: longest side is H.
	w, h = thumbDims(10000, 20000, 1024)
	if h != 1024 || w != 512 {
		t.Errorf("portrait = %dx%d, want 512x1024", w, h)
	}
	// Tiny image: never upscale.
	w, h = thumbDims(300, 200, 1024)
	if w != 300 || h != 200 {
		t.Errorf("tiny = %dx%d, want 300x200 (no upscale)", w, h)
	}
}

func TestStreamCropThumbnailDimsAndDecode(t *testing.T) {
	path := filepath.Join(testdir(), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	slide, err := opentile.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer slide.Close()
	rect := opentile.Region{Origin: opentile.Point{X: 100, Y: 100}, Size: opentile.Size{W: 1024, H: 1024}}
	jpegBytes, tw, th, err := streamCropThumbnail(slide, rect, 1024, 1024, 80)
	if err != nil {
		t.Fatalf("streamCropThumbnail: %v", err)
	}
	wantW, wantH := thumbDims(1024, 1024, thumbLongSide)
	if tw != wantW || th != wantH {
		t.Errorf("thumb dims = %d×%d, want %d×%d", tw, th, wantW, wantH)
	}
	img, derr := stdjpeg.Decode(bytes.NewReader(jpegBytes))
	if derr != nil {
		t.Fatalf("thumbnail not a decodable JPEG: %v", derr)
	}
	if b := img.Bounds(); b.Dx() != tw || b.Dy() != th {
		t.Errorf("decoded thumb = %v, want %d×%d", b, tw, th)
	}
}
