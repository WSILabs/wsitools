# IFE Writer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `convert --to ife` — write conformant IFE v1.0 files with JPEG/AVIF tiles (never IRIS), full metadata fidelity (MPP/mag/ICC/associated/attributes), and a verbatim tile-copy fast path.

**Architecture:** A pure-Go `internal/ife.Writer` emits the binary container with **direct-write + header backpatch** (tile blobs streamed to the output in arrival order, tables + metadata written last, the 38-byte header backpatched). Two feed paths reuse it: an `ifeSink` (the retile engine's re-encode path) and a verbatim tile-copy path — mirroring `streamwriter`+`streamwriterSink`+crop-verbatim. The verification oracle is opentile-go's existing IFE reader.

**Tech Stack:** Go, `encoding/binary` (LE), opentile-go reader, the retile engine (`internal/retile`), the codec registry (`internal/codec`, incl. the existing `png` codec).

**Spec:** `docs/superpowers/specs/2026-06-23-ife-writer-design.md`.

**Branch:** `feat/ife-writer` (already created).

---

## Critical facts (pinned from opentile-go `formats/ife/`)

All little-endian. Constants: `MAGIC = 0x49726973`, `NULL_OFFSET = 0xFFFFFFFFFFFFFFFF` (8B), `NULL_TILE = 0xFFFFFFFFFF` (40-bit), `TileSidePixels = 256`.

**Recovery magics (the `RECOVERY` enum from Iris-Headers `IrisCodecExtension.hpp`)** — **every block gets its correct tag**, because the official Iris-Codec validator (the gold-standard gate, Task 7) checks them even though opentile's reader ignores the tile-path ones: FILE_HEADER `0x5501`, TILE_TABLE `0x5502`, METADATA `0x5504`, ATTRIBUTES `0x5505`, LAYER_EXTENTS `0x5506`, TILE_OFFSETS `0x5507`, ATTRIBUTES_SIZES `0x5508`, ATTRIBUTES_BYTES `0x5509`, IMAGE_ARRAY `0x550A`, IMAGE_BYTES `0x550B`, ICC_PROFILE `0x550C`. (Do NOT write `0` — that passes opentile but fails the official validator.)

**Byte layouts:**

```
FILE_HEADER (38B @ offset 0):
  @0  u32 magic = 0x49726973     @4  u16 recovery=0x5501   @6  u64 file_size
  @14 u16 ext_major=1            @16 u16 ext_minor=0   @18 u32 file_revision=0
  @22 u64 tile_table_offset      @30 u64 metadata_offset

TILE_TABLE (44B):
  @0  u64 validation(=self off)  @8  u16 recovery=0x5502  @10 u8 encoding(2=JPEG,3=AVIF)
  @11 u8 format=2 (R8G8B8)       @12 u64 cipher_offset=NULL_OFFSET
  @20 u64 tile_offsets_offset    @28 u64 layer_extents_offset
  @36 u32 x_extent(native W px)  @40 u32 y_extent(native H px)

LAYER_EXTENTS (16B hdr + 12B×layers):
  hdr: @0 u64 validation  @8 u16 recovery=0x5506  @10 u16 entry_size=12  @12 u32 entry_number=layers
  entry: @0 u32 x_tiles  @4 u32 y_tiles  @8 f32 scale
  STORED COARSEST-FIRST (file index 0 = smallest layer; native layer last).

TILE_OFFSETS (16B hdr + 8B×tiles):
  hdr: @0 u64 validation  @8 u16 recovery=0x5507  @10 u16 entry_size=8  @12 u32 entry_number=total_tiles
  entry: @0 u40 offset  @5 u24 size  (sparse: offset = NULL_TILE)
  ORDER: layers coarsest-first; within a layer row-major (row*x_tiles + col).

METADATA (56B):
  @0 u64 validation  @8 u16 recovery=0x5504  @10 u16 codec_major  @12 u16 codec_minor
  @14 u16 codec_build  @16 u64 attributes_offset  @24 u64 images_offset
  @32 u64 icc_offset  @40 u64 annotations_offset=NULL_OFFSET
  @48 f32 microns_per_pixel  @52 f32 magnification
  (any sub-block pointer NULL_OFFSET when absent)

ICC_PROFILE (14B hdr + bytes):
  @0 u64 validation  @8 u16 recovery=0x550C  @10 u32 byte_count  then ICC bytes

IMAGE_ARRAY (16B hdr + 20B×images):
  hdr: @0 u64 validation  @8 u16 recovery=0x550A  @10 u16 entry_size=20  @12 u32 entry_number
  entry: @0 u64 bytes_offset  @8 u32 width  @12 u32 height  @16 u8 encoding(1=PNG,2=JPEG,3=AVIF)
         @17 u8 format=2  @18 u16 orientation=0
IMAGE_BYTES (16B hdr + title + img):
  @0 u64 validation  @8 u16 recovery=0x550B  @10 u16 title_size  @12 u32 image_size
  then title UTF-8 (title_size bytes), then compressed image (image_size bytes)
  TITLE = the associated type string ("label","macro","thumbnail","overview") — round-trips
  via the reader's case-insensitive normaliseAssociatedType.

ATTRIBUTES (29B):
  @0 u64 validation  @8 u16 recovery=0x5505  @10 u8 format=1(FreeText)  @11 u16 version=0
  @13 u64 lengths_offset  @21 u64 byte_array_offset
ATTRIBUTES_SIZES (16B hdr + 6B×entries):
  hdr: @0 u64 validation  @8 u16 recovery=0x5508  @10 u16 entry_size=6  @12 u32 entry_number
  entry: @0 u16 key_size  @2 u32 value_size
ATTRIBUTES_BYTES (14B hdr + concatenated bytes):
  @0 u64 validation  @8 u16 recovery=0x5509  @10 u32 total_byte_count
  then for each entry: key bytes (key_size) immediately followed by value bytes (value_size),
  entries concatenated in the same order as ATTRIBUTES_SIZES.
```

**THE PADDING QUIRK (read before writing any test):** opentile derives each level's pixel size as `x_tiles*256 × y_tiles*256` — NOT from `x_extent`. So a 2220×2967 source reads back from IFE as **2304×3072** (`ceil(2220/256)*256 × ceil(2967/256)*256`). Round-trip tests MUST assert the *padded* dims, and pixel-parity must be taken on the true sub-region (a crop to source dims) or use a source whose dims are already 256-multiples. The writer still sets `x_extent`/`y_extent` to true native pixels (spec-faithful; reader ignores them for dims).

**Scale field:** reader computes `downsample = max_scale / scale`. For an octave pyramid with native at file-index N-1, set `scale[fileIdx] = 1.0 / outputDownsample(apiLevel)` where apiLevel maps native=0. Concretely, write each layer's `scale = float32(nativeLongestSideTiles) / float32(thisLayerLongestSideTiles)` is fragile; instead use the simplest correct scheme: `scale = 1.0` for native, `0.5` for the 2× level, `0.25` for 4×, etc. — i.e. `scale[apiLevel] = 1.0 / 2^apiLevel`, then store the array reversed (coarsest-first). The reader's `max_scale` is the native `1.0`, giving `downsample = 1.0/scale = 2^apiLevel`. Correct octave ratios.

---

## File structure

- **Create `internal/ife/ife.go`** — package consts (magic, NULL_OFFSET, NULL_TILE, TileSidePixels, encoding/format/recovery enums) + the LE `putUint40`/`putUint24` helpers.
- **Create `internal/ife/writer.go`** — the `Writer` type: `Create`, `AddLevel`, `WriteTile`, the metadata setters (`SetMPP`, `SetMagnification`, `SetICCProfile`, `AddAssociated`, `SetAttributes`), `Finalize`, `Abort`. Owns offset recording + the `Finalize` block-emission + header backpatch.
- **Create `internal/ife/writer_test.go`** — synthetic round-trip unit tests via the opentile-go reader.
- **Create `cmd/wsitools/convert_ife.go`** — the `convert --to ife` driver: eligibility, dispatch (verbatim vs engine), `ifeSink`, metadata assembly.
- **Create `cmd/wsitools/convert_ife_test.go`** — integration tests (`//go:build integration`)? No — put end-to-end tests in `tests/integration/ife_test.go`.
- **Create `tests/integration/ife_test.go`** — end-to-end `convert --to ife` round-trip + verbatim byte-identity (`//go:build integration`).
- **Modify `cmd/wsitools/capabilities.go`** — add the `"ife"` case to `containerCapabilities`.
- **Modify `cmd/wsitools/convert.go`** — add `ife` to the `--to` help/target list and dispatch.
- **Modify `cmd/wsitools/convert_tiff.go` (or wherever `--to` dispatches)** — route `ife` to `runConvertIFE`.
- **Create `scripts/ife_validate.py`** — the official Iris-Codec validator wrapper (Task 6).
- **Modify `Makefile`** — `ife-validate` target (Task 6).
- **Modify `.github/workflows/ci.yml`** — install Iris-Codec + run `make ife-validate` (Task 6).

---

## Task 1: `internal/ife` package consts + LE helpers

**Files:** Create `internal/ife/ife.go`, `internal/ife/ife_test.go`.

- [ ] **Step 1: Write the failing test** — `internal/ife/ife_test.go`:

```go
package ife

import "testing"

func TestPutUint40(t *testing.T) {
	var b [5]byte
	putUint40(b[:], 0x123456789A)
	want := [5]byte{0x9A, 0x78, 0x56, 0x34, 0x12}
	if b != want {
		t.Errorf("putUint40 = % x, want % x", b, want)
	}
}

func TestPutUint24(t *testing.T) {
	var b [3]byte
	putUint24(b[:], 0xABCDEF)
	want := [3]byte{0xEF, 0xCD, 0xAB}
	if b != want {
		t.Errorf("putUint24 = % x, want % x", b, want)
	}
}

func TestConsts(t *testing.T) {
	if magicBytes != 0x49726973 {
		t.Errorf("magic = %#x", magicBytes)
	}
	if nullTile != 0xFFFFFFFFFF {
		t.Errorf("nullTile = %#x", nullTile)
	}
	if tileSidePixels != 256 {
		t.Errorf("tileSide = %d", tileSidePixels)
	}
}
```

- [ ] **Step 2: Run — FAIL** (`undefined: putUint40` etc.)

Run: `go test ./internal/ife/ -run 'PutUint40|PutUint24|Consts'`
Expected: FAIL (build error, undefined symbols).

- [ ] **Step 3: Implement** — `internal/ife/ife.go`:

```go
// Package ife writes Iris File Extension (IFE) v1.0 whole-slide files with
// JPEG/AVIF tiles. It never writes the IRIS-proprietary codec. The container is
// all little-endian; every block opens with an 8-byte validation field (== the
// block's own file offset) and a 2-byte recovery magic. Verified against
// opentile-go's IFE reader (formats/ife).
package ife

// On-disk constants (opentile-go formats/ife).
const (
	magicBytes     uint32 = 0x49726973 // "Iris" as LE uint32
	nullOffset     uint64 = 0xFFFFFFFFFFFFFFFF
	nullTile       uint64 = 0xFFFFFFFFFF // 40-bit all-ones
	tileSidePixels        = 256

	extMajor uint16 = 1
	extMinor uint16 = 0

	// Pyramid-tile encoding (TILE_TABLE.encoding).
	encJPEG uint8 = 2
	encAVIF uint8 = 3
	// Associated-image encoding (IMAGE_ENTRY.encoding); note 1=PNG here.
	imgEncPNG  uint8 = 1
	imgEncJPEG uint8 = 2
	imgEncAVIF uint8 = 3

	formatR8G8B8 uint8 = 2

	attrFormatFreeText uint8 = 1

	// Recovery magics — the RECOVERY enum from Iris-Headers IrisCodecExtension.hpp.
	// EVERY block gets its correct tag: the official Iris-Codec validator checks
	// them even though opentile's reader ignores the tile-path ones.
	recoverHeader          uint16 = 0x5501 // FILE_HEADER
	recoverTileTable       uint16 = 0x5502 // TILE_TABLE
	recoverMetadata        uint16 = 0x5504
	recoverAttributes      uint16 = 0x5505
	recoverLayerExtents    uint16 = 0x5506 // LAYER_EXTENTS
	recoverTileOffsets     uint16 = 0x5507 // TILE_OFFSETS
	recoverAttributesSizes uint16 = 0x5508
	recoverAttributesBytes uint16 = 0x5509
	recoverImageArray      uint16 = 0x550A
	recoverImageBytes      uint16 = 0x550B
	recoverICCProfile      uint16 = 0x550C
)

// putUint40 writes v as a 40-bit little-endian integer into b[0:5].
func putUint40(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
}

// putUint24 writes v as a 24-bit little-endian integer into b[0:3].
func putUint24(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
}
```

- [ ] **Step 4: Run — PASS**. `go test ./internal/ife/ -run 'PutUint40|PutUint24|Consts'`. `gofmt -l internal/ife/` clean.

- [ ] **Step 5: Commit**

```bash
git add internal/ife/ife.go internal/ife/ife_test.go
git commit -m "feat(ife): package consts + LE u40/u24 helpers

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `Writer` core — bare pyramid (header, tables, tile blobs, 56-byte METADATA)

**Files:** Create `internal/ife/writer.go`, extend `internal/ife/writer_test.go`.

This is the heart of the writer. The `Writer` opens the output, writes a placeholder header, accepts levels + tile blobs (recording offsets), and `Finalize` emits TILE_TABLE + LAYER_EXTENTS + TILE_OFFSETS + a 56-byte METADATA (all sub-block pointers NULL except MPP/mag), then backpatches the header.

- [ ] **Step 1: Write the failing test** — `internal/ife/writer_test.go`. The oracle is opentile-go's reader.

```go
package ife

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// solidTile returns a minimal valid JPEG for a 256x256 tile (any decodable JPEG;
// the reader does not decode, so a tiny baseline JPEG suffices). Built once.
func solidTile(t *testing.T) []byte {
	t.Helper()
	return testJPEG256 // a package var holding a precomputed 256x256 JPEG; see Step 3 note
}

func TestWriterBarePyramid(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.iris")

	w, err := Create(out, Options{Encoding: encJPEG, MPP: 0.25, Magnification: 20})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Two layers: native 2 tiles x 1 tile (512x256), downsample-2 1x1 (256x256).
	// AddLevel is called native-first; the writer stores coarsest-first internally.
	w.AddLevel(2, 1) // native: 2x1 tiles
	w.AddLevel(1, 1) // /2:     1x1 tiles
	tile := solidTile(t)
	// native level 0
	if err := w.WriteTile(0, 0, 0, tile); err != nil { t.Fatal(err) }
	if err := w.WriteTile(0, 1, 0, tile); err != nil { t.Fatal(err) }
	// level 1
	if err := w.WriteTile(1, 0, 0, tile); err != nil { t.Fatal(err) }
	if err := w.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Oracle: opentile-go re-opens it.
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile: %v", err)
	}
	defer sl.Close()
	if got := string(sl.Format()); got != "ife" {
		t.Errorf("format = %q, want ife", got)
	}
	levels := sl.Levels()
	if len(levels) != 2 {
		t.Fatalf("levels = %d, want 2", len(levels))
	}
	// PADDING QUIRK: dims are x_tiles*256 x y_tiles*256.
	if levels[0].Size.W != 512 || levels[0].Size.H != 256 {
		t.Errorf("L0 size = %dx%d, want 512x256", levels[0].Size.W, levels[0].Size.H)
	}
	if levels[1].Size.W != 256 || levels[1].Size.H != 256 {
		t.Errorf("L1 size = %dx%d, want 256x256", levels[1].Size.W, levels[1].Size.H)
	}
	md := sl.Metadata()
	if md.MPP.X != 0.25 {
		t.Errorf("MPP.X = %v, want 0.25", md.MPP.X)
	}
	if md.Magnification != 20 {
		t.Errorf("Magnification = %v, want 20", md.Magnification)
	}
	_ = os.Stat
}
```

> **Step 1 note on `testJPEG256`:** add a tiny helper file `internal/ife/testdata_test.go` that builds a 256×256 RGB JPEG once via the repo's existing JPEG encoder (`internal/codec/jpeg`), e.g. `jpeg.Factory{}.NewEncoder(...).EncodeStandalone(blackRGB)`, stored in a package-level `var testJPEG256 []byte` initialised in `TestMain`. Use the same encoder the writer's callers use so the bytes are valid. (Exact: mirror how `internal/dicomwriter` test helpers build tiles.)

- [ ] **Step 2: Run — FAIL** (`undefined: Create`, `Options`, etc.)

Run: `go test ./internal/ife/ -run TestWriterBarePyramid`
Expected: FAIL (undefined symbols).

- [ ] **Step 3: Implement** — `internal/ife/writer.go`. Key design: `WriteTile` appends the blob to the file immediately (serial; the engine's sinkDrainer calls it serially) and records `(fileLayer, col, row) → (offset, size)`. `AddLevel` is called native-first; convert to file-layer (coarsest-first) by reversing at `Finalize`. Tile-offset linear index uses the file (coarsest-first) order.

```go
package ife

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// Options configures a Writer at Create time.
type Options struct {
	Encoding      uint8   // encJPEG or encAVIF (pyramid-tile codec)
	XExtent       uint32  // native width in pixels (informational; reader derives dims from tiles)
	YExtent       uint32  // native height in pixels
	MPP           float64 // microns per pixel (0 = unknown)
	Magnification float64 // 0 = unknown
	CodecMajor    uint16
	CodecMinor    uint16
	CodecBuild    uint16
}

