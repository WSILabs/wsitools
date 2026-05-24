package streamwriter

import (
	"bytes"
	"sync"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

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
	buf := newReorderBuffer(tileorder.RowMajor, W, H, 32)

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
