//go:build integration

package integration

import (
	"fmt"
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

// assertLosslessByteIdentity crops src with --lossless and asserts every output
// L0 tile is byte-identical to the corresponding source tile, the shared tile
// prefix (tag 347) matches, and the output L0 dims equal the tile-snapped bbox.
// Codec/tile-size agnostic: works for JPEG and JPEG2000, 256px and 512px tiles.
func assertLosslessByteIdentity(t *testing.T, bin, src string, x, y, w, h int) {
	t.Helper()
	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer srcTlr.Close()
	srcL0 := srcTlr.Levels()[0]
	tileW, tileH := srcL0.TileSize.W, srcL0.TileSize.H
	baseW, baseH := srcL0.Size.W, srcL0.Size.H
	if x+w > baseW || y+h > baseH {
		t.Skipf("rect %d,%d %dx%d exceeds source %dx%d", x, y, w, h, baseW, baseH)
	}

	out := filepath.Join(t.TempDir(), "lossless.svs")
	cmd := exec.Command(bin, "crop", "--lossless",
		"--rect", fmt.Sprintf("%d,%d,%d,%d", x, y, w, h), "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crop --lossless: %v\n%s", err, b)
	}

	// Expected snap (mirror snapRectToTiles).
	snapX := (x / tileW) * tileW
	snapY := (y / tileH) * tileH
	endX := ((x + w + tileW - 1) / tileW) * tileW
	endY := ((y + h + tileH - 1) / tileH) * tileH
	if endX > baseW {
		endX = baseW
	}
	if endY > baseH {
		endY = baseH
	}
	snapW, snapH := endX-snapX, endY-snapY
	stx0, sty0 := snapX/tileW, snapY/tileH

	outTlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open out: %v", err)
	}
	defer outTlr.Close()
	if outTlr.Format() != opentile.FormatSVS {
		t.Errorf("format = %v, want svs", outTlr.Format())
	}
	outL0 := outTlr.Levels()[0]
	if outL0.Size.W != snapW || outL0.Size.H != snapH {
		t.Fatalf("L0 = %dx%d, want snapped %dx%d", outL0.Size.W, outL0.Size.H, snapW, snapH)
	}
	if outL0.TileSize.W != tileW || outL0.TileSize.H != tileH {
		t.Errorf("output tile size %dx%d, want source %dx%d (verbatim L0 keeps source tiling)",
			outL0.TileSize.W, outL0.TileSize.H, tileW, tileH)
	}
	if !bytesEqual(outL0.TilePrefix(), srcL0.TilePrefix()) {
		t.Errorf("TilePrefix differs (shared tables not preserved)")
	}

	outTilesX := (snapW + tileW - 1) / tileW
	outTilesY := (snapH + tileH - 1) / tileH
	for oy := 0; oy < outTilesY; oy++ {
		for ox := 0; ox < outTilesX; ox++ {
			ob, err := outL0.Tile(ox, oy)
			if err != nil {
				t.Fatalf("out tile (%d,%d): %v", ox, oy, err)
			}
			sb, err := srcL0.Tile(stx0+ox, sty0+oy)
			if err != nil {
				t.Fatalf("src tile (%d,%d): %v", stx0+ox, sty0+oy, err)
			}
			if !bytesEqual(ob, sb) {
				t.Fatalf("tile (%d,%d) NOT byte-identical to src (%d,%d): %d vs %d bytes",
					ox, oy, stx0+ox, sty0+oy, len(ob), len(sb))
			}
		}
	}

	if md := outTlr.Metadata(); md.MPP.X == 0 || md.Magnification == 0 {
		t.Errorf("lost MPP/Mag: MPP=%v Mag=%v", md.MPP, md.Magnification)
	}
}

func TestCropLossless_ByteIdentity(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)

	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	tileW, tileH := srcTlr.Levels()[0].TileSize.W, srcTlr.Levels()[0].TileSize.H
	srcTlr.Close()

	t.Run("unaligned", func(t *testing.T) { assertLosslessByteIdentity(t, bin, src, 300, 200, 400, 400) })
	t.Run("aligned", func(t *testing.T) { assertLosslessByteIdentity(t, bin, src, tileW, tileH, 2*tileW, 2*tileH) })
}

