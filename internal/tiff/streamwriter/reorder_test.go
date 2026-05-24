package streamwriter

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// errSentinel is a test-only sentinel error used to test Abort propagation.
type errSentinel struct{ msg string }

func (e *errSentinel) Error() string { return e.msg }

func tileBytes(x, y uint32) []byte {
	return []byte{byte(x), byte(y), 0xAA}
}

func TestReorderBufferDrainsInStrategyOrder(t *testing.T) {
	const W, H = uint32(3), uint32(3)
	buf := newReorderBuffer(tileorder.RowMajor, W, H, 16)

	// Submit in reverse row-major order.
	for y := int32(H - 1); y >= 0; y-- {
		for x := int32(W - 1); x >= 0; x-- {
			if err := buf.Submit(uint32(x), uint32(y), tileBytes(uint32(x), uint32(y))); err != nil {
				t.Fatalf("Submit(%d,%d): %v", x, y, err)
			}
		}
	}
	buf.CloseInput()

	// Drain: expect row-major order.
	var got []struct{ idx uint32 }
	for {
		idx, b, ok, err := buf.NextReady()
		if err != nil {
			t.Fatalf("NextReady: %v", err)
		}
		if !ok {
			break
		}
		// Verify the bytes match what was submitted at the idx's (x,y).
		x, y := tileorder.RowMajor.IndexToXY(idx, W, H)
		want := tileBytes(x, y)
		if !bytes.Equal(b, want) {
			t.Errorf("idx=%d: got %v, want %v", idx, b, want)
		}
		got = append(got, struct{ idx uint32 }{idx})
	}
	if len(got) != int(W*H) {
		t.Fatalf("drained %d tiles, want %d", len(got), W*H)
	}
	for i, g := range got {
		if g.idx != uint32(i) {
			t.Errorf("drain[%d]: idx=%d, want %d", i, g.idx, i)
		}
	}
}

func TestReorderBufferConcurrentSubmit(t *testing.T) {
	const W, H = uint32(8), uint32(8)
	buf := newReorderBuffer(tileorder.RowMajor, W, H, W*H)

	var wg sync.WaitGroup
	for y := uint32(0); y < H; y++ {
		for x := uint32(0); x < W; x++ {
			wg.Add(1)
			go func(x, y uint32) {
				defer wg.Done()
				if err := buf.Submit(x, y, tileBytes(x, y)); err != nil {
					t.Errorf("Submit(%d,%d): %v", x, y, err)
				}
			}(x, y)
		}
	}
	wg.Wait()
	buf.CloseInput()

	for i := uint32(0); i < W*H; i++ {
		idx, b, ok, err := buf.NextReady()
		if err != nil {
			t.Fatalf("NextReady #%d: %v", i, err)
		}
		if !ok {
			t.Fatalf("NextReady #%d: ok=false too early", i)
		}
		if idx != i {
			t.Errorf("drain #%d: idx=%d", i, idx)
		}
		x, y := tileorder.RowMajor.IndexToXY(idx, W, H)
		if !bytes.Equal(b, tileBytes(x, y)) {
			t.Errorf("drain #%d: bytes mismatch at (%d,%d)", i, x, y)
		}
	}
	_, _, ok, err := buf.NextReady()
	if err != nil {
		t.Errorf("trailing NextReady: %v", err)
	}
	if ok {
		t.Errorf("trailing NextReady: ok=true, want false (drained)")
	}
}

// TestReorderBufferBackPressureBlocksSubmit verifies that Submit blocks when
// the buffer is full and the submitted tile is not the head-of-queue.
func TestReorderBufferBackPressureBlocksSubmit(t *testing.T) {
	// capacity=1, 2×1 grid: idx 0 is head, idx 1 is non-head.
	// Submit idx=1 (non-head, buffer now at capacity).
	// Submit idx=1 again must block until NextReady drains idx=0 first,
	// making room. We verify blocking by racing against a short timer.
	const W, H = uint32(2), uint32(1)
	buf := newReorderBuffer(tileorder.RowMajor, W, H, 1)

	// Fill the buffer with idx=1 (non-head tile).
	if err := buf.Submit(1, 0, tileBytes(1, 0)); err != nil {
		t.Fatalf("first Submit(1,0): %v", err)
	}

	// Now the buffer is at capacity (1 pending, capacity=1) and nextIdx=0,
	// so a second Submit of any non-head tile must block.
	blocked := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(blocked)
		// Submit idx=1 again — should block until space opens.
		_ = buf.Submit(1, 0, tileBytes(1, 0))
		close(done)
	}()

	<-blocked
	select {
	case <-done:
		t.Fatal("Submit returned immediately; back-pressure did not block")
	case <-time.After(50 * time.Millisecond):
		// Good: Submit is still blocking.
	}

	// Submit the head tile (idx=0) to unblock NextReady, then drain idx=0
	// to free capacity for the blocked goroutine.
	if err := buf.Submit(0, 0, tileBytes(0, 0)); err != nil {
		t.Fatalf("Submit(0,0): %v", err)
	}
	_, _, ok, err := buf.NextReady()
	if !ok || err != nil {
		t.Fatalf("NextReady for idx=0: ok=%v err=%v", ok, err)
	}

	select {
	case <-done:
		// Good: blocked Submit unblocked after space freed.
	case <-time.After(2 * time.Second):
		t.Fatal("Submit did not unblock after NextReady freed capacity")
	}
}

// TestReorderBufferAbortReturnsError verifies that Abort unblocks a
// waiting Submit and that all subsequent calls return the abort error.
func TestReorderBufferAbortReturnsError(t *testing.T) {
	const W, H = uint32(2), uint32(1)
	buf := newReorderBuffer(tileorder.RowMajor, W, H, 1)
	sentinel := &errSentinel{"injected abort"}

	// Fill to capacity with a non-head tile so the next Submit blocks.
	if err := buf.Submit(1, 0, tileBytes(1, 0)); err != nil {
		t.Fatalf("Submit(1,0): %v", err)
	}

	errCh := make(chan error, 1)
	blocked := make(chan struct{})
	go func() {
		close(blocked)
		// This Submit should block then return the abort error.
		errCh <- buf.Submit(1, 0, tileBytes(1, 0))
	}()

	<-blocked
	time.Sleep(20 * time.Millisecond) // let goroutine reach cond.Wait
	buf.Abort(sentinel)

	select {
	case got := <-errCh:
		if !errors.Is(got, sentinel) {
			t.Errorf("Submit after Abort: got %v, want sentinel", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Submit did not unblock after Abort")
	}

	// NextReady must also return the abort error.
	_, _, _, err := buf.NextReady()
	if !errors.Is(err, sentinel) {
		t.Errorf("NextReady after Abort: got %v, want sentinel", err)
	}
}
