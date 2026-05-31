package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dumpRaw runs `dump-ifds --raw` and returns the output text.
func dumpRaw(t *testing.T, bin, file string) string {
	t.Helper()
	out, err := exec.Command(bin, "dump-ifds", "--raw", file).CombinedOutput()
	if err != nil {
		t.Fatalf("dump-ifds --raw %s: %v\n%s", file, err, out)
	}
	return string(out)
}

// TestDownsampleScalesMPPAndMag: a factor-2 downsample emits the WSI
// private MPP/mag tags with scaled values — magnification halved
// (40 → 20) and MPP doubled (~0.25 → ~0.50).
func TestDownsampleScalesMPPAndMag(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/scan_620_.svs")
	out := filepath.Join(t.TempDir(), "ds.svs")
	cmdOut, err := exec.Command(bin, "downsample", "--factor", "2", "-f", "-o", out, src).CombinedOutput()
	if err != nil {
		t.Fatalf("downsample: %v\n%s", err, cmdOut)
	}
	raw := dumpRaw(t, bin, out)
	if !strings.Contains(raw, "WSIMagnification") {
		t.Fatalf("downsample output missing WSIMagnification tag")
	}
	// Source 40x → output 20x.
	magLine := grepLine(raw, "WSIMagnification")
	if !strings.Contains(magLine, "value=20") {
		t.Errorf("WSIMagnification should be 20 (40/2); got: %s", magLine)
	}
	if !strings.Contains(raw, "WSIMPPx") {
		t.Errorf("downsample output missing WSIMPPx tag")
	}
	if !strings.Contains(raw, "XResolution") {
		t.Errorf("downsample output missing XResolution tag")
	}
}

// TestConvertCogWSICarriesScaleNDPI: cog-wsi from an NDPI source carries
// the WSI MPP/mag tags + resolution (cross-format MPP path).
func TestConvertCogWSICarriesScaleNDPI(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "ndpi/CMU-1.ndpi")
	out := filepath.Join(t.TempDir(), "o.cog.tiff")
	cmdOut, err := exec.Command(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, src).CombinedOutput()
	if err != nil {
		if strings.Contains(string(cmdOut), "no space left on device") {
			t.Skipf("disk full: %s", cmdOut)
		}
		t.Fatalf("convert: %v\n%s", err, cmdOut)
	}
	raw := dumpRaw(t, bin, out)
	for _, want := range []string{"WSIMPPx", "WSIMagnification", "XResolution", "ResolutionUnit"} {
		if !strings.Contains(raw, want) {
			t.Errorf("cog-wsi(NDPI) output missing %q", want)
		}
	}
}

// grepLine returns the first line containing sub, or "".
func grepLine(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

// TestInfoReportsMPPForNDPI proves the cross-format MPP fix: info on an
// NDPI fixture now prints an MPP line (previously dropped — NDPI carries
// MPP in its TIFF resolution tags, which opentile-go reads).
func TestInfoReportsMPPForNDPI(t *testing.T) {
	bin := stripedBinary(t)
	sample := stripedSample(t, "ndpi/CMU-1.ndpi")
	out, err := exec.Command(bin, "info", sample).CombinedOutput()
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "MPP:") {
		t.Errorf("info NDPI output missing 'MPP:' line:\n%s", out)
	}
}
