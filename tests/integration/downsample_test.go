//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	svsfmt "github.com/wsilabs/opentile-go/formats/svs"
)

func testdir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("WSI_TOOLS_TESTDIR")
	if d == "" {
		d = "../../sample_files"
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("WSI_TOOLS_TESTDIR=%s not accessible: %v", d, err)
	}
	return d
}

func buildOnce(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(os.TempDir(), "wsitools-it")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/wsitools")
	cmd.Dir = "../.."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	return bin
}

func TestDownsample_CMU1SmallRegion(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	bin := buildOnce(t)

	cmd := exec.Command(bin, "downsample", "-o", out, src)
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("downsample: %v\n%s", err, outBytes)
	}

	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("opentile.OpenFile(src): %v", err)
	}
	defer srcTlr.Close()
	outTlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile(out): %v", err)
	}
	defer outTlr.Close()

	if string(outTlr.Format()) != "svs" {
		t.Errorf("output format: got %q, want svs", outTlr.Format())
	}
	if got, want := len(outTlr.Levels()), len(srcTlr.Levels()); got != want {
		t.Errorf("level count: got %d, want %d", got, want)
	}

	srcL0, outL0 := srcTlr.Levels()[0], outTlr.Levels()[0]
	if outL0.Size().W != srcL0.Size().W/2 || outL0.Size().H != srcL0.Size().H/2 {
		t.Errorf("L0 size: got %vx%v, want %vx%v", outL0.Size().W, outL0.Size().H, srcL0.Size().W/2, srcL0.Size().H/2)
	}

	if md, ok := svsfmt.MetadataOf(outTlr); ok {
		// CMU-1-Small-Region is 20x; output should be 10x (factor 2).
		// Whichever it is, output AppMag = source AppMag / 2.
		srcMD, _ := svsfmt.MetadataOf(srcTlr)
		if srcMD != nil && md.Magnification != srcMD.Magnification/2 {
			t.Errorf("Magnification: got %v, want %v (=src/2)", md.Magnification, srcMD.Magnification/2)
		}
	}

	// Associated images pass-through: count matches and each image has non-zero bytes.
	// Note: JPEG-compressed associated images are reconstructed by opentile-go from
	// TIFF strips on re-read (DRI/restart-interval is recomputed from strip geometry),
	// so raw byte equality does not hold for JPEG kinds. LZW (label) bytes are
	// passthrough verbatim and do compare equal, but we don't test byte equality here
	// to keep the assertion robust across different wsiwriter strip layouts.
	srcAssoc := srcTlr.Associated()
	outAssoc := outTlr.Associated()
	if len(srcAssoc) != len(outAssoc) {
		t.Errorf("associated count: got %d, want %d", len(outAssoc), len(srcAssoc))
	}
	for i := range outAssoc {
		bs, err := outAssoc[i].Bytes()
		if err != nil {
			t.Errorf("associated[%d] (%s) Bytes() error: %v", i, outAssoc[i].Kind(), err)
		} else if len(bs) == 0 {
			t.Errorf("associated[%d] (%s) Bytes() returned empty", i, outAssoc[i].Kind())
		}
	}
}

func TestDownsample_SVSFixtures(t *testing.T) {
	td := testdir(t)
	matches, _ := filepath.Glob(filepath.Join(td, "svs", "*.svs"))
	if len(matches) == 0 {
		t.Skipf("no SVS fixtures in %s", td)
	}
	bin := buildOnce(t)
	for _, src := range matches {
		src := src
		base := filepath.Base(src)
		// Skip the 4.8 GB fixture — v0.1 holds the full L0 raster in
		// memory (~18 GB at 40x), which exceeds dev-machine RAM.
		// v0.2 streaming will lift this restriction.
		if base == "svs_40x_bigtiff.svs" {
			continue
		}
		t.Run(base, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.svs")
			cmd := exec.Command(bin, "downsample", "-o", out, src)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("downsample: %v\n%s", err, b)
			}
			tlr, err := opentile.OpenFile(out)
			if err != nil {
				t.Fatalf("re-open: %v", err)
			}
			defer tlr.Close()
			if string(tlr.Format()) != "svs" {
				t.Errorf("format: got %q, want svs", tlr.Format())
			}
			if len(tlr.Levels()) == 0 {
				t.Errorf("no levels in output")
			}
		})
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDownsample_AssociatedKindRoundTrip(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	bin := buildOnce(t)
	if b, err := exec.Command(bin, "downsample", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("downsample: %v\n%s", err, b)
	}

	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("opentile.OpenFile(src): %v", err)
	}
	defer srcTlr.Close()
	outTlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile(out): %v", err)
	}
	defer outTlr.Close()

	srcKinds := map[string]bool{}
	for _, a := range srcTlr.Associated() {
		srcKinds[a.Kind()] = true
	}
	outKinds := map[string]bool{}
	for _, a := range outTlr.Associated() {
		outKinds[a.Kind()] = true
	}
	for k := range srcKinds {
		if !outKinds[k] {
			t.Errorf("source had associated %q, output's missing it (kind round-trip broken)", k)
		}
	}
}