type tileRec struct {
	offset uint64
	size   uint32
}

type levelGrid struct {
	xTiles, yTiles uint32
	tiles          map[[2]int]tileRec // [col,row] -> rec; absent = sparse
}

// Writer emits one IFE v1.0 file. Levels are added native-first; the file stores
// them coarsest-first. Not safe for concurrent use; the engine drains tiles serially.
type Writer struct {
	f       *os.File
	path    string
	tmpPath string
	opts    Options
	pos     int64       // current append position (after the placeholder header)
	levels  []levelGrid // native-first (index 0 = native)
	icc     []byte
	assoc   []assocImage
	attrs   [][2]string // ordered key/value
	closed  bool
}

type assocImage struct {
	title       string
	width       uint32
	height      uint32
	encoding    uint8 // imgEncPNG/JPEG/AVIF
	blob        []byte
}

const fileHeaderSize = 38

// Create opens path for writing (via path+".tmp", atomic-renamed on Finalize) and
// reserves the 38-byte FILE_HEADER. Tile blobs are appended after it.
func Create(path string, opts Options) (*Writer, error) {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, fmt.Errorf("ife: create %s: %w", tmp, err)
	}
	// Reserve the header by writing 38 zero bytes; backpatched in Finalize.
	if _, err := f.Write(make([]byte, fileHeaderSize)); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, fmt.Errorf("ife: reserve header: %w", err)
	}
	return &Writer{f: f, path: path, tmpPath: tmp, opts: opts, pos: fileHeaderSize}, nil
}

