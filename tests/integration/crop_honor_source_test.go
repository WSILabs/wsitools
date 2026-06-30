//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// TestCropPreservesSourceCodec: crop only crops, so it must keep the source
// codec (a JPEG2000 SVS crop stays JPEG2000, not silently transcoded to JPEG).
func TestCropPreservesSourceCodec(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "crop.svs")
	if o, err := runCLI(bin, "crop", "--rect", "1000,1000,4000,4000", "-f", "-o", out, src); err != nil {
		t.Fatalf("crop: %v\n%s", err, o)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sl.Close()
	if c := sl.Levels()[0].Compression; c != opentile.CompressionJP2K {
		t.Errorf("crop L0 compression = %v, want JP2K (source codec preserved)", c)
	}
}

// TestCropPreservesSourceLevelRatios: a 4x-stepped source (CMU-2: 1x/4x/16x/32x)
// must crop to the same ratios, not a dense full-octave pyramid.
func TestCropPreservesSourceLevelRatios(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-2.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "crop.svs")
	if o, err := runCLI(bin, "crop", "--rect", "4000,4000,16000,16000", "-f", "-o", out, src); err != nil {
		t.Fatalf("crop: %v\n%s", err, o)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sl.Close()
	lv := sl.Levels()
	// Source CMU-2 is 1x/4x/16x/32x; a 16000 crop → 16000/4000/1000/500.
	wantW := []int{16000, 4000, 1000, 500}
	if len(lv) != len(wantW) {
		var got []int
		for _, l := range lv {
			got = append(got, l.Size.W)
		}
		t.Fatalf("crop level widths = %v, want %v (source ratios, not full octave)", got, wantW)
	}
	for i, w := range wantW {
		if lv[i].Size.W != w {
			t.Errorf("level %d width = %d, want %d", i, lv[i].Size.W, w)
		}
	}
}
