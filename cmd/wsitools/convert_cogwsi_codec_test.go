package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Regression: convert --to cog-wsi must HONOR --codec by re-encoding, not silently
// tile-copy the source JPEG. Before the fix, runConvertCOGWSI ignored --codec (and
// --quality/--tile-size) and always tile-copied, so a non-jpeg request produced a
// jpeg output while reporting success.
func TestConvertCOGWSIHonorsCodec(t *testing.T) {
	in := cmuFixture(t)
	for _, codec := range []string{"htj2k", "jpeg2000", "avif", "webp"} {
		t.Run(codec, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.tiff")
			if b, err := exec.Command(findBinary(t), "convert", "--to", "cog-wsi",
				"--codec", codec, "-o", out, in).CombinedOutput(); err != nil {
				t.Fatalf("convert --to cog-wsi --codec %s: %v\n%s", codec, err, b)
			}
			b, err := exec.Command(findBinary(t), "info", out).CombinedOutput()
			if err != nil {
				t.Fatalf("info: %v\n%s", err, b)
			}
			// Every pyramid level line must report the requested codec — if the
			// driver silently tile-copied, L0 would read back as jpeg.
			sawLevel := false
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "L") && strings.Contains(line, "tile") {
					sawLevel = true
					if !strings.Contains(line, codec) {
						t.Errorf("--codec %s: level line does not report %s (silent tile-copy?): %q", codec, codec, strings.TrimSpace(line))
					}
				}
			}
			if !sawLevel {
				t.Fatalf("no pyramid level lines in info output:\n%s", b)
			}
		})
	}
}
