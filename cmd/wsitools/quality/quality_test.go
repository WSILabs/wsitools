package quality_test

import (
	"errors"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
)

type fakeInspector struct {
	c    opentile.Compression
	info quality.Info
	err  error
}

func (f *fakeInspector) Compression() opentile.Compression                  { return f.c }
func (f *fakeInspector) Inspect(_ []byte) (quality.Info, error)             { return f.info, f.err }

func TestRegisterAndFor(t *testing.T) {
	want := quality.Info{Codec: "fake", QualityEstimate: 42}
	quality.Register(&fakeInspector{c: opentile.CompressionUnknown, info: want})

	got, ok := quality.For(opentile.CompressionUnknown)
	if !ok {
		t.Fatalf("For(CompressionUnknown): not registered")
	}
	gotInfo, err := got.Inspect(nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if gotInfo.QualityEstimate != 42 {
		t.Errorf("QualityEstimate: got %d, want 42", gotInfo.QualityEstimate)
	}
}

func TestForUnknownCompression(t *testing.T) {
	// Use a Compression value no test registers.
	_, ok := quality.For(opentile.Compression(99))
	if ok {
		t.Errorf("For(99): expected (nil, false)")
	}
}

func TestErrCorruptOrMismatch(t *testing.T) {
	// Smoke: the sentinel exists and is unique.
	if quality.ErrCorruptOrMismatch == nil {
		t.Fatal("ErrCorruptOrMismatch is nil")
	}
	if errors.Is(quality.ErrCorruptOrMismatch, errors.New("different error")) {
		t.Error("ErrCorruptOrMismatch incorrectly matches an unrelated error")
	}
}

func TestInfoZeroValue(t *testing.T) {
	var i quality.Info
	if i.Codec != "" || i.Lossless || i.QualityEstimate != 0 || i.ChromaSubsampling != "" || i.LayerCount != 0 || i.Notes != "" {
		t.Errorf("zero Info has non-zero fields: %+v", i)
	}
}
