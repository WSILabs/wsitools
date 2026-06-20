package dicomwriter

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/WSILabs/dicom"
	"github.com/WSILabs/dicom/pkg/tag"
)

// optStrA returns the first string value of a tag, or "" if the tag is absent.
func optStrA(ds dicom.Dataset, tg tag.Tag) string {
	e, err := ds.FindElementByTag(tg)
	if err != nil {
		return ""
	}
	v, ok := e.Value.GetValue().([]string)
	if !ok || len(v) == 0 {
		return ""
	}
	return v[0]
}

// TestWritePyramidStampsPyramidUID guards the DICOM Pyramid IOD linkage (D7
// finding): every VOLUME (pyramid level) instance carries the SAME PyramidUID,
// and associated images carry NONE.
func TestWritePyramidStampsPyramidUID(t *testing.T) {
	src := openGrundium(t)
	defer src.Close()
	if len(src.Levels()) < 2 {
		t.Skip("need a multi-level source to exercise pyramid linkage")
	}

	bufs := map[string]*bytes.Buffer{}
	factory := func(name string) (io.WriteCloser, error) {
		b := &bytes.Buffer{}
		bufs[name] = b
		return nopWriteCloser{b}, nil
	}
	if err := WritePyramid(src, Options{Associated: true}, factory); err != nil {
		t.Fatalf("WritePyramid: %v", err)
	}

	var pyramid string
	volumes := 0
	for name, b := range bufs {
		ds, err := dicom.Parse(bytes.NewReader(b.Bytes()), int64(b.Len()), nil)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		p := optStrA(ds, tag.PyramidUID)
		if strings.HasPrefix(name, "level-") {
			volumes++
			if p == "" {
				t.Errorf("VOLUME %s missing PyramidUID", name)
				continue
			}
			if pyramid == "" {
				pyramid = p
			} else if p != pyramid {
				t.Errorf("VOLUME %s PyramidUID %q != shared %q", name, p, pyramid)
			}
		} else if p != "" {
			t.Errorf("associated %s has PyramidUID %q (must be absent)", name, p)
		}
	}
	if pyramid == "" {
		t.Fatal("no VOLUME PyramidUID emitted")
	}
	if volumes < 2 {
		t.Fatalf("expected >=2 VOLUME levels, got %d", volumes)
	}
}