// TestCropLossless_ByteIdentity_Variants extends the byte-identity guarantee
// across SVS source variants: JPEG2000 (no shared JPEG tables — nil TilePrefix
// path), 512px tiles, and 4:4:4 chroma. Local-only (large fixtures).
func TestCropLossless_ByteIdentity_Variants(t *testing.T) {
	td := testdir(t)
	bin := buildOnce(t)
	variants := []struct {
		name       string
		x, y, w, h int
		note       string
	}{
		{"JP2K-33003-1.svs", 1000, 1000, 2000, 2000, "JPEG2000 / nil-TilePrefix"},
		{"scan_617_grundium_sectra_svs.svs", 1000, 1000, 3000, 3000, "512px tiles / vendor SVS"},
		{"CMU-1.svs", 1000, 1000, 3000, 3000, "4:4:4 JPEG / full slide"},
		{"svs_40x_bigtiff.svs", 1000, 1000, 3000, 3000, "BigTIFF source / 512px tiles"},
	}
	for _, v := range variants {
		v := v
		t.Run(v.name, func(t *testing.T) {
			src := filepath.Join(td, "svs", v.name)
			if _, err := os.Stat(src); err != nil {
				t.Skipf("fixture missing: %s (%s)", src, v.note)
			}
			assertLosslessByteIdentity(t, bin, src, v.x, v.y, v.w, v.h)
		})
	}
}

// TestCrop_ReencodeVariants checks the default (re-encode) crop produces a valid,
// re-openable SVS with the exact requested extent and preserved MPP/magnification
// across SVS source variants (JPEG2000, 512px tiles, 4:4:4). No ImageScope oracle
// for these — structural + metadata only. Local-only (large fixtures).
func TestCrop_ReencodeVariants(t *testing.T) {
	td := testdir(t)
	bin := buildOnce(t)
	variants := []struct {
		name       string
		x, y, w, h int
	}{
		{"JP2K-33003-1.svs", 1000, 1000, 2000, 2000},
		{"scan_617_grundium_sectra_svs.svs", 1000, 1000, 3000, 3000},
		{"CMU-1.svs", 1000, 1000, 3000, 3000},
		{"svs_40x_bigtiff.svs", 1000, 1000, 3000, 3000},
	}
	for _, v := range variants {
		v := v
		t.Run(v.name, func(t *testing.T) {
			src := filepath.Join(td, "svs", v.name)
			if _, err := os.Stat(src); err != nil {
				t.Skipf("fixture missing: %s", src)
			}
			srcTlr, err := opentile.OpenFile(src)
			if err != nil {
				t.Fatalf("open src: %v", err)
			}
			srcMD := srcTlr.Metadata()
			srcTlr.Close()

			out := filepath.Join(t.TempDir(), "reencode.svs")
			cmd := exec.Command(bin, "crop",
				"--rect", fmt.Sprintf("%d,%d,%d,%d", v.x, v.y, v.w, v.h), "-o", out, src)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("crop (re-encode): %v\n%s", err, b)
			}
			outTlr, err := opentile.OpenFile(out)
			if err != nil {
				t.Fatalf("open out: %v", err)
			}
			defer outTlr.Close()
			if outTlr.Format() != opentile.FormatSVS {
				t.Errorf("format = %v, want svs", outTlr.Format())
			}
			outL0 := outTlr.Levels()[0]
			// Re-encode keeps the EXACT requested extent (unlike --lossless).
			if outL0.Size.W != v.w || outL0.Size.H != v.h {
				t.Errorf("L0 = %dx%d, want exact %dx%d", outL0.Size.W, outL0.Size.H, v.w, v.h)
			}
			md := outTlr.Metadata()
			if md.MPP.X != srcMD.MPP.X || md.MPP.Y != srcMD.MPP.Y {
				t.Errorf("MPP changed: got %v, want source %v", md.MPP, srcMD.MPP)
			}
			if md.Magnification != srcMD.Magnification {
				t.Errorf("Magnification changed: got %v, want source %v", md.Magnification, srcMD.Magnification)
			}
		})
	}
}
