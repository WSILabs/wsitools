//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
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
