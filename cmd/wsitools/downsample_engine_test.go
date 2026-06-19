package main

import "testing"

// Compile-time: both M2 sinks satisfy retileSink.
var _ retileSink = (*streamwriterSink)(nil)
var _ retileSink = (*cogwsiSink)(nil)

// retileSinkFunc is a test-only retileSink backed by closures.
type retileSinkFunc struct {
	write    func(level, col, row int, b []byte) error
	finishFn func() error
}

func (f retileSinkFunc) WriteTile(level, col, row int, b []byte) error {
	return f.write(level, col, row, b)
}
func (f retileSinkFunc) finish() error { return f.finishFn() }

func TestCountingSinkForwardsAndCounts(t *testing.T) {
	rec := map[[3]int]int{}
	base := retileSinkFunc{
		write:    func(l, c, r int, b []byte) error { rec[[3]int{l, c, r}]++; return nil },
		finishFn: func() error { return nil },
	}
	var n int
	cs := &countingSink{inner: base, onWrite: func() { n++ }}
	if err := cs.WriteTile(0, 0, 0, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := cs.WriteTile(0, 1, 0, []byte{2}); err != nil {
		t.Fatal(err)
	}
	if err := cs.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if n != 2 {
		t.Errorf("onWrite called %d times, want 2", n)
	}
	if rec[[3]int{0, 0, 0}] != 1 || rec[[3]int{0, 1, 0}] != 1 {
		t.Errorf("inner did not receive both tiles: %v", rec)
	}
}
