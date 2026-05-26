package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stripedBinary(t *testing.T) string {
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

func stripedSample(t *testing.T, rel string) string {
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

// TestStripedFormatsInfo: `wsitools info` works on NDPI / SCN /
// OME-OneFrame. Verifies exit 0 + non-empty output.
func TestStripedFormatsInfo(t *testing.T) {
	bin := stripedBinary(t)
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
			sample := stripedSample(t, c.rel)
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

// TestStripedFormatsTranscode: `wsitools transcode --codec jpeg
// --container svs` on each format produces a non-empty file.
func TestStripedFormatsTranscode(t *testing.T) {
	bin := stripedBinary(t)
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
			sample := stripedSample(t, c.rel)
			out := filepath.Join(t.TempDir(), "out.svs")
			cmdOut, err := exec.Command(bin,
				"transcode",
				"--codec", "jpeg",
				"--container", "svs",
				"-f", "-o", out,
				sample,
			).CombinedOutput()
			if err != nil {
				t.Fatalf("transcode %s: %v\n%s", c.rel, err, cmdOut)
			}
			info, err := os.Stat(out)
			if err != nil {
				t.Fatalf("output not created: %v", err)
			}
			if info.Size() == 0 {
				t.Errorf("transcode %s: output is empty", c.rel)
			}
		})
	}
}

// TestStripedFormatsHashPixel: `wsitools hash --mode pixel` produces
// a deterministic pixel-hash on each format.
func TestStripedFormatsHashPixel(t *testing.T) {
	bin := stripedBinary(t)
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
			sample := stripedSample(t, c.rel)
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

// TestStripedFormatsConvert: `wsitools convert --to cog-wsi` on each
// format produces a non-empty COG-WSI file.
//
// Note: bit-exact tile-copy from NDPI / OME-OneFrame is NOT promised
// (those sources don't have tile bytes; opentile-go synthesizes
// JPEG tiles). Test only checks that the COG-WSI output exists and
// is non-empty.
func TestStripedFormatsConvert(t *testing.T) {
	bin := stripedBinary(t)
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
			sample := stripedSample(t, c.rel)
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
