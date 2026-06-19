package retile

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
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

func TestEncoderWorkerAndSinkRoundTrip(t *testing.T) {
	jobs := make(chan encodeJob, 8)
	writes := make(chan writeJob, 8)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); encoderWorker(context.Background(), jobs, writes, stubEncoder{}) }()
	}
	for i := 0; i < 4; i++ {
		jobs <- encodeJob{level: 1, col: i, row: 0, img: makeRGB(64, 64, byte(i))}
	}
	close(jobs)
	go func() { wg.Wait(); close(writes) }()

	sink := &captureSink{}
	var firstErr error
	sinkDrainer(writes, sink, &firstErr)
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
