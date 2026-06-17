package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SVS downsample via convert must match the standalone downsample (pixel-equal),
// and scale metadata (factor-4 of a 40x slide → ~10x, MPP ×4).
func TestConvertFactorSVSParity(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	a := filepath.Join(t.TempDir(), "a.svs")
	b := filepath.Join(t.TempDir(), "b.svs")
	if o, err := runBin(bin, "downsample", "--factor", "4", "--quiet", "-f", "-o", a, src); err != nil {
		t.Fatalf("downsample: %v\n%s", err, o)
	}
	if o, err := runBin(bin, "convert", "--to", "svs", "--factor", "4", "-f", "-o", b, src); err != nil {
		t.Fatalf("convert --factor: %v\n%s", err, o)
	}
	ha, _ := runBin(bin, "hash", "--mode", "pixel", a)
	hb, _ := runBin(bin, "hash", "--mode", "pixel", b)
	if pixelDigest(ha) == "" || pixelDigest(ha) != pixelDigest(hb) {
		t.Errorf("pixel hash mismatch:\n a=%s\n b=%s", ha, hb)
	}
	// CMU-1-Small-Region.svs is 20x; factor-4 → 5x.
	// (A 40x fixture would yield 10x; adjust this line if the fixture changes.)
	info, _ := runBin(bin, "info", b)
	if !strings.Contains(string(info), "Magnification: 5x") {
		t.Errorf("expected Magnification 5x (source 20x / factor 4) in:\n%s", info)
	}
}

// NOTE: dzi/szi --factor is no longer rejected — it downsamples during
// conversion (survey A3); see TestConvertDZIFactorHalvesDims et al. The former
// TestConvertFactorRejectsDZI (which asserted the removed rejection) was deleted.

// tiff + cog-wsi targets must produce scaled metadata (factor-4 of a 20x → 5x).
func TestConvertFactorTIFFTargets(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	// 20x fixture, factor 4 -> 5x, MPP x4.
	for _, tgt := range []struct{ to, ext string }{{"tiff", "tiff"}, {"cog-wsi", "cog.tiff"}} {
		tgt := tgt
		t.Run(tgt.to, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "o."+tgt.ext)
			if o, err := runBin(bin, "convert", "--to", tgt.to, "--factor", "4", "-f", "-o", out, src); err != nil {
				t.Fatalf("%s --factor 4: %v\n%s", tgt.to, err, o)
			}
			info, _ := runBin(bin, "info", out)
			if !strings.Contains(string(info), "Magnification: 5x") {
				t.Errorf("%s: expected 5x in info:\n%s", tgt.to, info)
			}
		})
	}
}

// invalid factor rejected (full message wired in a later task; here at least it must not silently pass).
func TestConvertFactorRejectsBadValue(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.svs")
	o, err := runBin(bin, "convert", "--to", "svs", "--factor", "3", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected invalid-factor rejection, got success:\n%s", o)
	}
}

func TestDownsampleAliasEqualsConvert(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	a := filepath.Join(t.TempDir(), "a.svs")
	b := filepath.Join(t.TempDir(), "b.svs")
	if o, err := runBin(bin, "downsample", "--factor", "2", "--quiet", "-f", "-o", a, src); err != nil {
		t.Fatalf("downsample: %v\n%s", err, o)
	}
	if o, err := runBin(bin, "convert", "--to", "svs", "--factor", "2", "-f", "-o", b, src); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ha, _ := runBin(bin, "hash", "--mode", "pixel", a)
	hb, _ := runBin(bin, "hash", "--mode", "pixel", b)
	if pixelDigest(ha) == "" || pixelDigest(ha) != pixelDigest(hb) {
		t.Errorf("downsample alias != convert --to svs --factor:\n a=%s\n b=%s", ha, hb)
	}
}

func TestConvertFactorOMETIFF(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.ome.tiff")
	if o, err := runBin(bin, "convert", "--to", "ome-tiff", "--factor", "4", "-f", "-o", out, src); err != nil {
		t.Fatalf("ome-tiff --factor 4: %v\n%s", err, o)
	}
	// 20x fixture, factor 4 -> 5x. Output must read back as ome-tiff at the reduced size.
	info, _ := runBin(bin, "info", out)
	if !strings.Contains(string(info), "Magnification: 5x") {
		t.Errorf("expected 5x in info:\n%s", info)
	}
	if !strings.Contains(string(info), "Format:  ome-tiff") {
		t.Errorf("expected ome-tiff format in info:\n%s", info)
	}
}

// downsample is format-preserving: OME-TIFF source -> OME-TIFF output (reduced).
func TestDownsamplePreservesOMETIFF(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "ome-tiff", "Leica-1.ome.tiff")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.ome.tiff")
	if o, err := runBin(bin, "downsample", "--factor", "2", "--quiet", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample ome-tiff: %v\n%s", err, o)
	}
	info, _ := runBin(bin, "info", out)
	if !strings.Contains(string(info), "Format:  ome-tiff") {
		t.Errorf("expected ome-tiff output, got:\n%s", info)
	}
}

// downsample of a COG-WSI source preserves cog-wsi.
func TestDownsamplePreservesCOGWSI(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "cog-wsi", "CMU-1-Small-Region_cog-wsi.tiff")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.cog.tiff")
	if o, err := runBin(bin, "downsample", "--factor", "2", "--quiet", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample cog-wsi: %v\n%s", err, o)
	}
	info, _ := runBin(bin, "info", out)
	if !strings.Contains(string(info), "Format:  cog-wsi") {
		t.Errorf("expected cog-wsi output, got:\n%s", info)
	}
}

// convert --to svs --factor accepts a NON-SVS source (cross-format reduce),
// matching its tiff/ome-tiff/cog-wsi siblings. The output must re-detect as SVS
// and carry scaled metadata synthesized from the source's opentile metadata.
// Guards A2 (the old code rejected src.Format() != SVS).
func TestConvertFactorSVSFromNonSVS(t *testing.T) {
	bin := stripedBinary(t)
	// cog-wsi source: non-SVS format, Aperio Make, MPP 0.499, 20x.
	src := filepath.Join(testDir(t), "cog-wsi", "CMU-1-Small-Region_cog-wsi.tiff")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.svs")
	if o, err := runBin(bin, "convert", "--to", "svs", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to svs --factor 2 from cog-wsi: %v\n%s", err, o)
	}
	info, _ := runBin(bin, "info", out)
	if !strings.Contains(string(info), "Format:  svs") {
		t.Errorf("expected svs output (re-detected), got:\n%s", info)
	}
	// 20x source / factor 2 → 10x.
	if !strings.Contains(string(info), "Magnification: 10x") {
		t.Errorf("expected Magnification 10x (source 20x / factor 2), got:\n%s", info)
	}
}

// downsample of a non-writable source format errors with a pointer to convert.
func TestDownsampleRejectsNonWritableFormat(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "ndpi", "CMU-1.ndpi")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "o.ndpi")
	o, err := runBin(bin, "downsample", "--factor", "2", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected error for ndpi source, got success:\n%s", o)
	}
	if !strings.Contains(string(o), "convert --to") {
		t.Errorf("error should point to convert --to, got:\n%s", o)
	}
}
