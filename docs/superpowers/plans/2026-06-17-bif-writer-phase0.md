# BIF Writer — Phase 0 Spike Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove wsitools can write a BIF that opentile-go reads back **pixel-identical**, with correct **serpentine** tile ordering — then emit a spec-shaped DP 200 artifact for the owner to load in openslide / Roche viewer / QuPath, settling the dialect question (feasibility spec §7).

**Architecture:** A new `internal/tiff/bifwriter` package built on the existing `internal/tiff` byte-emission core. The spike writes a BigTIFF with verbatim JPEG tiles copied from a source level, placed in BIF serpentine order. Two outputs: (1) a **minimal single-IFD BIF** (pyramid IFD carrying the `<iScan>` marker) that de-risks serpentine + offset patching against opentile's round-trip; (2) a **spec-shaped two-IFD BIF** (overview IFD 0 + pyramid IFD 1 with `<EncodeInfo Ver="2">`) for owner viewer-testing.

**Tech Stack:** Go, `internal/tiff` (BigTIFF `WriteHeader`/`EntryBuilder`/`Encode`, tag constants), `internal/source` (`Open`, `Level.TileInto` raw compressed tiles, `Level.DecodedTile` for pixel comparison), opentile-go v0.45.2 as the read oracle.

**Spec:** `docs/superpowers/specs/2026-06-17-bif-writer-feasibility.md` (this is Phase 0 of §6).

---

## Verified API facts (grounded — do not re-derive)

