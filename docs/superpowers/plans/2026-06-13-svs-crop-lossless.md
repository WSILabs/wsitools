# `wsitools crop --lossless` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `--lossless` mode to `wsitools crop` that copies the source L0 tiles **byte-identical** onto a tile-aligned superset of the requested rect (Strategy B), rebuilding only the lower overview levels.

**Architecture:** Snap the requested rect to the L0 tile grid (origin down, far edge up). Copy the contiguous source-tile block verbatim into output L0 (`writeLosslessL0`, propagating the level's shared JPEG tables via `Level.TilePrefix()`). Decode the snapped region (existing `MaterializeCroppedL0`) only to regenerate the thumbnail and rebuild L1…Ln (existing `buildPyramidFromRaster`, fed a once-halved raster via a new shared `halveRaster` helper). `--lossless` is an additive branch in `runCrop`; the shipped Strategy-A default path is preserved.

**Tech Stack:** Go, cobra, `internal/tiff/streamwriter` (`AddLevel`/`WriteTile` raw-tile path + `JPEGTables`), opentile-go v1 (`Level.Tile`/`TilePrefix`/`TileSize`/`Compression`), `internal/downscale`.

**Spec:** `docs/superpowers/specs/2026-06-13-svs-crop-lossless-design.md`
**Builds on:** the shipped `crop` (Strategy A) — `cmd/wsitools/crop.go`, `crop_thumbnail.go`, `internal/downscale/crop.go`, `svs_imagedesc.go`.

---

## File Structure

| File | Action |
|---|---|
| `cmd/wsitools/downsample.go` | Modify: extract `halveRaster`; `buildPyramidFromRaster`'s loop calls it |
| `cmd/wsitools/crop.go` | Modify: add `--lossless` flag + `cropLossless` global; `snapRectToTiles`; `runCrop` effective-rect refactor + lossless branch |
| `cmd/wsitools/crop_lossless.go` | Create: `writeLosslessL0` |
| `cmd/wsitools/crop_test.go` | Modify: add `TestSnapRectToTiles`, `TestHalveRaster` |
| `tests/integration/crop_test.go` | Modify: add `TestCropLossless_ByteIdentity` |

Reused unchanged: `MaterializeCroppedL0`, `BuildCropImageDescription`, `regenCropThumbnail`, `cropPyramidLevels`, `validateCropBounds`, `writeOneAssociated`.

---

## Task 1: Extract `halveRaster` shared helper

**Files:**
- Modify: `cmd/wsitools/downsample.go` (extract from `buildPyramidFromRaster`'s loop, ~lines 314-341; add `halveRaster` near `cropRaster` ~line 350)
- Test: `cmd/wsitools/crop_test.go` (add `TestHalveRaster`)

The lossless path's L0→L1 reduction must be byte-identical to `buildPyramidFromRaster`'s inter-level halve. Extract that step into one shared function so both call sites are guaranteed identical.

- [ ] **Step 1: Write the failing test**

Add to `cmd/wsitools/crop_test.go`:

```go
func TestHalveRaster(t *testing.T) {
	// 4x4 RGB raster (stride 12); halving → 2x2.
	w, h := 4, 4
	raster := make([]byte, w*h*3)
	for i := range raster {
		raster[i] = byte(i)
	}
	out, ow, oh, err := halveRaster(raster, w, h)
	if err != nil {
		t.Fatalf("halveRaster: %v", err)
	}
	if ow != 2 || oh != 2 {
		t.Errorf("dims = %dx%d, want 2x2", ow, oh)
	}
	if len(out) != ow*oh*3 {
		t.Errorf("len = %d, want %d", len(out), ow*oh*3)
	}
	// Odd dimensions are truncated to even before halving: 5x5 → 2x2.
	odd := make([]byte, 5*5*3)
	_, ow, oh, err = halveRaster(odd, 5, 5)
	if err != nil {
		t.Fatalf("halveRaster odd: %v", err)
	}
	if ow != 2 || oh != 2 {
		t.Errorf("odd dims = %dx%d, want 2x2 (5&^1=4, /2=2)", ow, oh)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestHalveRaster -count=1`
Expected: FAIL — `undefined: halveRaster`.

- [ ] **Step 3: Extract `halveRaster` and re-point the loop**

In `cmd/wsitools/downsample.go`, add this function immediately above `cropRaster` (~line 350):

```go
// halveRaster box-downscales an RGB888 raster by 2×, truncating odd dimensions
// to even first, returning the half-size raster and its new dimensions. Shared
// by buildPyramidFromRaster's inter-level reduction and the lossless crop's
// L0→L1 reduction so both produce byte-identical L1 pixels.
func halveRaster(raster []byte, w, h int) ([]byte, int, int, error) {
	evenW := w &^ 1
	evenH := h &^ 1
	if evenW != w || evenH != h {
		raster = cropRaster(raster, w, h, evenW, evenH)
		w, h = evenW, evenH
	}
	src := &otdecoder.Image{
		Width:  w,
		Height: h,
		Stride: w * 3,
		Format: otdecoder.PixelFormatRGB,
		Pix:    raster,
	}
	dst := otdecoder.NewImageFormat(w/2, h/2, otdecoder.PixelFormatRGB)
	if err := otresample.ImageInto(src, dst, otresample.Box); err != nil {
		return nil, 0, 0, err
	}
	return dst.Pix, w / 2, h / 2, nil
}
```

Then replace the inter-level halve block in `buildPyramidFromRaster` (the current lines 314-341, the `if outLvl < nLevels-1 { ... }` body) with:

```go
		if outLvl < nLevels-1 {
			var herr error
			currentRaster, currentW, currentH, herr = halveRaster(currentRaster, currentW, currentH)
			if herr != nil {
				if progress != nil {
					progress.Wait()
				}
				return fmt.Errorf("Box halving level %d→%d: %w", outLvl, outLvl+1, herr)
			}
			if currentW == 0 || currentH == 0 {
				break
			}
		}
```

This is behavior-preserving: `halveRaster` performs the identical even-up (`cropRaster`) + `otresample.ImageInto(Box)` steps the inline code did.

- [ ] **Step 4: Run tests + build**

Run: `go test ./cmd/wsitools/ -run 'TestHalveRaster|Pyramid|Downsample' -count=1` → PASS
Run: `go build ./...` → clean; `go vet ./cmd/wsitools/` → clean

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/downsample.go cmd/wsitools/crop_test.go
git commit -m "refactor(convert): extract halveRaster shared by pyramid build + lossless crop"
```

- [ ] **Step 6: Controller runs the downsample regression guard** (the implementer does NOT — heavy)

Controller runs: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestDownsample -count=1 -timeout 30m`
Expected: PASS (downsample output unchanged by the extraction).

---

## Task 2: `snapRectToTiles`

**Files:**
- Modify: `cmd/wsitools/crop.go` (add `snapRectToTiles`)
- Test: `cmd/wsitools/crop_test.go` (add `TestSnapRectToTiles`)

- [ ] **Step 1: Write the failing test**

Add to `cmd/wsitools/crop_test.go`:

```go
func TestSnapRectToTiles(t *testing.T) {
	// Unaligned rect, base larger than the bbox: snaps to the enclosing tile box.
	snapX, snapY, snapW, snapH, stx0, sty0, ntx, nty := snapRectToTiles(100, 80, 300, 300, 256, 256, 4096, 4096)
	if snapX != 0 || snapY != 0 || snapW != 512 || snapH != 512 {
		t.Errorf("unaligned snap = %d,%d %dx%d, want 0,0 512x512", snapX, snapY, snapW, snapH)
	}
	if stx0 != 0 || sty0 != 0 || ntx != 2 || nty != 2 {
		t.Errorf("unaligned tiles = stx0=%d sty0=%d ntx=%d nty=%d, want 0,0,2,2", stx0, sty0, ntx, nty)
	}

	// Already tile-aligned: snapped == requested.
	snapX, snapY, snapW, snapH, stx0, sty0, ntx, nty = snapRectToTiles(256, 512, 512, 256, 256, 256, 4096, 4096)
	if snapX != 256 || snapY != 512 || snapW != 512 || snapH != 256 {
		t.Errorf("aligned snap = %d,%d %dx%d, want 256,512 512x256", snapX, snapY, snapW, snapH)
	}
	if stx0 != 1 || sty0 != 2 || ntx != 2 || nty != 1 {
		t.Errorf("aligned tiles = stx0=%d sty0=%d ntx=%d nty=%d, want 1,2,2,1", stx0, sty0, ntx, nty)
	}

	// Edge clamp: far edge would exceed the image → clamped; last tile partial.
	snapX, snapY, snapW, snapH, _, _, ntx, nty = snapRectToTiles(400, 400, 150, 150, 256, 256, 600, 600)
	if snapX != 256 || snapY != 256 || snapW != 344 || snapH != 344 {
		t.Errorf("edge snap = %d,%d %dx%d, want 256,256 344x344", snapX, snapY, snapW, snapH)
	}
	if ntx != 2 || nty != 2 {
		t.Errorf("edge tiles = %dx%d, want 2x2 (last partial)", ntx, nty)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestSnapRectToTiles -count=1`
Expected: FAIL — `undefined: snapRectToTiles`.

- [ ] **Step 3: Implement**

Add to `cmd/wsitools/crop.go` (near `validateCropBounds`):

```go
// snapRectToTiles snaps a requested L0 rect to the tile grid for a lossless
// crop: origin DOWN to the enclosing tile boundary, far edge UP (clamped to the
// image), producing the tile-aligned bounding box of the request. Returns the
// snapped rect, the source tile-coordinate origin of the block (stx0,sty0), and
// the output tile-grid dimensions (outTilesX,outTilesY). A tile-aligned origin
// means output tile (ox,oy) maps 1:1 onto source tile (stx0+ox, sty0+oy).
func snapRectToTiles(x, y, w, h, tileW, tileH, baseW, baseH int) (snapX, snapY, snapW, snapH, stx0, sty0, outTilesX, outTilesY int) {
	snapX = (x / tileW) * tileW
	snapY = (y / tileH) * tileH
	endX := ((x + w + tileW - 1) / tileW) * tileW
	endY := ((y + h + tileH - 1) / tileH) * tileH
	if endX > baseW {
		endX = baseW
	}
	if endY > baseH {
		endY = baseH
	}
	snapW = endX - snapX
	snapH = endY - snapY
	stx0 = snapX / tileW
	sty0 = snapY / tileH
	outTilesX = (snapW + tileW - 1) / tileW
	outTilesY = (snapH + tileH - 1) / tileH
	return
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/wsitools/ -run TestSnapRectToTiles -count=1` → PASS; `go build ./...` clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/crop.go cmd/wsitools/crop_test.go
git commit -m "feat(crop): snapRectToTiles (tile-aligned bbox for lossless crop)"
```

---

## Task 3: `writeLosslessL0` (verbatim tile copy)

**Files:**
- Create: `cmd/wsitools/crop_lossless.go`

Emits pyramid level 0 by copying a contiguous block of source L0 tiles byte-identical, propagating the level's shared JPEG tables. No unit test (needs a real `*opentile.Level`); covered by Task 5's byte-identity integration test.

- [ ] **Step 1: Implement**

Create `cmd/wsitools/crop_lossless.go`:

```go
package main

import (
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// writeLosslessL0 emits pyramid level 0 by copying a contiguous block of source
// L0 tiles VERBATIM (byte-identical compressed bytes), propagating the source
// level's shared codec prefix (JPEG tables, tag 347). The crop origin must be
// tile-aligned (see snapRectToTiles) so output tile (ox,oy) maps 1:1 onto source
// tile (stx0+ox, sty0+oy). outW/outH are the snapped L0 dimensions.
//
// It mirrors encodeAndWriteLevel's concurrent NextReady drain (so non-row-major
// --tile-order still works and the reorder buffer never deadlocks on large
// grids), but the per-tile work is a raw byte copy instead of an encode.
func writeLosslessL0(w *streamwriter.Writer, srcL0 *opentile.Level, stx0, sty0, outTilesX, outTilesY, outW, outH int) error {
	lh, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth:      uint32(outW),
		ImageHeight:     uint32(outH),
		TileWidth:       uint32(srcL0.TileSize.W),
		TileHeight:      uint32(srcL0.TileSize.H),
		Compression:     opentile.CompressionToTIFFTag(srcL0.Compression),
		Photometric:     2, // RGB (Aperio) — same as encodeAndWriteLevel
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		JPEGTables:      srcL0.TilePrefix(), // shared tag-347 tables (nil if none)
		NewSubfileType:  0,
		WSIImageType:    tiff.WSIImageTypePyramid,
	})
	if err != nil {
		return fmt.Errorf("AddLevel: %w", err)
	}

	// Concurrent ordered drain (mirrors encodeAndWriteLevel).
	drainErr := make(chan error, 1)
	go func() {
		for {
			idx, bytes, ok, err := lh.NextReady()
			if err != nil {
				drainErr <- err
				return
			}
			if !ok {
				drainErr <- nil
				return
			}
			if err := lh.WriteTileAtIndex(idx, bytes); err != nil {
				lh.Abort(err)
				drainErr <- err
				return
			}
		}
	}()

	var submitErr error
	for oy := 0; oy < outTilesY && submitErr == nil; oy++ {
		for ox := 0; ox < outTilesX; ox++ {
			// Tile() returns a fresh slice per call (stable for the deferred
			// write); do NOT use the buffer-reusing TileInto here.
			tile, err := srcL0.Tile(stx0+ox, sty0+oy)
			if err != nil {
				submitErr = fmt.Errorf("read source tile (%d,%d): %w", stx0+ox, sty0+oy, err)
				break
			}
			if err := lh.WriteTile(uint32(ox), uint32(oy), tile); err != nil {
				submitErr = err
				break
			}
		}
	}
	lh.CloseInput()
	if submitErr != nil {
		lh.Abort(submitErr)
		<-drainErr
		return submitErr
	}
	return <-drainErr
}
```

- [ ] **Step 2: Build + vet**

Run: `go build ./...` → clean (confirms `opentile.CompressionToTIFFTag` returns `uint16` matching `LevelSpec.Compression`, and `srcL0.TilePrefix()`/`srcL0.Tile()`/`srcL0.TileSize`/`srcL0.Compression` resolve). `go vet ./cmd/wsitools/` clean.

> If `CompressionToTIFFTag` is found to return a non-`uint16` type, cast to `uint16(...)`. Verified at plan time: it returns `uint16` (opentile-go `compression.go:77`).

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/crop_lossless.go
git commit -m "feat(crop): writeLosslessL0 verbatim source-tile-block copy"
```

---

## Task 4: `--lossless` flag + `runCrop` effective-rect branch

**Files:**
- Modify: `cmd/wsitools/crop.go`

Add the flag, thread `lossless` into `runCrop`, compute the effective rect (snap when lossless), and branch the L0+pyramid emission. The Strategy-A path is preserved (effective rect == requested rect when not lossless).

- [ ] **Step 1: Add the flag + global + RunE wiring**

In `cmd/wsitools/crop.go`, add to the `var (...)` block: `cropLossless bool`.

In `init()`, add:

```go
	cropCmd.Flags().BoolVar(&cropLossless, "lossless", false, "Lossless crop: snap to the tile grid and copy L0 tiles verbatim (output is a tile-aligned superset of the rect)")
```

Change the `RunE` closure's `runCrop(...)` call to pass `cropLossless`:

```go
		return runCrop(cmd.Context(), args[0], cropOutput, x, y, w, h,
			cropQuality, cropWorkers, cropTileOrder, cropBigTIFF, cropForce, cropNoAssoc, cropLossless, time.Now())
```

Update the Long description's final paragraph to mention lossless mode:

```
The default crop is a full re-encode (one JPEG generation) — it matches how
Aperio ImageScope crops. With --lossless, the crop is snapped to the L0 tile
grid and the full-resolution tiles are copied verbatim (byte-identical); the
output is a tile-aligned superset of the requested rect (up to ~255px larger
per edge) and the command prints the snapped rect.
```

- [ ] **Step 2: Refactor `runCrop` signature + effective-rect + branch**

Change the `runCrop` signature to add `lossless bool` before `start`:

```go
func runCrop(ctx context.Context, input, output string, x, y, w, h, quality, workers int, tileOrderName, bigtiffFlag string, force, noAssociated, lossless bool, start time.Time) error {
```

After `validateCropBounds(...)` and after the `desc`/`quality` resolution (i.e. just before the `cropDesc := BuildCropImageDescription(...)` line), introduce the effective rect and snap when lossless. Replace the existing block:

```go
	cropDesc := BuildCropImageDescription(rawDesc, baseW, baseH, x, y, w, h, outputTileSize, outputTileSize, quality)
```

with:

```go
	// Effective rect: lossless snaps the request to the tile grid (origin down,
	// extent up) so L0 tiles copy verbatim; the default path uses the exact rect.
	ex, ey, ew, eh := x, y, w, h
	var stx0, sty0, outTilesX, outTilesY int
	tileW, tileH := srcL0.TileSize.W, srcL0.TileSize.H
	if lossless {
		ex, ey, ew, eh, stx0, sty0, outTilesX, outTilesY = snapRectToTiles(x, y, w, h, tileW, tileH, baseW, baseH)
		if ex != x || ey != y || ew != w || eh != h {
			fmt.Printf("lossless: snapped crop to %d,%d %dx%d (tile-aligned)\n", ex, ey, ew, eh)
		}
	}
	cropDesc := BuildCropImageDescription(rawDesc, baseW, baseH, ex, ey, ew, eh, outputTileSize, outputTileSize, quality)
```

> Note: keep the geometry token's tile field at `outputTileSize` (256) for both
> paths — it documents the *rebuilt* pyramid tiling; the verbatim L0's actual
> tile size is recorded in its TIFF tags by the writer regardless.

Then update the **BigTIFF auto** check and the **materialize** + **emission** to use the effective rect. Change the BigTIFF auto raster-size check from `int64(w)*int64(h)*3` to `int64(ew)*int64(eh)*3`.

Change the raster allocation + materialize from `(x, y, w, h)` to the effective rect:

```go
	rasterBytes := int64(ew) * int64(eh) * 3
	if rasterBytes < 0 {
		return fmt.Errorf("cropped L0 raster size overflows int64")
	}
	outL0 := make([]byte, rasterBytes)
	if err := downscale.MaterializeCroppedL0(ctx, srcL0, outL0, ex, ey, ew, eh); err != nil {
		return fmt.Errorf("materialize cropped L0: %w", err)
	}
```

Then replace the existing emission block (the `postL0Hook` definition through the `buildPyramidFromRaster(...)` call) with the mode branch:

```go
	nLevels := cropPyramidLevels(ew, eh, outputTileSize)
	if lossless {
		// L0: verbatim source-tile-block copy (byte-identical full-res data).
		if err := writeLosslessL0(wtr, srcL0, stx0, sty0, outTilesX, outTilesY, ew, eh); err != nil {
			return fmt.Errorf("write lossless L0: %w", err)
		}
		// Thumbnail between L0 and L1 (regenerated from the decoded crop).
		if !noAssociated {
			if err := regenCropThumbnail(wtr, outL0, ew, eh, quality); err != nil {
				return fmt.Errorf("regenerate thumbnail: %w", err)
			}
		}
		// Lower levels: rebuild from the once-halved raster (re-encode).
		if nLevels > 1 {
			l1, l1W, l1H, err := halveRaster(outL0, ew, eh)
			if err != nil {
				return fmt.Errorf("halve L0→L1: %w", err)
			}
			if err := buildPyramidFromRaster(ctx, wtr, l1, l1W, l1H, nLevels-1, quality, workers, nil); err != nil {
				return fmt.Errorf("build pyramid: %w", err)
			}
		}
	} else {
		// Strategy A: re-encode every level from the decoded raster; thumbnail
		// interleaved after L0 via the post-L0 hook.
		var postL0Hook func() error
		if !noAssociated {
			postL0Hook = func() error {
				return regenCropThumbnail(wtr, outL0, ew, eh, quality)
			}
		}
		if err := buildPyramidFromRaster(ctx, wtr, outL0, ew, eh, nLevels, quality, workers, postL0Hook); err != nil {
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
```

(The label/macro passthrough block and `wtr.Close()` that follow are unchanged. The `var label, macro opentile.AssociatedImage` segregation block that precedes the emission is unchanged and applies to both modes.)

- [ ] **Step 3: Build + vet + existing crop unit tests + CLI smoke**

Run: `go build ./...` clean; `go vet ./cmd/wsitools/` clean.
Run: `go test ./cmd/wsitools/ -run 'Crop|Snap|Halve|Thumb|Quality|BuildCrop|Rect' -count=1` → PASS.
Run: `go run ./cmd/wsitools crop --help` → shows `--lossless`.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/crop.go
git commit -m "feat(crop): --lossless mode (tile-snap + verbatim L0, rebuilt lower levels)"
```

---

## Task 5: byte-identity integration test

**Files:**
- Modify: `tests/integration/crop_test.go`

- [ ] **Step 1: Write the test**

Append to `tests/integration/crop_test.go` (the build tag + `decoder/all`/`formats/all` imports already exist from the shipped crop test):

```go
// TestCropLossless_ByteIdentity verifies --lossless copies L0 tiles byte-for-byte
// from the source onto a tile-aligned superset of the requested rect.
func TestCropLossless_ByteIdentity(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)

	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer srcTlr.Close()
	srcL0 := srcTlr.Levels()[0]
	tileW, tileH := srcL0.TileSize.W, srcL0.TileSize.H
	baseW, baseH := srcL0.Size.W, srcL0.Size.H

	// Two cases: an unaligned rect (snaps to a superset) and a tile-aligned rect.
	cases := []struct {
		name             string
		x, y, w, h       int
	}{
		{"unaligned", 300, 200, 400, 400},
		{"aligned", tileW, tileH, 2 * tileW, 2 * tileH},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if c.x+c.w > baseW || c.y+c.h > baseH {
				t.Skipf("rect %v exceeds source %dx%d", c, baseW, baseH)
			}
			out := filepath.Join(t.TempDir(), "lossless.svs")
			cmd := exec.Command(bin, "crop", "--lossless",
				"--rect", itoa4(c.x, c.y, c.w, c.h), "-o", out, src)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("crop --lossless: %v\n%s", err, b)
			}

			// Expected snap (mirror snapRectToTiles).
			snapX := (c.x / tileW) * tileW
			snapY := (c.y / tileH) * tileH
			endX := ((c.x + c.w + tileW - 1) / tileW) * tileW
			endY := ((c.y + c.h + tileH - 1) / tileH) * tileH
			if endX > baseW {
				endX = baseW
			}
			if endY > baseH {
				endY = baseH
			}
			snapW, snapH := endX-snapX, endY-snapY
			stx0, sty0 := snapX/tileW, snapY/tileH

			outTlr, err := opentile.OpenFile(out)
			if err != nil {
				t.Fatalf("open out: %v", err)
			}
			defer outTlr.Close()
			if outTlr.Format() != opentile.FormatSVS {
				t.Errorf("format = %v, want svs", outTlr.Format())
			}
			outL0 := outTlr.Levels()[0]
			if outL0.Size.W != snapW || outL0.Size.H != snapH {
				t.Fatalf("L0 = %dx%d, want snapped %dx%d", outL0.Size.W, outL0.Size.H, snapW, snapH)
			}

			// Shared JPEG tables identical.
			if !bytesEqual(outL0.TilePrefix(), srcL0.TilePrefix()) {
				t.Errorf("TilePrefix differs (shared JPEG tables not preserved)")
			}

			// Every output L0 tile byte-identical to its source tile.
			outTilesX := (snapW + tileW - 1) / tileW
			outTilesY := (snapH + tileH - 1) / tileH
			for oy := 0; oy < outTilesY; oy++ {
				for ox := 0; ox < outTilesX; ox++ {
					ob, err := outL0.Tile(ox, oy)
					if err != nil {
						t.Fatalf("out tile (%d,%d): %v", ox, oy, err)
					}
					sb, err := srcL0.Tile(stx0+ox, sty0+oy)
					if err != nil {
						t.Fatalf("src tile (%d,%d): %v", stx0+ox, sty0+oy, err)
					}
					if !bytesEqual(ob, sb) {
						t.Fatalf("tile (%d,%d) NOT byte-identical to src (%d,%d): %d vs %d bytes",
							ox, oy, stx0+ox, sty0+oy, len(ob), len(sb))
					}
				}
			}

			// Magnification/MPP preserved.
			if md := outTlr.Metadata(); md.MPP.X == 0 || md.Magnification == 0 {
				t.Errorf("lost MPP/Mag: MPP=%v Mag=%v", md.MPP, md.Magnification)
			}
		})
	}
}

