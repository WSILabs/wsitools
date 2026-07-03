package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
)

// downsample --factor 16 on a JPEG source currently fails at runtime
// (decoder/jpeg: scale=16). With the codec-agnostic fallback it must succeed
// AND actually reduce L0 by 16× (not silently no-op / mis-scale).
func TestDownsampleFactor16JPEG(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	srcW, _ := l0WidthAndCodec(t, src)
	out := filepath.Join(t.TempDir(), "ds16.svs")
	if cmdOut, err := runBin(bin, "downsample", "--factor", "16", "--quiet", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample --factor 16: %v\n%s", err, cmdOut)
	}
	outW, _ := l0WidthAndCodec(t, out)
	if want := srcW / 16; abs(outW-want) > 2 {
		t.Errorf("downsample --factor 16: L0 width = %d, want ≈%d (source %d / 16)", outW, want, srcW)
	}
}

// JPEG fast-scale path (factors 2/4/8) is unchanged and must reduce L0 by the
// requested factor.
func TestDownsampleJPEGFastScaleStillWorks(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	srcW, _ := l0WidthAndCodec(t, src)
	for _, f := range []int{2, 4, 8} {
		out := filepath.Join(t.TempDir(), "ds"+strconv.Itoa(f)+".svs")
		if o, err := runBin(bin, "downsample", "--factor", strconv.Itoa(f), "--quiet", "-f", "-o", out, src); err != nil {
			t.Fatalf("downsample --factor %d: %v\n%s", f, err, o)
		}
		outW, _ := l0WidthAndCodec(t, out)
		if want := srcW / f; abs(outW-want) > 2 {
			t.Errorf("downsample --factor %d: L0 width = %d, want ≈%d", f, outW, want)
		}
	}
}

// JP2K source downsamples via scaled (resolution) decode. Output pixels differ
// from the old box path by design; assert it reduces L0 4× AND preserves the
// source JP2K codec (single-axis transform must not silently transcode to jpeg).
func TestDownsampleJP2KSource(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	srcW, _ := l0WidthAndCodec(t, src)
	out := filepath.Join(t.TempDir(), "ds_jp2k.svs")
	if o, err := runBin(bin, "downsample", "--factor", "4", "--quiet", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample JP2K --factor 4: %v\n%s", o, err)
	}
	outW, outCodec := l0WidthAndCodec(t, out)
	if want := srcW / 4; abs(outW-want) > 2 {
		t.Errorf("downsample JP2K --factor 4: L0 width = %d, want ≈%d", outW, want)
	}
	if outCodec != opentile.CompressionJP2K {
		t.Errorf("downsample JP2K: L0 codec = %v, want JP2K preserved (not transcoded)", outCodec)
	}
}
