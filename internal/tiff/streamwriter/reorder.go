package streamwriter

import (
	"sync"

	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// reorderBuffer is a bounded per-level buffer that reorders out-of-order
// tile submissions from N concurrent workers into a single strict
// strategy-defined emission sequence consumed by the Sink goroutine.
//
// Not exported; constructed and owned by LevelHandle.
type reorderBuffer struct {
	strategy tileorder.OrderStrategy
	tilesX   uint32
	tilesY   uint32
	total    uint32

	mu       sync.Mutex
	cond     *sync.Cond
	pending  map[uint32][]byte
	nextIdx  uint32
	emitted  uint32
	capacity uint32
	closed   bool  // CloseInput called → no more Submits expected
	err      error // sticky abort error
}

func newReorderBuffer(s tileorder.OrderStrategy, tilesX, tilesY, capacity uint32) *reorderBuffer {
	if capacity == 0 {
		capacity = 1
	}
	b := &reorderBuffer{
		strategy: s,
		tilesX:   tilesX,
		tilesY:   tilesY,
		total:    tilesX * tilesY,
		pending:  make(map[uint32][]byte, capacity),
		capacity: capacity,
	}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Submit adds a tile to the buffer. In the happy-path (T2.1) the buffer
// is unbounded — tiles are always accepted. Back-pressure (blocking when
// the buffer reaches capacity) is added in T2.2.
func (b *reorderBuffer) Submit(x, y uint32, compressed []byte) error {
	idx := b.strategy.Index(x, y, b.tilesX, b.tilesY)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	b.pending[idx] = compressed
	b.cond.Broadcast()
	return nil
}

// CloseInput signals that no further Submits will arrive. Workers
// SHOULD call this exactly once via the Sink-side adapter after the
// worker output channel drains. NextReady will continue returning
// pending tiles in order until all `total` are emitted.
func (b *reorderBuffer) CloseInput() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.cond.Broadcast()
}

// NextReady returns the next-in-strategy-order tile, blocking until it
// arrives. Returns (_, _, false, nil) when all tiles have been emitted.
// Returns (_, _, false, err) on Abort.
func (b *reorderBuffer) NextReady() (idx uint32, compressed []byte, ok bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if b.err != nil {
			return 0, nil, false, b.err
		}
		if b.emitted == b.total {
			return 0, nil, false, nil
		}
		if bytes, present := b.pending[b.nextIdx]; present {
			idx = b.nextIdx
			delete(b.pending, b.nextIdx)
			b.nextIdx++
			b.emitted++
			b.cond.Broadcast()
			return idx, bytes, true, nil
		}
		b.cond.Wait()
	}
}

// Abort flips a sticky error bit; pending Submits and NextReady return
// it immediately. Safe to call multiple times (first wins).
func (b *reorderBuffer) Abort(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.err = err
	}
	b.cond.Broadcast()
}
