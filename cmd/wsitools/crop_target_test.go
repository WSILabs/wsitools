package main

import "testing"

// TestCropDefaultTargetFromSource documents that an empty target resolves to the
// source format's container (the format-preserving default). Guards the Task 1
// refactor: parameterizing runCrop's target must NOT change crop's behavior.
// The input strings are opentile Format values (what src.Format() returns);
// the output strings are the writer target names used in runCrop's switch.
func TestCropDefaultTargetFromSource(t *testing.T) {
	cases := []struct {
		format string
		want   string
		ok     bool
	}{
		{"svs", "svs", true},
		{"generic-tiff", "tiff", true},
		{"ome-tiff", "ome-tiff", true},
		{"cog-wsi", "cog-wsi", true},
		{"dicom", "dicom", true},
	}
	for _, c := range cases {
		got, ok := downsampleTargetForFormat(c.format)
		if ok != c.ok || got != c.want {
			t.Fatalf("downsampleTargetForFormat(%q) = (%q,%v), want (%q,%v)", c.format, got, ok, c.want, c.ok)
		}
	}
}
