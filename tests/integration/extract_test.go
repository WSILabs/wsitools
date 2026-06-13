//go:build integration

package integration

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExtract_LabelAsPNG(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	bin := buildOnce(t)

	out := filepath.Join(t.TempDir(), "label.png")
	cmd := exec.Command(bin, "extract", "--type", "label", "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extract: %v\n%s", err, b)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Errorf("decoded PNG has zero dimensions: %v", img.Bounds())
	}
}

func TestExtract_MacroAsJPEG_PassThrough(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	bin := buildOnce(t)

	out := filepath.Join(t.TempDir(), "overview.jpg")
	cmd := exec.Command(bin, "extract", "--type", "overview", "-o", out, "--format", "jpeg", src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extract: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Errorf("output is not a JPEG (missing SOI marker): %x", data[:4])
	}
}

func TestExtract_UnknownKind_Errors(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	bin := buildOnce(t)

	out := filepath.Join(t.TempDir(), "x.png")
	cmd := exec.Command(bin, "extract", "--type", "nonexistent", "-o", out, src)
	if b, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected error for unknown --type, got success:\n%s", b)
	}
}
