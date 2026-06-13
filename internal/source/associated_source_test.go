package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssociatedSourceLabel(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no CMU fixture")
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var found bool
	for _, a := range s.Associated() {
		if a.Type() != "label" {
			continue
		}
		found = true
		src, ok := a.Source()
		if !ok {
			t.Fatal("label Source() ok=false, want faithful source")
		}
		if len(src.Strips) < 2 {
			t.Errorf("label strips=%d, want >1 (CMU label is 67 strips)", len(src.Strips))
		}
		if src.Predictor != 2 {
			t.Errorf("label predictor=%d, want 2", src.Predictor)
		}
	}
	if !found {
		t.Skip("fixture has no label")
	}
}
