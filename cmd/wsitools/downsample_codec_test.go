package main

import (
	"os"
	"path/filepath"
	"testing"
)

// downsample --factor 16 on a JPEG source currently fails at runtime
// (decoder/jpeg: scale=16). With the codec-agnostic fallback it must succeed.
func TestDownsampleFactor16JPEG(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "ds16.svs")
	if cmdOut, err := runBin(bin, "downsample", "--factor", "16", "--quiet", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample --factor 16: %v\n%s", err, cmdOut)
	}
	if cmdOut, err := runBin(bin, "info", out); err != nil {
		t.Fatalf("info on output: %v\n%s", err, cmdOut)
	}
}

// JPEG fast-scale path (factors 2/4/8) is unchanged and still works.
func TestDownsampleJPEGFastScaleStillWorks(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	for _, f := range []string{"2", "4", "8"} {
		out := filepath.Join(t.TempDir(), "ds"+f+".svs")
		if o, err := runBin(bin, "downsample", "--factor", f, "--quiet", "-f", "-o", out, src); err != nil {
			t.Fatalf("downsample --factor %s: %v\n%s", f, err, o)
		}
	}
}

// JP2K source now downsamples via scaled (resolution) decode. Output pixels
// differ from the old box path by design; assert it succeeds and reads back.
func TestDownsampleJP2KSource(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out := filepath.Join(t.TempDir(), "ds_jp2k.svs")
	if o, err := runBin(bin, "downsample", "--factor", "4", "--quiet", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample JP2K --factor 4: %v\n%s", o, err)
	}
	if o, err := runBin(bin, "info", out); err != nil {
		t.Fatalf("info on JP2K downsample output: %v\n%s", err, o)
	}
}
