package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dumpIFD0Raw runs `dump-ifds --raw` and returns only the IFD 0 section.
func dumpIFD0Raw(t *testing.T, bin, file string) string {
	t.Helper()
	out, err := exec.Command(bin, "dump-ifds", "--raw", file).CombinedOutput()
	if err != nil {
		t.Fatalf("dump-ifds: %v\n%s", err, out)
	}
	s := string(out)
	start := strings.Index(s, "IFD 0")
	if start < 0 {
		t.Fatalf("no IFD 0 in dump:\n%s", s)
	}
	rest := s[start+len("IFD 0"):]
	if end := strings.Index(rest, "\nIFD 1"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestConvertSVSAperioTagsJPEG: tile-copying a genuine-Aperio JPEG SVS
// emits ImageDepth=1 and YCbCrSubSampling=[2,2] on L0.
func TestConvertSVSAperioTagsJPEG(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1-Small-Region.svs")
	out := filepath.Join(t.TempDir(), "out.svs")

	if o, err := exec.Command(bin, "convert", "--to", "svs", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ifd0 := dumpIFD0Raw(t, bin, out)
	if !strings.Contains(ifd0, "ImageDepth") {
		t.Errorf("L0 missing ImageDepth:\n%s", ifd0)
	}
	if !strings.Contains(ifd0, "YCbCrSubSampling") || !strings.Contains(ifd0, "[2, 2]") {
		t.Errorf("L0 missing YCbCrSubSampling [2, 2]:\n%s", ifd0)
	}
}

// TestConvertSVSAperioTagsNonJPEG: tile-copying a JPEG2000 Aperio SVS to
// --to tiff (tile-copy eligible, container != "svs") emits neither
// ImageDepth nor YCbCrSubSampling — the Aperio-conformance block is
// SVS-container-gated. This validates that the gate is tight; non-SVS
// output is not contaminated with Aperio-private tags.
func TestConvertSVSAperioTagsNonJPEG(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/JP2K-33003-1.svs")
	out := filepath.Join(t.TempDir(), "out.tiff")

	if o, err := exec.Command(bin, "convert", "--to", "tiff", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ifd0 := dumpIFD0Raw(t, bin, out)
	if strings.Contains(ifd0, "ImageDepth") {
		t.Errorf("L0 unexpectedly has ImageDepth for non-SVS output:\n%s", ifd0)
	}
	if strings.Contains(ifd0, "YCbCrSubSampling") {
		t.Errorf("L0 unexpectedly has YCbCrSubSampling for non-SVS output:\n%s", ifd0)
	}
}