// AddLevel registers a pyramid level's tile-grid dimensions. Call native-first.
func (w *Writer) AddLevel(xTiles, yTiles uint32) {
	w.levels = append(w.levels, levelGrid{
		xTiles: xTiles, yTiles: yTiles, tiles: map[[2]int]tileRec{},
	})
}

// WriteTile appends a compressed tile blob and records its offset/size. apiLevel
// is native-first (0 = native). Errors if the blob exceeds the 24-bit size cap.
func (w *Writer) WriteTile(apiLevel, col, row int, blob []byte) error {
	if apiLevel < 0 || apiLevel >= len(w.levels) {
		return fmt.Errorf("ife: WriteTile level %d out of range", apiLevel)
	}
	if len(blob) > 0xFFFFFF {
		return fmt.Errorf("ife: tile %d,%d,%d is %d bytes (>16MB 24-bit cap)", apiLevel, col, row, len(blob))
	}
	if w.pos+int64(len(blob)) > int64(nullTile) { // 40-bit offset cap (1 TB)
		return fmt.Errorf("ife: file exceeds 40-bit offset cap")
	}
	n, err := w.f.WriteAt(blob, w.pos)
	if err != nil {
		return fmt.Errorf("ife: write tile: %w", err)
	}
	w.levels[apiLevel].tiles[[2]int{col, row}] = tileRec{offset: uint64(w.pos), size: uint32(len(blob))}
	w.pos += int64(n)
	return nil
}

