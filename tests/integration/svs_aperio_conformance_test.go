//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// jpegSOFDims parses the image dimensions from a JPEG's first SOF marker.
func jpegSOFDims(b []byte) (w, h int, ok bool) {
	i := 2 // skip SOI
	for i+9 < len(b) {
		if b[i] != 0xFF {
			i++
			continue
		}
		m := b[i+1]
		if (m >= 0xC0 && m <= 0xC3) || (m >= 0xC5 && m <= 0xC7) || (m >= 0xC9 && m <= 0xCB) || (m >= 0xCD && m <= 0xCF) {
			h = int(b[i+5])<<8 | int(b[i+6])
			w = int(b[i+7])<<8 | int(b[i+8])
			return w, h, true
		}
		if m == 0xD8 || m == 0xD9 || (m >= 0xD0 && m <= 0xD7) {
			i += 2
			continue
		}
		seg := int(b[i+2])<<8 | int(b[i+3])
		i += 2 + seg
	}
	return 0, 0, false
}

// TestSVSEdgeTilesAreFullSize guards the TIFF-conformance fix: the retile engine
// must pad partial edge/corner tiles up to the full declared TileWidth×TileLength
// (OpenSlide/ImageScope reject a sub-full-size JPEG tile as a "dimensional
// mismatch"). --factor routes through the engine, and a 2220×2967 / 240px source
// yields partial right/bottom edge tiles, so every L0 edge tile's JPEG must
// decode to exactly 240×240.
func TestSVSEdgeTilesAreFullSize(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.svs")

	if o, err := runCLI(bin, "convert", "--to", "svs", "--codec", "jpeg", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --factor 2 --to svs: %v\n%s", err, o)
	}

	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer sl.Close()
	l0 := sl.Levels()[0]
	tw, th := l0.TileSize.W, l0.TileSize.H
	cols := (l0.Size.W + tw - 1) / tw
	rows := (l0.Size.H + th - 1) / th
	if cols < 2 || rows < 2 {
		t.Fatalf("need partial edges to test: grid %dx%d", cols, rows)
	}
	// Interior, right edge, bottom edge, corner — all must be full tile size.
	for _, c := range [][2]int{{0, 0}, {cols - 1, 0}, {0, rows - 1}, {cols - 1, rows - 1}} {
		b, err := l0.Tile(c[0], c[1])
		if err != nil {
			t.Fatalf("read tile (%d,%d): %v", c[0], c[1], err)
		}
		w, h, ok := jpegSOFDims(b)
		if !ok {
			t.Fatalf("tile (%d,%d): no JPEG SOF", c[0], c[1])
		}
		if w != tw || h != th {
			t.Errorf("tile (%d,%d) JPEG = %dx%d, want full tile %dx%d (edge tiles must be padded)", c[0], c[1], w, h, tw, th)
		}
	}
}

// TestDICOMEdgeFramesAreFullSize guards the same padding fix in the DICOM engine
// path: DICOM TILED_FULL requires every frame to be exactly Rows×Columns, but the
// retile engine hands partial edge frames (and sub-frame levels) at content size.
// `--to dicom --factor` routes through the engine; converting a 2220×2967 source
// yields L0 edge frames AND a deepest level smaller than one frame, so the edge
// frame and the deepest-level frame JPEGs must both decode to the full frame size.
func TestDICOMEdgeFramesAreFullSize(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "dcmout")

	if o, err := runCLI(bin, "convert", "--to", "dicom", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to dicom --factor 2: %v\n%s", err, o)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open dicom output: %v", err)
	}
	defer sl.Close()

	levels := sl.Levels()
	l0 := levels[0]
	fw, fh := l0.TileSize.W, l0.TileSize.H // DICOM Columns×Rows
	cols := (l0.Size.W + fw - 1) / fw
	rows := (l0.Size.H + fh - 1) / fh
	type tc struct {
		lvl, col, row int
		label         string
	}
	cases := []tc{{0, 0, 0, "L0 interior"}}
	if cols > 1 {
		cases = append(cases, tc{0, cols - 1, 0, "L0 right edge"})
	}
	if rows > 1 {
		cases = append(cases, tc{0, 0, rows - 1, "L0 bottom edge"})
	}
	// Deepest level (smaller than one frame).
	cases = append(cases, tc{len(levels) - 1, 0, 0, "deepest level"})

	for _, c := range cases {
		lv := levels[c.lvl]
		b, err := lv.Tile(c.col, c.row)
		if err != nil {
			t.Fatalf("%s: read frame L%d(%d,%d): %v", c.label, c.lvl, c.col, c.row, err)
		}
		w, h, ok := jpegSOFDims(b)
		if !ok {
			t.Fatalf("%s: frame has no JPEG SOF", c.label)
		}
		if w != lv.TileSize.W || h != lv.TileSize.H {
			t.Errorf("%s: frame JPEG = %dx%d, want full frame %dx%d (DICOM frames must be uniform Rows×Columns)",
				c.label, w, h, lv.TileSize.W, lv.TileSize.H)
		}
	}
}