func itoa4(a, b, c, d int) string {
	return fmt.Sprintf("%d,%d,%d,%d", a, b, c, d)
}
```

Add `"fmt"` to the test file's imports if not present.

> `bytesEqual` already exists in the integration package (`downsample_test.go`). Do NOT redefine it.

- [ ] **Step 2: Controller runs the lossless integration test** (heavy — fixture-gated)

Controller runs: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestCropLossless_ByteIdentity -count=1 -timeout 30m -v`
Expected: PASS — both cases; every L0 tile byte-identical; `TilePrefix` equal; dims = snapped bbox.

The implementer should compile-check only: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run NONEXIST -count=1` → `ok [no tests to run]`.

- [ ] **Step 3: Commit**

```bash
git add tests/integration/crop_test.go
git commit -m "test(crop): byte-identity integration test for --lossless L0 copy"
```

---

## Final verification (controller)

- [ ] `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -race ./internal/... ./cmd/wsitools/ -count=1 -timeout 30m` green.
- [ ] `go vet ./...` clean.
- [ ] Integration: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run 'TestCrop|TestDownsample' -count=1 -timeout 30m` green (lossless byte-identity + Strategy-A parity + downsample regression).
- [ ] `wsitools crop --help` documents `--lossless`.
- [ ] Dispatch a final whole-branch code reviewer, then use superpowers:finishing-a-development-branch.

## Notes / risks (carried from spec)

- **Tile-write mechanism:** `writeLosslessL0` uses the concurrent `NextReady` drain (mirrors `encodeAndWriteLevel`) so it's correct for any grid size and any `--tile-order`. `srcL0.Tile()` returns a fresh slice per call → no buffer-aliasing across deferred writes.
- **Mixed tile sizes:** lossless L0 keeps the *source* tile size; rebuilt lower levels use `outputTileSize` (256). Valid (each IFD declares its own `TileWidth`); opentile reads per-level.
- **`halveRaster` extraction must not change downsample output** — guarded by the Task 1 Step 6 regression run.
- **Decode redundancy:** the lossless path decodes the snapped region (thumbnail + lower levels) and separately reads raw L0 tiles (verbatim copy). Two reads of the same source tiles; acceptable.
