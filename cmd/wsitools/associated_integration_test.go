package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// TestSVSOverviewReplaceWorks: the overview trails the tiled pyramid, so the
// splice tail-rewrite replaces it while leaving the pyramid byte-identical. The
// overview content must change, the pyramid must be untouched, and the label/
// thumbnail classification must survive.
func TestSVSOverviewReplaceWorks(t *testing.T) {
	in := copyFile(t, svsFixture(t))
	origSize, origBytes, ok := assocOfType(t, in, "overview")
	if !ok {
		t.Skip("fixture has no overview")
	}
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, origSize.X, origSize.Y, color.RGBA{10, 20, 30, 255})
	out := filepath.Join(t.TempDir(), "out.svs")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedReplaceFor("overview", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, resize: "fit", bgHex: "F5F5E6", force: true,
	}); err != nil {
		t.Fatalf("overview replace: %v", err)
	}
	_, newBytes, ok := assocOfType(t, out, "overview")
	if !ok {
		t.Fatalf("overview missing after replace")
	}
	if bytes.Equal(origBytes, newBytes) {
		t.Errorf("overview content unchanged after replace")
	}
	if digestAfter := level0Digest(t, out); digestAfter != digestBefore {
		t.Errorf("pyramid changed after overview replace")
	}
	if _, _, ok := assocOfType(t, out, "label"); !ok {
		t.Errorf("label vanished after overview replace (classification corrupted)")
	}
}

// TestSVSThumbnailReplaceSingleLevel: when no tiled pyramid level follows the
// thumbnail (a single-level slide), replacing it works via the tail-rewrite —
// pyramid intact, thumbnail content changed. If the fixture is multi-level the
// replace is correctly refused (covered by TestSVSThumbnailReplaceMultiLevelGated),
// so this test skips in that case rather than asserting the wrong outcome.
func TestSVSThumbnailReplaceSingleLevel(t *testing.T) {
	in := copyFile(t, svsFixture(t))
	origSize, origBytes, ok := assocOfType(t, in, "thumbnail")
	if !ok {
		t.Skip("fixture has no thumbnail")
	}
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, origSize.X, origSize.Y, color.RGBA{10, 20, 30, 255})
	out := filepath.Join(t.TempDir(), "out.svs")
	digestBefore := level0Digest(t, in)
	err := runAssociatedReplaceFor("thumbnail", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, resize: "fit", bgHex: "F5F5E6", force: true,
	})
	if errors.Is(err, ErrUnsupportedAssoc) {
		t.Skip("fixture is multi-level (tiled levels follow the thumbnail); see TestSVSThumbnailReplaceMultiLevelGated")
	}
	if err != nil {
		t.Fatalf("thumbnail replace: %v", err)
	}
	_, newBytes, ok := assocOfType(t, out, "thumbnail")
	if !ok {
		t.Fatalf("thumbnail missing after replace")
	}
	if bytes.Equal(origBytes, newBytes) {
		t.Errorf("thumbnail content unchanged after replace")
	}
	if digestAfter := level0Digest(t, out); digestAfter != digestBefore {
		t.Errorf("pyramid changed after thumbnail replace")
	}
}

