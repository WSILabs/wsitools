package retile

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// stubEncoder encodes a tile to "w,h" bytes plus the first pixel — enough to
// prove the worker delivers each job's image to the sink intact.
type stubEncoder struct{}

func (stubEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	return []byte(fmt.Sprintf("%d,%d,%d", w, h, rgb[0])), nil
}

// captureSink records WriteTile calls.
type captureSink struct {
	mu   sync.Mutex
	rows []string
}

func (s *captureSink) WriteTile(level, col, row int, encoded []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, fmt.Sprintf("L%d:%d,%d=%s", level, col, row, string(encoded)))
	return nil
}

// failOnceSink fails the first WriteTile, then succeeds — to verify onTile is
// only called for SUCCESSFUL writes.
type failOnceSink struct{ failed bool }

func (s *failOnceSink) WriteTile(level, col, row int, encoded []byte) error {
	if !s.failed {
		s.failed = true
		return fmt.Errorf("boom")
	}
	return nil
}

func TestSinkDrainerOnTileHook(t *testing.T) {
	// 3 jobs; the sink fails the first write. onTile must fire only for the 2
	// successful writes.
	writes := make(chan writeJob, 3)
	for i := 0; i < 3; i++ {
		writes <- writeJob{level: 0, col: i, row: 0, body: []byte{byte(i)}}
	}
	close(writes)
	var firstErr error
	var n int
	sinkDrainer(writes, &failOnceSink{}, &firstErr, func() { n++ })
	if firstErr == nil {
		t.Fatal("expected the first write to error")
	}
	if n != 2 {
		t.Errorf("onTile called %d times, want 2 (only successful writes)", n)
	}
}

// nthErrorEncoder returns an error on the nth EncodeTile call (1-based).
type nthErrorEncoder struct {
	mu  sync.Mutex
	n   int
	cur int
}

func (e *nthErrorEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	e.mu.Lock()
	e.cur++
	cur := e.cur
	e.mu.Unlock()
	if cur == e.n {
		return nil, errors.New("boom")
	}
	return []byte{0x1}, nil
}

func TestEncoderWorkerPropagatesError(t *testing.T) {
	jobs := make(chan encodeJob, 8)
	out := make(chan writeJob, 8)
	var got error
	var once sync.Once
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	onErr := func(e error) { once.Do(func() { got = e; cancel() }) }

	done := make(chan struct{})
	go func() { encoderWorker(ctx, jobs, out, &nthErrorEncoder{n: 2}, onErr); close(done) }()

	// Drain any successful writeJobs so the worker never blocks on `out`.
	go func() {
		for range out {
		}
	}()
	jobs <- encodeJob{level: 0, col: 0, row: 0, img: makeRGB(4, 4, 0)}
	jobs <- encodeJob{level: 0, col: 1, row: 0, img: makeRGB(4, 4, 0)}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("encoderWorker did not return after encode error (hang)")
	}
	if got == nil || got.Error() != "boom" {
		t.Errorf("onErr got %v, want boom", got)
	}
}

func TestEncoderWorkerAndSinkRoundTrip(t *testing.T) {
	jobs := make(chan encodeJob, 8)
	writes := make(chan writeJob, 8)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); encoderWorker(context.Background(), jobs, writes, stubEncoder{}, func(error) {}) }()
	}
	for i := 0; i < 4; i++ {
		jobs <- encodeJob{level: 1, col: i, row: 0, img: makeRGB(64, 64, byte(i))}
	}
	close(jobs)
	go func() { wg.Wait(); close(writes) }()

	sink := &captureSink{}
	var firstErr error
	sinkDrainer(writes, sink, &firstErr, nil)
	if firstErr != nil {
		t.Fatalf("sink error: %v", firstErr)
	}
	sort.Strings(sink.rows)
	want := []string{"L1:0,0=64,64,0", "L1:1,0=64,64,1", "L1:2,0=64,64,2", "L1:3,0=64,64,3"}
	if len(sink.rows) != 4 {
		t.Fatalf("got %d writes, want 4: %v", len(sink.rows), sink.rows)
	}
	for i := range want {
		if sink.rows[i] != want[i] {
			t.Errorf("write[%d] = %q, want %q", i, sink.rows[i], want[i])
		}
	}
}
