# Streaming Retile Engine (SP2) — Milestone 5 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route lossy `crop` (svs/tiff/ome-tiff/cog-wsi) through the `internal/retile` engine — `SrcRegion =` the crop rect, identity scale — replacing the cropped-L0-in-RAM raster. Unify crop on `flooredLevelCount`. The last SP2 transform path.

**Architecture:** Generalize M3's engine helpers to a sub-region: `runDownsampleEngine` → `runEngineRetile(srcRegion, …)` (kernel derived: Nearest at identity, Box on downscale), and extract `buildEnginePyramid(srcRegion, outL0, postL0Hook)` cores from M3's `buildPyramid`/`buildPyramidCOGWSI` (which become thin factor wrappers). Both downsample (full-L0) and crop (rect) call the shared cores. A streaming thumbnail (small region read) replaces the raster-based `regenCropThumbnail` on the engine path. `--lossless` crop stays verbatim (byte-exact); DICOM crop stays `derivedsource`.

**Tech Stack:** Go; `internal/retile` (M1–M4) + M2/M3 sinks/encoder/helpers; `internal/codec/jpeg`; opentile-go `ScaledStrips`/`resample`.

**Scope:** M5 = the helper generalization + streaming thumbnail + lossy-crop routing + the `flooredLevelCount` unification. The raster builders (`buildPyramidFromRaster*`, `MaterializeCroppedL0`, `halveRaster`) stay (lossless crop uses them). Per `docs/superpowers/specs/2026-06-18-retile-engine-sp2-m5-design.md`.

---

## Key facts (ground truth)

- **M3 `runDownsampleEngine`** (downsample_engine.go): `func runDownsampleEngine(ctx, slide *opentile.Slide, srcL0, outL0 opentile.Size, levels []retile.LevelSpec, enc retile.TileEncoder, sink retileSink, workers int) error` — hardcodes `SrcRegion={Origin:{0,0},Size:srcL0}` + `Kernel: resample.Box`; wraps sink in a progress bar (`countingSink`/`sumLevelTiles`), unconditional `finish()`.
- **M3 `buildPyramid`** (downsample.go:229): `func buildPyramid(ctx, src *opentile.Slide, w *streamwriter.Writer, factor, quality, workers int, postL0Hook func() error) error` — computes `outL0 = srcL0/factor`, `levels := octaveLevelSpecsFor(outL0, outputTileSize)`, builds the jpeg encoder + `specFor(i) streamwriter.LevelSpec` (Compression jpeg, Photometric 2, SPP 3, BitsPerSample 8/8/8, JPEGTables, NewSubfileType 0, WSIImageType Pyramid), AddLevels L0→postL0Hook→L1.., `newStreamwriterSink(handles)`, calls `runDownsampleEngine`. **`buildPyramidCOGWSI`** (convert_factor.go) is the cog-wsi twin (no postL0Hook; `cogwsiwriter.LevelSpec` IsL0; `newCogwsiSink`).
- **`octaveLevelSpecsFor(outL0 opentile.Size, tile int) []retile.LevelSpec`** uses `flooredLevelCount`.
- **Crop** (crop.go): `runCrop` dispatches to `cropEmitSVS` (svs, crop.go:248) and `cropToTIFF`/`cropToOMETIFF`/`cropToCOGWSI`/`cropToDICOM` (crop_formats.go) via `cropEmitParams`. `cropEmitParams` carries the materialized raster `l0 []byte`, `l0W,l0H` (= ew,eh), `nLevels`, `quality`, `workers`, `lossless`, `stx0,sty0,outTilesX,outTilesY`, `srcL0 *opentile.Level`, `src *opentile.Slide`. The lossy block: `buildPyramidFromRaster(p.ctx, w, p.l0, p.l0W, p.l0H, p.nLevels, p.quality, p.workers, nil)` (cog-wsi: `buildPyramidFromRasterCOGWSI`). Thumbnail: `regenCropThumbnail(w, p.l0, p.l0W, p.l0H, p.quality)` (cog-wsi: `regenCropThumbnailCOGWSI`).
- **`cropPyramidLevels(l0W,l0H,tile)`** (crop.go:89): `n=1; for w/2>=tile && h/2>=tile {w/=2;h/=2;n++}`. **To be replaced by `flooredLevelCount`.**
- **`renderCropThumbnail(l0 []byte, l0W, l0H, quality) (jpegBytes []byte, tw, th int, err error)`** (crop_thumbnail.go): `tw,th = thumbDims(l0W,l0H,thumbLongSide)`; Box-downscale `l0` (l0W×l0H) → tw×th via `otresample.ImageInto`; copy to `image.RGBA`; `jpeg.Encode(quality)`. `thumbDims`, `thumbLongSide` are package symbols.
- **Materialize** = `downscale.MaterializeCroppedL0(ctx, srcL0, outL0buf, ex, ey, ew, eh) error` (crop.go:331). The lossy path's `make([]byte, ew*eh*3)` + this call is what M5 eliminates.
- Imports: `retile`=`internal/retile`, `opentile`=`github.com/wsilabs/opentile-go`, `resample`=`github.com/wsilabs/opentile-go/resample`, `otdecoder`=`github.com/wsilabs/opentile-go/decoder`, `otresample` (alias used in crop_thumbnail.go), `jpegcodec`=`internal/codec/jpeg`, `codec`=`internal/codec`, `outputTileSize`=256.

