# DICOM Derived-Pyramid Adapter (A1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make DICOM a transform *target* — `convert --to dicom --factor N`, `downsample --factor N <dicom>`, and `crop <dicom>` (re-encode + `--lossless`) — by feeding the existing `dicomwriter.WritePyramid` a synthesized `source.Source` over a derived (reduced/cropped) pyramid.

**Architecture:** A new `internal/derivedsource` package presents a derived pyramid as a `source.Source` with two level kinds — `rasterLevel` (holds RGB, JPEG-encodes tiles on demand) and `passthroughLevel` (returns a source's verbatim compressed frame at a tile offset). The validated DICOM writer is untouched except one new `Options` field. Three thin CLI emitters build the right `derived` source and hand it to a shared `emitDICOM`.

**Tech Stack:** Go; libjpeg-turbo via `internal/codec/jpeg`; opentile-go reader; `suyashkumar/dicom` (in the writer); `dciodvfy` for conformance.

**Spec:** `docs/superpowers/specs/2026-06-14-dicom-derived-pyramid-adapter-design.md`

---

## File structure

| File | Responsibility | Action |
|---|---|---|
| `internal/downscale/tile.go` | `ExtractTile` — pull a tileSize×tileSize RGB tile (zero-padded edges) from a raster | Create (move from cmd/wsitools) |
| `internal/derivedsource/derivedsource.go` | `derived` source + `rasterLevel` + `passthroughLevel` + constructors `FromReducedL0` / `WithLosslessL0` | Create |
| `internal/derivedsource/derivedsource_test.go` | unit tests for both level kinds + constructors | Create |
| `internal/source/opentile.go` | `FromSlide` constructor (refactor `Open` to use it) | Modify |
| `internal/dicomwriter/dicomwriter.go` | `Options.L0ImageType`; L0 ImageType override in `writeInstance` | Modify |
| `internal/dicomwriter/dicomwriter_test.go` | test for the L0ImageType override | Modify |
| `cmd/wsitools/convert_dicom.go` | `emitDICOM` helper; `writeDICOMPyramid` refactor | Modify |
| `cmd/wsitools/convert_factor.go` | `downsampleToDICOM`; `downsampleTargetForFormat` + dispatch `dicom` case | Modify |
| `cmd/wsitools/convert.go` | route `--to dicom --factor` → `runConvertFactor`; reject `--factor`+`--level` | Modify |
| `cmd/wsitools/crop_formats.go` | `cropToDICOM` (re-encode + lossless branches) | Modify |
| `cmd/wsitools/crop.go` | crop dispatch `dicom` case | Modify |
| `cmd/wsitools/downsample.go` | `extractTileFromRaster` delegates to `downscale.ExtractTile` | Modify |
| `tests/integration/dicom_transform_test.go` | end-to-end convert/downsample/crop into DICOM | Create |
| `CHANGELOG.md`, `docs/format-debt-survey-2026-06-13.md` | doc updates | Modify |

**Note on a spec deviation:** the spec said move `halveRaster` *and* tile extraction into `internal/downscale`. `internal/downscale.BoxHalve(rgb, w, h, 2)` already provides shared box-halving, so the adapter uses that and only `extractTileFromRaster` is relocated (`ExtractTile`). `halveRaster` stays in `cmd/wsitools` (it carries odd-dimension trimming + progress-bar plumbing specific to the streamwriter path). The adapter's lower-level pixels need not be byte-identical to the streamwriter path — the DICOM lossless oracle checks L0 *frames*, which are verbatim, not re-encoded lower levels.

---

## Task 1: Relocate tile extraction to `internal/downscale`

**Files:**
- Create: `internal/downscale/tile.go`
- Create: `internal/downscale/tile_test.go`
- Modify: `cmd/wsitools/downsample.go:484-507` (delete body, delegate)

- [ ] **Step 1: Write the failing test**

Create `internal/downscale/tile_test.go`:

```go
package downscale

import (
	"bytes"
	"testing"
)

func TestExtractTile_InteriorAndEdgePad(t *testing.T) {
	// 3×2 raster, RGB, distinct per-pixel values so we can spot misalignment.
	// pixel (x,y) = {x, y, 0}.
	w, h := 3, 2
	raster := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := (y*w + x) * 3
			raster[o], raster[o+1], raster[o+2] = byte(x), byte(y), 0
		}
	}
	// tileSize 2: tile (0,0) covers x∈[0,2), y∈[0,2) — fully inside.
	got := ExtractTile(raster, w, h, 0, 0, 2)
	if len(got) != 2*2*3 {
		t.Fatalf("tile len = %d, want %d", len(got), 2*2*3)
	}
	// row 0: (0,0),(1,0); row 1: (0,1),(1,1)
	want := []byte{0, 0, 0, 1, 0, 0, 0, 1, 0, 1, 1, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("interior tile = %v, want %v", got, want)
	}
	// tile (1,0) covers x∈[2,4): only x=2 valid, x=3 is edge → zero-padded.
	got = ExtractTile(raster, w, h, 1, 0, 2)
	want = []byte{2, 0, 0, 0, 0, 0, 2, 1, 0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("edge tile = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/downscale/ -run TestExtractTile -count=1`
Expected: FAIL — `undefined: ExtractTile`.

- [ ] **Step 3: Create the implementation**

Create `internal/downscale/tile.go`:

```go
package downscale

// ExtractTile copies a tileSize×tileSize RGB888 tile at tile coordinate (tx, ty)
// out of an RGB888 raster of rasterW×rasterH pixels. Pixels past the raster's
// right/bottom edge are left zero (the standard edge-pad). The returned slice is
// always tileSize*tileSize*3 bytes.
func ExtractTile(raster []byte, rasterW, rasterH, tx, ty, tileSize int) []byte {
	tile := make([]byte, tileSize*tileSize*3)
	x0 := tx * tileSize
	y0 := ty * tileSize
	if x0 >= rasterW || y0 >= rasterH {
		return tile // empty edge — full zero pad
	}
	copyW := tileSize
	if x0+copyW > rasterW {
		copyW = rasterW - x0
	}
	copyH := tileSize
	if y0+copyH > rasterH {
		copyH = rasterH - y0
	}
	srcStride := rasterW * 3
	dstStride := tileSize * 3
	for y := 0; y < copyH; y++ {
		srcOff := (y0+y)*srcStride + x0*3
		dstOff := y * dstStride
		copy(tile[dstOff:dstOff+copyW*3], raster[srcOff:srcOff+copyW*3])
	}
	return tile
}
```

- [ ] **Step 4: Delegate the cmd/wsitools copy**

In `cmd/wsitools/downsample.go`, replace the body of `extractTileFromRaster` (lines ~484-507) so it delegates (keeps the existing call sites + the `error` return they expect):

```go
func extractTileFromRaster(raster []byte, rasterW, rasterH, tx, ty int) ([]byte, error) {
	return downscale.ExtractTile(raster, rasterW, rasterH, tx, ty, outputTileSize), nil
}
```

(`downscale` is already imported in `downsample.go`.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/downscale/ -run TestExtractTile -count=1 && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add internal/downscale/tile.go internal/downscale/tile_test.go cmd/wsitools/downsample.go
git commit -m "refactor(downscale): extract ExtractTile (shared by streamwriter + DICOM adapter)"
```

---

## Task 2: `dicomwriter.Options.L0ImageType` override

**Files:**
- Modify: `internal/dicomwriter/dicomwriter.go:21-23` (Options) and `:259-269` (writeInstance ImageType)
- Modify: `internal/dicomwriter/dicomwriter_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/dicomwriter/dicomwriter_test.go`:

```go
func TestWriteInstance_L0ImageTypeOverride(t *testing.T) {
	src := openDICOMFixture(t, "scan_621_grundium_dicom")
	defer src.Close()

	var out bytes.Buffer
	// L0 with an explicit DERIVED/RESAMPLED override (downsample semantics).
	if err := WriteVolumeInstance(&out, src, 0, Options{
		L0ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"},
	}); err != nil {
		t.Fatalf("WriteVolumeInstance: %v", err)
	}
	ds, err := dicom.Parse(bytes.NewReader(out.Bytes()), int64(out.Len()), nil)
	if err != nil {
		t.Fatalf("dicom.Parse: %v", err)
	}
	e, err := ds.FindElementByTag(tag.ImageType)
	if err != nil {
		t.Fatalf("ImageType missing: %v", err)
	}
	got, _ := e.Value.GetValue().([]string)
	want := []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"}
	if len(got) != 4 || got[0] != want[0] || got[3] != want[3] {
		t.Errorf("ImageType = %v, want %v", got, want)
	}
}
```

(`WriteVolumeInstance` is the single-instance entry; confirm it threads `Options` to `writeInstance`'s ImageType logic — see Step 3.)

- [ ] **Step 2: Run test to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./internal/dicomwriter/ -run TestWriteInstance_L0ImageTypeOverride -count=1`
Expected: FAIL — `unknown field 'L0ImageType'` (compile error). If the DICOM fixture is absent it SKIPs; in that case note it and proceed (the override is also exercised by the integration tests).

- [ ] **Step 3: Implement the Options field + override**

In `internal/dicomwriter/dicomwriter.go`, extend `Options`:

```go
// Options controls the DICOM write. Associated enables emitting the slide's
// associated images (label/overview/thumbnail/…) as separate instances.
// L0ImageType, when non-nil, overrides level 0's ImageType (4 values) — used by
// transform emitters (downsample/crop) where L0 is no longer ORIGINAL.
type Options struct {
	Associated  bool
	L0ImageType []string
}
```

`WriteVolumeInstance` and `WritePyramid` both reach `writeInstance` — thread `opts` through. Find `writeInstance`'s signature and the call sites:

- `WritePyramid` calls `writeInstance(w, src, level, shared)` — change to `writeInstance(w, src, level, shared, opts)`.
- `WriteVolumeInstance` calls `writeInstance` similarly — pass its `opts`.

Then change `writeInstance`'s signature and the ImageType block (`:259-269`):

```go
func writeInstance(w io.Writer, src source.Source, level int, shared sharedUIDs, opts Options) error {
	// ... unchanged up to the ImageType switch ...

	var imageType []string
	switch {
	case level == 0 && opts.L0ImageType != nil:
		imageType = opts.L0ImageType
	case src.Format() == "dicom":
		imageType = []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"}
	case level == 0:
		imageType = []string{"ORIGINAL", "PRIMARY", "VOLUME", "NONE"}
	default:
		imageType = []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"}
	}
```

(The new first case wins for L0 when an override is supplied; everything else is unchanged.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./internal/dicomwriter/ -count=1`
Expected: PASS (or SKIP if no DICOM fixture) — and no other dicomwriter test regresses.

- [ ] **Step 5: Commit**

```bash
git add internal/dicomwriter/dicomwriter.go internal/dicomwriter/dicomwriter_test.go
git commit -m "feat(dicom): Options.L0ImageType overrides L0 ImageType for transform emitters"
```

---

## Task 3: `source.FromSlide` + `source.OpenWithSlide`

The DICOM emitters need both a `source.Source` (for metadata/associated/levels)
and the raw `*opentile.Slide` (raster materialization takes `*opentile.Level`).
`FromSlide` wraps an already-open slide; `OpenWithSlide` opens once
(ambiguity-guarded, like `Open`) and returns both. `Open` is refactored to
delegate, so the DICOM-dir ambiguity guard is shared, not duplicated.

**Files:**
- Modify: `internal/source/opentile.go` (add `FromSlide` + `OpenWithSlide`, refactor `Open`)
- Modify: `internal/source/opentile_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/source/opentile_test.go`:

```go
func TestOpenWithSlide_ReturnsBothHandles(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no svs fixture")
	}
	src, slide, err := OpenWithSlide(p)
	if err != nil {
		t.Fatalf("OpenWithSlide: %v", err)
	}
	defer src.Close()
	if slide == nil {
		t.Fatal("OpenWithSlide returned nil slide")
	}
	if src.Format() != string(opentile.FormatSVS) {
		t.Errorf("Format = %q, want svs", src.Format())
	}
	if len(src.Levels()) != len(slide.Levels()) {
		t.Errorf("source Levels = %d, slide Levels = %d", len(src.Levels()), len(slide.Levels()))
	}
}
```

(Ensure `opentile`, `path/filepath`, `os` are imported in the test file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./internal/source/ -run TestOpenWithSlide -count=1`
Expected: FAIL — `undefined: OpenWithSlide`.

- [ ] **Step 3: Implement `FromSlide` + `OpenWithSlide`, refactor `Open`**

In `internal/source/opentile.go`, add both constructors and refactor `Open` to
delegate to `OpenWithSlide`. Replace the existing `Open` (lines 19-49) with:

```go
// Open is the entry point. Opens the file via opentile-go and returns a Source.
func Open(path string) (Source, error) {
	src, _, err := OpenWithSlide(path)
	return src, err
}

// OpenWithSlide opens the file and returns BOTH the Source wrapper and the raw
// opentile slide. Callers needing *opentile.Level (raster materialization in the
// downsample/crop DICOM emitters) use the slide; everything else uses the
// Source. Applies the same DICOM-dir ambiguity guard and zero-geometry check as
// the original Open.
func OpenWithSlide(path string) (Source, *opentile.Slide, error) {
	// Safe-by-default: a directory holding >1 distinct WSM series is ambiguous;
	// refuse rather than silently opening the dominant one. A single .dcm is
	// never ambiguous, so only check dirs.
	if fi, statErr := os.Stat(path); statErr == nil && fi.IsDir() {
		if infos, lerr := dicom.ListWSMSeries(path); lerr == nil && len(infos) > 1 {
			return nil, nil, &AmbiguousSeriesError{Path: path, Series: infos}
		}
	}
	t, err := opentile.OpenFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("source: open %s: %w", path, err)
	}
	if levels := t.Levels(); len(levels) > 0 {
		lvl0 := levels[0]
		if lvl0.TileSize.W == 0 || lvl0.TileSize.H == 0 {
			t.Close()
			return nil, nil, fmt.Errorf("%w: %s reports zero tile geometry on level 0", ErrUnsupportedFormat, t.Format())
		}
	}
	return FromSlide(t, path), t, nil
}

// FromSlide wraps an already-open opentile slide as a Source, without reopening
// the file. Used by callers that already hold a *opentile.Slide (e.g. the crop
// emitter, whose front-end opened the slide). The returned Source's Close closes
// the underlying slide.
func FromSlide(t *opentile.Slide, path string) Source {
	// ReadSourceImageDescription returns ("", err) for non-TIFF sources (IFE) —
	// silence the error and treat "" as "no description".
	desc, _ := ReadSourceImageDescription(path)
	return &opentileSource{t: t, path: path, desc: desc}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./internal/source/ -count=1`
Expected: PASS — `TestOpenWithSlide` plus all existing source tests (the ambiguity + zero-geometry behavior is preserved, now routed through `OpenWithSlide`).

- [ ] **Step 5: Commit**

```bash
git add internal/source/opentile.go internal/source/opentile_test.go
git commit -m "feat(source): OpenWithSlide returns Source + raw slide; FromSlide wraps an open slide"
```

---

## Task 4: `derivedsource.rasterLevel`

**Files:**
- Create: `internal/derivedsource/derivedsource.go`
- Create: `internal/derivedsource/derivedsource_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/derivedsource/derivedsource_test.go`:

```go
package derivedsource

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func TestRasterLevel_TileIntoEncodesDecodableJPEG(t *testing.T) {
	// 4×4 solid mid-gray raster, one 4-px tile.
	w, h, ts := 4, 4, 4
	raster := make([]byte, w*h*3)
	for i := range raster {
		raster[i] = 128
	}
	l := &rasterLevel{raster: raster, w: w, h: h, tileSize: ts, quality: 90, index: 0}

	if l.Compression() != source.CompressionJPEG {
		t.Errorf("Compression = %v, want JPEG", l.Compression())
	}
	if got := l.Size(); got != (image.Point{X: 4, Y: 4}) {
		t.Errorf("Size = %v, want 4×4", got)
	}
	if got := l.Grid(); got != (image.Point{X: 1, Y: 1}) {
		t.Errorf("Grid = %v, want 1×1", got)
	}

	dst := make([]byte, l.TileMaxSize())
	n, err := l.TileInto(0, 0, dst)
	if err != nil {
		t.Fatalf("TileInto: %v", err)
	}
	// The frame must be a self-contained JPEG — decodable by the stdlib decoder
	// (which has no shared-tables mechanism), proving it is NOT abbreviated.
	img, err := jpeg.Decode(bytes.NewReader(dst[:n]))
	if err != nil {
		t.Fatalf("decode produced JPEG (must be self-contained for DICOM frames): %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 4 || b.Dy() != 4 {
		t.Errorf("decoded dims = %d×%d, want 4×4", b.Dx(), b.Dy())
	}
	// Center pixel ≈ 128 (JPEG is lossy; allow tolerance).
	r, _, _, _ := img.At(2, 2).RGBA()
	r8 := r >> 8
	if r8 < 118 || r8 > 138 {
		t.Errorf("center R = %d, want ≈128", r8)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/derivedsource/ -run TestRasterLevel -count=1`
Expected: FAIL — package/`rasterLevel` undefined.

- [ ] **Step 3: Implement `rasterLevel` (+ package skeleton)**

Create `internal/derivedsource/derivedsource.go`:

```go
// Package derivedsource presents a derived (reduced or cropped) pyramid as a
// source.Source so it can be handed to dicomwriter.WritePyramid, which reads
// compressed tiles verbatim via Level.TileInto. Two level kinds back the
// pyramid: rasterLevel (holds RGB, JPEG-encodes tiles on demand) and
// passthroughLevel (returns a source level's verbatim compressed frame at a
// tile offset, used for a lossless crop's L0).
package derivedsource

import (
	"fmt"
	"image"
	"io"
	"strconv"

	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/downscale"
	"github.com/wsilabs/wsitools/internal/source"
)

// rasterLevel is one pyramid level backed by an RGB888 raster; tiles are
// JPEG-baseline encoded on demand as complete (self-contained) frames — the
// form DICOM encapsulated PixelData requires.
type rasterLevel struct {
	raster   []byte
	w, h     int
	tileSize int
	quality  int
	index    int
}

func (l *rasterLevel) Index() int                  { return l.index }
func (l *rasterLevel) Size() image.Point           { return image.Point{X: l.w, Y: l.h} }
func (l *rasterLevel) TileSize() image.Point        { return image.Point{X: l.tileSize, Y: l.tileSize} }
func (l *rasterLevel) Compression() source.Compression { return source.CompressionJPEG }
func (l *rasterLevel) TileMaxSize() int             { return l.tileSize*l.tileSize*3 + 2048 }

func (l *rasterLevel) Grid() image.Point {
	return image.Point{
		X: (l.w + l.tileSize - 1) / l.tileSize,
		Y: (l.h + l.tileSize - 1) / l.tileSize,
	}
}

func (l *rasterLevel) TileInto(x, y int, dst []byte) (int, error) {
	// One encoder per call: stateless, concurrency-safe, and the table compute
	// is negligible against the encode itself.
	enc, err := jpegcodec.New(codec.LevelGeometry{
		TileWidth:   l.tileSize,
		TileHeight:  l.tileSize,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(l.quality)}})
	if err != nil {
		return 0, fmt.Errorf("derivedsource: new jpeg encoder: %w", err)
	}
	tileRGB := downscale.ExtractTile(l.raster, l.w, l.h, x, y, l.tileSize)
	frame, err := enc.EncodeStandalone(tileRGB, l.tileSize, l.tileSize)
	if err != nil {
		return 0, fmt.Errorf("derivedsource: encode tile (%d,%d): %w", x, y, err)
	}
	if len(frame) > len(dst) {
		return 0, io.ErrShortBuffer
	}
	return copy(dst, frame), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/derivedsource/ -run TestRasterLevel -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/derivedsource/
git commit -m "feat(derivedsource): rasterLevel — JPEG-encode derived-pyramid tiles on demand"
```

---

## Task 5: `derived` source + `FromReducedL0`

**Files:**
- Modify: `internal/derivedsource/derivedsource.go`
- Modify: `internal/derivedsource/derivedsource_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/derivedsource/derivedsource_test.go`:

```go
func TestFromReducedL0_SourceShape(t *testing.T) {
	// 8×8 L0 raster, 4-px tiles, 2 levels (8×8 then 4×4).
	w, h := 8, 8
	raster := make([]byte, w*h*3)
	for i := range raster {
		raster[i] = 64
	}
	md := source.Metadata{MPPX: 1.0, MPPY: 1.0, MPP: 1.0, Magnification: 10}
	src, err := FromReducedL0(raster, w, h, 2 /*nLevels*/, 4 /*tileSize*/, 90, "svs", md, nil)
	if err != nil {
		t.Fatalf("FromReducedL0: %v", err)
	}
	defer src.Close()

	if src.Format() != "svs" {
		t.Errorf("Format = %q, want svs", src.Format())
	}
	if src.SourceImageDescription() != "" {
		t.Errorf("SourceImageDescription = %q, want empty", src.SourceImageDescription())
	}
	lv := src.Levels()
	if len(lv) != 2 {
		t.Fatalf("Levels = %d, want 2", len(lv))
	}
	if lv[0].Size() != (image.Point{X: 8, Y: 8}) {
		t.Errorf("L0 size = %v, want 8×8", lv[0].Size())
	}
	if lv[1].Size() != (image.Point{X: 4, Y: 4}) {
		t.Errorf("L1 size = %v, want 4×4 (box-halved)", lv[1].Size())
	}
	if lv[0].Compression() != source.CompressionJPEG {
		t.Errorf("L0 compression = %v, want JPEG", lv[0].Compression())
	}
	if src.Metadata().Magnification != 10 {
		t.Errorf("Magnification = %v, want 10 (passed through)", src.Metadata().Magnification)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/derivedsource/ -run TestFromReducedL0 -count=1`
Expected: FAIL — `undefined: FromReducedL0`.

- [ ] **Step 3: Implement `derived` + `FromReducedL0`**

Append to `internal/derivedsource/derivedsource.go`:

```go
// derived implements source.Source over a list of synthesized levels.
type derived struct {
	format string
	levels []source.Level
	md     source.Metadata
	assoc  []source.AssociatedImage
}

func (d *derived) Format() string                       { return d.format }
func (d *derived) Levels() []source.Level               { return d.levels }
func (d *derived) Associated() []source.AssociatedImage { return d.assoc }
func (d *derived) Metadata() source.Metadata            { return d.md }
func (d *derived) SourceImageDescription() string       { return "" }
func (d *derived) Close() error                         { return nil }

// FromReducedL0 builds an all-raster derived source: L0 is the supplied
// (reduced or cropped) raster, and nLevels-1 lower levels are produced by box-
// halving. tileSize/quality drive the JPEG encode; format/md/assoc are carried
// onto the source (md already factor-scaled by the caller). Used by downsample,
// convert --factor, and the re-encode crop.
func FromReducedL0(l0 []byte, w, h, nLevels, tileSize, quality int, format string, md source.Metadata, assoc []source.AssociatedImage) (source.Source, error) {
	if nLevels < 1 {
		return nil, fmt.Errorf("derivedsource: nLevels must be >= 1, got %d", nLevels)
	}
	levels := make([]source.Level, 0, nLevels)
	raster, lw, lh := l0, w, h
	for i := 0; i < nLevels; i++ {
		levels = append(levels, &rasterLevel{raster: raster, w: lw, h: lh, tileSize: tileSize, quality: quality, index: i})
		if i == nLevels-1 {
			break
		}
		var err error
		raster, lw, lh, err = downscale.BoxHalve(raster, lw, lh, 2)
		if err != nil {
			return nil, fmt.Errorf("derivedsource: halve level %d→%d: %w", i, i+1, err)
		}
		if lw == 0 || lh == 0 {
			break
		}
	}
	return &derived{format: format, levels: levels, md: md, assoc: assoc}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/derivedsource/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/derivedsource/derivedsource.go internal/derivedsource/derivedsource_test.go
git commit -m "feat(derivedsource): derived source + FromReducedL0 (box-halved raster pyramid)"
```

---

## Task 6: `passthroughLevel` + `WithLosslessL0`

**Files:**
- Modify: `internal/derivedsource/derivedsource.go`
- Modify: `internal/derivedsource/derivedsource_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/derivedsource/derivedsource_test.go`:

```go
// fakeLevel is a source.Level whose TileInto returns a deterministic "frame"
// encoding the (x,y) it was asked for, so passthrough offset mapping is testable
// without a real codec.
type fakeLevel struct {
	tileSize image.Point
	comp     source.Compression
}

func (f *fakeLevel) Index() int                  { return 0 }
func (f *fakeLevel) Size() image.Point           { return image.Point{X: 1000, Y: 1000} }
func (f *fakeLevel) TileSize() image.Point       { return f.tileSize }
func (f *fakeLevel) Grid() image.Point           { return image.Point{X: 4, Y: 4} }
func (f *fakeLevel) Compression() source.Compression { return f.comp }
func (f *fakeLevel) TileMaxSize() int            { return 8 }
func (f *fakeLevel) TileInto(x, y int, dst []byte) (int, error) {
	body := []byte{byte(x), byte(y)}
	return copy(dst, body), nil
}

func TestPassthroughLevel_OffsetMappingAndCompression(t *testing.T) {
	fl := &fakeLevel{tileSize: image.Point{X: 256, Y: 256}, comp: source.CompressionJPEG2000}
	pl := &passthroughLevel{
		src:   fl,
		offX:  2,
		offY:  3,
		size:  image.Point{X: 512, Y: 512},
		grid:  image.Point{X: 2, Y: 2},
		index: 0,
	}
	if pl.Compression() != source.CompressionJPEG2000 {
		t.Errorf("Compression = %v, want JP2K (source's)", pl.Compression())
	}
	if pl.Size() != (image.Point{X: 512, Y: 512}) {
		t.Errorf("Size = %v, want 512×512 (snapped)", pl.Size())
	}
	if pl.TileSize() != (image.Point{X: 256, Y: 256}) {
		t.Errorf("TileSize = %v, want source 256×256", pl.TileSize())
	}
	// Output tile (1,1) must map to source tile (1+2, 1+3) = (3,4).
	dst := make([]byte, pl.TileMaxSize())
	n, err := pl.TileInto(1, 1, dst)
	if err != nil {
		t.Fatalf("TileInto: %v", err)
	}
	if n != 2 || dst[0] != 3 || dst[1] != 4 {
		t.Errorf("frame = %v (n=%d), want source tile (3,4)", dst[:n], n)
	}
}

func TestWithLosslessL0_MixedLevelKinds(t *testing.T) {
	fl := &fakeLevel{tileSize: image.Point{X: 4, Y: 4}, comp: source.CompressionJPEG}
	// snapped region 8×8 raster for the re-encoded lower levels.
	lower := make([]byte, 8*8*3)
	md := source.Metadata{MPP: 1.0, Magnification: 20}
	src, err := WithLosslessL0(fl, 1, 1, 2, 2, 8, 8, lower, 2 /*nLevels*/, 4 /*tileSize*/, 90, "dicom", md, nil)
	if err != nil {
		t.Fatalf("WithLosslessL0: %v", err)
	}
	lv := src.Levels()
	if len(lv) != 2 {
		t.Fatalf("Levels = %d, want 2", len(lv))
	}
	// L0 is passthrough → source compression (JPEG here, but the point is it is
	// the source's, not a freshly-chosen one); L1 is a raster level → JPEG.
	if _, ok := lv[0].(*passthroughLevel); !ok {
		t.Errorf("L0 kind = %T, want *passthroughLevel", lv[0])
	}
	if _, ok := lv[1].(*rasterLevel); !ok {
		t.Errorf("L1 kind = %T, want *rasterLevel", lv[1])
	}
	if lv[1].Size() != (image.Point{X: 4, Y: 4}) {
		t.Errorf("L1 size = %v, want 4×4 (halved from 8×8)", lv[1].Size())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/derivedsource/ -run 'TestPassthroughLevel|TestWithLosslessL0' -count=1`
Expected: FAIL — `passthroughLevel` / `WithLosslessL0` undefined.

- [ ] **Step 3: Implement `passthroughLevel` + `WithLosslessL0`**

Append to `internal/derivedsource/derivedsource.go`:

```go
// passthroughLevel returns a source level's verbatim compressed frame at a tile
// offset — the lossless-crop L0. Output tile (x,y) maps to source tile
// (x+offX, y+offY); Size/Grid report the snapped output geometry; TileSize and
// Compression are the source's.
type passthroughLevel struct {
	src        source.Level
	offX, offY int
	size       image.Point
	grid       image.Point
	index      int
}

func (l *passthroughLevel) Index() int                  { return l.index }
func (l *passthroughLevel) Size() image.Point           { return l.size }
func (l *passthroughLevel) Grid() image.Point           { return l.grid }
func (l *passthroughLevel) TileSize() image.Point       { return l.src.TileSize() }
func (l *passthroughLevel) Compression() source.Compression { return l.src.Compression() }
func (l *passthroughLevel) TileMaxSize() int            { return l.src.TileMaxSize() }
func (l *passthroughLevel) TileInto(x, y int, dst []byte) (int, error) {
	return l.src.TileInto(x+l.offX, y+l.offY, dst)
}

// WithLosslessL0 builds a derived source whose L0 is a passthrough over srcL0
// (verbatim frames for the tile-aligned crop region) and whose nLevels-1 lower
// levels are box-halved raster levels decoded from the snapped region
// (lowerRaster, snapW×snapH). Used by crop --lossless into DICOM.
func WithLosslessL0(srcL0 source.Level, offX, offY, gridW, gridH, snapW, snapH int, lowerRaster []byte, nLevels, tileSize, quality int, format string, md source.Metadata, assoc []source.AssociatedImage) (source.Source, error) {
	if nLevels < 1 {
		return nil, fmt.Errorf("derivedsource: nLevels must be >= 1, got %d", nLevels)
	}
	levels := make([]source.Level, 0, nLevels)
	levels = append(levels, &passthroughLevel{
		src: srcL0, offX: offX, offY: offY,
		size: image.Point{X: snapW, Y: snapH},
		grid: image.Point{X: gridW, Y: gridH},
		index: 0,
	})
	raster, lw, lh := lowerRaster, snapW, snapH
	for i := 1; i < nLevels; i++ {
		var err error
		raster, lw, lh, err = downscale.BoxHalve(raster, lw, lh, 2)
		if err != nil {
			return nil, fmt.Errorf("derivedsource: halve level %d→%d: %w", i-1, i, err)
		}
		if lw == 0 || lh == 0 {
			break
		}
		levels = append(levels, &rasterLevel{raster: raster, w: lw, h: lh, tileSize: tileSize, quality: quality, index: i})
	}
	return &derived{format: format, levels: levels, md: md, assoc: assoc}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/derivedsource/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/derivedsource/derivedsource.go internal/derivedsource/derivedsource_test.go
git commit -m "feat(derivedsource): passthroughLevel + WithLosslessL0 (verbatim L0 + raster lowers)"
```

---

## Task 7: `emitDICOM` helper

**Files:**
- Modify: `cmd/wsitools/convert_dicom.go`

- [ ] **Step 1: Extract `emitDICOM` from `writeDICOMPyramid`**

In `cmd/wsitools/convert_dicom.go`, add `emitDICOM` (the temp-dir → WritePyramid → atomic-rename core, parameterized by source + options + outDir + force):

```go
// emitDICOM writes a full DICOM-WSM pyramid for src into outDir (one
// level-<n>.dcm per instance, plus associated images). It writes into a temp
// sibling dir and renames into place so a failed run never leaves a partial
// pyramid. Shared by writeDICOMPyramid (convert --to dicom) and the
// downsample/crop DICOM emitters.
func emitDICOM(src source.Source, opts dicomwriter.Options, outDir string, force bool) error {
	parent := filepath.Dir(outDir)
	tmp, err := os.MkdirTemp(parent, ".wsitools-dcm-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	factory := func(name string) (io.WriteCloser, error) {
		return os.Create(filepath.Join(tmp, name+".dcm"))
	}
	if err := dicomwriter.WritePyramid(src, opts, factory); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("write DICOM pyramid: %w", err)
	}
	if force {
		if err := os.RemoveAll(outDir); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("remove existing %s: %w", outDir, err)
		}
	}
	if err := os.Rename(tmp, outDir); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("finalize %s: %w", outDir, err)
	}
	return nil
}
```

- [ ] **Step 2: Refactor `writeDICOMPyramid` to call it**

Replace the body of `writeDICOMPyramid` from the `tmp, err := os.MkdirTemp...` block through the `os.Rename` block with a single `emitDICOM` call, keeping the post-write summary:

```go
func writeDICOMPyramid(src source.Source, start time.Time) error {
	if err := emitDICOM(src, dicomwriter.Options{Associated: !cvNoAssociated}, cvOutput, cvForce); err != nil {
		return err
	}
	entries, _ := os.ReadDir(cvOutput)
	n := 0
	var total int64
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".dcm" {
			n++
			if info, err := e.Info(); err == nil {
				total += info.Size()
			}
		}
	}
	slog.Info("convert complete",
		"output", cvOutput, "instances", n, "size", formatBytes(total),
		"elapsed", time.Since(start).Round(time.Millisecond))
	fmt.Printf("wrote %s (%d instances, %s, %s)\n", cvOutput, n, formatBytes(total), time.Since(start).Round(time.Millisecond))
	return nil
}
```

- [ ] **Step 3: Build + run the existing DICOM tests to verify no regression**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./internal/dicomwriter/ -count=1 && go build ./...`
Expected: PASS; build clean. (The `convert --to dicom` behavior is unchanged — same files, same layout.)

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/convert_dicom.go
git commit -m "refactor(convert): extract emitDICOM (temp-dir → WritePyramid → atomic rename)"
```

---

## Task 8: `downsampleToDICOM` + dispatch + format mapping

**Files:**
- Modify: `cmd/wsitools/convert_factor.go` (add `downsampleToDICOM`, dispatch case, `downsampleTargetForFormat` mapping)
- Create/append: `tests/integration/dicom_transform_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `tests/integration/dicom_transform_test.go`:

```go
//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runCLI runs the built wsitools binary and returns combined output. The
// integration package runs the binary directly via exec (there is no shared
// run helper); buildOnce and testdir are defined in downsample_test.go.
func runCLI(bin string, args ...string) (string, error) {
	out, err := exec.Command(bin, args...).CombinedOutput()
	return string(out), err
}

// dicomFixture returns a DICOM source dir under the test pool, or skips.
func dicomFixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(testdir(t), "dicom", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no DICOM fixture %s", name)
	}
	return p
}

// countDCM returns the number of *.dcm files in dir.
func countDCM(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".dcm" {
			n++
		}
	}
	return n
}

