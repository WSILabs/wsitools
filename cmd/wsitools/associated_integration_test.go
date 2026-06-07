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

// --- Regression + coverage added after the replace-classification bug ---

// TestSVSOverviewRemoveKeepsLabel guards the exact bug class found in review:
// editing a non-label associated image must not disturb the label's
// classification. (remove is codec-agnostic and must be safe for every type.)
func TestSVSOverviewRemoveKeepsLabel(t *testing.T) {
	in := copyFile(t, svsFixture(t))
	if _, _, ok := assocOfType(t, in, "overview"); !ok {
		t.Skip("fixture has no overview")
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	if err := runAssociatedRemoveFor("overview", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("overview remove: %v", err)
	}
	if _, _, ok := assocOfType(t, out, "overview"); ok {
		t.Errorf("overview still present after remove")
	}
	if _, _, ok := assocOfType(t, out, "label"); !ok {
		t.Errorf("label vanished after overview remove (classification corrupted)")
	}
	if _, _, ok := assocOfType(t, out, "thumbnail"); !ok {
		t.Errorf("thumbnail vanished after overview remove")
	}
}

// TestSVSReplaceNonLabelGated: replacing a non-label image on SVS is not yet
// supported (opentile-go reads Aperio thumbnail/macro/overview as abbreviated
// JPEG). It must error clearly rather than emit an unreadable image.
func TestSVSReplaceNonLabelGated(t *testing.T) {
	in := copyFile(t, svsFixture(t))
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, 1280, 431, color.RGBA{10, 20, 30, 255})
	out := filepath.Join(t.TempDir(), "out.svs")
	err := runAssociatedReplaceFor("overview", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, force: true,
	})
	if !errors.Is(err, ErrUnsupportedAssoc) {
		t.Fatalf("want ErrUnsupportedAssoc for SVS overview replace, got %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Errorf("output written despite gate")
	}
}

// TestLabelRemoveInPlace exercises the atomic in-place path (outPath == input):
// the original is overwritten, the label is gone, the pyramid is unchanged, and
// no temp files are left behind.
func TestLabelRemoveInPlace(t *testing.T) {
	in := copyFile(t, svsFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("fixture has no label")
	}
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveFor("label", in, in, removeFlags{assocCommonFlags{inPlace: true, fsync: true}}); err != nil {
		t.Fatalf("in-place remove: %v", err)
	}
	if _, _, ok := assocOfType(t, in, "label"); ok {
		t.Errorf("label still present after in-place remove")
	}
	if level0Digest(t, in) != digestBefore {
		t.Errorf("pyramid changed after in-place remove")
	}
	// No leftover temp files in the directory.
	ents, _ := os.ReadDir(filepath.Dir(in))
	for _, e := range ents {
		if bytes.Contains([]byte(e.Name()), []byte(".tmp")) {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func genericFixture(t *testing.T, rels ...string) string {
	p := firstExisting(t, rels...)
	if p == "" {
		t.Skip("no generic-TIFF fixture present")
	}
	return p
}

// TestGenericTIFFLabelRemovePyramidIdentical: generic-TIFF remove keeps the
// pyramid byte-identical and drops the label.
func TestGenericTIFFLabelRemovePyramidIdentical(t *testing.T) {
	in := copyFile(t, genericFixture(t, "generic-tiff/synth-pyramid-with-label.tiff", "generic-tiff/CMU-1.stripped.tiff"))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("fixture has no label")
	}
	out := filepath.Join(t.TempDir(), "out.tiff")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveFor("label", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("generic-tiff label remove: %v", err)
	}
	if _, _, ok := assocOfType(t, out, "label"); ok {
		t.Errorf("label still present after remove")
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("generic-tiff pyramid changed after remove")
	}
}

// TestGenericTIFFReplaceJPEGRoundTrips closes the JPEG-in-TIFF round-trip gap:
// a JPEG-encoded replacement on generic-TIFF must read back as the right type
// AND decode to a valid image (WSIImageType makes the type authoritative; the
// generic reader decodes standard JPEG directly, unlike the Aperio path).
func TestGenericTIFFReplaceJPEGRoundTrips(t *testing.T) {
	in := copyFile(t, genericFixture(t, "generic-tiff/CMU-1.stripped.tiff"))
	ovSize, _, ok := assocOfType(t, in, "overview")
	if !ok {
		t.Skip("fixture has no overview")
	}
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, ovSize.X, ovSize.Y, color.RGBA{200, 30, 40, 255})
	out := filepath.Join(t.TempDir(), "out.tiff")
	if err := runAssociatedReplaceFor("overview", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, compression: "jpeg", bgHex: "F5F5E6", force: true,
	}); err != nil {
		t.Fatalf("generic-tiff overview replace: %v", err)
	}
	// Reads back as overview and the stored JPEG decodes.
	src, err := source.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var decoded bool
	for _, a := range src.Associated() {
		if a.Type() == "overview" {
			b, err := a.Bytes()
			if err != nil {
				t.Fatalf("overview Bytes: %v", err)
			}
			if _, _, err := image.Decode(bytes.NewReader(b)); err != nil {
				t.Fatalf("replaced overview does not decode: %v", err)
			}
			decoded = true
		}
	}
	if !decoded {
		t.Errorf("replaced overview not classified as overview on read-back")
	}
}

