package main

import (
	"image"
	"image/color"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/edit"
)

func solidImage(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func hasTagValue(rep *edit.ReplacementIFD, tag uint16, val uint64) bool {
	for _, tg := range rep.Tags {
		if tg.Tag == tag {
			// Compare against the little-endian-encoded inline bytes or count.
			// Decode first up-to-8 bytes of tg.Bytes as a uint, little-endian.
			var got uint64
			for i := 0; i < len(tg.Bytes) && i < 8; i++ {
				got |= uint64(tg.Bytes[i]) << (8 * uint(i))
			}
			if got == val {
				return true
			}
		}
	}
	return false
}

// asciiTag returns the NUL-trimmed ASCII value of a tag in rep, if present.
func asciiTag(rep *edit.ReplacementIFD, tag uint16) (string, bool) {
	for _, tg := range rep.Tags {
		if tg.Tag == tag {
			b := tg.Bytes
			for len(b) > 0 && b[len(b)-1] == 0 {
				b = b[:len(b)-1]
			}
			return string(b), true
		}
	}
	return "", false
}

// SVS classifies a trailing non-tiled page as Macro iff NewSubfileType==9, else
// Label (opentile-go formats/svs/series.go). The macro/overview replacement MUST
// carry 9 or it is misread as a second label, clobbering the real label.
func TestApplyAssocMarkers_SVSOverviewSubfile9(t *testing.T) {
	img := solidImage(1280, 430, color.RGBA{1, 2, 3, 255})
	rep, err := buildReplacementIFD(img, replaceOpts{typ: "overview", format: "svs", compression: "jpeg"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagValue(rep, edit.TagNewSubfileType, 9) {
		t.Errorf("SVS overview NewSubfileType != 9")
	}
	if v, ok := asciiTag(rep, tagWSIImageType); !ok || v != "overview" {
		t.Errorf("WSIImageType = %q ok=%v, want overview", v, ok)
	}
}

func TestApplyAssocMarkers_SVSLabelSubfile1(t *testing.T) {
	img := solidImage(200, 300, color.RGBA{1, 2, 3, 255})
	rep, err := buildReplacementIFD(img, replaceOpts{typ: "label", format: "svs", compression: "lzw"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagValue(rep, edit.TagNewSubfileType, 1) {
		t.Errorf("SVS label NewSubfileType != 1")
	}
}

// generic-TIFF classification treats the WSIImageType private tag as
// authoritative (opentile-go formats/generictiff/classifier.go), so a
// JPEG-encoded label isn't guessed as a thumbnail/overview.
func TestApplyAssocMarkers_GenericWSIImageType(t *testing.T) {
	img := solidImage(200, 300, color.RGBA{1, 2, 3, 255})
	rep, err := buildReplacementIFD(img, replaceOpts{typ: "label", format: "generic-tiff", compression: "jpeg"})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := asciiTag(rep, tagWSIImageType); !ok || v != "label" {
		t.Errorf("WSIImageType = %q ok=%v, want label", v, ok)
	}
}

func TestBuildReplacementLabelLZW(t *testing.T) {
	img := solidImage(8, 4, color.RGBA{200, 100, 50, 255})
	rep, err := buildReplacementIFD(img, replaceOpts{typ: "label", compression: "lzw", desc: "Aperio\nlabel 8x4"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagValue(rep, edit.TagCompression, 5) {
		t.Errorf("compression != LZW(5)")
	}
	if !hasTagValue(rep, edit.TagPredictor, 2) {
		t.Errorf("predictor != 2")
	}
	if len(rep.StripData) == 0 {
		t.Errorf("no strip data")
	}
}

func TestBuildReplacementLabelDefaultIsLZW(t *testing.T) {
	img := solidImage(8, 4, color.RGBA{1, 2, 3, 255})
	rep, err := buildReplacementIFD(img, replaceOpts{typ: "label"}) // no explicit compression
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagValue(rep, edit.TagCompression, 5) {
		t.Errorf("label default compression != LZW(5)")
	}
}

func TestBuildReplacementMacroJPEG(t *testing.T) {
	img := solidImage(16, 16, color.RGBA{10, 20, 30, 255})
	rep, err := buildReplacementIFD(img, replaceOpts{typ: "macro"}) // default for macro = JPEG
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagValue(rep, edit.TagCompression, 7) {
		t.Errorf("macro default compression != JPEG(7)")
	}
	if len(rep.StripData) == 0 {
		t.Errorf("no strip data")
	}
}
