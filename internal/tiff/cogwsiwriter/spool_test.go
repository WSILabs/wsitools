package cogwsiwriter

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSpoolAppendAndReadBack(t *testing.T) {
	dir := t.TempDir()
	s, err := openSpool(filepath.Join(dir, "L0"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	payloads := [][]byte{
		[]byte("hello"),
		[]byte("world!"),
		bytes.Repeat([]byte{0xAB}, 1024),
	}
	for _, p := range payloads {
		if err := s.Append(p); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if len(s.Entries()) != len(payloads) {
		t.Fatalf("entries: got %d want %d", len(s.Entries()), len(payloads))
	}
	for i, e := range s.Entries() {
		if int(e.Length) != len(payloads[i]) {
			t.Errorf("entry %d length: got %d want %d", i, e.Length, len(payloads[i]))
		}
	}

	if err := s.Rewind(); err != nil {
		t.Fatal(err)
	}
	for i, want := range payloads {
		got := make([]byte, len(want))
		n, err := io.ReadFull(s, got)
		if err != nil {
			t.Fatalf("read entry %d: %v (n=%d)", i, err, n)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("entry %d bytes mismatch: got %x want %x", i, got, want)
		}
	}
}

func TestSpoolRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "L0")
	s, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Append([]byte("x"))
	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("spool file still exists after Remove: err=%v", err)
	}
}
