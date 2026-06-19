# Streaming Retile Engine (SP3a) — DICOM via the engine — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route lossy DICOM transforms (`convert --to dicom --factor`, `downsample <dicom>`, `crop <dicom>` default) through the `internal/retile` engine via a disk-spool bridge, replacing the `derivedsource` reduced-raster path — gaining JP2K/HTJ2K derived levels + bounded memory, and unlocking deletion of the JPEG-baseline raster machinery.

**Architecture:** The engine PUSHES encoded tiles to a `spoolTileSink` (per-level disk spool, random-access by tile index). After the run, a `spoolSource` (a `source.Source` over the spool, carrying scale-adjusted metadata + associated + the codec's compression) is pulled by `dicomwriter.WritePyramid` exactly as `derivedsource` is today. DICOM frames are self-contained (no TIFF tag-347 sharing), so a `dicomFrameEncoder` produces standalone JPEG / J2K-passthrough frames. Lossless DICOM crop stays on `derivedsource.WithLosslessL0` (byte-exact passthrough).

**Tech Stack:** Go; `internal/retile` (SP2); `internal/codec` (+ jpeg `EncodeStandalone`); `internal/dicomwriter`; `internal/source`.

**Scope:** SP3a only — the spool bridge + lossy DICOM transforms via the engine + `--codec` for the DICOM `convert` path + deleting the now-dead JPEG-baseline raster path. Lossless DICOM crop, the BIF sink (SP3b), and CLI convergence (SP3c) are out of scope. Per `docs/superpowers/specs/2026-06-19-retile-engine-sp3a-dicom-design.md`.

---

## Key facts (ground truth)

- **Engine:** `retile.Run(ctx, retile.Spec{Slide, SrcRegion opentile.Region, OutL0 opentile.Size, Levels []retile.LevelSpec, Kernel resample.Kernel, Encoder retile.TileEncoder, Sink retile.TileSink, Workers int}) error`. `retile.TileSink` = `WriteTile(level, col, row int, encoded []byte) error` (called from ONE drainer goroutine; tiles arrive out-of-order). `octaveLevelSpecsFor(outL0 opentile.Size, tile int) []retile.LevelSpec` (uses `flooredLevelCount`). `retile.LevelSpec{Index, Width, Height, Cols, Rows, TileW, TileH, Overlap int}`.
- **`source.Source` interface** (internal/source): `Format() string`, `Levels() []Level`, `Associated() []AssociatedImage`, `Metadata() Metadata`, `SourceImageDescription() string`, `Close() error`. **`source.Level`**: `Index() int`, `Size() image.Point`, `TileSize() image.Point`, `Grid() image.Point`, `Overlapping() bool`, `Compression() source.Compression`, `TileMaxSize() int`, `TileInto(x,y int, dst []byte) (int, error)`, `DecodedTile(x,y int) (*decoder.Image, error)`.
- **`source.Compression`** enum: `CompressionJPEG`, `CompressionJPEG2000`, `CompressionHTJ2K`, etc.
- **`dicomwriter.WritePyramid(src source.Source, opts dicomwriter.Options, newWriter func(name string)(io.WriteCloser,error)) error`** — PULLS frames via `src.Level.TileInto` (verbatim, `encapsulatePixelData`). Reads MPP/associated/compression from `src`. **Unchanged.** `emitDICOM(src source.Source, opts dicomwriter.Options, outDir string, force bool) error` (convert_dicom.go:123) wraps it (temp dir → rename).
- **Current drivers:** `downsampleToDICOM` (convert_factor.go:867) — `MaterializeReducedL0` → `derivedsource.FromReducedL0(l0, outW, outH, len(slide.Levels()), outputTileSize, quality, workers, src.Format(), md, assoc)` → `emitDICOM(ds, Options{Associated, L0ImageType:["DERIVED","PRIMARY","VOLUME","RESAMPLED"]}, ...)`. md is scale-adjusted (MPP×factor, mag÷factor). `cropToDICOM` (crop_formats.go:393) — `regenCropThumbnailAssoc(assoc, p.l0, …)` then lossless→`WithLosslessL0` / lossy→`FromReducedL0` → `emitDICOM(Options{L0ImageType:["DERIVED","PRIMARY","VOLUME","NONE"]})`. **NOTE M5 made the crop materialize conditional on `lossless || target=="dicom"`, so cropToDICOM currently always has `p.l0`.**
- **jpeg encoder:** `jpegcodec.New(codec.LevelGeometry, codec.Quality) (*jpeg.Encoder, error)`; `(*jpeg.Encoder).EncodeStandalone(rgb []byte, w, h int) ([]byte, error)` (self-contained); `.Close()`. **codec:** `codec.Lookup(name) (codec.EncoderFactory, error)`; `fac.NewEncoder(geom, q) (codec.Encoder, error)`; `enc.EncodeTile(rgb, w, h, dst) ([]byte, error)` (J2K-family returns a complete codestream); `enc.TIFFCompressionTag() uint16`.
- **dciodvfy** = an EXTERNAL dclunie macexe, NOT on this machine / CI. The controller verifies via the opentile DICOM reader read-back + the existing `internal/dicomwriter` unit tests (which encode the conformance attributes) + `pydicom` if available; dciodvfy stays a manual external gate.
- DICOM fixtures: `sample_files/dicom/{scan_621_grundium_dicom, Leica-4, 3DHISTECH-JP2K, 3DHISTECH-HTJ2K, 3DHISTECH-1}` (dirs of `.dcm`).
- New code lives in `cmd/wsitools/dicom_engine.go` (driver glue depending on both retile + dicomwriter + source).

---

## Task 1: `tileSpool` — random-access-by-index disk spool

The engine pushes tiles out-of-order; the spool must serve any tile by its row-major index. A per-level file + an `(offset,len)` index.

**Files:**
- Create: `cmd/wsitools/tile_spool.go`
- Create: `cmd/wsitools/tile_spool_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/tile_spool_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"testing"
)

func TestTileSpoolOutOfOrderPutGet(t *testing.T) {
	dir := t.TempDir()
	sp, err := newTileSpool(dir+"/L0", 4) // 4 tiles
	if err != nil {
		t.Fatal(err)
	}
	// Put out of order.
	frames := map[int][]byte{0: []byte("aaa"), 1: []byte("bb"), 2: []byte("cccc"), 3: []byte("d")}
	for _, idx := range []int{2, 0, 3, 1} {
		if err := sp.put(idx, frames[idx]); err != nil {
			t.Fatalf("put %d: %v", idx, err)
		}
	}
	for idx, want := range frames {
		got, err := sp.get(idx)
		if err != nil {
			t.Fatalf("get %d: %v", idx, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("tile %d = %q, want %q", idx, got, want)
		}
	}
	// A missing tile errors.
	sp2, _ := newTileSpool(dir+"/L1", 2)
	if _, err := sp2.get(0); err == nil {
		t.Error("expected error for unwritten tile")
	}
	if err := sp.close(); err != nil {
		t.Fatal(err)
	}
	if err := sp.remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/L0"); !os.IsNotExist(err) {
		t.Error("spool file not removed")
	}
	_ = sp2.remove()
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestTileSpool -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: newTileSpool`.

- [ ] **Step 3: Implement `cmd/wsitools/tile_spool.go`**

```go
package main

import (
	"fmt"
	"io"
	"os"
)

// tileSpool is a disk-backed store of compressed tile frames for ONE pyramid
// level, addressable by row-major tile index. Frames are appended in arrival
// order (the engine emits out-of-order); the index maps tile-position →
// (offset,len), so get(idx) serves any tile regardless of write order. Single
// writer (the engine's one sink-drainer goroutine), so no locking.
type tileSpool struct {
	f     *os.File
	off   int64
	index []spoolEnt // len == tile count; len<0 marks "not written"
}

type spoolEnt struct {
	off int64
	n   int
}

func newTileSpool(path string, tiles int) (*tileSpool, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	idx := make([]spoolEnt, tiles)
	for i := range idx {
		idx[i].n = -1 // not written
	}
	return &tileSpool{f: f, index: idx}, nil
}

func (s *tileSpool) put(idx int, frame []byte) error {
	if idx < 0 || idx >= len(s.index) {
		return fmt.Errorf("tileSpool: index %d out of range [0,%d)", idx, len(s.index))
	}
	n, err := s.f.Write(frame)
	if err != nil {
		return err
	}
	s.index[idx] = spoolEnt{off: s.off, n: n}
	s.off += int64(n)
	return nil
}

func (s *tileSpool) get(idx int) ([]byte, error) {
	if idx < 0 || idx >= len(s.index) {
		return nil, fmt.Errorf("tileSpool: index %d out of range [0,%d)", idx, len(s.index))
	}
	e := s.index[idx]
	if e.n < 0 {
		return nil, fmt.Errorf("tileSpool: tile %d not written", idx)
	}
	buf := make([]byte, e.n)
	if _, err := s.f.ReadAt(buf, e.off); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

func (s *tileSpool) close() error  { return s.f.Close() }
func (s *tileSpool) remove() error { _ = s.f.Close(); return os.Remove(s.f.Name()) }
```

- [ ] **Step 4: Run to verify it passes + commit**

Run: `go test ./cmd/wsitools/ -run TestTileSpool -v 2>&1 | grep -v 'duplicate librar'` → PASS.
```bash
git add cmd/wsitools/tile_spool.go cmd/wsitools/tile_spool_test.go
git commit -m "feat(dicom): tileSpool — random-access-by-index disk spool for engine output"
```

---

## Task 2: `spoolTileSink` (engine push) + `spoolSource` (dicomwriter pull)

**Files:**
- Create: `cmd/wsitools/dicom_engine.go`
- Create: `cmd/wsitools/dicom_engine_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/dicom_engine_test.go` — a unit test that the sink spools tiles and the source serves them back with the right geometry/compression (no real slide needed):

```go
package main

import (
	"bytes"
	"image"
	"testing"

	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
)

func TestSpoolSinkAndSourceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	levels := []retile.LevelSpec{
		{Index: 0, Width: 512, Height: 512, Cols: 2, Rows: 2, TileW: 256, TileH: 256},
		{Index: 1, Width: 256, Height: 256, Cols: 1, Rows: 1, TileW: 256, TileH: 256},
	}
	md := source.Metadata{MPP: 0.5, Magnification: 20}
	sink, err := newSpoolTileSink(dir, levels)
	if err != nil {
		t.Fatal(err)
	}
	// Push L0's 4 tiles out of order + L1's 1 tile.
	frames := map[[3]int][]byte{
		{0, 1, 1}: []byte("L0-11"), {0, 0, 0}: []byte("L0-00"),
		{0, 1, 0}: []byte("L0-10"), {0, 0, 1}: []byte("L0-01"),
		{1, 0, 0}: []byte("L1-00"),
	}
	for k, v := range frames {
		if err := sink.WriteTile(k[0], k[1], k[2], v); err != nil {
			t.Fatalf("WriteTile %v: %v", k, err)
		}
	}
	src := newSpoolSource(sink, "dicom", source.CompressionJPEG, md, nil)
	defer src.Close()

	if src.Format() != "dicom" || src.Metadata().MPP != 0.5 {
		t.Errorf("source format/md wrong: %q %v", src.Format(), src.Metadata())
	}
	lv := src.Levels()
	if len(lv) != 2 || lv[0].Size() != (image.Point{X: 512, Y: 512}) || lv[0].Compression() != source.CompressionJPEG {
		t.Fatalf("levels wrong: %d %v %v", len(lv), lv[0].Size(), lv[0].Compression())
	}
	// Pull L0 tile (1,1) verbatim.
	buf := make([]byte, lv[0].TileMaxSize())
	n, err := lv[0].TileInto(1, 1, buf)
	if err != nil {
		t.Fatalf("TileInto: %v", err)
	}
	if !bytes.Equal(buf[:n], []byte("L0-11")) {
		t.Errorf("L0(1,1) = %q, want L0-11", buf[:n])
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestSpoolSinkAndSource -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: newSpoolTileSink`, `undefined: newSpoolSource`.

- [ ] **Step 3: Implement the sink + source in `cmd/wsitools/dicom_engine.go`**

```go
package main

import (
	"fmt"
	"image"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
)

// spoolTileSink implements retile.TileSink, spooling each level's frames to a
// per-level tileSpool indexed by row-major tile position.
type spoolTileSink struct {
	levels []retile.LevelSpec
	spools []*tileSpool
}

func newSpoolTileSink(dir string, levels []retile.LevelSpec) (*spoolTileSink, error) {
	spools := make([]*tileSpool, len(levels))
	for i, ls := range levels {
		sp, err := newTileSpool(fmt.Sprintf("%s/L%d", dir, i), ls.Cols*ls.Rows)
		if err != nil {
			return nil, err
		}
		spools[i] = sp
	}
	return &spoolTileSink{levels: levels, spools: spools}, nil
}

func (s *spoolTileSink) WriteTile(level, col, row int, encoded []byte) error {
	if level < 0 || level >= len(s.spools) {
		return fmt.Errorf("spoolTileSink: level %d out of range", level)
	}
	ls := s.levels[level]
	// Copy: the engine pool may reuse `encoded`'s backing array after return.
	frame := make([]byte, len(encoded))
	copy(frame, encoded)
	return s.spools[level].put(row*ls.Cols+col, frame)
}

func (s *spoolTileSink) remove() {
	for _, sp := range s.spools {
		if sp != nil {
			_ = sp.remove()
		}
	}
}

// spoolSource is a source.Source over a finished spoolTileSink, pulled by
// dicomwriter.WritePyramid. Frames are served verbatim; metadata/associated/
// compression are supplied by the driver.
type spoolSource struct {
	sink   *spoolTileSink
	format string
	comp   source.Compression
	md     source.Metadata
	assoc  []source.AssociatedImage
}

func newSpoolSource(sink *spoolTileSink, format string, comp source.Compression, md source.Metadata, assoc []source.AssociatedImage) *spoolSource {
	return &spoolSource{sink: sink, format: format, comp: comp, md: md, assoc: assoc}
}

func (s *spoolSource) Format() string                       { return s.format }
func (s *spoolSource) Metadata() source.Metadata            { return s.md }
func (s *spoolSource) Associated() []source.AssociatedImage { return s.assoc }
func (s *spoolSource) SourceImageDescription() string       { return "" }
func (s *spoolSource) Close() error                         { s.sink.remove(); return nil }
func (s *spoolSource) Levels() []source.Level {
	out := make([]source.Level, len(s.sink.levels))
	for i := range s.sink.levels {
		out[i] = &spoolLevel{src: s, i: i}
	}
	return out
}

type spoolLevel struct {
	src *spoolSource
	i   int
}

func (l *spoolLevel) spec() retile.LevelSpec { return l.src.sink.levels[l.i] }
func (l *spoolLevel) Index() int             { return l.i }
func (l *spoolLevel) Size() image.Point      { return image.Point{X: l.spec().Width, Y: l.spec().Height} }
func (l *spoolLevel) TileSize() image.Point  { return image.Point{X: l.spec().TileW, Y: l.spec().TileH} }
func (l *spoolLevel) Grid() image.Point      { return image.Point{X: l.spec().Cols, Y: l.spec().Rows} }
func (l *spoolLevel) Overlapping() bool      { return false }
func (l *spoolLevel) Compression() source.Compression { return l.src.comp }

func (l *spoolLevel) TileMaxSize() int {
	// An upper bound for sync.Pool sizing; the actual frame is returned by length.
	return l.spec().TileW*l.spec().TileH*3 + 1024
}

func (l *spoolLevel) TileInto(x, y int, dst []byte) (int, error) {
	ls := l.spec()
	frame, err := l.src.sink.spools[l.i].get(y*ls.Cols + x)
	if err != nil {
		return 0, err
	}
	if len(dst) < len(frame) {
		return 0, fmt.Errorf("spoolLevel: dst too small (%d < %d)", len(dst), len(frame))
	}
	return copy(dst, frame), nil
}

func (l *spoolLevel) DecodedTile(x, y int) (*decoder.Image, error) {
	// dicomwriter pulls verbatim frames via TileInto and does not decode; decode
	// is unused on this path. If a future consumer needs it, decode the spooled
	// frame via the codec. For now, signal unsupported rather than guess.
	return nil, fmt.Errorf("spoolLevel.DecodedTile: not supported (verbatim-frame source)")
}
```

VERIFY: confirm `dicomwriter.WritePyramid` does NOT call `Level.DecodedTile` (grep `internal/dicomwriter` for `DecodedTile`). If it DOES, implement `DecodedTile` by decoding the spooled frame via `decoder` (the frame is a complete codestream) instead of erroring. Also confirm `source.Compression` has `CompressionHTJ2K` (it does per source.go).

- [ ] **Step 4: Run to verify it passes + commit**

Run: `go test ./cmd/wsitools/ -run TestSpoolSinkAndSource -v 2>&1 | grep -v 'duplicate librar'` → PASS.
Run: `go build ./... 2>&1 | grep -v 'duplicate librar'` → clean.
```bash
git add cmd/wsitools/dicom_engine.go cmd/wsitools/dicom_engine_test.go
git commit -m "feat(dicom): spoolTileSink (engine push) + spoolSource (dicomwriter pull)"
```

---

## Task 3: `dicomFrameEncoder` — self-contained frames

**Files:**
- Modify: `cmd/wsitools/dicom_engine.go`
- Modify: `cmd/wsitools/dicom_engine_test.go`

- [ ] **Step 1: Write the failing test**

Add to `dicom_engine_test.go`:

```go
import (
	"bytes"
	stdjpeg "image/jpeg"
	// ...
)

func TestDicomFrameEncoderStandaloneJPEG(t *testing.T) {
	enc, comp, err := newDicomFrameEncoder("jpeg", 80)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer enc.Close()
	if comp != source.CompressionJPEG {
		t.Errorf("comp = %v, want JPEG", comp)
	}
	rgb := make([]byte, 64*64*3)
	for i := range rgb {
		rgb[i] = 128
	}
	frame, err := enc.EncodeTile(rgb, 64, 64)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	// DICOM frames MUST be self-contained (stdlib-decodable, with tables).
	if _, err := stdjpeg.Decode(bytes.NewReader(frame)); err != nil {
		t.Errorf("DICOM JPEG frame not self-contained: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestDicomFrameEncoder -v 2>&1 | grep -v 'duplicate librar'`
Expected: FAIL — `undefined: newDicomFrameEncoder`.

- [ ] **Step 3: Implement in `dicom_engine.go`**

```go
// dicomFrameEncoder implements retile.TileEncoder, producing SELF-CONTAINED
// frames (DICOM has no TIFF tag-347 shared-tables mechanism). JPEG uses
// EncodeStandalone; J2K-family codecs (jpeg2000/htj2k) already return a complete
// codestream from EncodeTile.
type dicomFrameEncoder struct {
	jpeg    *jpegcodec.Encoder // non-nil for jpeg
	codec   codec.Encoder      // non-nil for j2k-family
}

// newDicomFrameEncoder builds the frame encoder + reports the source.Compression
// the spoolSource should advertise (so dicomwriter picks the transfer syntax).
func newDicomFrameEncoder(codecName string, quality int) (enc *dicomFrameEncoder, comp source.Compression, err error) {
	switch codecName {
	case "", "jpeg":
		je, e := jpegcodec.New(codec.LevelGeometry{}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}})
		if e != nil {
			return nil, 0, fmt.Errorf("jpeg.New: %w", e)
		}
		return &dicomFrameEncoder{jpeg: je}, source.CompressionJPEG, nil
	case "jpeg2000":
		ce, c, e := newJ2KFrameEncoder("jpeg2000", quality)
		return ce, c, e
	case "htj2k":
		ce, c, e := newJ2KFrameEncoder("htj2k", quality)
		return ce, c, e
	default:
		return nil, 0, fmt.Errorf("--codec %q not supported for DICOM (jpeg, jpeg2000, htj2k)", codecName)
	}
}

func newJ2KFrameEncoder(codecName string, quality int) (*dicomFrameEncoder, source.Compression, error) {
	fac, err := codec.Lookup(codecName)
	if err != nil {
		return nil, 0, err
	}
	ce, err := fac.NewEncoder(codec.LevelGeometry{PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}})
	if err != nil {
		return nil, 0, err
	}
	comp := source.CompressionJPEG2000
	if codecName == "htj2k" {
		comp = source.CompressionHTJ2K
	}
	return &dicomFrameEncoder{codec: ce}, comp, nil
}

func (e *dicomFrameEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	if e.jpeg != nil {
		return e.jpeg.EncodeStandalone(rgb, w, h)
	}
	return e.codec.EncodeTile(rgb, w, h, nil) // J2K-family: already a complete codestream
}

func (e *dicomFrameEncoder) Close() error {
	if e.jpeg != nil {
		return e.jpeg.Close()
	}
	if e.codec != nil {
		return e.codec.Close()
	}
	return nil
}
```

Add imports: `strconv`, `jpegcodec`=`internal/codec/jpeg`, `codec`=`internal/codec`.

VERIFY: confirm the J2K `EncodeTile` output IS a complete, self-contained J2K codestream that `dicomwriter` can wrap as a DICOM frame (the derivedsource path produced standalone JPEG; J2K-family codestreams are inherently self-contained — but sanity-check by decoding a J2K `EncodeTile` output via the opentile decoder in a manual check during Task 4 integration). If the htj2k codec's `EncodeTile` isn't usable as a DICOM frame, scope SP3a's codecs to jpeg + jpeg2000 and defer htj2k.

- [ ] **Step 4: Run to verify it passes + commit**

Run: `go test ./cmd/wsitools/ -run TestDicomFrameEncoder -v 2>&1 | grep -v 'duplicate librar'` → PASS.
```bash
git add cmd/wsitools/dicom_engine.go cmd/wsitools/dicom_engine_test.go
git commit -m "feat(dicom): dicomFrameEncoder — self-contained frames (standalone JPEG / J2K passthrough)"
```

---

## Task 4: Route lossy DICOM transforms through the engine

**Files:**
- Modify: `cmd/wsitools/dicom_engine.go` (add `runDICOMEngine` driver helper)
- Modify: `cmd/wsitools/convert_factor.go` (`downsampleToDICOM` lossy → engine; thread `--codec`)
- Modify: `cmd/wsitools/crop_formats.go` (`cropToDICOM` lossy → engine)
- Test: controller-run integration

- [ ] **Step 1: Add `runDICOMEngine` driver helper to `dicom_engine.go`**

```go
// runDICOMEngine streams srcRegion → outL0 through the engine into a spool, then
// hands a spoolSource to emitDICOM. md/assoc/format describe the OUTPUT (md
// scale-adjusted by the caller). codecName selects the frame codec (default jpeg).
func runDICOMEngine(ctx context.Context, slide *opentile.Slide, srcRegion opentile.Region, outL0 opentile.Size, codecName string, quality, workers int, format string, md source.Metadata, assoc []source.AssociatedImage, opts dicomwriter.Options, output string, force bool) error {
	levels := octaveLevelSpecsFor(outL0, outputTileSize)

	enc, comp, err := newDicomFrameEncoder(codecName, quality)
	if err != nil {
		return err
	}
	defer enc.Close()

	spoolDir, err := os.MkdirTemp("", "wsitools-dcm-spool-*")
	if err != nil {
		return err
	}
	sink, err := newSpoolTileSink(spoolDir, levels)
	if err != nil {
		_ = os.RemoveAll(spoolDir)
		return err
	}

	kernel := resample.Box
	if outL0 == srcRegion.Size {
		kernel = resample.Nearest
	}
	runErr := retile.Run(ctx, retile.Spec{
		Slide: slide, SrcRegion: srcRegion, OutL0: outL0, Levels: levels,
		Kernel: kernel, Encoder: enc, Sink: sink, Workers: workers,
	})
	for _, sp := range sink.spools {
		_ = sp.close() // flush; the source re-reads via ReadAt
	}
	if runErr != nil {
		sink.remove()
		_ = os.RemoveAll(spoolDir)
		return runErr
	}

	src := newSpoolSource(sink, format, comp, md, assoc)
	defer func() { _ = src.Close(); _ = os.RemoveAll(spoolDir) }() // src.Close removes spools
	return emitDICOM(src, opts, output, force)
}
```
(Imports: `context`, `os`, `opentile`, `resample`, `retile`, `source`, `dicomwriter`.) NOTE: `tileSpool.get` uses `ReadAt`, which works after `close()` only if the file is reopened — but `os.File.ReadAt` after `Close()` FAILS. So either DON'T close the spool files before reading (keep them open until `src.Close`), or reopen for reading. SIMPLEST: do NOT `sp.close()` before constructing the source; let `spoolSource.Close()`/`sink.remove()` close+remove them. Remove the `for _, sp := range sink.spools { sp.close() }` loop above and rely on the OS write buffer being flushed by `ReadAt` on the same open handle (ReadAt on a written-but-not-fsync'd file returns the written bytes). VERIFY this works (write then ReadAt on the same `*os.File`); if not, `Sync()` each spool before reading instead of closing.

