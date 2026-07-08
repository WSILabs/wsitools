package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestInfoExtraFieldsDisplay covers the serial / ICC / properties surfacing:
// serial and ICC-presence always show when populated; properties show a count by
// default and the full sorted list under --properties.
func TestInfoExtraFieldsDisplay(t *testing.T) {
	r := &infoResult{
		Path:   "x.tiff",
		Format: "tiff",
		Metadata: infoMetadata{
			SerialNumber:    "CPAPERIOCS",
			ICCProfileBytes: 141992,
			Properties: map[string]string{
				"wsi-tools.source": "svs",
				"wsi-tools.codec":  "jpeg",
			},
		},
	}

	// Default: serial + ICC shown, properties as a count (not listed).
	infoProperties = false
	var buf bytes.Buffer
	if err := renderInfoText(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Serial:  CPAPERIOCS", "ICC:     present", "Properties: 2 (use --properties"} {
		if !strings.Contains(out, want) {
			t.Errorf("default output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "wsi-tools.source = svs") {
		t.Errorf("default output should not list properties\n%s", out)
	}

	// --properties: full sorted list.
	infoProperties = true
	defer func() { infoProperties = false }()
	buf.Reset()
	if err := renderInfoText(&buf, r); err != nil {
		t.Fatal(err)
	}
	out = buf.String()
	if !strings.Contains(out, "Properties (2):") || !strings.Contains(out, "wsi-tools.source = svs") {
		t.Errorf("--properties output missing full listing\n%s", out)
	}
	// sorted: codec before source
	if i, j := strings.Index(out, "wsi-tools.codec"), strings.Index(out, "wsi-tools.source"); !(i >= 0 && i < j) {
		t.Errorf("properties not sorted (codec should precede source)\n%s", out)
	}
}

// TestInfoWriterDisplay verifies the Writer line surfaces the file-writing
// software (distinct from the scanner Software) and is suppressed when it would
// merely duplicate Software. Guards the wsitools#-provenance behavior: a
// transcoded file preserves the source scanner in Software but records
// "wsitools/<ver>" in Writer, and info must show both.
func TestInfoWriterDisplay(t *testing.T) {
	for _, tc := range []struct {
		name         string
		software     string
		writer       string
		wantWriter   bool // expect a "Writer:" line
		wantContains string
	}{
		{"transcoded", "Aperio Image Library v11.2.1", "wsitools/0.26.0", true, "wsitools/0.26.0"},
		{"native_equal", "Aperio Image Library v10.0.51", "Aperio Image Library v10.0.51", false, ""},
		{"native_no_writer", "Aperio Image Library v10.0.51", "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := &infoResult{
				Path:   "x.tiff",
				Format: "tiff",
				Metadata: infoMetadata{
					Software: tc.software,
					Writer:   tc.writer,
				},
			}
			if err := renderInfoText(&buf, r); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			hasWriter := strings.Contains(out, "Writer:")
			if hasWriter != tc.wantWriter {
				t.Errorf("Writer line present = %v, want %v\n%s", hasWriter, tc.wantWriter, out)
			}
			if tc.wantContains != "" && !strings.Contains(out, tc.wantContains) {
				t.Errorf("output missing %q\n%s", tc.wantContains, out)
			}
		})
	}
}
