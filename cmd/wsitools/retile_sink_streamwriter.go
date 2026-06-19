package main

import (
	"sync"

	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// streamwriterSink routes engine tiles to per-level streamwriter handles and
// runs the writer's ordered-drain protocol, one drain goroutine per level (the
// exact pattern in transcodeLevel). The handle's reorder buffer tolerates the
// engine's out-of-order delivery; the drain runs CONCURRENTLY with the engine or
// the bounded reorder buffer fills and WriteTile blocks. Implements
// retile.TileSink.
type streamwriterSink struct {
	handles  []*streamwriter.LevelHandle
	wg       sync.WaitGroup
	mu       sync.Mutex
	firstErr error
}

func newStreamwriterSink(handles []*streamwriter.LevelHandle) *streamwriterSink {
	s := &streamwriterSink{handles: handles}
	for _, h := range handles {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				idx, body, ok, err := h.NextReady()
				if err != nil {
					s.setErr(err)
					return
				}
				if !ok {
					return
				}
				if err := h.WriteTileAtIndex(idx, body); err != nil {
					h.Abort(err)
					s.setErr(err)
					return
				}
			}
		}()
	}
	return s
}

func (s *streamwriterSink) setErr(err error) {
	s.mu.Lock()
	if s.firstErr == nil {
		s.firstErr = err
	}
	s.mu.Unlock()
}

func (s *streamwriterSink) WriteTile(level, col, row int, encoded []byte) error {
	return s.handles[level].WriteTile(uint32(col), uint32(row), encoded)
}

// finish signals end-of-input to every level and waits for all drains. Returns
// the first drain error (if any). Call AFTER retile.Run returns.
func (s *streamwriterSink) finish() error {
	for _, h := range s.handles {
		h.CloseInput()
	}
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstErr
}
