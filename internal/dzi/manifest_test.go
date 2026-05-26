package dzi

import (
	"bytes"
	"strings"
	"testing"
)

func TestManifestBytes(t *testing.T) {
	m := Manifest{Format: "jpeg", Overlap: 1, TileSize: 256, Width: 2220, Height: 2967}
	var buf bytes.Buffer
	if err := m.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`xmlns="http://schemas.microsoft.com/deepzoom/2008"`,
		`Format="jpeg"`,
		`Overlap="1"`,
		`TileSize="256"`,
		`Width="2220"`,
		`Height="2967"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q\nfull output:\n%s", want, s)
		}
	}
}

func TestManifestPNGFormat(t *testing.T) {
	m := Manifest{Format: "png", Overlap: 0, TileSize: 512, Width: 1024, Height: 768}
	var buf bytes.Buffer
	if err := m.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `Format="png"`) {
		t.Errorf("png format not preserved")
	}
}
