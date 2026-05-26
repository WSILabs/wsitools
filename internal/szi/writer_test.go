package szi

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/dzi"
	"github.com/wsilabs/wsitools/internal/source"
)

func TestSZIWriterProducesValidZip(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Config{
		Name: "cmu1", Width: 256, Height: 256,
		Format: "jpeg", TileSize: 256, Overlap: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteTile(0, 0, 0, []byte("FAKEJPEG-L0")); err != nil {
		t.Fatal(err)
	}
	max := dzi.MaxLevel(256, 256) // 8
	if err := w.WriteTile(max, 0, 0, []byte("FAKEJPEG-LMAX")); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteScanProperties(source.Metadata{Make: "Aperio"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range r.File {
		if f.Method != zip.Store {
			t.Errorf("%s: method=%d, want Store(0)", f.Name, f.Method)
		}
		found[f.Name] = true
	}
	for _, want := range []string{
		"cmu1/cmu1.dzi",
		"cmu1/cmu1_files/0/0_0.jpeg",
		"cmu1/cmu1_files/8/0_0.jpeg",
		"cmu1/scan-properties.xml",
	} {
		if !found[want] {
			t.Errorf("entry %q missing; got %v", want, keys(found))
		}
	}
	// scan-properties.xml contains Aperio.
	for _, f := range r.File {
		if f.Name == "cmu1/scan-properties.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			io.Copy(&b, rc)
			rc.Close()
			if !strings.Contains(b.String(), "Aperio") {
				t.Errorf("scan-properties missing Aperio")
			}
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
