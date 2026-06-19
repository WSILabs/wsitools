# Streaming Retile Engine (SP2) — Milestone 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the DZI pyramid-descent pipeline (`cmd/wsitools/convert_dzi_descent.go`) into a reusable, format-agnostic `internal/retile` streaming engine behind a `TileSink`/`TileEncoder` seam, and re-point `convert --to dzi|szi` at it with **pixel/byte-identical output** (zero behavior change).

**Architecture:** One `ScaledStrips` iterator feeds a chain of per-level builders (finest→coarsest, fixed 2× box descent), which emit RGB tiles to a worker-pool encoder, which hands compressed bytes to a serialized sink drainer. The engine is codec- and container-agnostic: callers supply a `TileEncoder` (RGB→bytes) and a `TileSink` (routes `WriteTile(level,col,row)` to a writer). The DZI/SZI drivers supply a jpeg-standalone/PNG encoder and a thin adapter over `dzi.Writer`/`szi.Writer`.

**Tech Stack:** Go; opentile-go `ScaledStrips`/`resample`; `internal/codec/jpeg` (libjpeg-turbo, cgo); existing `internal/dzi`/`internal/szi` writers.

**Scope:** This plan covers **Milestone 1 only** (engine extraction + DZI/SZI parity). Milestones 2–5 (cogwsi/streamwriter sinks + stitched-source routing, downsample, transcode, lossy crop) are sequenced in the roadmap at the end and become their own plans once M1 locks the engine API. Per the SP2 spec (`docs/superpowers/specs/2026-06-18-retile-engine-sp2-design.md`), each milestone is independently shippable.

---

## Why this scoping

The engine's encoder pool delivers tiles to the serialized sink **out of grid order** (multiple encoder goroutines finish at different times). The three target writers have sharply different ordering contracts:

- `dzi.Writer.WriteTile(level,col,row,body)` — tolerates **arbitrary** order (dedup map + random-access file paths). The engine's current `sinkDrainer` already relies on this.
- `cogwsiwriter.LevelHandle.WriteTile(tx,ty)` — requires **strict row-major** order starting `(0,0)`, no reorder buffer.
- `streamwriter.LevelHandle.WriteTile(x,y)` — has a reorder buffer but needs the `CloseInput`/`NextReady`/`WriteTileAtIndex` drain goroutine wired up.

M1 targets only `dzi`/`szi` (arbitrary-order tolerant), so the extraction is a **pure refactor** with a strong parity oracle. The cogwsi/streamwriter reorder design is an M2 concern, deliberately deferred so it can be designed against the *locked* engine API rather than a moving one.

## Key parity constraint (read before Task 7)

DZI tiles are **self-contained JPEGs** (`SOI+DQT+DHT+SOS+scan+EOI`), produced today by `jpeg.Encoder.EncodeStandalone` — **not** the abbreviated `codec.Encoder.EncodeTile` (which omits the DQT/DHT tables for TIFF tag-347 sharing). DZI also supports PNG output (`cfg.Format=="png"`, stdlib `png.Encode`), which is not a `codec.Encoder` at all.

**Therefore the engine takes a thin `retile.TileEncoder` seam (`EncodeTile(rgb []byte, w, h int) ([]byte, error)`), NOT `codec.Encoder`.** This is a deliberate refinement of the SP2 spec's literal `Encoder codec.Encoder` field: a thinner seam serves both DZI-standalone/PNG and (in M2) TIFF-family abbreviated+header encoders. The DZI driver supplies an adapter that reproduces today's `encodeTile` byte-for-byte.

## Engine level-index convention (read before Task 5/6/7)

`ComputeLevels` returns levels **finest-first** with `Index == k` (k=0 is the finest/native-resolution level, k=last is the coarsest). The engine calls `sink.WriteTile(k, col, row, body)` with this engine-relative `k`. **The sink owns format-specific numbering:** the DZI sink maps engine level `k` → DZI level number `(len(levels)-1) - k` (DZI numbers coarsest=0, native=MaxLevel). This keeps the engine fully format-agnostic and pushes DZI's inverted numbering into the one place that knows about it.

---

## File Structure

**New package `internal/retile`:**
- `internal/retile/image.go` — `RGBImage` + the `rgbPixPool` borrow/release helpers (moved from `convert_dzi_descent.go`).
- `internal/retile/downsample.go` — `boxDownsample2x` (moved verbatim).
- `internal/retile/level.go` — `LevelSpec`, the `levelBuilder` (parameterized by `LevelSpec` instead of `dzi.Config`), `assembleTile`, `copyRGBBand`, `edgeTileDims`.
- `internal/retile/encode.go` — `TileEncoder` interface, `encodeJob`/`writeJob`, `encoderWorker`, `sinkDrainer`.
- `internal/retile/retile.go` — `TileSink`, `Spec`, `Run`, `decoderImageToRGB`.
- `internal/retile/levels.go` — `ComputeLevels`.
- `internal/retile/*_test.go` — unit tests (ported + new).

**Modified (Task 7):**
- `cmd/wsitools/convert_dzi.go` — `dziWriterSink` adapter + `dziStandaloneEncoder` adapter; re-point `emitDZIPyramid` at `retile.Run`.
- `cmd/wsitools/convert_dzi_descent.go` — **deleted** (its content now lives in `internal/retile`).
- `cmd/wsitools/convert_dzi_descent_test.go` — **deleted** (tests ported to `internal/retile`).

The split is by responsibility (image buffers / downsample / level math / encode wiring / orchestration / level computation), each file small enough to hold in context.

---

## Task 1: Scaffold `internal/retile` with core public types

**Files:**
- Create: `internal/retile/image.go`
- Create: `internal/retile/doc.go`

- [ ] **Step 1: Create the package doc file**

Create `internal/retile/doc.go`:

```go
// Package retile is the streaming retile engine: one opentile ScaledStrips
// iterator feeds a chain of per-level builders (finest→coarsest, fixed 2× box
// descent) that emit RGB tiles to a worker-pool encoder, whose compressed
// output is handed to a serialized sink. The engine is codec- and
// container-agnostic — callers supply a TileEncoder (RGB→bytes) and a TileSink
// (routes WriteTile(level,col,row) to a writer). It generalizes the DZI
// pyramid-descent that originally lived in cmd/wsitools/convert_dzi_descent.go.
//
// Level numbering: the engine works finest-first with engine-relative index k
// (k=0 = finest). Sinks translate k to their container's numbering.
package retile
```

- [ ] **Step 2: Create `image.go` (RGBImage + pool)**

Create `internal/retile/image.go` — this is the `rgbImage`/pool block from `convert_dzi_descent.go:29-64`, exported as `RGBImage`:

```go
package retile

import "sync"

// RGBImage is a 3-byte-per-pixel RGB image. Pixel (x,y) lives at
// Pix[y*Stride + x*3]. Source pixels arrive from opentile-go as RGB and
// libjpeg-turbo accepts RGB, so the pipeline carries RGB throughout.
type RGBImage struct {
	Pix    []byte // length = H * Stride
	Stride int    // = W * 3
	W, H   int
}

// rgbPixPool pools the backing []byte for RGBImage tile destinations produced
// by assembleTile and consumed by the encoder. Ownership: assembleTile borrows;
// the encoder releases after EncodeTile returns.
var rgbPixPool = sync.Pool{
	New: func() any { b := make([]byte, 0, 258*258*3); return &b },
}

// newPooledRGB returns an *RGBImage whose Pix is borrowed from rgbPixPool.
// Callers must release via releaseRGB after the last read of Pix. The borrowed
// slice is not zeroed — callers must fully overwrite every byte.
func newPooledRGB(w, h int) *RGBImage {
	need := w * h * 3
	b := *(rgbPixPool.Get().(*[]byte))
	if cap(b) < need {
		b = make([]byte, need)
	} else {
		b = b[:need]
	}
	return &RGBImage{Pix: b, Stride: w * 3, W: w, H: h}
}

// releaseRGB returns img.Pix to the pool. Caller must not reference img after.
func releaseRGB(img *RGBImage) {
	b := img.Pix[:0]
	rgbPixPool.Put(&b)
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/retile/`
Expected: success (no output).

