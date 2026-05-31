package main

import (
	"os/exec"
	"strings"
	"testing"
)

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
