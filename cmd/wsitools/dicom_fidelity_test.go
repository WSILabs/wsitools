package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cog-wsi tile-copy must preserve source pixels (pixel-hash equality) and
// key metadata (MPP, magnification) on a DICOM source.
func TestDICOMConvertCogWSIFidelity(t *testing.T) {
	bin := strippedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	out := filepath.Join(t.TempDir(), "o.cog.tiff")
	if o, err := runBin(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, dir); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	srcHash, err := runBin(bin, "hash", "--mode", "pixel", dir)
	if err != nil {
		t.Fatalf("hash src: %v\n%s", err, srcHash)
	}
	outHash, err := runBin(bin, "hash", "--mode", "pixel", out)
	if err != nil {
		t.Fatalf("hash out: %v\n%s", err, outHash)
	}
	if pixelDigest(srcHash) != pixelDigest(outHash) {
		t.Errorf("pixel hash mismatch:\n src=%s\n out=%s", srcHash, outHash)
	}
	info, err := runBin(bin, "info", out)
	if err != nil || !strings.Contains(string(info), "MPP:") || !strings.Contains(string(info), "Magnification:") {
		t.Errorf("expected MPP+Magnification in output info:\n%s", info)
	}
}

// pixelDigest extracts the sha256-pixel:<hex> token from a hash line.
func pixelDigest(out []byte) string {
	for _, f := range strings.Fields(string(out)) {
		if strings.HasPrefix(f, "sha256-pixel:") {
			return f
		}
	}
	return ""
}
