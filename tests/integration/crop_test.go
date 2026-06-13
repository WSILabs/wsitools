//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	_ "github.com/wsilabs/opentile-go/decoder/all"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// TestCrop_CMU2ParityOracle crops CMU-2.svs to the same rect ImageScope used and
// asserts the decoded L0 matches the ImageScope crop within one JPEG generation.
func TestCrop_CMU2ParityOracle(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-2.svs")
	oracle := filepath.Join(td, "svs", "CMU-2_cropped_46492_3599_27836_25633_imagescope.svs")
	for _, p := range []string{src, oracle} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("fixture missing: %s", p)
		}
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "crop.svs")

	cmd := exec.Command(bin, "crop", "--rect", "46492,3599,27836,25633", "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crop: %v\n%s", err, b)
	}

	outTlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open crop output: %v", err)
	}
	defer outTlr.Close()
	orTlr, err := opentile.OpenFile(oracle)
	if err != nil {
		t.Fatalf("open oracle: %v", err)
	}
	defer orTlr.Close()

	if outTlr.Format() != opentile.FormatSVS {
		t.Errorf("output format = %v, want svs", outTlr.Format())
	}
	outL0 := outTlr.Levels()[0]
	if outL0.Size.W != 27836 || outL0.Size.H != 25633 {
		t.Errorf("output L0 = %dx%d, want 27836x25633", outL0.Size.W, outL0.Size.H)
	}
	if md := outTlr.Metadata(); md.MPP.X == 0 || md.Magnification == 0 {
		t.Errorf("output lost MPP/Magnification: MPP=%v Mag=%v", md.MPP, md.Magnification)
	}

	var haveThumb, haveLabel, haveOverview bool
	var thumbW, thumbH int
	for _, a := range outTlr.AssociatedImages() {
		switch a.Type() {
		case opentile.AssociatedThumbnail:
			haveThumb = true
			thumbW, thumbH = a.Size().W, a.Size().H
		case opentile.AssociatedLabel:
			haveLabel = true
		case opentile.AssociatedOverview:
			haveOverview = true
		}
	}
	if !haveThumb || !haveLabel || !haveOverview {
		t.Errorf("associated present: thumb=%v label=%v overview=%v", haveThumb, haveLabel, haveOverview)
	}
	if haveThumb {
		cropAspect := 27836.0 / 25633.0
		thumbAspect := float64(thumbW) / float64(thumbH)
		if d := thumbAspect/cropAspect - 1; d < -0.02 || d > 0.02 {
			t.Errorf("thumbnail aspect %0.4f != crop aspect %0.4f (within 2%%)", thumbAspect, cropAspect)
		}
	}

	regions := []opentile.Region{
		{Origin: opentile.Point{X: 256, Y: 256}, Size: opentile.Size{W: 256, H: 256}},
		{Origin: opentile.Point{X: 1024, Y: 1024}, Size: opentile.Size{W: 256, H: 256}},
		{Origin: opentile.Point{X: 4096, Y: 2048}, Size: opentile.Size{W: 512, H: 256}},
	}
	for _, r := range regions {
		oLv, err := outTlr.Pyramid(0).Level(0)
		if err != nil {
			t.Fatalf("out level0: %v", err)
		}
		aLv, err := orTlr.Pyramid(0).Level(0)
		if err != nil {
			t.Fatalf("oracle level0: %v", err)
		}
		op, err := oLv.ReadRegion(r, opentile.WithFormat(decoder.PixelFormatRGB))
		if err != nil {
			t.Fatalf("out ReadRegion %v: %v", r, err)
		}
		ap, err := aLv.ReadRegion(r, opentile.WithFormat(decoder.PixelFormatRGB))
		if err != nil {
			t.Fatalf("oracle ReadRegion %v: %v", r, err)
		}
		mean, mx, outlierFrac := diffStats(op.Pix, ap.Pix)
		t.Logf("region %v: mean=%.3f max=%d outliers(>16)=%.4f%%", r, mean, mx, outlierFrac*100)
		// Parity is asserted on the *meaningful* signals, not a brittle absolute
		// max: (1) a tiny mean proves the crop is correctly aligned and
		// pixel-faithful — a misalignment/seam bug would inflate the mean; and
		// (2) only a sparse fraction of bytes may differ strongly, which is the
		// signature of JPEG ringing at high-contrast edges diverging between
		// libjpeg (ours) and Aperio's encoder (one re-encode generation). A
		// structured defect would push many correlated pixels over the
		// threshold. Measured on CMU-2: mean ≤ ~0.95, outliers(>16) ≤ ~0.006%.
		if mean > 1.5 {
			t.Errorf("region %v mean abs diff %.3f > 1.5 — crop not pixel-faithful (alignment?)", r, mean)
		}
		if outlierFrac > 0.001 { // >0.1% of bytes diverging strongly ⇒ structured defect, not ringing
			t.Errorf("region %v has %.4f%% bytes with abs diff >16 (>0.1%% ⇒ not edge ringing)", r, outlierFrac*100)
		}
	}
}

// diffStats returns the mean per-byte absolute difference, the maximum, and the
// fraction of bytes whose absolute difference exceeds 16 (the "strong outlier"
// fraction used to distinguish sparse JPEG edge-ringing from a structured defect).
func diffStats(a, b []byte) (mean float64, max int, outlierFrac float64) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum int64
	var over16 int
	for i := 0; i < n; i++ {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += int64(d)
		if d > max {
			max = d
		}
		if d > 16 {
			over16++
		}
	}
	if n > 0 {
		mean = float64(sum) / float64(n)
		outlierFrac = float64(over16) / float64(n)
	}
	return mean, max, outlierFrac
}

// TestCrop_SmallRegionTileAligned crops a tile-aligned rect of the small fixture
// and verifies the output opens with exact dims (no parity oracle; v1 re-encodes
// regardless of alignment).
func TestCrop_SmallRegionTileAligned(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "crop.svs")
	if b, err := exec.Command(bin, "crop", "--x", "256", "--y", "256", "--w", "512", "--h", "512", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("crop: %v\n%s", err, b)
	}
	tlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer tlr.Close()
	l0 := tlr.Levels()[0]
	if l0.Size.W != 512 || l0.Size.H != 512 {
		t.Errorf("L0 = %dx%d, want 512x512", l0.Size.W, l0.Size.H)
	}
}