func TestDownsampleDICOM_Factor2(t *testing.T) {
	bin := buildOnce(t)
	src := dicomFixture(t, "scan_621_grundium_dicom")
	out := filepath.Join(t.TempDir(), "down.dcmdir")
	if o, err := runCLI(bin, "downsample", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample --factor 2 <dicom>: %v\n%s", err, o)
	}
	if n := countDCM(t, out); n < 1 {
		t.Errorf("output has %d .dcm instances, want >= 1", n)
	}
	// dciodvfy is run by the controller (conformance gate), not here.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run TestDownsampleDICOM_Factor2 -count=1`
Expected: FAIL — `downsample` rejects a DICOM source ("convert --to ..." not-writable error) because `downsampleTargetForFormat("dicom")` returns false.

- [ ] **Step 3: Add the format mapping + dispatch case**

In `cmd/wsitools/convert_factor.go`, extend `downsampleTargetForFormat`:

```go
	case opentile.FormatDICOM:
		return "dicom", true
```

(Place it alongside the SVS/OME/TIFF/COGWSI cases. `opentile.FormatDICOM` is `Format = "dicom"`, confirmed in opentile-go v0.41.0 `format.go`.)

Extend `dispatchDownsampleByTarget`'s switch:

```go
	case "dicom":
		return downsampleToDICOM(ctx, input, output, factor, targetMag, quality, workers, tileOrderName, force, bigtiffFlag, noAssociated)
```

- [ ] **Step 4: Implement `downsampleToDICOM`**

Add to `cmd/wsitools/convert_factor.go` (imports: `derivedsource`, `dicomwriter`, `source`, `downscale`, `opentile`, `context`, `fmt`, `os`):

```go
// downsampleToDICOM is the reduce-then-rebuild body for both
// `convert --to dicom --factor N` and `downsample --factor N <dicom>`. It
// materializes a reduced L0 raster, wraps it (plus box-halved lowers) as a
// derived source.Source, and emits a DICOM-WSM pyramid directory. tileOrderName
// and bigtiffFlag are accepted for dispatch-signature uniformity and ignored
// (DICOM has no TIFF tile order / BigTIFF).
func downsampleToDICOM(ctx context.Context, input, output string, factor, targetMag, quality, workers int, tileOrderName string, force bool, bigtiffFlag string, noAssociated bool) error {
	_ = tileOrderName
	_ = bigtiffFlag
	_ = workers
	if quality < 1 || quality > 100 {
		return fmt.Errorf("--quality must be in [1, 100], got %d", quality)
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if !force {
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("output exists (use --force to overwrite): %s", output)
		}
	}

	src, slide, err := source.OpenWithSlide(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()
	md := src.Metadata()

	// Resolve --target-mag against the source magnification, then validate.
	srcMag := md.Magnification
	if targetMag > 0 {
		if srcMag <= 0 {
			return fmt.Errorf("--target-mag set but source magnification is unknown/zero")
		}
		ratio := srcMag / float64(targetMag)
		f := int(ratio + 0.0001)
		if !isValidFactor(f) || float64(f) != ratio {
			return fmt.Errorf("source mag %g / target %d = %g is not a valid power-of-2 in {2,4,8,16}", srcMag, targetMag, ratio)
		}
		factor = f
	}
	if !isValidFactor(factor) {
		return fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", factor)
	}

	srcL0 := slide.Levels()[0]
	outW := srcL0.Size.W / factor
	outH := srcL0.Size.H / factor
	if outW <= 0 || outH <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outW, outH, factor)
	}

	rasterBytes := int64(outW) * int64(outH) * 3
	if rasterBytes < 0 {
		return fmt.Errorf("output L0 raster size overflows int64")
	}
	l0 := make([]byte, rasterBytes)
	if err := downscale.MaterializeReducedL0(ctx, srcL0, l0, outW, outH, factor); err != nil {
		return fmt.Errorf("materialize reduced L0: %w", err)
	}

	// Scale metadata: MPP grows by factor, magnification shrinks by it.
	if md.MPPX != 0 {
		md.MPPX *= float64(factor)
	}
	if md.MPPY != 0 {
		md.MPPY *= float64(factor)
	}
	if md.MPP != 0 {
		md.MPP *= float64(factor)
	}
	if md.Magnification != 0 {
		md.Magnification /= float64(factor)
	}

	assoc := src.Associated()
	if noAssociated {
		assoc = nil
	}
	ds, err := derivedsource.FromReducedL0(l0, outW, outH, len(slide.Levels()), outputTileSize, quality, src.Format(), md, assoc)
	if err != nil {
		return fmt.Errorf("build derived source: %w", err)
	}
	if err := emitDICOM(ds, dicomwriter.Options{
		Associated:  !noAssociated,
		L0ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"},
	}, output, force); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", output)
	return nil
}
```

- [ ] **Step 5: Run the integration test to verify it passes**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run TestDownsampleDICOM_Factor2 -count=1`
Expected: PASS (or SKIP if no DICOM fixture). Also `go build ./...` clean.

- [ ] **Step 6: Controller — dciodvfy gate**

The controller runs `dciodvfy` on each emitted `level-*.dcm` in the output dir and confirms **0 errors**. (Not an automated test step; the conformance gate as in P1/P2.)

- [ ] **Step 7: Commit**

```bash
git add cmd/wsitools/convert_factor.go tests/integration/dicom_transform_test.go
git commit -m "feat(downsample): downsample --factor into DICOM via derived-pyramid adapter"
```

---

## Task 9: `convert --to dicom --factor` routing

**Files:**
- Modify: `cmd/wsitools/convert.go:91-117`
- Modify: `tests/integration/dicom_transform_test.go`

- [ ] **Step 1: Write the failing integration test**

Add to `tests/integration/dicom_transform_test.go`:

```go
func TestConvertDICOM_FactorFromSVS(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no svs fixture")
	}
	out := filepath.Join(t.TempDir(), "svs2dcm.dcmdir")
	if o, err := runCLI(bin, "convert", "--to", "dicom", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to dicom --factor 2 <svs>: %v\n%s", err, o)
	}
	if n := countDCM(t, out); n < 1 {
		t.Errorf("output has %d .dcm instances, want >= 1", n)
	}
}