- **Serpentine (mirror exactly from `opentile-go@v0.45.2/formats/bif/serpentine.go`):**
  `imageToSerpentine(col,row,cols,rows)`: `stageRow = rows-1-row`; `stageCol = col`; if `stageRow%2==1` then `stageCol = cols-1-col`; return `stageRow*cols + stageCol`. Out-of-grid → -1. Inverse `serpentineToImage(idx,cols,rows)` divmods by `cols` and reverses the flips. Anchor values on a 24×21 grid (from opentile's `serpentine_test.go`): image(0,20)→0, image(23,20)→23, image(23,19)→24, image(0,0)→480.
- **`internal/tiff` core:**
  - `tiff.HeaderSize(bigtiff bool) int` (16 for BigTIFF), `tiff.WriteHeader(w io.WriterAt, bigtiff bool, firstIFDOffset uint64) error`.
  - `b := tiff.NewEntryBuilder(true)`; methods: `AddShort(tag uint16, []uint16)`, `AddLong(tag, []uint32)`, `AddLong8(tag, []uint64)`, `AddASCII(tag, string)`, `AddUndefined(tag, []byte)`, `AddBytes(tag, []byte)`, `AddTileOffsets(tag, []uint64) error` (emits LONG8 in BigTIFF). Entries auto-sort by tag.
  - `ifd, ext, err := b.Encode(ifdOffset uint64)` — `ifd` is the directory record (count + entries + a **zero next-IFD field as its last 8 bytes** in BigTIFF), `ext` is external value data that must be written immediately after `ifd` (external offsets are assigned starting at `ifdOffset + len(ifd)`). The writer patches the next-IFD field itself: overwrite `ifd[len(ifd)-8:]` with the next IFD's offset (or leave zero for the last IFD).
  - `tiff.IFDRecordSize(tagCount int, bigtiff bool) int` == `len(ifd)`.
  - Tag constants (`internal/tiff/tags.go`): `TagImageWidth`(256), `TagImageLength`(257), `TagBitsPerSample`(258), `TagCompression`(259), `TagPhotometricInterpretation`(262), `TagImageDescription`(270), `TagSamplesPerPixel`(277), `TagStripOffsets`(273), `TagRowsPerStrip`(278), `TagStripByteCounts`(279), `TagPlanarConfiguration`(284), `TagTileWidth`(322), `TagTileLength`(323), `TagTileOffsets`(324), `TagTileByteCounts`(325), `TagYCbCrSubSampling`(530). XMP is tag `700` (no constant — use the literal `uint16(700)`). Compression values: `CompressionNone`(1), `CompressionJPEG`(7).
- **`internal/source`:** `source.Open(path) (source.Source, error)`; `src.Levels() []source.Level`; `lvl.Size() image.Point`, `lvl.TileSize() image.Point`, `lvl.TileMaxSize() int`, `lvl.TileInto(x,y int, dst []byte) (int, error)` returns **raw compressed tile bytes**, `lvl.DecodedTile(x,y int) (*decoder.Image, error)` returns decoded pixels. Tile grid is `cols=ceil(W/tw)`, `rows=ceil(H/th)`.
- **Reader acceptance:** opentile detects BIF by the substring `<iScan` in any IFD's tag-700 bytes; classifies pyramid IFDs by `ImageDescription` starting `level=`; reads `<iScan>` from the first IFD whose XMP has the marker; reads `<EncodeInfo>` only from the level-0 pyramid IFD and **errors if its `Ver < 2`**; tolerates EncodeInfo being absent. `<iScan>` may be `<Metadata><iScan .../></Metadata>` or a bare `<iScan .../>`.

---

## Task 1: Serpentine remap

**Files:**
- Create: `internal/tiff/bifwriter/serpentine.go`
- Test: `internal/tiff/bifwriter/serpentine_test.go`

- [ ] **Step 1: Write the failing test** (anchors copied from opentile's own `serpentine_test.go` so we match the reader bit-for-bit)

```go
package bifwriter

import "testing"

func TestImageToSerpentineAnchors(t *testing.T) {
	// 24 cols x 21 rows, anchors from opentile-go formats/bif/serpentine_test.go.
	cases := []struct {
		col, row, want int
	}{
		{0, 20, 0},   // bottom-left image tile -> serpentine index 0
		{23, 20, 23}, // bottom-right -> 23 (stage row 0, L->R)
		{23, 19, 24}, // one up, right edge -> 24 (stage row 1, R->L starts at right)
		{0, 0, 480},  // top-left image tile -> last-ish
	}
	for _, c := range cases {
		if got := imageToSerpentine(c.col, c.row, 24, 21); got != c.want {
			t.Errorf("imageToSerpentine(%d,%d,24,21) = %d, want %d", c.col, c.row, got, c.want)
		}
	}
	if got := imageToSerpentine(24, 0, 24, 21); got != -1 {
		t.Errorf("out-of-grid col should be -1, got %d", got)
	}
}

func TestSerpentineRoundTrip(t *testing.T) {
	const cols, rows = 7, 5
	seen := make([]bool, cols*rows)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			idx := imageToSerpentine(col, row, cols, rows)
			if idx < 0 || idx >= cols*rows {
				t.Fatalf("idx out of range for (%d,%d): %d", col, row, idx)
			}
			if seen[idx] {
				t.Fatalf("idx %d produced twice (not a bijection)", idx)
			}
			seen[idx] = true
			gc, gr := serpentineToImage(idx, cols, rows)
			if gc != col || gr != row {
				t.Errorf("serpentineToImage(%d) = (%d,%d), want (%d,%d)", idx, gc, gr, col, row)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test, verify it fails to compile**

Run: `go test ./internal/tiff/bifwriter/ -run TestImageToSerpentine 2>&1 | head`
Expected: `undefined: imageToSerpentine`.

- [ ] **Step 3: Implement** `internal/tiff/bifwriter/serpentine.go` (mirror opentile's algorithm exactly)

```go
// Package bifwriter writes Ventana/Roche BIF (Biolmagene Image File) pyramids.
// Phase 0 (spike): verbatim-tile single-level + spec-shaped two-IFD output,
// verified by opentile-go round-trip. Tile ordering mirrors opentile-go's
// formats/bif/serpentine.go bit-for-bit (the read-side counterpart).
package bifwriter

// imageToSerpentine maps image-space (col,row) in a (cols,rows) tile grid to the
// index into BIF's TileOffsets array. Stage rows count up from the bottom; even
// stage rows go left-to-right, odd rows right-to-left; index 0 = bottom-left.
// Out-of-grid coordinates return -1.
func imageToSerpentine(col, row, cols, rows int) int {
	if col < 0 || row < 0 || col >= cols || row >= rows {
		return -1
	}
	stageRow := rows - 1 - row
	stageCol := col
	if stageRow%2 == 1 {
		stageCol = cols - 1 - col
	}
	return stageRow*cols + stageCol
}

// serpentineToImage is the inverse of imageToSerpentine. Out-of-range idx → (-1,-1).
func serpentineToImage(idx, cols, rows int) (col, row int) {
	if idx < 0 || idx >= cols*rows {
		return -1, -1
	}
	stageRow := idx / cols
	stageCol := idx % cols
	if stageRow%2 == 1 {
		stageCol = cols - 1 - stageCol
	}
	return stageCol, rows - 1 - stageRow
}
```

- [ ] **Step 4: Run the tests, verify they pass**

Run: `go test ./internal/tiff/bifwriter/ -run 'TestImageToSerpentine|TestSerpentineRoundTrip' -v 2>&1 | tail`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/bifwriter/serpentine.go internal/tiff/bifwriter/serpentine_test.go
git commit -m "feat(bifwriter): serpentine tile-order remap (mirrors opentile reader)"
```

---

## Task 2: Minimal iScan XMP synthesis

**Files:**
- Create: `internal/tiff/bifwriter/xml.go`
- Test: `internal/tiff/bifwriter/xml_test.go`

- [ ] **Step 1: Write the failing test**

```go
package bifwriter

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestIScanXMPWellFormedAndDetectable(t *testing.T) {
	blob := iScanXMP(IScanMeta{Magnification: 40, ScanRes: 0.25})
	// opentile detects BIF by this exact substring; it must be present.
	if !strings.Contains(string(blob), "<iScan") {
		t.Fatalf("iScan XMP missing the <iScan detection marker:\n%s", blob)
	}
	// Mandated constant the reader/spec requires (whitepaper Table 1b).
	if !strings.Contains(string(blob), `ScannerModel="VENTANA DP 200"`) {
		t.Errorf("missing ScannerModel=\"VENTANA DP 200\":\n%s", blob)
	}
	// Must be valid XML.
	var v any
	if err := xml.Unmarshal(blob, &v); err != nil {
		t.Errorf("iScan XMP is not well-formed XML: %v\n%s", err, blob)
	}
}
```

- [ ] **Step 2: Run, verify it fails to compile**

Run: `go test ./internal/tiff/bifwriter/ -run TestIScanXMP 2>&1 | head`
Expected: `undefined: iScanXMP` / `undefined: IScanMeta`.

- [ ] **Step 3: Implement** (append to `internal/tiff/bifwriter/xml.go`)

```go
package bifwriter

import "fmt"

// IScanMeta carries the minimal scanner metadata Phase 0 emits in the <iScan>
// block. Magnification and ScanRes drive the reader's MPP/magnification; the
// rest are spec-mandated constants/placeholders.
type IScanMeta struct {
	Magnification int     // 20 or 40
	ScanRes       float64 // microns/pixel at level 0 (0.465 @20x, 0.25 @40x)
}

// iScanXMP builds the IFD-0 <iScan> XMP payload (tag 700). Wrapped in
// <Metadata> per the DP 200 (spec-compliant) layout. ScannerModel is the
// mandated literal "VENTANA DP 200"; UnitNumber is a synthetic >=2,000,000
// placeholder; Z-layers=1 (single focal plane).
func iScanXMP(m IScanMeta) []byte {
	return []byte(fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>`+
			`<Metadata><iScan Mode="brightfield" Magnification="%d" ScanRes="%g" `+
			`UnitNumber="2000515" ScannerModel="VENTANA DP 200" Z-layers="1" `+
			`Z-spacing="0" UserName="wsitools" BuildVersion="0.0.0.0" `+
			`BuildDate="1/1/2020 0:0:0 AM" ScanWhitePoint="255" Anonymization="1"/>`+
			`</Metadata>`,
		m.Magnification, m.ScanRes))
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/tiff/bifwriter/ -run TestIScanXMP -v 2>&1 | tail`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/bifwriter/xml.go internal/tiff/bifwriter/xml_test.go
git commit -m "feat(bifwriter): minimal <iScan> XMP synthesis (DP 200 marker + mandated constants)"
```

