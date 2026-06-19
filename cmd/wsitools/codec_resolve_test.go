package main

import (
	"testing"

	_ "github.com/wsilabs/wsitools/internal/codec/jp2k"
	_ "github.com/wsilabs/wsitools/internal/codec/jpeg"
)

func TestResolveTransformCodec(t *testing.T) {
	fac, knobs, name, err := resolveTransformCodec("", "", 90)
	if err != nil || fac == nil || name != "jpeg" || knobs["q"] != "90" {
		t.Fatalf("empty: fac=%v name=%q knobs=%v err=%v", fac, name, knobs, err)
	}
	if _, _, n, err := resolveTransformCodec("jpeg2000", "85", 90); err != nil || n != "jpeg2000" {
		t.Fatalf("jpeg2000: name=%q err=%v", n, err)
	}
	if _, _, _, err := resolveTransformCodec("nonexistent-codec", "", 90); err == nil {
		t.Fatal("expected error for unknown codec")
	}
}
