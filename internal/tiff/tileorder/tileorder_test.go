package tileorder_test

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

func TestByNameUnknown(t *testing.T) {
	_, err := tileorder.ByName("does-not-exist")
	if err == nil {
		t.Fatalf("ByName(does-not-exist): expected error, got nil")
	}
}

func TestKnownNonEmpty(t *testing.T) {
	names := tileorder.Known()
	if len(names) == 0 {
		t.Fatalf("Known() returned no strategies")
	}
}