// SetICCProfile records the ICC blob to emit (nil/empty => no ICC_PROFILE block).
func (w *Writer) SetICCProfile(icc []byte) { w.icc = icc }

// Finalize writes the trailing blocks and backpatches the header, then atomically
// renames tmp -> path. After Finalize (or Abort) the Writer must not be reused.
func (w *Writer) Finalize() (err error) {
	if w.closed {
		return fmt.Errorf("ife: Finalize after close")
	}
	defer func() {
		w.closed = true
		cerr := w.f.Close()
		if err == nil && cerr != nil {
			err = cerr
		}
		if err != nil {
			os.Remove(w.tmpPath)
			return
		}
		err = os.Rename(w.tmpPath, w.path)
	}()

	// File layers are coarsest-first = reverse of native-first.
	n := len(w.levels)
	fileOrder := make([]int, n) // fileOrder[fileIdx] = apiLevel
	for i := range fileOrder {
		fileOrder[i] = n - 1 - i
	}

	put := binary.LittleEndian

	// --- METADATA (+ sub-blocks ICC/IMAGE_ARRAY/ATTRIBUTES) ---
	// Sub-blocks are written first so METADATA can point at them; METADATA last
	// of the metadata group. (Order in-file is irrelevant; offsets are explicit.)
	iccOff := nullOffset
	if len(w.icc) > 0 {
		iccOff = uint64(w.pos)
		if err = w.writeICC(); err != nil {
			return err
		}
	}
	imagesOff := nullOffset
	if len(w.assoc) > 0 {
		if imagesOff, err = w.writeImageArray(); err != nil {
			return err
		}
	}
	attrsOff := nullOffset
	if len(w.attrs) > 0 {
		if attrsOff, err = w.writeAttributes(); err != nil {
			return err
		}
	}
	metadataOff := uint64(w.pos)
	meta := make([]byte, 56)
	put.PutUint64(meta[0:8], metadataOff)
	put.PutUint16(meta[8:10], recoverMetadata)
	put.PutUint16(meta[10:12], w.opts.CodecMajor)
	put.PutUint16(meta[12:14], w.opts.CodecMinor)
	put.PutUint16(meta[14:16], w.opts.CodecBuild)
	put.PutUint64(meta[16:24], attrsOff)
	put.PutUint64(meta[24:32], imagesOff)
	put.PutUint64(meta[32:40], iccOff)
	put.PutUint64(meta[40:48], nullOffset) // annotations
	put.PutUint32(meta[48:52], math.Float32bits(float32(w.opts.MPP)))
	put.PutUint32(meta[52:56], math.Float32bits(float32(w.opts.Magnification)))
	if err = w.appendBlock(meta); err != nil {
		return err
	}

	// --- LAYER_EXTENTS (coarsest-first) ---
	layerExtOff := uint64(w.pos)
	le := make([]byte, blockHeaderValidation+12*n)
	put.PutUint64(le[0:8], layerExtOff)
	put.PutUint16(le[8:10], recoverLayerExtents)
	put.PutUint16(le[10:12], 12)
	put.PutUint32(le[12:16], uint32(n))
	for fi, api := range fileOrder {
		base := blockHeaderValidation + 12*fi
		g := w.levels[api]
		put.PutUint32(le[base:base+4], g.xTiles)
		put.PutUint32(le[base+4:base+8], g.yTiles)
		// scale = 1/2^api ; native (api 0) = 1.0, reader's max_scale.
		scale := float32(1.0) / float32(int64(1)<<uint(api))
		put.PutUint32(le[base+8:base+12], math.Float32bits(scale))
	}
	if err = w.appendBlock(le); err != nil {
		return err
	}

	// --- TILE_OFFSETS (coarsest-first, row-major) ---
	var totalTiles int
	for _, g := range w.levels {
		totalTiles += int(g.xTiles) * int(g.yTiles)
	}
	tileOffOff := uint64(w.pos)
	to := make([]byte, blockHeaderValidation+8*totalTiles)
	put.PutUint64(to[0:8], tileOffOff)
	put.PutUint16(to[8:10], recoverTileOffsets)
	put.PutUint16(to[10:12], 8)
	put.PutUint32(to[12:16], uint32(totalTiles))
	idx := 0
	for _, api := range fileOrder {
		g := w.levels[api]
		for row := 0; row < int(g.yTiles); row++ {
			for col := 0; col < int(g.xTiles); col++ {
				base := blockHeaderValidation + 8*idx
				rec, ok := g.tiles[[2]int{col, row}]
				if ok {
					putUint40(to[base:base+5], rec.offset)
					putUint24(to[base+5:base+8], rec.size)
				} else {
					putUint40(to[base:base+5], nullTile)
					putUint24(to[base+5:base+8], 0)
				}
				idx++
			}
		}
	}
	if err = w.appendBlock(to); err != nil {
		return err
	}

	// --- TILE_TABLE ---
	ttOff := uint64(w.pos)
	tt := make([]byte, 44)
	put.PutUint64(tt[0:8], ttOff)
	put.PutUint16(tt[8:10], recoverTileTable)
	tt[10] = w.opts.Encoding
	tt[11] = formatR8G8B8
	put.PutUint64(tt[12:20], nullOffset) // cipher
	put.PutUint64(tt[20:28], tileOffOff)
	put.PutUint64(tt[28:36], layerExtOff)
	put.PutUint32(tt[36:40], w.opts.XExtent)
	put.PutUint32(tt[40:44], w.opts.YExtent)
	if err = w.appendBlock(tt); err != nil {
		return err
	}

	// --- FILE_HEADER backpatch ---
	fileSize := uint64(w.pos)
	hdr := make([]byte, fileHeaderSize)
	put.PutUint32(hdr[0:4], magicBytes)
	put.PutUint16(hdr[4:6], recoverHeader)
	put.PutUint64(hdr[6:14], fileSize)
	put.PutUint16(hdr[14:16], extMajor)
	put.PutUint16(hdr[16:18], extMinor)
	put.PutUint32(hdr[18:22], 0)
	put.PutUint64(hdr[22:30], ttOff)
	put.PutUint64(hdr[30:38], metadataOff)
	if _, err = w.f.WriteAt(hdr, 0); err != nil {
		return fmt.Errorf("ife: backpatch header: %w", err)
	}
	return nil
}

