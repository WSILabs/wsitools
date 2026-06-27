# `convert --tile-size` + `--jobs` Removal — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a single `--tile-size N` flag to `wsitools convert` that replaces `--dzi-tile-size`, governs output tiling for every raster/re-encode target, and defaults to the source's tile size when unset; and remove the redundant `--jobs` alias of `--workers`.

**Architecture:** A `resolveTileSize` helper turns `--tile-size` (or unset → source L0 tile width → 256) into the output tile edge, threaded into every re-encode path (the retile engine already takes an arbitrary tile size). Forced re-encode (tile-size ≠ source on a tile-copyable input) defaults its codec to the source's own codec. `--to bif` errors; `--to dicom` re-tiles in `internal/derivedsource`. Separately, `--workers` becomes the one canonical worker flag.

**Tech Stack:** Go, cobra CLI, the `internal/retile` engine, `internal/derivedsource` (DICOM), `internal/dzi`/`szi`.

**Spec:** `docs/superpowers/specs/2026-06-27-convert-tile-size-design.md`

**Base:** branch `feat/convert-tile-size`, rebased on `main` (opentile-go v0.60.1 + the photometric fix). Build the CLI binary once for integration tests: `make build` (→ `bin/wsitools`); the `cmd/wsitools` integration tests use `./bin/wsitools` and are gated by `WSI_TOOLS_TESTDIR=$(pwd)/sample_files`.

---

## File Structure

| File | Responsibility / change |
|---|---|
| `cmd/wsitools/workers.go` | **delete** — `resolveWorkers` no longer needed once `--jobs` is gone |
| `cmd/wsitools/convert.go` | flag `dzi-tile-size`→`tile-size` (`cvDZITileSize`→`cvTileSize`, default `0`); drop `cvJobs`/`--jobs`; drop the `resolveWorkers` call; `--to bif` guard |
| `cmd/wsitools/crop.go`, `transcode.go` | drop `--jobs`/alias var; `--workers` directly |
| `cmd/wsitools/downsample.go` | `--workers` becomes primary (default `runtime.NumCPU()`), drop `--jobs`; replace `outputTileSize` |
| `cmd/wsitools/convert_shared.go` | new `resolveTileSize` + `reencodeCodecFor` helpers; `tileCopyEligible` forced-re-encode rule |
| `cmd/wsitools/convert_tiff.go`, `convert_stitched.go`, `convert_factor.go`, `convert_ife.go` | use `resolveTileSize` for output tiling |
| `cmd/wsitools/convert_dzi.go`, `convert_szi.go`, `dzi_lossless.go` | unified flag into DZI/SZI + `losslessDZIConfig` |
| `internal/derivedsource/retile_source.go` (new) | re-tiling source level for DICOM `--tile-size` |
| `cmd/wsitools/convert_factor.go` (DICOM branch) | pass the resolved tile size to the DICOM path |
| `cmd/wsitools/convert_tile_size_test.go` (new), `tests/integration/tile_size_test.go` (new) | unit + CLI coverage |

`outputTileSize = 256` (downsample.go:49) is removed; 256 survives only as the `resolveTileSize` fallback.

**Task order:** Task 1 (`--jobs`) is independent and lands first. Tasks 2–6 are the `--tile-size` core. Task 7 (DICOM re-tiling) is the largest and isolated. Task 8 is integration + the follow-up issue.

---

## Task 1: Remove the `--jobs` alias (`--workers` canonical)

**Files:**
- Delete: `cmd/wsitools/workers.go`
- Modify: `cmd/wsitools/convert.go`, `crop.go`, `transcode.go`, `downsample.go`
- Test: existing suites must stay green; no new test (pure flag cleanup).

- [ ] **Step 1: Delete `resolveWorkers` and its file**

```bash
git rm cmd/wsitools/workers.go
```

- [ ] **Step 2: `convert.go` — drop the alias**