// TestSVSSynthesizesThumbnailAtIFD1 guards the Aperio-layout fix: a source
// without a thumbnail, converted to SVS, must get a synthesized thumbnail at
// IFD 1. Genuine Aperio SVS always carries the thumbnail as the second IFD;
// ImageScope classifies IFD 1 positionally as the thumbnail, so without one the
// first reduced pyramid level lands at IFD 1 and is dropped. Built from a
// multi-level fixture stripped of associated images (--no-associated), so no
// dedicated no-thumbnail fixture is needed.
func TestSVSSynthesizesThumbnailAtIFD1(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	dir := t.TempDir()

	// 1. Multi-level source → tiff with NO associated images (no thumbnail).
	noThumb := filepath.Join(dir, "nothumb.tiff")
	if o, err := runCLI(bin, "convert", "--to", "tiff", "--no-associated", "-f", "-o", noThumb, src); err != nil {
		t.Fatalf("make no-thumbnail tiff: %v\n%s", err, o)
	}
	if ifds, err := runCLI(bin, "dump-ifds", noThumb); err != nil {
		t.Fatalf("dump-ifds intermediate: %v\n%s", err, ifds)
	} else if strings.Contains(ifds, "thumbnail") {
		t.Fatalf("intermediate tiff unexpectedly has a thumbnail:\n%s", ifds)
	}

	// 2. Convert the no-thumbnail source to SVS → must synthesize IFD 1 thumbnail.
	out := filepath.Join(dir, "out.svs")
	if o, err := runCLI(bin, "convert", "--to", "svs", "-f", "-o", out, noThumb); err != nil {
		t.Fatalf("convert no-thumbnail tiff → svs: %v\n%s", err, o)
	}

	ifds, err := runCLI(bin, "dump-ifds", out)
	if err != nil {
		t.Fatalf("dump-ifds output: %v\n%s", err, ifds)
	}
	// Keep only the per-IFD layout lines ("IFD 0  pyramid L0  ..."), excluding the
	// verbose detail lines ("IFD 0: WSIImageType=...", which carry a colon).
	lines := strings.Split(strings.TrimSpace(ifds), "\n")
	var ifdLines []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "IFD ") && !strings.Contains(t, ":") {
			ifdLines = append(ifdLines, l)
		}
	}
	if len(ifdLines) < 2 {
		t.Fatalf("expected ≥2 IFDs, got:\n%s", ifds)
	}
	// IFD 1 must be the thumbnail.
	if !strings.Contains(ifdLines[1], "thumbnail") {
		t.Errorf("IFD 1 is not a thumbnail (Aperio requires it):\n%s", strings.Join(ifdLines, "\n"))
	}
	// All 3 source pyramid levels must survive (none consumed as the thumbnail).
	pyr := 0
	for _, l := range ifdLines {
		if strings.Contains(l, "pyramid") {
			pyr++
		}
	}
	if pyr != 3 {
		t.Errorf("pyramid level count = %d, want 3 (no level eaten):\n%s", pyr, strings.Join(ifdLines, "\n"))
	}

	// opentile must also still read all 3 levels and see the thumbnail associated.
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer sl.Close()
	if n := len(sl.Levels()); n != 3 {
		t.Errorf("opentile level count = %d, want 3", n)
	}
	hasThumb := false
	for _, a := range sl.AssociatedImages() {
		if string(a.Type()) == "thumbnail" {
			hasThumb = true
		}
	}
	if !hasThumb {
		t.Error("output has no thumbnail associated image")
	}
}
