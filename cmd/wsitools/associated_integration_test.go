package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func testdir() string {
	if d := os.Getenv("WSI_TOOLS_TESTDIR"); d != "" {
		return d
	}
	return "../../sample_files"
}

func firstExisting(t *testing.T, rels ...string) string {
	t.Helper()
	for _, r := range rels {
		p := filepath.Join(testdir(), r)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func copyFile(t *testing.T, src string) string {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// level0Digest hashes all raw level-0 tiles via the source.Level interface
// (Grid / TileMaxSize / TileInto).
func level0Digest(t *testing.T, path string) string {
	t.Helper()
	src, err := source.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	lv := src.Levels()[0]
	grid := lv.Grid()
	h := sha256.New()
	buf := make([]byte, lv.TileMaxSize())
	for ty := 0; ty < grid.Y; ty++ {
		for tx := 0; tx < grid.X; tx++ {
			n, err := lv.TileInto(tx, ty, buf)
			if err != nil {
				t.Fatalf("tile (%d,%d): %v", tx, ty, err)
			}
			h.Write(buf[:n])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func assocOfType(t *testing.T, path, typ string) (image.Point, []byte, bool) {
	t.Helper()
	src, err := source.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, a := range src.Associated() {
		if a.Type() == typ {
			b, _ := a.Bytes()
			return a.Size(), b, true
		}
	}
	return image.Point{}, nil, false
}

func svsFixture(t *testing.T) string {
	p := firstExisting(t, "svs/CMU-1-Small-Region.svs", "svs/CMU-1.svs")
	if p == "" {
		t.Skip("no SVS fixture")
	}
	return p
}

func TestLabelRemovePyramidIdenticalAndPHIGone(t *testing.T) {
	in := copyFile(t, svsFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("fixture has no label")
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	digestBefore := level0Digest(t, in)

	if err := runAssociatedRemoveFor("label", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, _, ok := assocOfType(t, out, "label"); ok {
		t.Errorf("label still present after remove")
	}
	if digestAfter := level0Digest(t, out); digestAfter != digestBefore {
		t.Errorf("pyramid changed: before=%s after=%s", digestBefore, digestAfter)
	}
	outData, _ := os.ReadFile(out)
	if bytes.Contains(outData, []byte("\nlabel ")) {
		t.Errorf("label ImageDescription marker still present (PHI not erased)")
	}
	si, _ := os.Stat(in)
	so, _ := os.Stat(out)
	if so.Size() >= si.Size() {
		t.Errorf("output not smaller: in=%d out=%d", si.Size(), so.Size())
	}
}

func TestLabelReplacePreservesDimsAndPyramid(t *testing.T) {
	in := copyFile(t, svsFixture(t))
	origSize, origBytes, ok := assocOfType(t, in, "label")
	if !ok {
		t.Skip("fixture has no label")
	}
	// New solid-red PNG, deliberately a different size than the label.
	pngPath := filepath.Join(t.TempDir(), "new.png")
	img := image.NewRGBA(image.Rect(0, 0, origSize.X+50, origSize.Y+30))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.Set(x, y, color.RGBA{220, 20, 20, 255})
		}
	}
	pf, _ := os.Create(pngPath)
	_ = png.Encode(pf, img)
	pf.Close()

	out := filepath.Join(t.TempDir(), "out.svs")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedReplaceFor("label", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: pngPath, compression: "lzw", resize: "fit", bgHex: "F5F5E6",
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	newSize, newBytes, ok := assocOfType(t, out, "label")
	if !ok {
		t.Fatalf("label missing after replace")
	}
	if newSize != origSize {
		t.Errorf("label dims changed: orig=%v new=%v (replace should preserve existing dims)", origSize, newSize)
	}
	if bytes.Equal(origBytes, newBytes) {
		t.Errorf("label content unchanged after replace")
	}
	if digestAfter := level0Digest(t, out); digestAfter != digestBefore {
		t.Errorf("pyramid changed after replace")
	}
}

func TestUnsupportedFormatRejected(t *testing.T) {
	p := firstExisting(t, "ndpi/CMU-1.ndpi", "ome-tiff/Leica-1.ome.tiff")
	if p == "" {
		t.Skip("no unsupported-format fixture")
	}
	in := copyFile(t, p)
	out := filepath.Join(t.TempDir(), "out"+filepath.Ext(p))
	err := runAssociatedRemoveFor("label", in, out, removeFlags{assocCommonFlags{fsync: false}})
	if !errors.Is(err, ErrUnsupportedAssoc) {
		t.Fatalf("want ErrUnsupportedAssoc, got %v", err)
	}
}
