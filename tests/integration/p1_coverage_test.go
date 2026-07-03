//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// P1 coverage gaps surfaced by the codec×container audit: capability-conformant
// (container, codec) pairs and transform flags whose EFFECT on the output was
// never asserted — the same blind spot that let `convert --to cog-wsi --codec X`
// silently tile-copy jpeg. Each test asserts the ACTUAL stored codec / quality,
// not just exit 0.

// H1: svs and ome-tiff both list jpeg2000 conformant, but only tiff/dicom had a
// test asserting the output actually stores JP2K. A driver that silently fell
// back to jpeg (the cog-wsi bug shape) would otherwise ship undetected on a real
// Aperio path.
func TestConvertJP2KOutputCodecSVSAndOMETIFF(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	for _, c := range []struct{ target, ext string }{
		{"svs", "svs"},
		{"ome-tiff", "ome.tiff"},
	} {
		t.Run(c.target, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out."+c.ext)
			if o, err := runCLI(bin, "convert", "--to", c.target, "--codec", "jpeg2000", "-o", out, src); err != nil {
				t.Fatalf("convert --to %s --codec jpeg2000: %v\n%s", c.target, err, o)
			}
			tlr, err := opentile.OpenFile(out)
			if err != nil {
				t.Fatalf("open %s: %v", out, err)
			}
			defer tlr.Close()
			lv := tlr.Levels()
			if len(lv) == 0 {
				t.Fatalf("no levels")
			}
			for i, l := range lv {
				if l.Compression != opentile.CompressionJP2K {
					t.Errorf("%s level %d compression = %v, want JP2K (--codec jpeg2000 honored)", c.target, i, l.Compression)
				}
			}
		})
	}
}

// H3: ife lists avif conformant, but every ife test used the default jpeg — the
// AVIF-in-IFE path (distinct Iris codec framing) was completely unexercised.
func TestConvertIFEAVIFOutputCodec(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.iris")
	if o, err := runCLI(bin, "convert", "--to", "ife", "--codec", "avif", "-o", out, src); err != nil {
		t.Fatalf("convert --to ife --codec avif: %v\n%s", err, o)
	}
	tlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open %s: %v", out, err)
	}
	defer tlr.Close()
	lv := tlr.Levels()
	if len(lv) == 0 {
		t.Fatalf("no levels")
	}
	if got := lv[0].Compression; got != opentile.CompressionAVIF {
		t.Errorf("ife L0 compression = %v, want AVIF (--codec avif honored, not jpeg)", got)
	}
}

// H4: --quality's effect on the output was verified only for crop. This asserts
// that convert honors it — a low quality yields a clearly lower estimated JPEG
// quality than a high one, so a driver that dropped the knob (wired the default)
// would be caught.
func TestConvertQualityAffectsOutput(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	dir := t.TempDir()
	lo := filepath.Join(dir, "lo.tiff")
	hi := filepath.Join(dir, "hi.tiff")
	if o, err := runCLI(bin, "convert", "--to", "tiff", "--codec", "jpeg", "--quality", "30", "-o", lo, src); err != nil {
		t.Fatalf("convert q30: %v\n%s", err, o)
	}
	if o, err := runCLI(bin, "convert", "--to", "tiff", "--codec", "jpeg", "--quality", "90", "-o", hi, src); err != nil {
		t.Fatalf("convert q90: %v\n%s", err, o)
	}
	qLo, qHi := tileQuality(t, lo), tileQuality(t, hi)
	if qLo >= qHi {
		t.Errorf("--quality 30 (Q≈%d) should be < --quality 90 (Q≈%d)", qLo, qHi)
	}
	if qLo > 60 {
		t.Errorf("--quality 30 estimated Q≈%d, expected clearly low (<60)", qLo)
	}
	if qHi < 80 {
		t.Errorf("--quality 90 estimated Q≈%d, expected clearly high (>=80)", qHi)
	}
}

// H4 (downsample): the format-preserving downsample has its own int --quality
// flag threaded through a separate signature; assert its effect too.
func TestDownsampleQualityAffectsOutput(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	dir := t.TempDir()
	lo := filepath.Join(dir, "lo.svs")
	hi := filepath.Join(dir, "hi.svs")
	if o, err := runCLI(bin, "downsample", "--factor", "2", "--quality", "30", "-o", lo, src); err != nil {
		t.Fatalf("downsample q30: %v\n%s", err, o)
	}
	if o, err := runCLI(bin, "downsample", "--factor", "2", "--quality", "90", "-o", hi, src); err != nil {
		t.Fatalf("downsample q90: %v\n%s", err, o)
	}
	qLo, qHi := tileQuality(t, lo), tileQuality(t, hi)
	if qLo >= qHi {
		t.Errorf("downsample --quality 30 (Q≈%d) should be < --quality 90 (Q≈%d)", qLo, qHi)
	}
	if qLo > 60 {
		t.Errorf("downsample --quality 30 estimated Q≈%d, expected clearly low (<60)", qLo)
	}
	if qHi < 80 {
		t.Errorf("downsample --quality 90 estimated Q≈%d, expected clearly high (>=80)", qHi)
	}
}