---

## Task 3: Single-IFD BIF writer core

Write one pyramid IFD carrying verbatim source tiles in serpentine order, with the `<iScan>` XMP on that same IFD (so a single IFD both passes detection and is the pyramid). This is the minimal file opentile will open.

**Files:**
- Create: `internal/tiff/bifwriter/writer.go`
- Test: `internal/tiff/bifwriter/writer_test.go` (compile-only check here; the round-trip oracle is Task 4)

- [ ] **Step 1: Implement** `internal/tiff/bifwriter/writer.go`

```go
package bifwriter

import (
	"fmt"
	"io"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
)

// TileSource is the subset of source.Level the writer needs (verbatim
// compressed tiles + geometry). source.Level satisfies it.
type TileSource interface {
	SizeW() int
	SizeH() int
	TileW() int
	TileH() int
	TileMaxSize() int
	TileInto(x, y int, dst []byte) (int, error)
}

// levelAdapter adapts a source.Level to TileSource.
type levelAdapter struct{ l source.Level }

func (a levelAdapter) SizeW() int        { return a.l.Size().X }
func (a levelAdapter) SizeH() int        { return a.l.Size().Y }
func (a levelAdapter) TileW() int        { return a.l.TileSize().X }
func (a levelAdapter) TileH() int        { return a.l.TileSize().Y }
func (a levelAdapter) TileMaxSize() int  { return a.l.TileMaxSize() }
func (a levelAdapter) TileInto(x, y int, dst []byte) (int, error) {
	return a.l.TileInto(x, y, dst)
}

// FromLevel wraps a source.Level as a TileSource.
func FromLevel(l source.Level) TileSource { return levelAdapter{l} }

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// WriteSingleLevel writes a minimal one-IFD BIF: a tiled JPEG pyramid level
// (ImageDescription "level=0 ...") whose tiles are copied verbatim from src and
// stored in BIF serpentine order, carrying the <iScan> marker XMP. This is the
// spike's de-risk artifact — opentile must read it back pixel-identical.
func WriteSingleLevel(w io.WriterAt, src TileSource, meta IScanMeta) error {
	cols := ceilDiv(src.SizeW(), src.TileW())
	rows := ceilDiv(src.SizeH(), src.TileH())
	n := cols * rows

	// 1. Read every tile's compressed bytes, keyed by serpentine index.
	tileBytes := make([][]byte, n)
	buf := make([]byte, src.TileMaxSize())
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			nb, err := src.TileInto(col, row, buf)
			if err != nil {
				return fmt.Errorf("bifwriter: read tile (%d,%d): %w", col, row, err)
			}
			idx := imageToSerpentine(col, row, cols, rows)
			b := make([]byte, nb)
			copy(b, buf[:nb])
			tileBytes[idx] = b
		}
	}

	xmp := iScanXMP(meta)

	// 2. Build the IFD twice: first with placeholder offsets to learn the
	//    record+external size, then again with the real tile offsets. The
	//    external SIZE is identical between passes (same array lengths / string
	//    lengths), so tileDataStart computed from pass 1 is correct for pass 2.
	const ifdOffset = uint64(16) // BigTIFF header is 16 bytes; IFD 0 follows.
	placeholder := make([]uint64, n)
	zeroCounts := make([]uint64, n)
	ifd0, ext0, err := buildLevelIFD(src, cols, rows, placeholder, zeroCounts, xmp)
	if err != nil {
		return err
	}
	tileDataStart := ifdOffset + uint64(len(ifd0)) + uint64(len(ext0))

	offsets := make([]uint64, n)
	counts := make([]uint64, n)
	cursor := tileDataStart
	for i := 0; i < n; i++ {
		offsets[i] = cursor
		counts[i] = uint64(len(tileBytes[i]))
		cursor += uint64(len(tileBytes[i]))
	}

	ifd, ext, err := buildLevelIFD(src, cols, rows, offsets, counts, xmp)
	if err != nil {
		return err
	}
	if len(ifd) != len(ifd0) || len(ext) != len(ext0) {
		return fmt.Errorf("bifwriter: IFD size unstable between passes (%d/%d vs %d/%d)",
			len(ifd0), len(ext0), len(ifd), len(ext))
	}
	// Single IFD: next-IFD pointer stays zero (Encode already left it zero).

	// 3. Write header, IFD, external data, then tile bodies.
	if err := tiff.WriteHeader(w, true, ifdOffset); err != nil {
		return err
	}
	if _, err := w.WriteAt(ifd, int64(ifdOffset)); err != nil {
		return err
	}
	if _, err := w.WriteAt(ext, int64(ifdOffset)+int64(len(ifd))); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if _, err := w.WriteAt(tileBytes[i], int64(offsets[i])); err != nil {
			return fmt.Errorf("bifwriter: write tile %d: %w", i, err)
		}
	}
	return nil
}

// buildLevelIFD assembles the pyramid-level IFD (tiled JPEG/YCbCr) with the
// supplied serpentine-ordered tile offsets/counts and the iScan XMP.
func buildLevelIFD(src TileSource, cols, rows int, offsets, counts []uint64, xmp []byte) (ifd, ext []byte, err error) {
	b := tiff.NewEntryBuilder(true)
	b.AddLong(tiff.TagImageWidth, []uint32{uint32(src.SizeW())})
	b.AddLong(tiff.TagImageLength, []uint32{uint32(src.SizeH())})
	b.AddShort(tiff.TagBitsPerSample, []uint16{8, 8, 8})
	b.AddShort(tiff.TagCompression, []uint16{tiff.CompressionJPEG})
	b.AddShort(tiff.TagPhotometricInterpretation, []uint16{6}) // YCbCr
	b.AddASCII(tiff.TagImageDescription,
		fmt.Sprintf("level=0 mag=%g quality=90", magFor(src)))
	b.AddShort(tiff.TagSamplesPerPixel, []uint16{3})
	b.AddShort(tiff.TagPlanarConfiguration, []uint16{1})
	b.AddShort(tiff.TagTileWidth, []uint16{uint16(src.TileW())})
	b.AddShort(tiff.TagTileLength, []uint16{uint16(src.TileH())})
	if err := b.AddTileOffsets(tiff.TagTileOffsets, offsets); err != nil {
		return nil, nil, err
	}
	if err := b.AddTileOffsets(tiff.TagTileByteCounts, counts); err != nil {
		return nil, nil, err
	}
	b.AddShort(tiff.TagYCbCrSubSampling, []uint16{2, 2})
	b.AddUndefined(uint16(700), xmp) // XMP
	return b.Encode(16)
}

// magFor is a placeholder magnification for the single emitted level. Phase 0
// does not thread real magnification; opentile derives MPP from <iScan>/ScanRes,
// not from this token, so any positive value round-trips.
func magFor(src TileSource) float64 { return 40 }
```

