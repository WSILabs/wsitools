//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// TestConvertSVSJP2KTileCopy verifies the svs/jp2k asymmetry fix: a JPEG 2000
// source into svs (no --codec) is tile-copied verbatim rather than re-encoded
// (jp2k is a genuine Aperio SVS codec). The output keeps JPEG-2000 tiles and
// decodes to pixels identical to the source — proof the tiles were copied, not
// re-encoded (a jp2k re-encode would neither preserve exact pixels nor be the
// point).
func TestConvertSVSJP2KTileCopy(t *testing.T) {
	base := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(base); err != nil {
		t.Skipf("fixture missing: %s", base)
	}
	bin := buildOnce(t)
	dir := t.TempDir()

	// Build a JPEG-2000 SVS to use as the source.
	jp2kSrc := filepath.Join(dir, "src.jp2k.svs")
	if b, err := exec.Command(bin, "convert", "--to", "svs", "--codec", "jpeg2000", "-f", "-o", jp2kSrc, base).CombinedOutput(); err != nil {
		t.Fatalf("build jp2k source: %v\n%s", err, b)
	}

	// jp2k -> svs with NO --codec: must succeed (tile-copy path).
	out := filepath.Join(dir, "out.svs")
	if b, err := exec.Command(bin, "convert", "--to", "svs", "-f", "-o", out, jp2kSrc).CombinedOutput(); err != nil {
		t.Fatalf("jp2k -> svs (no codec): %v\n%s", err, b)
	}

	// Output L0 must still be JPEG 2000 (codec preserved, not switched to jpeg).
	tlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	if c := tlr.Levels()[0].Compression; c != opentile.CompressionJP2K {
		t.Errorf("output L0 compression = %v, want JPEG2000 (verbatim tile-copy)", c)
	}
	tlr.Close()

	// Pixels identical to the source → tiles were copied, not re-encoded.
	srcPix := pixelHash(t, bin, jp2kSrc)
	outPix := pixelHash(t, bin, out)
	if srcPix != outPix {
		t.Errorf("pixel hash changed (%s -> %s) — jp2k tiles were re-encoded, not copied", srcPix, outPix)
	}
}

func pixelHash(t *testing.T, bin, path string) string {
	t.Helper()
	b, err := exec.Command(bin, "hash", "--mode", "pixel", path).CombinedOutput()
	if err != nil {
		t.Fatalf("hash %s: %v\n%s", path, err, b)
	}
	// Output form: "sha256-pixel:<hex>  <path>" — take the first field (the hash),
	// not the trailing path (which differs between the two files).
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		t.Fatalf("empty hash output for %s", path)
	}
	return fields[0]
}
