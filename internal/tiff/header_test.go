package tiff

import (
	"bytes"
	"testing"
)

// fakeWriterAt is a simple in-memory io.WriterAt for tests.
type fakeWriterAt struct {
	buf []byte
}

func (f *fakeWriterAt) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(f.buf) {
		newBuf := make([]byte, end)
		copy(newBuf, f.buf)
		f.buf = newBuf
	}
	copy(f.buf[off:end], p)
	return len(p), nil
}

func TestWriteHeaderClassic(t *testing.T) {
	f := &fakeWriterAt{}
	if err := WriteHeader(f, false, 0x1234); err != nil {
		t.Fatal(err)
	}
	want := []byte{
		'I', 'I',
		0x2A, 0x00,
		0x34, 0x12, 0x00, 0x00,
	}
	if !bytes.Equal(f.buf, want) {
		t.Errorf("classic header: got %x want %x", f.buf, want)
	}
}

func TestWriteHeaderBigTIFF(t *testing.T) {
	f := &fakeWriterAt{}
	if err := WriteHeader(f, true, 0x123456789A); err != nil {
		t.Fatal(err)
	}
	want := []byte{
		'I', 'I',
		0x2B, 0x00,
		0x08, 0x00,
		0x00, 0x00,
		0x9A, 0x78, 0x56, 0x34, 0x12, 0x00, 0x00, 0x00,
	}
	if !bytes.Equal(f.buf, want) {
		t.Errorf("BigTIFF header: got %x want %x", f.buf, want)
	}
}

func TestHeaderSize(t *testing.T) {
	if got := HeaderSize(false); got != 8 {
		t.Errorf("HeaderSize(classic): got %d want 8", got)
	}
	if got := HeaderSize(true); got != 16 {
		t.Errorf("HeaderSize(BigTIFF): got %d want 16", got)
	}
}