- [ ] **Step 2: Route `downsampleToDICOM` (convert_factor.go)**

`downsampleToDICOM` currently: materialize → `FromReducedL0` → emitDICOM. Replace the lossy build with the engine. It already computes `outW,outH` (= srcL0/factor) and the scale-adjusted `md` + `assoc`. Replace from the `rasterBytes`/`MaterializeReducedL0`/`FromReducedL0` block through the `emitDICOM` call with:
```go
	srcRegion := opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: srcL0.Size}
	if err := runDICOMEngine(ctx, slide, srcRegion, opentile.Size{W: outW, H: outH}, codecName, quality, workers, src.Format(), md, assoc, dicomwriter.Options{
		Associated:  !noAssociated,
		L0ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"},
	}, output, force); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", output)
	return nil
```
Thread a `codecName string` param into `downsampleToDICOM` (and its caller `dispatchDownsampleByTarget`): the `convert --to dicom` path passes `cvCodec` (default to "jpeg" when empty); the `downsample <dicom>` command passes "jpeg". Read `dispatchDownsampleByTarget`'s signature and add `codecName` (default "jpeg"). `MaterializeReducedL0` is no longer called here (it may become dead — Task 5 audits).

- [ ] **Step 3: Route `cropToDICOM` lossy (crop_formats.go)**

