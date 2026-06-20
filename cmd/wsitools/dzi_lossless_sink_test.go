package main

import (
	"bytes"
	"testing"
)

type fakeDZISink struct{ calls map[[3]int][]byte }

func newFakeDZISink() *fakeDZISink { return &fakeDZISink{calls: map[[3]int][]byte{}} }
func (f *fakeDZISink) WriteTile(level, col, row int, body []byte) error {
	cp := make([]byte, len(body))
	copy(cp, body)
	f.calls[[3]int{level, col, row}] = cp
	return nil
}

type fakeTileReader struct {
	tile    []byte
	maxSize int
}

func (f *fakeTileReader) TileMaxSize() int { return f.maxSize }
func (f *fakeTileReader) TileInto(tx, ty int, dst []byte) (int, error) {
	return copy(dst, f.tile), nil
}

func TestLosslessDZISink_InteriorVerbatim_EdgeEngine(t *testing.T) {
	inner := newFakeDZISink()
	verbatim := []byte("VERBATIM-SOURCE-TILE")
	s := &losslessDZISink{
		inner: inner, src: &fakeTileReader{tile: verbatim, maxSize: 64},
		baseW: 300, baseH: 300, tileSize: 256,
	}
	enc := []byte("ENGINE-ENCODED")
	_ = s.WriteTile(0, 0, 0, enc) // interior → verbatim
	_ = s.WriteTile(0, 1, 0, enc) // edge (col 1: 256..300<512, partial) → engine
	_ = s.WriteTile(1, 0, 0, enc) // lower level → engine
	if got := inner.calls[[3]int{0, 0, 0}]; !bytes.Equal(got, verbatim) {
		t.Errorf("interior base: got %q want verbatim", got)
	}
	if got := inner.calls[[3]int{0, 1, 0}]; !bytes.Equal(got, enc) {
		t.Errorf("edge base: got %q want engine", got)
	}
	if got := inner.calls[[3]int{1, 0, 0}]; !bytes.Equal(got, enc) {
		t.Errorf("lower level: got %q want engine", got)
	}
}
