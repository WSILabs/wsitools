//go:build integration

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestValidateColorspaceMismatch guards the wsitools#44 validate check: a JPEG
// 2000 SVS whose RGB tiles are tagged 33003 (Aperio YCbCr) decodes fine here but
// renders wrong colors in Aperio-family readers. validate now probe-inspects the
// L0 codestream and, on a clear tag-vs-content mismatch, emits a warning
// "colorspace-mismatch" finding (which fails only under --strict). A correctly
// tagged (33005) file must NOT trigger it.
func TestValidateColorspaceMismatch(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)
	dir := t.TempDir()

	// Correct: RGB JPEG 2000 tagged 33005 — must validate clean.
	good := filepath.Join(dir, "good.svs")
	if b, err := exec.Command(bin, "convert", "--to", "svs", "--codec", "jpeg2000", "-f", "-o", good, src).CombinedOutput(); err != nil {
		t.Fatalf("build jp2k svs: %v\n%s", err, b)
	}
	if b, err := exec.Command(bin, "validate", good).CombinedOutput(); err != nil {
		t.Errorf("correct 33005 file should validate clean: %v\n%s", err, b)
	}

	// Mislabel it: patch every Compression(259) SHORT entry from 33005 to 33003,
	// keeping the RGB codestream — the #44 class of bug. (classic TIFF, little-
	// endian: tag=0x0103, type=SHORT, count=1, inline value 33005=0x80FD.)
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	entry33005 := []byte{0x03, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0xFD, 0x80}
	if !bytes.Contains(raw, entry33005) {
		t.Skip("could not locate a classic-TIFF LE 33005 Compression entry to patch (BigTIFF/BE?)")
	}
	entry33003 := []byte{0x03, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0xEB, 0x80}
	bad := filepath.Join(dir, "bad.svs")
	if err := os.WriteFile(bad, bytes.ReplaceAll(raw, entry33005, entry33003), 0o644); err != nil {
		t.Fatal(err)
	}

	// Default: passes (exit 0) but reports the colorspace-mismatch warning.
	out, err := exec.Command(bin, "validate", bad).CombinedOutput()
	if err != nil {
		t.Fatalf("mislabeled file should still pass by default (warning, not error): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("colorspace-mismatch")) {
		t.Errorf("validate missing colorspace-mismatch finding for a 33003-tagged RGB file:\n%s", out)
	}

	// --strict: a warning fails (exit 2).
	if err := exec.Command(bin, "validate", "--strict", bad).Run(); err == nil {
		t.Errorf("validate --strict should fail on a colorspace-mismatch")
	}
}
