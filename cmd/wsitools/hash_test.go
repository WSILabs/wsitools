package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashFileModeRejectsDirectory(t *testing.T) {
	bin := strippedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	out, err := runBin(bin, "hash", dir) // default --mode file
	if err == nil {
		t.Fatalf("expected error hashing a directory in file-mode, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "--mode pixel") {
		t.Errorf("error should point to --mode pixel, got:\n%s", out)
	}
}
