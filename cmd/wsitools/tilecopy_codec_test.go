package main

import (
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

// TestCheckTileCopyCodec guards wsitools#33: verbatim tile-copy must honor the
// codec×container capability table (the same one the re-encode path uses) for the
// non-standard codecs, instead of a blanket "no standard TIFF Compression tag"
// rejection. Standard TIFF compressions are always copyable; a codec with no TIFF
// representation (Iris) is rejected with a re-encode hint.
func TestCheckTileCopyCodec(t *testing.T) {
	for _, tc := range []struct {
		name      string
		container string
		codec     source.Compression
		allow     bool
		wantErr   bool
		wantWarn  bool
		errSubstr string
	}{
		{"jpeg->tiff standard", "tiff", source.CompressionJPEG, false, false, false, ""},
		{"lzw->tiff standard", "tiff", source.CompressionLZW, false, false, false, ""},
		{"jpeg2000->svs standard", "svs", source.CompressionJPEG2000, false, false, false, ""},
		{"htj2k->tiff conformant", "tiff", source.CompressionHTJ2K, false, false, false, ""},
		{"htj2k->cog-wsi conformant", "cog-wsi", source.CompressionHTJ2K, false, false, false, ""},
		{"avif->tiff conformant", "tiff", source.CompressionAVIF, false, false, false, ""},
		{"htj2k->ome-tiff nonconformant no flag", "ome-tiff", source.CompressionHTJ2K, false, true, false, "allow-nonconformant"},
		{"htj2k->ome-tiff nonconformant with flag", "ome-tiff", source.CompressionHTJ2K, true, false, true, ""},
		{"jpegxl->tiff conformant (opentile v0.60.2)", "tiff", source.CompressionJPEGXL, false, false, false, ""},
		{"jpegxl->cog-wsi conformant (opentile v0.60.2)", "cog-wsi", source.CompressionJPEGXL, false, false, false, ""},
		{"jpegxl->ome-tiff nonconformant no flag", "ome-tiff", source.CompressionJPEGXL, false, true, false, "allow-nonconformant"},
		{"iris->tiff unrepresentable", "tiff", source.CompressionIrisProprietary, false, true, false, "re-encode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warn, err := checkTileCopyCodec(tc.container, tc.codec, tc.allow)
			if tc.wantErr && err == nil {
				t.Fatalf("want error, got nil (warn=%q)", warn)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
			if tc.wantErr && tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("error %q missing substring %q", err.Error(), tc.errSubstr)
			}
			if got := warn != ""; got != tc.wantWarn {
				t.Errorf("warn present = %v (%q), want %v", got, warn, tc.wantWarn)
			}
		})
	}
}
