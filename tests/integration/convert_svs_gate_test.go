//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestConvertSVSGatesNonConformantDefaultCodec guards wsitools#42: converting a
// source whose tile codec is non-conformant for SVS (htj2k/avif/webp/jpegxl)
// into svs with NO --codec must be gated — it previously re-encoded the source
// codec verbatim into the SVS (the defaulted codec bypassed the conformance
// check that only ran for an explicit --codec), yielding an undecodable SVS with
// exit 0. It must now error like an explicit non-conformant codec, while
// --codec jpeg (re-encode) and --allow-nonconformant still work.
func TestConvertSVSGatesNonConformantDefaultCodec(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)
	dir := t.TempDir()

	// Build an htj2k-tiled cog-wsi to use as the non-conformant-for-SVS source.
	htj2k := filepath.Join(dir, "src.htj2k.cog.tiff")
	if b, err := exec.Command(bin, "convert", "--to", "cog-wsi", "--codec", "htj2k", "-f", "-o", htj2k, src).CombinedOutput(); err != nil {
		t.Fatalf("build htj2k source: %v\n%s", err, b)
	}

	// htj2k -> svs with NO --codec must ERROR (was: silent undecodable SVS).
	out := filepath.Join(dir, "out.svs")
	b, err := exec.Command(bin, "convert", "--to", "svs", "-f", "-o", out, htj2k).CombinedOutput()
	if err == nil {
		t.Fatalf("htj2k -> svs (no --codec) unexpectedly succeeded; want a non-conformant error\n%s", b)
	}
	if !strings.Contains(string(b), "non-conformant") {
		t.Errorf("error should explain non-conformance, got:\n%s", b)
	}

	// --codec jpeg re-encodes to a conformant, decodable SVS.
	outj := filepath.Join(dir, "out.jpeg.svs")
	if b, err := exec.Command(bin, "convert", "--to", "svs", "--codec", "jpeg", "-f", "-o", outj, htj2k).CombinedOutput(); err != nil {
		t.Fatalf("htj2k -> svs --codec jpeg should succeed: %v\n%s", err, b)
	}
	if b, err := exec.Command(bin, "hash", "--mode", "pixel", outj).CombinedOutput(); err != nil {
		t.Fatalf("re-encoded SVS should decode: %v\n%s", err, b)
	}
}
