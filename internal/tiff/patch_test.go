package tiff

import (
	"bytes"
	"testing"
)

func TestPatchUint32(t *testing.T) {
	f := &fakeWriterAt{buf: make([]byte, 16)}
	if err := PatchUint32(f, 4, 0xDEADBEEF); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 0, 0xEF, 0xBE, 0xAD, 0xDE, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(f.buf, want) {
		t.Errorf("got %x want %x", f.buf, want)
	}
}

func TestPatchUint64(t *testing.T) {
	f := &fakeWriterAt{buf: make([]byte, 16)}
	if err := PatchUint64(f, 0, 0x0123456789ABCDEF); err != nil {
		t.Fatal(err)
	}
	want := []byte{0xEF, 0xCD, 0xAB, 0x89, 0x67, 0x45, 0x23, 0x01, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(f.buf, want) {
		t.Errorf("got %x want %x", f.buf, want)
	}
}
