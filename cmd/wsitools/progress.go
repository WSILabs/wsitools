package main

import (
	"io"
	"os"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// progressEnabled reports whether an interactive progress bar should be drawn:
// not suppressed by --quiet, and stderr is an interactive terminal (so we never
// emit progress escape-codes into a pipe or CI log).
func progressEnabled() bool {
	return !flagQuiet && isTerminal(os.Stderr)
}

// isTerminal reports whether f is a character device (a terminal). Stdlib-only
// heuristic — avoids pulling in a TTY dependency.
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// progressBar is a thin, nil-safe wrapper over an mpb tile-count bar. When
// progress is disabled (quiet or non-TTY) every method is a no-op, so call sites
// never have to nil-check. Use it uniformly across every write path so the
// progress bar is consistent (and uniformly suppressed by --quiet).
type progressBar struct {
	p   *mpb.Progress
	bar *mpb.Bar
}

// newTileProgress builds a bar tracking `total` tiles/frames on stderr. label is
// the leading verb ("encoding", "copying", …). Returns a no-op bar when progress
// is disabled (--quiet or non-TTY) or total <= 0.
func newTileProgress(label string, total int64) *progressBar {
	return newTileProgressTo(os.Stderr, progressEnabled(), label, total)
}

// newTileProgressTo is the testable core of newTileProgress: it draws to out and
// is gated on the explicit `enabled` flag (so tests can force rendering to a
// buffer without a real terminal).
func newTileProgressTo(out io.Writer, enabled bool, label string, total int64) *progressBar {
	if !enabled || total <= 0 {
		return &progressBar{}
	}
	p := mpb.New(mpb.WithOutput(out))
	bar := p.AddBar(total,
		mpb.PrependDecorators(decor.Name(label+" "), decor.Percentage(decor.WCSyncSpace)),
		mpb.AppendDecorators(
			decor.EwmaSpeed(0, "%.0f tiles/s", 30),
			decor.Name(" ETA "),
			decor.EwmaETA(decor.ET_STYLE_GO, 30),
		),
	)
	return &progressBar{p: p, bar: bar}
}

// Increment advances the bar by one tile. Safe to call from a single goroutine
// (mpb bars are not concurrency-safe; the retile engine drives this from its one
// sink-drain goroutine, and the manual loops are single-threaded writers).
func (b *progressBar) Increment() {
	if b.bar != nil {
		b.bar.Increment()
	}
}

// Wait flushes and removes the bar. Always call once the writes are done.
func (b *progressBar) Wait() {
	if b.p != nil {
		b.p.Wait()
	}
}
