package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssociatedIFDOffset(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	// Try a couple of known SVS fixture names.
	candidates := []string{
		filepath.Join(dir, "svs", "CMU-1-Small-Region.svs"),
		filepath.Join(dir, "svs", "CMU-1.svs"),
	}
	var p string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			p = c
			break
		}
	}
	if p == "" {
		t.Skip("no SVS fixture present")
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var sawAssoc bool
	for _, a := range s.Associated() {
		sawAssoc = true
		off, ok := a.IFDOffset()
		if ok && off <= 0 {
			t.Errorf("%s: ok but offset %d <= 0", a.Type(), off)
		}
		// For SVS, label/macro should resolve to a positive offset.
		if (a.Type() == "label" || a.Type() == "macro") && (!ok || off <= 0) {
			t.Errorf("%s: IFDOffset ok=%v off=%d, want ok+positive", a.Type(), ok, off)
		}
	}
	if !sawAssoc {
		t.Skip("fixture has no associated images")
	}
}
