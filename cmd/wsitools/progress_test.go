package main

import (
	"bytes"
	"testing"
)

// TestTileProgressNonTTYWritesNothing confirms a bar never emits progress
// escape-codes into a non-terminal sink. mpb itself suppresses output when the
// writer is not a terminal, and our progressEnabled() gate adds --quiet/TTY
// checks on top — so piped/CI output stays clean. (Live rendering on a real
// terminal is mpb's own, terminal-gated behaviour and can't be exercised against
// an in-memory buffer.)
func TestTileProgressNonTTYWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	// Even an "enabled" bar pointed at a buffer (non-terminal) writes nothing.
	bar := newTileProgressTo(&buf, true, "encoding", 4)
	for i := 0; i < 4; i++ {
		bar.Increment()
	}
	bar.Wait()
	if buf.Len() != 0 {
		t.Errorf("bar wrote %d bytes to a non-terminal sink, want 0: %q", buf.Len(), buf.String())
	}
}

// TestTileProgressDisabledIsNoOp confirms a disabled bar (quiet/non-TTY), a
// zero-total bar, and the zero value are all safe no-ops (the common path).
func TestTileProgressDisabledIsNoOp(t *testing.T) {
	for _, b := range []*progressBar{
		newTileProgressTo(nil, false, "x", 10), // disabled: out unused
		newTileProgressTo(nil, true, "x", 0),   // nothing to do
		{},                                     // zero value
	} {
		b.Increment() // must not panic
		b.Wait()      // must not panic
	}
}