// TestSVSThumbnailReplaceMultiLevelGated: on a multi-level slide where the
// in-place splice can't relocate the thumbnail (IFD 1 precedes tiled pyramid
// levels whose L0 data is interleaved with the thumbnail offsets), rebuildSVS
// takes over and produces a correct output. Needs a multi-level SVS fixture
// (CMU-1.svs) that triggers ErrUnexpectedLayout from the splice engine.
func TestSVSThumbnailReplaceMultiLevelGated(t *testing.T) {
	p := firstExisting(t, "svs/CMU-1.svs")
	if p == "" {
		t.Skip("no multi-level SVS fixture (CMU-1.svs)")
	}
	in := copyFile(t, p)
	origSize, origBytes, ok := assocOfType(t, in, "thumbnail")
	if !ok {
		t.Skip("fixture has no thumbnail")
	}
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, origSize.X, origSize.Y, color.RGBA{10, 20, 30, 255})
	out := filepath.Join(t.TempDir(), "out.svs")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedReplaceFor("thumbnail", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, resize: "fit", bgHex: "F5F5E6", force: true,
	}); err != nil {
		t.Fatalf("thumbnail replace via rebuild: %v", err)
	}
	_, newBytes, ok := assocOfType(t, out, "thumbnail")
	if !ok {
		t.Fatalf("thumbnail missing/unclassified after multi-level replace")
	}
	if bytes.Equal(origBytes, newBytes) {
		t.Errorf("thumbnail content unchanged after replace")
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed after thumbnail replace")
	}
	for _, ty := range []string{"label", "overview"} {
		if _, _, ok := assocOfType(t, out, ty); !ok {
			t.Errorf("%s vanished after thumbnail replace", ty)
		}
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
	// Prefer a native cog-wsi fixture when present.
	if p := firstExisting(t, "cog-wsi/CMU-1_cog-wsi.tiff", "cog-wsi/CMU-1-Small-Region_cog-wsi.tiff"); p != "" {
		return p
	}
	// Otherwise synthesize one from an SVS fixture (present in CI), so the
	// cog-wsi editing paths still run wherever the SVS fixture exists rather
	// than silently skipping. rebuildCOGWSI with an empty plan is a faithful
	// SVS→cog-wsi copy carrying the SVS's associated images (label/overview/…).
	svs := firstExisting(t, "svs/CMU-1-Small-Region.svs", "svs/CMU-1.svs")
	if svs == "" {
		t.Skip("no cog-wsi or svs fixture")
	}
	src, err := source.Open(svs)
	if err != nil {
		t.Fatalf("open svs for cog-wsi synthesis: %v", err)
	}
	defer src.Close()
	out := filepath.Join(t.TempDir(), "synth_cog-wsi.tiff")
	if err := rebuildCOGWSI(src, out, assocEditPlan{}, false); err != nil {
		t.Fatalf("synthesize cog-wsi fixture: %v", err)
	}
	return out
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

func TestOMETIFFOverviewReplaceRoundTrips(t *testing.T) {
	in := copyFile(t, ometiffFixture(t))
	ovSize, _, ok := assocOfType(t, in, "overview")
	if !ok {
		t.Skip("fixture has no overview")
	}
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, ovSize.X, ovSize.Y, color.RGBA{200, 30, 40, 255})
	out := filepath.Join(t.TempDir(), "out.ome.tiff")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedReplaceForOMETIFF("overview", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, compression: "jpeg", bgHex: "F5F5E6", force: true,
	}); err != nil {
		t.Fatalf("ome-tiff overview replace: %v", err)
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

// ometiffFixture returns an OME-TIFF fixture path, synthesizing a small one
// from the SVS fixture (via rebuildOMETIFF) when no real OME-TIFF is present so
// the OME-TIFF tests still run in CI.
func ometiffFixture(t *testing.T) string {
	if p := firstExisting(t, "ome-tiff/Leica-1.ome.tiff", "ome-tiff/Leica-2.ome.tiff"); p != "" {
		return p
	}
	svs := firstExisting(t, "svs/CMU-1-Small-Region.svs", "svs/CMU-1.svs")
	if svs == "" {
		t.Skip("no ome-tiff or svs fixture")
	}
	src, err := source.Open(svs)
	if err != nil {
		t.Fatalf("open svs: %v", err)
	}
	defer src.Close()
	out := filepath.Join(t.TempDir(), "synth.ome.tiff")
	if err := rebuildOMETIFF(src, out, omeEditPlan{}, false); err != nil {
		t.Fatalf("synthesize ome-tiff: %v", err)
	}
	return out
}

func TestOMETIFFLabelRemove(t *testing.T) {
	in := copyFile(t, ometiffFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("fixture has no label")
	}
	out := filepath.Join(t.TempDir(), "out.ome.tiff")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveForOMETIFF("label", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("ome-tiff label remove: %v", err)
	}
	osrc, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if osrc.Format() != "ome-tiff" {
		t.Errorf("output format = %q, want ome-tiff", osrc.Format())
	}
	osrc.Close()
	if _, _, ok := assocOfType(t, out, "label"); ok {
		t.Errorf("label still present")
	}
	if _, _, ok := assocOfType(t, out, "overview"); !ok {
		t.Errorf("overview vanished (contract: only target changes)")
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed")
	}
}

// TestOMETIFFRemoveInPlace exercises the atomic in-place path for OME-TIFF:
// the original is overwritten, the label is gone, the pyramid is unchanged, and
// no temp files are left behind.
func TestOMETIFFRemoveInPlace(t *testing.T) {
	in := copyFile(t, ometiffFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("fixture has no label")
	}
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveForOMETIFF("label", in, in, removeFlags{assocCommonFlags{inPlace: true, fsync: true}}); err != nil {
		t.Fatalf("in-place remove: %v", err)
	}
	if _, _, ok := assocOfType(t, in, "label"); ok {
		t.Errorf("label present after in-place remove")
	}
	if level0Digest(t, in) != digestBefore {
		t.Errorf("pyramid changed after in-place remove")
	}
	ents, _ := os.ReadDir(filepath.Dir(in))
	for _, e := range ents {
		if bytes.Contains([]byte(e.Name()), []byte(".tmp")) {
			t.Errorf("leftover temp: %s", e.Name())
		}
	}
}

// TestOMETIFFRemoveAbsentErrors asserts that removing a type that is not
// present in the OME-TIFF returns an error rather than silently succeeding.
func TestOMETIFFRemoveAbsentErrors(t *testing.T) {
	in := copyFile(t, ometiffFixture(t))
	err := runAssociatedRemoveForOMETIFF("no-such-type", in, filepath.Join(t.TempDir(), "o.ome.tiff"), removeFlags{assocCommonFlags{fsync: false}})
	if err == nil {
		t.Fatal("expected error for absent type")
	}
}

// TestOMETIFFEditWarnsLossy asserts that every OME-TIFF edit emits the lossy
// rebuild warning via slog (the warning mentions "rudimentary" and "Bio-Formats").
func TestOMETIFFEditWarnsLossy(t *testing.T) {
	in := copyFile(t, ometiffFixture(t))
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	out := filepath.Join(t.TempDir(), "out.ome.tiff")
	// Remove a type the fixture has (overview is present in both synth + Leica).
	typ := "overview"
	if _, _, ok := assocOfType(t, in, typ); !ok {
		typ = "label"
	}
	if err := runAssociatedRemoveForOMETIFF(typ, in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "rudimentary") || !strings.Contains(got, "Bio-Formats") {
		t.Errorf("lossy warning not emitted on remove; slog output:\n%s", got)
	}

	// The replace path must warn too.
	buf.Reset()
	png := filepath.Join(t.TempDir(), "x.png")
	sz, _, _ := assocOfType(t, in, typ)
	if sz.X == 0 {
		sz.X, sz.Y = 64, 64
	}
	writeSolidPNG(t, png, sz.X, sz.Y, color.RGBA{1, 2, 3, 255})
	out2 := filepath.Join(t.TempDir(), "out2.ome.tiff")
	if err := runAssociatedReplaceForOMETIFF(typ, in, out2, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, compression: "jpeg", bgHex: "F5F5E6", force: true,
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "rudimentary") || !strings.Contains(got, "Bio-Formats") {
		t.Errorf("lossy warning not emitted on replace; slog output:\n%s", got)
	}
}

// TestSVSMultiLevelThumbnailReplace: replacing the thumbnail on a multi-level SVS
// (which can't splice) goes through the rebuild and lands the new thumbnail at
// IFD 1, classified correctly; pyramid pixel-identical; label/overview intact.
func TestSVSMultiLevelThumbnailReplace(t *testing.T) {
	in := copyFile(t, multiLevelSVSFixture(t))
	origSize, origBytes, ok := assocOfType(t, in, "thumbnail")
	if !ok {
		t.Skip("fixture has no thumbnail")
	}
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, origSize.X, origSize.Y, color.RGBA{10, 20, 30, 255})
	out := filepath.Join(t.TempDir(), "out.svs")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedReplaceFor("thumbnail", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, resize: "fit", bgHex: "F5F5E6", force: true,
	}); err != nil {
		t.Fatalf("thumbnail replace: %v", err)
	}
	_, newBytes, ok := assocOfType(t, out, "thumbnail")
	if !ok {
		t.Fatalf("thumbnail missing/unclassified after multi-level replace")
	}
	if bytes.Equal(origBytes, newBytes) {
		t.Errorf("thumbnail content unchanged after replace")
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed after thumbnail replace")
	}
	for _, ty := range []string{"label", "overview"} {
		if _, _, ok := assocOfType(t, out, ty); !ok {
			t.Errorf("%s vanished after thumbnail replace", ty)
		}
	}
}

