package main

import (
	"bytes"
	"strings"
	"testing"
)

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
