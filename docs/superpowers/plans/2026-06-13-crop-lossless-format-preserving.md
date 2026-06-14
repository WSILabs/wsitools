# Format-preserving lossless `crop` (Phase 2b) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** `crop --lossless` works for generic-TIFF, OME-TIFF, and cog-wsi (byte-identical L0 tile copy), not just SVS.

**Architecture:** Reuse the SVS lossless shape. `writeLosslessL0` (streamwriter) already serves tiff/ome-tiff; add `writeLosslessL0COGWSI`. Pass a `cropEmitParams` struct (incl. mode + snap coords) to the per-format emitters; each branches **only the pyramid emission** (re-encode vs lossless), sharing writer setup + the associated loop. The front-end snaps+materializes for all lossless targets.

**Spec:** `docs/superpowers/specs/2026-06-13-crop-lossless-format-preserving-design.md`
**Reuse:** `snapRectToTiles`, `writeLosslessL0`, `levelPhotometric`, `levelJPEGTables`, `halveRaster`, `buildPyramidFromRaster`, `buildPyramidFromRasterCOGWSI`, `regenCropThumbnail`, `regenCropThumbnailCOGWSI`, `cropPyramidLevels`, `TileBodyInto`.

---

## Task 1: `writeLosslessL0COGWSI`

**File:** `cmd/wsitools/crop_lossless.go`.

Mirror `writeLosslessL0` for cogwsiwriter (synchronous row-major; no drain — see `encodeAndWriteLevelCOGWSI`).

- [ ] **Step 1: Implement** — add to `crop_lossless.go`:

```go
// writeLosslessL0COGWSI emits pyramid level 0 into a cogwsiwriter by copying a
// contiguous block of source L0 tiles VERBATIM (abbreviated bodies + raw tag-347
// tables + source photometric — see writeLosslessL0). cogwsiwriter.WriteTile is
// strict row-major (no concurrent drain).
func writeLosslessL0COGWSI(w *cogwsiwriter.Writer, srcL0 *opentile.Level, stx0, sty0, outTilesX, outTilesY, outW, outH int) error {
	h, err := w.AddLevel(cogwsiwriter.LevelSpec{
		ImageWidth:      uint32(outW),
		ImageHeight:     uint32(outH),
		TileWidth:       uint32(srcL0.TileSize.W),
		TileHeight:      uint32(srcL0.TileSize.H),
		Compression:     opentile.CompressionToTIFFTag(srcL0.Compression),
		Photometric:     levelPhotometric(srcL0),
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		JPEGTables:      levelJPEGTables(srcL0),
		IsL0:            true,
	})
	if err != nil {
		return fmt.Errorf("AddLevel: %w", err)
	}
	scratch := make([]byte, srcL0.TileBodyMaxSize())
	for oy := 0; oy < outTilesY; oy++ {
		for ox := 0; ox < outTilesX; ox++ {
			n, err := srcL0.TileBodyInto(stx0+ox, sty0+oy, scratch)
			if err != nil {
				return fmt.Errorf("read source tile body (%d,%d): %w", stx0+ox, sty0+oy, err)
			}
			body := make([]byte, n)
			copy(body, scratch[:n])
			if err := h.WriteTile(uint32(ox), uint32(oy), body); err != nil {
				return fmt.Errorf("write tile (%d,%d): %w", ox, oy, err)
			}
		}
	}
	return nil
}
```

Add the `cogwsiwriter` import to `crop_lossless.go` if not present. `levelPhotometric`/`levelJPEGTables` are already in this file.

- [ ] **Step 2: Build + vet** — `go build ./...` clean; `go vet ./cmd/wsitools/` clean. (No unit test — `*opentile.Level` can't be faked; covered by Task 4's byte-identity test.) Verify `cogwsiwriter.LevelSpec`/`AddLevel`/`(*LevelHandle).WriteTile` field/method names against `encodeAndWriteLevelCOGWSI`.

- [ ] **Step 3: Commit** — `git add cmd/wsitools/crop_lossless.go && git commit -m "feat(crop): writeLosslessL0COGWSI verbatim L0 copy into cogwsiwriter"`

---

## Task 2: `cropEmitParams` struct refactor (no behaviour change)

**File:** `cmd/wsitools/crop_formats.go` (+ `crop.go` dispatch).

