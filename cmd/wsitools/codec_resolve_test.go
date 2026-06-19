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
	// Regression: an explicit codecName="jpeg" with an EMPTY --quality must still
	// use fallbackQ (90), not the parseQualityKnobs default (85). The downsample
	// dispatch defaults codecName to "jpeg", so this is the byte-identity path.
	_, knobs, name, err = resolveTransformCodec("jpeg", "", 90)
	if err != nil || name != "jpeg" || knobs["q"] != "90" {
		t.Fatalf("jpeg+empty-quality must use fallbackQ=90: name=%q knobs=%v err=%v", name, knobs, err)
	}
	// fallbackQ applies to a non-jpeg codec too when --quality is absent.
	if _, knobs, _, err := resolveTransformCodec("jpeg2000", "", 90); err != nil || knobs["q"] != "90" {
		t.Fatalf("jpeg2000+empty-quality must use fallbackQ=90: knobs=%v err=%v", knobs, err)
	}
}
