package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTileOrderFormatValidation exercises the CLI's per-format
// acceptance/rejection of --tile-order. Each (container, tile-order)
// combo is invoked against a minimal sample; expected outcome is
// success or a specific rejection message.
func TestTileOrderFormatValidation(t *testing.T) {
	bin := findBinary(t)
	sample := findSample(t)
	out := filepath.Join(t.TempDir(), "out.bin")

	cases := []struct {
		container  string
		tileOrder  string
		expectFail bool
	}{
		{"svs", "row-major", false},
		{"svs", "hilbert", true},
		{"svs", "morton", true},
		{"tiff", "row-major", false},
		{"tiff", "hilbert", false},
		{"tiff", "morton", false},
	}
	for _, c := range cases {
		t.Run(c.container+"/"+c.tileOrder, func(t *testing.T) {
			args := []string{
				"transcode", "--codec", "jpeg",
				"--container", c.container,
				"--tile-order", c.tileOrder,
				"-f", "-o", out, sample,
			}
			cmdOut, err := exec.Command(bin, args...).CombinedOutput()
			gotFail := err != nil
			if gotFail != c.expectFail {
				t.Errorf("container=%s order=%s: gotFail=%v wantFail=%v output:\n%s",
					c.container, c.tileOrder, gotFail, c.expectFail, cmdOut)
			}
			if c.expectFail && !strings.Contains(string(cmdOut), "tile order") {
				t.Errorf("expected rejection message about \"tile order\"; got: %s", cmdOut)
			}
		})
	}
}

// findBinary locates the wsitools binary built at ./bin/wsitools
// relative to the repo root; skips the test if not found.
func findBinary(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		"/Users/cornish/GitHub/wsitools/bin/wsitools",
		"./bin/wsitools",
		"../../bin/wsitools",
	} {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	t.Skip("wsitools binary not found; run `make build` first")
	return ""
}

func findSample(t *testing.T) string {
	t.Helper()
	candidate := filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files/svs/CMU-1-Small-Region.svs")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	t.Skip("sample file not available")
	return ""
}
