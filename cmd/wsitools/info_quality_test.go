package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func infoBinary(t *testing.T) string {
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

func sampleSlide(t *testing.T) string {
	t.Helper()
	candidate := filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files/svs/CMU-1-Small-Region.svs")
	if _, err := os.Stat(candidate); err != nil {
		t.Skip("sample slide not available")
	}
	return candidate
}

func TestInfoQualityOnSVSJSON(t *testing.T) {
	bin := infoBinary(t)
	sample := sampleSlide(t)

	cmd := exec.Command(bin, "info", "--json", sample)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}

	var result struct {
		Levels []struct {
			Compression string `json:"compression"`
			Quality     *struct {
				Codec             string `json:"codec"`
				Lossless          bool   `json:"lossless"`
				QualityEstimate   int    `json:"quality_estimate"`
				ChromaSubsampling string `json:"chroma_subsampling"`
				Colorspace        string `json:"colorspace"`
				BitDepth          int    `json:"bit_depth"`
			} `json:"quality"`
		} `json:"levels"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v\noutput:\n%s", err, out)
	}
	if len(result.Levels) == 0 {
		t.Fatal("no levels in output")
	}

	// L0 should be JPEG with a non-zero Q estimate.
	// CMU-1-Small-Region.svs uses Aperio's shared JPEGTABLES mechanism;
	// the DQT-derived Q estimate may be lower than the nominal encoder Q.
	// We accept any estimate in [1, 100] to cover different encoders.
	l0 := result.Levels[0]
	if l0.Quality == nil {
		t.Fatal("L0 has no quality field")
	}
	if l0.Quality.Codec != "JPEG" {
		t.Errorf("L0 codec: got %q, want \"JPEG\"", l0.Quality.Codec)
	}
	if l0.Quality.QualityEstimate < 1 || l0.Quality.QualityEstimate > 100 {
		t.Errorf("L0 QualityEstimate: %d outside [1, 100]", l0.Quality.QualityEstimate)
	}
	if l0.Quality.ChromaSubsampling == "" {
		t.Error("L0 ChromaSubsampling: empty (expected 4:2:0 or 4:4:4)")
	}
	// A JPEG L0 must surface an effective colorspace — either RGB (Aperio's
	// APP14 raw-RGB framing, as in this fixture) or YCbCr (JFIF). Never blank.
	switch l0.Quality.Colorspace {
	case "RGB", "YCbCr":
	default:
		t.Errorf("L0 Colorspace: got %q, want RGB or YCbCr", l0.Quality.Colorspace)
	}
	// A JPEG L0 codestream must report a bit depth (8 for this brightfield fixture).
	if l0.Quality.BitDepth != 8 {
		t.Errorf("L0 BitDepth: got %d, want 8", l0.Quality.BitDepth)
	}
}

func TestInfoQualityOnSVSText(t *testing.T) {
	bin := infoBinary(t)
	sample := sampleSlide(t)

	cmd := exec.Command(bin, "info", sample)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	text := string(out)
	if !bytes.Contains(out, []byte("Q≈")) {
		t.Errorf("text output missing Q≈ quality summary:\n%s", text)
	}
	if !bytes.Contains(out, []byte("Levels:")) {
		t.Errorf("text output missing Levels: header:\n%s", text)
	}
	// The output should have AT LEAST one level line ending with the
	// quality summary; we don't pin exact format.
	if !strings.Contains(text, "jpeg") {
		t.Errorf("text output missing jpeg compression line:\n%s", text)
	}
}
