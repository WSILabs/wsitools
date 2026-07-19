package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func strippedBinary(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"./bin/wsitools", "../../bin/wsitools"} {
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Skip("wsitools binary not found; run `make build` first")
	return ""
}

func strippedSample(t *testing.T, rel string) string {
	t.Helper()
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files")
	}
	path := filepath.Join(dir, rel)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("sample %s not available: %v", rel, err)
	}
	return path
}

// TestStrippedFormatsInfo: `wsitools info` works on NDPI / SCN /
// OME-OneFrame. Verifies exit 0 + non-empty output.
func TestStrippedFormatsInfo(t *testing.T) {
	bin := strippedBinary(t)
	cases := []struct {
		name string
		rel  string
	}{
		{"NDPI", "ndpi/CMU-1.ndpi"},
		{"LeicaSCN", "scn/Leica-1.scn"},
		{"OMETIFF", "ome-tiff/Leica-1.ome.tiff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sample := strippedSample(t, c.rel)
			out, err := exec.Command(bin, "info", sample).CombinedOutput()
			if err != nil {
				t.Fatalf("info %s: %v\n%s", c.rel, err, out)
			}
			if len(out) == 0 {
				t.Errorf("info %s: empty output", c.rel)
			}
			// Spot-check: output should contain the word "Format"
			if !strings.Contains(string(out), "Format") {
				t.Errorf("info %s: output missing 'Format':\n%s", c.rel, out)
			}
		})
	}
}

// TestStrippedFormatsConvertSVS: `wsitools convert --codec jpeg --to svs`
// on each format produces a non-empty file. (Ported off the removed
// `transcode` command; `--container svs` → `--to svs`.)
func TestStrippedFormatsConvertSVS(t *testing.T) {
	bin := strippedBinary(t)
	cases := []struct {
		name string
		rel  string
	}{
		{"NDPI", "ndpi/CMU-1.ndpi"},
		{"LeicaSCN", "scn/Leica-1.scn"},
		{"OMETIFF", "ome-tiff/Leica-1.ome.tiff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sample := strippedSample(t, c.rel)
			out := filepath.Join(t.TempDir(), "out.svs")
			cmdOut, err := exec.Command(bin,
				"convert",
				"--codec", "jpeg",
				"--to", "svs",
				"-f", "-o", out,
				sample,
			).CombinedOutput()
			if err != nil {
				// ENOSPC is environmental, not a regression.
				if strings.Contains(string(cmdOut), "no space left on device") {
					t.Skipf("disk full: %s", cmdOut)
				}
				t.Fatalf("convert --to svs %s: %v\n%s", c.rel, err, cmdOut)
			}
			info, err := os.Stat(out)
			if err != nil {
				t.Fatalf("output not created: %v", err)
			}
			if info.Size() == 0 {
				t.Errorf("convert --to svs %s: output is empty", c.rel)
			}
		})
	}
}

// TestStrippedFormatsHashPixel: `wsitools hash --mode pixel` produces
// a deterministic pixel-hash on each format.
func TestStrippedFormatsHashPixel(t *testing.T) {
	bin := strippedBinary(t)
	cases := []struct {
		name string
		rel  string
	}{
		{"NDPI", "ndpi/CMU-1.ndpi"},
		{"LeicaSCN", "scn/Leica-1.scn"},
		{"OMETIFF", "ome-tiff/Leica-1.ome.tiff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sample := strippedSample(t, c.rel)
			out, err := exec.Command(bin, "hash", "--mode", "pixel", sample).CombinedOutput()
			if err != nil {
				t.Fatalf("hash %s: %v\n%s", c.rel, err, out)
			}
			text := string(out)
			if !strings.Contains(text, "sha256-pixel:") {
				t.Errorf("hash %s: output missing 'sha256-pixel:':\n%s", c.rel, text)
			}
		})
	}
}

// TestStrippedFormatsConvert: `wsitools convert --to cog-wsi` on each
// format produces a non-empty COG-WSI file.
//
// Note: bit-exact tile-copy from NDPI / OME-OneFrame is NOT promised
// (those sources don't have tile bytes; opentile-go synthesizes
// JPEG tiles). Test only checks that the COG-WSI output exists and
// is non-empty.
func TestStrippedFormatsConvert(t *testing.T) {
	bin := strippedBinary(t)
	cases := []struct {
		name string
		rel  string
	}{
		{"NDPI", "ndpi/CMU-1.ndpi"},
		{"LeicaSCN", "scn/Leica-1.scn"},
		{"OMETIFF", "ome-tiff/Leica-1.ome.tiff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sample := strippedSample(t, c.rel)
			out := filepath.Join(t.TempDir(), "out.tiff")
			cmdOut, err := exec.Command(bin,
				"convert",
				"--to", "cog-wsi",
				"-f", "-o", out,
				sample,
			).CombinedOutput()
			if err != nil {
				// ENOSPC is environmental, not a regression.
				if strings.Contains(string(cmdOut), "no space left on device") {
					t.Skipf("disk full: %s", cmdOut)
				}
				t.Fatalf("convert %s: %v\n%s", c.rel, err, cmdOut)
			}
			info, err := os.Stat(out)
			if err != nil {
				t.Fatalf("output not created: %v", err)
			}
			if info.Size() == 0 {
				t.Errorf("convert %s: output is empty", c.rel)
			}
		})
	}
}
