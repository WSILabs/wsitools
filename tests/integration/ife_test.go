//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

func TestConvertToIFE_RoundTrip(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out.iris")
	bin := buildOnce(t)
	if b, err := exec.Command(bin, "convert", "--to", "ife", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert --to ife: %v\n%s", err, b)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer sl.Close()
	if string(sl.Format()) != "ife" {
		t.Errorf("format = %q, want ife", sl.Format())
	}
	if len(sl.Levels()) == 0 {
		t.Fatal("no levels")
	}
	// PADDING QUIRK: L0 dims = ceil(srcW/256)*256 x ceil(srcH/256)*256.
	// CMU-1-Small-Region is 2220x2967 -> 2304x3072.
	if w, h := sl.Levels()[0].Size.W, sl.Levels()[0].Size.H; w != 2304 || h != 3072 {
		t.Errorf("L0 = %dx%d, want 2304x3072 (256-padded)", w, h)
	}
}
