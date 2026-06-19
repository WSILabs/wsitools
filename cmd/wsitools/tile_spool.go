package main

import (
	"fmt"
	"io"
	"os"
)

// tileSpool is a disk-backed store of compressed tile frames for ONE pyramid
// level, addressable by row-major tile index. Frames are appended in arrival
// order (the engine emits out-of-order); the index maps tile-position →
// (offset,len), so get(idx) serves any tile regardless of write order. Reads use
// ReadAt on the still-open file, so get works before close. Single writer (the
// engine's one sink-drainer goroutine), so no locking.
type tileSpool struct {
	f     *os.File
	off   int64
	index []spoolEnt // len == tile count; n<0 marks "not written"
}

type spoolEnt struct {
	off int64
	n   int
}

func newTileSpool(path string, tiles int) (*tileSpool, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	idx := make([]spoolEnt, tiles)
	for i := range idx {
		idx[i].n = -1
	}
	return &tileSpool{f: f, index: idx}, nil
}

func (s *tileSpool) put(idx int, frame []byte) error {
	if idx < 0 || idx >= len(s.index) {
		return fmt.Errorf("tileSpool: index %d out of range [0,%d)", idx, len(s.index))
	}
	n, err := s.f.Write(frame)
	if err != nil {
		return err
	}
	s.index[idx] = spoolEnt{off: s.off, n: n}
	s.off += int64(n)
	return nil
}

func (s *tileSpool) get(idx int) ([]byte, error) {
	if idx < 0 || idx >= len(s.index) {
		return nil, fmt.Errorf("tileSpool: index %d out of range [0,%d)", idx, len(s.index))
	}
	e := s.index[idx]
	if e.n < 0 {
		return nil, fmt.Errorf("tileSpool: tile %d not written", idx)
	}
	buf := make([]byte, e.n)
	n, err := s.f.ReadAt(buf, e.off)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if n != e.n {
		return nil, fmt.Errorf("tileSpool: tile %d short read (%d of %d) — spool corrupt", idx, n, e.n)
	}
	return buf, nil
}

func (s *tileSpool) close() error  { return s.f.Close() }
func (s *tileSpool) remove() error { _ = s.f.Close(); return os.Remove(s.f.Name()) }
