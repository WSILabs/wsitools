package dicomwriter

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/wsilabs/wsitools/internal/source"
)

func openGrundium(t *testing.T) source.Source {
	t.Helper()
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no dicom fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// emitsAssociated reports whether writeAssociated would emit (vs skip) the image
// based on its codec (uses the production predicate so the rule lives in one place).
func emitsAssociated(a source.AssociatedImage) bool {
	return associatedSupported(a.Compression())
}

func TestWriteAssociated(t *testing.T) {
	src := openGrundium(t)
	defer src.Close()
	assoc := src.Associated()
	if len(assoc) == 0 {
		t.Skip("fixture has no associated images")
	}
	shared := newSharedUIDs()
	flavors := map[string]string{"label": "LABEL", "overview": "OVERVIEW", "macro": "OVERVIEW", "thumbnail": "THUMBNAIL"}
	for i, a := range assoc {
		if !emitsAssociated(a) {
			continue // non-JPEG associated image is skipped by writeAssociated
		}
		var buf bytes.Buffer
		if err := writeAssociated(&buf, src, a, shared, 100+i); err != nil {
			t.Fatalf("writeAssociated(%s): %v", a.Type(), err)
		}
		ds, err := dicom.Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
		if err != nil {
			t.Fatalf("parse %s: %v", a.Type(), err)
		}
		it, err := ds.FindElementByTag(tag.ImageType)
		if err != nil {
			t.Fatalf("%s ImageType: %v", a.Type(), err)
		}
		got := it.Value.GetValue().([]string)
		if len(got) < 3 || got[2] != flavors[a.Type()] {
			t.Errorf("%s ImageType[2] = %v, want %s", a.Type(), got, flavors[a.Type()])
		}
		if nf := firstStrA(t, ds, tag.NumberOfFrames); nf != "1" {
			t.Errorf("%s NumberOfFrames = %q, want 1", a.Type(), nf)
		}
		if s := firstStrA(t, ds, tag.SeriesInstanceUID); s != shared.Series {
			t.Errorf("%s SeriesInstanceUID = %q, want shared %q", a.Type(), s, shared.Series)
		}
		if fr := firstStrA(t, ds, tag.FrameOfReferenceUID); fr != shared.FrameOfReference {
			t.Errorf("%s FrameOfReferenceUID not shared", a.Type())
		}
	}
}

func TestWritePyramidWithAssociated(t *testing.T) {
	src := openGrundium(t)
	defer src.Close()
	if len(src.Associated()) == 0 {
		t.Skip("fixture has no associated images")
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
	// Levels present.
	for level := range src.Levels() {
		if bufs[fmt.Sprintf("level-%d", level)] == nil {
			t.Errorf("missing level-%d", level)
		}
	}
	// Associated present + shared Series + unique contiguous InstanceNumbers.
	var series string
	seen := map[string]bool{}
	insts := map[int]bool{}
	for name, b := range bufs {
		ds, err := dicom.Parse(bytes.NewReader(b.Bytes()), int64(b.Len()), nil)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		s := firstStrA(t, ds, tag.SeriesInstanceUID)
		if series == "" {
			series = s
		} else if s != series {
			t.Errorf("%s SeriesInstanceUID %q != %q", name, s, series)
		}
		sop := firstStrA(t, ds, tag.SOPInstanceUID)
		if seen[sop] {
			t.Errorf("duplicate SOPInstanceUID at %s", name)
		}
		seen[sop] = true
		inst, _ := strconv.Atoi(firstStrA(t, ds, tag.InstanceNumber))
		if insts[inst] {
			t.Errorf("duplicate InstanceNumber %d at %s", inst, name)
		}
		insts[inst] = true
	}
	for _, a := range src.Associated() {
		if !emitsAssociated(a) {
			continue // non-JPEG associated image is skipped (logged), no .dcm expected
		}
		if bufs[a.Type()] == nil {
			t.Errorf("missing associated %s.dcm", a.Type())
		}
	}
}

func firstStrA(t *testing.T, ds dicom.Dataset, tg tag.Tag) string {
	t.Helper()
	e, err := ds.FindElementByTag(tg)
	if err != nil {
		t.Fatalf("missing %v: %v", tg, err)
	}
	return e.Value.GetValue().([]string)[0]
}
