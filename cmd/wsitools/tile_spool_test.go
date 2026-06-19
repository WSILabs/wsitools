package main

import (
	"bytes"
	"os"
	"testing"
)

func TestTileSpoolOutOfOrderPutGet(t *testing.T) {
	dir := t.TempDir()
	sp, err := newTileSpool(dir+"/L0", 4) // 4 tiles
	if err != nil {
		t.Fatal(err)
	}
	frames := map[int][]byte{0: []byte("aaa"), 1: []byte("bb"), 2: []byte("cccc"), 3: []byte("d")}
	for _, idx := range []int{2, 0, 3, 1} { // out of order
		if err := sp.put(idx, frames[idx]); err != nil {
			t.Fatalf("put %d: %v", idx, err)
		}
	}
	for idx, want := range frames {
		got, err := sp.get(idx) // read while spool still open (ReadAt on the open file)
		if err != nil {
			t.Fatalf("get %d: %v", idx, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("tile %d = %q, want %q", idx, got, want)
		}
	}
	sp2, _ := newTileSpool(dir+"/L1", 2)
	if _, err := sp2.get(0); err == nil {
		t.Error("expected error for unwritten tile")
	}
	if err := sp.close(); err != nil {
		t.Fatal(err)
	}
	if err := sp.remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/L0"); !os.IsNotExist(err) {
		t.Error("spool file not removed")
	}
	_ = sp2.remove()
}
