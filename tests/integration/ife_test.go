//go:build integration

package integration

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestConvertToIFE_VerbatimByteIdentical generates a 256px-JPEG pyramid TIFF (the
// IFE fixture pool has no native 256px-JPEG WSI; CMU-1 is 240px-tiled and the
// no-transform TIFF path preserves source geometry, so we force the 256px retile
// engine with --factor), converts it to IFE — which should take the verbatim
// tile-copy fast path — and asserts a sample of pyramid tile bytes are
// byte-identical between the TIFF and the IFE.
func TestConvertToIFE_VerbatimByteIdentical(t *testing.T) {
	td := testdir(t)
	srcSVS := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(srcSVS); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	dir := t.TempDir()

	// 1. make a 256px-JPEG pyramid TIFF. --factor forces the retile engine (which
	// emits 256px tiles, the default output tile size); the no-transform path
	// would instead preserve the source's 240px tiling.
	tiff256 := filepath.Join(dir, "src256.tiff")
	if b, err := runCLI(bin, "convert", "--to", "tiff", "--codec", "jpeg", "--factor", "2", "-o", tiff256, srcSVS); err != nil {
		t.Fatalf("make tiff: %v\n%s", err, b)
	}

	// 2. convert that to IFE (should take the verbatim path).
	out := filepath.Join(dir, "out.iris")
	b, err := runCLI(bin, "convert", "--to", "ife", "-o", out, tiff256)
	if err != nil {
		t.Fatalf("convert --to ife: %v\n%s", err, b)
	}
	// Sanity: confirm the verbatim path was taken (not a silent fall-through to
	// the engine, which would re-encode and break byte-identity).
	if !strings.Contains(string(b), "verbatim tile-copy") {
		t.Fatalf("expected verbatim tile-copy path, got:\n%s", b)
	}

	// 3. open both via opentile; assert pyramid tile bytes byte-identical for a
	// sample of (level,col,row). opentile *Level.Tile(tx,ty) returns the raw
	// compressed tile bytes (it backs source.Level.TileInto).
	srcSlide, err := opentile.OpenFile(tiff256)
	if err != nil {
		t.Fatal(err)
	}
	defer srcSlide.Close()
	ifeSlide, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatal(err)
	}
	defer ifeSlide.Close()

	srcLevels := srcSlide.Levels()
	ifeLevels := ifeSlide.Levels()
	if len(ifeLevels) != len(srcLevels) {
		t.Fatalf("level count: ife=%d src=%d", len(ifeLevels), len(srcLevels))
	}

	// Compare L0 (0,0) plus one mid-pyramid tile.
	type coord struct{ level, col, row int }
	coords := []coord{{0, 0, 0}}
	if len(srcLevels) > 1 {
		mid := len(srcLevels) / 2
		coords = append(coords, coord{mid, 0, 0})
	}
	for _, c := range coords {
		sb, err := srcLevels[c.level].Tile(c.col, c.row)
		if err != nil {
			t.Fatalf("src tile L%d %d,%d: %v", c.level, c.col, c.row, err)
		}
		ib, err := ifeLevels[c.level].Tile(c.col, c.row)
		if err != nil {
			t.Fatalf("ife tile L%d %d,%d: %v", c.level, c.col, c.row, err)
		}
		if !bytes.Equal(sb, ib) {
			t.Errorf("tile L%d %d,%d NOT byte-identical: src=%d bytes ife=%d bytes",
				c.level, c.col, c.row, len(sb), len(ib))
		}
	}
}

// TestConvertToIFE_PNGLabelDecodesBack guards opentile-go#74 (fixed in v0.49.0).
// CMU-1's LZW label is re-encoded to lossless PNG (encoding=1) in the IFE; before
// v0.49.0 opentile-go mapped PNG associated images to CompressionUnknown and could
// not decode them, so `extract --type label` failed on our own output. v0.49.0
// adds PNG associated decode, so it must now succeed.
func TestConvertToIFE_PNGLabelDecodesBack(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.iris")
	if b, err := runCLI(bin, "convert", "--to", "ife", "-o", out, src); err != nil {
		t.Fatalf("convert --to ife: %v\n%s", err, b)
	}
	lbl := filepath.Join(dir, "label.png")
	if b, err := runCLI(bin, "extract", "--type", "label", "-o", lbl, out); err != nil {
		t.Fatalf("extract PNG label (opentile-go#74): %v\n%s", err, b)
	}
	if fi, err := os.Stat(lbl); err != nil || fi.Size() == 0 {
		t.Fatalf("extracted label missing/empty: %v", err)
	}
}

// TestConvertToIFE_RectCrop: convert --to ife --rect crops the source region
// (the IFE writer composes crop via the retile engine's SrcRegion).
func TestConvertToIFE_RectCrop(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "rect.iris")
	if b, err := runCLI(bin, "convert", "--to", "ife", "--rect", "0,0,1024,1024", "-o", out, src); err != nil {
		t.Fatalf("convert --to ife --rect: %v\n%s", err, b)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer sl.Close()
	if string(sl.Format()) != "ife" {
		t.Errorf("format = %q, want ife", sl.Format())
	}
	if w, h := sl.Levels()[0].Size.W, sl.Levels()[0].Size.H; w != 1024 || h != 1024 {
		t.Errorf("cropped L0 = %dx%d, want 1024x1024", w, h)
	}
}

// TestConvertToIFE_FactorScalesMetadata guards that --factor scales MPP (×factor)
// and magnification (÷factor) — a regression: the IFE --factor path previously
// wrote the source MPP/mag unchanged on a downsampled pyramid.
func TestConvertToIFE_FactorScalesMetadata(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	srcSlide, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	srcMPP := srcSlide.Metadata().MPP.X
	srcMag := srcSlide.Metadata().Magnification
	srcSlide.Close()

	out := filepath.Join(t.TempDir(), "f2.iris")
	if b, err := runCLI(bin, "convert", "--to", "ife", "--factor", "2", "-o", out, src); err != nil {
		t.Fatalf("convert --to ife --factor 2: %v\n%s", err, b)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer sl.Close()
	md := sl.Metadata()
	if want := srcMPP * 2; math.Abs(md.MPP.X-want) > 0.001 { // IFE stores MPP as f32
		t.Errorf("MPP = %v, want ~%v (src %v ×2)", md.MPP.X, want, srcMPP)
	}
	if want := srcMag / 2; md.Magnification != want {
		t.Errorf("Magnification = %v, want %v (src %v ÷2)", md.Magnification, want, srcMag)
	}
}
