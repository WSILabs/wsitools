# Streaming Retile Engine (SP2) — Milestone 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route `convert --factor`/`--target-mag` and the standalone `downsample` command (svs/tiff/ome-tiff/cog-wsi) through the M1 `internal/retile` engine, replacing the reduced-L0-in-RAM raster build — eliminating survey C5. Plus harden the engine's `encoderWorker` to propagate encode errors (M2-review follow-up).

**Architecture:** The downsample bodies (`downsampleToSVS/TIFF/OMETIFF/COGWSI`, shared by `convert --factor` and `downsample`) delegate the pixel-pyramid build to two functions: `buildPyramid` (streamwriter) and `buildPyramidCOGWSI` (cog-wsi). M3 swaps ONLY those two builders' internals from `downscale.MaterializeReducedL0` + raster-pyramid to `retile.Run(OutL0 = SourceL0/factor, Kernel=Box)` — one streaming pass. Everything else (factor/metadata/writer/associated) is untouched. Pixel-equivalent to today (same box algorithm).

**Tech Stack:** Go; `internal/retile` (M1) + its M2 sinks (`streamwriterSink`, `cogwsiSink`, `codecTileEncoder`, `octaveLevelSpecsFor`); `internal/codec/jpeg`; opentile-go `ScaledStrips`/`resample`.

**Scope:** M3 = the 2 builder swaps + the engine hardening. The 4 `downsampleToX` callers, the raster builders (`buildPyramidFromRaster`/`buildPyramidFromRasterCOGWSI` — still used by `crop`), and DICOM `--factor` (stays `derivedsource`) are UNCHANGED. Deleting the now-bypassed raster path is SP3. Per `docs/superpowers/specs/2026-06-18-retile-engine-sp2-m3-design.md`.

---

## Key facts (ground truth)

- **Engine (M1+M2):** `retile.Run(ctx, retile.Spec{Slide *opentile.Slide, SrcRegion opentile.Region, OutL0 opentile.Size, Levels []retile.LevelSpec, Kernel resample.Kernel, Encoder retile.TileEncoder, Sink retile.TileSink, Workers int}) error`. `octaveLevelSpecsFor(outL0 opentile.Size, tile int) []retile.LevelSpec`. `codecTileEncoder{enc codec.Encoder}` implements `retile.TileEncoder`. Sinks: `newStreamwriterSink([]*streamwriter.LevelHandle) *streamwriterSink` and `newCogwsiSink([]*cogwsiwriter.LevelHandle, []retile.LevelSpec) *cogwsiSink`, both with `WriteTile(level,col,row,encoded) error` + `finish() error`.
- **`internal/retile.encoderWorker`** (encode.go): on `EncodeTile` error it currently just `return`s (silent). `Run` (retile.go) owns `ctx, cancel := context.WithCancel(ctx)`.
- **Builders to swap:**
  - `buildPyramid(ctx, src *opentile.Slide, w *streamwriter.Writer, factor, quality, workers int, postL0Hook func() error) error` (downsample.go:229) — materializes reduced L0 (`make([]byte, outW*outH*3)`), then `buildPyramidFromRaster(... len(srcLevels) ...)` which per-level `encodeAndWriteLevel` + `halveRaster`, running `postL0Hook` after L0 and an `mpb` progress bar.
  - `buildPyramidCOGWSI(ctx, src *opentile.Slide, w *cogwsiwriter.Writer, factor, quality, workers int) error` (convert_factor.go:967) — same, no postL0Hook.
