package main

import (
	"archive/zip"
	"bytes"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A3: convert --to dzi/szi --factor N / --target-mag M downsamples during
// conversion (previously rejected). The output manifest describes the reduced
// image; the descent scales the source L0 region down via opentile ScaledStrips.

var dziDimRe = regexp.MustCompile(`(Width|Height)="(\d+)"`)

func dziDims(t *testing.T, manifest []byte) (w, h int) {
	t.Helper()
	for _, m := range dziDimRe.FindAllStringSubmatch(string(manifest), -1) {
		v, _ := strconv.Atoi(m[2])
		switch m[1] {
		case "Width":
			if w == 0 {
				w = v
			}
		case "Height":
			if h == 0 {
				h = v
			}
		}
	}
	if w == 0 || h == 0 {
		t.Fatalf("could not parse Width/Height from manifest:\n%s", manifest)
	}
	return w, h
}

func cmuFixture(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	in := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing")
	}
	return in
}

// emitDZI runs `convert --to dzi [extra...] -o <tmp>/out.dzi <in>` and returns
// the manifest dims + the _files tile-tree path.
func emitDZI(t *testing.T, in string, extra ...string) (w, h int, filesDir string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.dzi")
	args := append([]string{"convert", "--to", "dzi"}, extra...)
	args = append(args, "-o", out, in)
	if b, err := exec.Command(findBinary(t), args...).CombinedOutput(); err != nil {
		t.Fatalf("convert %v: %v\n%s", extra, err, b)
	}
	manifest, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	w, h = dziDims(t, manifest)
	return w, h, strings.TrimSuffix(out, ".dzi") + "_files"
}

func TestConvertDZIFactorHalvesDims(t *testing.T) {
	in := cmuFixture(t)
	w0, h0, _ := emitDZI(t, in)                      // L0 (no scaling)
	w2, h2, files := emitDZI(t, in, "--factor", "2") // reduced

	if w2 != w0/2 || h2 != h0/2 {
		t.Errorf("--factor 2 dims = %dx%d, want %dx%d (L0 %dx%d / 2)", w2, h2, w0/2, h0/2, w0, h0)
	}
	// The DZI level-0 tile (the 1px-ish coarsest) must be a decodable JPEG —
	// proves the Box-kernel scaled read produced real pixels, not garbage.
	tile, err := os.ReadFile(filepath.Join(files, "0", "0_0.jpeg"))
	if err != nil {
		t.Fatalf("read L0 tile: %v", err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(tile)); err != nil {
		t.Fatalf("L0 tile is not a decodable JPEG: %v", err)
	}
}

func TestConvertDZITargetMagResolvesToFactor(t *testing.T) {
	in := cmuFixture(t)
	// CMU-1-Small-Region AppMag is 20×; --target-mag 10 ⇒ factor 2.
	w0, h0, _ := emitDZI(t, in)
	wm, hm, _ := emitDZI(t, in, "--target-mag", "10")
	if wm != w0/2 || hm != h0/2 {
		t.Errorf("--target-mag 10 dims = %dx%d, want %dx%d (factor 2 from AppMag 20)", wm, hm, w0/2, h0/2)
	}
}

func TestConvertSZIFactorHalvesDims(t *testing.T) {
	in := cmuFixture(t)
	out := filepath.Join(t.TempDir(), "out.szi")
	if b, err := exec.Command(findBinary(t), "convert", "--to", "szi", "--factor", "2", "-o", out, in).CombinedOutput(); err != nil {
		t.Fatalf("convert szi --factor 2: %v\n%s", err, b)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open szi: %v", err)
	}
	defer zr.Close()
	var manifest []byte
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".dzi") {
			rc, _ := f.Open()
			manifest, _ = io.ReadAll(rc)
			rc.Close()
		}
	}
	if manifest == nil {
		t.Fatal("no .dzi manifest in szi archive")
	}
	w, h := dziDims(t, manifest)
	// CMU L0 is 2220×2967 ⇒ factor 2 ⇒ 1110×1483.
	if w != 1110 || h != 1483 {
		t.Errorf("szi --factor 2 dims = %dx%d, want 1110x1483", w, h)
	}
}
