package atomic

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.bin")
	err := WriteAtomic(target, func(w *os.File) error {
		_, e := w.Write([]byte("hello"))
		return e
	}, true)
	if err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("dir has %d entries, want 1 (no temp leftover)", len(ents))
	}
}

func TestWriteAtomicWriteErrorLeavesTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.bin")
	os.WriteFile(target, []byte("original"), 0o644)
	want := errors.New("boom")
	err := WriteAtomic(target, func(w *os.File) error { return want }, true)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want boom", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "original" {
		t.Fatalf("target modified: %q", got)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp file leftover: %d entries", len(ents))
	}
}