- **Per-level streamwriter LevelSpec the downsample path uses** (encodeAndWriteLevel, downsample.go:389): `ImageWidth/Height`, `TileWidth/Height = outputTileSize`, `Compression: tiff.CompressionJPEG`, `Photometric: 2`, `SamplesPerPixel: 3`, `BitsPerSample: []uint16{8,8,8}`, `JPEGTables: enc.LevelHeader()`, `NewSubfileType: 0` (ALL pyramid IFDs — opentile SVS classifier rejects the reduced bit), `WSIImageType: tiff.WSIImageTypePyramid`. Encoder: `jpegcodec.Factory{}.NewEncoder(codec.LevelGeometry{TileWidth:outputTileSize,TileHeight:outputTileSize,PixelFormat:codec.PixelFormatRGB8}, codec.Quality{Knobs:{"q":quality}})`. `outputTileSize = 256` (downsample.go:53). The L0 ImageDescription is set globally via `streamwriter.Options.ImageDescription` by the caller — NOT per-level ExtraTags (unlike M2).
- **cog-wsi LevelSpec** (encodeAndWriteLevelCOGWSI / buildPyramidFromRasterCOGWSI): `cogwsiwriter.LevelSpec{ImageWidth/Height, TileWidth/Height=outputTileSize, Compression: enc.TIFFCompressionTag(), Photometric:2, SamplesPerPixel:3, BitsPerSample:{8,8,8}, JPEGTables: enc.LevelHeader(), IsL0: lvl==0}`.
- `jpegcodec` import alias = `github.com/wsilabs/wsitools/internal/codec/jpeg`; `codec` = `internal/codec`; `tiff` = `internal/tiff`; `resample` = `github.com/wsilabs/opentile-go/resample`; `retile` = `internal/retile`; `opentile` = `github.com/wsilabs/opentile-go`.
- `flagQuiet`, `flagVerbose` are package globals; `mpb`/`decor` already imported in downsample.go.

---

## Task 1: Engine hardening — `encoderWorker` propagates encode errors

**Files:**
- Modify: `internal/retile/encode.go`
- Modify: `internal/retile/retile.go`
- Test: `internal/retile/encode_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/retile/encode_test.go`:

```go
import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

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

	jobs <- encodeJob{level: 0, col: 0, row: 0, img: makeRGB(4, 4, 0)}
	jobs <- encodeJob{level: 0, col: 1, row: 0, img: makeRGB(4, 4, 0)}
	// drain any successful writeJobs so the worker never blocks on `out`.
	go func() {
		for range out {
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("encoderWorker did not return after encode error (hang)")
	}
	if got == nil || got.Error() != "boom" {
		t.Errorf("onErr got %v, want boom", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/retile/ -run TestEncoderWorkerPropagatesError -v`
Expected: FAIL — `encoderWorker` does not accept the `onErr` argument (compile error).

- [ ] **Step 3: Add the `onErr` parameter to `encoderWorker`**

In `internal/retile/encode.go`, change `encoderWorker` to accept `onErr func(error)` and call it on encode error:

```go
// encoderWorker pulls encodeJobs, encodes via enc, and pushes writeJobs. The
// tile's RGB buffer is released back to the pool after encoding. On an encode
// error it reports via onErr (which records the first error and cancels the run)
// and exits. Exits cleanly when jobs is closed or ctx is cancelled.
func encoderWorker(ctx context.Context, jobs <-chan encodeJob, out chan<- writeJob, enc TileEncoder, onErr func(error)) {
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
				onErr(err)
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
```

- [ ] **Step 4: Wire the error capture into `Run`**

In `internal/retile/retile.go`, inside `Run`, after `ctx, cancel := context.WithCancel(ctx)` and before spawning workers, add the capture; pass it to `encoderWorker`; and return it after the waits. Concretely:

Add an `encErr` capture near the top of `Run` (after the `cancel` is set up):

```go
	var encMu sync.Mutex
	var encErr error
	onEncErr := func(e error) {
		encMu.Lock()
		if encErr == nil {
			encErr = e
		}
		encMu.Unlock()
		cancel()
	}
```

Change the worker spawn from `encoderWorker(ctx, encodeJobs, writeJobs, spec.Encoder)` to:

```go
			encoderWorker(ctx, encodeJobs, writeJobs, spec.Encoder, onEncErr)
```

And change the final return sequence (currently `if srcErr != nil { return srcErr }; return sinkErr`) to check `encErr` between them:

```go
	if srcErr != nil {
		return srcErr
	}
	encMu.Lock()
	ee := encErr
	encMu.Unlock()
	if ee != nil {
		return ee
	}
	return sinkErr
```

