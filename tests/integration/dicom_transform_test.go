//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI runs the built wsitools binary and returns combined output. The
// integration package runs the binary directly via exec (there is no shared
// run helper); buildOnce and testdir are defined in downsample_test.go.
func runCLI(bin string, args ...string) (string, error) {
	out, err := exec.Command(bin, args...).CombinedOutput()
	return string(out), err
}

// dicomFixture returns a DICOM source dir under the test pool, or skips.
func dicomFixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(testdir(t), "dicom", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no DICOM fixture %s", name)
	}
	return p
}

// countDCM returns the number of *.dcm files in dir.
func countDCM(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".dcm" {
			n++
		}
	}
	return n
}

func TestDownsampleDICOM_Factor2(t *testing.T) {
	bin := buildOnce(t)
	src := dicomFixture(t, "scan_621_grundium_dicom")
	out := filepath.Join(t.TempDir(), "down.dcmdir")
	if o, err := runCLI(bin, "downsample", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample --factor 2 <dicom>: %v\n%s", err, o)
	}
	if n := countDCM(t, out); n < 1 {
		t.Errorf("output has %d .dcm instances, want >= 1", n)
	}
	// dciodvfy is run by the controller (conformance gate), not here.
}

func TestConvertDICOM_FactorFromSVS(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no svs fixture")
	}
	out := filepath.Join(t.TempDir(), "svs2dcm.dcmdir")
	if o, err := runCLI(bin, "convert", "--to", "dicom", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to dicom --factor 2 <svs>: %v\n%s", err, o)
	}
	if n := countDCM(t, out); n < 1 {
		t.Errorf("output has %d .dcm instances, want >= 1", n)
	}
}

func TestConvertDICOM_FactorRejectsLevel(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no svs fixture")
	}
	out := filepath.Join(t.TempDir(), "x.dcmdir")
	o, err := runCLI(bin, "convert", "--to", "dicom", "--factor", "2", "--level", "0", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected --factor + --level rejection, got success:\n%s", o)
	}
	if !strings.Contains(o, "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' in output, got:\n%s", o)
	}
}

func TestCropDICOM_ReEncode(t *testing.T) {
	bin := buildOnce(t)
	src := dicomFixture(t, "scan_621_grundium_dicom")
	out := filepath.Join(t.TempDir(), "crop.dcmdir")
	if o, err := runCLI(bin, "crop", "--rect", "0,0,512,512", "-f", "-o", out, src); err != nil {
		t.Fatalf("crop <dicom>: %v\n%s", err, o)
	}
	if n := countDCM(t, out); n < 1 {
		t.Errorf("output has %d .dcm instances, want >= 1", n)
	}
}

func TestCropDICOM_Lossless(t *testing.T) {
	bin := buildOnce(t)
	src := dicomFixture(t, "scan_621_grundium_dicom")
	out := filepath.Join(t.TempDir(), "croplossless.dcmdir")
	// Lossless snaps the rect up to the source tile grid; output is a tile-
	// aligned superset of the requested region.
	if o, err := runCLI(bin, "crop", "--rect", "0,0,512,512", "--lossless", "-f", "-o", out, src); err != nil {
		t.Fatalf("crop --lossless <dicom>: %v\n%s", err, o)
	}
	if n := countDCM(t, out); n < 1 {
		t.Errorf("output has %d .dcm instances, want >= 1", n)
	}
	// The L0-frame byte-identity oracle is run by the controller (needs to parse
	// DICOM frames + compare to the source); this test just guards the CLI path.
}
