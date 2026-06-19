package main

import (
	"fmt"

	retile "github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

// cogwsiLevelReorder turns the engine's out-of-order per-level tile delivery into
// the strict row-major sequence cogwsiwriter.LevelHandle.WriteTile requires. It
// holds out-of-order tiles in a map keyed by row-major index and flushes the
// contiguous front. The engine drives one sink from a single goroutine, so no
// locking is needed. Memory stays ~O(workers): the engine emits each level
// roughly row-major, so few tiles are ever held.
type cogwsiLevelReorder struct {
	cols, rows int
	next       int // next row-major index to emit
	held       map[int][]byte
	write      func(tx, ty uint32, body []byte) error
}

func newCogwsiLevelReorder(cols, rows int, write func(tx, ty uint32, body []byte) error) *cogwsiLevelReorder {
	return &cogwsiLevelReorder{cols: cols, rows: rows, held: map[int][]byte{}, write: write}
}

func (r *cogwsiLevelReorder) submit(col, row int, body []byte) error {
	idx := row*r.cols + col
	if idx < r.next || idx >= r.cols*r.rows {
		return fmt.Errorf("cogwsi reorder: tile (%d,%d) idx %d out of range [%d,%d)", col, row, idx, r.next, r.cols*r.rows)
	}
	r.held[idx] = body
	for {
		b, ok := r.held[r.next]
		if !ok {
			break
		}
		col := r.next % r.cols
		row := r.next / r.cols
		if err := r.write(uint32(col), uint32(row), b); err != nil {
			return err
		}
		delete(r.held, r.next)
		r.next++
	}
	return nil
}

func (r *cogwsiLevelReorder) complete() bool { return r.next == r.cols*r.rows && len(r.held) == 0 }

// cogwsiSink routes engine tiles to per-level cogwsiwriter handles through a
// per-level row-major reorder. Implements retile.TileSink.
type cogwsiSink struct {
	reorders []*cogwsiLevelReorder
}

func newCogwsiSink(handles []*cogwsiwriter.LevelHandle, levels []retile.LevelSpec) *cogwsiSink {
	rs := make([]*cogwsiLevelReorder, len(handles))
	for i, h := range handles {
		rs[i] = newCogwsiLevelReorder(levels[i].Cols, levels[i].Rows, h.WriteTile)
	}
	return &cogwsiSink{reorders: rs}
}

func (s *cogwsiSink) WriteTile(level, col, row int, encoded []byte) error {
	if level < 0 || level >= len(s.reorders) {
		return fmt.Errorf("cogwsiSink: level %d out of range", level)
	}
	return s.reorders[level].submit(col, row, encoded)
}

// finish errors if any level did not receive every tile (a lost-tile guard).
func (s *cogwsiSink) finish() error {
	for i, r := range s.reorders {
		if !r.complete() {
			return fmt.Errorf("cogwsiSink: level %d incomplete (next=%d of %d)", i, r.next, r.cols*r.rows)
		}
	}
	return nil
}