Replace the three emitters' positional params with a struct, so Task 3 can add mode+coords without a 20-param signature. **Re-encode behaviour unchanged.**

- [ ] **Step 1: Define the struct** (in `crop_formats.go`):

```go
// cropEmitParams carries everything a per-format crop emitter needs. lossless,
// srcL0 and the stx0/sty0/outTilesX/outTilesY tile-block coords are used only on
// the lossless path (Phase 2b); re-encode ignores them.
type cropEmitParams struct {
	ctx          context.Context
	src          *opentile.Slide
	srcL0        *opentile.Level
	input        string
	output       string
	l0           []byte
	l0W, l0H     int
	nLevels      int
	quality      int
	workers      int
	order        tileorder.OrderStrategy
	bigtiffFlag  string
	noAssociated bool
	lossless     bool
	stx0, sty0   int
	outTilesX    int
	outTilesY    int
	start        time.Time
}
```

- [ ] **Step 2: Change emitter signatures** to `func cropToTIFF(p cropEmitParams) error` (same for `cropToOMETIFF`, `cropToCOGWSI`). In each body, replace the former params with `p.<field>` (e.g. `p.ctx`, `p.src`, `p.l0`, `p.l0W`, `p.quality`, `p.order`, `p.bigtiffFlag`, `p.noAssociated`, `p.start`; `cropSourceScale(p.input, p.src)`; `buildPyramidFromRaster(p.ctx, w, p.l0, p.l0W, p.l0H, p.nLevels, p.quality, p.workers, nil)`; cog-wsi uses `p.input`/`p.src`/etc.). No logic change.

- [ ] **Step 3: Update the dispatch in `runCrop`** (`crop.go`) — build the struct and pass it:

```go
	p := cropEmitParams{
		ctx: ctx, src: src, srcL0: srcL0, input: input, output: output,
		l0: outL0, l0W: w, l0H: h, nLevels: nLevels, quality: q, workers: workers,
		order: order, bigtiffFlag: bigtiffFlag, noAssociated: noAssociated,
		lossless: false, start: start,
	}
	switch target {
	case "tiff":
		return cropToTIFF(p)
	case "ome-tiff":
		return cropToOMETIFF(p)
	case "cog-wsi":
		return cropToCOGWSI(p)
	default:
		return fmt.Errorf("crop: target %q not implemented", target)
	}
```

(Field values match the current re-encode call: `l0W=w, l0H=h` are the exact rect; `lossless=false`; `srcL0`/snap coords zero-valued — unused on re-encode.)

- [ ] **Step 4: Build + vet + tests** — `go build ./...` clean; `go vet ./cmd/wsitools/` clean; `go test ./cmd/wsitools/ -run Crop -count=1` PASS.

- [ ] **Step 5 (controller): Phase-2a re-encode matrix regression** (struct refactor changed nothing):
`WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run 'TestCrop_FormatPreserving|TestCrop_ThumbnailRegen' -count=1 -timeout 30m` → PASS.

- [ ] **Step 6: Commit** — `git add cmd/wsitools/crop_formats.go cmd/wsitools/crop.go && git commit -m "refactor(crop): cropEmitParams struct for per-format emitters"`

---

## Task 3: Front-end snap+materialize + emitter lossless branches

**Files:** `cmd/wsitools/crop.go`, `cmd/wsitools/crop_formats.go`.

- [ ] **Step 1: Front-end** — in `runCrop`, REMOVE the `lossless && target != "svs"` guard. In the non-SVS section, compute the effective rect + snap coords and materialize accordingly. Replace the current exact-rect materialize block with:

```go
	// Effective rect: lossless snaps to the tile grid; re-encode uses exact rect.
	ex, ey, ew, eh := x, y, w, h
	var stx0, sty0, outTilesX, outTilesY int
	if lossless {
		ex, ey, ew, eh, stx0, sty0, outTilesX, outTilesY = snapRectToTiles(x, y, w, h, srcL0.TileSize.W, srcL0.TileSize.H, baseW, baseH)
		if ex != x || ey != y || ew != w || eh != h {
			fmt.Printf("lossless: snapped crop to %d,%d %dx%d (tile-aligned)\n", ex, ey, ew, eh)
		}
	}
	rasterBytes := int64(ew) * int64(eh) * 3
	if rasterBytes < 0 {
		return fmt.Errorf("cropped L0 raster size overflows int64")
	}
	outL0 := make([]byte, rasterBytes)
	if err := downscale.MaterializeCroppedL0(ctx, srcL0, outL0, ex, ey, ew, eh); err != nil {
		return fmt.Errorf("materialize cropped L0: %w", err)
	}
	nLevels := cropPyramidLevels(ew, eh, outputTileSize)

	p := cropEmitParams{
		ctx: ctx, src: src, srcL0: srcL0, input: input, output: output,
		l0: outL0, l0W: ew, l0H: eh, nLevels: nLevels, quality: q, workers: workers,
		order: order, bigtiffFlag: bigtiffFlag, noAssociated: noAssociated,
		lossless: lossless, stx0: stx0, sty0: sty0, outTilesX: outTilesX, outTilesY: outTilesY,
		start: start,
	}
	switch target {
	case "tiff":
		return cropToTIFF(p)
	case "ome-tiff":
		return cropToOMETIFF(p)
	case "cog-wsi":
		return cropToCOGWSI(p)
	default:
		return fmt.Errorf("crop: target %q not implemented", target)
	}
```

(`q` is the resolved non-SVS quality — keep the existing `q := quality; if q == 0 { q = 90 }` block above this.)

- [ ] **Step 2: `cropToTIFF` / `cropToOMETIFF` lossless branch** — replace the single `buildPyramidFromRaster(...)` pyramid call with:

```go
	if p.lossless {
		if err := writeLosslessL0(w, p.srcL0, p.stx0, p.sty0, p.outTilesX, p.outTilesY, p.l0W, p.l0H); err != nil {
			return fmt.Errorf("write lossless L0: %w", err)
		}
		if p.nLevels > 1 {
			l1, l1W, l1H, err := halveRaster(p.l0, p.l0W, p.l0H)
			if err != nil {
				return fmt.Errorf("halve L0→L1: %w", err)
			}
			if err := buildPyramidFromRaster(p.ctx, w, l1, l1W, l1H, p.nLevels-1, p.quality, p.workers, nil); err != nil {
				return fmt.Errorf("build pyramid: %w", err)
			}
		}
	} else {
		if err := buildPyramidFromRaster(p.ctx, w, p.l0, p.l0W, p.l0H, p.nLevels, p.quality, p.workers, nil); err != nil {
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
```

The associated loop (with the Phase-2a in-place thumbnail regen) that FOLLOWS is unchanged and shared by both modes. For OME-TIFF: the `omeAssocs`/OME-XML already use `p.l0W/p.l0H` (= snapped dims for lossless), so dims stay consistent — no extra change.

- [ ] **Step 3: `cropToCOGWSI` lossless branch** — same shape with the cog-wsi helpers:

```go
	if p.lossless {
		if err := writeLosslessL0COGWSI(w, p.srcL0, p.stx0, p.sty0, p.outTilesX, p.outTilesY, p.l0W, p.l0H); err != nil {
			aborted = true
			return fmt.Errorf("write lossless L0: %w", err)
		}
		if p.nLevels > 1 {
			l1, l1W, l1H, err := halveRaster(p.l0, p.l0W, p.l0H)
			if err != nil {
				aborted = true
				return fmt.Errorf("halve L0→L1: %w", err)
			}
			if err := buildPyramidFromRasterCOGWSI(p.ctx, w, l1, l1W, l1H, p.nLevels-1, p.quality); err != nil {
				aborted = true
				return fmt.Errorf("build pyramid: %w", err)
			}
		}
	} else {
		if err := buildPyramidFromRasterCOGWSI(p.ctx, w, p.l0, p.l0W, p.l0H, p.nLevels, p.quality); err != nil {
			aborted = true
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
```

(The cog-wsi associated loop that follows — with Phase-2a in-place thumbnail regen — is unchanged.)

- [ ] **Step 4: Build + vet + help + crop unit tests** — `go build ./...` clean; `go vet ./cmd/wsitools/` clean; `go test ./cmd/wsitools/ -run Crop -count=1` PASS; `go run ./cmd/wsitools crop --lossless --help` (no longer errors conceptually). Update the `--lossless` flag help text in `crop.go` init() to drop "SVS only": e.g. `"Lossless crop: snap to the tile grid and copy L0 tiles verbatim (byte-identical; output is a tile-aligned superset)"`. Also update the Long description's "(SVS only for now)" note.

