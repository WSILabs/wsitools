package main

import (
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestParseRect(t *testing.T) {
	cases := []struct {
		in      string
		wantX   int
		wantY   int
		wantW   int
		wantH   int
		wantErr string // substring; empty means no error expected
	}{
		{"0,0,512,512", 0, 0, 512, 512, ""},
		{"100,200,640,480", 100, 200, 640, 480, ""},
		{" 100 , 200 , 640 , 480 ", 100, 200, 640, 480, ""}, // whitespace ok
		{"0,0,512", 0, 0, 0, 0, "expected X,Y,W,H"},
		{"0,0,512,512,99", 0, 0, 0, 0, "expected X,Y,W,H"},
		{"0,0,abc,512", 0, 0, 0, 0, "not an integer"},
		{"0,0,0,512", 0, 0, 0, 0, "W and H must be positive"},
		{"0,0,512,0", 0, 0, 0, 0, "W and H must be positive"},
		{"0,0,-1,512", 0, 0, 0, 0, "W and H must be positive"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			x, y, w, h, err := parseRect(c.in)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("got error %v, want nil", err)
				}
				if x != c.wantX || y != c.wantY || w != c.wantW || h != c.wantH {
					t.Errorf("got (%d,%d,%d,%d), want (%d,%d,%d,%d)", x, y, w, h, c.wantX, c.wantY, c.wantW, c.wantH)
				}
			} else {
				if err == nil {
					t.Fatalf("got nil error, want substring %q", c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("got %v, want substring %q", err, c.wantErr)
				}
			}
		})
	}
}

// resolveRectScenario builds a fresh cobra.Command + flags + sets the
// given flag values, then calls resolveRect. Captures both result
// and error.
func resolveRectScenario(t *testing.T, setRect bool, rect string, setX, setY, setW, setH bool, x, y, w, h int) (rx, ry, rw, rh int, err error) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().StringVar(&regionRect, "rect", "", "")
	cmd.Flags().IntVar(&regionX, "x", 0, "")
	cmd.Flags().IntVar(&regionY, "y", 0, "")
	cmd.Flags().IntVar(&regionW, "w", 0, "")
	cmd.Flags().IntVar(&regionH, "h", 0, "")

	// Reset package-level globals (test order independence).
	regionRect = ""
	regionX, regionY, regionW, regionH = 0, 0, 0, 0

	args := []string{}
	if setRect {
		args = append(args, "--rect", rect)
	}
	if setX {
		args = append(args, "--x", itoa(x))
	}
	if setY {
		args = append(args, "--y", itoa(y))
	}
	if setW {
		args = append(args, "--w", itoa(w))
	}
	if setH {
		args = append(args, "--h", itoa(h))
	}
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags(%v): %v", args, err)
	}
	return resolveRect(cmd)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+(n%10))) + digits
		n /= 10
	}
	if neg {
		return "-" + digits
	}
	return digits
}