func TestConvertDICOM_FactorRejectsLevel(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no svs fixture")
	}
	out := filepath.Join(t.TempDir(), "x.dcmdir")
	o, err := runCLI(bin, "convert", "--to", "dicom", "--factor", "2", "--level", "0", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected --factor + --level rejection, got success:\n%s", o)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run 'TestConvertDICOM_Factor' -count=1`
Expected: FAIL — current code returns `--factor/--target-mag not supported for --to dicom (yet)`.

- [ ] **Step 3: Update `runConvert` routing**

In `cmd/wsitools/convert.go`, change the factor-guard (lines ~91-98) to drop `dicom` from the rejection:

```go
	if cvFactor != 1 || cvTargetMag != 0 {
		if cvTo == "dzi" || cvTo == "szi" {
			return fmt.Errorf("--factor/--target-mag not supported for --to %s (yet)", cvTo)
		}
		if cvFactor != 1 && !isValidFactor(cvFactor) {
			return fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", cvFactor)
		}
	}
```

Change the `case "dicom"` (line ~116) to branch on factor:

```go
	case "dicom":
		if cvFactor != 1 || cvTargetMag != 0 {
			if cmd.Flags().Changed("level") {
				return fmt.Errorf("--factor/--target-mag and --level are mutually exclusive (--factor emits the full reduced pyramid)")
			}
			return runConvertFactor(cmd, input, "dicom", start)
		}
		return runConvertDICOM(cmd, input, start)
```

(`runConvertFactor` already parses quality/workers and calls `dispatchDownsampleByTarget`, which now has the `dicom` case from Task 8.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run 'TestConvertDICOM_Factor' -count=1`
Expected: PASS (the SVS→DICOM `--factor` test runs in CI too, since CMU-1-Small-Region.svs is a CI fixture).

- [ ] **Step 5: Controller — dciodvfy gate** on the SVS→DICOM output (0 errors).

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert.go tests/integration/dicom_transform_test.go
git commit -m "feat(convert): --to dicom --factor routes to the DICOM downsample emitter"
```

---

## Task 10: `cropToDICOM` (re-encode + lossless)

**Files:**
- Modify: `cmd/wsitools/crop_formats.go` (add `cropToDICOM`)
- Modify: `cmd/wsitools/crop.go` (dispatch `dicom` case; format guard)
- Modify: `tests/integration/dicom_transform_test.go`

- [ ] **Step 1: Write the failing integration tests**

Add to `tests/integration/dicom_transform_test.go`:

```go
func TestCropDICOM_ReEncode(t *testing.T) {
	bin := buildOnce(t)
	src := dicomFixture(t, "scan_621_grundium_dicom")
	out := filepath.Join(t.TempDir(), "crop.dcmdir")
	// crop a 512×512 region at (0,0); exact extent (no --lossless).
	if o, err := runCLI(bin, "crop", "--rect", "0,0,512,512", "-f", "-o", out, src); err != nil {
		t.Fatalf("crop <dicom>: %v\n%s", err, o)
	}
	if n := countDCM(t, out); n < 1 {
		t.Errorf("output has %d .dcm instances, want >= 1", n)
	}
}

func TestCropDICOM_LosslessL0FrameByteIdentical(t *testing.T) {
	bin := buildOnce(t)
	src := dicomFixture(t, "scan_621_grundium_dicom")
	out := filepath.Join(t.TempDir(), "croplossless.dcmdir")
	// Lossless snaps the rect up to the source tile grid; output is a tile-
	// aligned superset of the requested region.
	if o, err := runCLI(bin, "crop", "--rect", "0,0,512,512", "--lossless", "-f", "-o", out, src); err != nil {
		t.Fatalf("crop --lossless <dicom>: %v\n%s", err, o)
	}
	if n := countDCM(t, out); n < 1 {
		t.Errorf("output has %d .dcm instances, want >= 1", n)
	}
	// Byte-identity oracle (controller verifies frame (0,0) of level-0.dcm equals
	// the source L0 frame at the snapped tile offset; see Step 6).
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run TestCropDICOM -count=1`
Expected: FAIL — crop rejects a DICOM source (no `dicom` dispatch case → "target ... not implemented" or a format guard).

- [ ] **Step 3: Add the crop dispatch case + format guard**

In `cmd/wsitools/crop.go`, find the emit dispatch `switch target` (near line ~228) and add:

```go
	case "dicom":
		return cropToDICOM(p)
```

Find the crop front-end's writable-format guard (the format→target resolution; crop reuses `downsampleTargetForFormat`). With Task 8's mapping, `downsampleTargetForFormat("dicom")` already returns `("dicom", true)`, so a DICOM source now resolves to the `dicom` target — no extra guard change needed. Verify the front-end doesn't separately reject `dicom`; if it has an allowlist, add `dicom`.

- [ ] **Step 4: Implement `cropToDICOM`**

Add to `cmd/wsitools/crop_formats.go` (imports: `derivedsource`, `dicomwriter`, `source`):

```go
// cropToDICOM emits a cropped DICOM-WSM pyramid. Default (p.lossless == false):
// the cropped L0 raster + box-halved lowers are JPEG re-encoded. Lossless: L0 is
// a passthrough over the source's verbatim frames for the tile-snapped region
// (p.stx0/p.sty0 offset, p.outTilesX/Y grid), and lower levels are re-encoded
// from the decoded snapped raster. Crop preserves L0 MPP/magnification.
func cropToDICOM(p cropEmitParams) error {
	src := source.FromSlide(p.src, p.input)
	md := src.Metadata()
	assoc := src.Associated()
	if p.noAssociated {
		assoc = nil
	}

	var ds source.Source
	var err error
	if p.lossless {
		comp := src.Levels()[0].Compression()
		if comp != source.CompressionJPEG && comp != source.CompressionJPEG2000 {
			return fmt.Errorf("--lossless into DICOM needs JPEG or JPEG 2000 source frames; got %s", comp)
		}
		ds, err = derivedsource.WithLosslessL0(
			src.Levels()[0], p.stx0, p.sty0, p.outTilesX, p.outTilesY, p.l0W, p.l0H,
			p.l0, p.nLevels, outputTileSize, p.quality, src.Format(), md, assoc)
	} else {
		ds, err = derivedsource.FromReducedL0(
			p.l0, p.l0W, p.l0H, p.nLevels, outputTileSize, p.quality, src.Format(), md, assoc)
	}
	if err != nil {
		return fmt.Errorf("build derived source: %w", err)
	}

	if err := emitDICOM(ds, dicomwriter.Options{
		Associated:  !p.noAssociated,
		L0ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
	}, p.output, cropForce); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", p.output)
	return nil
}
```

(`cropForce` is the crop command's `-f` flag global, bound in `crop.go`; the other crop emitters resolve output existence in the front-end, but DICOM's directory rename is handled inside `emitDICOM`.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run TestCropDICOM -count=1`
Expected: PASS (or SKIP without the DICOM fixture). `go build ./...` clean.

- [ ] **Step 6: Controller — dciodvfy gate + lossless byte-identity oracle**

The controller:
1. Runs `dciodvfy` on each `level-*.dcm` (0 errors) for both the re-encode and lossless outputs.
2. For the lossless output, parses `level-0.dcm` (via `suyashkumar/dicom`), reads encapsulated frame `(0,0)`, opens the source via `source.Open`, reads source L0 frame at `(p.stx0, p.sty0)` (here `(0,0)`), and asserts **byte-equality** — the lossless guarantee. (A small standalone Go program or a temporary test invoked by the controller; not part of the gated integration suite, since it needs the DICOM fixture.)

- [ ] **Step 7: Commit**

```bash
git add cmd/wsitools/crop_formats.go cmd/wsitools/crop.go tests/integration/dicom_transform_test.go
git commit -m "feat(crop): crop a DICOM source into DICOM (re-encode + --lossless frame-copy)"
```

---

## Task 11: Docs — CHANGELOG, survey, help text

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `docs/format-debt-survey-2026-06-13.md`
- Modify: `cmd/wsitools/convert_factor.go` (the `--factor` flag usage string)

- [ ] **Step 1: CHANGELOG `### Added` under `[Unreleased]`**

Add:

```markdown
- **DICOM is now a transform target.** `convert --to dicom --factor N`,
  `downsample --factor N <dicom>` (format-preserving), and `crop <dicom>`
  (re-encode, plus `--lossless` verbatim-L0 frame-copy) emit a reduced/cropped
  DICOM-WSM pyramid. Implemented via a new `internal/derivedsource` adapter that
  presents a derived pyramid as a `source.Source` to the existing
  `dicomwriter.WritePyramid`; re-encoded levels are JPEG-baseline (no JP2K/HTJ2K
  encoder yet). Output `-o` is a pyramid **directory** (as `convert --to dicom`
  already is). dciodvfy-validated. (Survey A1.)
```

- [ ] **Step 2: Survey — mark A1 done**

In `docs/format-debt-survey-2026-06-13.md`, strike the A1 row and the A1 candidate row, mirroring the C1/A2/D1/B3 closures (e.g. `~~...~~ **DONE** (merge <sha>)`).

- [ ] **Step 3: Update the `--factor` flag help**

In `cmd/wsitools/convert_factor.go`'s flag registration (`convertCmd.Flags().IntVar(&cvFactor, "factor", ...)` lives in `convert.go`), update the usage string to include dicom:

```go
	convertCmd.Flags().IntVar(&cvFactor, "factor", 1, "downsample factor for svs|tiff|ome-tiff|cog-wsi|dicom (1 = no scaling; one of {2,4,8,16})")
```

- [ ] **Step 4: Build + full focused suite**

Run:
```bash
go build ./... && \
WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./internal/derivedsource/ ./internal/downscale/ ./internal/source/ ./internal/dicomwriter/ -count=1 && \
WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/ -run 'DICOM|CropDICOM|DownsampleDICOM|ConvertDICOM' -count=1
```
Expected: all PASS/SKIP, no FAIL.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md docs/format-debt-survey-2026-06-13.md cmd/wsitools/convert.go
git commit -m "docs(dicom): record DICOM transform-target support (A1)"
```

---

## Final verification (controller, after all tasks)

- [ ] `go build ./...` and `go build -tags nocgo ./...` both clean.
- [ ] `go vet ./...` clean.
- [ ] Full unit suite green: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./... -count=1` (heavy `-race` `cmd/wsitools` run separately with `-timeout 30m`).
- [ ] Integration suite green: `WSI_TOOLS_TESTDIR=$PWD/sample_files go test -tags integration ./tests/integration/... -count=1 -timeout 30m`.
- [ ] **dciodvfy gate:** 0 errors on every emitted instance across `convert --to dicom --factor` (SVS + DICOM sources), `downsample --factor <dicom>`, `crop <dicom>` (re-encode + lossless).
- [ ] **Lossless oracle:** `crop --lossless <dicom>` level-0 frame `(0,0)` is byte-identical to the source L0 frame at the snapped tile offset.
- [ ] Dispatch a final code reviewer over the whole branch (focus: the synthesized `source.Source` honors the interface contract; frame row-major order is preserved through `passthroughLevel`; `L0ImageType` is the only writer change; emitDICOM atomicity; no stray files on skip).
- [ ] Use `superpowers:finishing-a-development-branch`.
