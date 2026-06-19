package retile

import (
	"context"
	"testing"
	"time"
)

func TestEdgeTileDimsInteriorAndEdge(t *testing.T) {
	// 300-wide image, 256 tiles: col0 = 256, col1 = 44.
	if tw, _ := edgeTileDims(300, 300, 256, 0, 0); tw != 256 {
		t.Errorf("interior tw = %d, want 256", tw)
	}
	if tw, th := edgeTileDims(300, 300, 256, 1, 1); tw != 44 || th != 44 {
		t.Errorf("edge (tw,th) = (%d,%d), want (44,44)", tw, th)
	}
}

func TestLevelBuilderEmitsTilesForCompletedStrip(t *testing.T) {
	// 512×512 level, tile 256, overlap 1 → cols=2 rows=2. L_max builder, no child.
	jobs := make(chan encodeJob, 16)
	lb := &levelBuilder{
		spec: LevelSpec{Index: 1, Width: 512, Height: 512, Cols: 2, Rows: 2, TileW: 256, TileH: 256, Overlap: 1},
		jobs: jobs, ctx: context.Background(),
	}
	lb.feed(makeRGB(512, 256, 0))
	lb.feed(makeRGB(512, 256, 1))
	lb.flush()
	close(jobs)

	var n int
	rowsSeen := map[int]int{}
	for j := range jobs {
		n++
		rowsSeen[j.row]++
	}
	if n != 4 {
		t.Errorf("emitted %d tiles, want 4", n)
	}
	if rowsSeen[0] != 2 || rowsSeen[1] != 2 {
		t.Errorf("rows distribution: %v, want row0×2 + row1×2", rowsSeen)
	}
}

func TestLevelBuilderCascade(t *testing.T) {
	// L2 width 512 → L1 256 → L0 128; tile 256, overlap 0.
	jobs := make(chan encodeJob, 32)
	ctx := context.Background()
	coarsest := &levelBuilder{spec: LevelSpec{Index: 0, Width: 128, Height: 128, Cols: 1, Rows: 1, TileW: 256, TileH: 256}, jobs: jobs, ctx: ctx}
	mid := &levelBuilder{spec: LevelSpec{Index: 1, Width: 256, Height: 256, Cols: 1, Rows: 1, TileW: 256, TileH: 256}, child: coarsest, jobs: jobs, ctx: ctx}
	top := &levelBuilder{spec: LevelSpec{Index: 2, Width: 512, Height: 512, Cols: 2, Rows: 2, TileW: 256, TileH: 256}, child: mid, jobs: jobs, ctx: ctx}

	top.feed(makeRGB(512, 256, 1))
	top.feed(makeRGB(512, 256, 2))
	top.flush()
	close(jobs)

	counts := map[int]int{}
	for j := range jobs {
		counts[j.level]++
	}
	if counts[2] != 4 || counts[1] != 1 || counts[0] != 1 {
		t.Errorf("level tile counts = %v, want {2:4, 1:1, 0:1}", counts)
	}
}

func TestLevelBuilderIntermediateSkipsEmitButReduces(t *testing.T) {
	// L2 (512, emit) → L1 (256, INTERMEDIATE) → L0 (128, emit); tile 256, overlap 0.
	// The middle level must enqueue ZERO encodeJobs but still feed the coarsest.
	jobs := make(chan encodeJob, 32)
	ctx := context.Background()
	coarsest := &levelBuilder{spec: LevelSpec{Index: 0, Width: 128, Height: 128, Cols: 1, Rows: 1, TileW: 256, TileH: 256}, jobs: jobs, ctx: ctx}
	mid := &levelBuilder{spec: LevelSpec{Index: -1, Width: 256, Height: 256, Cols: 1, Rows: 1, TileW: 256, TileH: 256, Intermediate: true}, child: coarsest, jobs: jobs, ctx: ctx}
	top := &levelBuilder{spec: LevelSpec{Index: 1, Width: 512, Height: 512, Cols: 2, Rows: 2, TileW: 256, TileH: 256}, child: mid, jobs: jobs, ctx: ctx}

	top.feed(makeRGB(512, 256, 1))
	top.feed(makeRGB(512, 256, 2))
	top.flush()
	close(jobs)

	counts := map[int]int{}
	for j := range jobs {
		counts[j.level]++
	}
	if counts[1] != 4 {
		t.Errorf("top (emit) tiles = %d, want 4", counts[1])
	}
	if counts[-1] != 0 {
		t.Errorf("intermediate level emitted %d tiles, want 0", counts[-1])
	}
	if counts[0] != 1 {
		t.Errorf("coarsest (emit, fed through the intermediate) tiles = %d, want 1 (chain must still run)", counts[0])
	}
}

func TestLevelBuilderEmitRowRespectsContext(t *testing.T) {
	jobs := make(chan encodeJob) // zero-buffer: unconditional send blocks forever
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lb := &levelBuilder{
		spec: LevelSpec{Index: 1, Width: 512, Height: 256, Cols: 2, Rows: 1, TileW: 256, TileH: 256},
		cur:  makeRGB(512, 256, 0), jobs: jobs, ctx: ctx,
	}
	done := make(chan struct{})
	go func() { lb.emitRow(0); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("emitRow blocked despite cancelled context")
	}
	select {
	case j := <-jobs:
		t.Errorf("unexpected encodeJob delivered: level=%d col=%d", j.level, j.col)
	default:
	}
}