- [ ] **Step 4: Commit**

```bash
git add internal/retile/doc.go internal/retile/image.go
git commit -m "feat(retile): scaffold internal/retile package + RGBImage pool"
```

---

## Task 2: Port `boxDownsample2x` with tests

**Files:**
- Create: `internal/retile/downsample.go`
- Create: `internal/retile/downsample_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/retile/downsample_test.go`:

```go
package retile

import "testing"

// makeRGB returns an *RGBImage of size w×h where pixel (x,y) =
// (id, byte(x), byte(y)) — encodes id+col+row for traceable assertions.
func makeRGB(w, h int, id byte) *RGBImage {
	img := &RGBImage{Pix: make([]byte, w*h*3), Stride: w * 3, W: w, H: h}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*img.Stride + x*3
			img.Pix[i+0] = id
			img.Pix[i+1] = byte(x)
			img.Pix[i+2] = byte(y)
		}
	}
	return img
}

func TestBoxDownsample2xHalvesDimensions(t *testing.T) {
	dst := boxDownsample2x(makeRGB(8, 8, 0xAA))
	if dst.W != 4 || dst.H != 4 {
		t.Errorf("dst dims: %dx%d, want 4x4", dst.W, dst.H)
	}
}

func TestBoxDownsample2xAverages2x2(t *testing.T) {
	src := &RGBImage{Pix: make([]byte, 4*4*3), Stride: 4 * 3, W: 4, H: 4}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			i := y*src.Stride + x*3
			src.Pix[i+0] = byte(y * 64)
			src.Pix[i+1] = byte(x * 64)
			src.Pix[i+2] = 100
		}
	}
	dst := boxDownsample2x(src)
	if dst.W != 2 || dst.H != 2 {
		t.Fatalf("dst dims: %dx%d, want 2x2", dst.W, dst.H)
	}
	i := 0
	if rr, gg, bb := dst.Pix[i+0], dst.Pix[i+1], dst.Pix[i+2]; rr != 32 || gg != 32 || bb != 100 {
		t.Errorf("dst(0,0) = R=%d G=%d B=%d; want R=32 G=32 B=100", rr, gg, bb)
	}
}

func TestBoxDownsample2xOddDimsRoundUp(t *testing.T) {
	dst := boxDownsample2x(makeRGB(9, 7, 0x11))
	if dst.W != 5 || dst.H != 4 {
		t.Errorf("dst dims: %dx%d, want 5x4 (ceil(9/2)×ceil(7/2))", dst.W, dst.H)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/retile/ -run TestBoxDownsample2x -v`
Expected: FAIL — `undefined: boxDownsample2x`.

- [ ] **Step 3: Implement `boxDownsample2x`**

Create `internal/retile/downsample.go` (moved verbatim from `convert_dzi_descent.go:508-542`, retyped to `*RGBImage`):

```go
package retile

// boxDownsample2x reduces an RGBImage by 2× in both dimensions via 2×2 box
// averaging. Odd-dimension sources treat the last row/column as duplicated, so
// the output is ceil(srcH/2) × ceil(srcW/2).
func boxDownsample2x(src *RGBImage) *RGBImage {
	srcW, srcH := src.W, src.H
	if srcW == 0 || srcH == 0 {
		return &RGBImage{Pix: []byte{}, Stride: 0, W: 0, H: 0}
	}
	w := (srcW + 1) / 2
	h := (srcH + 1) / 2
	dst := newPooledRGB(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx0, sy0 := x*2, y*2
			sx1 := sx0 + 1
			if sx1 >= srcW {
				sx1 = srcW - 1
			}
			sy1 := sy0 + 1
			if sy1 >= srcH {
				sy1 = srcH - 1
			}
			i0 := sy0*src.Stride + sx0*3
			i1 := sy0*src.Stride + sx1*3
			i2 := sy1*src.Stride + sx0*3
			i3 := sy1*src.Stride + sx1*3
			r := (uint32(src.Pix[i0+0]) + uint32(src.Pix[i1+0]) + uint32(src.Pix[i2+0]) + uint32(src.Pix[i3+0])) / 4
			g := (uint32(src.Pix[i0+1]) + uint32(src.Pix[i1+1]) + uint32(src.Pix[i2+1]) + uint32(src.Pix[i3+1])) / 4
			b := (uint32(src.Pix[i0+2]) + uint32(src.Pix[i1+2]) + uint32(src.Pix[i2+2]) + uint32(src.Pix[i3+2])) / 4
			di := y*dst.Stride + x*3
			dst.Pix[di+0] = byte(r)
			dst.Pix[di+1] = byte(g)
			dst.Pix[di+2] = byte(b)
		}
	}
	return dst
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/retile/ -run TestBoxDownsample2x -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/retile/downsample.go internal/retile/downsample_test.go
git commit -m "feat(retile): port boxDownsample2x + tests"
```

---

## Task 3: Port `levelBuilder` parameterized by `LevelSpec`

This is the heart of the descent. The only change from the original is replacing `dzi.Config` (which carried `Width`/`Height`/`TileSize`/`Overlap`/`Format` for the whole pyramid) with a per-level `LevelSpec`, and inlining `dzi.EdgeTileDims` as `edgeTileDims`.

**Files:**
- Create: `internal/retile/level.go`
- Create: `internal/retile/level_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/retile/level_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/retile/ -run 'TestEdgeTileDims|TestLevelBuilder' -v`
Expected: FAIL — `undefined: levelBuilder`, `undefined: edgeTileDims`, `undefined: encodeJob`, `undefined: LevelSpec`.

- [ ] **Step 3: Implement `level.go`**

Create `internal/retile/level.go`. This is `levelBuilder` + helpers from `convert_dzi_descent.go:79-277`, with `cfg dzi.Config` replaced by `spec LevelSpec` and `dzi.EdgeTileDims` inlined as `edgeTileDims`. Note `encodeJob` is declared in Task 4's `encode.go`; this task's test only needs the type to exist, so declare it here is wrong — instead, Task 4 must precede compilation. **To keep this task self-contained and compiling, add a minimal `encodeJob` here and move it to `encode.go` in Task 4.** Simpler: declare `encodeJob` in `level.go` now (it is consumed by `emitRow`), and Task 4 adds `writeJob`/workers without redeclaring it.

