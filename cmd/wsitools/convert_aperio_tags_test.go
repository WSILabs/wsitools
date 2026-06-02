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
// emits ImageDepth=1 and YCbCrSubSampling=[1,1] matching the tiles'
// actual 4:4:4 encoding (parsed from the real tile via LumaSampling).
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
	// CMU-1-Small-Region.svs tiles are RGB/4:4:4 JPEG, so the actual
	// subsampling is [1,1] (the source's declared 530=[2,2] tag is ignored
	// — we describe the bytes we write).
	if !strings.Contains(ifd0, "YCbCrSubSampling") || !strings.Contains(ifd0, "[1, 1]") {
		t.Errorf("L0 missing YCbCrSubSampling [1, 1] (actual tile subsampling):\n%s", ifd0)
	}
}

// TestConvertNonSVSContainerOmitsAperioTags: the Aperio L0 tags are
// SVS-only. Converting the same JPEG source to a plain tiff container must
// emit neither ImageDepth nor YCbCrSubSampling. Uses CMU-1-Small-Region.svs
// (in the CI fixture set) so the gate is exercised in CI, not just locally.
func TestConvertNonSVSContainerOmitsAperioTags(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1-Small-Region.svs")
	out := filepath.Join(t.TempDir(), "out.tiff")

	if o, err := exec.Command(bin, "convert", "--to", "tiff", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ifd0 := dumpIFD0Raw(t, bin, out)
	if strings.Contains(ifd0, "ImageDepth") {
		t.Errorf("non-SVS (tiff) output unexpectedly has ImageDepth:\n%s", ifd0)
	}
	if strings.Contains(ifd0, "YCbCrSubSampling") {
		t.Errorf("non-SVS (tiff) output unexpectedly has YCbCrSubSampling:\n%s", ifd0)
	}
}
