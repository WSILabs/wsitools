package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/vbauerster/mpb/v8/decor"
)

// TestTileSpeedDecoratorTracksPlainIncrements guards the "0 tiles/s ETA 0s"
// regression: our tile bars are advanced with bar.Increment() (which updates
// only the count), so the speed/ETA decorators must derive their reading from
// current progress + elapsed time, NOT from EWMA per-iteration samples (which
// bar.Increment never feeds). An Ewma speed decorator, given a bar that has made
// real progress but received no EwmaIncrement, reports a permanent "0 tiles/s";
// an Average one reports the true rate. This asserts the shared decorators are
// the Average kind by feeding the speed decorator a half-done Statistics and
// requiring a non-zero rate.
func TestTileSpeedDecoratorTracksPlainIncrements(t *testing.T) {
	speed := tileSpeedETADecorators()[0] // the "N tiles/s" decorator
	out, _ := speed.Decor(decor.Statistics{Total: 200, Current: 100})
	fields := strings.Fields(out) // "12345 tiles/s" (mpb may pad with spaces)
	if len(fields) == 0 {
		t.Fatalf("speed decorator produced empty output %q", out)
	}
	rate, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		t.Fatalf("speed decorator output %q: cannot parse rate %q: %v", out, fields[0], err)
	}
	if !(rate > 0) {
		t.Errorf("speed decorator rate = %v (output %q), want > 0 — a plain-incremented "+
			"bar reads 0 with an Ewma decorator (the bug); Average must report progress", rate, out)
	}
}

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