// appendBlock writes b at the current append position and advances pos.
func (w *Writer) appendBlock(b []byte) error {
	if _, err := w.f.WriteAt(b, w.pos); err != nil {
		return fmt.Errorf("ife: append block: %w", err)
	}
	w.pos += int64(len(b))
	return nil
}

// Abort closes and removes the temp file without producing output.
func (w *Writer) Abort() {
	if w.closed {
		return
	}
	w.closed = true
	w.f.Close()
	os.Remove(w.tmpPath)
}

// writeICC, writeImageArray, writeAttributes are implemented in Task 4 (Slice 3);
// in this task provide stub bodies that panic if called, since bare-pyramid
// never populates icc/assoc/attrs:
//   func (w *Writer) writeICC() error { panic("icc: implemented in slice 3") }
//   func (w *Writer) writeImageArray() (uint64, error) { panic("...") }
//   func (w *Writer) writeAttributes() (uint64, error) { panic("...") }
```

> **Implementer:** the three `write*` stubs keep Task 2 compiling. Task 4 replaces them. `SetMPP`/`SetMagnification` aren't needed — MPP/mag come via `Options`. `AddAssociated`/`SetAttributes` are added in Slice 3 (Task 4).

- [ ] **Step 4: Run — PASS**. `go test ./internal/ife/ -run TestWriterBarePyramid`. `gofmt -l internal/ife/` clean. `go vet ./internal/ife/`.

- [ ] **Step 5: Commit**

```bash
git add internal/ife/writer.go internal/ife/writer_test.go internal/ife/testdata_test.go
git commit -m "feat(ife): Writer core — bare pyramid round-trips through opentile

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `convert --to ife` engine path + capability gate (Slice 2)

**Files:** Create `cmd/wsitools/convert_ife.go`; modify `cmd/wsitools/capabilities.go`, `cmd/wsitools/convert.go`, and the `--to` dispatch (`cmd/wsitools/convert_tiff.go` `runConvertTIFF` target switch — match how `bif` dispatches, see `convert_bif.go`/`runConvertToBIF`). Create `tests/integration/ife_test.go`.

- [ ] **Step 1: Capability-gate unit test** — extend `cmd/wsitools/capabilities_test.go`:

```go
func TestContainerCapabilitiesIFE(t *testing.T) {
	caps := containerCapabilities("ife")
	for _, c := range []string{"jpeg", "avif"} {
		if !codecInSet(caps.conformant, c) {
			t.Errorf("ife should accept %s", c)
		}
	}
	for _, c := range []string{"jpeg2000", "htj2k", "jpegxl", "webp", "png"} {
		if codecInSet(caps.conformant, c) {
			t.Errorf("ife should NOT list %s conformant", c)
		}
	}
}
```

- [ ] **Step 2: Run — FAIL** (`containerCapabilities("ife")` returns empty caps).

Run: `go test ./cmd/wsitools/ -run TestContainerCapabilitiesIFE`
Expected: FAIL.

- [ ] **Step 3: Implement the capability entry** — in `cmd/wsitools/capabilities.go`, add to the `switch container` in `containerCapabilities`:

```go
	case "ife":
		return containerCaps{
			conformant: []string{"jpeg", "avif"},
			redirect:   "IFE tiles are jpeg or avif",
		}
```

- [ ] **Step 4: Implement `runConvertIFE` (engine path only)** — `cmd/wsitools/convert_ife.go`. Mirror `downsampleToTIFF`/the engine targets: resolve codec via `resolveTransformCodec` (gate to jpeg/avif), open source, build the `ifeSink`, run the engine to 256px tiles, set MPP/mag from source metadata, `Finalize`. Use `ife.Writer`. The `ifeSink`:

```go
// ifeSink adapts ife.Writer to retile.TileSink. The engine emits (level,col,row)
// native-first; ife.Writer.WriteTile takes the same convention.
type ifeSink struct{ w *ife.Writer }

func (s ifeSink) WriteTile(level, col, row int, encoded []byte) error {
	return s.w.WriteTile(level, col, row, encoded)
}
```

Driver skeleton (fill from the engine pattern in `convert_factor.go` `downsampleToTIFF` — same level computation + `runEngineRetile`, with `outL0 = sourceL0` for the no-transform case and 256px tiles):

```go
func runConvertIFE(ctx context.Context, input, output string, factor, targetMag int,
	quality string, codecName string, workers int, force, noAssociated bool) error {
	// 1. Gate codec: only jpeg/avif (validateCodec already runs in runConvert; this is belt-and-suspenders).
	if codecName == "" { codecName = "jpeg" }
	if codecName != "jpeg" && codecName != "avif" {
		return fmt.Errorf("convert --to ife: --codec %q not supported; IFE tiles are jpeg or avif", codecName)
	}
	// 2. Open source, compute output levels (octave-floored from outL0), tile size = 256.
	// 3. Create ife.Writer with Options{Encoding: encFor(codecName), XExtent/YExtent = native px, MPP, Magnification}.
	// 4. For each output level: w.AddLevel(ceil(levelW/256), ceil(levelH/256)).
	// 5. Run the retile engine (256px tiles) into ifeSink{w}. (Associated images: Slice 3.)
	// 6. w.Finalize().
	// See convert_factor.go downsampleToTIFF for the exact engine invocation; substitute the sink + 256 tile size.
}
```

> **Implementer:** `encFor("jpeg")` returns the `ife` package's encoding byte — export a small helper `ife.EncodingFor(codec string) (uint8, bool)` (jpeg→2, avif→3) rather than leaking the consts. Add it to `internal/ife/ife.go` with a test. The engine's tile size is set where `downsampleToTIFF` sets `outputTileSize`; IFE forces 256.

- [ ] **Step 5: Wire `--to ife` dispatch + help** — in `convert.go` add `ife` to the `--to` flag description list; in the target switch (where `bif`/`dzi`/etc. dispatch) add `case "ife": return runConvertIFE(...)`. Mirror `convert_bif.go`'s wiring exactly.

- [ ] **Step 6: Integration round-trip test** — `tests/integration/ife_test.go` (`//go:build integration`):

```go
//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

func TestConvertToIFE_RoundTrip(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out.iris")
	bin := buildOnce(t)
	if b, err := runCmd(bin, "convert", "--to", "ife", "-o", out, src); err != nil {
		t.Fatalf("convert --to ife: %v\n%s", err, b)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer sl.Close()
	if string(sl.Format()) != "ife" {
		t.Errorf("format = %q, want ife", sl.Format())
	}
	if len(sl.Levels()) == 0 {
		t.Fatal("no levels")
	}
	// PADDING QUIRK: L0 dims are ceil(srcW/256)*256 x ceil(srcH/256)*256.
	// CMU-1-Small-Region is 2220x2967 -> 2304x3072.
	if w, h := sl.Levels()[0].Size.W, sl.Levels()[0].Size.H; w != 2304 || h != 3072 {
		t.Errorf("L0 = %dx%d, want 2304x3072 (256-padded)", w, h)
	}
}
```

