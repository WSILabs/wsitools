package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --factor with dzi/szi is rejected.
func TestConvertFactorRejectsDZI(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.dzi")
	o, err := runBin(bin, "convert", "--to", "dzi", "--factor", "2", "-f", "-o", out, src)
	if err == nil || !strings.Contains(string(o), "factor") {
		t.Fatalf("expected --factor/dzi rejection, got err=%v\n%s", err, o)
	}
}

// invalid factor rejected (full message wired in a later task; here at least it must not silently pass).
func TestConvertFactorRejectsBadValue(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.svs")
	o, err := runBin(bin, "convert", "--to", "svs", "--factor", "3", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected invalid-factor rejection, got success:\n%s", o)
	}
}