// TestSVSMultiLevelThumbnailRemove: removing the thumbnail on a multi-level SVS
// succeeds via rebuild; thumbnail gone, label/overview kept, pyramid intact.
func TestSVSMultiLevelThumbnailRemove(t *testing.T) {
	in := copyFile(t, multiLevelSVSFixture(t))
	if _, _, ok := assocOfType(t, in, "thumbnail"); !ok {
		t.Skip("fixture has no thumbnail")
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveFor("thumbnail", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("thumbnail remove: %v", err)
	}
	if _, _, ok := assocOfType(t, out, "thumbnail"); ok {
		t.Errorf("thumbnail still present after remove")
	}
	for _, ty := range []string{"label", "overview"} {
		if _, _, ok := assocOfType(t, out, ty); !ok {
			t.Errorf("%s vanished after thumbnail remove", ty)
		}
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed after thumbnail remove")
	}
}

// multiLevelSVSFixture returns a multi-level SVS (tiled pyramid levels follow the
// thumbnail at IFD 1), or skips. 239551.svs is JPEG-tiled with 3 levels +
// thumbnail/label/overview.
func multiLevelSVSFixture(t *testing.T) string {
	p := firstExisting(t, "svs/239551.svs")
	if p == "" {
		t.Skip("no multi-level SVS fixture (svs/239551.svs)")
	}
	return p
}

// TestConvertToSVSMultiLevelKeepsThumbnail guards the IFD-1 placement fix: a
// multi-level SVS converted via the tile-copy path must keep the thumbnail and
// overview (they were dropped when the thumbnail stranded after the pyramid).
func TestConvertToSVSMultiLevelKeepsThumbnail(t *testing.T) {
	bin := stripedBinary(t)
	in := multiLevelSVSFixture(t)
	for _, ty := range []string{"thumbnail", "overview", "label"} {
		if _, _, ok := assocOfType(t, in, ty); !ok {
			t.Skipf("fixture lacks %s", ty)
		}
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	if o, err := runBin(bin, "convert", "--to", "svs", "-f", "-o", out, in); err != nil {
		t.Fatalf("convert --to svs: %v\n%s", err, o)
	}
	info, _ := runBin(bin, "info", out)
	for _, ty := range []string{"thumbnail", "label", "overview"} {
		if !strings.Contains(string(info), ty) {
			t.Errorf("converted multi-level SVS dropped %s:\n%s", ty, info)
		}
	}
	// Pyramid must be pixel-identical (verbatim tile-copy).
	if ds, db := pixelDigest(mustRun(t, bin, "hash", "--mode", "pixel", in)), pixelDigest(mustRun(t, bin, "hash", "--mode", "pixel", out)); ds == "" || ds != db {
		t.Errorf("pyramid pixels changed: src=%s out=%s", ds, db)
	}
}

func mustRun(t *testing.T, bin string, args ...string) []byte {
	t.Helper()
	out, _ := runBin(bin, args...)
	return out
}

// TestConvertToSVSMultiLevelReencodeKeepsThumbnail: the --codec re-encode path
// must also keep the thumbnail+overview on a multi-level SVS.
func TestConvertToSVSMultiLevelReencodeKeepsThumbnail(t *testing.T) {
	bin := stripedBinary(t)
	in := multiLevelSVSFixture(t)
	for _, ty := range []string{"thumbnail", "overview"} {
		if _, _, ok := assocOfType(t, in, ty); !ok {
			t.Skipf("fixture lacks %s", ty)
		}
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	if o, err := runBin(bin, "convert", "--to", "svs", "--codec", "jpeg", "-f", "-o", out, in); err != nil {
		t.Fatalf("convert --to svs --codec jpeg: %v\n%s", err, o)
	}
	// Assert opentile re-classifies each type (positional for the SVS thumbnail),
	// not just that the string appears in info — matches the tile-copy test.
	for _, ty := range []string{"thumbnail", "label", "overview"} {
		if _, _, ok := assocOfType(t, out, ty); !ok {
			t.Errorf("re-encoded multi-level SVS dropped %s (not classified by opentile)", ty)
		}
	}
}

// TestOMETIFFRealLeicaOverviewRemove exercises the multi-image real Leica
// OME-TIFF path (macro-series + main-series). Self-skips in -short mode and
// when the fixture is absent (e.g. CI).
func TestOMETIFFRealLeicaOverviewRemove(t *testing.T) {
	p := firstExisting(t, "ome-tiff/Leica-1.ome.tiff", "ome-tiff/Leica-2.ome.tiff")
	if p == "" {
		t.Skip("no real OME-TIFF fixture")
	}
	if testing.Short() {
		t.Skip("large fixture; skipped in -short")
	}
	in := copyFile(t, p)
	if _, _, ok := assocOfType(t, in, "overview"); !ok {
		t.Skip("fixture has no overview")
	}
	out := filepath.Join(t.TempDir(), "out.ome.tiff")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveForOMETIFF("overview", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("real-leica overview remove: %v", err)
	}
	osrc, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if osrc.Format() != "ome-tiff" {
		t.Errorf("format = %q", osrc.Format())
	}
	osrc.Close()
	if _, _, ok := assocOfType(t, out, "overview"); ok {
		t.Errorf("overview still present")
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed")
	}
}