> **Implementer:** match the existing integration helpers (`testdir`, `buildOnce`, `runCmd`/`runBin`) used in `tests/integration/downsample_test.go`. Run with `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestConvertToIFE`.

- [ ] **Step 7: Run all** — `go test ./cmd/wsitools/ -run 'IFE|Capabilities'` PASS; integration round-trip PASS (controller runs the fixture-gated integration test). `gofmt`/`go vet` clean.

- [ ] **Step 8: Commit**

```bash
git add cmd/wsitools/convert_ife.go cmd/wsitools/capabilities.go cmd/wsitools/capabilities_test.go cmd/wsitools/convert.go cmd/wsitools/convert_tiff.go tests/integration/ife_test.go internal/ife/ife.go internal/ife/ife_test.go
git commit -m "feat(ife): convert --to ife engine path + capability gate

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Metadata sub-blocks — ICC, IMAGE_ARRAY, ATTRIBUTES (Slice 3)

**Files:** Modify `internal/ife/writer.go` (replace the three stub bodies + add `AddAssociated`/`SetAttributes`); extend `internal/ife/writer_test.go`; wire association+ICC+attrs assembly in `cmd/wsitools/convert_ife.go`.

- [ ] **Step 1: Failing test for ICC round-trip** — `internal/ife/writer_test.go`:

```go
func TestWriterICCRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "icc.iris")
	icc := []byte("FAKE-ICC-PROFILE-BYTES-0123456789")
	w, _ := Create(out, Options{Encoding: encJPEG, MPP: 0.5})
	w.SetICCProfile(icc)
	w.AddLevel(1, 1)
	w.WriteTile(0, 0, 0, solidTile(t))
	if err := w.Finalize(); err != nil { t.Fatal(err) }

	sl, err := opentile.OpenFile(out)
	if err != nil { t.Fatal(err) }
	defer sl.Close()
	if got := sl.ICCProfile(); string(got) != string(icc) {
		t.Errorf("ICC = %q, want %q", got, icc)
	}
}
```

- [ ] **Step 2: Run — FAIL** (panics in `writeICC` stub).

- [ ] **Step 3: Implement `writeICC`** — replace the stub in `writer.go`:

```go
// writeICC appends the ICC_PROFILE block at the current position. Caller has
// already captured w.pos as the block offset.
func (w *Writer) writeICC() error {
	put := binary.LittleEndian
	b := make([]byte, 14+len(w.icc))
	put.PutUint64(b[0:8], uint64(w.pos))
	put.PutUint16(b[8:10], recoverICCProfile)
	put.PutUint32(b[10:14], uint32(len(w.icc)))
	copy(b[14:], w.icc)
	return w.appendBlock(b)
}
```

- [ ] **Step 4: Run — PASS** (`go test ./internal/ife/ -run ICC`).

- [ ] **Step 5: Failing test for associated images** — add `AddAssociated` + a round-trip test asserting an emitted label round-trips with the right type + bytes (use `solidTile` as the blob, `imgEncJPEG`, title "label"):

```go
func TestWriterAssociatedRoundTrip(t *testing.T) {
	dir := t.TempDir(); out := filepath.Join(dir, "assoc.iris")
	w, _ := Create(out, Options{Encoding: encJPEG})
	label := solidTile(t)
	w.AddAssociated("label", 256, 256, imgEncJPEG, label)
	w.AddLevel(1, 1); w.WriteTile(0, 0, 0, solidTile(t))
	if err := w.Finalize(); err != nil { t.Fatal(err) }

	sl, _ := opentile.OpenFile(out); defer sl.Close()
	imgs := sl.AssociatedImages()
	if len(imgs) != 1 { t.Fatalf("assoc = %d, want 1", len(imgs)) }
	if imgs[0].Type() != opentile.AssociatedLabel {
		t.Errorf("type = %v, want label", imgs[0].Type())
	}
}
```

- [ ] **Step 6: Implement `AddAssociated` + `writeImageArray`** — write each IMAGE_BYTES block (recording its offset), then the IMAGE_ARRAY header + entries pointing at them. Returns the IMAGE_ARRAY offset:

```go
func (w *Writer) AddAssociated(title string, width, height uint32, encoding uint8, blob []byte) {
	w.assoc = append(w.assoc, assocImage{title: title, width: width, height: height, encoding: encoding, blob: blob})
}

func (w *Writer) writeImageArray() (uint64, error) {
	put := binary.LittleEndian
	offs := make([]uint64, len(w.assoc))
	for i, a := range w.assoc {
		offs[i] = uint64(w.pos)
		ib := make([]byte, 16+len(a.title)+len(a.blob))
		put.PutUint64(ib[0:8], uint64(w.pos))
		put.PutUint16(ib[8:10], recoverImageBytes)
		put.PutUint16(ib[10:12], uint16(len(a.title)))
		put.PutUint32(ib[12:16], uint32(len(a.blob)))
		copy(ib[16:16+len(a.title)], a.title)
		copy(ib[16+len(a.title):], a.blob)
		if err := w.appendBlock(ib); err != nil {
			return 0, err
		}
	}
	arrOff := uint64(w.pos)
	ab := make([]byte, blockHeaderValidation+20*len(w.assoc))
	put.PutUint64(ab[0:8], arrOff)
	put.PutUint16(ab[8:10], recoverImageArray)
	put.PutUint16(ab[10:12], 20)
	put.PutUint32(ab[12:16], uint32(len(w.assoc)))
	for i, a := range w.assoc {
		base := blockHeaderValidation + 20*i
		put.PutUint64(ab[base:base+8], offs[i])
		put.PutUint32(ab[base+8:base+12], a.width)
		put.PutUint32(ab[base+12:base+16], a.height)
		ab[base+16] = a.encoding
		ab[base+17] = formatR8G8B8
		put.PutUint16(ab[base+18:base+20], 0)
	}
	if err := w.appendBlock(ab); err != nil {
		return 0, err
	}
	return arrOff, nil
}
```

- [ ] **Step 7: Run — PASS** (`-run Associated`).

- [ ] **Step 8: Failing test for ATTRIBUTES** — set two k/v pairs, assert they round-trip via `sl.Metadata()` Properties (confirm the exact accessor opentile exposes — `Metadata().Properties` map; pin from the reader). Then implement `SetAttributes` + `writeAttributes` (write ATTRIBUTES_SIZES, ATTRIBUTES_BYTES, then the 29-byte ATTRIBUTES header pointing at them; per the layout table). Body:

```go
func (w *Writer) SetAttributes(kvs [][2]string) { w.attrs = kvs }