Remove the `cvJobs` var declaration and the flag line `convertCmd.Flags().IntVar(&cvJobs, "jobs", 0, "alias of --workers")`. Replace the resolve line (`convert.go:110`):
```go
	cvWorkers = resolveWorkers(cvWorkers, cmd.Flags().Changed("workers"), cvJobs, cmd.Flags().Changed("jobs"))
```
with nothing (delete it) — `cvWorkers` is already the flag value. Keep `convertCmd.Flags().IntVar(&cvWorkers, "workers", 0, "pipeline workers (0 = GOMAXPROCS)")`.

- [ ] **Step 3: `transcode.go` — drop the alias**

Remove `transcodeCmd.Flags().IntVar(&cvJobs, "jobs", 0, "alias of --workers")` (line 34). `cvJobs` is shared with convert; once both alias lines are gone, remove the `cvJobs` declaration (wherever it lives — `grep -rn "cvJobs" cmd/wsitools` to find and delete the `var`).

- [ ] **Step 4: `crop.go` — drop the alias**

Remove `cropCmd.Flags().IntVar(&cropJobs, "jobs", 0, "alias of --workers")` (line 79), the `cropJobs` var, and change the resolve (line 64):
```go
		workers := resolveWorkers(cropWorkers, cmd.Flags().Changed("workers"), cropJobs, cmd.Flags().Changed("jobs"))
```
to:
```go
		workers := cropWorkers
```

- [ ] **Step 5: `downsample.go` — flip primary to `--workers`**

Replace lines 103–104:
```go
	downsampleCmd.Flags().IntVar(&dsJobs, "jobs", runtime.NumCPU(), "worker goroutines")
	downsampleCmd.Flags().IntVar(&dsWorkers, "workers", 0, "alias of --jobs")
```
with a single flag (keep the `NumCPU` default behavior):
```go
	downsampleCmd.Flags().IntVar(&dsWorkers, "workers", runtime.NumCPU(), "worker goroutines")
```
Delete the `dsJobs` var (line 61) and the resolve line (117):
```go
	dsJobs = resolveWorkers(dsJobs, cmd.Flags().Changed("jobs"), dsWorkers, cmd.Flags().Changed("workers"))
```
Then replace every remaining `dsJobs` reference (the slog `"jobs", dsJobs` at 125 and the call arg at 155) with `dsWorkers`. Run `grep -n dsJobs cmd/wsitools/downsample.go` and confirm zero remain.

- [ ] **Step 6: Build + vet (no stale references)**

Run:
```bash
go build ./cmd/wsitools/ 2>&1 | grep -v "duplicate librar"
grep -rn "resolveWorkers\|cvJobs\|cropJobs\|dsJobs\|\"jobs\"" cmd/wsitools/*.go | grep -v _test
```
Expected: builds clean; the grep returns nothing.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor(cli): remove --jobs alias; --workers is canonical

Drop the redundant --jobs alias across convert/crop/downsample/transcode and
delete resolveWorkers/workers.go. downsample's primary flips to --workers
(default NumCPU). Trims the convert option list."
```

---

## Task 2: `resolveTileSize` + `reencodeCodecFor` helpers + flag rename

**Files:**
- Modify: `cmd/wsitools/convert.go` (flag rename), `cmd/wsitools/convert_shared.go` (helpers)
- Test: `cmd/wsitools/convert_tile_size_test.go` (new)

- [ ] **Step 1: Rename the flag in `convert.go`**

Replace the var `cvDZITileSize` declaration default and the flag registration (convert.go:96):
```go
	convertCmd.Flags().IntVar(&cvDZITileSize, "dzi-tile-size", 256, "DZI/SZI tile size in pixels")
```
with:
```go
	convertCmd.Flags().IntVar(&cvTileSize, "tile-size", 0, "output tile size in pixels (0 = match source; replaces --dzi-tile-size)")
```
Rename the `cvDZITileSize` var to `cvTileSize` everywhere it's declared/used (`grep -rln cvDZITileSize cmd/wsitools` — convert.go, convert_dzi.go, convert_szi.go). Default is now `0` (unset).

- [ ] **Step 2: Write the failing unit test**

Create `cmd/wsitools/convert_tile_size_test.go`:
```go
package main

import "testing"

