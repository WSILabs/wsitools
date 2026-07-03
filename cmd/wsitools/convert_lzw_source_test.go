package main

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
)

// F1: the convert re-encode and downsample materialize paths must decode
// LZW / uncompressed / Deflate source tiles via opentile-go's level-decode.
// Before F1 they picked a standalone codec by compression and errored
// "no decoder for source compression lzw" on these sources (standalone
// codec-of-bytes decode covers only JPEG / JP2K).

func TestConvertReencodeDecodesLZWAndUncompressedSource(t *testing.T) {
	bin := stripedBinary(t)
	for _, fx := range []struct{ name, file string }{
		{"lzw", "590_crop_lzw_imagescope.tif"},
		{"uncompressed", "590_crop_none_imagescope.tif"},
	} {
		t.Run(fx.name, func(t *testing.T) {
			src := filepath.Join(testDir(t), "svs", fx.file)
			if _, err := os.Stat(src); err != nil {
				t.Skipf("fixture absent: %v", err)
			}
			srcW, _ := l0WidthAndCodec(t, src)
			out := filepath.Join(t.TempDir(), "out.tiff")
			if o, err := runBin(bin, "convert", "--to", "tiff", "--codec", "jpeg",
				"--quality", "85", "-o", out, src); err != nil {
				t.Fatalf("convert --to tiff --codec jpeg on %s source: %v\n%s", fx.name, err, o)
			}
			// Re-encode preserves geometry and produces JPEG output; assert both,
			// not just that it ran.
			outW, outCodec := l0WidthAndCodec(t, out)
			if outW != srcW {
				t.Errorf("%s re-encode: L0 width = %d, want %d (geometry preserved)", fx.name, outW, srcW)
			}
			if outCodec != opentile.CompressionJPEG {
				t.Errorf("%s re-encode: L0 codec = %v, want JPEG", fx.name, outCodec)
			}
			// And the re-encoded output must decode end-to-end (pixel-hashable).
			if o, err := runBin(bin, "hash", "--mode", "pixel", out); err != nil {
				t.Fatalf("pixel-hash re-encoded output: %v\n%s", err, o)
			}
		})
	}
}

func TestDownsampleDecodesLZWSource(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "590_crop_lzw_imagescope.tif")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	srcW, _ := l0WidthAndCodec(t, src)
	out := filepath.Join(t.TempDir(), "ds.tiff")
	if o, err := runBin(bin, "downsample", "--factor", "2", "--quiet", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample --factor 2 on LZW source: %v\n%s", err, o)
	}
	outW, _ := l0WidthAndCodec(t, out)
	if want := srcW / 2; abs(outW-want) > 2 {
		t.Errorf("downsample LZW --factor 2: L0 width = %d, want ≈%d", outW, want)
	}
}
