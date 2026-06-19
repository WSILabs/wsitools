package main

import (
	"fmt"
	"testing"
)

// fakeRowMajorWriter records WriteTile calls and enforces strict row-major.
type fakeRowMajorWriter struct {
	cols, rows int
	got        []string
}

func (f *fakeRowMajorWriter) WriteTile(tx, ty uint32, body []byte) error {
	f.got = append(f.got, fmt.Sprintf("%d,%d:%s", tx, ty, string(body)))
	return nil
}

func TestCogwsiReorderEmitsRowMajor(t *testing.T) {
	// 3×2 grid (cols=3, rows=2), fed out of order; must emerge row-major.
	fw := &fakeRowMajorWriter{cols: 3, rows: 2}
	rb := newCogwsiLevelReorder(3, 2, fw.WriteTile)

	// Out-of-order submission (col,row) with body "<col><row>".
	order := [][2]int{{2, 0}, {0, 0}, {1, 1}, {1, 0}, {0, 1}, {2, 1}}
	for _, cr := range order {
		body := []byte(fmt.Sprintf("%d%d", cr[0], cr[1]))
		if err := rb.submit(cr[0], cr[1], body); err != nil {
			t.Fatalf("submit (%d,%d): %v", cr[0], cr[1], err)
		}
	}
	want := []string{"0,0:00", "1,0:10", "2,0:20", "0,1:01", "1,1:11", "2,1:21"}
	if len(fw.got) != len(want) {
		t.Fatalf("got %d writes, want %d: %v", len(fw.got), len(want), fw.got)
	}
	for i := range want {
		if fw.got[i] != want[i] {
			t.Errorf("write[%d] = %q, want %q (not strict row-major)", i, fw.got[i], want[i])
		}
	}
	if !rb.complete() {
		t.Errorf("reorder not complete after all tiles")
	}
}
