//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateProbesDecodability guards wsitools#43: a structurally well-formed
// file whose tiles use a codec this reader can't decode (htj2k tiles forced into
// an SVS) used to report "valid" — false assurance, since region/hash fail on it.
// validate now probe-decodes an L0 tile and fails with an "undecodable-tile"
// finding. A genuinely-good slide still validates cleanly.
func TestValidateProbesDecodability(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)
	dir := t.TempDir()

	// Force an undecodable file: htj2k tiles into an SVS via --allow-nonconformant.
	htj2k := filepath.Join(dir, "src.htj2k.cog.tiff")
	if b, err := exec.Command(bin, "convert", "--to", "cog-wsi", "--codec", "htj2k", "-f", "-o", htj2k, src).CombinedOutput(); err != nil {
		t.Fatalf("build htj2k source: %v\n%s", err, b)
	}
	bad := filepath.Join(dir, "bad.svs")
	if b, err := exec.Command(bin, "convert", "--to", "svs", "--allow-nonconformant", "-f", "-o", bad, htj2k).CombinedOutput(); err != nil {
		t.Fatalf("build htj2k-in-svs: %v\n%s", err, b)
	}

	// validate must FAIL (exit 2) with the undecodable-tile finding.
	b, err := exec.Command(bin, "validate", bad).CombinedOutput()
	if err == nil {
		t.Fatalf("validate of an undecodable SVS unexpectedly passed:\n%s", b)
	}
	if !strings.Contains(string(b), "undecodable-tile") {
		t.Errorf("validate output missing the undecodable-tile finding:\n%s", b)
	}

	// A good slide still validates cleanly (no false positive).
	if b, err := exec.Command(bin, "validate", src).CombinedOutput(); err != nil {
		t.Fatalf("validate of a good slide should pass: %v\n%s", err, b)
	}
}