(`sync` is already imported in retile.go.)

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/retile/ -run TestEncoderWorkerPropagatesError -v`
Expected: PASS.

Then the whole engine package (the existing `TestEncoderWorkerAndSinkRoundTrip` uses `encoderWorker` with the OLD 4-arg signature — UPDATE that call to pass a no-op `func(error){}` as the 5th arg):

In `internal/retile/encode_test.go`, find the existing `encoderWorker(context.Background(), jobs, writes, stubEncoder{})` call in `TestEncoderWorkerAndSinkRoundTrip` and add `, func(error) {}`:
```go
		go func() { defer wg.Done(); encoderWorker(context.Background(), jobs, writes, stubEncoder{}, func(error) {}) }()
```

Run: `go test -race ./internal/retile/ 2>&1 | grep -v 'duplicate librar' | tail -2`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/retile/encode.go internal/retile/retile.go internal/retile/encode_test.go
git commit -m "fix(retile): propagate encoder errors from Run instead of dropping tiles"
```

---

## Task 2: `retileSink` interface + `runDownsampleEngine` helper (with progress)

**Files:**
- Create: `cmd/wsitools/downsample_engine.go`
- Create: `cmd/wsitools/downsample_engine_test.go`

- [ ] **Step 1: Write the failing test (compile-time interface assertions)**

Create `cmd/wsitools/downsample_engine_test.go`:

```go
package main

import "testing"

// Compile-time: both M2 sinks satisfy retileSink.
var _ retileSink = (*streamwriterSink)(nil)
var _ retileSink = (*cogwsiSink)(nil)

func TestCountingSinkForwardsAndCounts(t *testing.T) {
	rec := map[[3]int]int{}
	base := retileSinkFunc{
		write:  func(l, c, r int, b []byte) error { rec[[3]int{l, c, r}]++; return nil },
		finish: func() error { return nil },
	}
	var n int
	cs := &countingSink{inner: base, onWrite: func() { n++ }}
	_ = cs.WriteTile(0, 0, 0, []byte{1})
	_ = cs.WriteTile(0, 1, 0, []byte{2})
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

// retileSinkFunc is a test-only retileSink backed by closures.
type retileSinkFunc struct {
	write  func(level, col, row int, b []byte) error
	finish func() error
}

func (f retileSinkFunc) WriteTile(level, col, row int, b []byte) error {
	return f.write(level, col, row, b)
}
func (f retileSinkFunc) finishSink() error { return f.finish() }
```

NOTE: the test references `countingSink{inner, onWrite}` and a `finish()` method. Adjust the test's `retileSinkFunc` to match the real `retileSink` interface method set you define in Step 3 (it must have `WriteTile` + `finish`). If `retileSink.finish` is the method name, rename `finishSink`→`finish` in the helper. Keep the test faithful to the interface — the point is: `countingSink` forwards WriteTile to its inner sink, counts each call, and delegates finish.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestCountingSink -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: retileSink`, `undefined: countingSink`.

- [ ] **Step 3: Implement `cmd/wsitools/downsample_engine.go`**

