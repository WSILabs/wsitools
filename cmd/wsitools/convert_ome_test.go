package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestConvertOMEPyramidRoundTrips is the dropped-pyramid regression: a
// multi-level source converted to ome-tiff must read back with the SAME number
// of pyramid levels (sub-resolutions stored as SubIFDs), not 1.
func TestConvertOMEPyramidRoundTrips(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1.svs")
	out := filepath.Join(t.TempDir(), "out.ome.tiff")

	if o, err := exec.Command(bin, "convert", "--to", "ome-tiff", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	srcInfo, err := exec.Command(bin, "info", src).CombinedOutput()
	if err != nil {
		t.Fatalf("info src: %v\n%s", err, srcInfo)
	}
	outInfo, err := exec.Command(bin, "info", out).CombinedOutput()
	if err != nil {
		t.Fatalf("info out: %v\n%s", err, outInfo)
	}
	// Counts pyramid level lines in `info` output ("  L0  …", "  L1  …").
	// Associated-image lines use a different prefix, so they don't match.
	srcLevels := strings.Count(string(srcInfo), "\n  L")
	outLevels := strings.Count(string(outInfo), "\n  L")
	if outLevels != srcLevels {
		t.Errorf("ome-tiff level count = %d, want %d (source):\nSOURCE:\n%s\nOUT:\n%s",
			outLevels, srcLevels, srcInfo, outInfo)
	}
	if outLevels < 2 {
		t.Fatalf("expected a multi-level pyramid, got %d", outLevels)
	}
}

// TestConvertOMEStructure: L0 carries SubIFDs (330) + SampleFormat (339), and
// associated images are surfaced by the reader.
func TestConvertOMEStructure(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1.svs")
	out := filepath.Join(t.TempDir(), "out.ome.tiff")

	if o, err := exec.Command(bin, "convert", "--to", "ome-tiff", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ifd0 := dumpIFD0Raw(t, bin, out) // helper from convert_aperio_tags_test.go
	if !strings.Contains(ifd0, "SubIFDs") {
		t.Errorf("L0 missing SubIFDs:\n%s", ifd0)
	}
	if !strings.Contains(ifd0, "SampleFormat") {
		t.Errorf("L0 missing SampleFormat:\n%s", ifd0)
	}
	info, _ := exec.Command(bin, "info", out).CombinedOutput()
	s := strings.ToLower(string(info))
	if !strings.Contains(s, "label") && !strings.Contains(s, "macro") {
		t.Errorf("associated images not surfaced by reader:\n%s", info)
	}
}

// TestConvertOMESingleLevel: a single-level source converted to ome-tiff is
// still valid — L0 carries SampleFormat but NO SubIFDs tag (no sub-resolutions
// to reference), and the reader sees exactly one pyramid level.
func TestConvertOMESingleLevel(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1-Small-Region.svs")
	out := filepath.Join(t.TempDir(), "out.ome.tiff")

	if o, err := exec.Command(bin, "convert", "--to", "ome-tiff", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ifd0 := dumpIFD0Raw(t, bin, out)
	if !strings.Contains(ifd0, "SampleFormat") {
		t.Errorf("L0 missing SampleFormat:\n%s", ifd0)
	}
	if strings.Contains(ifd0, "SubIFDs") {
		t.Errorf("single-level output unexpectedly has a SubIFDs tag:\n%s", ifd0)
	}
	// Reader sees exactly one pyramid level.
	info, err := exec.Command(bin, "info", out).CombinedOutput()
	if err != nil {
		t.Fatalf("info: %v\n%s", err, info)
	}
	if got := strings.Count(string(info), "\n  L"); got != 1 {
		t.Errorf("ome-tiff level count = %d, want 1:\n%s", got, info)
	}
}