- [ ] **Step 2: Add a compile/smoke test** `internal/tiff/bifwriter/writer_test.go`

```go
package bifwriter

import (
	"bytes"
	"io"
	"testing"
)

// fakeLevel is a tiny in-memory TileSource: a 2x2 tile grid of distinct
// 1-byte "tiles" (not real JPEG — this test only checks structural assembly
// and serpentine placement, not decodability; Task 4 does the real round-trip).
type fakeLevel struct{}

func (fakeLevel) SizeW() int       { return 3 }
func (fakeLevel) SizeH() int       { return 3 }
func (fakeLevel) TileW() int       { return 2 }
func (fakeLevel) TileH() int       { return 2 }
func (fakeLevel) TileMaxSize() int { return 1 }
func (fakeLevel) TileInto(x, y int, dst []byte) (int, error) {
	dst[0] = byte(10*y + x) // encodes (col,row) so we can locate it in the file
	return 1, nil
}

// writerAt adapts a bytes.Buffer-like backing for io.WriterAt.
type bufAt struct{ b []byte }

func (w *bufAt) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(w.b) {
		w.b = append(w.b, make([]byte, end-len(w.b))...)
	}
	copy(w.b[off:], p)
	return len(p), nil
}

func TestWriteSingleLevelAssembles(t *testing.T) {
	var w bufAt
	if err := WriteSingleLevel(&w, fakeLevel{}, IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
		t.Fatalf("WriteSingleLevel: %v", err)
	}
	// BigTIFF magic (II, 0x2B).
	if !bytes.HasPrefix(w.b, []byte{0x49, 0x49, 0x2B, 0x00}) {
		t.Errorf("output is not little-endian BigTIFF: % x", w.b[:8])
	}
	// The <iScan marker must be in the bytes (detection).
	if !bytes.Contains(w.b, []byte("<iScan")) {
		t.Errorf("output missing <iScan marker")
	}
	_ = io.Discard
}
```

