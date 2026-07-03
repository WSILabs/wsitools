//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// TestConvertSZIRoundTrips guards wsitools#26: `convert --to szi` used to emit a
// scan-properties.xml rooted at <scan-properties>, but the real format (and
// opentile-go's reader) expects <image> — so every szi output was unreadable.
// The output must reopen AND round-trip the source scale metadata (which the
// corrected property names now carry).
func TestConvertSZIRoundTrips(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.szi")
	if o, err := runCLI(bin, "convert", "--to", "szi", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to szi: %v\n%s", err, o)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("szi output does not reopen (wsitools#26 regression): %v", err)
	}
	defer sl.Close()
	if sl.Format() != opentile.FormatSZI {
		t.Errorf("reopened format = %v, want szi", sl.Format())
	}
	// CMU-1-Small-Region is 20x / MPP 0.499; the corrected scan-properties names
	// (ObjectiveMagnification / MicronsPerPixel*) must carry them into the szi.
	if m := sl.Metadata().Magnification; m != 20 {
		t.Errorf("szi magnification = %v, want 20 (metadata round-trip)", m)
	}
}