```go
package retile

import "context"

// LevelSpec is one output pyramid level. The engine works finest-first; Index
// is engine-relative (0 = finest). Sinks translate Index to container numbering.
type LevelSpec struct {
	Index         int
	Width, Height int // level pixel dims
	Cols, Rows    int // tile grid
	TileW, TileH  int
	Overlap       int // 0 for TIFF-family/cog-wsi; 1 for DZI
}

// edgeTileDims returns the content dimensions of tile (col,row) for a w×h image
// with the given tile size. Interior tiles return (tileSize,tileSize); right/
// bottom edge tiles return the truncated remainder. Overlap is added separately.
// Inlined from internal/dzi.EdgeTileDims to keep the engine dzi-free.
func edgeTileDims(w, h, tileSize, col, row int) (int, int) {
	tw := tileSize
	if (col+1)*tileSize > w {
		tw = w - col*tileSize
	}
	th := tileSize
	if (row+1)*tileSize > h {
		th = h - row*tileSize
	}
	return tw, th
}

// encodeJob hands a rendered tile to the encoder pool. level is the engine-
// relative level index (LevelSpec.Index).
type encodeJob struct {
	level, col, row int
	img             *RGBImage
}

// levelBuilder builds tiles for one pyramid level. The chain is linked via
// child; each level downsamples its strips into the next coarser level. The
// finest builder receives strips from the ScaledStrips iterator; the coarsest
// has child=nil.
type levelBuilder struct {
	spec LevelSpec

	// Rolling 3-strip overlap buffer.
	prev, cur, next *RGBImage

	// Accumulator for the strip currently being filled from downsampled parent
	// rows. nil for the finest builder (the iterator feeds full strips).
	accum     *RGBImage
	accumRows int

	rowIndex int // next tile row to emit

	child *levelBuilder
	jobs  chan<- encodeJob
	ctx   context.Context
}

// feed accepts one strip at this level's resolution: rotate the buffer, emit the
// now-complete row, and downsample 2× to the child.
func (lb *levelBuilder) feed(strip *RGBImage) {
	lb.prev = lb.cur
	lb.cur = lb.next
	lb.next = strip
	if lb.cur != nil {
		lb.emitRow(lb.rowIndex)
		lb.rowIndex++
	}
	if lb.child != nil {
		lb.child.acceptDownsampled(boxDownsample2x(strip))
	}
}

// acceptDownsampled buffers a half-height parent strip until it has accumulated
// a full strip at this level's resolution, then promotes it via feed.
func (lb *levelBuilder) acceptDownsampled(half *RGBImage) {
	if lb.accum == nil {
		lb.accum = &RGBImage{
			Pix:    make([]byte, lb.spec.Width*lb.spec.TileH*3),
			Stride: lb.spec.Width * 3,
			W:      lb.spec.Width,
			H:      lb.spec.TileH,
		}
	}
	halfH, halfW := half.H, half.W
	for y := 0; y < halfH; y++ {
		if lb.accumRows+y >= lb.spec.TileH {
			break
		}
		srcRow := half.Pix[y*half.Stride : y*half.Stride+halfW*3]
		dstRow := lb.accum.Pix[(lb.accumRows+y)*lb.accum.Stride : (lb.accumRows+y)*lb.accum.Stride+halfW*3]
		copy(dstRow, srcRow)
	}
	lb.accumRows += halfH
	if lb.accumRows >= lb.spec.TileH {
		completed := lb.accum
		lb.accum = nil
		lb.accumRows = 0
		lb.feed(completed)
	}
}

// flush emits remaining rows after the source iterator finishes, then cascades.
func (lb *levelBuilder) flush() {
	if lb.accum != nil && lb.accumRows > 0 {
		short := &RGBImage{
			Pix:    make([]byte, lb.spec.Width*lb.accumRows*3),
			Stride: lb.spec.Width * 3,
			W:      lb.spec.Width,
			H:      lb.accumRows,
		}
		for y := 0; y < lb.accumRows; y++ {
			copy(short.Pix[y*short.Stride:], lb.accum.Pix[y*lb.accum.Stride:y*lb.accum.Stride+lb.spec.Width*3])
		}
		lb.feed(short)
		lb.accum = nil
		lb.accumRows = 0
	}
	lb.prev = lb.cur
	lb.cur = lb.next
	lb.next = nil
	if lb.cur != nil && lb.rowIndex < lb.spec.Rows {
		lb.emitRow(lb.rowIndex)
		lb.rowIndex++
	}
	if lb.child != nil {
		lb.child.flush()
	}
}

// emitRow slices cur into the row's tiles and enqueues encodeJobs.
func (lb *levelBuilder) emitRow(row int) {
	for col := 0; col < lb.spec.Cols; col++ {
		tile := lb.assembleTile(col, row)
		select {
		case lb.jobs <- encodeJob{level: lb.spec.Index, col: col, row: row, img: tile}:
		case <-lb.ctx.Done():
			releaseRGB(tile)
			return
		}
	}
}

// assembleTile builds the RGB tile for (col,row) from the rolling buffer with
// overlap rules.
func (lb *levelBuilder) assembleTile(col, row int) *RGBImage {
	lw := lb.spec.Width
	cw, ch := edgeTileDims(lw, lb.spec.Rows*lb.spec.TileH, lb.spec.TileH, col, row)
	if lb.cur != nil && lb.cur.H < lb.spec.TileH {
		ch = lb.cur.H
	}
	leftOv := lb.spec.Overlap
	if col == 0 {
		leftOv = 0
	}
	topOv := lb.spec.Overlap
	if row == 0 || lb.prev == nil {
		topOv = 0
	}
	rightOv := lb.spec.Overlap
	if col*lb.spec.TileW+cw >= lw {
		rightOv = 0
	}
	bottomOv := lb.spec.Overlap
	if lb.next == nil {
		bottomOv = 0
	}
	outW := cw + leftOv + rightOv
	outH := ch + topOv + bottomOv

	dst := newPooledRGB(outW, outH)
	srcX0 := col*lb.spec.TileW - leftOv
	if topOv > 0 && lb.prev != nil {
		copyRGBBand(dst, 0, lb.prev, lb.prev.H-topOv, srcX0, outW, topOv)
	}
	if lb.cur != nil {
		copyRGBBand(dst, topOv, lb.cur, 0, srcX0, outW, ch)
	}
	if bottomOv > 0 && lb.next != nil {
		copyRGBBand(dst, topOv+ch, lb.next, 0, srcX0, outW, bottomOv)
	}
	return dst
}

// copyRGBBand copies a horizontal band between two RGBImages. Out-of-range
// source columns are white-filled (defensive against overlap-rule errors).
func copyRGBBand(dst *RGBImage, dstY int, src *RGBImage, srcY, srcX, w, h int) {
	srcW := src.W
	for r := 0; r < h; r++ {
		dstRowStart := (dstY + r) * dst.Stride
		srcRowStart := (srcY + r) * src.Stride
		for c := 0; c < w; c++ {
			sx := srcX + c
			var rr, gg, bb byte
			if sx >= 0 && sx < srcW {
				si := srcRowStart + sx*3
				rr, gg, bb = src.Pix[si+0], src.Pix[si+1], src.Pix[si+2]
			} else {
				rr, gg, bb = 0xFF, 0xFF, 0xFF
			}
			di := dstRowStart + c*3
			dst.Pix[di+0], dst.Pix[di+1], dst.Pix[di+2] = rr, gg, bb
		}
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/retile/ -run 'TestEdgeTileDims|TestLevelBuilder' -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add internal/retile/level.go internal/retile/level_test.go
git commit -m "feat(retile): port levelBuilder parameterized by LevelSpec"
```

---

## Task 4: Port the encoder/sink worker wiring behind `TileEncoder`/`TileSink`

