package main

import (
	"os"
	"path/filepath"
	"testing"
)

// extract of the uncompressed DICOM label must succeed and produce a PNG.
func TestExtractDICOMUncompressedLabel(t *testing.T) {
	bin := stripedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	out := filepath.Join(t.TempDir(), "label.png")
	if cmdOut, err := runBin(bin, "extract", "--type", "label", "-o", out, dir); err != nil {
		t.Fatalf("extract label: %v\n%s", err, cmdOut)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty %s: %v", out, err)
	}
}