In `cropToDICOM`: keep the LOSSLESS branch (`WithLosslessL0`) UNCHANGED. For the LOSSY branch, replace `FromReducedL0`→`emitDICOM` with the engine. Crop preserves MPP/mag, so `md` is unchanged (`src.Metadata()`). The crop thumbnail must come from the streaming read (not `p.l0`, which is nil on the engine path — but NOTE M5 keeps `p.l0` materialized for `target=="dicom"`; SP3a should stop materializing for lossy dicom too, so update crop.go's `runCrop`/`cropEmit` materialize condition to `lossless` only for the dicom case once the engine path lands — OR keep materializing for lossless-dicom only). Concretely:
- In `cropToDICOM`, for `!p.lossless`: build the crop thumbnail via `streamCropThumbnail(p.src, rect, p.l0W, p.l0H, p.quality)` → a `croppedThumbnail` → swap into `assoc` (replacing `regenCropThumbnailAssoc(assoc, p.l0, …)` which needs the raster). Use the existing `croppedThumbnail` type. Then:
```go
		rect := opentile.Region{Origin: opentile.Point{X: p.ex, Y: p.ey}, Size: opentile.Size{W: p.l0W, H: p.l0H}}
		return runDICOMEngine(p.ctx, p.src, rect, opentile.Size{W: p.l0W, H: p.l0H}, "jpeg", p.quality, p.workers, src.Format(), md, assoc, dicomwriter.Options{
			Associated:  !p.noAssociated,
			L0ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		}, p.output, cropForce)
```
- The lossless branch keeps `regenCropThumbnailAssoc(assoc, p.l0, …)` + `WithLosslessL0`.
- Update the materialize condition in `runCrop` (crop.go) so the cropped raster is materialized for `lossless || (target=="dicom" && lossless)` — i.e. only for lossless now (dicom lossy no longer needs it). Since lossless already implies materialize, the condition simplifies to `lossless` (drop the `|| target=="dicom"`). VERIFY no other lossy-dicom code path reads `p.l0`.

- [ ] **Step 4: Build + unit suite**

Run: `go build ./... 2>&1 | grep -v 'duplicate librar'` → clean.
Run: `go test ./cmd/wsitools/ -run 'TestTileSpool|TestSpoolSink|TestDicomFrameEncoder' 2>&1 | grep -v 'duplicate librar' | tail -3` → PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/dicom_engine.go cmd/wsitools/convert_factor.go cmd/wsitools/crop_formats.go cmd/wsitools/crop.go
git commit -m "feat(dicom): route lossy DICOM downsample/crop through the engine (spool bridge); --codec"
```

- [ ] **Step 6: CONTROLLER integration (DICOM downsample/crop via engine + JP2K)**

The controller runs (DICOM fixtures are dirs of .dcm; pass the dir or a .dcm):
```bash
make build
DCM=$(pwd)/sample_files/dicom/scan_621_grundium_dicom   # a small WSM series
echo "=== DICOM downsample --factor 2 (jpeg) ==="
./bin/wsitools downsample "$DCM" --factor 2 -o /tmp/sp3a-ds.dcm -f 2>&1 | grep -v 'duplicate librar'
./bin/wsitools info /tmp/sp3a-ds.dcm 2>&1 | grep -v 'duplicate librar' | grep -E 'L[0-9]| MPP|Magnif'
./bin/wsitools validate /tmp/sp3a-ds.dcm 2>&1 | grep -v 'duplicate librar' | tail -1
echo "=== DICOM downsample --codec jpeg2000 (the win) ==="
./bin/wsitools convert "$DCM" --to dicom --factor 2 --codec jpeg2000 -o /tmp/sp3a-jp2k.dcm -f 2>&1 | grep -v 'duplicate librar'
./bin/wsitools info /tmp/sp3a-jp2k.dcm 2>&1 | grep -v 'duplicate librar' | grep -E 'L[0-9]|jpeg2000|jpeg'
echo "=== DICOM crop (lossy) ==="
./bin/wsitools crop "$DCM" --rect 0,0,2048,2048 -o /tmp/sp3a-crop.dcm -f 2>&1 | grep -v 'duplicate librar'
./bin/wsitools validate /tmp/sp3a-crop.dcm 2>&1 | grep -v 'duplicate librar' | tail -1
echo "=== DICOM crop --lossless still byte-exact (unchanged passthrough) ==="
./bin/wsitools crop "$DCM" --rect 0,0,2048,2048 --lossless -o /tmp/sp3a-crop-ll.dcm -f 2>&1 | grep -v 'duplicate librar'
./bin/wsitools validate /tmp/sp3a-crop-ll.dcm 2>&1 | grep -v 'duplicate librar' | tail -1
```
Expected: all succeed + `validate` clean (opentile reads them back as valid WSM). The jpeg2000 output's L0 reports `jpeg2000` compression (the derived-codec win). Lossless crop unchanged. (dciodvfy is a manual external gate — run separately on a mac with dclunie's dicom3tools if a conformance check is desired; note it in the report.) If `pydicom` is available, optionally dump TransferSyntaxUID to confirm the JP2K TS.

---

## Task 5: Delete the now-dead JPEG-baseline raster path

**Files:**
- Modify/Delete: `internal/derivedsource/derivedsource.go` (`rasterLevel`, `FromReducedL0`), `internal/derivedsource/transcode.go` (`TranscodeToJPEG` if unused)
- Modify: `internal/downscale/downscale.go` (`MaterializeReducedL0` if now unused)
- Test: caller audit + race

- [ ] **Step 1: Audit callers**

```bash
for sym in FromReducedL0 rasterLevel TranscodeToJPEG MaterializeReducedL0; do
  echo "== $sym =="; grep -rn "$sym" cmd/wsitools/*.go internal/**/*.go 2>/dev/null | grep -v "_test\|func $sym\|// "
done
```
Expected: `FromReducedL0` no longer called (downsample/crop dicom now use the engine). `MaterializeReducedL0` no longer called (was only `downsampleToDICOM`). `WithLosslessL0`/`passthroughLevel` STILL called (lossless DICOM crop) — KEEP. `MaterializeCroppedL0` STILL called (lossless DICOM crop materialize) — KEEP. Only delete symbols with ZERO remaining non-test callers.

- [ ] **Step 2: Delete the dead symbols**

Remove `FromReducedL0` + `rasterLevel` (and its helpers) from `derivedsource`, keeping `passthroughLevel` + `WithLosslessL0`. Remove `MaterializeReducedL0` from `internal/downscale` if unused. Remove `TranscodeToJPEG` if unused. Update `derivedsource`'s package doc. Adjust/trim `derivedsource_test.go` for the removed symbols (delete tests of `FromReducedL0`/`rasterLevel`; keep `WithLosslessL0`/`passthroughLevel` tests).

- [ ] **Step 3: Build + tests**

Run: `go build ./... 2>&1 | grep -v 'duplicate librar'` → clean.
Run: `go test ./internal/derivedsource/ ./internal/downscale/ 2>&1 | grep -v 'duplicate librar' | tail -3` → PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/derivedsource/ internal/downscale/
git commit -m "refactor(dicom): delete the now-dead JPEG-baseline raster path (rasterLevel/FromReducedL0)"
```

- [ ] **Step 5: CONTROLLER full verification**

```bash
WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -race -count=1 -timeout 30m ./internal/retile/ ./internal/dicomwriter/ ./internal/derivedsource/ ./cmd/wsitools/ 2>&1 | grep -v 'duplicate librar' | tail -6
```
Expected: all PASS. Plus re-run the Task 4 Step 6 DICOM conversions to confirm still green post-deletion.

---

## Final verification + finish

Dispatch a final code reviewer over `main..HEAD`, then use **superpowers:finishing-a-development-branch**. Branch: `feat/retile-engine-sp3a-dicom` off `main`.

**SP3a acceptance:**
- `tileSpool` (random-access), `spoolTileSink`, `spoolSource`, `dicomFrameEncoder` (self-contained frames) with unit tests.
- Lossy DICOM downsample/crop route through the engine + spool → `dicomwriter.WritePyramid`; outputs read back via opentile, validate clean; octave-floored levels; metadata + associated preserved.
- `--codec jpeg2000` produces JP2K-compressed DICOM derived levels (the win).
- Lossless DICOM crop still byte-exact (unchanged passthrough); verbatim `convert --to dicom` unchanged.
- The dead JPEG-baseline raster path (`rasterLevel`/`FromReducedL0`/`MaterializeReducedL0`) deleted with no dangling callers.
- Full `-race` green. (dciodvfy = manual external gate, noted.)

---

## Self-Review

**Spec coverage:**
- Spool bridge (spoolTileSink + spoolSource) → Tasks 1-2. ✓
- Self-contained `dicomFrameEncoder` (standalone JPEG / J2K passthrough) → Task 3. ✓
- Lossy DICOM transforms → engine; `--codec`; lossless crop unchanged → Task 4. ✓
- Delete dead JPEG-baseline raster → Task 5. ✓
- JP2K/HTJ2K win + bounded memory + dciodvfy-as-manual-gate + read-back testing → Task 4 Step 6 + Task 5 Step 5. ✓

**Placeholder scan:** none. The "VERIFY DecodedTile unused / ReadAt-after-write works / J2K frame is DICOM-usable / audit callers" notes are explicit verification steps with defined fallbacks (decode the frame / Sync instead of close / scope to jpeg+jpeg2000 / keep symbols with callers).

**Type consistency:** `newTileSpool(path,tiles)→*tileSpool` (`put`/`get`/`close`/`remove`), `newSpoolTileSink(dir,levels)→*spoolTileSink` (`WriteTile`/`remove`), `newSpoolSource(sink,format,comp,md,assoc)→*spoolSource`, `spoolLevel`, `newDicomFrameEncoder(codecName,quality)→(*dicomFrameEncoder, source.Compression, error)` (`EncodeTile`/`Close`), `runDICOMEngine(...)` — consistent across tasks; `spoolSource`/`spoolLevel` implement the real `source.Source`/`source.Level` method sets.

**Risk:** (1) `ReadAt` on a written-but-open spool file — flagged in Task 4 Step 1 with the `Sync` fallback. (2) J2K `EncodeTile` output must be a DICOM-usable codestream — flagged in Task 3 with the "scope to jpeg+jpeg2000, defer htj2k" fallback. (3) Deletion (Task 5) gated on a caller audit + the engine path passing read-back first. (4) dciodvfy unavailable here — read-back + unit conformance attributes substitute; dciodvfy noted as a manual external gate.