**Files:**
- Create: `internal/retile/encode.go`
- Create: `internal/retile/encode_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/retile/encode_test.go`. A stub `TileEncoder` (no cgo) proves the worker/sink wiring; it returns a deterministic body so we can assert routing.

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/retile/ -run TestEncoderWorkerAndSinkRoundTrip -v`
Expected: FAIL — `undefined: writeJob`, `undefined: encoderWorker`, `undefined: sinkDrainer`, `undefined: TileEncoder`, `undefined: TileSink`.

- [ ] **Step 3: Implement `encode.go`**

Create `internal/retile/encode.go` (ports `writeJob`/`encoderWorker`/`sinkDrainer` from `convert_dzi_descent.go:73-76,279-353`, with the encoder behind `TileEncoder` and `cfg`/`format`/`quality` removed — the encoder owns format/quality). `TileSink` is declared here:

```go
package retile

import "context"

// TileEncoder encodes one RGB tile (w×h, 3 bytes/px, stride w*3) to a
// self-contained compressed body. Implementations MUST be safe for concurrent
// EncodeTile calls (the engine shares one encoder across worker goroutines).
type TileEncoder interface {
	EncodeTile(rgb []byte, w, h int) ([]byte, error)
}

// TileSink receives encoded output tiles. level is the engine-relative level
// index (LevelSpec.Index); the sink translates it to its container numbering.
// The engine emits tiles for multiple levels INTERLEAVED and, within a level,
// out of grid order (the encoder pool finishes tiles out of order); a sink whose
// writer requires ordering must buffer/reorder internally.
type TileSink interface {
	WriteTile(level, col, row int, encoded []byte) error
}

// writeJob hands encoded bytes to the sink-drain goroutine.
type writeJob struct {
	level, col, row int
	body            []byte
}

