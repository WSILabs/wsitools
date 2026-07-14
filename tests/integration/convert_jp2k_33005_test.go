//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

// l0Compression259 returns the raw TIFF Compression (tag 259) of a file's first
// IFD — the distinction opentile collapses (33003 YCbCr vs 33005 RGB both map to
// one JP2K enum), so the raw tag is the only way to assert the #44 fix.
func l0Compression259(t *testing.T, path string) uint16 {
	t.Helper()
	recs, err := source.WalkIFDs(path)
	if err != nil || len(recs) == 0 {
		t.Fatalf("WalkIFDs %s: %v (recs=%d)", path, err, len(recs))
	}
	return uint16(recs[0].Compression)
}

// TestConvertSVSJP2KTaggedRGB33005 guards wsitools#44: wsitools encodes RGB/sRGB
// JPEG 2000 but tagged it 33003 (Aperio YCbCr), so Aperio/OpenSlide readers
// mis-applied a YCbCr→RGB conversion. The encoder now emits 33005 (Aperio RGB),
// and a verbatim tile-copy preserves the source's raw J2K tag rather than
// collapsing it to 33003. Requires opentile-go ≥ v0.61.0 (reads 33005).
func TestConvertSVSJP2KTaggedRGB33005(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)
	dir := t.TempDir()

	// Encode path: --codec jpeg2000 must tag 33005 (was 33003).
	enc := filepath.Join(dir, "enc.svs")
	if b, err := exec.Command(bin, "convert", "--to", "svs", "--codec", "jpeg2000", "-f", "-o", enc, src).CombinedOutput(); err != nil {
		t.Fatalf("encode jp2k svs: %v\n%s", err, b)
	}
	if got := l0Compression259(t, enc); got != 33005 {
		t.Errorf("encoded jp2k SVS L0 Compression = %d, want 33005 (Aperio RGB)", got)
	}

	// Tile-copy path: jp2k source -> svs (no --codec) must PRESERVE 33005, not
	// downgrade to 33003.
	tc := filepath.Join(dir, "tc.svs")
	if b, err := exec.Command(bin, "convert", "--to", "svs", "-f", "-o", tc, enc).CombinedOutput(); err != nil {
		t.Fatalf("tile-copy jp2k -> svs: %v\n%s", err, b)
	}
	if got := l0Compression259(t, tc); got != 33005 {
		t.Errorf("tile-copied jp2k SVS L0 Compression = %d, want 33005 (raw tag preserved)", got)
	}

	// Both must still decode + validate through opentile v0.61.0 (which reads 33005).
	for _, p := range []string{enc, tc} {
		if b, err := exec.Command(bin, "validate", p).CombinedOutput(); err != nil {
			t.Errorf("validate %s failed (33005 must be decodable): %v\n%s", filepath.Base(p), err, b)
		}
	}
}
