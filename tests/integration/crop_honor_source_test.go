//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
	_ "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"
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

// tileQuality inspects the L0 tile's estimated JPEG quality via the same
// estimator `info` uses.
func tileQuality(t *testing.T, path string) int {
	t.Helper()
	sl, err := opentile.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer sl.Close()
	l0 := sl.Levels()[0]
	insp, ok := quality.For(l0.Compression)
	if !ok {
		t.Fatalf("no inspector for %v", l0.Compression)
	}
	b, err := l0.Tile(0, 0)
	if err != nil {
		t.Fatalf("tile: %v", err)
	}
	info, err := insp.Inspect(b)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	return info.QualityEstimate
}

// TestCropQualityFloorHonorsHigherSource: the default quality is a floor. A
// high-quality (Q95) source cropped WITHOUT --quality keeps a high quality
// (>85), while an explicit --quality 60 wins (well below the 85 floor).
func TestCropQualityFloorHonorsHigherSource(t *testing.T) {
	src := cmuFixture(t) // CMU-1-Small-Region.svs
	bin := buildOnce(t)
	dir := t.TempDir()

	// A Q95 JPEG tiff to crop.
	hq := filepath.Join(dir, "hq.tiff")
	if o, err := runCLI(bin, "convert", "--to", "tiff", "--codec", "jpeg", "--quality", "95", "-f", "-o", hq, src); err != nil {
		t.Fatalf("make Q95 tiff: %v\n%s", err, o)
	}

	// Crop with no --quality → floor honors the source's high quality.
	def := filepath.Join(dir, "def.tiff")
	if o, err := runCLI(bin, "crop", "--rect", "0,0,1500,1500", "-f", "-o", def, hq); err != nil {
		t.Fatalf("crop default: %v\n%s", err, o)
	}
	if q := tileQuality(t, def); q <= 85 {
		t.Errorf("default crop quality = %d, want >85 (source ~95 honored as floor)", q)
	}

	// Crop with explicit --quality 60 → user wins (below the floor).
	lo := filepath.Join(dir, "lo.tiff")
	if o, err := runCLI(bin, "crop", "--rect", "0,0,1500,1500", "--quality", "60", "-f", "-o", lo, hq); err != nil {
		t.Fatalf("crop q60: %v\n%s", err, o)
	}
	if q := tileQuality(t, lo); q >= 85 {
		t.Errorf("explicit --quality 60 crop quality = %d, want <85 (user choice wins over floor)", q)
	}
}