---

## Task 1: Generalize the engine helpers to a sub-region (behavior-identical refactor)

**Files:**
- Modify: `cmd/wsitools/downsample_engine.go` (`runDownsampleEngine` → `runEngineRetile`)
- Modify: `cmd/wsitools/downsample.go` (`buildPyramid` → wrapper + `buildEnginePyramid` core)
- Modify: `cmd/wsitools/convert_factor.go` (`buildPyramidCOGWSI` → wrapper + `buildEnginePyramidCOGWSI` core)

- [ ] **Step 1: Generalize `runDownsampleEngine` → `runEngineRetile`**

In `downsample_engine.go`, change the signature and body to take a `srcRegion opentile.Region` and derive the kernel:

```go
// runEngineRetile runs one streaming retile pass over srcRegion → outL0. The
// kernel is Nearest at identity scale (crop / factor-1) and Box on a real
// downscale (downsample). It wraps the sink in a progress bar and ALWAYS
// finishes it (joining drains), preferring the Run error.
func runEngineRetile(ctx context.Context, slide *opentile.Slide, srcRegion opentile.Region, outL0 opentile.Size, levels []retile.LevelSpec, enc retile.TileEncoder, sink retileSink, workers int) error {
	kernel := resample.Box
	if outL0 == srcRegion.Size {
		kernel = resample.Nearest // identity read (crop): no resampling
	}

	var progress *mpb.Progress
	var wrapped retileSink = sink
	if !flagQuiet {
		progress = mpb.New(mpb.WithOutput(os.Stderr))
		bar := progress.AddBar(sumLevelTiles(levels),
			mpb.PrependDecorators(decor.Name("encoding "), decor.Percentage(decor.WCSyncSpace)),
			mpb.AppendDecorators(decor.EwmaSpeed(0, "%.0f tiles/s", 30), decor.Name(" ETA "), decor.EwmaETA(decor.ET_STYLE_GO, 30)),
		)
		wrapped = &countingSink{inner: sink, onWrite: bar.Increment}
	}

	runErr := retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: srcRegion,
		OutL0:     outL0,
		Levels:    levels,
		Kernel:    kernel,
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

- [ ] **Step 2: Extract `buildEnginePyramid` core; make `buildPyramid` a wrapper**

In `downsample.go`, replace `buildPyramid` with a thin wrapper + the extracted core:

```go
func buildPyramid(ctx context.Context, src *opentile.Slide, w *streamwriter.Writer, factor, quality, workers int, postL0Hook func() error) error {
	srcL0 := src.Levels()[0]
	srcSize := opentile.Size{W: srcL0.Size.W, H: srcL0.Size.H}
	outL0 := opentile.Size{W: srcSize.W / factor, H: srcSize.H / factor}
	if outL0.W <= 0 || outL0.H <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outL0.W, outL0.H, factor)
	}
	return buildEnginePyramid(ctx, src, w, opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: srcSize}, outL0, quality, workers, postL0Hook)
}