func (w *Writer) writeAttributes() (uint64, error) {
	put := binary.LittleEndian
	// ATTRIBUTES_SIZES
	sizesOff := uint64(w.pos)
	sb := make([]byte, blockHeaderValidation+6*len(w.attrs))
	put.PutUint64(sb[0:8], sizesOff)
	put.PutUint16(sb[8:10], recoverAttributesSizes)
	put.PutUint16(sb[10:12], 6)
	put.PutUint32(sb[12:16], uint32(len(w.attrs)))
	var total int
	for i, kv := range w.attrs {
		base := blockHeaderValidation + 6*i
		put.PutUint16(sb[base:base+2], uint16(len(kv[0])))
		put.PutUint32(sb[base+2:base+6], uint32(len(kv[1])))
		total += len(kv[0]) + len(kv[1])
	}
	if err := w.appendBlock(sb); err != nil { return 0, err }
	// ATTRIBUTES_BYTES (14-byte header: validation, recovery, u32 total)
	bytesOff := uint64(w.pos)
	bb := make([]byte, 14+total)
	put.PutUint64(bb[0:8], bytesOff)
	put.PutUint16(bb[8:10], recoverAttributesBytes)
	put.PutUint32(bb[10:14], uint32(total))
	p := 14
	for _, kv := range w.attrs {
		p += copy(bb[p:], kv[0])
		p += copy(bb[p:], kv[1])
	}
	if err := w.appendBlock(bb); err != nil { return 0, err }
	// ATTRIBUTES (29-byte header)
	attrOff := uint64(w.pos)
	hb := make([]byte, 29)
	put.PutUint64(hb[0:8], attrOff)
	put.PutUint16(hb[8:10], recoverAttributes)
	hb[10] = attrFormatFreeText
	put.PutUint16(hb[11:13], 0)
	put.PutUint64(hb[13:21], sizesOff)
	put.PutUint64(hb[21:29], bytesOff)
	if err := w.appendBlock(hb); err != nil { return 0, err }
	return attrOff, nil
}
```

> **Implementer:** confirm the ATTRIBUTES_BYTES 14-byte header field at @10 is the u32 total byte count by reading `readAttributesBytes` in opentile-go `formats/ife/metadata.go` (line ~349) before finalizing; adjust if it differs.

- [ ] **Step 9: Wire metadata assembly into `runConvertIFE`** — after creating the writer and before `Finalize`: `w.SetICCProfile(src.ICCProfile())`; for each `src.AssociatedImages()` (unless `--no-associated`), choose verbatim-JPEG (when the associated source encoding is JPEG, via opentile's associated-source bytes) else decode→PNG via `internal/codec/png`, then `w.AddAssociated(typeString, width, height, enc, blob)`; map a curated set of `src.Metadata().Properties` + `source-format`/`wsitools-version` into `w.SetAttributes(...)`. Mirror how `convert_factor.go`/`dicomwriter` pull associated bytes + ICC.

- [ ] **Step 10: Run + commit**

```bash
go test ./internal/ife/ -count=1
git add internal/ife/writer.go internal/ife/writer_test.go cmd/wsitools/convert_ife.go
git commit -m "feat(ife): ICC + associated images + attributes sub-blocks

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Verbatim tile-copy fast path + byte-identity (Slice 4)

**Files:** Modify `cmd/wsitools/convert_ife.go` (add eligibility + verbatim path); extend `tests/integration/ife_test.go`.

- [ ] **Step 1: Eligibility helper + dispatch** — in `runConvertIFE`, before the engine path, check verbatim eligibility: no `--factor`/`--target-mag`/`--rect`; `--codec` empty or equal to the source tile codec; source pyramid tile size == 256; source tile codec ∈ {jpeg, avif}. Implement `ifeVerbatimEligible(src, codecName, factor, targetMag, rectSet) bool`.

- [ ] **Step 2: Verbatim path** — when eligible: for each source level (native-first), set `w.AddLevel(ceil(levelW/256), ceil(levelH/256))`; iterate the level's tile grid and pull each tile's verbatim compressed bytes (opentile tile access — the same path cog-wsi uses for tile-copy; see `convert_cogwsi.go`/`AssociatedSourceOf`-equivalent for tiles), `w.WriteTile(level, col, row, verbatimBytes)`. Encoding byte from the source codec. Sparse tiles: opentile-sourced grids are dense, so all tiles present.

- [ ] **Step 3: Byte-identity integration test** — extend `tests/integration/ife_test.go`. Use a 256px-tiled JPEG SVS fixture (`svs/JP2K-33003-1.svs` is JPEG2000 — NOT eligible; use a 256px-JPEG source. If none in the pool, generate one: `convert --to tiff --codec jpeg` produces 256px JPEG tiles, then `--to ife` from THAT). Assert: (a) opentile re-opens the IFE; (b) for a sample of (level,col,row), the IFE tile bytes equal the source tile bytes read via opentile. Pseudocode:

```go
func TestConvertToIFE_VerbatimByteIdentical(t *testing.T) {
	// src256 = a 256px-JPEG-tiled WSI (generate via convert --to tiff --codec jpeg if needed)
	// convert --to ife -o out.iris src256
	// open both via opentile; for several (level,col,row): assert tileBytes(ife) == tileBytes(src256)
}
```

> **Implementer:** the exact opentile call to fetch a raw compressed tile is the same one the cog-wsi tile-copy uses — find it in `cmd/wsitools/convert_cogwsi.go` and reuse. If a 256px-JPEG fixture isn't available, build one in the test's TempDir with a first `convert --to tiff --codec jpeg` pass (256 is the default output tile size) and use it as the verbatim source.

- [ ] **Step 4: Run + commit**

```bash
WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestConvertToIFE -count=1
git add cmd/wsitools/convert_ife.go tests/integration/ife_test.go
git commit -m "feat(ife): verbatim tile-copy fast path (byte-identical for 256px jpeg/avif)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Iris-Codec validator gate (gold standard) + CI

The official `Iris-Codec` validator (`Codec.validate_slide_path`) is the IFE
equivalent of `dciodvfy`. It is **stricter** than opentile (it checks the
`recovery` magics on every block), so it is the gate that proves our files are
*spec-conformant*, not just opentile-readable. This task adds a Python validator
script, a `make ife-validate` target, and a CI step — mirroring the
`make dicom-validate` + dciodvfy pattern (format-debt D5).

**Files:** Create `scripts/ife_validate.py`; modify `Makefile`, `.github/workflows/ci.yml`.

- [ ] **Step 1: Validator script** — `scripts/ife_validate.py`:

```python
#!/usr/bin/env python3
"""Validate IFE files against the official IrisDigitalPathology Iris-Codec
implementation. Exits non-zero if any file fails validation."""
import sys
from iris_codec import Codec

