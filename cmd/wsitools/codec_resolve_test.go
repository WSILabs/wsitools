package main

import (
	"testing"

	_ "github.com/wsilabs/wsitools/internal/codec/jp2k"
	_ "github.com/wsilabs/wsitools/internal/codec/jpeg"
)

func TestResolveTransformCodec(t *testing.T) {
	// Empty codecName + empty quality → jpeg factory, per-codec default q=85.
	fac, knobs, name, err := resolveTransformCodec("", "")
	if err != nil || fac == nil || name != "jpeg" || knobs["q"] != "85" {
		t.Fatalf("empty: fac=%v name=%q knobs=%v err=%v", fac, name, knobs, err)
	}
	// Explicit --quality overrides the per-codec default.
	if _, _, n, err := resolveTransformCodec("jpeg2000", "85"); err != nil || n != "jpeg2000" {
		t.Fatalf("jpeg2000: name=%q err=%v", n, err)
	}
	if _, _, _, err := resolveTransformCodec("nonexistent-codec", ""); err == nil {
		t.Fatal("expected error for unknown codec")
	}
	// An explicit codecName="jpeg" with an EMPTY --quality uses the per-codec
	// default q=85 (not the old fallbackQ=90).
	_, knobs, name, err = resolveTransformCodec("jpeg", "")
	if err != nil || name != "jpeg" || knobs["q"] != "85" {
		t.Fatalf("jpeg+empty-quality must use per-codec default q=85: name=%q knobs=%v err=%v", name, knobs, err)
	}
	// A non-jpeg codec with empty --quality also uses the per-codec default (q=85).
	if _, knobs, _, err := resolveTransformCodec("jpeg2000", ""); err != nil || knobs["q"] != "85" {
		t.Fatalf("jpeg2000+empty-quality must use per-codec default q=85: knobs=%v err=%v", knobs, err)
	}
}

func TestCodecDefaultKnobs(t *testing.T) {
	for _, c := range []string{"jpeg", "jpeg2000", "htj2k", "avif", "webp"} {
		if got := codecDefaultKnobs(c); got["q"] != "85" {
			t.Errorf("%s default: %v, want q=85", c, got)
		}
	}
	if got := codecDefaultKnobs("jpegxl"); got["distance"] != "1.0" {
		t.Errorf("jpegxl default: %v, want distance=1.0", got)
	}
	if got := codecDefaultKnobs("png"); len(got) != 0 {
		t.Errorf("png default: %v, want empty", got)
	}
	if got := codecDefaultKnobs("unknown"); got["q"] != "85" {
		t.Errorf("unknown default: %v, want q=85", got)
	}
}

func TestQFromKnobs(t *testing.T) {
	if qFromKnobs(map[string]string{"q": "70"}) != 70 {
		t.Error("q=70")
	}
	if qFromKnobs(map[string]string{"distance": "1.0"}) != 85 {
		t.Error("no q → 85")
	}
	if qFromKnobs(map[string]string{}) != 85 {
		t.Error("empty → 85")
	}
	if qFromKnobs(map[string]string{"q": "999"}) != 85 {
		t.Error("out-of-range q → 85")
	}
}
