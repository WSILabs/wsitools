package main

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func TestResolveTileSize(t *testing.T) {
	cases := []struct {
		name             string
		srcL0TileW, flag int
		want             int
	}{
		{"flag set wins", 256, 512, 512},
		{"unset matches source", 240, 0, 240},
		{"unset, no source tile -> 256", 0, 0, 256},
		{"flag set even with no source tile", 0, 1024, 1024},
	}
	for _, c := range cases {
		if got := resolveTileSize(c.srcL0TileW, c.flag); got != c.want {
			t.Errorf("%s: resolveTileSize(%d,%d) = %d, want %d", c.name, c.srcL0TileW, c.flag, got, c.want)
		}
	}
}

func TestReencodeCodecFor(t *testing.T) {
	if name, err := reencodeCodecFor(source.CompressionJPEG2000, "jpeg"); err != nil || name != "jpeg" {
		t.Errorf("explicit flag: got (%q,%v), want (jpeg,nil)", name, err)
	}
	if name, err := reencodeCodecFor(source.CompressionJPEG2000, ""); err != nil || name != "jpeg2000" {
		t.Errorf("jp2k source: got (%q,%v), want (jpeg2000,nil)", name, err)
	}
	if name, err := reencodeCodecFor(source.CompressionJPEG, ""); err != nil || name != "jpeg" {
		t.Errorf("jpeg source: got (%q,%v), want (jpeg,nil)", name, err)
	}
	if _, err := reencodeCodecFor(source.CompressionLZW, ""); err == nil {
		t.Error("LZW source, no --codec: expected error, got nil")
	}
}