// buildEnginePyramid builds a streamwriter jpeg pyramid by streaming srcRegion
// through the retile engine to outL0 (octave-floored levels). postL0Hook runs
// after L0's AddLevel, before L1 (the thumbnail-IFD interleave). Shared by
// downsample (full-L0 region, outL0=L0/factor) and crop (rect region, identity).
func buildEnginePyramid(ctx context.Context, slide *opentile.Slide, w *streamwriter.Writer, srcRegion opentile.Region, outL0 opentile.Size, quality, workers int, postL0Hook func() error) error {
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
	return runEngineRetile(ctx, slide, srcRegion, outL0, levels, &codecTileEncoder{enc: enc}, sink, workers)
}
```

This is byte-identical to M3's `buildPyramid` for downsample (same levels, encoder, kernel — `outL0 != srcSize` for factor>1 → Box). Confirm `fmt`, `strconv`, `tiff`, `opentile`, `streamwriter`, `jpegcodec`, `codec` are imported (they were used by the old `buildPyramid`).

- [ ] **Step 3: Same extraction for cog-wsi in `convert_factor.go`**

Replace `buildPyramidCOGWSI` with a wrapper + `buildEnginePyramidCOGWSI(ctx, slide, w *cogwsiwriter.Writer, srcRegion opentile.Region, outL0 opentile.Size, quality, workers int) error` core (no postL0Hook — cog-wsi has no interleaved thumbnail IFD). Mirror Step 2: the wrapper computes `srcSize`/`outL0=srcSize/factor` and calls the core with the full-L0 region; the core builds `octaveLevelSpecsFor(outL0)` levels + `cogwsiwriter.LevelSpec` (Compression `enc.TIFFCompressionTag()`, Photometric 2, SPP 3, BitsPerSample 8/8/8, JPEGTables, IsL0 i==0) + `newCogwsiSink(handles, levels)` + `runEngineRetile(srcRegion, outL0, …)`.

- [ ] **Step 4: Build + downsample regression**

Run: `go build ./... 2>&1 | grep -v 'duplicate librar'` → clean.
Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'Downsample|Factor|TestCountingSink' 2>&1 | grep -v 'duplicate librar' | tail -8` → PASS (behavior-identical; downsample must stay green — if a downsample test fails, the extraction changed behavior; STOP and diff).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/downsample_engine.go cmd/wsitools/downsample.go cmd/wsitools/convert_factor.go
git commit -m "refactor(retile): generalize engine helpers to a sub-region (runEngineRetile + buildEnginePyramid cores)"
```

---

## Task 2: `streamCropThumbnail` — raster-free crop thumbnail

**Files:**
- Modify: `cmd/wsitools/crop_thumbnail.go`
- Test: `cmd/wsitools/crop_thumbnail_test.go` (create or extend)

- [ ] **Step 1: Write the failing test**

Create/extend `cmd/wsitools/crop_thumbnail_test.go` — a fixture-gated test that the streaming thumbnail matches the raster thumbnail's dims and is a decodable JPEG:

```go
package main