// encoderWorker pulls encodeJobs, encodes via enc, and pushes writeJobs. The
// tile's RGB buffer is released back to the pool after encoding. Exits cleanly
// when jobs is closed or ctx is cancelled.
func encoderWorker(ctx context.Context, jobs <-chan encodeJob, out chan<- writeJob, enc TileEncoder) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			body, err := enc.EncodeTile(job.img.Pix, job.img.W, job.img.H)
			releaseRGB(job.img)
			if err != nil {
				return
			}
			select {
			case out <- writeJob{level: job.level, col: job.col, row: job.row, body: body}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// sinkDrainer pulls writeJobs and calls sink.WriteTile serially. Stores the
// first error in *firstErr; subsequent errors are dropped.
func sinkDrainer(jobs <-chan writeJob, sink TileSink, firstErr *error) {
	for job := range jobs {
		if err := sink.WriteTile(job.level, job.col, job.row, job.body); err != nil {
			if *firstErr == nil {
				*firstErr = err
			}
		}
	}
}
```

Note: the original `encodeTile` released the RGB *inside* the format switch; here the engine releases it in `encoderWorker` after `EncodeTile` returns, so the `TileEncoder` only reads the bytes. This preserves the pool-ownership invariant (the encoder must not retain `rgb` past return).

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/retile/ -run TestEncoderWorkerAndSinkRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/retile/encode.go internal/retile/encode_test.go
git commit -m "feat(retile): port encoder/sink workers behind TileEncoder/TileSink"
```

---

## Task 5: Implement `Run` (orchestration: ScaledStrips → chain → pool → sink)

**Files:**
- Create: `internal/retile/retile.go`
- Create: `internal/retile/retile_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/retile/retile_test.go`. This drives `Run` through a real `*opentile.Slide` over a fixture (gated), verifying the engine produces the expected tile counts per level and decodable bodies. Uses the existing fixture helper convention (`WSI_TOOLS_TESTDIR`, default `./sample_files`).

```go
package retile

import (
	"context"
	"image"
	stdjpeg "image/jpeg"
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/decoder/all"
	_ "github.com/wsilabs/opentile-go/formats/all"
	"github.com/wsilabs/opentile-go/resample"
)

func testdir() string {
	if d := os.Getenv("WSI_TOOLS_TESTDIR"); d != "" {
		return d
	}
	return "../../sample_files"
}

// countingSink records, per engine level, the set of (col,row) written and that
// each body is a decodable JPEG of the expected size.
type countingSink struct {
	mu       sync.Mutex
	perLevel map[int]int
	t        *testing.T
}

func (s *countingSink) WriteTile(level, col, row int, encoded []byte) error {
	img, err := stdjpeg.Decode(bytes.NewReader(encoded))
	if err != nil {
		s.t.Errorf("L%d (%d,%d): body not a decodable JPEG: %v", level, col, row, err)
		return nil
	}
	_ = img
	s.mu.Lock()
	s.perLevel[level]++
	s.mu.Unlock()
	return nil
}

func TestRunEmitsFullOctavePyramid(t *testing.T) {
	path := filepath.Join(testdir(), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	slide, err := opentile.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer slide.Close()

	l0 := slide.Pyramids()[0].Levels[0]
	srcW, srcH := l0.Size.W, l0.Size.H
	const ts = 256
	levels := ComputeLevels(opentile.Size{W: srcW, H: srcH}, ts, ts, 1 /*overlap*/, 2 /*ratio*/, octaveCount(srcW, srcH))

	sink := &countingSink{perLevel: map[int]int{}, t: t}
	err = Run(context.Background(), Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: opentile.Size{W: srcW, H: srcH}},
		OutL0:     opentile.Size{W: srcW, H: srcH},
		Levels:    levels,
		Kernel:    resample.Nearest, // identity scale (out == src)
		Encoder:   stubJPEG{},
		Sink:      sink,
		Workers:   4,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Finest level (k=0) must have Cols*Rows tiles.
	wantFinest := levels[0].Cols * levels[0].Rows
	if sink.perLevel[0] != wantFinest {
		t.Errorf("finest level tiles = %d, want %d", sink.perLevel[0], wantFinest)
	}
	// Coarsest level (k=last) is 1×1 → exactly 1 tile.
	last := len(levels) - 1
	if sink.perLevel[last] != 1 {
		t.Errorf("coarsest level tiles = %d, want 1", sink.perLevel[last])
	}
}

// octaveCount returns the number of octave levels from native down to 1×1.
func octaveCount(w, h int) int {
	m := w
	if h > m {
		m = h
	}
	n := 1
	for m > 1 {
		m = (m + 1) / 2
		n++
	}
	return n
}

// stubJPEG produces a tiny valid JPEG via the stdlib encoder so the test needs
// no cgo. (The production path uses libjpeg-turbo via the driver's adapter.)
type stubJPEG struct{}

func (stubJPEG) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w*3 + x*3
			o := img.PixOffset(x, y)
			img.Pix[o+0], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = rgb[i+0], rgb[i+1], rgb[i+2], 0xFF
		}
	}
	var b bytes.Buffer
	if err := stdjpeg.Encode(&b, img, &stdjpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/retile/ -run TestRunEmitsFullOctavePyramid -v`
Expected: FAIL — `undefined: Run`, `undefined: Spec`, `undefined: ComputeLevels` (ComputeLevels lands in Task 6; this task may show that undefined too — that is fine, both are implemented before the final green run. If you prefer strict per-task green, implement Task 6 first; the two tasks are order-independent).

- [ ] **Step 3: Implement `retile.go`**

Create `internal/retile/retile.go` (ports `runDescent` + `decoderImageToRGB` from `convert_dzi_descent.go:355-502`, driven by `Spec.Levels`):

```go
package retile

import (
	"context"
	"io"
	"runtime"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/opentile-go/resample"
)

// Spec configures one streaming retile pass.
type Spec struct {
	Slide     *opentile.Slide
	SrcRegion opentile.Region // L0-coord source rect (full slide, or a crop)
	OutL0     opentile.Size   // output L0 dims (= Levels[0] dims)
	Levels    []LevelSpec     // output pyramid, finest first (Levels[0] = OutL0 resolution)
	Kernel    resample.Kernel // strip resample kernel (caller picks Nearest at identity, Box on downscale)
	Encoder   TileEncoder
	Sink      TileSink
	Workers   int
}

// Run executes the pass: ScaledStrips → level-builder chain (2× box descent) →
// encoder pool → sink. One L0 decode; memory bounded by the rolling strip
// buffers. Returns the first error from any stage. Requires an octave pyramid
// (each Levels[k+1] ≈ Levels[k]/2); ComputeLevels with levelRatio=2 produces one.
func Run(ctx context.Context, spec Spec) error {
	workers := spec.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if len(spec.Levels) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	encodeJobs := make(chan encodeJob, 2*workers)
	writeJobs := make(chan writeJob, 2*workers)

	// Build the level chain finest→coarsest. Levels[0] is the finest (fed by the
	// iterator); each subsequent is the box-downsampled child.
	builders := make([]*levelBuilder, len(spec.Levels))
	for i := range spec.Levels {
		builders[i] = &levelBuilder{spec: spec.Levels[i], jobs: encodeJobs, ctx: ctx}
		if i > 0 {
			builders[i-1].child = builders[i]
		}
	}
	top := builders[0]

	var encWG sync.WaitGroup
	for i := 0; i < workers; i++ {
		encWG.Add(1)
		go func() {
			defer encWG.Done()
			encoderWorker(ctx, encodeJobs, writeJobs, spec.Encoder)
		}()
	}

	var sinkWG sync.WaitGroup
	var sinkErr error
	sinkWG.Add(1)
	go func() {
		defer sinkWG.Done()
		sinkDrainer(writeJobs, spec.Sink, &sinkErr)
	}()

	stripOpts := []opentile.StripOption{
		opentile.WithStripContext(ctx),
		opentile.WithStripKernel(spec.Kernel),
	}
	if workers > 0 {
		stripOpts = append(stripOpts, opentile.WithStripWorkers(workers))
	}
	it := spec.Slide.Pyramid(0).ScaledStrips(spec.SrcRegion, spec.OutL0, spec.Levels[0].TileH, stripOpts...)
	defer it.Close()

	var srcErr error
	for {
		img, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			srcErr = err
			cancel()
			break
		}
		top.feed(decoderImageToRGB(img))
	}
	if srcErr == nil {
		top.flush()
	}

	close(encodeJobs)
	encWG.Wait()
	close(writeJobs)
	sinkWG.Wait()

	if srcErr != nil {
		return srcErr
	}
	return sinkErr
}

// decoderImageToRGB returns an *RGBImage view (zero-copy when src is already
// RGB) or an alpha-stripped copy. Safe to alias src.Pix: ScaledStrips allocates
// a fresh *decoder.Image per Next() and top.feed processes it synchronously
// before the next Next().
func decoderImageToRGB(img *decoder.Image) *RGBImage {
	if img.Format == decoder.PixelFormatRGB {
		return &RGBImage{Pix: img.Pix, Stride: img.Stride, W: img.Width, H: img.Height}
	}
	dst := newPooledRGB(img.Width, img.Height)
	for y := 0; y < img.Height; y++ {
		for x := 0; x < img.Width; x++ {
			si := y*img.Stride + x*4
			di := y*dst.Stride + x*3
			dst.Pix[di+0], dst.Pix[di+1], dst.Pix[di+2] = img.Pix[si+0], img.Pix[si+1], img.Pix[si+2]
		}
	}
	return dst
}
```

Add `"sync"` to the import block (used by the WaitGroups).

- [ ] **Step 4: Run the test to verify it passes**

Run (after Task 6 lands `ComputeLevels`): `go test ./internal/retile/ -run TestRunEmitsFullOctavePyramid -v`
Expected: PASS, or SKIP if the fixture is absent. To run with fixtures: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/retile/ -run TestRunEmitsFullOctavePyramid -v` and confirm PASS (not SKIP).

- [ ] **Step 5: Commit**

```bash
git add internal/retile/retile.go internal/retile/retile_test.go
git commit -m "feat(retile): implement Run orchestration over Spec.Levels"
```

---

## Task 6: Implement `ComputeLevels`

**Files:**
- Create: `internal/retile/levels.go`
- Create: `internal/retile/levels_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/retile/levels_test.go`:

```go
package retile

import (
	"testing"

	opentile "github.com/wsilabs/opentile-go"
)

func TestComputeLevelsOctaveDownToOnePx(t *testing.T) {
	// 512×512, tile 256, overlap 1, ratio 2, 10 levels → finest=512, coarsest=1×1.
	got := ComputeLevels(opentile.Size{W: 512, H: 512}, 256, 256, 1, 2, 10)
	if len(got) != 10 {
		t.Fatalf("levels = %d, want 10", len(got))
	}
	if got[0].Index != 0 || got[0].Width != 512 || got[0].Height != 512 {
		t.Errorf("finest = %+v, want Index0 512×512", got[0])
	}
	if got[0].Cols != 2 || got[0].Rows != 2 {
		t.Errorf("finest grid = %d×%d, want 2×2", got[0].Cols, got[0].Rows)
	}
	if got[0].Overlap != 1 {
		t.Errorf("overlap = %d, want 1", got[0].Overlap)
	}
	last := got[9]
	if last.Index != 9 || last.Width != 1 || last.Height != 1 || last.Cols != 1 || last.Rows != 1 {
		t.Errorf("coarsest = %+v, want Index9 1×1 grid1×1", last)
	}
}

func TestComputeLevelsCeilHalvingMatchesDZI(t *testing.T) {
	// Odd dims exercise the ceil-halving identity ceil(ceil(n/2)/2)==ceil(n/4).
	got := ComputeLevels(opentile.Size{W: 300, H: 200}, 256, 256, 1, 2, 4)
	want := [][2]int{{300, 200}, {150, 100}, {75, 50}, {38, 25}}
	for i, w := range want {
		if got[i].Width != w[0] || got[i].Height != w[1] {
			t.Errorf("level %d = %d×%d, want %d×%d", i, got[i].Width, got[i].Height, w[0], w[1])
		}
	}
}

func TestComputeLevelsStopsAtDegenerateDim(t *testing.T) {
	// Asking for more levels than the image supports stops at 1×1.
	got := ComputeLevels(opentile.Size{W: 4, H: 4}, 256, 256, 0, 2, 10)
	if len(got) != 3 {
		t.Fatalf("levels = %d, want 3 (4→2→1)", len(got))
	}
	if got[len(got)-1].Width != 1 || got[len(got)-1].Height != 1 {
		t.Errorf("coarsest = %d×%d, want 1×1", got[len(got)-1].Width, got[len(got)-1].Height)
	}
}

func TestComputeLevelsArbitraryTileSizeAndOverlap(t *testing.T) {
	got := ComputeLevels(opentile.Size{W: 1024, H: 768}, 512, 384, 0, 2, 2)
	if got[0].Cols != 2 || got[0].Rows != 2 {
		t.Errorf("finest grid = %d×%d, want 2×2 (ceil(1024/512)×ceil(768/384))", got[0].Cols, got[0].Rows)
	}
	if got[0].TileW != 512 || got[0].TileH != 384 {
		t.Errorf("tile = %d×%d, want 512×384", got[0].TileW, got[0].TileH)
	}
	if got[0].Overlap != 0 {
		t.Errorf("overlap = %d, want 0", got[0].Overlap)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/retile/ -run TestComputeLevels -v`
Expected: FAIL — `undefined: ComputeLevels`.

- [ ] **Step 3: Implement `levels.go`**

Create `internal/retile/levels.go`:

```go
package retile

import opentile "github.com/wsilabs/opentile-go"

// ComputeLevels derives an output pyramid from the L0 dims, tiling, and
// ratio/count. Levels are finest-first; level k has dims ceil(outL0 / ratio^k)
// (ceil-halving per octave when levelRatio==2). Index == k (engine-relative;
// 0 = finest). The descent stops early if a dimension reaches 1 before
// levelCount is met, so the returned slice may be shorter than levelCount.
//
// levelRatio is currently 2 (octave); the engine's 2× box descent only realizes
// octave pyramids. Other ratios compute correct geometry but are not yet
// produced by Run (reserved for SP3 --level-ratio).
func ComputeLevels(outL0 opentile.Size, tileW, tileH, overlap, levelRatio, levelCount int) []LevelSpec {
	if levelRatio < 2 {
		levelRatio = 2
	}
	levels := make([]LevelSpec, 0, levelCount)
	w, h := outL0.W, outL0.H
	for k := 0; k < levelCount; k++ {
		cols := (w + tileW - 1) / tileW
		rows := (h + tileH - 1) / tileH
		levels = append(levels, LevelSpec{
			Index: k, Width: w, Height: h,
			Cols: cols, Rows: rows, TileW: tileW, TileH: tileH, Overlap: overlap,
		})
		if w <= 1 && h <= 1 {
			break
		}
		w = ceilDiv(w, levelRatio)
		h = ceilDiv(h, levelRatio)
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
	}
	return levels
}

func ceilDiv(n, d int) int {
	if n <= 0 {
		return 1
	}
	return (n + d - 1) / d
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/retile/ -run TestComputeLevels -v`
Expected: PASS (all four). Then run the whole package: `go test ./internal/retile/ -v` → PASS (or fixture SKIP for `TestRunEmitsFullOctavePyramid`).

- [ ] **Step 5: Commit**

```bash
git add internal/retile/levels.go internal/retile/levels_test.go
git commit -m "feat(retile): ComputeLevels (octave pyramid geometry)"
```

---

## Task 7: Re-point DZI/SZI drivers at the engine; delete the old descent

This is the swap. The DZI driver gains two small adapters — a `TileEncoder` reproducing today's `encodeTile` (jpeg-standalone / PNG) and a `TileSink` over `dziTileSink` that maps engine level `k` → DZI level `maxLevel-k` — then calls `retile.Run`. The old `convert_dzi_descent.go` and its test are deleted.

**Files:**
- Modify: `cmd/wsitools/convert_dzi.go` (replace `emitDZIPyramid`'s body; add adapters)
- Delete: `cmd/wsitools/convert_dzi_descent.go`
- Delete: `cmd/wsitools/convert_dzi_descent_test.go`

- [ ] **Step 1: Write the failing test**

Add to a new file `cmd/wsitools/convert_dzi_sink_test.go` — proves the DZI level-index mapping (engine k → DZI level) and the encoder adapter's format switch, in package `main` (no fixture needed):

```go
package main

import (
	"bytes"
	stdjpeg "image/jpeg"
	"image/png"
	"testing"
)

// recordingDZISink captures (level,col,row) routed through the adapter.
type recordingDZISink struct{ writes []string }

func (s *recordingDZISink) WriteTile(level, col, row int, body []byte) error {
	s.writes = append(s.writes, sprintfLevel(level, col, row))
	return nil
}

func TestDZIWriterSinkMapsEngineLevelToDZINumber(t *testing.T) {
	rec := &recordingDZISink{}
	// 10 engine levels (k=0 finest .. k=9 coarsest). Engine k=0 → DZI 9; k=9 → DZI 0.
	sink := newDZIWriterSink(rec, 10)
	if err := sink.WriteTile(0, 1, 2, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteTile(9, 0, 0, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if rec.writes[0] != sprintfLevel(9, 1, 2) {
		t.Errorf("engine k=0 → %q, want DZI level 9", rec.writes[0])
	}
	if rec.writes[1] != sprintfLevel(0, 0, 0) {
		t.Errorf("engine k=9 → %q, want DZI level 0", rec.writes[1])
	}
}

func TestDZIStandaloneEncoderJPEGIsSelfContained(t *testing.T) {
	enc, err := newDZIStandaloneEncoder("jpeg", 85)
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}
	defer enc.Close()
	rgb := make([]byte, 16*16*3)
	for i := range rgb {
		rgb[i] = 128
	}
	body, err := enc.EncodeTile(rgb, 16, 16)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := stdjpeg.Decode(bytes.NewReader(body)); err != nil {
		t.Errorf("DZI JPEG must be self-contained (stdlib-decodable): %v", err)
	}
}

func TestDZIStandaloneEncoderPNG(t *testing.T) {
	enc, err := newDZIStandaloneEncoder("png", 0)
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}
	defer enc.Close()
	rgb := make([]byte, 8*8*3)
	body, err := enc.EncodeTile(rgb, 8, 8)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("png decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
		t.Errorf("png dims = %v, want 8×8", b)
	}
}
```

Add a tiny shared helper `sprintfLevel` in the same test file:

```go
import "fmt"

func sprintfLevel(level, col, row int) string { return fmt.Sprintf("%d:%d,%d", level, col, row) }
```

(Combine the two `import` blocks into one when you create the file.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/wsitools/ -run 'TestDZIWriterSink|TestDZIStandaloneEncoder' -v`
Expected: FAIL — `undefined: newDZIWriterSink`, `undefined: newDZIStandaloneEncoder`.

- [ ] **Step 3: Add the adapters and rewrite `emitDZIPyramid`**

In `cmd/wsitools/convert_dzi.go`, replace the imports and the `emitDZIPyramid` function. First, update the import block to drop nothing and add the engine + jpeg + resample + png deps:

```go
import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/resample"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/dzi"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
)
```

Replace `emitDZIPyramid` (lines 98-104) with the engine-driven version plus the two adapters:

```go
// emitDZIPyramid drives the streaming retile engine to fill the DZI/SZI tile
// tree. srcW/srcH are the source L0 dimensions; cfg.Width/Height are the
// (possibly --factor-reduced) output dimensions. The engine scales the source
// region to the output and descends the octave pyramid.
func emitDZIPyramid(ctx context.Context, slide *opentile.Slide, w dziTileSink, cfg dzi.Config, srcW, srcH int) error {
	levels := retile.ComputeLevels(
		opentile.Size{W: cfg.Width, H: cfg.Height},
		cfg.TileSize, cfg.TileSize, cfg.Overlap,
		2 /*octave*/, dziOctaveCount(cfg.Width, cfg.Height),
	)
	enc, err := newDZIStandaloneEncoder(cfg.Format, parseDZIQuality(cvQuality))
	if err != nil {
		return err
	}
	defer enc.Close()

	// Nearest at identity scale (no --factor) — matches the profiled fast path;
	// Box (area-averaging) when the top read is a real downscale.
	kernel := resample.Nearest
	if srcW != cfg.Width || srcH != cfg.Height {
		kernel = resample.Box
	}

	return retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: opentile.Size{W: srcW, H: srcH}},
		OutL0:     opentile.Size{W: cfg.Width, H: cfg.Height},
		Levels:    levels,
		Kernel:    kernel,
		Encoder:   enc,
		Sink:      newDZIWriterSink(w, len(levels)),
		Workers:   cvWorkers,
	})
}

// dziOctaveCount returns the number of DZI levels (native down to 1×1).
func dziOctaveCount(w, h int) int { return dzi.MaxLevel(w, h) + 1 }

// dziWriterSink adapts a dziTileSink (dzi.Writer/szi.Writer) to retile.TileSink.
// The engine numbers levels finest-first (k=0); DZI numbers them coarsest-first
// (level 0 = 1×1, level MaxLevel = native). With nLevels engine levels, engine k
// maps to DZI level (nLevels-1) - k.
type dziWriterSink struct {
	w       dziTileSink
	nLevels int
}

func newDZIWriterSink(w dziTileSink, nLevels int) *dziWriterSink {
	return &dziWriterSink{w: w, nLevels: nLevels}
}

func (s *dziWriterSink) WriteTile(level, col, row int, encoded []byte) error {
	return s.w.WriteTile(s.nLevels-1-level, col, row, encoded)
}

// dziStandaloneEncoder reproduces the old encodeTile: self-contained JPEG via
// libjpeg-turbo (EncodeStandalone) or stdlib PNG. Implements retile.TileEncoder.
type dziStandaloneEncoder struct {
	format string
	jpeg   *jpeg.Encoder // nil for png
}

func newDZIStandaloneEncoder(format string, quality int) (*dziStandaloneEncoder, error) {
	switch format {
	case "jpeg":
		enc, err := jpeg.New(
			codec.LevelGeometry{},
			codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}},
		)
		if err != nil {
			return nil, fmt.Errorf("jpeg.New: %w", err)
		}
		return &dziStandaloneEncoder{format: "jpeg", jpeg: enc}, nil
	case "png":
		return &dziStandaloneEncoder{format: "png"}, nil
	default:
		return nil, fmt.Errorf("unsupported dzi format %q", format)
	}
}

func (e *dziStandaloneEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	switch e.format {
	case "jpeg":
		return e.jpeg.EncodeStandalone(rgb, w, h)
	case "png":
		var b bytes.Buffer
		if err := png.Encode(&b, &rgbBytesAsImage{pix: rgb, stride: w * 3, w: w, h: h}); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	default:
		return nil, fmt.Errorf("unsupported dzi format %q", e.format)
	}
}

func (e *dziStandaloneEncoder) Close() error {
	if e.jpeg != nil {
		return e.jpeg.Close()
	}
	return nil
}

// rgbBytesAsImage wraps a raw RGB byte buffer as image.Image for stdlib PNG.
// Reports NRGBA with alpha hard-coded to 255 (opaque), matching the old
// rgbImageAsImage so PNG output is logically identical.
type rgbBytesAsImage struct {
	pix    []byte
	stride int
	w, h   int
}

func (r *rgbBytesAsImage) ColorModel() color.Model { return color.NRGBAModel }
func (r *rgbBytesAsImage) Bounds() image.Rectangle { return image.Rect(0, 0, r.w, r.h) }
func (r *rgbBytesAsImage) At(x, y int) color.Color {
	i := y*r.stride + x*3
	return color.NRGBA{R: r.pix[i+0], G: r.pix[i+1], B: r.pix[i+2], A: 0xFF}
}
```

- [ ] **Step 4: Delete the old descent and its test**

```bash
git rm cmd/wsitools/convert_dzi_descent.go cmd/wsitools/convert_dzi_descent_test.go
```

- [ ] **Step 5: Build and run the new unit tests**

Run: `go build ./... && go test ./cmd/wsitools/ -run 'TestDZIWriterSink|TestDZIStandaloneEncoder' -v`
Expected: build clean; tests PASS.

If the build reports unused symbols in `cmd/wsitools` that previously lived only in `convert_dzi_descent.go` (e.g. `parseDZIQuality` is still used; confirm nothing else dangles), resolve by confirming each deleted symbol had no other caller: `grep -rn 'rgbImage\|boxDownsample2x\|levelBuilder\|runDescent\|encoderWorker\|sinkDrainer\|decoderImageToRGB\|encodeTile\b' cmd/wsitools/*.go` should return only the new `convert_dzi.go` adapters (no references to the deleted internals). `parseDZIQuality` remains in `convert_dzi.go` and is still referenced.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert_dzi.go cmd/wsitools/convert_dzi_sink_test.go
git commit -m "refactor(convert): route DZI/SZI through internal/retile engine"
```

---

## Task 8: Parity gate — byte-identical DZI/SZI output (controller-run)

The extraction is a pure refactor; given the same libjpeg-turbo, output must be **byte-identical**. This task captures a reference tree from pre-swap `main` and diffs the post-swap output. **The controller runs these steps** (needs the local fixture + a built binary); they are not committed tests.

**Files:** none (verification only).

- [ ] **Step 1: Capture the reference output from pre-swap `main`**

From a clean checkout of `main` at the commit *before* Task 7 (use a throwaway worktree so the feature branch is untouched):

```bash
git worktree add /tmp/retile-ref-main main
( cd /tmp/retile-ref-main && make build )
FIX="$(pwd)/sample_files/svs/CMU-1-Small-Region.svs"
/tmp/retile-ref-main/bin/wsitools convert "$FIX" --to dzi  -o /tmp/ref.dzi   --force
/tmp/retile-ref-main/bin/wsitools convert "$FIX" --to szi  -o /tmp/ref.szi   --force
```

Expected: both conversions succeed; `/tmp/ref_files/` and `/tmp/ref.szi` exist.

- [ ] **Step 2: Build the feature branch and generate the new output**

```bash
make build
./bin/wsitools convert "$FIX" --to dzi -o /tmp/new.dzi --force
./bin/wsitools convert "$FIX" --to szi -o /tmp/new.szi --force
```

Expected: both succeed.

- [ ] **Step 3: Diff the DZI tree byte-for-byte**

```bash
diff /tmp/ref.dzi /tmp/new.dzi && echo "MANIFEST OK"
diff -r /tmp/ref_files /tmp/new_files && echo "TILES OK"
```

Expected: no diff output; `MANIFEST OK` and `TILES OK` printed. A non-empty diff means the refactor changed output — STOP and use systematic-debugging (compare a single mismatching tile's bytes; the most likely culprits are the kernel selection at identity scale, the level-index mapping, or the encoder quality knob).

- [ ] **Step 4: Diff the SZI archive contents**

```bash
mkdir -p /tmp/ref_szi /tmp/new_szi
( cd /tmp/ref_szi && unzip -oq /tmp/ref.szi )
( cd /tmp/new_szi && unzip -oq /tmp/new.szi )
diff -r /tmp/ref_szi /tmp/new_szi && echo "SZI OK"
```

Expected: no diff; `SZI OK`. (The ZIP container bytes themselves may differ in ordering/timestamps; compare the *unpacked* contents, which must be identical.)

- [ ] **Step 5: Run the full unit + race suite for the touched packages**

Run: `go test -race -count=1 ./internal/retile/ ./cmd/wsitools/ -timeout 30m`
Expected: PASS. (cmd/wsitools is heavy under `-race`; the 30m timeout avoids the false-FAIL noted in CLAUDE.md.)

To exercise the fixture-gated engine test as well:
Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/retile/ -run TestRunEmitsFullOctavePyramid -v`
Expected: PASS (not SKIP).

- [ ] **Step 6: Clean up the reference worktree**

```bash
git worktree remove /tmp/retile-ref-main
rm -rf /tmp/ref.dzi /tmp/ref_files /tmp/ref.szi /tmp/new.dzi /tmp/new_files /tmp/new.szi /tmp/ref_szi /tmp/new_szi
```

- [ ] **Step 7: Commit the milestone marker (optional)**

No code change; this task is a gate. If a doc note is desired, none is required — proceed to finishing-a-development-branch.

---

## Final verification (after all tasks)

Dispatch a final code-reviewer over the whole `internal/retile` package + `convert_dzi.go` diff, then use **superpowers:finishing-a-development-branch**. The branch should be `feat/retile-engine-m1` off `main`.

Acceptance for M1:
- `internal/retile` compiles and its unit suite is green (downsample, level builder, encode wiring, Run, ComputeLevels).
- `convert --to dzi` and `--to szi` produce **byte-identical** output to pre-swap `main` (Task 8 gate).
- `cmd/wsitools` race suite green.
- `convert_dzi_descent.go` is gone; no dangling references.

---

## Subsequent milestones (M2–M5) — roadmap, each its own plan

M1 locks the engine API (`Spec`/`LevelSpec`/`TileEncoder`/`TileSink`/`Run`/`ComputeLevels`). Each milestone below becomes its own brainstorm-light → writing-plans cycle once M1 is merged. They are listed with the **key design decision** each must resolve, so the next planner starts informed.

### M2 — cogwsi + streamwriter sinks + stitched-source routing
**Standalone value:** BIF → cog-wsi/svs/tiff/ome-tiff produces a correct stitched pyramid; retire the overlap guard for those targets.
**Key design decisions:**
- **Sink ordering.** `cogwsiwriter.LevelHandle.WriteTile(tx,ty)` requires strict row-major; the engine delivers out-of-order. Either (a) give the `cogwsiSink` a per-level reorder buffer, or (b) add a reorder stage to cogwsiwriter. `streamwriterSink` should wire the existing `CloseInput`/`NextReady`/`WriteTileAtIndex` drain goroutine (it already has a reorder buffer). Decide per-writer; prefer the sink-side buffer to keep writers unchanged.
- **TIFF-family `TileEncoder`.** Wrap `codec.Encoder.EncodeTile` (abbreviated) + emit `LevelHeader()` as TIFF tag 347 on the level (the writers already support `JPEGTables`). The engine's `TileEncoder.EncodeTile` returns the abbreviated body; the sink/driver attaches the shared header at `AddLevel` time.
- **Match-source pyramid shape.** The engine's 2× box descent emits a *full octave* pyramid. Source pyramids are often non-octave (SVS 4×/16×). Decide how "match-source ratios/count" reconciles with the octave descent — likely: emit the octave pyramid but only *materialize* the levels whose dims match the source's stored levels, or accept the octave pyramid as the canonical output shape (document the deviation). This is the single biggest open question; resolve it before coding M2.
- **Driver reorg.** The driver keeps associated/metadata/SVS-thumbnail-at-IFD-1/classification emission (unchanged); it `AddLevel`s every level from `ComputeLevels`, wraps handles in a sink, calls `Run`, closes. Verify against BIF fixtures: read back via opentile, stitched dims, associated preserved.

### M3 — downsample → engine
Route `--factor`/`--target-mag` for the TIFF-family + cog-wsi through `Run` (`OutL0 = Size/N`). Bypass `runConvertFactor`'s raster-L0 path (deletion is SP3). **Verify:** pixel-equivalent to today + **bounded memory** on a large slide (the C5 guard — measure peak RSS, assert it does not scale with L0 area).

### M4 — transcode → engine
Same-geometry re-encode (`OutL0 = Size`, match-source levels, different codec) through `Run`. **Verify:** level shape preserved, only codec changes. Depends on M2's match-source-shape decision.

### M5 — lossy crop → engine
`crop` (non-`--lossless`) routes through `Run(SrcRegion=rect, OutL0=rect.Size)`. Lossless crop stays verbatim (unchanged). **Verify:** lossy crop pixel-equivalent to today; lossless crop still byte-identical.

### Deferred beyond SP2
- **BIF sink** (engine→BIF) — needs a bifwriter push refactor; later SP. Until then `--to bif` from an overlapping source keeps the guard.
- **SP3 — CLI convergence** — crop/downsample/transcode as formal aliases, `validate()` capability table, `--rect` on `convert`, delete dead raster code.
- **Orientation, `--preserve-levels`, DICOM-via-engine** — parent-spec slots.

---

## Self-Review

**Spec coverage (M1 scope):**
- Engine `internal/retile` generalizing the DZI descent → Tasks 1–6. ✓
- `TileSink` interface → Task 4 (`encode.go`). ✓ (Spec also names `Spec.Encoder codec.Encoder`; M1 refines this to the thinner `retile.TileEncoder` — documented in "Key parity constraint", justified by DZI standalone-JPEG/PNG.)
- `ComputeLevels` shared by engine + drivers → Task 6, consumed in Task 7. ✓
- DZI/SZI re-pointed at the engine → Task 7. ✓
- "Prove via DZI-parity first" → Task 8 byte-identity gate. ✓
- Reuse vs rewrite (strip-feed loop, rolling band, boxDownsample2x, assembleTile, worker wiring **reused**; Spec/LevelSpec/TileSink/ComputeLevels **new**) → Tasks 2–6 port verbatim, Tasks 1/4/6 add the new types. ✓
- M2–M5 routing, sinks, match-source shape → roadmap section (deferred to own plans per the M1-first scoping decision). ✓
- Error handling (first-error propagation + ctx cancel) → preserved verbatim in `Run` (Task 5). ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every test shows full assertions. ✓

**Type consistency:** `RGBImage`, `LevelSpec{Index,Width,Height,Cols,Rows,TileW,TileH,Overlap}`, `Spec`, `TileEncoder.EncodeTile(rgb,w,h)`, `TileSink.WriteTile(level,col,row,encoded)`, `encodeJob{level,col,row,img}`, `writeJob{level,col,row,body}`, `ComputeLevels(outL0,tileW,tileH,overlap,levelRatio,levelCount)`, `Run(ctx,Spec)` — names/signatures match across Tasks 1–7. The DZI adapters (`newDZIWriterSink`, `newDZIStandaloneEncoder`, `dziWriterSink`, `dziStandaloneEncoder`, `rgbBytesAsImage`) are consistent between Task 7's impl and its test. `octaveCount` (test helper, Task 5) and `dziOctaveCount` (driver, Task 7) and the `dzi.MaxLevel+1`/ceil-halving loop all compute the same level count — verified equal: both count native→1×1 by ceil-halving. ✓

**Note on Task 5/6 ordering:** `TestRunEmitsFullOctavePyramid` (Task 5) references `ComputeLevels` (Task 6). The tasks are order-independent for *implementation* but the Task 5 test only goes green after Task 6. Flagged inline in Task 5 Step 2. An executor preferring strict per-task green should implement Task 6 before Task 5's final run.