def main(paths):
    rc = 0
    for p in paths:
        result = Codec.validate_slide_path(p)
        if result.success():
            print(f"OK    {p}")
        else:
            print(f"FAIL  {p}: {result.message()}")
            rc = 1
    return rc

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
```

> **Implementer:** confirm the import path — the PyPI package `Iris-Codec`
> imports as `iris_codec` (snake_case) or `Codec`; check `pip show -f Iris-Codec`
> / the README's `from iris_codec import Codec`. Adjust the import to match.

- [ ] **Step 2: `make ife-validate` target** — in `Makefile`, mirror
`dicom-validate`: build the binary, convert the SVS fixture to IFE in a temp dir,
run the validator, hard-fail on non-zero. Add `ife-validate` to `.PHONY`:

```makefile
ife-validate: build
	@if [ -z "$$WSI_TOOLS_TESTDIR" ]; then \
		echo "WSI_TOOLS_TESTDIR not set; skipping ife-validate"; exit 0; \
	fi
	@command -v "$(PYTHON_IFE)" >/dev/null 2>&1 || { echo "$(PYTHON_IFE) not found (pip install Iris-Codec)"; exit 1; }; \
	RC=0; DIR=$$(mktemp -d -t ife-val.XXXXXX); \
	SVS="$$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs"; \
	if [ -f "$$SVS" ]; then \
		./bin/wsitools convert --to ife -f -o "$$DIR/jpeg.iris" "$$SVS"; \
		./bin/wsitools convert --to ife --codec avif -f -o "$$DIR/avif.iris" "$$SVS"; \
		"$(PYTHON_IFE)" scripts/ife_validate.py "$$DIR"/*.iris || RC=$$?; \
	else echo "missing $$SVS; skipping"; fi; \
	rm -rf "$$DIR"; exit $$RC
```

with `PYTHON_IFE ?= python3` near the top of the Makefile (overridable like
`DCIODVFY`, e.g. a venv python).

- [ ] **Step 3: CI step** — in `.github/workflows/ci.yml` macOS job, after the
dciodvfy gate, add Iris-Codec install + validation (cache the pip install keyed
on a pinned version):

```yaml
      - name: Install Iris-Codec (IFE validator)
        run: |
          python3 -m pip install --quiet 'Iris-Codec==2025.3.1' openslide-bin
      - name: IFE conformance (Iris-Codec validator)
        env:
          WSI_TOOLS_TESTDIR: ${{ github.workspace }}/sample_files
        run: make ife-validate PYTHON_IFE=python3
```

> **Implementer:** pin the version (`2025.3.1` at writing) and bump deliberately,
> as with the dciodvfy snapshot. If `openslide-bin` is unneeded for validating
> our own `.iris` output (it is only a reader backend), drop it to slim the install.

- [ ] **Step 4: Controller-run gate** — `WSI_TOOLS_TESTDIR=$(pwd)/sample_files make ife-validate PYTHON_IFE=/path/to/venv/python` → every emitted `.iris` reports `OK`. (Set up a venv with `pip install Iris-Codec` first; matches how D7 used a venv.) **This is the gold-standard pass: it proves the recovery magics + every block layout are conformant, not merely opentile-readable.**

- [ ] **Step 5: Commit**

```bash
git add scripts/ife_validate.py Makefile .github/workflows/ci.yml
git commit -m "ci(ife): Iris-Codec validator gate (official IFE conformance)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Final integration sweep

- [ ] **Step 1:** `go test ./internal/ife/ -race -count=1` PASS.
- [ ] **Step 2:** `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run IFE -count=1` PASS.
- [ ] **Step 3:** `go test ./cmd/wsitools/ -run 'IFE|Capabilities|Convert' -count=1` PASS.
- [ ] **Step 4:** `gofmt -l internal/ife/ cmd/wsitools/convert_ife.go` clean; `go vet ./internal/ife/ ./cmd/wsitools/`.
- [ ] **Step 5:** `WSI_TOOLS_TESTDIR=$(pwd)/sample_files make ife-validate PYTHON_IFE=<venv-python>` → all `OK` (the official conformance gate).
- [ ] **Step 6:** `./bin/wsitools convert --to ife -o /tmp/smoke.iris sample_files/svs/CMU-1-Small-Region.svs && ./bin/wsitools info /tmp/smoke.iris` shows format ife, levels, MPP/mag. Clean up `/tmp/smoke.iris`.

---

## Self-review

**Spec coverage:** Writer core + bare pyramid (Tasks 1–2); engine path + capability gate (Task 3); ICC + IMAGE_ARRAY + ATTRIBUTES (Task 4); verbatim copy + byte-identity (Task 5); **official Iris-Codec validator gate + CI (Task 6)**; round-trip + dims (Task 3 test) + verbatim (Task 5 test) + synthetic (Task 2 test). The padding quirk is documented and asserted (Task 2/Task 3 tests assert 256-padded dims). The correct `RECOVERY` magics (FILE_HEADER 0x5501 / TILE_TABLE 0x5502 / LAYER_EXTENTS 0x5506 / TILE_OFFSETS 0x5507 / sub-blocks 0x5504–0x550C) are written on every block so the official validator passes. Boundaries (no ANNOTATIONS/cipher/v2.0/IRIS) are honored by writing NULL pointers + jpeg/avif only.

**Placeholder scan:** Task 3 `runConvertIFE` body and Task 5 verbatim path reference the engine/tile-copy patterns by exact file (`convert_factor.go` `downsampleToTIFF`, `convert_cogwsi.go`) rather than re-pasting large existing functions — the implementer copies the established pattern, substituting the sink + 256 tile size. The two "confirm/pin from the parser" notes (ATTRIBUTES_BYTES @10 field; the `Metadata().Properties` accessor) are explicit verification steps, not vague gaps.

**Type consistency:** `Writer` API — `Create(path, Options)`, `AddLevel(xTiles,yTiles)`, `WriteTile(apiLevel,col,row,blob)`, `SetICCProfile`, `AddAssociated(title,w,h,enc,blob)`, `SetAttributes([][2]string)`, `Finalize`, `Abort` — is used consistently across Tasks 2/4/5. `ifeSink.WriteTile` matches `retile.TileSink`. Encoding bytes (`encJPEG=2`/`encAVIF=3`, `imgEncPNG=1`/JPEG=2/AVIF=3) used consistently. `containerCapabilities("ife")` conformant set {jpeg,avif} matches the gate test.

## Boundaries

**In:** the four slices above + the Iris-Codec validator gate (Task 6). **Deferred (per spec):** ANNOTATIONS, cipher/encryption, IFE v2.0, reading IRIS-encoded IFE. (Cross-validation against the reference implementation is now **in scope** via the Iris-Codec validator, not deferred.)
