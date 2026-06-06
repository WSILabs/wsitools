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
