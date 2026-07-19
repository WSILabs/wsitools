package main

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func TestTileCopyEligible(t *testing.T) {
	cases := []struct {
		name   string
		target string
		codec  string
		srcC   source.Compression
		tiled  bool
		want   bool
	}{
		{"cogwsi jpeg tiled", "cog-wsi", "", source.CompressionJPEG, true, true},
		{"svs jpeg tiled", "svs", "", source.CompressionJPEG, true, true},
		// JPEG 2000 is a genuine Aperio SVS codec → tile-copy verbatim, not
		// re-encode (wsitools svs-jp2k asymmetry).
		{"svs jp2k tiled", "svs", "", source.CompressionJPEG2000, true, true},
		{"tiff webp tiled", "tiff", "", source.CompressionWebP, true, true},
		{"ome-tiff jpeg tiled", "ome-tiff", "", source.CompressionJPEG, true, true},
		// --codec forces re-encode.
		{"svs jpeg tiled w/ codec", "svs", "jpeg", source.CompressionJPEG, true, false},
		// Stripped source.
		{"cogwsi stripped", "cog-wsi", "", source.CompressionJPEG, false, false},
		// SVS is JPEG-only.
		{"svs webp tiled", "svs", "", source.CompressionWebP, true, false},
		// dzi/szi never tile-copy.
		{"dzi jpeg tiled", "dzi", "", source.CompressionJPEG, true, false},
		{"szi jpeg tiled", "szi", "", source.CompressionJPEG, true, false},
	}
	for _, c := range cases {
		got := tileCopyEligible(c.target, c.codec, c.srcC, c.tiled, 0, 0)
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTargetAcceptsCodec(t *testing.T) {
	if !targetAcceptsCodec("cog-wsi", source.CompressionAVIF) {
		t.Errorf("cog-wsi should accept AVIF")
	}
	if targetAcceptsCodec("svs", source.CompressionAVIF) {
		t.Errorf("svs should reject AVIF")
	}
	if !targetAcceptsCodec("svs", source.CompressionJPEG) {
		t.Errorf("svs should accept JPEG")
	}
	if !targetAcceptsCodec("svs", source.CompressionJPEG2000) {
		t.Errorf("svs should accept JPEG2000 (genuine Aperio codec → tile-copy verbatim)")
	}
	if !targetAcceptsCodec("tiff", source.CompressionJPEG2000) {
		t.Errorf("tiff should accept JPEG2000")
	}
	if targetAcceptsCodec("dzi", source.CompressionJPEG) {
		t.Errorf("dzi shouldn't even appear in this helper (always false)")
	}
}