func TestResolveTileSize(t *testing.T) {
	cases := []struct {
		name              string
		srcL0TileW, flag  int
		want              int
	}{
		{"flag set wins", 256, 512, 512},
		{"unset matches source", 240, 0, 240},
		{"unset, no source tile → 256", 0, 0, 256},
		{"flag set even with no source tile", 0, 1024, 1024},
	}
	for _, c := range cases {
		if got := resolveTileSize(c.srcL0TileW, c.flag); got != c.want {
			t.Errorf("%s: resolveTileSize(%d,%d) = %d, want %d", c.name, c.srcL0TileW, c.flag, got, c.want)
		}
	}
}
```

- [ ] **Step 3: Run it to confirm it fails**

Run: `go test ./cmd/wsitools/ -run TestResolveTileSize 2>&1 | grep -v "duplicate librar"`
Expected: FAIL — `undefined: resolveTileSize`.

- [ ] **Step 4: Implement `resolveTileSize` in `convert_shared.go`**

```go
// resolveTileSize returns the output tile edge: the user's --tile-size when >0,
// else the source level-0 tile width, else 256 when the source has no usable
// square tile geometry.
func resolveTileSize(srcL0TileW, flag int) int {
	if flag > 0 {
		return flag
	}
	if srcL0TileW > 0 {
		return srcL0TileW
	}
	return 256
}
```

- [ ] **Step 5: Run the test to confirm it passes**

Run: `go test ./cmd/wsitools/ -run TestResolveTileSize 2>&1 | grep -v "duplicate librar"`
Expected: PASS.

- [ ] **Step 6: Add `reencodeCodecFor` + its test**

First widen the test file's import block (replace `import "testing"` from Step 2 with):
```go
import (
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)
```
Then append the test function:
```go
func TestReencodeCodecFor(t *testing.T) {
	// codecFlag wins.
	if name, err := reencodeCodecFor(source.CompressionJPEG2000, "jpeg"); err != nil || name != "jpeg" {
		t.Errorf("explicit flag: got (%q,%v), want (jpeg,nil)", name, err)
	}
	// default = source codec.
	if name, err := reencodeCodecFor(source.CompressionJPEG2000, ""); err != nil || name != "jpeg2000" {
		t.Errorf("jp2k source: got (%q,%v), want (jpeg2000,nil)", name, err)
	}
	if name, err := reencodeCodecFor(source.CompressionJPEG, ""); err != nil || name != "jpeg" {
		t.Errorf("jpeg source: got (%q,%v), want (jpeg,nil)", name, err)
	}
	// no encoder for source codec, no flag → error.
	if _, err := reencodeCodecFor(source.CompressionLZW, ""); err == nil {
		t.Error("LZW source, no --codec: expected error, got nil")
	}
}
```
Then implement in `convert_shared.go` (uses `codec.Lookup`; `source.Compression.String()` already yields registry names):
```go
// reencodeCodecFor picks the codec for a forced re-encode (e.g. --tile-size
// differs from the source tiling). An explicit codecFlag always wins. Otherwise
// the source's own codec is preserved — source.Compression.String() yields the
// codec-registry name ("jpeg"/"jpeg2000"/"webp"/"avif"/"jpegxl"/"htj2k"). If the
// source codec has no wsitools encoder (LZW/Deflate/None/…), it errors asking for
// an explicit --codec.
func reencodeCodecFor(src source.Compression, codecFlag string) (string, error) {
	if codecFlag != "" {
		return codecFlag, nil
	}
	name := src.String()
	if _, err := codec.Lookup(name); err != nil {
		return "", fmt.Errorf("re-encoding required (e.g. --tile-size differs from source) but no encoder for source codec %q; pass --codec", name)
	}
	return name, nil
}
```
Ensure `convert_shared.go` imports `fmt`, `github.com/wsilabs/wsitools/internal/codec`, and `github.com/wsilabs/wsitools/internal/source`.

- [ ] **Step 7: Run both unit tests**

Run: `go test ./cmd/wsitools/ -run "TestResolveTileSize|TestReencodeCodecFor" -v 2>&1 | grep -v "duplicate librar" | grep -E "PASS|FAIL|ok"`
Expected: both PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/convert_shared.go cmd/wsitools/convert_tile_size_test.go cmd/wsitools/convert_dzi.go cmd/wsitools/convert_szi.go
git commit -m "feat(convert): --tile-size flag (replaces --dzi-tile-size) + resolve helpers

Add resolveTileSize (flag>0 | source tile | 256) and reencodeCodecFor (source-
codec default, error if no encoder). Rename --dzi-tile-size → --tile-size
(cvDZITileSize → cvTileSize, default 0 = unset)."
```