- [ ] **Step 5: Commit** — `git add cmd/wsitools/crop.go cmd/wsitools/crop_formats.go && git commit -m "feat(crop): --lossless for tiff/ome-tiff/cog-wsi (verbatim L0, format-preserving)"`

---

## Task 4: Byte-identity integration tests (non-SVS lossless)

**File:** `tests/integration/crop_test.go`.

Generalize the existing `assertLosslessByteIdentity` to a per-format output, then add a matrix. The helper currently writes `lossless.svs` and asserts via `*Level.Tile()`/`TilePrefix()` (format-agnostic on `*opentile.Level`).

- [ ] **Step 1:** Add an output-extension/format parameter path. Add a new test using the existing tile-by-tile byte-identity logic (copy/adapt `assertLosslessByteIdentity` into `assertLosslessByteIdentityFmt(t, bin, src, x,y,w,h int, ext string)` that names the output `lossless.<ext>` — the assertions are unchanged). Then:

```go
// TestCropLossless_FormatPreserving verifies --lossless copies L0 tiles
// byte-identical for the non-SVS TIFF family. Local-only.
func TestCropLossless_FormatPreserving(t *testing.T) {
	bin := buildOnce(t)
	td := testdir(t)
	cases := []struct {
		file, ext  string
		x, y, w, h int
	}{
		{"generic-tiff/CMU-1.tiff", "tiff", 500, 500, 2000, 2000},       // 240px tiles
		{"ome-tiff/Leica-1.ome.tiff", "ome.tiff", 500, 500, 2000, 2000}, // 512px tiles
		{"cog-wsi/CMU-1_cog-wsi.tiff", "tiff", 500, 500, 2000, 2000},     // writeLosslessL0COGWSI
		{"cog-wsi/JP2K-33003-1_cog-wsi.tiff", "tiff", 500, 500, 2000, 2000}, // J2K, nil TilePrefix
	}
	for _, c := range cases {
		c := c
		t.Run(c.file, func(t *testing.T) {
			src := filepath.Join(td, c.file)
			if _, err := os.Stat(src); err != nil {
				t.Skipf("fixture missing: %s", src)
			}
			assertLosslessByteIdentityFmt(t, bin, src, c.x, c.y, c.w, c.h, c.ext)
		})
	}
}
```

`assertLosslessByteIdentityFmt` is `assertLosslessByteIdentity` with two changes:
(a) output filename `"lossless."+ext`; (b) it must NOT assert `outTlr.Format()==svs` — instead assert the output format **equals the source format** (`opentile.OpenFile(src).Format()`), since lossless is now format-preserving. Keep every byte-identity assertion (tile-by-tile equality, TilePrefix equality, snapped dims, MPP/mag).

- [ ] **Step 2 (controller): run it** — `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestCropLossless_FormatPreserving -count=1 -timeout 30m -v`. Expected: all PASS — every output L0 tile byte-identical to source, format preserved. Implementer compile-checks only.

- [ ] **Step 3: Commit** — `git add tests/integration/crop_test.go && git commit -m "test(crop): non-SVS lossless byte-identity matrix (tiff/ome-tiff/cog-wsi + J2K)"`

---

## Final verification (controller)

- [ ] `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -race ./cmd/wsitools/ ./internal/tiff/... -count=1 -timeout 30m` green.
- [ ] Integration: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run 'TestCrop|TestDownsample' -count=1 -timeout 30m` green (all lossless + re-encode + SVS regressions).
- [ ] `gofmt -l` clean; final whole-branch review; superpowers:finishing-a-development-branch. MERGE-VERIFY: grep merged files + re-run a key lossless test on merged HEAD.

## Notes / risks

- **`cropEmitParams` refactor (Task 2) is no-behaviour-change** — guarded by the Phase-2a re-encode/thumbnail integration tests.
- **cog-wsi verbatim correctness** — Task 4's `CMU-1_cog-wsi.tiff` + `JP2K-33003-1_cog-wsi.tiff` byte-identity cases are the empirical proof of `writeLosslessL0COGWSI`.
- **J2K nil-TilePrefix** — `levelJPEGTables` returns nil for J2K (no tag 347), `levelPhotometric` falls back to 2; the SVS lossless already proved this path; the cog-wsi J2K case confirms it for cog-wsi.
- **SVS lossless unchanged** — `cropEmitSVS` is not touched.
