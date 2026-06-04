package source

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildMultiSeriesDir symlinks the .dcm of two different-series fixtures
// (Leica-4 + 3DHISTECH-1) into one temp dir. Skips if fixtures/symlinks absent.
func buildMultiSeriesDir(t *testing.T) (dir string, oneLeicaDcm string) {
	t.Helper()
	base := os.Getenv("WSI_TOOLS_TESTDIR")
	if base == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	tmp := t.TempDir()
	var leicaDcm string
	for _, src := range []string{"Leica-4", "3DHISTECH-1"} {
		srcDir := filepath.Join(base, "dicom", src)
		dcms, _ := filepath.Glob(filepath.Join(srcDir, "*.dcm"))
		if len(dcms) == 0 {
			t.Skipf("fixture %s absent", src)
		}
		for i, d := range dcms {
			abs, _ := filepath.Abs(d)
			link := filepath.Join(tmp, src+"-"+filepath.Base(d))
			if err := os.Symlink(abs, link); err != nil {
				t.Skipf("symlink unsupported: %v", err)
			}
			if src == "Leica-4" && i == 0 {
				leicaDcm = link
			}
		}
	}
	return tmp, leicaDcm
}

func TestDICOMMultiSeriesDirIsAmbiguous(t *testing.T) {
	dir, _ := buildMultiSeriesDir(t)
	_, err := Open(dir)
	if err == nil {
		t.Fatal("expected ambiguity error opening a multi-series directory, got nil")
	}
	var amb *AmbiguousSeriesError
	if !errors.As(err, &amb) {
		t.Fatalf("expected *AmbiguousSeriesError, got %T: %v", err, err)
	}
	if len(amb.Series) < 2 {
		t.Errorf("expected >=2 candidate series, got %d", len(amb.Series))
	}
	// Message should be actionable.
	msg := err.Error()
	for _, want := range []string{"distinct WSM series", ".dcm"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

func TestDICOMSingleInstanceInMultiSeriesDirOpens(t *testing.T) {
	_, oneDcm := buildMultiSeriesDir(t)
	if oneDcm == "" {
		t.Skip("no anchor .dcm")
	}
	src, err := Open(oneDcm) // a named .dcm is never ambiguous
	if err != nil {
		t.Fatalf("expected single .dcm to open its own series, got: %v", err)
	}
	defer src.Close()
	if src.Format() != "dicom" {
		t.Errorf("Format = %q, want dicom", src.Format())
	}
}

func TestDICOMSingleSeriesDirOpens(t *testing.T) {
	base := os.Getenv("WSI_TOOLS_TESTDIR")
	if base == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	leicaDir := filepath.Join(base, "dicom", "Leica-4")
	if _, err := os.Stat(leicaDir); os.IsNotExist(err) {
		t.Skip("fixture dicom/Leica-4 absent")
	}
	src, err := Open(leicaDir)
	if err != nil {
		t.Fatalf("single-series dir should open without error, got: %v", err)
	}
	defer src.Close()
	if src.Format() != "dicom" {
		t.Errorf("Format = %q, want dicom", src.Format())
	}
}
