package bifwriter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func TestSpecShapedOpensInOpentile(t *testing.T) {
	src, err := source.Open(filepath.Join(fixtureDir(t), "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Skipf("open source: %v", err)
	}
	defer src.Close()

	// If BIF_SPIKE_OUT is set, write there for manual viewer testing; else tmp.
	out := os.Getenv("BIF_SPIKE_OUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "specshaped.bif")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSpecShaped(f, FromLevel(src.Levels()[0]), IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
		f.Close()
		t.Fatalf("WriteSpecShaped: %v", err)
	}
	f.Close()
	t.Logf("wrote spec-shaped BIF to %s", out)

	got, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen spec-shaped BIF: %v", err)
	}
	defer got.Close()
	if got.Format() != "bif" {
		t.Fatalf("detected %q, want bif", got.Format())
	}
}