- [ ] **Step 3: Run**

Run: `go test ./internal/tiff/bifwriter/ -run 'TestWriteSingleLevelAssembles' -v 2>&1 | tail`
Expected: PASS. (If `magFor`'s `math` import trips `unused`, drop the `math` import and the `_ = math.Floor` line — keep `magFor` returning `40`.)

- [ ] **Step 4: Commit**

```bash
git add internal/tiff/bifwriter/writer.go internal/tiff/bifwriter/writer_test.go
git commit -m "feat(bifwriter): single-IFD BIF writer (verbatim tiles, serpentine order)"
```

---

## Task 4: opentile round-trip oracle (the de-risk gate)

Write a real BIF from a real source level and prove opentile reads it back pixel-identical. This is the task that validates serpentine + offset patching. **Controller runs this** (needs `WSI_TOOLS_TESTDIR` fixtures + opentile).

**Files:**
- Test: `internal/tiff/bifwriter/roundtrip_test.go`

- [ ] **Step 1: Write the round-trip test**

```go
package bifwriter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("WSI_TOOLS_TESTDIR")
	if d == "" {
		d = "../../../sample_files"
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("fixtures unavailable (%s): %v", d, err)
	}
	return d
}

// TestRoundTripPixelIdentical: write a BIF from a small SVS level, reopen it via
// opentile (the BIF reader), and assert every tile decodes to the same pixels as
// the source level. A wrong serpentine mapping scrambles tiles and fails here.
func TestRoundTripPixelIdentical(t *testing.T) {
	src, err := source.Open(filepath.Join(fixtureDir(t), "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Skipf("open source: %v", err)
	}
	defer src.Close()
	lvl := src.Levels()[0]

	out := filepath.Join(t.TempDir(), "spike.bif")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSingleLevel(f, FromLevel(lvl), IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
		f.Close()
		t.Fatalf("WriteSingleLevel: %v", err)
	}
	f.Close()

	// Reopen through wsitools' source layer (opentile under the hood).
	got, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen written BIF: %v", err)
	}
	defer got.Close()
	if got.Format() != "bif" {
		t.Fatalf("written file detected as %q, want bif", got.Format())
	}
	gl := got.Levels()[0]
	if gl.Size() != lvl.Size() {
		t.Fatalf("level size %v != source %v", gl.Size(), lvl.Size())
	}

	cols := ceilDiv(lvl.Size().X, lvl.TileSize().X)
	rows := ceilDiv(lvl.Size().Y, lvl.TileSize().Y)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			want, err := lvl.DecodedTile(col, row)
			if err != nil {
				t.Fatalf("source DecodedTile(%d,%d): %v", col, row, err)
			}
			have, err := gl.DecodedTile(col, row)
			if err != nil {
				t.Fatalf("bif DecodedTile(%d,%d): %v", col, row, err)
			}
			if len(want.Pix) != len(have.Pix) {
				t.Fatalf("tile (%d,%d) pix len %d != %d", col, row, len(have.Pix), len(want.Pix))
			}
			for i := range want.Pix {
				if want.Pix[i] != have.Pix[i] {
					t.Fatalf("tile (%d,%d) pixel %d differs: src=%d bif=%d (serpentine mismatch?)",
						col, row, i, want.Pix[i], have.Pix[i])
				}
			}
		}
	}
}
```

- [ ] **Step 2: Build + run (controller)**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/tiff/bifwriter/ -run TestRoundTripPixelIdentical -v 2>&1 | grep -v "duplicate librar" | tail -20`
Expected: PASS. If it fails with a pixel mismatch, the serpentine mapping or offset patching is wrong — debug against `opentile-go/formats/bif/serpentine.go` (the mapping must match exactly). If it fails to open/detect, check the `<iScan` marker reached tag 700 and the `level=` ImageDescription is present.

- [ ] **Step 3: Commit**

```bash
git add internal/tiff/bifwriter/roundtrip_test.go
git commit -m "test(bifwriter): opentile round-trip pixel-identity gate (validates serpentine)"
```

---

## Task 5: Spec-shaped artifact (overview IFD 0 + EncodeInfo) for viewer testing

Extend the writer to emit the **two-IFD DP 200 shape** — IFD 0 overview (`Label_Image`, `<iScan>` XMP, a small uncompressed-RGB striped placeholder) + IFD 1 pyramid (`level=0`, verbatim tiles, `<EncodeInfo Ver="2">`) — and a hook that writes one to a known path for the owner to open in openslide / Roche viewer / QuPath.

**Files:**
- Modify: `internal/tiff/bifwriter/xml.go` (add `encodeInfoXMP`)
- Modify: `internal/tiff/bifwriter/writer.go` (add `WriteSpecShaped`)
- Test: `internal/tiff/bifwriter/specshaped_test.go`

- [ ] **Step 1: Add EncodeInfo synthesis** (append to `xml.go`)

```go
// encodeInfoXMP builds the minimal level-0 <EncodeInfo Ver="2"> for a single
// AOI with no tile overlap: SlideInfo/AoiInfo with the tile grid, a
// SlideStitchInfo/ImageInfo, FrameInfo with one <Frame> per tile in serpentine
// (TILE_OFFSETS) order, and AoiOrigin (0,0). TileJointInfo overlaps are all 0
// (abutting tiles). Reader requires Ver>=2.
func encodeInfoXMP(cols, rows, tileW, tileH int) []byte {
	var frames []byte
	for idx := 0; idx < cols*rows; idx++ {
		col, row := serpentineToImage(idx, cols, rows)
		frames = append(frames, []byte(fmt.Sprintf(
			`<Frame XY="%d,%d" Z="0" Focus="0"/>`, col, row))...)
	}
	return []byte(fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>`+
			`<EncodeInfo Ver="2">`+
			`<SlideInfo Rack="0" Slot="0" BaseName="wsitools">`+
			`<AoiInfo XIMAGESIZE="%d" YIMAGESIZE="%d" NumRows="%d" NumCols="%d" Pos-X="0" Pos-Y="0"/>`+
			`</SlideInfo>`+
			`<SlideStitchInfo>`+
			`<ImageInfo AOIScanned="1" AOIIndex="0" NumRows="%d" NumCols="%d" Width="%d" Height="%d" Pos-X="0" Pos-Y="0">`+
			`<FrameInfo AOIScanned="1" AOIIndex="0">%s</FrameInfo>`+
			`</ImageInfo>`+
			`</SlideStitchInfo>`+
			`<AoiOrigin><AOI0 OriginX="0" OriginY="0"/></AoiOrigin>`+
			`</EncodeInfo>`,
		tileW, tileH, rows, cols, rows, cols, tileW, tileH, frames))
}
```

- [ ] **Step 2: Add `WriteSpecShaped`** (append to `writer.go`)

This reuses the single-IFD machinery but emits two IFDs: a tiny overview (uncompressed RGB strip, a solid mid-gray placeholder sized to a 1:3 slide aspect of the level) carrying the iScan XMP, then the pyramid IFD carrying the EncodeInfo XMP, chained. Write the overview pixels + both IFDs + tile data, patching IFD 0's next-IFD pointer to IFD 1's offset.

```go
// WriteSpecShaped writes the two-IFD DP 200 shape for owner viewer-testing:
// IFD 0 = overview (Label_Image, iScan XMP, small uncompressed-RGB strip
// placeholder); IFD 1 = pyramid level (level=0, verbatim tiles in serpentine
// order, EncodeInfo XMP). NOTE: the overview is a synthetic gray placeholder in
// Phase 0 — real overview/probability generation is Phase 1+.
func WriteSpecShaped(w io.WriterAt, src TileSource, meta IScanMeta) error {
	cols := ceilDiv(src.SizeW(), src.TileW())
	rows := ceilDiv(src.SizeH(), src.TileH())
	n := cols * rows

	// --- read pyramid tiles (serpentine-keyed) ---
	tileBytes := make([][]byte, n)
	buf := make([]byte, src.TileMaxSize())
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			nb, err := src.TileInto(col, row, buf)
			if err != nil {
				return fmt.Errorf("bifwriter: read tile (%d,%d): %w", col, row, err)
			}
			idx := imageToSerpentine(col, row, cols, rows)
			b := make([]byte, nb)
			copy(b, buf[:nb])
			tileBytes[idx] = b
		}
	}

	// --- overview placeholder: ovW x ovH solid gray RGB, single strip ---
	const ovW, ovH = 128, 384 // 1:3 slide-ish aspect; content irrelevant to the spike
	ovPix := make([]byte, ovW*ovH*3)
	for i := range ovPix {
		ovPix[i] = 0xC0
	}

	iscan := iScanXMP(meta)
	encinfo := encodeInfoXMP(cols, rows, src.TileW(), src.TileH())

	// --- layout: header | IFD0 | ext0 | IFD1 | ext1 | ovPix | tiles ---
	// Two passes for stable sizes, same trick as WriteSingleLevel.
	const hdr = uint64(16)
	plN := make([]uint64, n)
	ifd0a, ext0a, err := buildOverviewIFD(ovW, ovH, 0, iscan) // placeholder strip offset
	if err != nil {
		return err
	}
	ifd1Off := hdr + uint64(len(ifd0a)) + uint64(len(ext0a))
	ifd1a, ext1a, err := buildLevelIFDAt(ifd1Off, src, cols, rows, plN, plN, encinfo)
	if err != nil {
		return err
	}
	ovOff := ifd1Off + uint64(len(ifd1a)) + uint64(len(ext1a))
	tilesStart := ovOff + uint64(len(ovPix))

	offsets := make([]uint64, n)
	counts := make([]uint64, n)
	cur := tilesStart
	for i := 0; i < n; i++ {
		offsets[i] = cur
		counts[i] = uint64(len(tileBytes[i]))
		cur += uint64(len(tileBytes[i]))
	}

	ifd0, ext0, err := buildOverviewIFD(ovW, ovH, ovOff, iscan)
	if err != nil {
		return err
	}
	ifd1, ext1, err := buildLevelIFDAt(ifd1Off, src, cols, rows, offsets, counts, encinfo)
	if err != nil {
		return err
	}
	if len(ifd0) != len(ifd0a) || len(ext0) != len(ext0a) || len(ifd1) != len(ifd1a) || len(ext1) != len(ext1a) {
		return fmt.Errorf("bifwriter: IFD size unstable between passes")
	}
	// Patch IFD 0's next-IFD pointer (last 8 BigTIFF bytes) to IFD 1's offset.
	setNextIFD(ifd0, ifd1Off)

	if err := tiff.WriteHeader(w, true, hdr); err != nil {
		return err
	}
	writes := []struct {
		off uint64
		b   []byte
	}{
		{hdr, ifd0}, {hdr + uint64(len(ifd0)), ext0},
		{ifd1Off, ifd1}, {ifd1Off + uint64(len(ifd1)), ext1},
		{ovOff, ovPix},
	}
	for _, wr := range writes {
		if _, err := w.WriteAt(wr.b, int64(wr.off)); err != nil {
			return err
		}
	}
	for i := 0; i < n; i++ {
		if _, err := w.WriteAt(tileBytes[i], int64(offsets[i])); err != nil {
			return err
		}
	}
	return nil
}

// setNextIFD overwrites the trailing 8-byte (BigTIFF) next-IFD pointer of an
// encoded IFD record.
func setNextIFD(ifd []byte, next uint64) {
	tiff.PutNextIFD(ifd, next) // see Step 3 — add this helper to internal/tiff
}

// buildLevelIFDAt is buildLevelIFD with an explicit ifd offset (for IFD 1).
func buildLevelIFDAt(off uint64, src TileSource, cols, rows int, offsets, counts []uint64, xmp []byte) (ifd, ext []byte, err error) {
	b := tiff.NewEntryBuilder(true)
	b.AddLong(tiff.TagImageWidth, []uint32{uint32(src.SizeW())})
	b.AddLong(tiff.TagImageLength, []uint32{uint32(src.SizeH())})
	b.AddShort(tiff.TagBitsPerSample, []uint16{8, 8, 8})
	b.AddShort(tiff.TagCompression, []uint16{tiff.CompressionJPEG})
	b.AddShort(tiff.TagPhotometricInterpretation, []uint16{6})
	b.AddASCII(tiff.TagImageDescription, fmt.Sprintf("level=0 mag=%g quality=90", magFor(src)))
	b.AddShort(tiff.TagSamplesPerPixel, []uint16{3})
	b.AddShort(tiff.TagPlanarConfiguration, []uint16{1})
	b.AddShort(tiff.TagTileWidth, []uint16{uint16(src.TileW())})
	b.AddShort(tiff.TagTileLength, []uint16{uint16(src.TileH())})
	if err := b.AddTileOffsets(tiff.TagTileOffsets, offsets); err != nil {
		return nil, nil, err
	}
	if err := b.AddTileOffsets(tiff.TagTileByteCounts, counts); err != nil {
		return nil, nil, err
	}
	b.AddShort(tiff.TagYCbCrSubSampling, []uint16{2, 2})
	b.AddUndefined(uint16(700), xmp)
	return b.Encode(off)
}

// buildOverviewIFD builds the IFD-0 overview: a wxh uncompressed RGB single
// strip at stripOff, ImageDescription "Label_Image", iScan XMP.
func buildOverviewIFD(w, h int, stripOff uint64, xmp []byte) (ifd, ext []byte, err error) {
	b := tiff.NewEntryBuilder(true)
	b.AddLong(tiff.TagImageWidth, []uint32{uint32(w)})
	b.AddLong(tiff.TagImageLength, []uint32{uint32(h)})
	b.AddShort(tiff.TagBitsPerSample, []uint16{8, 8, 8})
	b.AddShort(tiff.TagCompression, []uint16{tiff.CompressionNone})
	b.AddShort(tiff.TagPhotometricInterpretation, []uint16{2}) // RGB
	b.AddASCII(tiff.TagImageDescription, "Label_Image")
	b.AddLong8(tiff.TagStripOffsets, []uint64{stripOff})
	b.AddShort(tiff.TagSamplesPerPixel, []uint16{3})
	b.AddLong(tiff.TagRowsPerStrip, []uint32{uint32(h)})
	b.AddLong8(tiff.TagStripByteCounts, []uint64{uint64(w * h * 3)})
	b.AddShort(tiff.TagPlanarConfiguration, []uint16{1})
	b.AddUndefined(uint16(700), xmp)
	return b.Encode(16) // ifd 0 starts right after the 16-byte header
}
```

- [ ] **Step 3: Add the `PutNextIFD` helper to `internal/tiff`**

The next-IFD pointer is the trailing 8 bytes (BigTIFF) / 4 bytes (classic) of an encoded IFD record. Add to `internal/tiff/entry.go`:

```go
// PutNextIFD overwrites the trailing next-IFD pointer of an IFD record produced
// by EntryBuilder.Encode. Pass the same bigtiff flag used to build it.
func PutNextIFD(ifd []byte, next uint64) {
	// BigTIFF records end with an 8-byte pointer; classic with 4. We detect by
	// length parity is unreliable, so writers that need classic should pass a
	// 4-byte variant. Phase 0 is BigTIFF-only:
	binary.LittleEndian.PutUint64(ifd[len(ifd)-8:], next)
}
```

(If `internal/tiff` already exposes an equivalent next-IFD patch helper, use that instead and delete this; check `grep -rn "NextIFD\|next-IFD\|PatchUint64" internal/tiff/`.)

- [ ] **Step 4: Test it opens + emit the owner artifact** `internal/tiff/bifwriter/specshaped_test.go`

```go
package bifwriter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func TestSpecShapedOpensInOpentile(t *testing.T) {
	src, err := source.Open(filepath.Join(fixtureDir(t), "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Skipf("open source: %v", err)
	}
	defer src.Close()

	// If BIF_SPIKE_OUT is set, write there for manual viewer testing; else tmp.
	out := os.Getenv("BIF_SPIKE_OUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "specshaped.bif")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSpecShaped(f, FromLevel(src.Levels()[0]), IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
		f.Close()
		t.Fatalf("WriteSpecShaped: %v", err)
	}
	f.Close()
	t.Logf("wrote spec-shaped BIF to %s", out)

	got, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen spec-shaped BIF: %v", err)
	}
	defer got.Close()
	if got.Format() != "bif" {
		t.Fatalf("detected %q, want bif", got.Format())
	}
}
```

- [ ] **Step 5: Build, run, and emit the artifact (controller)**

```bash
go build ./... 2>&1 | grep -v "duplicate librar" | grep -v "^# "
WSI_TOOLS_TESTDIR=$(pwd)/sample_files BIF_SPIKE_OUT=/tmp/wsitools-spike.bif \
  go test ./internal/tiff/bifwriter/ -run 'TestSpecShaped|TestRoundTrip' -v 2>&1 | grep -v "duplicate librar" | tail -20
```
Expected: both PASS; `/tmp/wsitools-spike.bif` exists.

- [ ] **Step 6: Owner viewer checks (manual — record results in the commit body / spec)**

Open `/tmp/wsitools-spike.bif` in:
- **openslide** (`openslide-show-properties /tmp/wsitools-spike.bif`, or Python `openslide.OpenSlide`) — does it open? render?
- **Roche viewer** — does it open and render correctly?
- **QuPath** — does it open (via bio-formats/openslide)?

Record which accept/reject and any error messages. This is the dialect-question data point that gates Phase 1+ (feasibility spec §7).

- [ ] **Step 7: Commit**

```bash
git add internal/tiff/bifwriter/xml.go internal/tiff/bifwriter/writer.go internal/tiff/bifwriter/specshaped_test.go internal/tiff/entry.go
git commit -m "feat(bifwriter): spec-shaped two-IFD DP 200 output (overview + EncodeInfo) for viewer testing"
```

---

## Final verification

- [ ] **Step 1: Full package suite**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/tiff/bifwriter/ -v 2>&1 | grep -v "duplicate librar" | tail -25`
Expected: all PASS (round-trip + spec-shaped skip only if fixtures absent).

- [ ] **Step 2: Build + vet**

Run: `go build ./... 2>&1 | grep -v "duplicate librar" | grep -v "^# " ; go vet ./internal/tiff/bifwriter/ 2>&1 | grep -v "duplicate librar"`
Expected: clean.

- [ ] **Step 3: Record the Phase 0 outcome**

Update `docs/superpowers/specs/2026-06-17-bif-writer-feasibility.md` §7 with the viewer results (openslide/Roche/QuPath accept-or-reject + errors), which determines the Phase 1 dialect decision.

---

## Notes for the implementer

- **The round-trip test (Task 4) is the real gate.** Everything else is plumbing; if tiles come back scrambled, the serpentine mapping or an offset is wrong. Debug against `opentile-go@v0.45.2/formats/bif/serpentine.go` — our `imageToSerpentine` must equal theirs exactly.
- **Two-pass writing** (build IFD with placeholder offsets to size it, then with real offsets) is load-bearing — `AddTileOffsets` needs the offset values, which depend on where tile data starts, which depends on the IFD+ext size. The size is identical between passes because only values change, not array lengths.
- **Don't widen scope.** No `convert --to bif` CLI, no crop/downsample, no probability IFD, no ICC, no real overview — those are Phases 1–3. Phase 0 proves serpentine + produces one viewer-testable artifact.
- **If `internal/tiff` already has a next-IFD patch helper**, use it instead of adding `PutNextIFD` (Task 5 Step 3 says to check first).
- All output is **BigTIFF little-endian**; keep source files UTF-8 (the spec/whitepaper uses `×`/`·` elsewhere but this code does not).
