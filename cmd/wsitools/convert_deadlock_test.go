package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestConvertSVSLargeNoDeadlock guards the tile-copy reorder-buffer
// deadlock: CMU-1.svs's L0 has ~29,800 tiles, far exceeding the
// streamwriter's 1024 reorder-buffer capacity. Without a concurrent
// drain, WriteTile blocks forever at tile ~1025. The conversion must
// complete (not time out).
func TestConvertSVSLargeNoDeadlock(t *testing.T) {
	bin := strippedBinary(t)
	src := strippedSample(t, "svs/CMU-1.svs")
	out := filepath.Join(t.TempDir(), "out.svs")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "convert", "--to", "svs", "-f", "-o", out, src)
	cmdOut, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("convert --to svs on a >1024-tile slide deadlocked (timed out):\n%s", cmdOut)
	}
	if err != nil {
		t.Fatalf("convert: %v\n%s", err, cmdOut)
	}
	if fi, e := os.Stat(out); e != nil || fi.Size() == 0 {
		t.Fatalf("output missing/empty: %v", e)
	}
}
