//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
	qualityjpeg "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"
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

// TestCropLosslessUniformCodec guards wsitools#28: lossless crop of a non-JPEG
// source used to produce a mixed-codec pyramid (L0 verbatim jpeg2000, reduced
// levels re-encoded to jpeg) because the raster reduced-level encoder hardcoded
// JPEG. Every level must now share the source codec.
func TestCropLosslessUniformCodec(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "crop.svs")
	if o, err := runCLI(bin, "crop", "--lossless", "--x", "1000", "--y", "1000", "--w", "4096", "--h", "4096", "-f", "-o", out, src); err != nil {
		t.Fatalf("lossless crop: %v\n%s", err, o)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sl.Close()
	lv := sl.Levels()
	if len(lv) < 2 {
		t.Fatalf("expected a multi-level pyramid, got %d", len(lv))
	}
	for i, l := range lv {
		if l.Compression != opentile.CompressionJP2K {
			t.Errorf("level %d codec = %v, want JP2K (uniform with the verbatim L0, not a mixed pyramid)", i, l.Compression)
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

// jpegSubsampling reads a JPEG tile's chroma subsampling from its SOF marker.
func jpegSubsampling(t *testing.T, tile []byte) string {
	t.Helper()
	h, v, ok := qualityjpeg.LumaSampling(tile)
	if !ok {
		t.Fatalf("not a decodable JPEG tile (no SOF)")
	}
	switch {
	case h == 1 && v == 1:
		return "4:4:4"
	case h == 2 && v == 1:
		return "4:2:2"
	case h == 1 && v == 2:
		return "4:4:0"
	case h == 2 && v == 2:
		return "4:2:0"
	}
	return fmt.Sprintf("%dx%d", h, v)
}

// TestCropLosslessSubsamplingConsistent: a lossless crop copies L0 verbatim but
// re-encodes the reduced levels (raster path). Those levels must HONOR the source
// chroma subsampling (match L0) rather than fall back to the encoder default
// 4:2:0 — otherwise the pyramid is internally inconsistent AND the
// YCbCrSubSampling tag (derived from the source) contradicts the actual bytes.
// Regression for the CMU-2 (4:4:4) lossless-crop bug: L0 was 4:4:4 but every
// reduced level came out 4:2:0 while the tag claimed 4:4:4.
func TestCropLosslessSubsamplingConsistent(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-2.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "crop.svs")
	if o, err := runCLI(bin, "crop", "--lossless", "--x", "10000", "--y", "5000", "--w", "8192", "--h", "8192", "-f", "-o", out, src); err != nil {
		t.Fatalf("lossless crop: %v\n%s", err, o)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sl.Close()
	lv := sl.Levels()
	if len(lv) < 2 {
		t.Fatalf("expected a multi-level pyramid, got %d level(s)", len(lv))
	}
	b0, err := lv[0].Tile(0, 0)
	if err != nil {
		t.Fatalf("L0 tile: %v", err)
	}
	l0ss := jpegSubsampling(t, b0)
	if l0ss != "4:4:4" {
		t.Fatalf("L0 subsampling = %s, want 4:4:4 (CMU-2 is 4:4:4, copied verbatim)", l0ss)
	}
	for i := 1; i < len(lv); i++ {
		b, err := lv[i].Tile(0, 0)
		if err != nil {
			t.Fatalf("level %d tile: %v", i, err)
		}
		if ss := jpegSubsampling(t, b); ss != l0ss {
			t.Errorf("level %d subsampling = %s, want %s (reduced levels must honor the source, not default to 4:2:0)", i, ss, l0ss)
		}
	}
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