func TestResolveRectFromRect(t *testing.T) {
	x, y, w, h, err := resolveRectScenario(t, true, "1,2,3,4", false, false, false, false, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if x != 1 || y != 2 || w != 3 || h != 4 {
		t.Errorf("got (%d,%d,%d,%d), want (1,2,3,4)", x, y, w, h)
	}
}

func TestResolveRectFromIndividual(t *testing.T) {
	x, y, w, h, err := resolveRectScenario(t, false, "", true, true, true, true, 10, 20, 30, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if x != 10 || y != 20 || w != 30 || h != 40 {
		t.Errorf("got (%d,%d,%d,%d), want (10,20,30,40)", x, y, w, h)
	}
}

func TestResolveRectMutuallyExclusive(t *testing.T) {
	_, _, _, _, err := resolveRectScenario(t, true, "0,0,10,10", true, false, false, false, 100, 0, 0, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("want substring 'mutually exclusive', got: %v", err)
	}
}

func TestResolveRectMissingIndividual(t *testing.T) {
	// Set only --x; --y, --w, --h missing.
	_, _, _, _, err := resolveRectScenario(t, false, "", true, false, false, false, 100, 0, 0, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"--y", "--w", "--h"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want substring %q, got: %v", want, err)
		}
	}
}

func TestResolveRectNoneSet(t *testing.T) {
	_, _, _, _, err := resolveRectScenario(t, false, "", false, false, false, false, 0, 0, 0, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "either --rect or all of") {
		t.Errorf("want substring 'either --rect or all of', got: %v", err)
	}
}

func TestResolveRectIndividualNonPositiveWH(t *testing.T) {
	_, _, _, _, err := resolveRectScenario(t, false, "", true, true, true, true, 0, 0, 0, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "must be positive") {
		t.Errorf("want substring 'must be positive', got: %v", err)
	}
}

// regionBinary returns the absolute path to the wsitools binary built
// via `make build`. Tests skip if it's not present.
func regionBinary(t *testing.T) string {
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

func regionSample(t *testing.T) string {
	t.Helper()
	candidate := filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files/svs/CMU-1-Small-Region.svs")
	if _, err := os.Stat(candidate); err != nil {
		t.Skip("sample slide not available")
	}
	return candidate
}

func TestRegionBasicSVS(t *testing.T) {
	bin := regionBinary(t)
	sample := regionSample(t)
	out := filepath.Join(t.TempDir(), "out.png")

	cmd := exec.Command(bin, "region", "--level", "0", "--rect", "0,0,128,128", "-o", out, sample)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wsitools region: %v\n%s", err, output)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output not created: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("output is empty")
	}

	// Decode the PNG and check dimensions.
	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
		t.Errorf("dims: got %dx%d, want 128x128", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestRegionRGBAOutput(t *testing.T) {
	bin := regionBinary(t)
	sample := regionSample(t)
	out := filepath.Join(t.TempDir(), "out.png")

	cmd := exec.Command(bin, "region",
		"--level", "0", "--rect", "0,0,64,64",
		"--format", "rgba",
		"-o", out, sample)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wsitools region: %v\n%s", err, output)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// All alpha bytes must be 0xFF (the decoder synthesizes opaque alpha).
	nrgba, ok := img.(*image.NRGBA)
	if !ok {
		t.Skipf("PNG decoded to %T (not NRGBA); skipping alpha-byte check", img)
	}
	for i := 3; i < len(nrgba.Pix); i += 4 {
		if nrgba.Pix[i] != 0xFF {
			t.Fatalf("alpha at offset %d: got %d, want 0xFF", i, nrgba.Pix[i])
		}
	}
}

func TestRegionExtensionCheck(t *testing.T) {
	bin := regionBinary(t)
	sample := regionSample(t)
	out := filepath.Join(t.TempDir(), "out.bmp")

	cmd := exec.Command(bin, "region", "--level", "0", "--rect", "0,0,64,64", "-o", out, sample)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success\n%s", output)
	}
	if !strings.Contains(string(output), "only PNG output is supported") {
		t.Errorf("want 'only PNG output is supported' in error, got: %s", output)
	}
}

func TestRegionOverwriteWithoutForce(t *testing.T) {
	bin := regionBinary(t)
	sample := regionSample(t)
	out := filepath.Join(t.TempDir(), "out.png")

	// Pre-create the output file.
	if err := os.WriteFile(out, []byte("placeholder"), 0644); err != nil {
		t.Fatalf("pre-create output: %v", err)
	}

	cmd := exec.Command(bin, "region", "--level", "0", "--rect", "0,0,64,64", "-o", out, sample)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success\n%s", output)
	}
	if !strings.Contains(string(output), "output exists") {
		t.Errorf("want 'output exists' in error, got: %s", output)
	}
}

func TestRegionOverwriteWithForce(t *testing.T) {
	bin := regionBinary(t)
	sample := regionSample(t)
	out := filepath.Join(t.TempDir(), "out.png")

	// Pre-create the output file.
	if err := os.WriteFile(out, []byte("placeholder"), 0644); err != nil {
		t.Fatalf("pre-create output: %v", err)
	}

	cmd := exec.Command(bin, "region", "--level", "0", "--rect", "0,0,64,64",
		"--force", "-o", out, sample)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success with --force, got: %v\n%s", err, output)
	}

	// Verify it was actually overwritten (file should now decode as PNG, not be "placeholder").
	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, err := png.Decode(f); err != nil {
		t.Errorf("output is not a valid PNG: %v", err)
	}
}

func TestRegionOutOfRangeLevel(t *testing.T) {
	bin := regionBinary(t)
	sample := regionSample(t)
	out := filepath.Join(t.TempDir(), "out.png")

	cmd := exec.Command(bin, "region", "--level", "99", "--rect", "0,0,64,64", "-o", out, sample)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success\n%s", output)
	}
	if !strings.Contains(string(output), "out of range") {
		t.Errorf("want 'out of range' in error, got: %s", output)
	}
}