---

## Task 3: Wire `resolveTileSize` into TIFF-family + ife re-encode paths

Replace `outputTileSize` (256) and bare source-tile reads with `resolveTileSize(srcL0TileW, cvTileSize)`.

**Files:** `cmd/wsitools/convert_stitched.go`, `convert_factor.go`, `downsample.go`, `convert_ife.go`, `convert_tiff.go`.

- [ ] **Step 1: `convert_stitched.go` — both functions**

In `convertStitchedCOGWSI` and `convertStitchedTIFF`, replace:
```go
	tile := l0.TileSize.W
	if tile <= 0 {
		tile = 256
	}
```
with:
```go
	tile := resolveTileSize(l0.TileSize.W, cvTileSize)
```
(`convertTranscodeTIFF` keeps per-source-level tile sizes — it preserves the source pyramid structure; `--tile-size` is applied via the `transcodeLevel` change in Step 4, so leave `convertTranscodeTIFF`'s `levels[i].TileW` reads but resolve them — see Step 5.)

- [ ] **Step 2: `downsample.go` — remove the constant**

Delete `outputTileSize = 256` (line 49). The downsample level writers (`specFor` closure ~line 264 and `encodeAndWriteLevel` ~line 434, plus the COG-WSI siblings in convert_factor.go) take `outputTileSize`; thread a resolved value instead. In each downsample/factor body that currently uses `outputTileSize`, compute once near the top from the open source:
```go
	outTile := resolveTileSize(srcL0TileW, cvTileSize) // srcL0TileW from the opened source's L0 TileSize().X
```
and replace every `outputTileSize` usage with `outTile`. (`srcL0TileW` is available where the source is opened — `src.Levels()[0].TileSize().X` or, in `downsampleToSVS`, from the `opentile.OpenFile` slide's `Levels()[0].TileSize.W`.)

- [ ] **Step 3: `convert_factor.go` — COG-WSI + level writers**

Same as Step 2 for `encodeAndWriteLevelCOGWSI` and the `cogwsiwriter.LevelSpec` loop (`convert_factor.go:1050`, `:1109`): replace `outputTileSize` with the resolved `outTile`. The factor functions receive the source; resolve `outTile` once and pass it down (add an `outTile int` parameter to `encodeAndWriteLevel`/`encodeAndWriteLevelCOGWSI` rather than reading a global).

- [ ] **Step 4: `convert_tiff.go transcodeLevel` — resolved tile**

`transcodeLevel` currently uses `lvl.TileSize().X/Y` for the output. Resolve against the flag:
```go
	tileW := resolveTileSize(lvl.TileSize().X, cvTileSize)
	tileH := tileW
```
(square output tiles). Use `tileW/tileH` for the `LevelSpec.TileWidth/Height` and the encoder geometry.

- [ ] **Step 5: `convert_ife.go` — resolved tile**

Replace `octaveLevelSpecsFor(outL0, outputTileSize)` (convert_ife.go:113) with `octaveLevelSpecsFor(outL0, resolveTileSize(srcL0TileW, cvTileSize))`, where `srcL0TileW` is the IFE source L0 tile width (`slide.Levels()[0].TileSize.W`).

- [ ] **Step 6: Build**

Run: `go build ./cmd/wsitools/ 2>&1 | grep -v "duplicate librar"; grep -rn "outputTileSize" cmd/wsitools/*.go | grep -v _test`
Expected: builds; no `outputTileSize` references remain.

- [ ] **Step 7: Manual smoke (unset → match source; set → override)**

```bash
make build
WSI=$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs
./bin/wsitools convert --to tiff --codec jpeg -f -o /tmp/ts_match.tiff "$WSI"
./bin/wsitools dump-ifds --raw /tmp/ts_match.tiff | grep -m1 TileWidth   # ~ source tile width
./bin/wsitools convert --to tiff --codec jpeg --tile-size 512 -f -o /tmp/ts_512.tiff "$WSI"
./bin/wsitools dump-ifds --raw /tmp/ts_512.tiff | grep -m1 TileWidth     # 512
```
Expected: first ≈ source tile width; second `TileWidth = 512`.

- [ ] **Step 8: Commit**

```bash
git add cmd/wsitools/convert_stitched.go cmd/wsitools/convert_factor.go cmd/wsitools/downsample.go cmd/wsitools/convert_ife.go cmd/wsitools/convert_tiff.go
git commit -m "feat(convert): output tile size = resolveTileSize in re-encode paths

Replace the hardcoded outputTileSize=256 (factor/downsample/ife) and bare
source-tile reads (stitched/transcode) with resolveTileSize(srcL0TileW,
cvTileSize): unset matches the source tiling, --tile-size N overrides."
```

---

## Task 4: `tileCopyEligible` forces re-encode when `--tile-size` ≠ source; route source-codec default

**Files:** `cmd/wsitools/convert_shared.go` (`tileCopyEligible`), `cmd/wsitools/convert.go` (dispatch), `convert_tiff.go` (re-encode codec).

- [ ] **Step 1: `tileCopyEligible` gains the tile-size rule**

`tileCopyEligible(target, codecFlag string, src source.Compression, srcNativelyTiled bool) bool` (convert_shared.go:52). Add a `tileSize, srcL0TileW int` pair so it can disqualify a copy when the requested size differs:
```go
func tileCopyEligible(target, codecFlag string, src source.Compression, srcNativelyTiled bool, tileSize, srcL0TileW int) bool {
	// A verbatim copy can't change tile size; a --tile-size that differs from
	// the source forces a re-encode.
	if tileSize > 0 && tileSize != srcL0TileW {
		return false
	}
	// ... existing eligibility checks unchanged ...
}
```
Update its callers (`grep -rn tileCopyEligible cmd/wsitools`) to pass `cvTileSize` and the source L0 tile width.

- [ ] **Step 2: Forced re-encode resolves codec from the source**

In `runConvertTIFF` (convert.go:65), the branch that currently errors when tile-copy is ineligible and `cvCodec == ""`:
```go
	if cvCodec == "" {
		return fmt.Errorf("--codec required for --to %s with source codec %s (no tile-copy path)", target, srcCodec)
	}
	return runConvertTIFFReencode(cmd, input, target, cvCodec, cvQuality, cvWorkers, start)
```
Change the codec resolution to default to the source codec via `reencodeCodecFor`:
```go
	codecName, cerr := reencodeCodecFor(srcCodec, cvCodec)
	if cerr != nil {
		return cerr
	}
	return runConvertTIFFReencode(cmd, input, target, codecName, cvQuality, cvWorkers, start)
```
(So a `--tile-size`-forced re-encode of a JPEG source picks `jpeg`, a JP2K source picks `jpeg2000`, and an LZW source errors per `reencodeCodecFor`.)

- [ ] **Step 3: Build + smoke (same-size stays copy; diff size re-encodes with source codec)**

```bash
make build
WSI=$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs
SRC_TILE=$(./bin/wsitools info "$WSI" | grep -m1 -oE "tile [0-9]+" | grep -oE "[0-9]+")
# same-size → lossless tile-copy (fast; pixel-stable)
./bin/wsitools convert --to svs --tile-size "$SRC_TILE" -f -o /tmp/ts_copy.svs "$WSI"
# different size, no --codec → re-encode with source codec (jpeg)
./bin/wsitools convert --to svs --tile-size 512 -f -o /tmp/ts_re.svs "$WSI"
./bin/wsitools dump-ifds --raw /tmp/ts_re.svs | grep -m1 TileWidth   # 512
```
Expected: `--tile-size <src>` is a quick copy; `--tile-size 512` re-encodes (TileWidth 512), no `--codec` error.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/convert_shared.go cmd/wsitools/convert.go cmd/wsitools/convert_tiff.go
git commit -m "feat(convert): --tile-size != source forces re-encode w/ source codec

tileCopyEligible disqualifies a verbatim copy when --tile-size differs from the
source tiling; the forced re-encode defaults its codec to the source's own
(reencodeCodecFor), erroring if the source codec has no encoder."
```

---

## Task 5: DZI/SZI use the unified flag

**Files:** `cmd/wsitools/convert_dzi.go`, `convert_szi.go`, `dzi_lossless.go`.

- [ ] **Step 1: Resolve DZI/SZI tile size from the unified flag**

In both `convert_dzi.go` and `convert_szi.go`, replace `tileSize, overlap := cvDZITileSize, cvDZIOverlap` (now `cvTileSize` after Task 2's rename) with a resolved value:
```go
	tileSize, overlap := resolveTileSize(src.Levels()[0].TileSize().X, cvTileSize), cvDZIOverlap
```
So unset → match source (uniform), set → use N.

- [ ] **Step 2: Update the `losslessDZIConfig` inputs**

The lossless path (`convert_dzi.go:74`) passes `userSetTileSize: cmd.Flags().Changed("dzi-tile-size")` and `reqTileSize: cvDZITileSize`. Update to the renamed flag:
```go
			userSetTileSize: cmd.Flags().Changed("tile-size"),
			reqTileSize:     resolveTileSize(l0.TileSize.W, cvTileSize),
```
(Resolve `reqTileSize` so the lossless resolver compares the *effective* requested size against the source. `dzi_lossless.go`'s logic at line 35 — `if in.userSetTileSize && in.reqTileSize != in.srcTileSize` — is unchanged.) Apply the same in `convert_szi.go`.

- [ ] **Step 3: Build + smoke**

```bash
make build
WSI=$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs
./bin/wsitools convert --to dzi --tile-size 512 -f -o /tmp/ts.dzi "$WSI"
grep -o 'TileSize="[0-9]*"' /tmp/ts.dzi   # TileSize="512"
./bin/wsitools convert --to dzi -f -o /tmp/ts_def.dzi "$WSI"
grep -o 'TileSize="[0-9]*"' /tmp/ts_def.dzi   # source tile width (match-source default)
```
Expected: `--tile-size 512` → `TileSize="512"`; unset → source tile width.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/convert_dzi.go cmd/wsitools/convert_szi.go cmd/wsitools/dzi_lossless.go
git commit -m "feat(convert): dzi/szi use unified --tile-size (default match-source)"
```

---

## Task 6: `--to bif` errors on `--tile-size`

**Files:** `cmd/wsitools/convert.go` (dispatch) or `convert_bif.go`.

- [ ] **Step 1: Guard in the bif dispatch**

In `runConvert` where it dispatches `case "bif": return runConvertBIF(...)` (convert.go:205), add before the call:
```go
	case "bif":
		if cvTileSize > 0 {
			return fmt.Errorf("--tile-size is not supported for --to bif (verbatim DP-200 tiling)")
		}
		return runConvertBIF(cmd, input, start)
```

- [ ] **Step 2: Build + smoke**

```bash
make build
./bin/wsitools convert --to bif --tile-size 512 -f -o /tmp/x.bif "$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs"; echo "exit=$?"
```
Expected: non-zero exit, message `--tile-size is not supported for --to bif`.

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/convert.go
git commit -m "feat(convert): error on --tile-size for --to bif (vendor format)"
```

---

## Task 7: `--to dicom` honors `--tile-size` (re-tile in derivedsource)

The DICOM transform path (`internal/derivedsource`) re-encodes source tiles **1:1** (`transcodeLevel.TileSize()` returns `src.TileSize()`, and `TileInto`/`DecodedTile` map output tile (x,y) → the same source tile). Honoring `--tile-size` means presenting a **re-tiled grid** over the source pixels: each output frame is composited from the source via region reads. This is the one new piece of machinery.

**Files:**
- Create: `internal/derivedsource/retile_source.go`
- Modify: `cmd/wsitools/convert_factor.go` (DICOM branch), `internal/derivedsource` constructors.

- [ ] **Step 1: Write the failing test**

Create `internal/derivedsource/retile_source_test.go`:
```go
package derivedsource

import (
	"image"
	"testing"
)

// a fake source.Level returning a known solid color via DecodedTile-style reads
// is heavy; instead assert the geometry math of the retiled level.
func TestRetiledGeometry(t *testing.T) {
	// source: 1000x800, source tile 256; request output tile 512.
	g := retiledGrid(image.Point{X: 1000, Y: 800}, 512)
	if g.X != 2 || g.Y != 2 { // ceil(1000/512)=2, ceil(800/512)=2
		t.Fatalf("grid = %v, want (2,2)", g)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/derivedsource/ -run TestRetiledGeometry 2>&1 | grep -v "duplicate librar"`
Expected: FAIL — `undefined: retiledGrid`.

- [ ] **Step 3: Implement the re-tiling level**

Create `internal/derivedsource/retile_source.go`. It wraps the slide and presents a level whose `TileSize` is the requested size, `Grid` = `ceil(Size/tileSize)`, and whose tiles are read from the source via `(*opentile.Level).ReadRegion` (decode the requested output-tile rectangle from the source pixels) then re-encoded with the JPEG-baseline encoder already used by `transcode.go`. Key pieces:
```go
func retiledGrid(size image.Point, tile int) image.Point {
	return image.Point{X: (size.X + tile - 1) / tile, Y: (size.Y + tile - 1) / tile}
}
```
The retiled level implements the same `source.Level` interface as `transcodeLevel`: `Size()` = source level size; `TileSize()` = `image.Point{tile, tile}`; `Grid()` = `retiledGrid(Size(), tile)`; `TileInto(col,row,dst)` reads `ReadRegion(col*tile, row*tile, w, h)` from the source level (clamping `w/h` at the right/bottom edge, padding to `tile×tile` like TILED_FULL), tight-packs RGB, and `enc.EncodeStandalone(rgb, tile, tile)`. Reuse `tightTileRGB`/the worker-guarded encoder pattern from `transcode.go`.

- [ ] **Step 4: Run the geometry test**

Run: `go test ./internal/derivedsource/ -run TestRetiledGeometry 2>&1 | grep -v "duplicate librar"`
Expected: PASS.

- [ ] **Step 5: Route the DICOM path to the retiled source when `--tile-size` is set**

In `convert_factor.go`'s `downsampleToDICOM` (the DICOM branch of `dispatchDownsampleByTarget`), thread the resolved tile size in. When `cvTileSize > 0` (and differs from source), build the pyramid from retiled levels (the new `retile_source.go`) instead of `transcodeLevel`; otherwise keep today's 1:1 behavior. The `dicomwriter` spec's `TileSize` (→ `Rows`/`Columns`, `dataset.go:232-233`) then follows the retiled tile size automatically.

- [ ] **Step 6: Smoke (DICOM Rows/Columns follow --tile-size)**

```bash
make build
WSI=$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs
./bin/wsitools convert --to dicom --tile-size 512 -f -o /tmp/ts_dcm "$WSI"
# dump first instance's Rows/Columns (use dump-ifds DICOM support or python pydicom if available)
./bin/wsitools info /tmp/ts_dcm 2>/dev/null | grep -iE "tile|512" | head
```
Expected: the DICOM frames are 512×512 (`Rows=512`, `Columns=512`). If `dciodvfy` is available locally, confirm 0 errors on an instance.

- [ ] **Step 7: Commit**

```bash
git add internal/derivedsource/retile_source.go internal/derivedsource/retile_source_test.go cmd/wsitools/convert_factor.go
git commit -m "feat(convert): --to dicom honors --tile-size (re-tile via derivedsource)

Add a re-tiling derivedsource level that composites resolved-size frames from
the source via ReadRegion; the DICOM Rows/Columns follow. Routed when
--tile-size differs from the source tiling; 1:1 transcode otherwise."
```

---

## Task 8: Integration tests + follow-up issue

**Files:** `tests/integration/tile_size_test.go` (new).

- [ ] **Step 1: Write the CLI integration tests**

Create `tests/integration/tile_size_test.go` (build tag `integration`; mirror the helpers in existing integration tests — `buildOnce`/`runCLI`/`testdir`). Cover, using `svs/CMU-1-Small-Region.svs`:
```go
//go:build integration

package integration

// TestTileSizeOverride: --to tiff --tile-size 512 → L0 TileWidth 512.
// TestTileSizeDefaultMatchesSource: --to tiff --factor 2 (unset) → L0 TileWidth
//   == source L0 tile width (regression for the old hardcoded 256).
// TestTileSizeSameAsSourceStaysCopy: --to svs --tile-size <src> → pixel-stable,
//   no re-encode (compare `hash --mode pixel` to a plain copy).
// TestTileSizeBIFErrors: --to bif --tile-size 512 → non-zero exit + message.
// TestTileSizeDICOMRowsColumns: --to dicom --tile-size 512 → frame Rows/Columns 512.
// TestTileSizeDZIManifest: --to dzi --tile-size 512 → manifest TileSize="512".
```
Each: run the binary, then assert via `dump-ifds --raw` (TileWidth), file content (`.dzi` manifest), or DICOM dataset. Write the actual assertion bodies (no placeholders) following `convert_aperio_tags_test.go`'s `dumpIFD0Raw` style.

- [ ] **Step 2: Run the integration tests**

Run:
```bash
make build
WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration -run "TestTileSize" ./tests/integration/... -v 2>&1 | grep -v "duplicate librar" | grep -E "PASS|FAIL|ok"
```
Expected: all PASS.

- [ ] **Step 3: File the LZW/uncompressed-encode follow-up issue**

```bash
gh issue create --repo WSILabs/wsitools \
  --title "Add LZW / uncompressed (and Deflate) tile encoders" \
  --body "Re-tiling a lossless-source (LZW/uncompressed ImageScope export, e.g. via \`convert --tile-size\` differing from source) currently errors when no --codec is given, because there is no encoder for the source codec — only JPEG/JP2K/JXL/AVIF/WebP/HTJ2K + PNG. Adding LZW/uncompressed (and Deflate) encoders would let \`reencodeCodecFor\` preserve a lossless source's compression family instead of erroring. See docs/superpowers/specs/2026-06-27-convert-tile-size-design.md."
```

- [ ] **Step 4: Full suite + commit**

```bash
WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -count=1 -timeout 30m 2>&1 | grep -E "FAIL|ok "
git add tests/integration/tile_size_test.go
git commit -m "test(convert): integration coverage for --tile-size across targets"
```

---

## Update help/docs (in Task 2's commit or a final doc commit)

- `README.md` / any `--dzi-tile-size` mention → `--tile-size`.
- `CHANGELOG.md` `[Unreleased]`: an `### Added` entry for `--tile-size` and a `### Changed`/`### Removed` note for `--dzi-tile-size` and `--jobs` removal.

---

## Self-Review notes (author)

- **Spec coverage:** flag rename + default (Task 2/3/5); resolveTileSize/reencodeCodecFor (Task 2); re-encode wiring (Task 3); tileCopyEligible forced re-encode + source-codec default (Task 4); DZI/SZI (Task 5); bif error (Task 6); dicom honor (Task 7); `--jobs` removal (Task 1); tests + issue (Task 8). All spec sections map to a task.
- **Type consistency:** `resolveTileSize(srcL0TileW, flag int) int` and `reencodeCodecFor(src source.Compression, codecFlag string) (string, error)` used consistently; `cvTileSize` is the single renamed var.
- **Ordering:** Task 1 (`--jobs`) is independent and first; Task 7 (DICOM re-tiling) is the largest/riskiest and last before tests, so the TIFF-family + dzi/szi value lands even if DICOM needs iteration.
- **Risk:** Task 7's `ReadRegion`-based re-tiling is the one genuinely new component; the geometry test gates the math, the smoke test the frame size. Everything else is plumbing over the existing retile engine.