```go
package main

import (
	"context"
	"os"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/resample"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	"github.com/wsilabs/wsitools/internal/retile"
)

// retileSink is a retile.TileSink that also knows how to drain/join itself.
// Both streamwriterSink and cogwsiSink satisfy it.
type retileSink interface {
	WriteTile(level, col, row int, encoded []byte) error
	finish() error
}

// countingSink wraps a retileSink, invoking onWrite after each forwarded tile
// (for a progress bar). finish delegates.
type countingSink struct {
	inner   retileSink
	onWrite func()
}

func (c *countingSink) WriteTile(level, col, row int, encoded []byte) error {
	if err := c.inner.WriteTile(level, col, row, encoded); err != nil {
		return err
	}
	if c.onWrite != nil {
		c.onWrite()
	}
	return nil
}

func (c *countingSink) finish() error { return c.inner.finish() }

// totalTiles sums Cols*Rows across all output levels (for the progress bar).
func totalTiles(levels []retile.LevelSpec) int64 {
	var n int64
	for _, l := range levels {
		n += int64(l.Cols) * int64(l.Rows)
	}
	return n
}

// runDownsampleEngine runs one streaming retile pass: ScaledStrips scales the
// full source L0 to outL0 (Box, area-averaging) and the engine box-2× derives
// the octave pyramid in `levels`, encoding via enc into sink. It wraps sink in a
// progress bar (unless --quiet) and ALWAYS finishes the sink (joining its drain
// goroutines) even on a Run error, preferring the Run error.
func runDownsampleEngine(ctx context.Context, slide *opentile.Slide, srcL0, outL0 opentile.Size, levels []retile.LevelSpec, enc retile.TileEncoder, sink retileSink, workers int) error {
	var progress *mpb.Progress
	var bar *mpb.Bar
	wrapped := sink
	if !flagQuiet {
		progress = mpb.New(mpb.WithOutput(os.Stderr))
		bar = progress.AddBar(totalTiles(levels),
			mpb.PrependDecorators(decor.Name("encoding "), decor.Percentage(decor.WCSyncSpace)),
			mpb.AppendDecorators(decor.EwmaSpeed(0, "%.0f tiles/s", 30), decor.Name(" ETA "), decor.EwmaETA(decor.ET_STYLE_GO, 30)),
		)
		wrapped = &countingSink{inner: sink, onWrite: bar.Increment}
	}

	runErr := retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: srcL0},
		OutL0:     outL0,
		Levels:    levels,
		Kernel:    resample.Box, // real downscale: area-average the L0→outL0 read
		Encoder:   enc,
		Sink:      wrapped,
		Workers:   workers,
	})
	if ferr := wrapped.finish(); ferr != nil && runErr == nil {
		runErr = ferr
	}
	if progress != nil {
		progress.Wait()
	}
	return runErr
}
```

NOTE: confirm the mpb import path/version matches downsample.go's existing imports (`github.com/vbauerster/mpb/v8` + `/decor`). If downsample.go uses a different major version, match it. Confirm `bar.Increment` is a valid method value for the installed mpb version (it is for v8). If the test's `countingSink` field names differ, align them.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestCountingSink -v 2>&1 | grep -v 'duplicate librar'` → PASS.
Run: `go build ./... 2>&1 | grep -v 'duplicate librar'` → clean (confirms the `var _ retileSink` assertions hold — both sinks satisfy the interface).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/downsample_engine.go cmd/wsitools/downsample_engine_test.go
git commit -m "feat(downsample): retileSink interface + runDownsampleEngine helper"
```

---

## Task 3: Swap `buildPyramid` (streamwriter) to the engine

**Files:**
- Modify: `cmd/wsitools/downsample.go` (`buildPyramid` only — leave `buildPyramidFromRaster`, `encodeAndWriteLevel`, `halveRaster` intact; `crop` uses them)
- Test: controller-run integration

- [ ] **Step 1: Rewrite `buildPyramid`**