// writeSolidPNG writes a w×h solid-color PNG to path.
func writeSolidPNG(t *testing.T, path string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func cogwsiFixture(t *testing.T) string {
	p := firstExisting(t, "cog-wsi/CMU-1_cog-wsi.tiff", "cog-wsi/CMU-1-Small-Region_cog-wsi.tiff")
	if p == "" {
		t.Skip("no cog-wsi fixture")
	}
	return p
}

func TestCOGWSILabelRemove(t *testing.T) {
	in := copyFile(t, cogwsiFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("fixture has no label")
	}
	out := filepath.Join(t.TempDir(), "out.tiff")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveForCOGWSI("label", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("cog-wsi label remove: %v", err)
	}
	// Output reopens as conformant cog-wsi.
	osrc, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := osrc.Format(); got != "cog-wsi" {
		t.Errorf("output format = %q, want cog-wsi", got)
	}
	osrc.Close()
	// Label gone; other associated images survive.
	if _, _, ok := assocOfType(t, out, "label"); ok {
		t.Errorf("label still present")
	}
	if _, _, ok := assocOfType(t, out, "overview"); !ok {
		t.Errorf("overview vanished (contract: only target changes)")
	}
	// Pyramid pixels identical.
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed")
	}
}

func TestCOGWSIRemoveInPlacePreservesMeta(t *testing.T) {
	in := copyFile(t, cogwsiFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("no label")
	}
	mdBefore := func() (float64, float64) {
		s, _ := source.Open(in)
		defer s.Close()
		m := s.Metadata()
		return m.MPPX, m.Magnification
	}
	mppBefore, magBefore := mdBefore()
	if err := runAssociatedRemoveForCOGWSI("label", in, in, removeFlags{assocCommonFlags{inPlace: true, fsync: true}}); err != nil {
		t.Fatalf("in-place remove: %v", err)
	}
	if _, _, ok := assocOfType(t, in, "label"); ok {
		t.Errorf("label still present after in-place remove")
	}
	s, _ := source.Open(in)
	defer s.Close()
	m := s.Metadata()
	if m.MPPX != mppBefore || m.Magnification != magBefore {
		t.Errorf("metadata changed: MPPX %v->%v, Mag %v->%v", mppBefore, m.MPPX, magBefore, m.Magnification)
	}
	// No leftover temp files.
	ents, _ := os.ReadDir(filepath.Dir(in))
	for _, e := range ents {
		if bytes.Contains([]byte(e.Name()), []byte(".tmp")) {
			t.Errorf("leftover temp: %s", e.Name())
		}
	}
}

func TestCOGWSIRemoveAbsentErrors(t *testing.T) {
	in := copyFile(t, cogwsiFixture(t))
	out := filepath.Join(t.TempDir(), "o.tiff")
	err := runAssociatedRemoveForCOGWSI("no-such-type", in, out, removeFlags{assocCommonFlags{fsync: false}})
	if err == nil {
		t.Fatal("expected error for absent type")
	}
}

func TestCOGWSIOverviewReplaceRoundTrips(t *testing.T) {
	in := copyFile(t, cogwsiFixture(t))
	ovSize, _, ok := assocOfType(t, in, "overview")
	if !ok {
		t.Skip("fixture has no overview")
	}
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, ovSize.X, ovSize.Y, color.RGBA{200, 30, 40, 255})
	out := filepath.Join(t.TempDir(), "out.tiff")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedReplaceForCOGWSI("overview", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, compression: "jpeg", bgHex: "F5F5E6", force: true,
	}); err != nil {
		t.Fatalf("cog-wsi overview replace: %v", err)
	}
	src, err := source.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var decoded bool
	for _, a := range src.Associated() {
		if a.Type() == "overview" {
			b, err := a.Bytes()
			if err != nil {
				t.Fatalf("overview bytes: %v", err)
			}
			if _, _, err := image.Decode(bytes.NewReader(b)); err != nil {
				t.Fatalf("replaced overview does not decode: %v", err)
			}
			decoded = true
		}
	}
	if !decoded {
		t.Errorf("overview missing/not classified after replace")
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed after replace")
	}
}
