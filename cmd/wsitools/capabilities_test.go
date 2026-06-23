package main

import "testing"

func TestValidateCodec(t *testing.T) {
	cases := []struct {
		container, codec string
		allow            bool
		wantErr          bool
		wantWarn         bool
	}{
		// conformant → ok, no warn
		{"tiff", "jpeg2000", false, false, false},
		{"svs", "jpeg2000", false, false, false},
		{"cog-wsi", "avif", false, false, false},
		{"dzi", "png", false, false, false},
		{"dicom", "htj2k", false, false, false},
		// nonconformant → error by default, warn under --allow
		{"ome-tiff", "avif", false, true, false},
		{"ome-tiff", "avif", true, false, true},
		{"tiff", "jpegxl", false, true, false},
		{"tiff", "jpegxl", true, false, true},
		{"svs", "avif", false, true, false},
		{"svs", "avif", true, false, true},
		// unsupported → hard error regardless of --allow
		{"dicom", "avif", false, true, false},
		{"dicom", "avif", true, true, false},
		{"dzi", "avif", false, true, false},
		{"dzi", "avif", true, true, false},
		{"szi", "jpeg2000", true, true, false},
	}
	for _, c := range cases {
		t.Run(c.container+"/"+c.codec, func(t *testing.T) {
			warn, err := validateCodec(c.container, c.codec, c.allow)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if (warn != "") != c.wantWarn {
				t.Fatalf("warn=%q wantWarn=%v", warn, c.wantWarn)
			}
		})
	}
}

func TestContainerCapabilitiesIFE(t *testing.T) {
	caps := containerCapabilities("ife")
	for _, c := range []string{"jpeg", "avif"} {
		if !codecInSet(caps.conformant, c) {
			t.Errorf("ife should accept %s", c)
		}
	}
	for _, c := range []string{"jpeg2000", "htj2k", "jpegxl", "webp", "png"} {
		if codecInSet(caps.conformant, c) {
			t.Errorf("ife should NOT list %s conformant", c)
		}
	}
}
