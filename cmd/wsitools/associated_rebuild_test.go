package main

import (
	"image"
	"image/color"
	"testing"
)

func TestBuildReplacementAssocSpec_Codecs(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{10, 20, 30, 255})
		}
	}
	cases := []struct {
		name     string
		o        replaceOpts
		wantComp uint16
	}{
		{"label default lzw", replaceOpts{typ: "label"}, 5},
		{"overview default jpeg", replaceOpts{typ: "overview"}, 7},
		{"explicit jpeg", replaceOpts{typ: "label", compression: "jpeg"}, 7},
		{"explicit deflate", replaceOpts{typ: "label", compression: "deflate"}, 8},
		{"explicit none", replaceOpts{typ: "label", compression: "none"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, err := buildReplacementAssocSpec(img, c.o)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Compression != c.wantComp {
				t.Errorf("compression = %d, want %d", spec.Compression, c.wantComp)
			}
			if spec.Type != c.o.typ {
				t.Errorf("type = %q, want %q", spec.Type, c.o.typ)
			}
			if spec.Width != 40 || spec.Height != 20 {
				t.Errorf("dims = %dx%d, want 40x20", spec.Width, spec.Height)
			}
			if len(spec.Bytes) == 0 {
				t.Error("empty payload")
			}
		})
	}
}

// TestBuildReplacementStrippedSpec_Codecs mirrors TestBuildReplacementAssocSpec_Codecs
// for the OME-TIFF path. buildReplacementStrippedSpec defaults to JPEG for ALL
// types (including label) because opentile-go's OME-TIFF reader only decodes
// Compression 7 (JPEG) and 1 (none); LZW/Deflate round-trip is blocked.
func TestBuildReplacementStrippedSpec_Codecs(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{10, 20, 30, 255})
		}
	}
	cases := []struct {
		name     string
		o        replaceOpts
		wantComp uint16
	}{
		{"label default jpeg", replaceOpts{typ: "label"}, 7},
		{"overview default jpeg", replaceOpts{typ: "overview"}, 7},
		{"explicit lzw", replaceOpts{typ: "label", compression: "lzw"}, 5},
		{"explicit none", replaceOpts{typ: "label", compression: "none"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, err := buildReplacementStrippedSpec(img, c.o)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Compression != c.wantComp {
				t.Errorf("comp=%d want %d", spec.Compression, c.wantComp)
			}
			if spec.Width != 40 || spec.Height != 20 {
				t.Errorf("dims %dx%d", spec.Width, spec.Height)
			}
			if spec.WSIImageType != c.o.typ {
				t.Errorf("type=%q", spec.WSIImageType)
			}
			if len(spec.StripBytes) == 0 {
				t.Error("empty payload")
			}
		})
	}
}
