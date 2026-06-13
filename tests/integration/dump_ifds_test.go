//go:build integration

package integration

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpIFDs_HumanText(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	bin := buildOnce(t)

	out, err := exec.Command(bin, "dump-ifds", src).CombinedOutput()
	if err != nil {
		t.Fatalf("dump-ifds: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{"IFD 0", "pyramid", "SubfileType="} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output:\n%s", want, got)
		}
	}
}

func TestDumpIFDs_JSON(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	bin := buildOnce(t)

	out, err := exec.Command(bin, "dump-ifds", "--json", src).CombinedOutput()
	if err != nil {
		t.Fatalf("dump-ifds --json: %v\n%s", err, out)
	}
	var got struct {
		Path   string `json:"path"`
		Format string `json:"format"`
		IFDs   []struct {
			Index           int    `json:"index"`
			ImageType       string `json:"image_type"`
			Width           uint64 `json:"width"`
			Height          uint64 `json:"height"`
			Compression     uint64 `json:"compression_tag"`
			CompressionName string `json:"compression"`
		} `json:"ifds"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if got.Format != "svs" {
		t.Errorf("Format = %q, want svs", got.Format)
	}
	if len(got.IFDs) < 4 {
		t.Errorf("expected >= 4 IFDs, got %d", len(got.IFDs))
	}
	// At least one IFD should be classified as a pyramid level.
	foundPyramid := false
	for _, ifd := range got.IFDs {
		if ifd.ImageType == "pyramid" {
			foundPyramid = true
			break
		}
	}
	if !foundPyramid {
		t.Errorf("expected at least one IFD classified as pyramid")
	}
}
