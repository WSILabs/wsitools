# TIFF Core Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract TIFF byte-emission primitives out of `internal/wsiwriter` and `internal/cogwsi` into a new shared `internal/tiff` package, and replace both writer packages with new ones (`internal/tiff/streamwriter`, `internal/tiff/cogwsiwriter`) that consume the core. No user-visible behavior changes.

**Architecture:** Three sequential landings. Landing 1 adds `internal/tiff` as a pure addition (no consumer). Landing 2 ports `internal/cogwsi` → `internal/tiff/cogwsiwriter` on top of the core. Landing 3 replaces `internal/wsiwriter` with `internal/tiff/streamwriter`, moves Aperio SVS-shape tag emission to caller-side helpers, and rewrites `transcode.go` + `downsample.go`. Golden-master hash fixtures pre-/post-landing-3 verify byte-identical outputs.

**Tech Stack:** Go 1.22+, cobra (CLI), `github.com/cornish/opentile-go` (reader, via `internal/source`).

**Reference docs (read before starting):**
- `docs/superpowers/specs/2026-05-21-tiff-core-extraction-design.md` — the design spec this plan implements.
- `internal/wsiwriter/tiff.go` — current streaming writer (902 lines) being replaced.
- `internal/cogwsi/ifd.go` — the `ifdBuilder` that's getting promoted to `internal/tiff/entry.go`.
- `internal/wsiwriter/jpegtables.go` — moves to `internal/tiff/jpegtables.go`.

**Pre-flight check:** confirm `git status` is clean and you're on `main` before starting any landing.

---

## File Structure

**New files (landing 1):**

- `internal/tiff/doc.go`
- `internal/tiff/types.go` + `types_test.go`
- `internal/tiff/tags.go` + `tags_test.go`
- `internal/tiff/wsitags.go` + `wsitags_test.go`
- `internal/tiff/header.go` + `header_test.go`
- `internal/tiff/entry.go` + `entry_test.go` (extracted from `cogwsi/ifd.go`)
- `internal/tiff/jpegtables.go` + `jpegtables_test.go` (copied from `wsiwriter/jpegtables.go`)
- `internal/tiff/bigtiff.go` + `bigtiff_test.go`
- `internal/tiff/patch.go` + `patch_test.go`

**Moved/restructured (landing 2):**

- `internal/cogwsi/*` → `internal/tiff/cogwsiwriter/*` (via `git mv`)
- `internal/tiff/cogwsiwriter/ifd.go` — deleted (content now in core)
- `internal/tiff/cogwsiwriter/tags.go` — split (constants go to core; cogwsi-specific helpers stay in new `validate.go`)
- `internal/tiff/cogwsiwriter/validate.go` (new, holds `validAssocKinds` + `ErrInvalidAssocKind`)
- `cmd/wsitools/convert.go` — modified (import path + `cogwsi.X` → `cogwsiwriter.X` renames)

**New + moved (landing 3):**

- `internal/tiff/streamwriter/doc.go`
- `internal/tiff/streamwriter/options.go`
- `internal/tiff/streamwriter/writer.go` + `writer_test.go`
- `internal/tiff/streamwriter/levelhandle.go`
- `internal/tiff/streamwriter/stripped.go`
- `internal/tiff/streamwriter/golden_test.go` (ported from `wsiwriter/svs_roundtrip_test.go` + `tiff_test.go`)
- `cmd/wsitools/svs_tags.go` (new helper for Aperio-shape `ExtraTags`)
- `cmd/wsitools/transcode.go` — modified
- `cmd/wsitools/downsample.go` — modified
- `internal/wsiwriter/` — deleted entirely after landing 3 acceptance.
- `docs/superpowers/golden-masters-v0.6.0-transcode.txt` — committed before landing 3.

---

# LANDING 1 — Add `internal/tiff` core

Pure addition. No consumer. After this landing, `internal/wsiwriter` and `internal/cogwsi` are untouched and the v0.6.0 binary behavior is unchanged. The new `internal/tiff` package builds and its tests pass.

## Task 1.1: Scaffold `internal/tiff` package with TIFF type constants

**Files:**
- Create: `internal/tiff/doc.go`
- Create: `internal/tiff/types.go`
- Create: `internal/tiff/types_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tiff/types_test.go`:

```go
package tiff

import "testing"

func TestTIFFTypeConstants(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"BYTE", TypeBYTE, 1},
		{"ASCII", TypeASCII, 2},
		{"SHORT", TypeSHORT, 3},
		{"LONG", TypeLONG, 4},
		{"RATIONAL", TypeRATIONAL, 5},
		{"DOUBLE", TypeDOUBLE, 12},
		{"LONG8", TypeLONG8, 16},
		{"IFD8", TypeIFD8, 18},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}

func TestTypeByteSize(t *testing.T) {
	cases := []struct {
		t    uint16
		want int
	}{
		{TypeBYTE, 1},
		{TypeASCII, 1},
		{TypeSHORT, 2},
		{TypeLONG, 4},
		{TypeRATIONAL, 8},
		{TypeDOUBLE, 8},
		{TypeLONG8, 8},
		{TypeIFD8, 8},
	}
	for _, c := range cases {
		if got := TypeByteSize(c.t); got != c.want {
			t.Errorf("TypeByteSize(%d): got %d want %d", c.t, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/...`

Expected: `package github.com/cornish/wsitools/internal/tiff: no Go files`

- [ ] **Step 3: Create doc.go**

Create `internal/tiff/doc.go`:

```go
// Package tiff provides TIFF byte-emission primitives shared by the
// streamwriter and cogwsiwriter packages. It contains no I/O
// orchestration — only header serialization, IFD entry encoding,
// type/tag constants, JPEGTables construction, BigTIFF auto-promote
// math, and in-place patch helpers.
//
// Design spec: docs/superpowers/specs/2026-05-21-tiff-core-extraction-design.md.
//
// All functions assume little-endian byte order. WSI files in practice
// are universally LE, and the v0.6.0 cogwsi/ifd.go (the ancestor of
// this package's EntryBuilder) removed its byte-order parameter for
// the same reason.
package tiff
```

- [ ] **Step 4: Create types.go**

Create `internal/tiff/types.go`:

```go
package tiff

// TIFF data type constants (TIFF 6.0 §2, BigTIFF additions).
const (
	TypeBYTE     uint16 = 1
	TypeASCII    uint16 = 2
	TypeSHORT    uint16 = 3
	TypeLONG     uint16 = 4
	TypeRATIONAL uint16 = 5
	TypeDOUBLE   uint16 = 12
	TypeLONG8    uint16 = 16
	TypeIFD8     uint16 = 18
)

// TypeByteSize returns the byte length of one value of the given TIFF
// type, or 0 if the type is unknown.
func TypeByteSize(t uint16) int {
	switch t {
	case TypeBYTE, TypeASCII:
		return 1
	case TypeSHORT:
		return 2
	case TypeLONG:
		return 4
	case TypeRATIONAL, TypeDOUBLE, TypeLONG8, TypeIFD8:
		return 8
	}
	return 0
}
```

- [ ] **Step 5: Run to verify pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/... -v`

Expected: PASS for both tests.

- [ ] **Step 6: Commit**

```bash
git add internal/tiff/doc.go internal/tiff/types.go internal/tiff/types_test.go
git commit -m "feat(tiff): scaffold internal/tiff package with TIFF type constants"
```

---

## Task 1.2: Standard + WSI private TIFF tag IDs

**Files:**
- Create: `internal/tiff/tags.go`
- Create: `internal/tiff/tags_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/tiff/tags_test.go`:

```go
package tiff

import "testing"

func TestStandardTagIDs(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"NewSubfileType", TagNewSubfileType, 254},
		{"ImageWidth", TagImageWidth, 256},
		{"ImageLength", TagImageLength, 257},
		{"BitsPerSample", TagBitsPerSample, 258},
		{"Compression", TagCompression, 259},
		{"PhotometricInterpretation", TagPhotometricInterpretation, 262},
		{"ImageDescription", TagImageDescription, 270},
		{"Make", TagMake, 271},
		{"Model", TagModel, 272},
		{"StripOffsets", TagStripOffsets, 273},
		{"SamplesPerPixel", TagSamplesPerPixel, 277},
		{"RowsPerStrip", TagRowsPerStrip, 278},
		{"StripByteCounts", TagStripByteCounts, 279},
		{"PlanarConfiguration", TagPlanarConfiguration, 284},
		{"Software", TagSoftware, 305},
		{"DateTime", TagDateTime, 306},
		{"TileWidth", TagTileWidth, 322},
		{"TileLength", TagTileLength, 323},
		{"TileOffsets", TagTileOffsets, 324},
		{"TileByteCounts", TagTileByteCounts, 325},
		{"JPEGTables", TagJPEGTables, 347},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}

func TestWSIPrivateTagIDs(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"WSIImageType", TagWSIImageType, 65080},
		{"WSILevelIndex", TagWSILevelIndex, 65081},
		{"WSILevelCount", TagWSILevelCount, 65082},
		{"WSISourceFormat", TagWSISourceFormat, 65083},
		{"WSIToolsVersion", TagWSIToolsVersion, 65084},
		{"WSIMPPX", TagWSIMPPX, 65085},
		{"WSIMPPY", TagWSIMPPY, 65086},
		{"WSIMagnification", TagWSIMagnification, 65087},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
		if c.got < 32768 {
			t.Errorf("%s tag id %d outside TIFF private range (>= 32768)", c.name, c.got)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -run TestStandardTagIDs -v`

Expected: compile error (`undefined: TagNewSubfileType` etc).

- [ ] **Step 3: Implement tags.go**

Create `internal/tiff/tags.go`:

```go
package tiff

// Standard TIFF tag IDs we use (subset of TIFF 6.0 §2 plus BigTIFF
// additions). Centralized so writers don't redeclare them.
const (
	TagNewSubfileType            uint16 = 254
	TagImageWidth                uint16 = 256
	TagImageLength               uint16 = 257
	TagBitsPerSample             uint16 = 258
	TagCompression               uint16 = 259
	TagPhotometricInterpretation uint16 = 262
	TagImageDescription          uint16 = 270
	TagMake                      uint16 = 271
	TagModel                     uint16 = 272
	TagStripOffsets              uint16 = 273
	TagSamplesPerPixel           uint16 = 277
	TagRowsPerStrip              uint16 = 278
	TagStripByteCounts           uint16 = 279
	TagPlanarConfiguration       uint16 = 284
	TagSoftware                  uint16 = 305
	TagDateTime                  uint16 = 306
	TagTileWidth                 uint16 = 322
	TagTileLength                uint16 = 323
	TagTileOffsets               uint16 = 324
	TagTileByteCounts            uint16 = 325
	TagJPEGTables                uint16 = 347
)

// WSI private tag IDs (range 65080–65087) reserved by wsitools.
// See docs/superpowers/specs/2026-05-20-cog-wsi-format.md §5.2.
const (
	TagWSIImageType     uint16 = 65080
	TagWSILevelIndex    uint16 = 65081
	TagWSILevelCount    uint16 = 65082
	TagWSISourceFormat  uint16 = 65083
	TagWSIToolsVersion  uint16 = 65084
	TagWSIMPPX          uint16 = 65085
	TagWSIMPPY          uint16 = 65086
	TagWSIMagnification uint16 = 65087
)
```

- [ ] **Step 4: Run to verify pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -v`

Expected: PASS for all tests.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/tags.go internal/tiff/tags_test.go
git commit -m "feat(tiff): standard + WSI private TIFF tag ID constants"
```

---

## Task 1.3: `WSIImageType` constants + `ValidateWSIImageType`

**Files:**
- Create: `internal/tiff/wsitags.go`
- Create: `internal/tiff/wsitags_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/tiff/wsitags_test.go`:

```go
package tiff

import "testing"

func TestWSIImageTypeConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"Pyramid", WSIImageTypePyramid, "pyramid"},
		{"Label", WSIImageTypeLabel, "label"},
		{"Macro", WSIImageTypeMacro, "macro"},
		{"Overview", WSIImageTypeOverview, "overview"},
		{"Thumbnail", WSIImageTypeThumbnail, "thumbnail"},
		{"Probability", WSIImageTypeProbability, "probability"},
		{"Map", WSIImageTypeMap, "map"},
		{"Associated", WSIImageTypeAssociated, "associated"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q want %q", c.name, c.got, c.want)
		}
	}
}

func TestValidateWSIImageTypeAcceptsCanonical(t *testing.T) {
	for _, v := range []string{
		WSIImageTypePyramid, WSIImageTypeLabel, WSIImageTypeMacro,
		WSIImageTypeOverview, WSIImageTypeThumbnail,
		WSIImageTypeProbability, WSIImageTypeMap, WSIImageTypeAssociated,
	} {
		if err := ValidateWSIImageType(v); err != nil {
			t.Errorf("ValidateWSIImageType(%q): unexpected error %v", v, err)
		}
	}
}