import (
	"bytes"
	stdjpeg "image/jpeg"
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/decoder/all"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

func TestStreamCropThumbnailDimsAndDecode(t *testing.T) {
	path := filepath.Join(cropTestDir(), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	slide, err := opentile.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer slide.Close()
	// Crop rect: a 1024×1024 region at (100,100), within the small-region L0.
	rect := opentile.Region{Origin: opentile.Point{X: 100, Y: 100}, Size: opentile.Size{W: 1024, H: 1024}}
	jpegBytes, tw, th, err := streamCropThumbnail(slide, rect, 1024, 1024, 80)
	if err != nil {
		t.Fatalf("streamCropThumbnail: %v", err)
	}
	// Dims must equal thumbDims(ew,eh,thumbLongSide) — the same sizing the raster path uses.
	wantW, wantH := thumbDims(1024, 1024, thumbLongSide)
	if tw != wantW || th != wantH {
		t.Errorf("thumb dims = %d×%d, want %d×%d", tw, th, wantW, wantH)
	}
	img, err := stdjpeg.Decode(bytes.NewReader(jpegBytes))
	if err != nil {
		t.Fatalf("thumbnail not a decodable JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != tw || b.Dy() != th {
		t.Errorf("decoded thumb = %v, want %d×%d", b.Bounds(), tw, th)
	}
}

func cropTestDir() string {
	if d := os.Getenv("WSI_TOOLS_TESTDIR"); d != "" {
		return d
	}
	return "../../sample_files"
}
```

NOTE: if a `testDir()`-style helper already exists in package main, reuse it instead of `cropTestDir` (grep first; avoid a duplicate).

- [ ] **Step 2: Run to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestStreamCropThumbnail -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: streamCropThumbnail`.

- [ ] **Step 3: Implement `streamCropThumbnail` + share the encode**

In `crop_thumbnail.go`, factor the "RGB raster → thumbnail JPEG" half of `renderCropThumbnail` into a shared `encodeThumbnailJPEG(rgb []byte, tw, th, quality int) ([]byte, error)` (the `image.RGBA` copy + `jpeg.Encode`), have `renderCropThumbnail` call it, and add the streaming variant:

```go
// streamCropThumbnail regenerates the crop thumbnail WITHOUT a full raster: it
// reads the crop rect downscaled directly to thumbnail dims via ScaledStrips
// (Box), then JPEG-encodes. ew,eh are the crop L0 dims (for sizing). Used by the
// streaming (engine) crop path, which holds no decoded raster.
func streamCropThumbnail(slide *opentile.Slide, rect opentile.Region, ew, eh, quality int) (jpegBytes []byte, tw, th int, err error) {
	tw, th = thumbDims(ew, eh, thumbLongSide)
	it := slide.Pyramid(0).ScaledStrips(rect, opentile.Size{W: tw, H: th}, th, opentile.WithStripKernel(otresample.Box))
	defer it.Close()
	img, err := it.Next()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("thumbnail region read: %w", err)
	}
	// img is a tw×th RGB strip (one strip covers the whole small image). Tighten
	// to a contiguous tw*th*3 buffer if the decoder stride exceeds tw*3.
	rgb := tightThumbRGB(img, tw, th)
	jpegBytes, err = encodeThumbnailJPEG(rgb, tw, th, quality)
	if err != nil {
		return nil, 0, 0, err
	}
	return jpegBytes, tw, th, nil
}

// tightThumbRGB returns a contiguous tw*th*3 RGB buffer from a decoder.Image
// (handles stride padding and the RGBA→RGB case defensively).
func tightThumbRGB(img *otdecoder.Image, tw, th int) []byte {
	out := make([]byte, tw*th*3)
	bpp := 3
	if img.Format == otdecoder.PixelFormatRGBA {
		bpp = 4
	}
	for y := 0; y < th; y++ {
		srow := img.Pix[y*img.Stride:]
		drow := out[y*tw*3:]
		for x := 0; x < tw; x++ {
			drow[x*3+0] = srow[x*bpp+0]
			drow[x*3+1] = srow[x*bpp+1]
			drow[x*3+2] = srow[x*bpp+2]
		}
	}
	return out
}
```

VERIFY the `ScaledStrips` + `WithStripKernel`/`otresample.Box` API against how `internal/retile/retile.go` and `crop_thumbnail.go` already use them (strip iterator `Next()/Close()`, `decoder.Image.{Pix,Stride,Width,Height,Format}`). `it.Next()` with `stripHeight=th` yields the full image in one strip; if the iterator instead yields multiple strips, loop and stitch into `rgb` by row offset. Adjust to the real API; the contract is: produce a tight tw×th RGB buffer. If `ScaledStrips`/`WithStripKernel` signatures differ, mirror the engine's usage exactly.

Also refactor `renderCropThumbnail` to call `encodeThumbnailJPEG` (extract the `image.RGBA`-copy + `jpeg.Encode` block verbatim into it; `renderCropThumbnail` keeps doing its own `otresample.ImageInto` downscale of the full raster, then calls `encodeThumbnailJPEG`).

- [ ] **Step 4: Run to verify it passes**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestStreamCropThumbnail -v 2>&1 | grep -v 'duplicate librar'` → PASS.
Run: `go test ./cmd/wsitools/ -run 'Crop|Thumb' 2>&1 | grep -v 'duplicate librar' | tail -5` → existing crop/thumbnail tests still PASS (the `renderCropThumbnail` refactor is behavior-identical).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/crop_thumbnail.go cmd/wsitools/crop_thumbnail_test.go
git commit -m "feat(crop): streamCropThumbnail — raster-free thumbnail via a small region read"
```

---

## Task 3: Route lossy crop through the engine + unify level count

**Files:**
- Modify: `cmd/wsitools/crop.go` (`runCrop`/`cropEmitSVS`: don't materialize on lossy; pass rect; `flooredLevelCount`)
- Modify: `cmd/wsitools/crop_formats.go` (`cropToTIFF`/`cropToOMETIFF`/`cropToCOGWSI`: lossy → engine)
- Test: controller-run integration

- [ ] **Step 1: Add `ex,ey` to `cropEmitParams`; materialize only for lossless**

In `crop.go`'s `cropEmitParams` (crop_formats.go), add `ex, ey int` (the effective rect origin). In `runCrop`, set `p.ex, p.ey` to the effective origin (`ex,ey` — snapped for lossless, requested `x,y` for lossy). **Materialize `p.l0` ONLY when `lossless`** (the lossy engine path doesn't need it); leave `p.l0 = nil` for lossy. (The lossy emit paths must no longer read `p.l0`.) Keep `p.l0W,p.l0H = ew,eh`.

- [ ] **Step 2: Unify the level count on `flooredLevelCount`**

Replace every `cropPyramidLevels(ew, eh, outputTileSize)` (in `runCrop`/`cropEmitSVS` — and wherever `p.nLevels` is set) with `flooredLevelCount(ew, eh, outputTileSize)`. This is the unified count for BOTH the lossy (engine) and `--lossless` (raster lower-level rebuild) paths. Delete `cropPyramidLevels` (or leave it unused — prefer deleting; grep for other callers first).

- [ ] **Step 3: Route the lossy branch to the engine in each emit path**

In `cropToTIFF` (and identically `cropToOMETIFF`, `cropEmitSVS`), replace the lossy `else` branch:
```go
	} else {
		if err := buildPyramidFromRaster(p.ctx, w, p.l0, p.l0W, p.l0H, p.nLevels, p.quality, p.workers, nil); err != nil {
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
```
with the engine path (rect region, identity, streaming thumbnail interleaved via postL0Hook):
```go
	} else {
		rect := opentile.Region{Origin: opentile.Point{X: p.ex, Y: p.ey}, Size: opentile.Size{W: p.l0W, H: p.l0H}}
		var postL0Hook func() error
		if !p.noAssociated {
			postL0Hook = func() error {
				jpegBytes, tw, th, terr := streamCropThumbnail(p.src, rect, p.l0W, p.l0H, p.quality)
				if terr != nil {
					return terr
				}
				return w.AddStripped(streamwriter.StrippedSpec{
					Width: uint32(tw), Height: uint32(th), RowsPerStrip: uint32(th),
					BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3,
					Photometric: 6, Compression: tiff.CompressionJPEG,
					StripBytes: jpegBytes, NewSubfileType: 0, WSIImageType: tiff.WSIImageTypeThumbnail,
				})
			}
		}
		if err := buildEnginePyramid(p.ctx, p.src, w, rect, opentile.Size{W: p.l0W, H: p.l0H}, p.quality, p.workers, postL0Hook); err != nil {
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
```
And in the trailing associated loop, the lossy path now emits the thumbnail via the hook, so SKIP the `regenCropThumbnail` call when `!p.lossless` (the hook already did it); the lossless path keeps calling `regenCropThumbnail`. Concretely, guard the associated loop's thumbnail re-gen with `if p.lossless` (lossy already handled it via postL0Hook), and keep `writeOneAssociated` for label/macro in both.

The `AddStripped` thumbnail spec above is the SAME one `regenCropThumbnail` uses (mirror its fields exactly). To avoid duplicating it, you MAY add a helper `addCropThumbnailStripped(w *streamwriter.Writer, jpegBytes []byte, tw, th int) error` and call it from both `regenCropThumbnail` and the postL0Hook.

For **`cropToCOGWSI`**: cog-wsi has no interleaved thumbnail IFD (the thumbnail is an associated image added after the pyramid). So: lossy branch → `buildEnginePyramidCOGWSI(p.ctx, p.src, w, rect, opentile.Size{p.l0W,p.l0H}, p.quality, p.workers)` (no postL0Hook), and AFTER it, regenerate the thumbnail via the streaming read: `jpegBytes, tw, th, _ := streamCropThumbnail(p.src, rect, p.l0W, p.l0H, p.quality)` then `w.AddAssociated(cogwsiwriter.AssociatedSpec{Type: tiff.WSIImageTypeThumbnail, …, Bytes: jpegBytes})` (mirror `regenCropThumbnailCOGWSI`'s fields). Keep the lossless branch on `buildPyramidFromRasterCOGWSI` + `regenCropThumbnailCOGWSI(w, p.l0, …)`.

NOTE: `cropToDICOM` is UNCHANGED (stays on `derivedsource`). Match each file's exact variable names + error handling; the overlapping concern is that the lossy path no longer references `p.l0` (nil on lossy).

- [ ] **Step 4: Build + existing crop tests**

Run: `go build ./... 2>&1 | grep -v 'duplicate librar'` → clean.
Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'Crop' 2>&1 | grep -v 'duplicate librar' | tail -12` → PASS. Tests asserting an exact crop level count will now see the unified `flooredLevelCount` value (one more level in some cases — the approved change); update those expectations to `flooredLevelCount(ew,eh,256)` and report each (test, old→new count). A `--lossless` byte-identity test MUST still pass (the verbatim L0 path is unchanged); if it fails, that's a real regression — STOP.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/crop.go cmd/wsitools/crop_formats.go
git commit -m "feat(crop): route lossy crop through the engine; unify level count on flooredLevelCount"
```

- [ ] **Step 6: CONTROLLER integration (lossy crop, all formats)**

```bash
make build
FIX=$(pwd)/sample_files/svs/CMU-1.svs
for tgt in "" ; do :; done   # SVS default
echo "=== lossy crop SVS (a 4096×4096 region) ==="
./bin/wsitools crop "$FIX" --rect 10000,10000,4096,4096 -o /tmp/m5-crop.svs -f 2>&1 | grep -v 'duplicate librar'
./bin/wsitools info /tmp/m5-crop.svs 2>&1 | grep -v 'duplicate librar' | grep -E '  L[0-9]| MPP|Magnif|thumbnail'
./bin/wsitools validate /tmp/m5-crop.svs 2>&1 | grep -v 'duplicate librar' | tail -1
echo "=== --lossless crop still byte-exact (unchanged path) ==="
./bin/wsitools crop "$FIX" --rect 10000,10000,4096,4096 --lossless -o /tmp/m5-crop-ll.svs -f 2>&1 | grep -v 'duplicate librar'
./bin/wsitools validate /tmp/m5-crop-ll.svs 2>&1 | grep -v 'duplicate librar' | tail -1
```
Expected: lossy crop succeeds, L0 = 4096×4096, octave-floored (`flooredLevelCount`) levels, thumbnail at IFD 1, MPP/mag preserved, validate clean. `--lossless` still works (snapped, byte-exact). Verify the exact `--rect` flag/syntax against `crop --help` and adjust.

---

## Task 4: Verification — pixel-equivalence + lossless byte-identity + memory + race (controller)

**Files:** none (verification only).

- [ ] **Step 1: Pixel-equivalence — cropped L0 vs source region**

```bash
FIX=$(pwd)/sample_files/svs/CMU-1.svs
# The crop's L0 should equal the source's same rect (re-encoded) ≈ jpeg noise.
./bin/wsitools region "$FIX" --rect 10500,10500,512,512 --level 0 -o /tmp/m5-src.png -f 2>&1 | grep -v 'duplicate librar'
# In the crop output, that source rect is at (10500-10000, 10500-10000)=(500,500).
./bin/wsitools region /tmp/m5-crop.svs --rect 500,500,512,512 --level 0 -o /tmp/m5-new.png -f 2>&1 | grep -v 'duplicate librar'
python3 - <<'PY'
from PIL import Image, ImageChops, ImageStat
a=Image.open("/tmp/m5-src.png").convert("RGB"); b=Image.open("/tmp/m5-new.png").convert("RGB")
print("crop L0 region mean abs diff:", [round(x,2) for x in ImageStat.Stat(ImageChops.difference(a,b)).mean])
PY
```
Expected: small mean (~jpeg re-encode noise) — confirms the rect is read at the correct offset and composited correctly. A large diff means the SrcRegion offset is wrong — STOP and debug.

- [ ] **Step 2: `--lossless` byte-identity regression**

Confirm the lossless crop's L0 tiles are byte-identical to the source's (the existing crop byte-identity test covers this; re-run it explicitly):
```bash
WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'Lossless|ByteExact|CropLossless' -v 2>&1 | grep -v 'duplicate librar' | tail -8
```
Expected: PASS (unchanged path).

- [ ] **Step 3: Bounded memory (no cropped raster on the lossy path)**

```bash
grep -n "MaterializeCroppedL0" cmd/wsitools/crop.go cmd/wsitools/crop_formats.go
```
Expected: `MaterializeCroppedL0` is now called only on the `lossless` path (not in the lossy emit). Optionally `/usr/bin/time -l` a large lossy crop to confirm flat RSS.

- [ ] **Step 4: Full race suite**

```bash
WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -race -count=1 -timeout 30m ./internal/retile/ ./cmd/wsitools/ 2>&1 | grep -v 'duplicate librar' | tail -4
```
Expected: all PASS.

---

## Final verification + finish

Dispatch a final code reviewer over `main..HEAD`, then use **superpowers:finishing-a-development-branch**. Branch: `feat/retile-engine-m5` off `main`.

**M5 acceptance:**
- `runEngineRetile` (sub-region, kernel-derived) + `buildEnginePyramid`/COGWSI cores; downsample behavior-identical (M3 tests green).
- Lossy crop (svs/tiff/ome-tiff/cog-wsi) routes through the engine; L0 = rect; level count == `flooredLevelCount`; thumbnail present (svs at IFD 1); metadata preserved; validate clean; pixel-equivalent to the source rect.
- `--lossless` crop still byte-identical; DICOM crop unchanged.
- No cropped-raster allocation on the lossy path; full `-race` green.

---

## Self-Review

**Spec coverage:**
- Mechanism (M3 with SrcRegion=rect, Nearest) → Task 1 (`runEngineRetile` kernel-derive) + Task 3 (rect region). ✓
- Unify on `flooredLevelCount` (both paths) → Task 3 Step 2. ✓
- Lossy→engine, lossless→unchanged (flag-gated) → Task 3 Step 3 (lossy `else` only; lossless branch untouched except nLevels). ✓
- Streaming thumbnail → Task 2 + Task 3 (postL0Hook / cogwsi after-pyramid). ✓
- Generalize the M3 helper → Task 1 (`runEngineRetile` + `buildEnginePyramid` cores; downsample wrappers). ✓
- DICOM crop unchanged; raster builders stay → noted in Task 3 (cropToDICOM untouched; lossless uses raster). ✓
- Testing (pixel-equiv, lossless byte-identity, level-count, memory, race) → Tasks 2/4. ✓

**Placeholder scan:** none. The "VERIFY ScaledStrips API / mirror the engine's usage", "reuse testDir if present", and "update level-count expectations" notes are explicit verification steps with defined actions.

**Type consistency:** `runEngineRetile(ctx, slide, srcRegion opentile.Region, outL0 opentile.Size, levels, enc, sink, workers)`, `buildEnginePyramid(ctx, slide, w, srcRegion, outL0, quality, workers, postL0Hook)`, `buildEnginePyramidCOGWSI(...)`, `streamCropThumbnail(slide, rect, ew, eh, quality)→(jpegBytes,tw,th,err)`, `encodeThumbnailJPEG(rgb,tw,th,quality)`, `cropEmitParams.{ex,ey}` — consistent across tasks; the downsample wrappers (`buildPyramid`/`buildPyramidCOGWSI`) keep their existing signatures so M3 callers are untouched.

**Risk:** (1) the `ScaledStrips` single-strip thumbnail read — if the iterator yields multiple strips, Task 2 must stitch (flagged). (2) crop level-count change breaks exact-count tests — Task 3 updates them to `flooredLevelCount` (approved). (3) the engine read isn't byte-exact, so lossy crop L0 differs ~0.1/255 from the source — fine for lossy; lossless stays on the verbatim path (unaffected).