Replace the body of `buildPyramid` (downsample.go:229) with the engine path. Keep the SAME signature (`postL0Hook` runs after L0's AddLevel, before L1):

```go
func buildPyramid(ctx context.Context, src *opentile.Slide, w *streamwriter.Writer, factor, quality, workers int, postL0Hook func() error) error {
	srcL0 := src.Levels()[0]
	srcSize := opentile.Size{W: srcL0.Size.W, H: srcL0.Size.H}
	outL0 := opentile.Size{W: srcSize.W / factor, H: srcSize.H / factor}
	if outL0.W <= 0 || outL0.H <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outL0.W, outL0.H, factor)
	}
	levels := octaveLevelSpecsFor(outL0, outputTileSize)

	enc, err := jpegcodec.Factory{}.NewEncoder(codec.LevelGeometry{
		TileWidth: outputTileSize, TileHeight: outputTileSize, PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}})
	if err != nil {
		return fmt.Errorf("new encoder: %w", err)
	}
	defer enc.Close()
	tables := enc.LevelHeader()

	specFor := func(i int) streamwriter.LevelSpec {
		return streamwriter.LevelSpec{
			ImageWidth:      uint32(levels[i].Width),
			ImageHeight:     uint32(levels[i].Height),
			TileWidth:       outputTileSize,
			TileHeight:      outputTileSize,
			Compression:     tiff.CompressionJPEG,
			Photometric:     2,
			SamplesPerPixel: 3,
			BitsPerSample:   []uint16{8, 8, 8},
			JPEGTables:      tables,
			NewSubfileType:  0,
			WSIImageType:    tiff.WSIImageTypePyramid,
		}
	}

	handles := make([]*streamwriter.LevelHandle, len(levels))
	h0, err := w.AddLevel(specFor(0))
	if err != nil {
		return fmt.Errorf("add level 0: %w", err)
	}
	handles[0] = h0
	// postL0Hook (thumbnail IFD) must land at IFD 1 — between L0 and L1 AddLevels.
	if postL0Hook != nil {
		if err := postL0Hook(); err != nil {
			return fmt.Errorf("post-L0 hook: %w", err)
		}
	}
	for i := 1; i < len(levels); i++ {
		h, err := w.AddLevel(specFor(i))
		if err != nil {
			return fmt.Errorf("add level %d: %w", i, err)
		}
		handles[i] = h
	}

	sink := newStreamwriterSink(handles)
	return runDownsampleEngine(ctx, src, srcSize, outL0, levels, &codecTileEncoder{enc: enc}, sink, workers)
}
```

After this rewrite, `buildPyramid` no longer references `downscale.MaterializeReducedL0` or `buildPyramidFromRaster`. Check whether `downscale` is still imported/used elsewhere in downsample.go (it is — `halveRaster` uses `otresample`, but `MaterializeReducedL0` may now be unused in THIS file). Run `go build` and fix any now-unused import (the `downscale` import is likely still used by other functions; if `go build` flags it unused, remove it — but verify with grep first: `grep -n 'downscale\.' cmd/wsitools/downsample.go`).

- [ ] **Step 2: Build + existing downsample unit tests**

Run: `go build ./... 2>&1 | grep -v 'duplicate librar'` → clean.
Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'Downsample|Factor' 2>&1 | grep -v 'duplicate librar' | tail -10`
Expected: PASS (or fixture SKIP). Some tests assert exact output level COUNT = source level count — those will now see octave-floored counts and may need updating. If a test fails on level count, that is the EXPECTED octave-floored change (user-approved): update the test's expectation to the floored count (`flooredLevelCount(outW, outH, 256)`), NOT revert the behavior. Report any such test you change.

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/downsample.go
git commit -m "feat(downsample): build svs/tiff/ome-tiff pyramid via the streaming engine"
```

- [ ] **Step 4: CONTROLLER integration (convert --factor + downsample, svs/tiff/ome-tiff)**

Controller runs:
```bash
make build
FIX=$(pwd)/sample_files/svs/CMU-1.svs   # a larger SVS if available; else CMU-1-Small-Region.svs
./bin/wsitools convert "$FIX" --to svs --factor 2 -o /tmp/m3-svs-f2.svs -f
./bin/wsitools info /tmp/m3-svs-f2.svs            # L0 = srcL0/2; octave-floored levels; thumbnail IFD 1
./bin/wsitools validate /tmp/m3-svs-f2.svs
./bin/wsitools downsample "$FIX" --factor 2 -o /tmp/m3-ds.svs -f && ./bin/wsitools validate /tmp/m3-ds.svs
```
Expected: convert + downsample succeed; L0 halved; MPP doubled, mag halved; thumbnail at IFD 1; validate clean.

---

## Task 4: Swap `buildPyramidCOGWSI` to the engine

**Files:**
- Modify: `cmd/wsitools/convert_factor.go` (`buildPyramidCOGWSI` only — leave `buildPyramidFromRasterCOGWSI`, `encodeAndWriteLevelCOGWSI` intact; `crop` uses them)
- Test: controller-run integration

- [ ] **Step 1: Rewrite `buildPyramidCOGWSI`**

Replace the body of `buildPyramidCOGWSI` (convert_factor.go:967):

```go
func buildPyramidCOGWSI(ctx context.Context, src *opentile.Slide, w *cogwsiwriter.Writer, factor, quality, workers int) error {
	srcL0 := src.Levels()[0]
	srcSize := opentile.Size{W: srcL0.Size.W, H: srcL0.Size.H}
	outL0 := opentile.Size{W: srcSize.W / factor, H: srcSize.H / factor}
	if outL0.W <= 0 || outL0.H <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outL0.W, outL0.H, factor)
	}
	levels := octaveLevelSpecsFor(outL0, outputTileSize)

	enc, err := jpegcodec.Factory{}.NewEncoder(codec.LevelGeometry{
		TileWidth: outputTileSize, TileHeight: outputTileSize, PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}})
	if err != nil {
		return fmt.Errorf("new encoder: %w", err)
	}
	defer enc.Close()

	handles := make([]*cogwsiwriter.LevelHandle, len(levels))
	for i := range levels {
		h, err := w.AddLevel(cogwsiwriter.LevelSpec{
			ImageWidth: uint32(levels[i].Width), ImageHeight: uint32(levels[i].Height),
			TileWidth: outputTileSize, TileHeight: outputTileSize,
			Compression: enc.TIFFCompressionTag(), Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			JPEGTables: enc.LevelHeader(),
			IsL0:       i == 0,
		})
		if err != nil {
			return fmt.Errorf("add level %d: %w", i, err)
		}
		handles[i] = h
	}

	sink := newCogwsiSink(handles, levels)
	return runDownsampleEngine(ctx, src, srcSize, outL0, levels, &codecTileEncoder{enc: enc}, sink, workers)
}
```

Confirm `strconv` and `jpegcodec`/`codec`/`opentile` are imported in convert_factor.go (add any missing). After the rewrite, `buildPyramidCOGWSI` no longer calls `downscale.MaterializeReducedL0`/`buildPyramidFromRasterCOGWSI`; verify `go build` and fix any now-unused import (grep first — `downscale` is still used by `downsampleToDICOM`, so the import stays).

- [ ] **Step 2: Build + existing tests**

Run: `go build ./... 2>&1 | grep -v 'duplicate librar'` → clean.
Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'COGWSI|Cogwsi|Factor|Downsample' 2>&1 | grep -v 'duplicate librar' | tail -10`
Expected: PASS (level-count assertions may need the floored update, as in Task 3).

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/convert_factor.go
git commit -m "feat(downsample): build cog-wsi pyramid via the streaming engine"
```

- [ ] **Step 4: CONTROLLER integration (--factor cog-wsi)**

```bash
make build
FIX=$(pwd)/sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools convert "$FIX" --to cog-wsi --factor 2 -o /tmp/m3-cog-f2.tif -f
./bin/wsitools info /tmp/m3-cog-f2.tif
./bin/wsitools validate /tmp/m3-cog-f2.tif
```
Expected: succeeds; L0 halved; octave-floored levels; validate clean.

---

## Task 5: Verification — pixel-equivalence + bounded memory + race (controller)

**Files:** none (verification only).

- [ ] **Step 1: Pixel-equivalence vs the pre-engine raster path**

Capture the pre-swap output from a `main` worktree, then diff regions:
```bash
git worktree add /tmp/m3-ref main
( cd /tmp/m3-ref && make build )
FIX=$(pwd)/sample_files/svs/CMU-1-Small-Region.svs
/tmp/m3-ref/bin/wsitools convert "$FIX" --to svs --factor 2 -o /tmp/m3-ref-f2.svs -f
./bin/wsitools convert "$FIX" --to svs --factor 2 -o /tmp/m3-new-f2.svs -f
# Compare an L0 region from each (engine vs raster) — expect mean diff ~0-1/255.
for f in ref new; do ./bin/wsitools region /tmp/m3-$f-f2.svs --rect 500,500,512,512 --level 0 -o /tmp/m3-$f-reg.png -f; done
python3 - <<'PY'
from PIL import Image, ImageChops, ImageStat
a=Image.open("/tmp/m3-ref-reg.png").convert("RGB"); b=Image.open("/tmp/m3-new-reg.png").convert("RGB")
st=ImageStat.Stat(ImageChops.difference(a,b))
print("mean abs diff:", [round(x,2) for x in st.mean], "max:", st.extrema)
PY
```
Expected: mean ~0–1/255 (pixel-equivalent — same box algorithm). A large diff means the engine downscale diverged — STOP and debug (kernel? level dims?). NOTE: the level COUNT may differ (octave-floored vs source-count) — that is expected; compare L0 pixels, not level count.

- [ ] **Step 2: Bounded-memory (C5) check**

```bash
# Engine path should NOT allocate a full reduced-L0 raster. Compare peak RSS.
/usr/bin/time -l ./bin/wsitools convert "$FIX" --to svs --factor 2 -o /tmp/m3-mem.svs -f 2>&1 | grep -iE 'maximum resident|real'
/usr/bin/time -l /tmp/m3-ref/bin/wsitools convert "$FIX" --to svs --factor 2 -o /tmp/m3-mem-ref.svs -f 2>&1 | grep -iE 'maximum resident|real'
```
Expected: the engine peak RSS is ≤ the raster path's (and crucially does not contain a `~outW*outH*3` spike). On CMU-1-Small-Region the absolute numbers are small; the meaningful assertion is "no L0-area-proportional allocation" — note the engine number is flat. (If a multi-GB SVS fixture is available locally, run it there for a decisive contrast.)

- [ ] **Step 3: Full race suite**

```bash
WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -race -count=1 -timeout 30m ./internal/retile/ ./cmd/wsitools/ 2>&1 | grep -v 'duplicate librar' | tail -4
```
Expected: all PASS.

- [ ] **Step 4: Cleanup**

```bash
git worktree remove /tmp/m3-ref
```

---

## Final verification + finish

Dispatch a final code reviewer over `main..HEAD`, then use **superpowers:finishing-a-development-branch**. Branch: `feat/retile-engine-m3` off `main`.

**M3 acceptance:**
- `internal/retile` encoderWorker propagates encode errors (unit test); no hang.
- `convert --factor`/`downsample` for svs/tiff/ome-tiff/cog-wsi route through the engine; output pixel-equivalent to the raster path (~0–1/255); octave-floored levels; metadata (MPP×factor, mag÷factor) + associated + SVS thumbnail-at-IFD-1 preserved; validate clean.
- Bounded memory (no reduced-L0 raster allocation).
- dicom `--factor` unchanged; `crop`'s raster builders unchanged; full `-race` green.

---

## Self-Review

**Spec coverage:**
- factor = OutL0/N + Box, octave-floored, pixel-equivalent → Tasks 3/4 (`runDownsampleEngine` with `resample.Box`, `octaveLevelSpecsFor`) + Task 5 Step 1. ✓
- Swap only the 2 builders; callers/raster-builders/dicom unchanged → Tasks 3 (`buildPyramid`) + 4 (`buildPyramidCOGWSI`), explicit "leave intact" notes. ✓
- Shared helper → Task 2 (`runDownsampleEngine`). ✓
- encoderWorker hardening → Task 1. ✓
- Metadata/associated/thumbnail preserved → unchanged caller code + `postL0Hook` between L0/L1 (Task 3). ✓
- Bounded-memory + pixel-equivalence + race testing → Task 5. ✓

**Placeholder scan:** none. The mpb-version / unused-import / level-count-test "confirm" notes are explicit verification steps with defined actions, not placeholders.

**Type consistency:** `retileSink{WriteTile,finish}`, `countingSink{inner,onWrite}`, `runDownsampleEngine(ctx, slide, srcL0, outL0 opentile.Size, levels, enc retile.TileEncoder, sink retileSink, workers)`, `octaveLevelSpecsFor`, `codecTileEncoder{enc}`, `newStreamwriterSink`/`newCogwsiSink`, `encoderWorker(ctx,jobs,out,enc,onErr)` — consistent across tasks and matching M1/M2 definitions. The Task 2 test note flags aligning `retileSinkFunc`'s method name to the real `finish`.

**Risk:** Task 3/4 — existing downsample tests that assert exact level COUNT will change under octave-floored; the plan says update them to the floored count (user-approved), not revert. Flagged inline.