func TestValidateWSIImageTypeRejectsUnknown(t *testing.T) {
	for _, v := range []string{"", "Pyramid", "labels", "macros", "unknown"} {
		if err := ValidateWSIImageType(v); err == nil {
			t.Errorf("ValidateWSIImageType(%q): expected error, got nil", v)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -run TestWSI -v`

Expected: compile error.

- [ ] **Step 3: Implement wsitags.go**

Create `internal/tiff/wsitags.go`:

```go
package tiff

import "fmt"

// WSIImageType canonical values. Lowercase to match opentile-go's
// AssociatedImage.Kind() vocabulary.
const (
	WSIImageTypePyramid     = "pyramid"
	WSIImageTypeLabel       = "label"
	WSIImageTypeMacro       = "macro"
	WSIImageTypeOverview    = "overview"
	WSIImageTypeThumbnail   = "thumbnail"
	WSIImageTypeProbability = "probability"
	WSIImageTypeMap         = "map"
	WSIImageTypeAssociated  = "associated"
)

var validWSIImageTypes = map[string]bool{
	WSIImageTypePyramid:     true,
	WSIImageTypeLabel:       true,
	WSIImageTypeMacro:       true,
	WSIImageTypeOverview:    true,
	WSIImageTypeThumbnail:   true,
	WSIImageTypeProbability: true,
	WSIImageTypeMap:         true,
	WSIImageTypeAssociated:  true,
}

// ValidateWSIImageType returns nil if v is one of the canonical
// WSIImageType values; otherwise returns a descriptive error.
// Stricter subsets (e.g. cogwsi's 4-value associated-image set) live
// in the writer packages that enforce them.
func ValidateWSIImageType(v string) error {
	if !validWSIImageTypes[v] {
		return fmt.Errorf("tiff: invalid WSIImageType %q (want one of pyramid|label|macro|overview|thumbnail|probability|map|associated)", v)
	}
	return nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -v`

Expected: PASS for all tests.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/wsitags.go internal/tiff/wsitags_test.go
git commit -m "feat(tiff): canonical WSIImageType values + ValidateWSIImageType"
```

---

## Task 1.4: TIFF/BigTIFF header writer

**Files:**
- Create: `internal/tiff/header.go`
- Create: `internal/tiff/header_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/tiff/header_test.go`:

```go
package tiff

import (
	"bytes"
	"testing"
)

// fakeWriterAt is a simple in-memory io.WriterAt for tests.
type fakeWriterAt struct {
	buf []byte
}

func (f *fakeWriterAt) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(f.buf) {
		newBuf := make([]byte, end)
		copy(newBuf, f.buf)
		f.buf = newBuf
	}
	copy(f.buf[off:end], p)
	return len(p), nil
}

func TestWriteHeaderClassic(t *testing.T) {
	f := &fakeWriterAt{}
	if err := WriteHeader(f, false, 0x1234); err != nil {
		t.Fatal(err)
	}
	want := []byte{
		'I', 'I', // little-endian
		0x2A, 0x00, // classic TIFF magic = 42
		0x34, 0x12, 0x00, 0x00, // IFD0 offset = 0x1234 (LE)
	}
	if !bytes.Equal(f.buf, want) {
		t.Errorf("classic header: got %x want %x", f.buf, want)
	}
}

func TestWriteHeaderBigTIFF(t *testing.T) {
	f := &fakeWriterAt{}
	if err := WriteHeader(f, true, 0x123456789A); err != nil {
		t.Fatal(err)
	}
	want := []byte{
		'I', 'I',
		0x2B, 0x00, // BigTIFF magic = 43
		0x08, 0x00, // offset size = 8
		0x00, 0x00, // constant zero
		0x9A, 0x78, 0x56, 0x34, 0x12, 0x00, 0x00, 0x00, // IFD0 offset (LE uint64)
	}
	if !bytes.Equal(f.buf, want) {
		t.Errorf("BigTIFF header: got %x want %x", f.buf, want)
	}
}

func TestHeaderSize(t *testing.T) {
	if got := HeaderSize(false); got != 8 {
		t.Errorf("HeaderSize(classic): got %d want 8", got)
	}
	if got := HeaderSize(true); got != 16 {
		t.Errorf("HeaderSize(BigTIFF): got %d want 16", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -run TestWriteHeader -v`

Expected: compile error.

- [ ] **Step 3: Implement header.go**

Create `internal/tiff/header.go`:

```go
package tiff

import (
	"encoding/binary"
	"io"
)

// HeaderSize returns the byte length of the TIFF header: 8 for classic,
// 16 for BigTIFF.
func HeaderSize(bigtiff bool) int {
	if bigtiff {
		return 16
	}
	return 8
}

// WriteHeader writes the TIFF header at offset 0 of w. firstIFDOffset
// is the absolute byte offset of IFD 0 in the output file. All bytes
// are little-endian.
func WriteHeader(w io.WriterAt, bigtiff bool, firstIFDOffset uint64) error {
	hdr := make([]byte, HeaderSize(bigtiff))
	hdr[0], hdr[1] = 'I', 'I'
	if bigtiff {
		binary.LittleEndian.PutUint16(hdr[2:4], 0x002B) // BigTIFF magic
		binary.LittleEndian.PutUint16(hdr[4:6], 8)      // offset size
		binary.LittleEndian.PutUint16(hdr[6:8], 0)      // constant zero
		binary.LittleEndian.PutUint64(hdr[8:16], firstIFDOffset)
	} else {
		binary.LittleEndian.PutUint16(hdr[2:4], 0x002A) // classic magic
		binary.LittleEndian.PutUint32(hdr[4:8], uint32(firstIFDOffset))
	}
	_, err := w.WriteAt(hdr, 0)
	return err
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/header.go internal/tiff/header_test.go
git commit -m "feat(tiff): WriteHeader + HeaderSize for classic + BigTIFF"
```

---

## Task 1.5: `EntryBuilder` (extracted from `cogwsi/ifd.go`)

**Files:**
- Create: `internal/tiff/entry.go`
- Create: `internal/tiff/entry_test.go`

**Background:** This is the canonical IFD entry builder, promoted from `internal/cogwsi/ifd.go`. The cogwsi version is already clean (after the `12168dd` fix that removed the misleading `bo` parameter and added the overflow guard on `AddTileOffsets`). We move it verbatim with one rename: `ifdBuilder` (unexported) → `EntryBuilder` (exported), and `newIFDBuilder` → `NewEntryBuilder`. Plus add the size helpers (`EntrySize`, `IFDRecordSize`) that cogwsi/layout.go currently defines as package-level constants.

- [ ] **Step 1: Write failing tests**

Create `internal/tiff/entry_test.go`:

```go
package tiff

import (
	"encoding/binary"
	"testing"
)

func TestEntryBuilderClassicSimple(t *testing.T) {
	b := NewEntryBuilder(false /*bigtiff*/)
	b.AddShort(TagImageWidth, []uint16{512})
	b.AddShort(TagImageLength, []uint16{384})
	ifd, ext, err := b.Encode(100 /*ifdOffset*/)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 0 {
		t.Errorf("expected no external bytes, got %d", len(ext))
	}
	// Classic IFD: uint16 entry_count + 2 entries * 12 + uint32 next_ifd = 30.
	if len(ifd) != 30 {
		t.Errorf("ifd size: got %d want 30", len(ifd))
	}
	if binary.LittleEndian.Uint16(ifd[:2]) != 2 {
		t.Errorf("entry count: got %d want 2", binary.LittleEndian.Uint16(ifd[:2]))
	}
}

func TestEntryBuilderBigTIFFLongArray(t *testing.T) {
	b := NewEntryBuilder(true /*bigtiff*/)
	offsets := []uint64{1000, 2000, 3000}
	b.AddLong8(TagTileOffsets, offsets)
	ifd, ext, err := b.Encode(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 24 {
		t.Errorf("external bytes: got %d want 24", len(ext))
	}
	// BigTIFF IFD: uint64 entry_count + 1 entry * 20 + uint64 next_ifd = 36.
	if len(ifd) != 36 {
		t.Errorf("ifd size: got %d want 36", len(ifd))
	}
}

func TestEntryBuilderASCIIInline(t *testing.T) {
	b := NewEntryBuilder(false)
	b.AddASCII(TagSoftware, "go")
	ifd, ext, _ := b.Encode(100)
	if len(ext) != 0 {
		t.Errorf("short ASCII should be inline, got %d external bytes", len(ext))
	}
	const entryStart = 2
	count := binary.LittleEndian.Uint32(ifd[entryStart+4 : entryStart+8])
	if count != 3 {
		t.Errorf("ASCII count: got %d want 3 (go\\0)", count)
	}
}

func TestEntryBuilderASCIIExternal(t *testing.T) {
	b := NewEntryBuilder(false)
	long := "this string is more than four bytes long"
	b.AddASCII(TagImageDescription, long)
	ifd, ext, _ := b.Encode(100)
	if len(ext) != len(long)+1 {
		t.Errorf("external bytes: got %d want %d", len(ext), len(long)+1)
	}
}

func TestAddTileOffsetsClassicOverflow(t *testing.T) {
	b := NewEntryBuilder(false)
	err := b.AddTileOffsets(TagTileOffsets, []uint64{0xFFFFFFFFFF})
	if err == nil {
		t.Errorf("expected overflow error in classic mode for offset > 4 GiB")
	}
}

func TestEntrySize(t *testing.T) {
	if got := EntrySize(false); got != 12 {
		t.Errorf("EntrySize(classic): got %d want 12", got)
	}
	if got := EntrySize(true); got != 20 {
		t.Errorf("EntrySize(BigTIFF): got %d want 20", got)
	}
}

func TestIFDRecordSize(t *testing.T) {
	// Classic: 2 (count) + N*12 + 4 (next) = 6 + 12N.
	if got := IFDRecordSize(5, false); got != 6+5*12 {
		t.Errorf("IFDRecordSize(5, classic): got %d want %d", got, 6+5*12)
	}
	// BigTIFF: 8 (count) + N*20 + 8 (next) = 16 + 20N.
	if got := IFDRecordSize(5, true); got != 16+5*20 {
		t.Errorf("IFDRecordSize(5, BigTIFF): got %d want %d", got, 16+5*20)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -run TestEntry -v`

Expected: compile error.

- [ ] **Step 3: Implement entry.go**

Create `internal/tiff/entry.go`:

```go
package tiff

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// Internal byte-size constants for TIFF directory layout.
const (
	classicEntrySize = 12 // uint16 tag, uint16 type, uint32 count, uint32 value
	bigTIFFEntrySize = 20 // uint16 tag, uint16 type, uint64 count, uint64 value
)

// EntrySize returns the byte length of one IFD entry (directory record),
// 12 for classic TIFF and 20 for BigTIFF.
func EntrySize(bigtiff bool) int {
	if bigtiff {
		return bigTIFFEntrySize
	}
	return classicEntrySize
}

// IFDRecordSize returns the byte length of an IFD directory record with
// tagCount entries. Classic: 2 (count) + N*12 + 4 (next-IFD). BigTIFF:
// 8 (count) + N*20 + 8 (next-IFD).
func IFDRecordSize(tagCount int, bigtiff bool) int {
	if bigtiff {
		return 8 + tagCount*bigTIFFEntrySize + 8
	}
	return 2 + tagCount*classicEntrySize + 4
}

type ifdEntry struct {
	tag         uint16
	tiffType    uint16
	count       uint64
	inlineValue [8]byte
	externalRaw []byte
}

// EntryBuilder accumulates TIFF directory entries; Encode emits the
// directory record + concatenated external bytes for entries that
// don't fit inline. Little-endian only.
type EntryBuilder struct {
	bigtiff bool
	entries []ifdEntry
}

// NewEntryBuilder returns a new builder. bigtiff selects classic vs
// BigTIFF entry layout.
func NewEntryBuilder(bigtiff bool) *EntryBuilder {
	return &EntryBuilder{bigtiff: bigtiff}
}

func (b *EntryBuilder) inlineCap() int {
	if b.bigtiff {
		return 8
	}
	return 4
}

func (b *EntryBuilder) addRaw(tag uint16, tiffType uint16, count uint64, payload []byte) {
	e := ifdEntry{tag: tag, tiffType: tiffType, count: count}
	if len(payload) <= b.inlineCap() {
		copy(e.inlineValue[:], payload)
	} else {
		e.externalRaw = payload
	}
	b.entries = append(b.entries, e)
}

// AddShort appends a SHORT (uint16) array entry.
func (b *EntryBuilder) AddShort(tag uint16, vals []uint16) {
	payload := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint16(payload[i*2:], v)
	}
	b.addRaw(tag, TypeSHORT, uint64(len(vals)), payload)
}

// AddLong appends a LONG (uint32) array entry.
func (b *EntryBuilder) AddLong(tag uint16, vals []uint32) {
	payload := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(payload[i*4:], v)
	}
	b.addRaw(tag, TypeLONG, uint64(len(vals)), payload)
}

// AddLong8 appends a BigTIFF LONG8 (uint64) array entry. Only valid in BigTIFF.
func (b *EntryBuilder) AddLong8(tag uint16, vals []uint64) {
	payload := make([]byte, 8*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint64(payload[i*8:], v)
	}
	b.addRaw(tag, TypeLONG8, uint64(len(vals)), payload)
}

// AddTileOffsets appends offsets as LONG (classic) or LONG8 (BigTIFF).
// Returns an error if any offset exceeds the classic TIFF 4 GiB limit
// when in classic mode.
func (b *EntryBuilder) AddTileOffsets(tag uint16, offsets []uint64) error {
	if b.bigtiff {
		b.AddLong8(tag, offsets)
		return nil
	}
	asLong := make([]uint32, len(offsets))
	for i, o := range offsets {
		if o > 0xFFFFFFFF {
			return fmt.Errorf("tiff: tile offset %d (tag %d, index %d) overflows classic TIFF; BigTIFF promotion missed", o, tag, i)
		}
		asLong[i] = uint32(o)
	}
	b.AddLong(tag, asLong)
	return nil
}

// AddASCII appends an ASCII entry. count includes the trailing NUL.
func (b *EntryBuilder) AddASCII(tag uint16, s string) {
	payload := append([]byte(s), 0)
	b.addRaw(tag, TypeASCII, uint64(len(payload)), payload)
}

// AddBytes appends raw bytes (BYTE type).
func (b *EntryBuilder) AddBytes(tag uint16, payload []byte) {
	b.addRaw(tag, TypeBYTE, uint64(len(payload)), payload)
}

// AddDouble appends a DOUBLE (float64) array entry.
func (b *EntryBuilder) AddDouble(tag uint16, vals []float64) {
	payload := make([]byte, 8*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint64(payload[i*8:], math.Float64bits(v))
	}
	b.addRaw(tag, TypeDOUBLE, uint64(len(vals)), payload)
}

// AddRational appends a RATIONAL (uint32/uint32) array entry. Pairs of
// (numerator, denominator) per value.
func (b *EntryBuilder) AddRational(tag uint16, nums, denoms []uint32) {
	if len(nums) != len(denoms) {
		panic("tiff: AddRational: nums/denoms length mismatch")
	}
	payload := make([]byte, 8*len(nums))
	for i := range nums {
		binary.LittleEndian.PutUint32(payload[i*8:], nums[i])
		binary.LittleEndian.PutUint32(payload[i*8+4:], denoms[i])
	}
	b.addRaw(tag, TypeRATIONAL, uint64(len(nums)), payload)
}

// Encode serializes the IFD record at ifdOffset and returns:
//   - ifd: the directory bytes
//   - ext: external bytes that go at ifdOffset + len(ifd)
//
// External entries' inline-value slots are filled with their final
// absolute offsets.
func (b *EntryBuilder) Encode(ifdOffset uint64) (ifd, ext []byte, err error) {
	if !b.bigtiff && ifdOffset > 0xFFFFFFFF {
		return nil, nil, fmt.Errorf("tiff: classic TIFF IFD offset overflow: %d", ifdOffset)
	}

	// Sort by tag (TIFF requires ascending tag order).
	sorted := append([]ifdEntry(nil), b.entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].tag < sorted[j].tag })

	dirSize := uint64(IFDRecordSize(len(sorted), b.bigtiff))
	ifd = make([]byte, dirSize)

	// Assign external offsets, accumulate external buffer.
	cursor := ifdOffset + dirSize
	var extBuf []byte
	for i := range sorted {
		if sorted[i].externalRaw == nil {
			continue
		}
		setOffset(sorted[i].inlineValue[:], cursor, b.bigtiff)
		extBuf = append(extBuf, sorted[i].externalRaw...)
		cursor += uint64(len(sorted[i].externalRaw))
	}

	// Write entry count.
	if b.bigtiff {
		binary.LittleEndian.PutUint64(ifd[0:8], uint64(len(sorted)))
	} else {
		binary.LittleEndian.PutUint16(ifd[0:2], uint16(len(sorted)))
	}

	// Write entries.
	off := uint64(8)
	if !b.bigtiff {
		off = 2
	}
	for _, e := range sorted {
		binary.LittleEndian.PutUint16(ifd[off:off+2], e.tag)
		binary.LittleEndian.PutUint16(ifd[off+2:off+4], e.tiffType)
		if b.bigtiff {
			binary.LittleEndian.PutUint64(ifd[off+4:off+12], e.count)
			copy(ifd[off+12:off+20], e.inlineValue[:8])
			off += bigTIFFEntrySize
		} else {
			binary.LittleEndian.PutUint32(ifd[off+4:off+8], uint32(e.count))
			copy(ifd[off+8:off+12], e.inlineValue[:4])
			off += classicEntrySize
		}
	}
	// next-IFD field stays zero; the writer patches it during finalize.
	return ifd, extBuf, nil
}

func setOffset(slot []byte, val uint64, bigtiff bool) {
	if bigtiff {
		binary.LittleEndian.PutUint64(slot[:8], val)
	} else {
		binary.LittleEndian.PutUint32(slot[:4], uint32(val))
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -v`

Expected: PASS for all tests.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/entry.go internal/tiff/entry_test.go
git commit -m "feat(tiff): EntryBuilder + EntrySize + IFDRecordSize (extracted from cogwsi/ifd.go)"
```

---

## Task 1.6: BigTIFF auto-promote helpers

**Files:**
- Create: `internal/tiff/bigtiff.go`
- Create: `internal/tiff/bigtiff_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/tiff/bigtiff_test.go`:

```go
package tiff

import "testing"

func TestBigTIFFModeResolveAuto(t *testing.T) {
	// Under threshold: classic.
	if Resolve(BigTIFFAuto, 100*1024*1024) {
		t.Errorf("100 MiB should not promote")
	}
	// Over threshold: BigTIFF.
	if !Resolve(BigTIFFAuto, 3*(1<<30)) {
		t.Errorf("3 GiB should promote")
	}
}

func TestBigTIFFModeResolveOverrides(t *testing.T) {
	if !Resolve(BigTIFFOn, 100) {
		t.Errorf("BigTIFFOn must promote regardless of size")
	}
	if Resolve(BigTIFFOff, 100*(1<<30)) {
		t.Errorf("BigTIFFOff must NOT promote regardless of size")
	}
}

func TestAutoPromoteThreshold(t *testing.T) {
	// 2 GiB exactly: should NOT promote (predicate is strictly >).
	if AutoPromote(2*(1<<30), 0) {
		t.Errorf("exactly 2 GiB should not promote")
	}
	// 2 GiB + 1: should promote.
	if !AutoPromote(2*(1<<30)+1, 0) {
		t.Errorf("2 GiB + 1 should promote")
	}
	// Metadata adds to total.
	if !AutoPromote(2*(1<<30)-100, 200) {
		t.Errorf("data+meta over 2 GiB should promote")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -run TestBigTIFF -v`

Expected: compile error.

- [ ] **Step 3: Implement bigtiff.go**

Create `internal/tiff/bigtiff.go`:

```go
package tiff

// BigTIFFMode controls classic-vs-BigTIFF selection in writers.
type BigTIFFMode int

const (
	BigTIFFAuto BigTIFFMode = iota
	BigTIFFOn
	BigTIFFOff
)

// safetyMargin is the byte budget added on top of caller-supplied
// dataBytes + metaBytes to leave room for write-time padding and tag-
// array growth before crossing the 2 GiB threshold.
const safetyMargin = 64 * 1024

// AutoPromote reports whether predicted output > 2 GiB.
// Total = dataBytes + metaBytes + safetyMargin; promote when total > 2 GiB.
// The 2 GiB threshold (rather than the classic-TIFF 4 GiB ceiling)
// leaves ample headroom for late-discovered metadata.
func AutoPromote(dataBytes, metaBytes uint64) bool {
	return dataBytes+metaBytes+safetyMargin > (2 << 30)
}

// Resolve applies a BigTIFFMode against a predicted byte total.
// BigTIFFOn returns true; BigTIFFOff returns false; BigTIFFAuto
// returns AutoPromote(predictedBytes, 0).
func Resolve(mode BigTIFFMode, predictedBytes uint64) bool {
	switch mode {
	case BigTIFFOn:
		return true
	case BigTIFFOff:
		return false
	}
	return AutoPromote(predictedBytes, 0)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/bigtiff.go internal/tiff/bigtiff_test.go
git commit -m "feat(tiff): BigTIFF auto-promote mode + Resolve helper"
```

---

## Task 1.7: In-place patch helpers

**Files:**
- Create: `internal/tiff/patch.go`
- Create: `internal/tiff/patch_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/tiff/patch_test.go`:

```go
package tiff

import (
	"bytes"
	"testing"
)

func TestPatchUint32(t *testing.T) {
	f := &fakeWriterAt{buf: make([]byte, 16)}
	if err := PatchUint32(f, 4, 0xDEADBEEF); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 0, 0xEF, 0xBE, 0xAD, 0xDE, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(f.buf, want) {
		t.Errorf("got %x want %x", f.buf, want)
	}
}

func TestPatchUint64(t *testing.T) {
	f := &fakeWriterAt{buf: make([]byte, 16)}
	if err := PatchUint64(f, 0, 0x0123456789ABCDEF); err != nil {
		t.Fatal(err)
	}
	want := []byte{0xEF, 0xCD, 0xAB, 0x89, 0x67, 0x45, 0x23, 0x01, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(f.buf, want) {
		t.Errorf("got %x want %x", f.buf, want)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -run TestPatch -v`

Expected: compile error.

- [ ] **Step 3: Implement patch.go**

Create `internal/tiff/patch.go`:

```go
package tiff

import (
	"encoding/binary"
	"io"
)

// PatchUint32 writes a little-endian uint32 at the given offset. Used
// by streaming-style writers to fill in IFD offsets they emitted as
// placeholders.
func PatchUint32(w io.WriterAt, at int64, v uint32) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	_, err := w.WriteAt(buf[:], at)
	return err
}

// PatchUint64 writes a little-endian uint64 at the given offset.
// BigTIFF equivalent of PatchUint32.
func PatchUint64(w io.WriterAt, at int64, v uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	_, err := w.WriteAt(buf[:], at)
	return err
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/patch.go internal/tiff/patch_test.go
git commit -m "feat(tiff): in-place PatchUint32 + PatchUint64 helpers"
```

---

## Task 1.8: JPEGTables helpers (moved from wsiwriter)

**Files:**
- Create: `internal/tiff/jpegtables.go`
- Create: `internal/tiff/jpegtables_test.go`

**Background:** This is a verbatim copy from `internal/wsiwriter/jpegtables.go`. The existing functions (`ExtractJPEGTables`, `StripJPEGTables`) and constants stay byte-for-byte the same — only the package name changes (from `wsiwriter` to `tiff`) and error messages get reprefixed. The existing `internal/wsiwriter/jpegtables_test.go` is the basis for the new test file.

- [ ] **Step 1: Copy the file content and rename package**

Create `internal/tiff/jpegtables.go`:

```go
package tiff

import (
	"bytes"
	"fmt"
)

// JPEG marker constants. Two-byte sequences 0xFF, 0x?? define markers.
const (
	jpegSOI = 0xD8
	jpegEOI = 0xD9
	jpegDQT = 0xDB
	jpegDHT = 0xC4
	jpegDRI = 0xDD
	jpegSOS = 0xDA
)

// ExtractJPEGTables walks a self-contained JPEG and returns a tables-only JPEG
// containing SOI + all DQT + all DHT + (optional DRI) + EOI markers, suitable
// for writing into TIFF tag 347 (JPEGTables).
//
// The tables-only JPEG must end before SOS — once SOS is reached, scan data
// follows and must be excluded.
func ExtractJPEGTables(jpg []byte) ([]byte, error) {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != jpegSOI {
		return nil, fmt.Errorf("tiff: not a JPEG (no SOI)")
	}
	out := []byte{0xFF, jpegSOI}
	i := 2
	for i < len(jpg)-1 {
		if jpg[i] != 0xFF {
			i++
			continue
		}
		marker := jpg[i+1]
		if marker == 0xFF {
			i++ // padding fill byte
			continue
		}
		if marker == jpegSOI || marker == jpegEOI || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if marker == jpegSOS {
			break
		}
		if i+4 > len(jpg) {
			return nil, fmt.Errorf("tiff: truncated JPEG marker length")
		}
		segLen := int(jpg[i+2])<<8 | int(jpg[i+3])
		segEnd := i + 2 + segLen
		if segEnd > len(jpg) {
			return nil, fmt.Errorf("tiff: truncated JPEG segment")
		}
		if marker == jpegDQT || marker == jpegDHT || marker == jpegDRI {
			out = append(out, jpg[i:segEnd]...)
		}
		i = segEnd
	}
	out = append(out, 0xFF, jpegEOI)
	return out, nil
}

// StripJPEGTables walks a self-contained JPEG and returns a copy with all DQT
// and DHT markers removed. Result is the abbreviated-form tile bytes that pair
// with a JPEGTables tag of the same shared tables.
func StripJPEGTables(jpg []byte) ([]byte, error) {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != jpegSOI {
		return nil, fmt.Errorf("tiff: not a JPEG")
	}
	var out bytes.Buffer
	out.Write([]byte{0xFF, jpegSOI})
	i := 2
	for i < len(jpg)-1 {
		if jpg[i] != 0xFF {
			i++
			continue
		}
		marker := jpg[i+1]
		if marker == 0xFF {
			i++
			continue
		}
		if marker == jpegSOI || marker == jpegEOI || (marker >= 0xD0 && marker <= 0xD7) {
			out.Write(jpg[i : i+2])
			i += 2
			continue
		}
		if marker == jpegSOS {
			out.Write(jpg[i:])
			return out.Bytes(), nil
		}
		segLen := int(jpg[i+2])<<8 | int(jpg[i+3])
		segEnd := i + 2 + segLen
		if marker != jpegDQT && marker != jpegDHT {
			out.Write(jpg[i:segEnd])
		}
		i = segEnd
	}
	return out.Bytes(), nil
}
```

- [ ] **Step 2: Copy + adapt the test file from wsiwriter**

The existing tests at `/Users/cornish/GitHub/wsitools/internal/wsiwriter/jpegtables_test.go` test these functions. Read that file, copy its body to `internal/tiff/jpegtables_test.go`, and change:
- `package wsiwriter` → `package tiff`
- Update any error-message strings the tests check for (`wsiwriter:` → `tiff:`).

Create `internal/tiff/jpegtables_test.go` with the adapted content. (Open the source file with Read, copy verbatim, do the two find-and-replaces.)

- [ ] **Step 3: Run tests to verify pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -run TestExtractJPEGTables -v && go test ./internal/tiff/ -run TestStripJPEGTables -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tiff/jpegtables.go internal/tiff/jpegtables_test.go
git commit -m "feat(tiff): JPEGTables construction + stripping helpers (moved from wsiwriter)"
```

---

## Task 1.9: TIFF Compression tag constants

**Files:**
- Modify: `internal/tiff/tags.go`
- Modify: `internal/tiff/tags_test.go`

**Background:** `transcode` and `downsample` currently reference compression constants via `wsiwriter.CompressionJPEG` etc. Adding them to `internal/tiff/tags.go` lets landing 3 do clean import-path rewrites without scattering raw TIFF magic numbers across `cmd/wsitools/`.

- [ ] **Step 1: Append a test**

Append to `internal/tiff/tags_test.go`:

```go
func TestCompressionConstants(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"None", CompressionNone, 1},
		{"LZW", CompressionLZW, 5},
		{"JPEG", CompressionJPEG, 7},
		{"Deflate", CompressionDeflate, 8},
		{"JPEG2000", CompressionJPEG2000, 33003},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -run TestCompression -v`

Expected: compile error.

- [ ] **Step 3: Append the constants to tags.go**

Append to `internal/tiff/tags.go`:

```go
// TIFF Compression tag values we support. The Compression tag (259)
// itself is declared above; these are the value-space constants.
const (
	CompressionNone     uint16 = 1
	CompressionLZW      uint16 = 5
	CompressionJPEG     uint16 = 7
	CompressionDeflate  uint16 = 8
	CompressionJPEG2000 uint16 = 33003 // Aperio / OpenJPEG codestream
)
```

- [ ] **Step 4: Run to verify pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/tags.go internal/tiff/tags_test.go
git commit -m "feat(tiff): TIFF Compression tag value constants"
```

---

## Task 1.10: `tiff.RawTag` for caller-supplied tag entries

**Files:**
- Modify: `internal/tiff/entry.go`
- Modify: `internal/tiff/entry_test.go`

**Background:** Landing 3's `streamwriter` accepts caller-supplied tags via `LevelSpec.ExtraTags` and `StrippedSpec.ExtraTags`. The carrier type is `tiff.RawTag` and `EntryBuilder` needs an `AddRaw` method to consume them. Both belong in landing 1 so streamwriter can depend on them.

- [ ] **Step 1: Append failing test**

Append to `internal/tiff/entry_test.go`:

```go
func TestAddRawShort(t *testing.T) {
	b := NewEntryBuilder(false)
	if err := b.AddRaw(RawTag{Tag: TagImageWidth, Type: TypeSHORT, Value: []uint16{512}}); err != nil {
		t.Fatal(err)
	}
	ifd, _, _ := b.Encode(0)
	const entryStart = 2
	if binary.LittleEndian.Uint16(ifd[entryStart:entryStart+2]) != TagImageWidth {
		t.Errorf("AddRaw didn't add the expected tag")
	}
}

func TestAddRawASCII(t *testing.T) {
	b := NewEntryBuilder(false)
	if err := b.AddRaw(RawTag{Tag: TagImageDescription, Type: TypeASCII, Value: "hello"}); err != nil {
		t.Fatal(err)
	}
	ifd, _, _ := b.Encode(0)
	const entryStart = 2
	if binary.LittleEndian.Uint32(ifd[entryStart+4:entryStart+8]) != uint32(len("hello")+1) {
		t.Errorf("AddRaw ASCII count mismatch")
	}
}

func TestAddRawRejectsUnknownType(t *testing.T) {
	b := NewEntryBuilder(false)
	err := b.AddRaw(RawTag{Tag: 256, Type: 99 /*nonexistent*/, Value: []uint16{1}})
	if err == nil {
		t.Errorf("expected error for unknown TIFF type 99")
	}
}

func TestAddRawTypeMismatch(t *testing.T) {
	b := NewEntryBuilder(false)
	// SHORT type but []uint32 value — should error.
	err := b.AddRaw(RawTag{Tag: 256, Type: TypeSHORT, Value: []uint32{1}})
	if err == nil {
		t.Errorf("expected error for type/value mismatch")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -run TestAddRaw -v`

Expected: compile error.

- [ ] **Step 3: Add `RawTag` type + `AddRaw` method to entry.go**

Append to `internal/tiff/entry.go`:

```go
// RawTag is a caller-supplied tag entry. Used by writers to accept
// format-specific tags (e.g. Aperio SVS-shape tags emitted by transcode)
// without baking the format into the writer.
//
// Type must be one of the exported Type* constants. Value must match:
//   - TypeBYTE  → []byte
//   - TypeASCII → string
//   - TypeSHORT → []uint16
//   - TypeLONG  → []uint32
//   - TypeLONG8 → []uint64 (BigTIFF only)
//   - TypeDOUBLE → []float64
//   - TypeRATIONAL → [2][]uint32 i.e. [2 elements: numerators, denominators of equal length]
type RawTag struct {
	Tag   uint16
	Type  uint16
	Value any
}

// AddRaw dispatches a RawTag to the appropriate typed-Add method.
// Returns an error if Type is unknown or Value type doesn't match Type.
func (b *EntryBuilder) AddRaw(t RawTag) error {
	switch t.Type {
	case TypeBYTE:
		v, ok := t.Value.([]byte)
		if !ok {
			return fmt.Errorf("tiff: AddRaw tag %d: TypeBYTE expects []byte, got %T", t.Tag, t.Value)
		}
		b.AddBytes(t.Tag, v)
	case TypeASCII:
		v, ok := t.Value.(string)
		if !ok {
			return fmt.Errorf("tiff: AddRaw tag %d: TypeASCII expects string, got %T", t.Tag, t.Value)
		}
		b.AddASCII(t.Tag, v)
	case TypeSHORT:
		v, ok := t.Value.([]uint16)
		if !ok {
			return fmt.Errorf("tiff: AddRaw tag %d: TypeSHORT expects []uint16, got %T", t.Tag, t.Value)
		}
		b.AddShort(t.Tag, v)
	case TypeLONG:
		v, ok := t.Value.([]uint32)
		if !ok {
			return fmt.Errorf("tiff: AddRaw tag %d: TypeLONG expects []uint32, got %T", t.Tag, t.Value)
		}
		b.AddLong(t.Tag, v)
	case TypeLONG8:
		v, ok := t.Value.([]uint64)
		if !ok {
			return fmt.Errorf("tiff: AddRaw tag %d: TypeLONG8 expects []uint64, got %T", t.Tag, t.Value)
		}
		b.AddLong8(t.Tag, v)
	case TypeDOUBLE:
		v, ok := t.Value.([]float64)
		if !ok {
			return fmt.Errorf("tiff: AddRaw tag %d: TypeDOUBLE expects []float64, got %T", t.Tag, t.Value)
		}
		b.AddDouble(t.Tag, v)
	case TypeRATIONAL:
		v, ok := t.Value.([2][]uint32)
		if !ok {
			return fmt.Errorf("tiff: AddRaw tag %d: TypeRATIONAL expects [2][]uint32, got %T", t.Tag, t.Value)
		}
		b.AddRational(t.Tag, v[0], v[1])
	default:
		return fmt.Errorf("tiff: AddRaw tag %d: unknown TIFF type %d", t.Tag, t.Type)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/ -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/entry.go internal/tiff/entry_test.go
git commit -m "feat(tiff): RawTag carrier type + EntryBuilder.AddRaw method"
```

---

## Task 1.11: Verify landing 1 is complete + no regressions

- [ ] **Step 1: Full test suite**

Run: `cd /Users/cornish/GitHub/wsitools && go test -race -count=1 ./...`

Expected: ALL packages pass, including the still-existing `internal/wsiwriter` and `internal/cogwsi`. The new `internal/tiff` is purely additive and shouldn't have affected them.

- [ ] **Step 2: Build check**

Run: `cd /Users/cornish/GitHub/wsitools && go build ./...`

Expected: success.

- [ ] **Step 3: Confirm package contents**

Run: `cd /Users/cornish/GitHub/wsitools && ls internal/tiff/`

Expected output should include: `doc.go`, `types.go`, `tags.go`, `wsitags.go`, `header.go`, `entry.go`, `bigtiff.go`, `patch.go`, `jpegtables.go`, plus corresponding `_test.go` files.

- [ ] **Step 4: Push (optional checkpoint)**

```bash
git push origin main
```

**Landing 1 acceptance:** `internal/tiff` package is fully built and tested; consumers (`wsiwriter`, `cogwsi`) are untouched; v0.6.0 binary behavior unchanged.

---

# LANDING 2 — Port `internal/cogwsi` → `internal/tiff/cogwsiwriter`

Move-and-edit: the existing cogwsi package moves under the new path and its internals re-route to `internal/tiff` helpers. The smaller, lower-risk port.

## Task 2.1: Move package directory

**Files:**
- Move: `internal/cogwsi/` → `internal/tiff/cogwsiwriter/`

- [ ] **Step 1: Move via git**

Run: `cd /Users/cornish/GitHub/wsitools && git mv internal/cogwsi internal/tiff/cogwsiwriter`

Confirm files moved:

Run: `cd /Users/cornish/GitHub/wsitools && ls internal/tiff/cogwsiwriter/`

Expected: doc.go, ghost.go, ghost_test.go, ifd.go, ifd_test.go, layout.go, layout_test.go, spool.go, spool_test.go, tags.go, tags_test.go, writer.go, writer_test.go.

- [ ] **Step 2: Rename package declarations in all Go files**

For each `.go` file in `internal/tiff/cogwsiwriter/`, change the package declaration from `package cogwsi` (or `package cogwsi_test` for external test files) to `package cogwsiwriter` (or `package cogwsiwriter_test`).

Use this Bash command to do them all at once:

```bash
cd /Users/cornish/GitHub/wsitools
for f in internal/tiff/cogwsiwriter/*.go; do
  sed -i '' 's/^package cogwsi_test$/package cogwsiwriter_test/' "$f"
  sed -i '' 's/^package cogwsi$/package cogwsiwriter/' "$f"
done
```

Then for the test file's import of itself:

```bash
sed -i '' 's|"github.com/cornish/wsitools/internal/cogwsi"|"github.com/cornish/wsitools/internal/tiff/cogwsiwriter"|g' internal/tiff/cogwsiwriter/*.go
```

Check `internal/tiff/cogwsiwriter/writer_test.go` (the external test file) imports `cogwsiwriter` as expected.

- [ ] **Step 3: Update convert.go import**

In `cmd/wsitools/convert.go`, change the import line and all qualified references:

```bash
sed -i '' 's|"github.com/cornish/wsitools/internal/cogwsi"|"github.com/cornish/wsitools/internal/tiff/cogwsiwriter"|g' cmd/wsitools/convert.go
sed -i '' 's/\bcogwsi\./cogwsiwriter\./g' cmd/wsitools/convert.go
```

- [ ] **Step 4: Build + test**

Run: `cd /Users/cornish/GitHub/wsitools && go build ./... && go test -race -count=1 ./internal/tiff/... ./cmd/wsitools/`

Expected: build succeeds; cogwsiwriter and convert tests pass (no test changes yet — purely path/package rename).

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/cogwsiwriter/ cmd/wsitools/convert.go
git commit -m "refactor(cogwsi): move internal/cogwsi -> internal/tiff/cogwsiwriter (rename only)"
```

---

## Task 2.2: Replace duplicate `ifdBuilder` with `internal/tiff` core

**Files:**
- Delete: `internal/tiff/cogwsiwriter/ifd.go`
- Delete: `internal/tiff/cogwsiwriter/ifd_test.go`
- Modify: `internal/tiff/cogwsiwriter/writer.go`
- Modify: `internal/tiff/cogwsiwriter/layout.go`

- [ ] **Step 1: Delete the now-redundant local entry builder**

Run:

```bash
cd /Users/cornish/GitHub/wsitools
git rm internal/tiff/cogwsiwriter/ifd.go internal/tiff/cogwsiwriter/ifd_test.go
```

- [ ] **Step 2: Update writer.go to use `tiff.EntryBuilder`**

In `internal/tiff/cogwsiwriter/writer.go`:

1. Add `"github.com/cornish/wsitools/internal/tiff"` to the import block.
2. Replace `newIFDBuilder(plan.BigTIFF)` with `tiff.NewEntryBuilder(plan.BigTIFF)` (every occurrence; should be 2–3 sites in `Close`).
3. Replace all in-package references that used the deleted symbols:
   - `b.AddTileOffsets(...)` keeps working (now a `tiff.EntryBuilder` method).
   - `b.AddLong`, `b.AddShort`, `b.AddASCII`, `b.AddBytes`, `b.AddDouble` continue to work.
4. Update `populateLevelIFD`'s parameter type from `*ifdBuilder` to `*tiff.EntryBuilder`.
5. Update `populateAssocIFD`'s parameter type similarly.

Run: `cd /Users/cornish/GitHub/wsitools && go build ./internal/tiff/cogwsiwriter/ 2>&1`

Iterate until clean.

- [ ] **Step 3: Update layout.go to use core size helpers**

In `internal/tiff/cogwsiwriter/layout.go`:

1. Add `"github.com/cornish/wsitools/internal/tiff"` to the import block (if not already there).
2. Delete the local `classicTagEntrySize`, `bigTIFFTagEntrySize`, `classicHeaderSize`, `bigTIFFHeaderSize` constants — they're now in `internal/tiff/entry.go` and `header.go`.
3. Replace `ifdRecordSize(tagCount, useBig)` calls with `uint64(tiff.IFDRecordSize(tagCount, useBig))`. (The local function may stay as a thin wrapper if many call sites; or inline-call `tiff.IFDRecordSize`. Up to implementer.)
4. Replace local `classicHeaderSize`/`bigTIFFHeaderSize` references with `tiff.HeaderSize(useBig)`.
5. Replace local entry-size references in `ifdSizeForLevel` (and any other layout helpers that depended on `classicTagEntrySize` / `bigTIFFTagEntrySize`) with `tiff.EntrySize(useBig)`.
6. Delete the local `ifdRecordSize` function (its body is now in `tiff.IFDRecordSize`).

Run: `cd /Users/cornish/GitHub/wsitools && go build ./internal/tiff/cogwsiwriter/ 2>&1`

Iterate until clean.

- [ ] **Step 4: Run cogwsiwriter tests**

Run: `cd /Users/cornish/GitHub/wsitools && go test -race -count=1 ./internal/tiff/cogwsiwriter/...`

Expected: PASS. The `ifd_test.go` is deleted; everything else carries through.

- [ ] **Step 5: Run convert integration tests**

Run: `cd /Users/cornish/GitHub/wsitools && WSI_TOOLS_TESTDIR=$PWD/sample_files go test -count=1 ./cmd/wsitools/ -run TestConvert -timeout 600s`

Expected: PASS (bit-exact tile copy against real samples; verifies the port introduces no regressions).

- [ ] **Step 6: Commit**

```bash
git add internal/tiff/cogwsiwriter/
git commit -m "refactor(cogwsiwriter): replace local ifdBuilder with tiff.EntryBuilder + tiff size helpers"
```

---

## Task 2.3: Split cogwsi tags into core + writer-local validate

**Files:**
- Modify: `internal/tiff/cogwsiwriter/tags.go`
- Create: `internal/tiff/cogwsiwriter/validate.go`
- Modify: `internal/tiff/cogwsiwriter/tags_test.go`

- [ ] **Step 1: Inspect the current tags.go**

Read `internal/tiff/cogwsiwriter/tags.go`. The file aliases `wsiwriter` tag IDs and defines 65085–65087 plus the `validAssocKinds` map + `ErrInvalidAssocKind` sentinel. All the tag constants now live in `internal/tiff/tags.go`; only the cogwsi-specific validator stays.

- [ ] **Step 2: Move validator to validate.go**

Create `internal/tiff/cogwsiwriter/validate.go`:

```go
package cogwsiwriter

import (
	"errors"
	"fmt"

	"github.com/cornish/wsitools/internal/tiff"
)

// ErrInvalidAssocKind is returned by AddAssociated when the spec's
// Kind isn't one of the four COG-WSI v0.1 §6 allowed values.
var ErrInvalidAssocKind = errors.New("invalid associated image kind")

// validAssocKinds is the COG-WSI v0.1 set of allowed WSIImageType
// values for associated-image IFDs. Stricter than the general
// tiff.ValidateWSIImageType set (which permits probability, map,
// associated as well).
var validAssocKinds = map[string]bool{
	tiff.WSIImageTypeLabel:     true,
	tiff.WSIImageTypeMacro:     true,
	tiff.WSIImageTypeThumbnail: true,
	tiff.WSIImageTypeOverview:  true,
}

// validateAssocKind returns nil if kind is one of the four allowed
// associated-image kinds; otherwise wraps ErrInvalidAssocKind.
func validateAssocKind(kind string) error {
	if !validAssocKinds[kind] {
		return fmt.Errorf("cogwsi: invalid associated kind %q (want one of label|macro|thumbnail|overview): %w", kind, ErrInvalidAssocKind)
	}
	return nil
}
```

- [ ] **Step 3: Delete the old tags.go**

```bash
cd /Users/cornish/GitHub/wsitools
git rm internal/tiff/cogwsiwriter/tags.go internal/tiff/cogwsiwriter/tags_test.go
```

- [ ] **Step 4: Update writer.go to use the new symbols**

In `internal/tiff/cogwsiwriter/writer.go`:

1. Replace references to local `TagWSIImageType`, `TagWSILevelIndex`, etc. with `tiff.TagWSIImageType`, etc.
2. Replace local `WSIImageTypePyramid` with `tiff.WSIImageTypePyramid`.
3. In `AddAssociated`, change the kind-validation logic from `if !validAssocKinds[spec.Kind] { return fmt.Errorf(... ErrInvalidAssocKind) }` to `if err := validateAssocKind(spec.Kind); err != nil { return err }`. (The validator returns a wrapped `ErrInvalidAssocKind`, preserving `errors.Is` semantics.)
4. Update any `cogwsiwriter.ErrInvalidAssocKind` references in `cmd/wsitools/convert.go` to still resolve — the sentinel kept its exported name and package path.

Run: `cd /Users/cornish/GitHub/wsitools && go build ./...`

Iterate until clean.

- [ ] **Step 5: Add a validate_test.go**

Create `internal/tiff/cogwsiwriter/validate_test.go`:

```go
package cogwsiwriter

import (
	"errors"
	"testing"
)

func TestValidateAssocKindAcceptsAllowed(t *testing.T) {
	for _, k := range []string{"label", "macro", "thumbnail", "overview"} {
		if err := validateAssocKind(k); err != nil {
			t.Errorf("validateAssocKind(%q): unexpected error %v", k, err)
		}
	}
}

func TestValidateAssocKindRejectsOther(t *testing.T) {
	for _, k := range []string{"", "pyramid", "probability", "map", "associated"} {
		err := validateAssocKind(k)
		if err == nil {
			t.Errorf("validateAssocKind(%q): expected error", k)
			continue
		}
		if !errors.Is(err, ErrInvalidAssocKind) {
			t.Errorf("validateAssocKind(%q): error should wrap ErrInvalidAssocKind, got %v", k, err)
		}
	}
}
```

- [ ] **Step 6: Run all relevant tests**

Run: `cd /Users/cornish/GitHub/wsitools && go test -race -count=1 ./internal/tiff/... ./cmd/wsitools/`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tiff/cogwsiwriter/ cmd/wsitools/convert.go
git commit -m "refactor(cogwsiwriter): split tags into tiff core + writer-local validate.go"
```

---

## Task 2.4: Verify landing 2 against golden outputs

**Files:** none modified; verification only.

- [ ] **Step 1: Re-build the CLI**

Run: `cd /Users/cornish/GitHub/wsitools && make build`

- [ ] **Step 2: Compare COG-WSI output against the v0.6.0 reference outputs**

We have ten v0.6.0 COG-WSI files committed at `~/GitHub/opentile-go/sample_files/cog-wsi/`. Convert the same sources via the post-landing-2 binary and diff:

```bash
cd /Users/cornish/GitHub/wsitools
diff_count=0
for src_base in \
  "svs/CMU-1-Small-Region.svs|CMU-1-Small-Region_cog-wsi.tiff" \
  "svs/CMU-1.svs|CMU-1_cog-wsi.tiff" \
  "svs/JP2K-33003-1.svs|JP2K-33003-1_cog-wsi.tiff" \
  "philips-tiff/Philips-1.tiff|Philips-1_cog-wsi.tiff" \
  "ome-tiff/Leica-1.ome.tiff|Leica-1_cog-wsi.tiff" \
  "bif/Ventana-1.bif|Ventana-1_cog-wsi.tiff" \
; do
  src="${src_base%|*}"
  golden="${src_base#*|}"
  tmp=$(mktemp -t convert.XXXXXX).tiff
  ./bin/wsitools convert --to cog-wsi -f -o "$tmp" ~/GitHub/opentile-go/sample_files/"$src" >/dev/null 2>&1
  if ! diff -q "$tmp" ~/GitHub/opentile-go/sample_files/cog-wsi/"$golden" >/dev/null; then
    echo "MISMATCH: $src vs $golden"
    diff_count=$((diff_count+1))
  else
    echo "MATCH: $(basename "$golden")"
  fi
  rm "$tmp"
done
echo "$diff_count mismatches"
```

Expected: every file matches; `0 mismatches`.

If any file mismatches, halt and investigate before committing. Likely root causes are off-by-one in entry-size accounting (revisit Task 2.2 Step 3) or stale references.

- [ ] **Step 3: Push (checkpoint)**

```bash
cd /Users/cornish/GitHub/wsitools
git push origin main
```

**Landing 2 acceptance:** `internal/cogwsi` no longer exists; `internal/tiff/cogwsiwriter` builds on top of `internal/tiff`; convert command unchanged in behavior; bit-exact output verified against ten v0.6.0 reference COG-WSI files.

---

# LANDING 3 — Replace `internal/wsiwriter` with `internal/tiff/streamwriter`

The riskiest landing. Golden-master hash fixtures captured pre-landing, verified post-landing.

## Task 3.1: Capture golden-master hashes from v0.6.0 transcode + downsample

**Files:**
- Create: `docs/superpowers/golden-masters-v0.6.0-transcode.txt`

- [ ] **Step 1: Confirm current binary is v0.6.0-equivalent (landing 2 just landed)**

Run: `cd /Users/cornish/GitHub/wsitools && ./bin/wsitools version`

Expected: `wsitools 0.7.0-dev`.

Note: the binary at this point includes the landing-2 changes (cogwsi → cogwsiwriter rename). Those changes affect convert only, not transcode/downsample. So transcode + downsample outputs are still v0.6.0-equivalent. We capture their hashes now, before landing 3 touches them.

- [ ] **Step 2: Capture transcode hashes**

```bash
cd /Users/cornish/GitHub/wsitools
GOLDEN_FILE=docs/superpowers/golden-masters-v0.6.0-transcode.txt
> "$GOLDEN_FILE"
echo "# v0.6.0 transcode + downsample golden-master output hashes" >> "$GOLDEN_FILE"
echo "# Captured pre-landing-3 of TIFF core extraction (commit $(git rev-parse --short HEAD))" >> "$GOLDEN_FILE"
echo "# Re-run after landing 3; all hashes must match." >> "$GOLDEN_FILE"
echo "" >> "$GOLDEN_FILE"

SAMPLES=~/GitHub/opentile-go/sample_files

# Transcode --container svs (SVS-shaped output) — JPEG sources only.
for f in "$SAMPLES/svs/CMU-1-Small-Region.svs" "$SAMPLES/svs/CMU-1.svs"; do
  tmp=$(mktemp -t svs.XXXXXX).svs
  ./bin/wsitools transcode --codec jpeg --container svs -o "$tmp" "$f" >/dev/null
  hash=$(shasum -a 256 "$tmp" | awk '{print $1}')
  echo "transcode-svs  jpeg  $(basename "$f")  sha256:$hash" >> "$GOLDEN_FILE"
  rm "$tmp"
done

# Transcode --container tiff (generic pyramidal TIFF).
for f in "$SAMPLES/svs/CMU-1-Small-Region.svs" "$SAMPLES/svs/CMU-1.svs"; do
  tmp=$(mktemp -t tiff.XXXXXX).tiff
  ./bin/wsitools transcode --codec jpeg --container tiff -o "$tmp" "$f" >/dev/null
  hash=$(shasum -a 256 "$tmp" | awk '{print $1}')
  echo "transcode-tiff  jpeg  $(basename "$f")  sha256:$hash" >> "$GOLDEN_FILE"
  rm "$tmp"
done

# Downsample (which uses wsiwriter via its own pipeline).
for f in "$SAMPLES/svs/CMU-1-Small-Region.svs"; do
  tmp=$(mktemp -t ds.XXXXXX).svs
  ./bin/wsitools downsample --factor 2 -o "$tmp" "$f" >/dev/null
  hash=$(shasum -a 256 "$tmp" | awk '{print $1}')
  echo "downsample-2x  $(basename "$f")  sha256:$hash" >> "$GOLDEN_FILE"
  rm "$tmp"
done

cat "$GOLDEN_FILE"
```

Expected output: a populated `docs/superpowers/golden-masters-v0.6.0-transcode.txt` with several sha256 lines.

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/golden-masters-v0.6.0-transcode.txt
git commit -m "test: capture v0.6.0 transcode + downsample golden-master hashes (pre-landing-3)"
```

---

## Task 3.2: Scaffold `internal/tiff/streamwriter` package

**Files:**
- Create: `internal/tiff/streamwriter/doc.go`
- Create: `internal/tiff/streamwriter/options.go`
- Create: `internal/tiff/streamwriter/writer.go`
- Create: `internal/tiff/streamwriter/writer_test.go`

- [ ] **Step 1: Smoke test**

Create `internal/tiff/streamwriter/writer_test.go`:

```go
package streamwriter_test

import (
	"testing"

	"github.com/cornish/wsitools/internal/tiff/streamwriter"
)

func TestPackageCompiles(t *testing.T) {
	var _ *streamwriter.Writer
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/streamwriter/...`

Expected: `package github.com/cornish/wsitools/internal/tiff/streamwriter: no Go files`.

- [ ] **Step 3: Create doc.go + options.go + writer stub**

Create `internal/tiff/streamwriter/doc.go`:

```go
// Package streamwriter writes WSI TIFF files using a streaming
// orchestration model: tile bytes are written inline to the output
// file as WriteTile is called, IFDs are emitted with placeholder
// offsets, then patched in place once each level is complete.
//
// Backs the wsitools transcode + downsample commands.
//
// Design spec: docs/superpowers/specs/2026-05-21-tiff-core-extraction-design.md.
package streamwriter
```

Create `internal/tiff/streamwriter/options.go`:

```go
package streamwriter

import (
	"time"

	"github.com/cornish/wsitools/internal/tiff"
)

// Options configures a new Writer.
type Options struct {
	BigTIFF tiff.BigTIFFMode

	// Standard TIFF metadata tags, emitted on L0 when set.
	ImageDescription string
	Make             string
	Model            string
	Software         string
	DateTime         time.Time

	// wsitools private tags emitted on L0 when set.
	SourceFormat string
	ToolsVersion string
}
```

Create `internal/tiff/streamwriter/writer.go`:

```go
package streamwriter

// Writer is the public handle for a streaming TIFF file under
// construction. Construct via Create.
type Writer struct {
	// fields populated in later tasks
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/streamwriter/...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/streamwriter/
git commit -m "feat(streamwriter): scaffold package + Options + Writer stub"
```

---

## Task 3.3: Port streaming writer body — Create, AddLevel, WriteTile, AddStripped, Close, Abort

**Files:**
- Modify: `internal/tiff/streamwriter/writer.go`
- Create: `internal/tiff/streamwriter/levelhandle.go`
- Create: `internal/tiff/streamwriter/stripped.go`
- Create: `internal/tiff/streamwriter/writer_test.go` (extended)

**Background:** This is the heart of landing 3. The current `internal/wsiwriter/tiff.go` is 902 lines and we're rewriting its public surface (with C-latitude) on top of `internal/tiff` primitives. The orchestration stays the same (streaming write + in-place patching); the byte emission delegates to `tiff.EntryBuilder` / `tiff.WriteHeader` / `tiff.PatchUint32/64`.

Rather than reproduce 900 lines of code in this plan, the implementer should:

1. **Open `internal/wsiwriter/tiff.go`** as the reference implementation.
2. **Open `docs/superpowers/specs/2026-05-21-tiff-core-extraction-design.md` §5** for the target public API (Options struct, LevelSpec / StrippedSpec with `ExtraTags []tiff.RawTag`, `AddStripped` instead of `AddAssociated`).
3. **Port function by function**, replacing local primitives with `tiff.*` calls.

The implementer should follow this concrete sequence of edits, verifying tests pass after each:

- [ ] **Step 1: Write the structural smoke test**

Replace the body of `internal/tiff/streamwriter/writer_test.go` with:

```go
package streamwriter_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cornish/wsitools/internal/tiff"
	"github.com/cornish/wsitools/internal/tiff/streamwriter"
)

func TestCreateAndCloseEmpty(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := streamwriter.Create(out, streamwriter.Options{
		ToolsVersion: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer w.Abort()
	// No levels, no associated images — must Close cleanly.
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("output missing after Close: %v", err)
	}
}

func TestAddLevelAndWriteTile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()

	h, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8,
		TileWidth: 8, TileHeight: 8,
		BitsPerSample:   []uint16{8, 8, 8},
		SamplesPerPixel: 3,
		Photometric:     2,
		Compression:     1,
		NewSubfileType:  0,
		WSIImageType:    tiff.WSIImageTypePyramid,
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}
	if err := h.WriteTile(0, 0, []byte("xxxxxxxx")); err != nil {
		t.Fatalf("WriteTile: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAddStripped(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	// L0 pyramid first (writer requires a base level).
	h, _ := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3, Photometric: 2, Compression: 1,
		WSIImageType: tiff.WSIImageTypePyramid,
	})
	h.WriteTile(0, 0, []byte("xxxxxxxx"))

	if err := w.AddStripped(streamwriter.StrippedSpec{
		Width: 100, Height: 100, RowsPerStrip: 100,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3, Photometric: 2,
		Compression:    1,
		StripBytes:     make([]byte, 30000),
		NewSubfileType: 1,
		WSIImageType:   tiff.WSIImageTypeLabel,
	}); err != nil {
		t.Fatalf("AddStripped: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWSIImageTypeValidation(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, _ := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	defer w.Abort()
	_, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3, Photometric: 2, Compression: 1,
		WSIImageType: "not-a-real-kind",
	})
	if err == nil {
		t.Errorf("expected validation error for bad WSIImageType")
	}
}
```

- [ ] **Step 2: Implement the writer body**

Read `internal/wsiwriter/tiff.go` thoroughly. Port its body into `internal/tiff/streamwriter/writer.go`, `levelhandle.go`, and `stripped.go`. Apply these contracts from the spec §5.1:

- **Public types** (in `writer.go` unless noted):
  - `Writer` (Create returns; opaque internals)
  - `LevelSpec` — fields per design spec §5.1; new `ExtraTags []tiff.RawTag` field; `WSIImageType` validated at AddLevel time.
  - `LevelHandle` (in `levelhandle.go`) — opaque; `WriteTile(x, y uint32, compressed []byte) error`.
  - `StrippedSpec` (in `stripped.go`) — replaces `AddAssociated`'s spec type; fields per design spec §5.1.
  - `Create(path string, opts Options) (*Writer, error)`.
  - `(*Writer).AddLevel(LevelSpec) (*LevelHandle, error)`.
  - `(*Writer).AddStripped(StrippedSpec) error`.
  - `(*Writer).Close() error`.
  - `(*Writer).Abort() error`.

- **`tiff.RawTag` type and `EntryBuilder.AddRaw` method** — already added in landing 1 Task 1.10. The writer's `Close` iterates `LevelSpec.ExtraTags` (and `StrippedSpec.ExtraTags`) and calls `b.AddRaw(rawTag)` on each.

- **Orchestration** — port from `internal/wsiwriter/tiff.go`:
  - `Create`: open `*os.File` (with `.tmp` suffix during writing, rename on success); record byteOrder=little, bigtiff per Options. Write the TIFF header via `tiff.WriteHeader` once we know the first-IFD offset (placeholder until Close patches it).
  - `AddLevel`: validate `LevelSpec.WSIImageType` via `tiff.ValidateWSIImageType`; record an internal entry pending IFD emission; return a `*LevelHandle`.
  - `WriteTile`: append the tile bytes inline to the output (track offset via a running cursor); record tile offset + length in the level handle.
  - `AddStripped`: validate `WSIImageType`; write StripBytes inline; record the strip IFD's metadata.
  - `Close`: for each level + stripped image (in source order), build a `tiff.EntryBuilder`, populate the standard tags, append `LevelSpec.ExtraTags` / `StrippedSpec.ExtraTags`, emit IFD record + external bytes to the file, patch the previous IFD's next-IFD pointer using `tiff.PatchUint32` or `PatchUint64`. Finally patch the TIFF header's first-IFD offset.
  - `Abort`: remove temp file + spool state.

Refer to `internal/wsiwriter/tiff.go`'s functions `buildTiledTags`, `emitIFD`, `writeOutOfBandValues`, `writeHeader`, `patchOffset` as the templates. The byte-emission steps inside those functions are replaced by `tiff.EntryBuilder` method calls.

Aim to keep each new file <300 LOC. Split helpers across `levelhandle.go` (LevelHandle methods + state) and `stripped.go` (StrippedSpec + helpers).

- [ ] **Step 3: Iterate until smoke tests pass**

Run: `cd /Users/cornish/GitHub/wsitools && go test -race -count=1 ./internal/tiff/streamwriter/...`

Expected: all four smoke tests PASS. Iterate as needed — this step is open-ended.

- [ ] **Step 4: Commit**

```bash
git add internal/tiff/streamwriter/ internal/tiff/entry.go internal/tiff/entry_test.go
git commit -m "feat(streamwriter): Writer body with Create/AddLevel/WriteTile/AddStripped/Close on tiff core"
```

---

## Task 3.4: Port the existing wsiwriter test suites to streamwriter

**Files:**
- Create: `internal/tiff/streamwriter/golden_test.go`
- Create: `internal/tiff/streamwriter/svs_roundtrip_test.go`

**Background:** `internal/wsiwriter/svs_roundtrip_test.go` (107 lines), `tiff_test.go` (354 lines), and `svs_test.go` (46 lines) are the strongest existing protections against output-bytes regressions. Port them to streamwriter, adapting to the new API.

- [ ] **Step 1: Port svs_roundtrip_test.go**

Read the existing file at `/Users/cornish/GitHub/wsitools/internal/wsiwriter/svs_roundtrip_test.go`. Create `internal/tiff/streamwriter/svs_roundtrip_test.go` with the same intent but adapted:

1. Package: `package streamwriter_test`.
2. Import: `"github.com/cornish/wsitools/internal/tiff/streamwriter"`.
3. Calls: replace `wsiwriter.Create(... WithFoo(x) ...)` with `streamwriter.Create(path, streamwriter.Options{Foo: x, ...})`.
4. `AddAssociated` → `AddStripped`. The spec types differ slightly; replace v0.6.0 `AssociatedSpec{Kind: ..., ...}` with `StrippedSpec{WSIImageType: ..., ...}`.
5. `WithLayout(LayoutSVS)` (if present) — replace with caller-supplied `ExtraTags`. For this test, that means assembling the Aperio-specific ImageDescription string + tags as `[]tiff.RawTag{{Tag: tiff.TagImageDescription, Type: tiff.TypeASCII, Value: "..."}, ...}` and passing through `LevelSpec.ExtraTags`.

Run: `cd /Users/cornish/GitHub/wsitools && go test ./internal/tiff/streamwriter/ -run TestSVSRoundTrip -v`

Iterate until PASS.

- [ ] **Step 2: Port tiff_test.go content**

Read `/Users/cornish/GitHub/wsitools/internal/wsiwriter/tiff_test.go`. Identify the discrete test functions inside (each `func TestXxx`). For each, port to a new test file in `internal/tiff/streamwriter/`. Group related ones; final shape suggested:

- `internal/tiff/streamwriter/golden_test.go` — basic Create/AddLevel/WriteTile/Close shape tests.
- `internal/tiff/streamwriter/extratags_test.go` — exercising `LevelSpec.ExtraTags` and `StrippedSpec.ExtraTags`.
- `internal/tiff/streamwriter/bigtiff_test.go` — BigTIFF-on, BigTIFF-off, BigTIFF-auto behaviors.

Port each, replacing calls per Step 1's rules.

Run: `cd /Users/cornish/GitHub/wsitools && go test -race -count=1 ./internal/tiff/streamwriter/...`

Iterate until PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/tiff/streamwriter/
git commit -m "test(streamwriter): port svs_roundtrip + tiff_test from wsiwriter"
```

---

## Task 3.5: Create caller-side SVS-shape helper near transcode

**Files:**
- Create: `cmd/wsitools/svs_tags.go`
- Create: `cmd/wsitools/svs_tags_test.go`

**Background:** The Aperio-faithful tag set that today is produced by `wsiwriter.WithLayout(LayoutSVS)` moves out of the writer. transcode.go assembles the tags when `--container svs` is in effect and passes them via `LevelSpec.ExtraTags` / `StrippedSpec.ExtraTags`.

- [ ] **Step 1: Inspect what wsiwriter currently emits for SVS shape**

Read `internal/wsiwriter/svs.go`. Identify the distinct SVS-specific behavior:
- Specific `ImageDescription` content format (Aperio's structured string).
- `NewSubfileType=9` for macro IFD (the Aperio-private bit 3 marker).
- Specific `NewSubfileType` placement / IFD ordering for label vs macro.

- [ ] **Step 2: Write the test**

Create `cmd/wsitools/svs_tags_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/cornish/wsitools/internal/tiff"
)

func TestBuildSVSL0ExtraTagsContainsImageDescription(t *testing.T) {
	desc := "Aperio Image Library v11.2.1\r\nMPP = 0.499"
	tags := buildSVSL0ExtraTags(desc)
	found := false
	for _, raw := range tags {
		if raw.Tag == tiff.TagImageDescription {
			if s, ok := raw.Value.(string); ok && strings.Contains(s, "Aperio Image Library") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected ImageDescription tag with Aperio prefix, got %v", tags)
	}
}

func TestSVSMacroSubfileTypeIs9(t *testing.T) {
	tags := buildSVSMacroExtraTags()
	for _, raw := range tags {
		if raw.Tag == tiff.TagNewSubfileType {
			if vs, ok := raw.Value.([]uint32); ok && len(vs) == 1 && vs[0] == 9 {
				return // pass
			}
		}
	}
	t.Errorf("expected NewSubfileType=9 on macro tags, got %v", tags)
}
```

Run: `cd /Users/cornish/GitHub/wsitools && go test ./cmd/wsitools/ -run TestBuildSVS -v` — expect compile error.

- [ ] **Step 3: Implement svs_tags.go**

Create `cmd/wsitools/svs_tags.go`:

```go
package main

import "github.com/cornish/wsitools/internal/tiff"

// buildSVSL0ExtraTags returns the Aperio-specific tag set for the L0
// pyramid IFD of an SVS-shaped transcode output. desc is the source
// Aperio ImageDescription string (preserved verbatim from the source SVS).
func buildSVSL0ExtraTags(desc string) []tiff.RawTag {
	return []tiff.RawTag{
		{Tag: tiff.TagImageDescription, Type: tiff.TypeASCII, Value: desc},
	}
}

// buildSVSMacroExtraTags returns the Aperio-specific tag set for the
// macro associated image IFD. Aperio marks macro IFDs with
// NewSubfileType=9 (bits 0+3): bit 0 is the standard "reduced
// resolution" flag; bit 3 is Aperio's private marker for "this is the
// macro image."
func buildSVSMacroExtraTags() []tiff.RawTag {
	return []tiff.RawTag{
		{Tag: tiff.TagNewSubfileType, Type: tiff.TypeLONG, Value: []uint32{9}},
	}
}

// buildSVSLabelExtraTags returns the Aperio-specific tag set for the
// label associated image IFD. Standard NewSubfileType=1.
func buildSVSLabelExtraTags() []tiff.RawTag {
	return []tiff.RawTag{
		{Tag: tiff.TagNewSubfileType, Type: tiff.TypeLONG, Value: []uint32{1}},
	}
}
```

Run: `cd /Users/cornish/GitHub/wsitools && go test ./cmd/wsitools/ -run TestBuildSVS -v`

Iterate until PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/svs_tags.go cmd/wsitools/svs_tags_test.go
git commit -m "feat(transcode): caller-side SVS-shape ExtraTags builders"
```

---

## Task 3.6: Port transcode.go to streamwriter

**Files:**
- Modify: `cmd/wsitools/transcode.go`

- [ ] **Step 1: Replace imports + types**

In `cmd/wsitools/transcode.go`:

1. Add `"github.com/cornish/wsitools/internal/tiff/streamwriter"` and `"github.com/cornish/wsitools/internal/tiff"` to imports.
2. Remove `"github.com/cornish/wsitools/internal/wsiwriter"`.

- [ ] **Step 2: Convert `wsiwriter.Option`-style Options to the Options struct**

Replace the `wOpts := []wsiwriter.Option{...}` construction with `streamwriter.Options{...}` struct literal:

```go
// OLD (delete):
// wOpts := []wsiwriter.Option{
//     wsiwriter.WithBigTIFF(bigtiff),
//     wsiwriter.WithToolsVersion(Version),
//     ...
// }
// w, err := wsiwriter.Create(tcOutput, wOpts...)

// NEW:
opts := streamwriter.Options{
    BigTIFF:      bigtiffMode,  // tiff.BigTIFFMode, not bool
    ToolsVersion: Version,
    SourceFormat: src.Format(),
}
if md.Make != "" {
    opts.Make = md.Make
}
if md.Model != "" {
    opts.Model = md.Model
}
// ... etc for Software, DateTime, ImageDescription
w, err := streamwriter.Create(tcOutput, opts)
```

Note: `bigtiff bool` (from `resolveBigTIFF`) now needs to be `tiff.BigTIFFMode`. Adjust the call site — replace `bigtiff := resolveBigTIFF(tcBigTIFF, src)` with a new helper `bigtiffMode := resolveBigTIFFMode(tcBigTIFF, src)` that returns the mode enum. Update `resolveBigTIFF` either by signature change or by adding a wrapper.

For minimum surface area: change `resolveBigTIFF` to return `tiff.BigTIFFMode`:

```go
func resolveBigTIFFMode(mode string, src source.Source) tiff.BigTIFFMode {
    switch mode {
    case "on":
        return tiff.BigTIFFOn
    case "off":
        return tiff.BigTIFFOff
    }
    // Auto: predict via source pixel count (existing logic) — if predicted > 2 GiB, return On; else Off.
    // (Alternatively: just return tiff.BigTIFFAuto and let streamwriter resolve internally;
    // current code resolves the mode eagerly using source pixel counts because the writer can't
    // see source size. Keep the eager resolution here for now.)
    var total int64
    for _, lvl := range src.Levels() {
        total += int64(lvl.Size().X) * int64(lvl.Size().Y)
    }
    if total > (2 << 30) {
        return tiff.BigTIFFOn
    }
    return tiff.BigTIFFOff
}
```

- [ ] **Step 3: Convert `wsiwriter.LevelSpec` → `streamwriter.LevelSpec`**

The v0.6.0 wsiwriter.LevelSpec and the new streamwriter.LevelSpec differ in the field name `WSIImageType` (now required, validated) and the new `ExtraTags []tiff.RawTag`. Field-by-field translation:

```go
// In transcodeLevel:
spec := streamwriter.LevelSpec{
    ImageWidth:                uint32(lvl.Size().X),
    ImageHeight:               uint32(lvl.Size().Y),
    TileWidth:                 uint32(lvl.TileSize().X),
    TileHeight:                uint32(lvl.TileSize().Y),
    Compression:               enc.TIFFCompressionTag(),
    Photometric:               2, // RGB; was PhotometricInterpretation
    SamplesPerPixel:           3,
    BitsPerSample:             []uint16{8, 8, 8},
    JPEGTables:                enc.LevelHeader(),
    NewSubfileType:            0, // L0 always 0 in streamwriter outputs; overviews 1 (set below)
    WSIImageType:              tiff.WSIImageTypePyramid,
}
if container == "svs" && isL0 {
    spec.ExtraTags = buildSVSL0ExtraTags(srcImageDescription)
}
```

For overview levels (`lvl.Index() > 0`), set `NewSubfileType: 1`. Adjust caller logic accordingly.

- [ ] **Step 4: Convert `wsiwriter.AssociatedSpec` → `streamwriter.StrippedSpec`**

In `writeAssociatedImages`, replace `wsiwriter.AssociatedSpec` with `streamwriter.StrippedSpec`. The Kind field (string) becomes the `WSIImageType` field. Compression-related field names should match the new spec. For SVS container, add the appropriate `ExtraTags`:

```go
spec := streamwriter.StrippedSpec{
    Width:           uint32(a.Size().X),
    Height:          uint32(a.Size().Y),
    RowsPerStrip:    uint32(a.Size().Y),
    BitsPerSample:   []uint16{8, 8, 8},
    SamplesPerPixel: 3,
    Photometric:     2,
    Compression:     mapCompressionForOutput(a.Compression()),
    StripBytes:      bs,
    NewSubfileType:  newSubfileTypeFor(container, a.Kind()),
    WSIImageType:    a.Kind(),
}
if container == "svs" {
    switch a.Kind() {
    case "macro", "overview":
        spec.ExtraTags = buildSVSMacroExtraTags()
    case "label":
        spec.ExtraTags = buildSVSLabelExtraTags()
    }
}
```

- [ ] **Step 5: Compression-tag constants relocation**

Replace `wsiwriter.CompressionJPEG`, `wsiwriter.CompressionLZW`, `wsiwriter.CompressionJPEG2000`, `wsiwriter.CompressionNone` references in `cmd/wsitools/transcode.go` with the equivalent `tiff.Compression*` constants (added in landing 1 Task 1.9). Mechanical find-and-replace:

```bash
cd /Users/cornish/GitHub/wsitools
sed -i '' 's/\bwsiwriter\.CompressionJPEG2000\b/tiff.CompressionJPEG2000/g' cmd/wsitools/transcode.go
sed -i '' 's/\bwsiwriter\.CompressionJPEG\b/tiff.CompressionJPEG/g' cmd/wsitools/transcode.go
sed -i '' 's/\bwsiwriter\.CompressionLZW\b/tiff.CompressionLZW/g' cmd/wsitools/transcode.go
sed -i '' 's/\bwsiwriter\.CompressionDeflate\b/tiff.CompressionDeflate/g' cmd/wsitools/transcode.go
sed -i '' 's/\bwsiwriter\.CompressionNone\b/tiff.CompressionNone/g' cmd/wsitools/transcode.go
```

Confirm no `wsiwriter.Compression*` references remain:

Run: `cd /Users/cornish/GitHub/wsitools && grep -n "wsiwriter.Compression" cmd/wsitools/transcode.go`

Expected: zero matches.

- [ ] **Step 6: Build + test**

Run: `cd /Users/cornish/GitHub/wsitools && go build ./... && go test -race -count=1 ./...`

Iterate until clean. Note: some integration tests may fail at this point because streamwriter's `Close` isn't fully wired or some edge case isn't handled. Investigate per-failure.

- [ ] **Step 7: Run transcode against a sample and visually inspect**

```bash
cd /Users/cornish/GitHub/wsitools
./bin/wsitools transcode --codec jpeg --container svs -o /tmp/sanity.svs ~/GitHub/opentile-go/sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools info /tmp/sanity.svs
```

Expected: opens cleanly via opentile-go, reports format=svs, levels match source.

- [ ] **Step 8: Commit**

```bash
git add cmd/wsitools/transcode.go internal/tiff/tags.go
git commit -m "refactor(transcode): port from wsiwriter to streamwriter + caller-side SVS tags"
```

---

## Task 3.7: Port downsample.go to streamwriter

**Files:**
- Modify: `cmd/wsitools/downsample.go`

**Background:** downsample uses wsiwriter the same way transcode does, but without SVS-shape. The port is mechanical: import + spec field renames.

- [ ] **Step 1: Inspect downsample.go**

Read `cmd/wsitools/downsample.go` to find every `wsiwriter.X` reference. The likely set is:
- `wsiwriter.Option`, `wsiwriter.With*` functional options
- `wsiwriter.LevelSpec`
- `wsiwriter.AssociatedSpec` (or none, if downsample doesn't write associated)
- `wsiwriter.Create`
- `wsiwriter.Compression*` constants

- [ ] **Step 2: Replace imports**

In `cmd/wsitools/downsample.go`:

1. Add `"github.com/cornish/wsitools/internal/tiff"` and `"github.com/cornish/wsitools/internal/tiff/streamwriter"` to the import block.
2. Remove `"github.com/cornish/wsitools/internal/wsiwriter"`.

- [ ] **Step 3: Convert `wsiwriter.Option`-style to `streamwriter.Options` struct**

Where downsample currently calls `wsiwriter.Create(path, wsiwriter.WithBigTIFF(b), wsiwriter.WithToolsVersion(v), ...)`, rewrite as:

```go
opts := streamwriter.Options{
    BigTIFF:      bigtiffMode, // tiff.BigTIFFMode, not bool
    ToolsVersion: Version,
}
if md.Make != "" {
    opts.Make = md.Make
}
// ... etc for Model, Software, DateTime, SourceFormat, ImageDescription
w, err := streamwriter.Create(downsampleOutput, opts)
```

If downsample's local `bigtiff bool` variable needs to become `tiff.BigTIFFMode`, add a small `resolveDownsampleBigTIFFMode(mode string, src source.Source) tiff.BigTIFFMode` helper (or reuse `resolveBigTIFFMode` from transcode.go if both files are in `package main` — they are).

- [ ] **Step 4: Convert `wsiwriter.LevelSpec` → `streamwriter.LevelSpec`**

Field-by-field mapping (same as transcode.go Task 3.6 Step 3):

```go
spec := streamwriter.LevelSpec{
    ImageWidth:      uint32(out.W),
    ImageHeight:     uint32(out.H),
    TileWidth:       uint32(tileW),
    TileHeight:      uint32(tileH),
    Compression:     enc.TIFFCompressionTag(),
    Photometric:     2,
    SamplesPerPixel: 3,
    BitsPerSample:   []uint16{8, 8, 8},
    JPEGTables:      enc.LevelHeader(),
    NewSubfileType:  newSubfileForLevel(lvl), // 0 for L0, 1 for overviews
    WSIImageType:    tiff.WSIImageTypePyramid,
}
```

`ExtraTags` stays unset — downsample never produces SVS-shape output.

- [ ] **Step 5: Convert `wsiwriter.AssociatedSpec` → `streamwriter.StrippedSpec` if used**

If downsample.go has `w.AddAssociated(wsiwriter.AssociatedSpec{...})` calls, rewrite as `w.AddStripped(streamwriter.StrippedSpec{...})` with the same field-rename rules from transcode.go Task 3.6 Step 4. If downsample doesn't add associated images at all, skip this step.

- [ ] **Step 6: Replace `wsiwriter.Compression*` references**

```bash
cd /Users/cornish/GitHub/wsitools
sed -i '' 's/\bwsiwriter\.CompressionJPEG2000\b/tiff.CompressionJPEG2000/g' cmd/wsitools/downsample.go
sed -i '' 's/\bwsiwriter\.CompressionJPEG\b/tiff.CompressionJPEG/g' cmd/wsitools/downsample.go
sed -i '' 's/\bwsiwriter\.CompressionLZW\b/tiff.CompressionLZW/g' cmd/wsitools/downsample.go
sed -i '' 's/\bwsiwriter\.CompressionDeflate\b/tiff.CompressionDeflate/g' cmd/wsitools/downsample.go
sed -i '' 's/\bwsiwriter\.CompressionNone\b/tiff.CompressionNone/g' cmd/wsitools/downsample.go
```

Confirm zero stale references:

Run: `cd /Users/cornish/GitHub/wsitools && grep -n "wsiwriter\." cmd/wsitools/downsample.go`

Expected: zero matches.

Downsample does NOT use SVS-shape; no caller-side ExtraTags needed for L0 / strips.

- [ ] **Step 7: Build + test**

Run: `cd /Users/cornish/GitHub/wsitools && go build ./... && go test -race -count=1 ./...`

Iterate until clean.

- [ ] **Step 8: Manual sanity check**

```bash
cd /Users/cornish/GitHub/wsitools
./bin/wsitools downsample --factor 2 -o /tmp/ds.svs ~/GitHub/opentile-go/sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools info /tmp/ds.svs
```

Expected: opens cleanly, half the resolution of source.

- [ ] **Step 9: Commit**

```bash
git add cmd/wsitools/downsample.go
git commit -m "refactor(downsample): port from wsiwriter to streamwriter"
```

---

## Task 3.8: Delete `internal/wsiwriter/`

**Files:**
- Delete: `internal/wsiwriter/` (entire directory)

- [ ] **Step 1: Verify no remaining references**

Run: `cd /Users/cornish/GitHub/wsitools && grep -rn "wsiwriter" --include="*.go" .`

Expected: zero matches. If any remain, fix them before proceeding.

- [ ] **Step 2: Remove the package**

Run: `cd /Users/cornish/GitHub/wsitools && git rm -r internal/wsiwriter/`

- [ ] **Step 3: Build + test**

Run: `cd /Users/cornish/GitHub/wsitools && go build ./... && go test -race -count=1 ./...`

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git commit -m "refactor: remove internal/wsiwriter (replaced by internal/tiff/streamwriter)"
```

---

## Task 3.9: Golden-master verification

**Files:** none modified; verification only.

- [ ] **Step 1: Recapture hashes with the post-landing-3 binary**

```bash
cd /Users/cornish/GitHub/wsitools
make build  # rebuild with all landing 3 changes

NEW_HASHES=/tmp/new-hashes.txt
> "$NEW_HASHES"

SAMPLES=~/GitHub/opentile-go/sample_files
for f in "$SAMPLES/svs/CMU-1-Small-Region.svs" "$SAMPLES/svs/CMU-1.svs"; do
  tmp=$(mktemp -t svs.XXXXXX).svs
  ./bin/wsitools transcode --codec jpeg --container svs -o "$tmp" "$f" >/dev/null
  hash=$(shasum -a 256 "$tmp" | awk '{print $1}')
  echo "transcode-svs  jpeg  $(basename "$f")  sha256:$hash" >> "$NEW_HASHES"
  rm "$tmp"
done

for f in "$SAMPLES/svs/CMU-1-Small-Region.svs" "$SAMPLES/svs/CMU-1.svs"; do
  tmp=$(mktemp -t tiff.XXXXXX).tiff
  ./bin/wsitools transcode --codec jpeg --container tiff -o "$tmp" "$f" >/dev/null
  hash=$(shasum -a 256 "$tmp" | awk '{print $1}')
  echo "transcode-tiff  jpeg  $(basename "$f")  sha256:$hash" >> "$NEW_HASHES"
  rm "$tmp"
done

for f in "$SAMPLES/svs/CMU-1-Small-Region.svs"; do
  tmp=$(mktemp -t ds.XXXXXX).svs
  ./bin/wsitools downsample --factor 2 -o "$tmp" "$f" >/dev/null
  hash=$(shasum -a 256 "$tmp" | awk '{print $1}')
  echo "downsample-2x  $(basename "$f")  sha256:$hash" >> "$NEW_HASHES"
  rm "$tmp"
done

cat "$NEW_HASHES"
```

- [ ] **Step 2: Diff against the golden-master file**

```bash
cd /Users/cornish/GitHub/wsitools
# Strip comment lines + blanks from the golden file before diffing.
grep -v "^#" docs/superpowers/golden-masters-v0.6.0-transcode.txt | grep -v "^$" > /tmp/golden-hashes.txt
diff /tmp/golden-hashes.txt /tmp/new-hashes.txt
```

Expected: **no output** (i.e., perfectly identical).

If any line differs, halt and investigate. The most likely culprits are:
- Tag ordering changes (TIFF requires ascending; verify the new EntryBuilder sort matches the v0.6.0 wsiwriter order).
- ImageDescription content drift (verify the Aperio string is preserved verbatim).
- Off-by-one in patch offset arithmetic.

Resolve before final commit / push.

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/cornish/GitHub/wsitools && go test -race -count=1 ./...`

Expected: clean.

Run: `cd /Users/cornish/GitHub/wsitools && WSI_TOOLS_TESTDIR=$PWD/sample_files go test -count=1 ./cmd/wsitools/ -run TestConvert -timeout 600s`

Expected: clean.

- [ ] **Step 4: Push (checkpoint)**

```bash
cd /Users/cornish/GitHub/wsitools
git push origin main
```

**Landing 3 acceptance:** `internal/wsiwriter` deleted; `internal/tiff/streamwriter` is the sole streaming writer; transcode + downsample byte-identical output to v0.6.0; all tests pass.

---

## Task 3.10: CHANGELOG + release prep for v0.7.0

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Edit CHANGELOG.md**

Replace the `## [Unreleased]` heading (or add one) with the v0.7.0 entry, before the `## [0.6.0]` section:

```markdown
## [0.7.0] — YYYY-MM-DD

### Changed (internal)

- Refactor: extracted TIFF byte-emission primitives into a new
  `internal/tiff` package. The streaming writer that backs
  `transcode` and `downsample` moves to `internal/tiff/streamwriter`,
  and the COG-WSI writer that backs `convert` moves to
  `internal/tiff/cogwsiwriter`. Both consume the shared core.
- No user-visible behavior change: output of all three commands
  (`transcode`, `downsample`, `convert`) is byte-identical to v0.6.0
  for the same inputs and flags.
- New `tiff.RawTag` type supports caller-supplied tag entries; the
  Aperio SVS-shape tag set moves from the writer to a caller-side
  helper (`cmd/wsitools/svs_tags.go`).
```

Set YYYY-MM-DD to today's date when committing.

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: CHANGELOG entry for v0.7.0 TIFF core extraction"
```

(Release version-tag bump comes in a separate `release: bump Version to 0.7.0` commit after final verification, mirroring the v0.6.0 pattern. Not part of this plan — happens as standalone release housekeeping when you're ready.)

---

# Spec Coverage Self-Review

| Spec section | Requirement | Task(s) |
|---|---|---|
| §1 Goal | Extract shared core | All landing 1 tasks |
| §2.1 In scope | New `internal/tiff` | 1.1–1.10 |
| §2.1 In scope | New streamwriter | 3.2–3.4 |
| §2.1 In scope | New cogwsiwriter | 2.1–2.3 |
| §2.1 In scope | transcode + downsample updates | 3.6–3.7 |
| §2.1 In scope | SVS-shape tags caller-side | 3.5 |
| §2.1 In scope | Golden-master verification | 3.1 + 3.9 |
| §3.1 Package layout | `internal/tiff` + 2 sub-packages | 1.1, 2.1, 3.2 |
| §4 Core surface | All files per §4.1 | 1.1–1.10 |
| §4.2 LE-only | No `bo` parameter | 1.5 (entry.go), 1.4 (header.go) |
| §4.1 RawTag + AddRaw | Carrier type for ExtraTags | 1.10 |
| §5.2 Compression constants | `tiff.Compression*` | 1.9 |
| §5.1 streamwriter surface | Options/Spec types | 3.2, 3.3 |
| §5.2 API changes | AddAssociated → AddStripped | 3.3, 3.6 |
| §5.2 API changes | ExtraTags | 3.3, 3.6, 3.7 |
| §5.2 API changes | Options struct (not WithXxx) | 3.2, 3.6 |
| §5.4 SVS-shape relocation | Caller-side helpers | 3.5 |
| §6.2 cogwsiwriter file reorganization | tags split, ifd deleted | 2.2, 2.3 |
| §7 Migration sequence | Three landings | Tasks grouped by L1/L2/L3 |
| §8.3 Integration safety nets | Golden master | 3.1, 3.9 |
| §10 Release & rollout | CHANGELOG v0.7.0 | 3.10 |

No gaps identified.
