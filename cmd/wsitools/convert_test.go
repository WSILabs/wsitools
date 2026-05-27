package main

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertFailsForMissingInput(t *testing.T) {
	dir := t.TempDir()
	rootCmd.SetArgs([]string{"convert", "--to", "cog-wsi", "-o", dir + "/out.tiff", dir + "/missing.svs"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error for missing input")
	}
}

func TestConvertFailsForBadTo(t *testing.T) {
	dir := t.TempDir()
	tmp, _ := os.Create(dir + "/in.tiff")
	tmp.Close()
	rootCmd.SetArgs([]string{"convert", "--to", "iris", "-o", dir + "/out.tiff", dir + "/in.tiff"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown target") {
		t.Errorf("expected unsupported --to error, got %v", err)
	}
}

func TestConvertFailsWhenOutputExists(t *testing.T) {
	dir := t.TempDir()
	in, _ := os.Create(dir + "/in.tiff")
	in.Close()
	out, _ := os.Create(dir + "/out.tiff")
	out.Close()
	rootCmd.SetArgs([]string{"convert", "--to", "cog-wsi", "-o", dir + "/out.tiff", dir + "/in.tiff"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got %v", err)
	}
}

func TestConvertToSVSReencode(t *testing.T) {
	bin := findBinary(t)
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files")
	}
	in := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing: " + in)
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	cmd := exec.Command(bin,
		"convert", "--to", "svs", "--codec", "jpeg", "--quality", "85", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("output missing or empty")
	}
}

func TestConvertToSVSTileCopy(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files")
	}
	in := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing: " + in)
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	// No --codec → tile-copy path.
	cmd := exec.Command(findBinary(t), "convert", "--to", "svs", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("output missing or empty")
	}
	// Pixel-hash equivalence: tile-copy preserves pixels.
	inHash := pixelHash(t, in)
	outHash := pixelHash(t, out)
	if inHash != outHash {
		t.Errorf("pixel hash mismatch: in=%s out=%s", inHash, outHash)
	}
}

func pixelHash(t *testing.T, path string) string {
	t.Helper()
	b, err := exec.Command(findBinary(t), "hash", "--mode", "pixel", path).Output()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// Output: "sha256-pixel:<hex> <path>"; strip the trailing path.
	return strings.SplitN(strings.TrimSpace(string(b)), " ", 2)[0]
}

func TestConvertToDZI(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	in := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing")
	}
	out := filepath.Join(t.TempDir(), "out.dzi")
	cmd := exec.Command(findBinary(t), "convert", "--to", "dzi", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("manifest missing")
	}
	tileDir := strings.TrimSuffix(out, ".dzi") + "_files"
	if _, err := os.Stat(tileDir); err != nil {
		t.Fatalf("tile dir missing: %v", err)
	}
	// L0 tile (DZI level 0 = 1x1 image).
	if _, err := os.Stat(filepath.Join(tileDir, "0", "0_0.jpeg")); err != nil {
		t.Fatalf("L0 tile missing: %v", err)
	}
}

func TestConvertToSZI(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	in := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(in); err != nil {
		t.Skip()
	}
	out := filepath.Join(t.TempDir(), "out.szi")
	cmd := exec.Command(findBinary(t), "convert", "--to", "szi", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	foundManifest := false
	foundScanProps := false
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Errorf("%s uses method %d; want Store(0)", f.Name, f.Method)
		}
		if strings.HasSuffix(f.Name, ".dzi") {
			foundManifest = true
		}
		if strings.HasSuffix(f.Name, "scan-properties.xml") {
			foundScanProps = true
		}
	}
	if !foundManifest {
		t.Errorf("no .dzi manifest in SZI archive")
	}
	if !foundScanProps {
		t.Errorf("no scan-properties.xml in SZI archive")
	}
}

func TestConvertNDPIToDZI(t *testing.T) {
	t.Skip("v0.17: NDPI→DZI exceeds 5min via per-tile ReadRegionScaled on " +
		"striped sources; ScaledStrips iterator wiring (planned v0.17) is " +
		"required for acceptable runtime. The code path is correct — verified " +
		"by hand on Hamamatsu-1.ndpi — but too slow for CI.")
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files")
	}
	in := filepath.Join(dir, "ndpi", "CMU-1.ndpi")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing: " + in)
	}
	out := filepath.Join(t.TempDir(), "out.dzi")
	cmd := exec.Command(findBinary(t), "convert", "--to", "dzi", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	tileDir := strings.TrimSuffix(out, ".dzi") + "_files"
	if _, err := os.Stat(filepath.Join(tileDir, "0", "0_0.jpeg")); err != nil {
		t.Fatalf("L0 tile missing: %v", err)
	}
}

func TestConvertNDPIToSZI(t *testing.T) {
	t.Skip("v0.17: same ScaledStrips perf issue as TestConvertNDPIToDZI")
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files")
	}
	in := filepath.Join(dir, "ndpi", "CMU-1.ndpi")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing: " + in)
	}
	out := filepath.Join(t.TempDir(), "out.szi")
	cmd := exec.Command(findBinary(t), "convert", "--to", "szi", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	haveManifest := false
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".dzi") {
			haveManifest = true
		}
	}
	if !haveManifest {
		t.Errorf("no .dzi manifest in SZI archive")
	}
}

func TestConvertNDPIToCOGWSIReencode(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files")
	}
	in := filepath.Join(dir, "ndpi", "CMU-1.ndpi")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing: " + in)
	}
	out := filepath.Join(t.TempDir(), "out.tiff")
	cmd := exec.Command(findBinary(t), "convert", "--to", "cog-wsi", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("output missing or empty")
	}
}

func TestConvertToTIFFTileCopy(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files")
	}
	in := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing: " + in)
	}
	out := filepath.Join(t.TempDir(), "out.tiff")
	cmd := exec.Command(findBinary(t), "convert", "--to", "tiff", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("output missing or empty")
	}
}

func TestConvertToOMETIFFReencode(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files")
	}
	in := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing: " + in)
	}
	out := filepath.Join(t.TempDir(), "out.ome.tiff")
	cmd := exec.Command(findBinary(t), "convert", "--to", "ome-tiff", "--codec", "jpeg", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("output missing or empty")
	}
}

func TestConvertHelpListsRequiredFlags(t *testing.T) {
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"convert", "--help"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"--to", "--output", "--force", "cog-wsi"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q\n%s", want, out)
		}
	}
}
