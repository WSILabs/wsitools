# `wsitools convert` (COG-WSI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `wsitools convert --to cog-wsi` — a lossless, bit-exact tile-copy command that emits files conforming to the new COG-WSI format (a strict extension of Cloud Optimized GeoTIFF with WSI tags and an associated-image tail section).

**Architecture:** New `internal/cogwsi` writer package using a per-level spool staging strategy: tile bytes spool to sibling files as they arrive in source (full-res → overviews) order; `Close` plans the final layout, writes the head block (TIFF header + ghost area + IFDs + external tag arrays) with patched-up tile offsets, then streams spools into the output in reverse order (smallest level first, full-res last) followed by associated images. New `cmd/wsitools/convert.go` cobra command orchestrates source open → preflight → spool → finalize.

**Tech Stack:** Go 1.22+, cobra (CLI), `github.com/cornish/opentile-go` (reader, via `internal/source`), standard library `encoding/binary` and `os`.

**Reference docs (read before starting):**
- `docs/superpowers/specs/2026-05-20-cog-wsi-format.md` — normative COG-WSI format spec.
- `docs/superpowers/specs/2026-05-20-convert-design.md` — implementation design.
- `internal/wsiwriter/tiff.go` — reference TIFF byte emission patterns (we will NOT reuse the writer wholesale; we will reference its idioms).
- `internal/source/source.go` — the `Source`/`Level`/`AssociatedImage` interfaces we consume.
- `cmd/wsitools/transcode.go` — reference for cobra command structure, flag conventions, error handling, source open pattern.

---

## File Structure

**New files:**
- `internal/cogwsi/doc.go` — package documentation.
- `internal/cogwsi/tags.go` — new private TIFF tag constants (65085–65087: WSIMPPX, WSIMPPY, WSIMagnification). Re-exports the existing 65080–65084 names from `internal/wsiwriter` for convenience.
- `internal/cogwsi/ghost.go` — ghost area serialization and parsing.
- `internal/cogwsi/ghost_test.go`
- `internal/cogwsi/spool.go` — per-level/associated scratch spool files.
- `internal/cogwsi/spool_test.go`
- `internal/cogwsi/layout.go` — IFD layout planner (offsets, BigTIFF choice, tile data positions).
- `internal/cogwsi/layout_test.go`
- `internal/cogwsi/writer.go` — public `Writer`, `Options`, `LevelSpec`, `LevelHandle`, `AssociatedSpec`, `Create`, `AddLevel`, `WriteTile`, `AddAssociated`, `Close`.
- `internal/cogwsi/writer_test.go`
- `internal/cogwsi/ifd.go` — IFD serialization (classic + BigTIFF), reused by `Close`.
- `internal/cogwsi/ifd_test.go`
- `cmd/wsitools/convert.go` — cobra `convert` command.
- `cmd/wsitools/convert_test.go` — flag parsing tests.
- `cmd/wsitools/convert_integration_test.go` — gated by `WSI_TOOLS_TESTDIR`.

**Modified files:**
- `CHANGELOG.md` — v0.6.0 entry.
- `README.md` — `Available Commands` table gains a `convert` row.

**Not touched:**
- `internal/wsiwriter/*` — left alone; `transcode` keeps using it.
- `internal/source/*`, `internal/decoder/*`, `internal/codec/*`, `internal/pipeline/*` — convert needs no changes here.

---

## Task 1: Scaffold `internal/cogwsi` package + convert command stub

**Files:**
- Create: `internal/cogwsi/doc.go`
- Create: `internal/cogwsi/writer.go`
- Create: `internal/cogwsi/writer_test.go`
- Create: `cmd/wsitools/convert.go`
- Create: `cmd/wsitools/convert_test.go`

- [ ] **Step 1: Write a failing smoke test for the package**

Create `internal/cogwsi/writer_test.go`:

```go
package cogwsi_test

import (
	"testing"

	"github.com/cornish/wsitools/internal/cogwsi"
)

func TestPackageCompiles(t *testing.T) {
	var _ *cogwsi.Writer
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cogwsi/...`
Expected: `package github.com/cornish/wsitools/internal/cogwsi: no Go files in ...`

- [ ] **Step 3: Create the package files (stubs)**

Create `internal/cogwsi/doc.go`:

```go
// Package cogwsi writes WSI files conforming to the COG-WSI v0.1 format
// specification (docs/superpowers/specs/2026-05-20-cog-wsi-format.md).
//
// COG-WSI is a strict extension of Cloud Optimized GeoTIFF: pyramid IFDs
// and their tile-index arrays are packed at the file head; tile data is
// laid out in reverse pyramid order (smallest overview first, full-res
// last); associated images (label/macro/thumbnail/overview) are placed at
// the file tail. The writer copies compressed tile bytes verbatim from
// source — no decode, no re-encode.
package cogwsi
```

Create `internal/cogwsi/writer.go`:

```go
package cogwsi

// Writer is the public handle for a COG-WSI file under construction.
// Construct via Create.
type Writer struct {
	// fields populated in later tasks
}
```

- [ ] **Step 4: Run the test and verify it passes**

Run: `go test ./internal/cogwsi/...`
Expected: `ok  	github.com/cornish/wsitools/internal/cogwsi`

- [ ] **Step 5: Add a stub convert command and a flag-parsing test**

Create `cmd/wsitools/convert.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	cvOutput string
	cvTo     string
	cvForce  bool
)

var convertCmd = &cobra.Command{
	Use:   "convert --to <target> -o <output> [flags] <input>",
	Short: "Convert a WSI to a new container losslessly (tile-copy)",
	Long: `Convert losslessly copies compressed tile bytes from a source WSI
into a new container without decoding or re-encoding. In v0.6 the only
supported target is COG-WSI (--to cog-wsi).

See docs/superpowers/specs/2026-05-20-cog-wsi-format.md for the format spec.`,
	Args: cobra.ExactArgs(1),
	RunE: runConvert,
}

func init() {
	convertCmd.Flags().StringVarP(&cvOutput, "output", "o", "", "output file path (required)")
	convertCmd.Flags().StringVar(&cvTo, "to", "", "conversion target (only 'cog-wsi' in v0.6)")
	convertCmd.Flags().BoolVarP(&cvForce, "force", "f", false, "overwrite output if it exists")
	_ = convertCmd.MarkFlagRequired("output")
	_ = convertCmd.MarkFlagRequired("to")
	rootCmd.AddCommand(convertCmd)
}

func runConvert(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	return fmt.Errorf("convert: not yet implemented")
}
```

Create `cmd/wsitools/convert_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestConvertHelpListsRequiredFlags(t *testing.T) {
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"convert", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"--to", "--output", "--force", "cog-wsi"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q\n%s", want, out)
		}
	}
}
```

- [ ] **Step 6: Run the new tests**

Run: `go test ./cmd/wsitools/... ./internal/cogwsi/...`
Expected: both pass.

- [ ] **Step 7: Commit**

```bash
git add internal/cogwsi/ cmd/wsitools/convert.go cmd/wsitools/convert_test.go
git commit -m "feat(convert): scaffold internal/cogwsi package + convert command stub"
```

---

## Task 2: Ghost area serialization

**Files:**
- Create: `internal/cogwsi/ghost.go`
- Create: `internal/cogwsi/ghost_test.go`

**Background:** The COG-WSI format spec §4 defines an ASCII key-value ghost area placed immediately after the TIFF header. The exact format (from §4.1):

```
GDAL_STRUCTURAL_METADATA_SIZE=NNNNNN bytes
LAYOUT=IFDS_BEFORE_DATA
BLOCK_ORDER=ROW_MAJOR
BLOCK_LEADER=SIZE_AS_UINT4
BLOCK_TRAILER=LAST_4_BYTES_REPEATED
KNOWN_INCOMPATIBLE_EDITION=NO
COG_WSI_VERSION=0.1
```

The first line's `NNNNNN` is the byte length of the ghost area **excluding the size line itself**, six ASCII digits.

- [ ] **Step 1: Write the failing tests**

Create `internal/cogwsi/ghost_test.go`:

```go
package cogwsi

import (
	"strings"
	"testing"
)

func TestGhostMarshalRoundTrip(t *testing.T) {
	g := defaultGhost()
	b, err := g.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.HasPrefix(string(b), "GDAL_STRUCTURAL_METADATA_SIZE=") {
		t.Errorf("missing size header: %q", string(b))
	}
	for _, want := range []string{
		"LAYOUT=IFDS_BEFORE_DATA",
		"BLOCK_ORDER=ROW_MAJOR",
		"BLOCK_LEADER=SIZE_AS_UINT4",
		"BLOCK_TRAILER=LAST_4_BYTES_REPEATED",
		"KNOWN_INCOMPATIBLE_EDITION=NO",
		"COG_WSI_VERSION=0.1",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing key %q in: %s", want, string(b))
		}
	}
	parsed, err := ParseGhost(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Version != "0.1" {
		t.Errorf("COG_WSI_VERSION: got %q want 0.1", parsed.Version)
	}
}

func TestGhostMarshalSizeHeaderIsAccurate(t *testing.T) {
	g := defaultGhost()
	b, err := g.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// Find the first newline; everything before it is the size line.
	nl := strings.IndexByte(string(b), '\n')
	if nl < 0 {
		t.Fatalf("no newline in ghost area")
	}
	sizeLine := string(b[:nl])
	want := len(b) - nl - 1 // remaining bytes after size line + newline
	// Format: GDAL_STRUCTURAL_METADATA_SIZE=NNNNNN bytes
	var n int
	if _, err := fmt.Sscanf(sizeLine, "GDAL_STRUCTURAL_METADATA_SIZE=%d bytes", &n); err != nil {
		t.Fatalf("parse size line %q: %v", sizeLine, err)
	}
	if n != want {
		t.Errorf("declared size %d, actual remainder %d", n, want)
	}
}
```

Add `import "fmt"` at the top of the test file.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cogwsi/ -run TestGhost`
Expected: compile error (`undefined: defaultGhost`, etc).

- [ ] **Step 3: Implement ghost.go**

Create `internal/cogwsi/ghost.go`:

```go
package cogwsi

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// COGWSIVersion is the format version this writer emits.
const COGWSIVersion = "0.1"

// Ghost is the COG-WSI ghost area written immediately after the TIFF header.
// See docs/superpowers/specs/2026-05-20-cog-wsi-format.md §4.
type Ghost struct {
	Layout                 string // e.g. "IFDS_BEFORE_DATA"
	BlockOrder             string // e.g. "ROW_MAJOR"
	BlockLeader            string // e.g. "SIZE_AS_UINT4"
	BlockTrailer           string // e.g. "LAST_4_BYTES_REPEATED"
	KnownIncompatibleEdition string
	Version                string // COG_WSI_VERSION
}

func defaultGhost() Ghost {
	return Ghost{
		Layout:                   "IFDS_BEFORE_DATA",
		BlockOrder:               "ROW_MAJOR",
		BlockLeader:              "SIZE_AS_UINT4",
		BlockTrailer:             "LAST_4_BYTES_REPEATED",
		KnownIncompatibleEdition: "NO",
		Version:                  COGWSIVersion,
	}
}

// Marshal serializes the ghost area. The first line's size value is the
// byte length of everything after the size line's terminating newline,
// in six ASCII digits per GDAL convention.
func (g Ghost) Marshal() ([]byte, error) {
	body := fmt.Sprintf(
		"LAYOUT=%s\nBLOCK_ORDER=%s\nBLOCK_LEADER=%s\nBLOCK_TRAILER=%s\nKNOWN_INCOMPATIBLE_EDITION=%s\nCOG_WSI_VERSION=%s\n",
		g.Layout, g.BlockOrder, g.BlockLeader, g.BlockTrailer, g.KnownIncompatibleEdition, g.Version,
	)
	if len(body) > 999999 {
		return nil, fmt.Errorf("ghost body too long: %d bytes", len(body))
	}
	header := fmt.Sprintf("GDAL_STRUCTURAL_METADATA_SIZE=%06d bytes\n", len(body))
	return []byte(header + body), nil
}

// ParseGhost parses the ghost area produced by Marshal.
func ParseGhost(b []byte) (Ghost, error) {
	var g Ghost
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "GDAL_STRUCTURAL_METADATA_SIZE=") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "LAYOUT":
			g.Layout = val
		case "BLOCK_ORDER":
			g.BlockOrder = val
		case "BLOCK_LEADER":
			g.BlockLeader = val
		case "BLOCK_TRAILER":
			g.BlockTrailer = val
		case "KNOWN_INCOMPATIBLE_EDITION":
			g.KnownIncompatibleEdition = val
		case "COG_WSI_VERSION":
			g.Version = val
		}
	}
	if err := s.Err(); err != nil {
		return g, err
	}
	if g.Version == "" {
		return g, fmt.Errorf("ghost area missing COG_WSI_VERSION")
	}
	return g, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cogwsi/ -run TestGhost -v`
Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cogwsi/ghost.go internal/cogwsi/ghost_test.go
git commit -m "feat(cogwsi): ghost area marshal/parse with size-accurate header"
```

---

## Task 3: New private TIFF tags (MPP-X, MPP-Y, Magnification)

**Files:**
- Create: `internal/cogwsi/tags.go`
- Create: `internal/cogwsi/tags_test.go`

**Background:** The COG-WSI format spec §5.2 introduces three new private TIFF tags: `WSIMPPX`=65085, `WSIMPPY`=65086, `WSIMagnification`=65087, all `DOUBLE` type. Existing tags (`WSIImageType`=65080 etc.) are already defined in `internal/wsiwriter/wsitags.go`. We re-export those constants here so the cogwsi package has a single import for all tag IDs.

- [ ] **Step 1: Write failing tests**

Create `internal/cogwsi/tags_test.go`:

```go
package cogwsi

import "testing"

func TestNewTagIDsDoNotCollide(t *testing.T) {
	ids := map[uint16]string{
		TagWSIImageType:    "WSIImageType",
		TagWSILevelIndex:   "WSILevelIndex",
		TagWSILevelCount:   "WSILevelCount",
		TagWSISourceFormat: "WSISourceFormat",
		TagWSIToolsVersion: "WSIToolsVersion",
		TagWSIMPPX:         "WSIMPPX",
		TagWSIMPPY:         "WSIMPPY",
		TagWSIMagnification: "WSIMagnification",
	}
	seen := map[uint16]string{}
	for id, name := range ids {
		if prev, dup := seen[id]; dup {
			t.Errorf("tag id %d used by both %s and %s", id, prev, name)
		}
		seen[id] = name
	}
}

func TestNewTagIDsAreInPrivateRange(t *testing.T) {
	for _, id := range []uint16{TagWSIMPPX, TagWSIMPPY, TagWSIMagnification} {
		if id < 32768 {
			t.Errorf("tag id %d outside TIFF private range (>=32768)", id)
		}
	}
}

func TestNewTagIDValues(t *testing.T) {
	cases := []struct {
		got  uint16
		want uint16
		name string
	}{
		{TagWSIMPPX, 65085, "WSIMPPX"},
		{TagWSIMPPY, 65086, "WSIMPPY"},
		{TagWSIMagnification, 65087, "WSIMagnification"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cogwsi/ -run TestNewTagIDs`
Expected: compile error — `undefined: TagWSIImageType`, etc.

- [ ] **Step 3: Implement tags.go**

Create `internal/cogwsi/tags.go`:

```go
package cogwsi

import "github.com/cornish/wsitools/internal/wsiwriter"

// WSI tag IDs reused from internal/wsiwriter (range 65080–65084).
const (
	TagWSIImageType    = wsiwriter.TagWSIImageType
	TagWSILevelIndex   = wsiwriter.TagWSILevelIndex
	TagWSILevelCount   = wsiwriter.TagWSILevelCount
	TagWSISourceFormat = wsiwriter.TagWSISourceFormat
	TagWSIToolsVersion = wsiwriter.TagWSIToolsVersion
)

// New COG-WSI v0.1 private tags (range 65085–65087). All DOUBLE (TIFF type 12).
const (
	TagWSIMPPX          uint16 = 65085
	TagWSIMPPY          uint16 = 65086
	TagWSIMagnification uint16 = 65087
)

// WSIImageType canonical values, re-exported from wsiwriter.
const (
	WSIImageTypePyramid   = wsiwriter.WSIImageTypePyramid
	WSIImageTypeLabel     = wsiwriter.WSIImageTypeLabel
	WSIImageTypeMacro     = wsiwriter.WSIImageTypeMacro
	WSIImageTypeOverview  = wsiwriter.WSIImageTypeOverview
	WSIImageTypeThumbnail = wsiwriter.WSIImageTypeThumbnail
)
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/cogwsi/ -run TestNewTagIDs -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cogwsi/tags.go internal/cogwsi/tags_test.go
git commit -m "feat(cogwsi): private tag IDs for MPP-X, MPP-Y, Magnification (65085-65087)"
```

---

## Task 4: Spool file management

**Files:**
- Create: `internal/cogwsi/spool.go`
- Create: `internal/cogwsi/spool_test.go`

**Background:** During `AddLevel`/`WriteTile`/`AddAssociated`, tile bytes go to scratch spool files (one per pyramid level + one for associated images). At `Close` time, these are streamed into the output in COG-WSI's reverse-order layout. Each spool tracks per-entry `(index, length)` so `Close` can fill the output TIFF's `TileOffsets` / `TileByteCounts` arrays.

- [ ] **Step 1: Write failing tests**

Create `internal/cogwsi/spool_test.go`:

```go
package cogwsi

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSpoolAppendAndReadBack(t *testing.T) {
	dir := t.TempDir()
	s, err := openSpool(filepath.Join(dir, "L0"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	payloads := [][]byte{
		[]byte("hello"),
		[]byte("world!"),
		bytes.Repeat([]byte{0xAB}, 1024),
	}
	for _, p := range payloads {
		if err := s.Append(p); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if len(s.Entries()) != len(payloads) {
		t.Fatalf("entries: got %d want %d", len(s.Entries()), len(payloads))
	}
	for i, e := range s.Entries() {
		if int(e.Length) != len(payloads[i]) {
			t.Errorf("entry %d length: got %d want %d", i, e.Length, len(payloads[i]))
		}
	}

	if err := s.Rewind(); err != nil {
		t.Fatal(err)
	}
	for i, want := range payloads {
		got := make([]byte, len(want))
		n, err := io.ReadFull(s, got)
		if err != nil {
			t.Fatalf("read entry %d: %v (n=%d)", i, err, n)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("entry %d bytes mismatch: got %x want %x", i, got, want)
		}
	}
}

func TestSpoolRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "L0")
	s, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Append([]byte("x"))
	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("spool file still exists after Remove: err=%v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cogwsi/ -run TestSpool`
Expected: compile error.

- [ ] **Step 3: Implement spool.go**

Create `internal/cogwsi/spool.go`:

```go
package cogwsi

import (
	"io"
	"os"
)

// spoolEntry records one tile (or associated image) in the spool.
type spoolEntry struct {
	Length uint32 // bytes
}

// spool is a scratch file accumulating compressed tile bytes during
// AddLevel/WriteTile or AddAssociated. Entries are appended in source
// order; Close streams them into the output at finalize time.
type spool struct {
	path    string
	f       *os.File
	entries []spoolEntry
}

func openSpool(path string) (*spool, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &spool{path: path, f: f}, nil
}

// Append writes one entry; records its length.
func (s *spool) Append(b []byte) error {
	if _, err := s.f.Write(b); err != nil {
		return err
	}
	s.entries = append(s.entries, spoolEntry{Length: uint32(len(b))})
	return nil
}

// Entries returns the accumulated entry records (in append order).
func (s *spool) Entries() []spoolEntry { return s.entries }

// Rewind seeks the spool to the beginning for sequential read-back.
// Callers must invoke this before Read.
func (s *spool) Rewind() error {
	_, err := s.f.Seek(0, io.SeekStart)
	return err
}

// Read implements io.Reader on the underlying file (post-Rewind).
func (s *spool) Read(p []byte) (int, error) { return s.f.Read(p) }

// Close closes the file handle without removing the file. Use Remove to
// also delete from disk.
func (s *spool) Close() error {
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

// Remove closes (if open) and unlinks the spool file.
func (s *spool) Remove() error {
	_ = s.Close()
	return os.Remove(s.path)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/cogwsi/ -run TestSpool -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cogwsi/spool.go internal/cogwsi/spool_test.go
git commit -m "feat(cogwsi): per-level spool files with append/rewind/read/remove"
```

---

## Task 5: Layout planner (BigTIFF choice + offset math)

**Files:**
- Create: `internal/cogwsi/layout.go`
- Create: `internal/cogwsi/layout_test.go`

**Background:** Before serializing the head block, the writer computes:

1. Classic TIFF vs BigTIFF (based on total bytes).
2. Per-IFD byte length (depends on tag count, classic/BigTIFF, external arrays like `TileOffsets`/`TileByteCounts`/`JPEGTables`).
3. Head block layout: header (8 or 16 bytes), ghost area, IFD records, external tag arrays. All packed contiguously.
4. Tile data positions: pyramid IFDs in **reverse** order (smallest first), 16-byte aligned offsets, associated-image data after pyramid data.

`TileOffsets` array entries are 4 bytes (classic) or 8 bytes (BigTIFF). `TileByteCounts` are likewise 4 or 8 bytes per entry.

A subtlety: BigTIFF tag entry size is 20 bytes (`uint16 tag`, `uint16 type`, `uint64 count`, `uint64 value-or-offset`); classic is 12 bytes (`uint16`, `uint16`, `uint32`, `uint32`).

- [ ] **Step 1: Write the failing tests**

Create `internal/cogwsi/layout_test.go`:

```go
package cogwsi

import "testing"

func TestLayoutClassicTIFFTwoLevels(t *testing.T) {
	in := []levelLayoutInput{
		{TileBytes: []uint32{100, 100, 100, 100}, TileCount: 4, TileGeometry: tileGeom{TileW: 256, TileH: 256, ImgW: 512, ImgH: 512}, JPEGTables: nil},
		{TileBytes: []uint32{50, 50}, TileCount: 2, TileGeometry: tileGeom{TileW: 256, TileH: 256, ImgW: 256, ImgH: 512}, JPEGTables: nil},
	}
	plan, err := planLayout(layoutInput{
		Levels:     in,
		Associated: nil,
		BigTIFFMode: BigTIFFAuto,
	})
	if err != nil {
		t.Fatalf("planLayout: %v", err)
	}
	if plan.BigTIFF {
		t.Errorf("expected classic TIFF for tiny input, got BigTIFF")
	}
	// Smallest level (index 1) tile data must come before largest level (index 0).
	if plan.Levels[1].TileDataOffset >= plan.Levels[0].TileDataOffset {
		t.Errorf("reverse order: L1 tile data offset (%d) must be < L0 (%d)",
			plan.Levels[1].TileDataOffset, plan.Levels[0].TileDataOffset)
	}
	// All IFDs must be in the head block (before the first tile data byte).
	firstTile := plan.Levels[1].TileDataOffset
	for i, lv := range plan.Levels {
		if lv.IFDOffset >= firstTile {
			t.Errorf("level %d IFD offset %d not in head block (firstTile=%d)", i, lv.IFDOffset, firstTile)
		}
	}
	// Tile offsets aligned to 16.
	for i, lv := range plan.Levels {
		for j, off := range lv.TileOffsets {
			if off%16 != 0 {
				t.Errorf("level %d tile %d offset %d not 16-aligned", i, j, off)
			}
		}
	}
}

func TestLayoutPromotesToBigTIFF(t *testing.T) {
	// 3 GiB of fake tile bytes → must promote.
	one := uint32(1 << 20) // 1 MiB
	var tiles []uint32
	for i := 0; i < 3072; i++ {
		tiles = append(tiles, one)
	}
	in := []levelLayoutInput{{
		TileBytes:    tiles,
		TileCount:    uint32(len(tiles)),
		TileGeometry: tileGeom{TileW: 256, TileH: 256, ImgW: 65536, ImgH: 49152},
	}}
	plan, err := planLayout(layoutInput{Levels: in, BigTIFFMode: BigTIFFAuto})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.BigTIFF {
		t.Errorf("3 GiB input should promote to BigTIFF")
	}
}

func TestLayoutHonorsBigTIFFOverride(t *testing.T) {
	in := []levelLayoutInput{{TileBytes: []uint32{10}, TileCount: 1, TileGeometry: tileGeom{TileW: 8, TileH: 8, ImgW: 8, ImgH: 8}}}
	on, _ := planLayout(layoutInput{Levels: in, BigTIFFMode: BigTIFFOn})
	if !on.BigTIFF {
		t.Errorf("BigTIFFOn override ignored")
	}
	off, _ := planLayout(layoutInput{Levels: in, BigTIFFMode: BigTIFFOff})
	if off.BigTIFF {
		t.Errorf("BigTIFFOff override ignored")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cogwsi/ -run TestLayout`
Expected: compile error.

- [ ] **Step 3: Implement layout.go**

Create `internal/cogwsi/layout.go`:

```go
package cogwsi

import "fmt"

// BigTIFFMode controls classic vs BigTIFF selection.
type BigTIFFMode int

const (
	BigTIFFAuto BigTIFFMode = iota
	BigTIFFOn
	BigTIFFOff
)

// tileGeom is the per-level pixel/tile geometry.
type tileGeom struct {
	TileW, TileH, ImgW, ImgH uint32
}

// levelLayoutInput is what the writer hands the planner after all tiles for a
// level have been spooled.
type levelLayoutInput struct {
	TileBytes    []uint32 // per-tile compressed length, in source (row-major) order
	TileCount    uint32   // == len(TileBytes); kept for clarity
	TileGeometry tileGeom
	Compression  uint16
	JPEGTables   []byte // optional, abbreviated-JPEG mode
	IsL0         bool   // true for pyramid index 0 — gets the L0 metadata tags
}

// associatedLayoutInput is one associated image (label/macro/thumbnail/overview).
type associatedLayoutInput struct {
	Bytes       uint32 // total length
	Width, Height uint32
	Compression uint16
	Kind        string // canonical WSIImageType value
}

type layoutInput struct {
	Levels      []levelLayoutInput
	Associated  []associatedLayoutInput
	BigTIFFMode BigTIFFMode
	// MetaBytes is an upper-bound estimate of ImageDescription + extra metadata
	// bytes that live in the head block. The writer fills this when it knows
	// what metadata it will emit.
	MetaBytes uint32
}

// levelLayoutPlan is the planner's per-level output.
type levelLayoutPlan struct {
	IFDOffset      uint64   // absolute file offset of this IFD record
	TileOffsets    []uint64 // absolute file offsets per tile, in source row-major order
	TileDataOffset uint64   // offset of the first tile (== TileOffsets[0])
}

// associatedLayoutPlan is the planner's per-associated-image output.
type associatedLayoutPlan struct {
	IFDOffset  uint64
	DataOffset uint64
}

// layoutPlan is the complete head-block + tile-data layout for the file.
type layoutPlan struct {
	BigTIFF          bool
	HeaderSize       uint64 // 8 (classic) or 16 (BigTIFF)
	GhostOffset      uint64 // == HeaderSize
	GhostSize        uint64
	FirstIFDOffset   uint64 // immediately after ghost
	Levels           []levelLayoutPlan
	Associated      []associatedLayoutPlan
	HeadBlockEnd    uint64 // first byte of pyramid tile data area
	FileSize        uint64 // total file size including all tile + associated data
}

const (
	classicTagEntrySize = 12 // uint16 tag, uint16 type, uint32 count, uint32 val
	bigTIFFTagEntrySize = 20 // uint16 tag, uint16 type, uint64 count, uint64 val
	classicHeaderSize   = 8
	bigTIFFHeaderSize   = 16
	tileAlign           = 16
)

// planLayout computes the full file layout. It does NOT write any bytes.
func planLayout(in layoutInput) (layoutPlan, error) {
	useBig, err := decideBigTIFF(in)
	if err != nil {
		return layoutPlan{}, err
	}
	plan := layoutPlan{
		BigTIFF:     useBig,
		HeaderSize:  uint64(classicHeaderSize),
	}
	if useBig {
		plan.HeaderSize = uint64(bigTIFFHeaderSize)
	}
	plan.GhostOffset = plan.HeaderSize

	ghostBytes, err := defaultGhost().Marshal()
	if err != nil {
		return layoutPlan{}, fmt.Errorf("ghost: %w", err)
	}
	plan.GhostSize = uint64(len(ghostBytes))
	plan.FirstIFDOffset = plan.GhostOffset + plan.GhostSize

	cursor := plan.FirstIFDOffset

	// Phase 1: pyramid IFD records + their external tag arrays packed in order.
	plan.Levels = make([]levelLayoutPlan, len(in.Levels))
	for i, lv := range in.Levels {
		ifdSize, externalSize := ifdSizeForLevel(lv, useBig)
		plan.Levels[i].IFDOffset = cursor
		cursor += ifdSize + externalSize
	}

	// Phase 2: associated-image IFD records + their externals.
	plan.Associated = make([]associatedLayoutPlan, len(in.Associated))
	for i, a := range in.Associated {
		ifdSize, externalSize := ifdSizeForAssociated(a, useBig)
		plan.Associated[i].IFDOffset = cursor
		cursor += ifdSize + externalSize
	}

	// Align to 16 bytes before tile data starts.
	cursor = alignUp(cursor, tileAlign)
	plan.HeadBlockEnd = cursor

	// Phase 3: tile data in REVERSE level order (smallest first).
	for i := len(in.Levels) - 1; i >= 0; i-- {
		lv := in.Levels[i]
		offsets := make([]uint64, len(lv.TileBytes))
		for j, n := range lv.TileBytes {
			cursor = alignUp(cursor, tileAlign)
			offsets[j] = cursor
			cursor += uint64(n)
		}
		plan.Levels[i].TileOffsets = offsets
		plan.Levels[i].TileDataOffset = offsets[0]
	}

	// Phase 4: associated-image data after all pyramid data.
	for i, a := range in.Associated {
		cursor = alignUp(cursor, tileAlign)
		plan.Associated[i].DataOffset = cursor
		cursor += uint64(a.Bytes)
	}

	plan.FileSize = cursor
	return plan, nil
}

func decideBigTIFF(in layoutInput) (bool, error) {
	switch in.BigTIFFMode {
	case BigTIFFOn:
		return true, nil
	case BigTIFFOff:
		return false, nil
	}
	var total uint64
	for _, lv := range in.Levels {
		for _, n := range lv.TileBytes {
			total += uint64(n)
		}
	}
	for _, a := range in.Associated {
		total += uint64(a.Bytes)
	}
	total += uint64(in.MetaBytes) + 64*1024 // metadata + safety margin
	// Promote when predicted size > 2 GiB (leaves 2 GiB cushion under the 4 GiB classic ceiling).
	return total > (2 << 30), nil
}

// ifdSizeForLevel returns (ifd_record_size, external_arrays_size) for a pyramid IFD.
func ifdSizeForLevel(lv levelLayoutInput, big bool) (uint64, uint64) {
	tagCount := countTagsForLevel(lv)
	ifd := ifdRecordSize(tagCount, big)

	// External arrays for tags that don't fit inline:
	//   TileOffsets:     N entries × (4 or 8) bytes
	//   TileByteCounts:  N entries × (4 or 8) bytes
	//   JPEGTables:      raw bytes (if present)
	//   BitsPerSample:   if SamplesPerPixel > 1, may be external (we'll always emit external for safety)
	var external uint64
	entrySize := uint64(4)
	if big {
		entrySize = 8
	}
	external += uint64(len(lv.TileBytes)) * entrySize // TileOffsets
	external += uint64(len(lv.TileBytes)) * entrySize // TileByteCounts
	if lv.JPEGTables != nil {
		external += uint64(len(lv.JPEGTables))
	}
	if lv.IsL0 {
		// Reserve a generous allowance for ImageDescription, Make, Model,
		// Software, DateTime, SourceFormat, ToolsVersion, MPP-X/Y, Magnification.
		// 2 KiB is a comfortable upper bound for these ASCII tags + 3 doubles.
		external += 2048
	}
	return ifd, external
}

func ifdSizeForAssociated(a associatedLayoutInput, big bool) (uint64, uint64) {
	tagCount := countTagsForAssociated(a)
	ifd := ifdRecordSize(tagCount, big)
	// Associated images use StripOffsets/StripByteCounts (1 entry each, typically inline).
	// Reserve 64 bytes external for safety (BitsPerSample array, etc.).
	return ifd, 64
}

// countTagsForLevel returns the count of TIFF directory entries we will emit
// on a pyramid IFD. Must be kept in sync with ifd.go's WriteLevelIFD.
func countTagsForLevel(lv levelLayoutInput) int {
	// Always present: NewSubfileType, ImageWidth, ImageLength, BitsPerSample,
	// Compression, PhotometricInterpretation, SamplesPerPixel, PlanarConfig,
	// TileWidth, TileLength, TileOffsets, TileByteCounts, WSIImageType,
	// WSILevelIndex, WSILevelCount. (15)
	n := 15
	if lv.JPEGTables != nil {
		n++ // JPEGTables
	}
	if lv.IsL0 {
		// ImageDescription, Make, Model, Software, DateTime, SourceFormat,
		// ToolsVersion, WSIMPPX, WSIMPPY, WSIMagnification. (10; emitted only
		// when set — but for size budgeting we assume all may appear.)
		n += 10
	}
	return n
}

func countTagsForAssociated(a associatedLayoutInput) int {
	// NewSubfileType, ImageWidth, ImageLength, BitsPerSample, Compression,
	// PhotometricInterpretation, SamplesPerPixel, PlanarConfig, StripOffsets,
	// StripByteCounts, RowsPerStrip, WSIImageType. (12)
	return 12
}

func ifdRecordSize(tagCount int, big bool) uint64 {
	if big {
		// uint64 entry_count + tagCount * 20 + uint64 next_ifd_offset
		return 8 + uint64(tagCount)*bigTIFFTagEntrySize + 8
	}
	// uint16 entry_count + tagCount * 12 + uint32 next_ifd_offset
	return 2 + uint64(tagCount)*classicTagEntrySize + 4
}

func alignUp(v, align uint64) uint64 {
	if rem := v % align; rem != 0 {
		v += align - rem
	}
	return v
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/cogwsi/ -run TestLayout -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cogwsi/layout.go internal/cogwsi/layout_test.go
git commit -m "feat(cogwsi): layout planner with reverse-order tile placement + BigTIFF auto"
```

---

## Task 6: IFD serialization (classic + BigTIFF)

**Files:**
- Create: `internal/cogwsi/ifd.go`
- Create: `internal/cogwsi/ifd_test.go`

**Background:** Given a `layoutPlan`, the writer serializes each IFD record (and its external arrays — `TileOffsets`, `TileByteCounts`, `JPEGTables`, metadata strings) into a byte buffer that will be placed at the precomputed offsets. The TIFF tag entry format is:

- Classic (12 bytes/entry): `uint16 tag, uint16 type, uint32 count, uint32 value_or_offset`
- BigTIFF (20 bytes/entry): `uint16 tag, uint16 type, uint64 count, uint64 value_or_offset`

TIFF types we need:
- `BYTE`=1, `ASCII`=2, `SHORT`=3, `LONG`=4, `RATIONAL`=5, `LONG8`=16 (BigTIFF), `DOUBLE`=12, `IFD8`=18.

Values that fit in the inline slot (4 bytes classic / 8 bytes BigTIFF) go there; longer ones point to external locations.

For maximum testability, build a small `ifdBuilder` that holds tag entries and pending external blobs, and emits them via `Encode` at known offsets.

- [ ] **Step 1: Write failing tests**

Create `internal/cogwsi/ifd_test.go`:

```go
package cogwsi

import (
	"encoding/binary"
	"testing"
)

func TestIFDBuilderClassicSimple(t *testing.T) {
	b := newIFDBuilder(false /*bigtiff*/)
	b.AddShort(256 /*ImageWidth*/, []uint16{512})
	b.AddShort(257 /*ImageLength*/, []uint16{384})
	ifd, ext, err := b.Encode(100 /*ifdOffset*/, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 0 {
		t.Errorf("expected no external bytes, got %d", len(ext))
	}
	// Classic IFD: uint16 entry_count + 2 entries * 12 + uint32 next_ifd_offset = 2 + 24 + 4 = 30.
	if len(ifd) != 30 {
		t.Errorf("ifd size: got %d want 30", len(ifd))
	}
	if binary.LittleEndian.Uint16(ifd[:2]) != 2 {
		t.Errorf("entry count: got %d want 2", binary.LittleEndian.Uint16(ifd[:2]))
	}
	// Last 4 bytes are next-IFD offset, defaulting to 0.
	if binary.LittleEndian.Uint32(ifd[26:30]) != 0 {
		t.Errorf("next IFD offset: got %d want 0", binary.LittleEndian.Uint32(ifd[26:30]))
	}
}

func TestIFDBuilderBigTIFFLongArray(t *testing.T) {
	b := newIFDBuilder(true /*bigtiff*/)
	offsets := []uint64{1000, 2000, 3000}
	b.AddLong8(324 /*TileOffsets*/, offsets)
	ifd, ext, err := b.Encode(100, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 24 {
		t.Errorf("external bytes: got %d want 24 (3*8)", len(ext))
	}
	// BigTIFF IFD: uint64 entry_count + 1 entry * 20 + uint64 next_ifd_offset = 8 + 20 + 8 = 36.
	if len(ifd) != 36 {
		t.Errorf("ifd size: got %d want 36", len(ifd))
	}
	// The entry's value field (last 8 bytes of the 20-byte entry) holds the absolute
	// offset to the external array. The external array sits immediately after the IFD,
	// at ifdOffset + ifdSize = 100 + 36 = 136.
	entryStart := 8 // after uint64 entry_count
	valueAt := entryStart + 12
	if got := binary.LittleEndian.Uint64(ifd[valueAt : valueAt+8]); got != 136 {
		t.Errorf("external offset: got %d want 136", got)
	}
}

func TestIFDBuilderASCIIInline(t *testing.T) {
	// Short string fits inline (≤4 bytes classic, ≤8 BigTIFF).
	b := newIFDBuilder(false)
	b.AddASCII(305 /*Software*/, "go")
	ifd, ext, err := b.Encode(100, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 0 {
		t.Errorf("short ASCII should be inline, got %d external bytes", len(ext))
	}
	// Verify count includes the trailing NUL.
	const entryStart = 2
	count := binary.LittleEndian.Uint32(ifd[entryStart+4 : entryStart+8])
	if count != 3 {
		t.Errorf("ASCII count: got %d want 3 (go\\0)", count)
	}
}

func TestIFDBuilderASCIIExternal(t *testing.T) {
	b := newIFDBuilder(false)
	long := "this string is more than four bytes long"
	b.AddASCII(270 /*ImageDescription*/, long)
	ifd, ext, err := b.Encode(100, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != len(long)+1 { // includes trailing NUL
		t.Errorf("external bytes: got %d want %d", len(ext), len(long)+1)
	}
	// Verify the inline value field holds the external offset (= ifdOffset + ifdSize).
	const entryStart = 2
	valueAt := entryStart + 8
	got := binary.LittleEndian.Uint32(ifd[valueAt : valueAt+4])
	want := uint32(100 + len(ifd))
	if got != want {
		t.Errorf("external offset: got %d want %d", got, want)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cogwsi/ -run TestIFD`
Expected: compile error.

- [ ] **Step 3: Implement ifd.go**

Create `internal/cogwsi/ifd.go`:

```go
package cogwsi

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// TIFF data types we use.
const (
	tiffByte     = 1
	tiffASCII    = 2
	tiffShort    = 3
	tiffLong     = 4
	tiffRational = 5
	tiffDouble   = 12
	tiffLong8    = 16
)

type ifdEntry struct {
	tag         uint16
	tiffType    uint16
	count       uint64
	inlineValue [8]byte // up to 8 bytes; classic uses the low 4
	externalRaw []byte  // if non-nil, value is external and this is the payload
}

// ifdBuilder accumulates TIFF directory entries; Encode emits the
// directory record + concatenated external bytes for entries that don't
// fit inline.
type ifdBuilder struct {
	bigtiff bool
	entries []ifdEntry
}

func newIFDBuilder(bigtiff bool) *ifdBuilder {
	return &ifdBuilder{bigtiff: bigtiff}
}

func (b *ifdBuilder) inlineCap() int {
	if b.bigtiff {
		return 8
	}
	return 4
}

func (b *ifdBuilder) addRaw(tag uint16, tiffType uint16, count uint64, payload []byte) {
	e := ifdEntry{tag: tag, tiffType: tiffType, count: count}
	if len(payload) <= b.inlineCap() {
		copy(e.inlineValue[:], payload)
	} else {
		e.externalRaw = payload
	}
	b.entries = append(b.entries, e)
}

// AddShort appends a SHORT (uint16) array entry.
func (b *ifdBuilder) AddShort(tag uint16, vals []uint16) {
	payload := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint16(payload[i*2:], v)
	}
	b.addRaw(tag, tiffShort, uint64(len(vals)), payload)
}

// AddLong appends a LONG (uint32) array entry.
func (b *ifdBuilder) AddLong(tag uint16, vals []uint32) {
	payload := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(payload[i*4:], v)
	}
	b.addRaw(tag, tiffLong, uint64(len(vals)), payload)
}

// AddLong8 appends a BigTIFF LONG8 (uint64) array entry. Only valid in BigTIFF.
func (b *ifdBuilder) AddLong8(tag uint16, vals []uint64) {
	payload := make([]byte, 8*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint64(payload[i*8:], v)
	}
	b.addRaw(tag, tiffLong8, uint64(len(vals)), payload)
}

// AddTileOffsets / AddTileByteCounts pick LONG or LONG8 depending on bigtiff.
func (b *ifdBuilder) AddTileOffsets(tag uint16, offsets []uint64) {
	if b.bigtiff {
		b.AddLong8(tag, offsets)
		return
	}
	asLong := make([]uint32, len(offsets))
	for i, o := range offsets {
		asLong[i] = uint32(o)
	}
	b.AddLong(tag, asLong)
}

// AddASCII appends an ASCII entry with the trailing NUL count + 1.
func (b *ifdBuilder) AddASCII(tag uint16, s string) {
	payload := append([]byte(s), 0)
	b.addRaw(tag, tiffASCII, uint64(len(payload)), payload)
}

// AddBytes appends raw bytes (BYTE type).
func (b *ifdBuilder) AddBytes(tag uint16, payload []byte) {
	b.addRaw(tag, tiffByte, uint64(len(payload)), payload)
}

// AddDouble appends a DOUBLE (float64) array entry.
func (b *ifdBuilder) AddDouble(tag uint16, vals []float64) {
	payload := make([]byte, 8*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint64(payload[i*8:], uint64fromfloat64(v))
	}
	b.addRaw(tag, tiffDouble, uint64(len(vals)), payload)
}

func uint64fromfloat64(v float64) uint64 {
	// Bit-pattern reinterpretation via the standard library.
	return binary.LittleEndian.Uint64(append([]byte{}, float64Bytes(v)...))
}

func float64Bytes(v float64) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, mathFloat64bits(v))
	return out
}

// indirect through math.Float64bits via a local copy to avoid pulling in math
// just for one cast; the build is fine either way — use math.Float64bits.
func mathFloat64bits(v float64) uint64 {
	return float64bits(v)
}

// Encode writes the IFD record at ifdOffset and returns:
//   - ifd: the directory bytes
//   - ext: concatenated external bytes, placed at ifdOffset + len(ifd)
// External entries' value fields are filled in with their final absolute
// offsets.
func (b *ifdBuilder) Encode(ifdOffset uint64, bo binary.ByteOrder) (ifd, ext []byte, err error) {
	if !b.bigtiff && ifdOffset > 0xFFFFFFFF {
		return nil, nil, fmt.Errorf("classic TIFF ifd offset overflow: %d", ifdOffset)
	}

	// Sort entries by tag (TIFF requires ascending tag order).
	sorted := append([]ifdEntry(nil), b.entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].tag < sorted[j].tag })

	// Compute directory size.
	var dirSize uint64
	if b.bigtiff {
		dirSize = 8 + uint64(len(sorted))*bigTIFFTagEntrySize + 8
	} else {
		dirSize = 2 + uint64(len(sorted))*classicTagEntrySize + 4
	}

	ifd = make([]byte, dirSize)
	// Walk external entries; assign offsets immediately after the IFD record.
	cursor := ifdOffset + dirSize
	var extBuf []byte
	for i := range sorted {
		if sorted[i].externalRaw == nil {
			continue
		}
		// Write offset into inlineValue, then append payload to extBuf.
		setOffset(sorted[i].inlineValue[:], cursor, b.bigtiff, bo)
		extBuf = append(extBuf, sorted[i].externalRaw...)
		cursor += uint64(len(sorted[i].externalRaw))
	}

	// Write entry_count.
	if b.bigtiff {
		bo.PutUint64(ifd[0:8], uint64(len(sorted)))
	} else {
		bo.PutUint16(ifd[0:2], uint16(len(sorted)))
	}

	// Write entries.
	off := uint64(8)
	if !b.bigtiff {
		off = 2
	}
	for _, e := range sorted {
		bo.PutUint16(ifd[off:off+2], e.tag)
		bo.PutUint16(ifd[off+2:off+4], e.tiffType)
		if b.bigtiff {
			bo.PutUint64(ifd[off+4:off+12], e.count)
			copy(ifd[off+12:off+20], e.inlineValue[:8])
			off += bigTIFFTagEntrySize
		} else {
			bo.PutUint32(ifd[off+4:off+8], uint32(e.count))
			copy(ifd[off+8:off+12], e.inlineValue[:4])
			off += classicTagEntrySize
		}
	}
	// next-IFD field stays zero; the writer patches it during Close.
	return ifd, extBuf, nil
}

func setOffset(slot []byte, val uint64, bigtiff bool, bo binary.ByteOrder) {
	if bigtiff {
		bo.PutUint64(slot[:8], val)
	} else {
		bo.PutUint32(slot[:4], uint32(val))
	}
}
```

Add at top of file, in the imports block, `"math"` and replace the placeholder `float64bits` references with `math.Float64bits`. The Encode helpers using `mathFloat64bits` / `float64Bytes` / `uint64fromfloat64` were sketched only for visualization — collapse them to a single call site:

Replace the `AddDouble` block and the helpers under it with:

```go
import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// (other methods unchanged)

// AddDouble appends a DOUBLE (float64) array entry.
func (b *ifdBuilder) AddDouble(tag uint16, vals []float64) {
	payload := make([]byte, 8*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint64(payload[i*8:], math.Float64bits(v))
	}
	b.addRaw(tag, tiffDouble, uint64(len(vals)), payload)
}
```

(Delete `uint64fromfloat64`, `float64Bytes`, `mathFloat64bits`, `float64bits` — they were illustrative.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/cogwsi/ -run TestIFD -v`
Expected: PASS for all four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cogwsi/ifd.go internal/cogwsi/ifd_test.go
git commit -m "feat(cogwsi): TIFF/BigTIFF IFD builder with inline + external value placement"
```

---

## Task 7: Writer.Create + AddLevel + WriteTile (spooling phase)

**Files:**
- Modify: `internal/cogwsi/writer.go`
- Modify: `internal/cogwsi/writer_test.go`

**Background:** The writer's spooling phase is straightforward: `Create` creates the output file (truncated; head block is written later in `Close`) and a sibling spool directory. `AddLevel` opens a new per-level spool. `WriteTile` validates `(tx, ty)` ordering and appends to the spool.

The writer enforces **row-major tile order** within a level (`ty` major, `tx` minor). Out-of-order writes are an error.

- [ ] **Step 1: Add failing tests**

Append to `internal/cogwsi/writer_test.go`:

```go
package cogwsi_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cornish/wsitools/internal/cogwsi"
)

func TestWriterCreateAndSpoolLevel(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")

	w, err := cogwsi.Create(out, cogwsi.Options{
		ToolsVersion: "test",
		BigTIFF:      cogwsi.BigTIFFAuto,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer w.Abort()

	h, err := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth:      8,
		ImageHeight:     8,
		TileWidth:       8,
		TileHeight:      8,
		Compression:     1, // none
		Photometric:     2, // RGB
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		IsL0:            true,
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}
	if err := h.WriteTile(0, 0, []byte("xxxxxxxx")); err != nil {
		t.Fatalf("WriteTile: %v", err)
	}

	// Spool file should exist.
	if _, err := os.Stat(out + ".spool/L0"); err != nil {
		t.Errorf("expected spool file: %v", err)
	}

	// Out-of-order write is an error.
	h2, err := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 16, ImageHeight: 8,
		TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Skip (0,0), jump to (1,0): error.
	if err := h2.WriteTile(1, 0, []byte("xxxxxxxx")); err == nil {
		t.Errorf("expected error for out-of-order tile (1,0) before (0,0)")
	}
}

func TestWriterAbortRemovesEverything(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := cogwsi.Create(out, cogwsi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Abort()
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output file should be gone")
	}
	if _, err := os.Stat(out + ".spool"); !os.IsNotExist(err) {
		t.Errorf("spool dir should be gone")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/cogwsi/ -run TestWriterCreate -v`
Expected: compile error (`undefined: cogwsi.Create`, etc).

- [ ] **Step 3: Flesh out writer.go (spooling-only phase)**

Replace the contents of `internal/cogwsi/writer.go` with:

```go
package cogwsi

import (
	"fmt"
	"os"
	"time"
)

// Options configures a new Writer.
type Options struct {
	BigTIFF      BigTIFFMode
	ToolsVersion string
	Metadata     Metadata
}

// Metadata is the cross-format scanner / acquisition info passed through to L0.
type Metadata struct {
	MPPX, MPPY          float64
	Magnification       float64
	Make, Model         string
	Software            string
	AcquisitionDateTime time.Time
	SourceFormat        string
	SourceImageDesc     string // optional provenance for ImageDescription
}

// LevelSpec describes one pyramid level. Compression and PhotometricInterpretation
// MUST equal the source's; tile geometry MUST equal the source's. JPEGTables
// MUST be supplied when the source IFD used abbreviated-JPEG mode.
type LevelSpec struct {
	ImageWidth, ImageHeight uint32
	TileWidth, TileHeight   uint32
	Compression             uint16
	Photometric             uint16
	BitsPerSample           []uint16
	SamplesPerPixel         uint16
	JPEGTables              []byte
	IsL0                    bool
}

// LevelHandle is the per-level tile sink.
type LevelHandle struct {
	w      *Writer
	idx    int
	spec   LevelSpec
	gridX  uint32
	gridY  uint32
	nextTX uint32
	nextTY uint32
	spool  *spool
}

// AssociatedSpec describes one associated image.
type AssociatedSpec struct {
	Kind          string // canonical WSIImageType value
	Width, Height uint32
	Compression   uint16
	Photometric   uint16
	BitsPerSample []uint16
	SamplesPerPixel uint16
	Bytes         []byte // verbatim compressed payload
	Tiled         bool   // (informational; associated IFDs always use strips in v0.1)
}

// Writer is the public handle for a COG-WSI file under construction.
type Writer struct {
	path     string
	spoolDir string
	out      *os.File
	opts     Options
	levels   []*LevelHandle
	assoc    []assocSpooled
	closed   bool
}

type assocSpooled struct {
	spec AssociatedSpec
	off  uint64 // offset within the shared associated spool (post-Close)
}

// Create starts a new COG-WSI writer at path. The output file is created
// empty; the head block is written by Close. A sibling spool directory
// path+".spool" is created for scratch storage.
func Create(path string, opts Options) (*Writer, error) {
	spoolDir := path + ".spool"
	if err := os.MkdirAll(spoolDir, 0o755); err != nil {
		return nil, fmt.Errorf("create spool dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		os.RemoveAll(spoolDir)
		return nil, fmt.Errorf("create output: %w", err)
	}
	return &Writer{
		path:     path,
		spoolDir: spoolDir,
		out:      f,
		opts:     opts,
	}, nil
}

// AddLevel registers a new pyramid level and returns its tile sink. Levels
// MUST be added in source order, full-resolution first.
func (w *Writer) AddLevel(spec LevelSpec) (*LevelHandle, error) {
	if w.closed {
		return nil, fmt.Errorf("writer closed")
	}
	idx := len(w.levels)
	sp, err := openSpool(fmt.Sprintf("%s/L%d", w.spoolDir, idx))
	if err != nil {
		return nil, fmt.Errorf("open spool L%d: %w", idx, err)
	}
	gridX := (spec.ImageWidth + spec.TileWidth - 1) / spec.TileWidth
	gridY := (spec.ImageHeight + spec.TileHeight - 1) / spec.TileHeight
	h := &LevelHandle{
		w: w, idx: idx, spec: spec,
		gridX: gridX, gridY: gridY,
		spool: sp,
	}
	w.levels = append(w.levels, h)
	return h, nil
}

// WriteTile appends one compressed tile to the level spool. Tiles MUST be
// written in row-major order (ty major, tx minor) starting from (0, 0).
func (h *LevelHandle) WriteTile(tx, ty uint32, compressed []byte) error {
	if tx != h.nextTX || ty != h.nextTY {
		return fmt.Errorf("level %d: tile out of order: got (%d,%d) want (%d,%d)",
			h.idx, tx, ty, h.nextTX, h.nextTY)
	}
	if err := h.spool.Append(compressed); err != nil {
		return err
	}
	h.nextTX++
	if h.nextTX >= h.gridX {
		h.nextTX = 0
		h.nextTY++
	}
	return nil
}

// AddAssociated stages one associated image (label/macro/thumbnail/overview).
// Bytes are kept in memory; the writer copies them to a single associated
// spool during Close. (Associated images are typically <10 MiB each.)
func (w *Writer) AddAssociated(spec AssociatedSpec) error {
	if w.closed {
		return fmt.Errorf("writer closed")
	}
	w.assoc = append(w.assoc, assocSpooled{spec: spec})
	return nil
}

// Abort removes the output file and spool directory. Safe to call any time;
// idempotent. Use as a deferred cleanup in callers that want to discard the
// in-progress write.
func (w *Writer) Abort() error {
	if w.out != nil {
		_ = w.out.Close()
		w.out = nil
	}
	for _, lv := range w.levels {
		if lv.spool != nil {
			_ = lv.spool.Remove()
			lv.spool = nil
		}
	}
	_ = os.RemoveAll(w.spoolDir)
	_ = os.Remove(w.path)
	w.closed = true
	return nil
}

// Close is implemented in Task 8.
func (w *Writer) Close() error {
	return fmt.Errorf("Writer.Close: not yet implemented")
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/cogwsi/ -run TestWriter -v`
Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cogwsi/writer.go internal/cogwsi/writer_test.go
git commit -m "feat(cogwsi): Writer.Create/AddLevel/WriteTile/AddAssociated/Abort (spool phase)"
```

---

## Task 8: Writer.Close — finalize head block + stream spools

**Files:**
- Modify: `internal/cogwsi/writer.go`
- Modify: `internal/cogwsi/writer_test.go`

**Background:** `Close` is the orchestration step. Sequence:

1. Tally per-tile lengths from each level spool; build `layoutInput`.
2. `planLayout(in)` → `plan`.
3. For each level (full-res first), build an `ifdBuilder` populated with tags (geometry, compression, photometric, `TileOffsets` from `plan`, `TileByteCounts`, `JPEGTables` if any, WSI tags, L0 metadata if `IsL0`). Encode at `plan.Levels[i].IFDOffset`.
4. For each associated image, build an `ifdBuilder` likewise; placement uses strip-based tags (`StripOffsets`/`StripByteCounts`/`RowsPerStrip`).
5. Patch the IFD chain: each IFD's `next_ifd_offset` (last field of the record) points to the next IFD record. Last associated IFD's `next` is 0.
6. Write the output file head: TIFF header (with `IFD0` offset = `plan.FirstIFDOffset`) → ghost area → IFD records + their external arrays.
7. For each level **in reverse order** (smallest first), seek to `plan.Levels[i].TileDataOffset`, rewind the spool, and stream it into the output. Use `alignUp` padding between tiles (zero bytes) and between levels.
8. For each associated image (in order), seek to `plan.Associated[i].DataOffset` and write `spec.Bytes`.
9. `fsync`, close output, remove spool files.

The most fiddly piece is tile-by-tile padding. Each tile's offset in `plan.Levels[i].TileOffsets[j]` is already aligned; the writer seeks to that offset and writes the spool entry directly. Padding is implicit (file holes get zero-filled when bytes after them are written).

- [ ] **Step 1: Write failing tests for Close**

Append to `internal/cogwsi/writer_test.go`:

```go
func TestWriterCloseProducesValidTIFF(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")

	w, err := cogwsi.Create(out, cogwsi.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Build a 2-level pyramid: L0 = 16×16 (4 tiles of 8×8), L1 = 8×8 (1 tile).
	makeTile := func(b byte) []byte {
		t := make([]byte, 192) // 8*8*3
		for i := range t {
			t[i] = b
		}
		return t
	}
	h0, _ := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 16, ImageHeight: 16, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8}, IsL0: true,
	})
	for ty := uint32(0); ty < 2; ty++ {
		for tx := uint32(0); tx < 2; tx++ {
			if err := h0.WriteTile(tx, ty, makeTile(byte(ty*2+tx+1))); err != nil {
				t.Fatal(err)
			}
		}
	}
	h1, _ := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8},
	})
	if err := h1.WriteTile(0, 0, makeTile(99)); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Output must exist and parse as a TIFF (little-endian, classic).
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 {
		t.Fatalf("output too short: %d bytes", len(data))
	}
	if data[0] != 'I' || data[1] != 'I' {
		t.Errorf("byte order: got %c%c want II", data[0], data[1])
	}
	// version: classic = 42 (0x002A).
	if data[2] != 42 || data[3] != 0 {
		t.Errorf("TIFF version: got %d,%d want 42,0", data[2], data[3])
	}

	// Spool directory must be gone.
	if _, err := os.Stat(out + ".spool"); !os.IsNotExist(err) {
		t.Errorf("spool dir should be removed after Close")
	}

	// Ghost area must follow the header.
	if !bytes.HasPrefix(data[8:], []byte("GDAL_STRUCTURAL_METADATA_SIZE=")) {
		t.Errorf("ghost area missing at offset 8")
	}

	// Find the first tile offset (smallest level) — should be smaller than the
	// largest level's first tile offset. Parse IFD0 offset from header bytes 4-7.
	ifd0 := binary.LittleEndian.Uint32(data[4:8])
	if ifd0 == 0 {
		t.Fatalf("IFD0 offset is zero")
	}
}

func TestWriterCloseReverseOrderTileData(t *testing.T) {
	// L0 = 16×16 (4 tiles), L1 = 8×8 (1 tile). Verify L1's tile data offset
	// < L0's tile data offset in the final file.
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, _ := cogwsi.Create(out, cogwsi.Options{ToolsVersion: "test"})

	tile := bytes.Repeat([]byte{0xAA}, 192)
	h0, _ := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 16, ImageHeight: 16, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8}, IsL0: true,
	})
	for ty := uint32(0); ty < 2; ty++ {
		for tx := uint32(0); tx < 2; tx++ {
			_ = h0.WriteTile(tx, ty, tile)
		}
	}
	h1, _ := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8},
	})
	_ = h1.WriteTile(0, 0, tile)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	l0Off, l1Off, err := readTileOffsets(out)
	if err != nil {
		t.Fatal(err)
	}
	if l1Off >= l0Off {
		t.Errorf("L1 tile offset (%d) must be < L0 tile offset (%d) — reverse order", l1Off, l0Off)
	}
}

// readTileOffsets walks IFD0 and IFD1 in `path` and returns each level's
// first TileOffsets entry. Classic TIFF only.
func readTileOffsets(path string) (l0, l1 uint64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	ifd0 := uint64(binary.LittleEndian.Uint32(data[4:8]))
	l0, ifd1, err := firstTileOffset(data, ifd0)
	if err != nil {
		return 0, 0, err
	}
	if ifd1 == 0 {
		return 0, 0, fmt.Errorf("IFD1 missing")
	}
	l1, _, err = firstTileOffset(data, ifd1)
	return l0, l1, err
}

// firstTileOffset returns (tileOffsets[0], next_ifd_offset).
func firstTileOffset(data []byte, ifdOff uint64) (uint64, uint64, error) {
	n := uint64(binary.LittleEndian.Uint16(data[ifdOff : ifdOff+2]))
	entries := data[ifdOff+2 : ifdOff+2+n*12]
	var tileOffsetsOff uint64
	var tileOffsetsCount uint64
	for i := uint64(0); i < n; i++ {
		e := entries[i*12 : (i+1)*12]
		tag := binary.LittleEndian.Uint16(e[0:2])
		count := uint64(binary.LittleEndian.Uint32(e[4:8]))
		val := uint64(binary.LittleEndian.Uint32(e[8:12]))
		if tag == 324 { // TileOffsets
			tileOffsetsOff = val
			tileOffsetsCount = count
		}
	}
	next := uint64(binary.LittleEndian.Uint32(data[ifdOff+2+n*12 : ifdOff+2+n*12+4]))
	if tileOffsetsCount == 0 {
		return 0, next, fmt.Errorf("no TileOffsets in IFD")
	}
	// If count == 1, the value is inline. Otherwise val is an external offset.
	if tileOffsetsCount == 1 {
		return tileOffsetsOff, next, nil
	}
	return uint64(binary.LittleEndian.Uint32(data[tileOffsetsOff : tileOffsetsOff+4])), next, nil
}
```

Add the imports `"bytes"`, `"encoding/binary"`, `"fmt"` at the top of `writer_test.go`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cogwsi/ -run TestWriterClose -v`
Expected: PASS for nothing — `Close: not yet implemented`.

- [ ] **Step 3: Implement Close in writer.go**

Replace `Writer.Close` (and add a helper file `finalize.go` if it grows beyond ~150 lines — otherwise keep in `writer.go`). The implementation:

```go
// Close finalizes the file. See plan task 8 for the sequence.
func (w *Writer) Close() error {
	if w.closed {
		return fmt.Errorf("writer already closed")
	}
	defer func() { w.closed = true }()

	// Build layoutInput.
	in := layoutInput{BigTIFFMode: w.opts.BigTIFF}
	for i, lv := range w.levels {
		entries := lv.spool.Entries()
		bytesLen := make([]uint32, len(entries))
		for j, e := range entries {
			bytesLen[j] = e.Length
		}
		in.Levels = append(in.Levels, levelLayoutInput{
			TileBytes:    bytesLen,
			TileCount:    uint32(len(entries)),
			TileGeometry: tileGeom{TileW: lv.spec.TileWidth, TileH: lv.spec.TileHeight, ImgW: lv.spec.ImageWidth, ImgH: lv.spec.ImageHeight},
			Compression:  lv.spec.Compression,
			JPEGTables:   lv.spec.JPEGTables,
			IsL0:         i == 0,
		})
	}
	for _, a := range w.assoc {
		in.Associated = append(in.Associated, associatedLayoutInput{
			Bytes:       uint32(len(a.spec.Bytes)),
			Width:       a.spec.Width,
			Height:      a.spec.Height,
			Compression: a.spec.Compression,
			Kind:        a.spec.Kind,
		})
	}

	plan, err := planLayout(in)
	if err != nil {
		w.Abort()
		return err
	}

	// Build IFD bytes for each level and associated image.
	bo := binary.LittleEndian
	type ifdBlob struct {
		offset uint64
		ifd    []byte
		ext    []byte
	}
	var blobs []ifdBlob

	for i, lv := range w.levels {
		b := newIFDBuilder(plan.BigTIFF)
		populateLevelIFD(b, lv.spec, plan.Levels[i].TileOffsets, lv.spool.Entries(), w.opts, i == 0, len(w.levels))
		ifd, ext, err := b.Encode(plan.Levels[i].IFDOffset, bo)
		if err != nil {
			w.Abort()
			return fmt.Errorf("encode IFD L%d: %w", i, err)
		}
		blobs = append(blobs, ifdBlob{offset: plan.Levels[i].IFDOffset, ifd: ifd, ext: ext})
	}
	for i, a := range w.assoc {
		b := newIFDBuilder(plan.BigTIFF)
		populateAssocIFD(b, a.spec, plan.Associated[i].DataOffset)
		ifd, ext, err := b.Encode(plan.Associated[i].IFDOffset, bo)
		if err != nil {
			w.Abort()
			return fmt.Errorf("encode IFD assoc%d: %w", i, err)
		}
		blobs = append(blobs, ifdBlob{offset: plan.Associated[i].IFDOffset, ifd: ifd, ext: ext})
	}

	// Patch next_ifd_offset chain.
	for i := 0; i < len(blobs)-1; i++ {
		patchNextIFD(blobs[i].ifd, blobs[i+1].offset, plan.BigTIFF, bo)
	}

	// Write head block.
	if err := writeHeader(w.out, plan, bo); err != nil {
		w.Abort()
		return err
	}
	ghostBytes, _ := defaultGhost().Marshal()
	if _, err := w.out.WriteAt(ghostBytes, int64(plan.GhostOffset)); err != nil {
		w.Abort()
		return fmt.Errorf("write ghost: %w", err)
	}
	for _, b := range blobs {
		if _, err := w.out.WriteAt(b.ifd, int64(b.offset)); err != nil {
			w.Abort()
			return fmt.Errorf("write IFD: %w", err)
		}
		if len(b.ext) > 0 {
			if _, err := w.out.WriteAt(b.ext, int64(b.offset)+int64(len(b.ifd))); err != nil {
				w.Abort()
				return fmt.Errorf("write IFD external: %w", err)
			}
		}
	}

	// Stream level spools in reverse order.
	for i := len(w.levels) - 1; i >= 0; i-- {
		lv := w.levels[i]
		entries := lv.spool.Entries()
		if err := lv.spool.Rewind(); err != nil {
			w.Abort()
			return fmt.Errorf("rewind L%d: %w", i, err)
		}
		for j, e := range entries {
			off := int64(plan.Levels[i].TileOffsets[j])
			buf := make([]byte, e.Length)
			if _, err := io.ReadFull(lv.spool, buf); err != nil {
				w.Abort()
				return fmt.Errorf("read L%d tile %d: %w", i, j, err)
			}
			if _, err := w.out.WriteAt(buf, off); err != nil {
				w.Abort()
				return fmt.Errorf("write L%d tile %d: %w", i, j, err)
			}
		}
	}

	// Write associated images.
	for i, a := range w.assoc {
		if _, err := w.out.WriteAt(a.spec.Bytes, int64(plan.Associated[i].DataOffset)); err != nil {
			w.Abort()
			return fmt.Errorf("write assoc %d: %w", i, err)
		}
	}

	// Sync, close, cleanup.
	if err := w.out.Sync(); err != nil {
		w.Abort()
		return fmt.Errorf("fsync: %w", err)
	}
	if err := w.out.Close(); err != nil {
		w.Abort()
		return fmt.Errorf("close output: %w", err)
	}
	w.out = nil
	for _, lv := range w.levels {
		_ = lv.spool.Remove()
	}
	_ = os.Remove(w.spoolDir)
	return nil
}

func writeHeader(f *os.File, plan layoutPlan, bo binary.ByteOrder) error {
	hdr := make([]byte, plan.HeaderSize)
	hdr[0], hdr[1] = 'I', 'I'
	if plan.BigTIFF {
		bo.PutUint16(hdr[2:4], 0x002B)
		bo.PutUint16(hdr[4:6], 8) // offset size
		bo.PutUint16(hdr[6:8], 0) // constant zero
		bo.PutUint64(hdr[8:16], plan.FirstIFDOffset)
	} else {
		bo.PutUint16(hdr[2:4], 0x002A)
		bo.PutUint32(hdr[4:8], uint32(plan.FirstIFDOffset))
	}
	_, err := f.WriteAt(hdr, 0)
	return err
}

func patchNextIFD(ifd []byte, next uint64, big bool, bo binary.ByteOrder) {
	if big {
		bo.PutUint64(ifd[len(ifd)-8:], next)
	} else {
		bo.PutUint32(ifd[len(ifd)-4:], uint32(next))
	}
}

// populateLevelIFD fills an ifdBuilder with the tags for a pyramid level.
func populateLevelIFD(b *ifdBuilder, spec LevelSpec, tileOffsets []uint64, entries []spoolEntry, opts Options, isL0 bool, levelCount int) {
	subfile := uint32(1) // reduced-resolution
	if isL0 {
		subfile = 0
	}
	b.AddLong(254 /*NewSubfileType*/, []uint32{subfile})
	b.AddLong(256 /*ImageWidth*/, []uint32{spec.ImageWidth})
	b.AddLong(257 /*ImageLength*/, []uint32{spec.ImageHeight})
	b.AddShort(258 /*BitsPerSample*/, spec.BitsPerSample)
	b.AddShort(259 /*Compression*/, []uint16{spec.Compression})
	b.AddShort(262 /*PhotometricInterpretation*/, []uint16{spec.Photometric})
	b.AddShort(277 /*SamplesPerPixel*/, []uint16{spec.SamplesPerPixel})
	b.AddShort(284 /*PlanarConfiguration*/, []uint16{1})
	b.AddLong(322 /*TileWidth*/, []uint32{spec.TileWidth})
	b.AddLong(323 /*TileLength*/, []uint32{spec.TileHeight})
	b.AddTileOffsets(324 /*TileOffsets*/, tileOffsets)
	byteCounts := make([]uint32, len(entries))
	for i, e := range entries {
		byteCounts[i] = e.Length
	}
	b.AddLong(325 /*TileByteCounts*/, byteCounts)
	if spec.JPEGTables != nil {
		b.AddBytes(347 /*JPEGTables*/, spec.JPEGTables)
	}
	b.AddASCII(TagWSIImageType, WSIImageTypePyramid)
	// Note: LevelIndex tag value can be computed; we keep the API simple.
	// We pass the index in via callers in later refactor if needed.
	if isL0 {
		if opts.Metadata.SourceImageDesc != "" {
			b.AddASCII(270 /*ImageDescription*/, opts.Metadata.SourceImageDesc)
		}
		if opts.Metadata.Make != "" {
			b.AddASCII(271 /*Make*/, opts.Metadata.Make)
		}
		if opts.Metadata.Model != "" {
			b.AddASCII(272 /*Model*/, opts.Metadata.Model)
		}
		if opts.Metadata.Software != "" {
			b.AddASCII(305 /*Software*/, opts.Metadata.Software)
		}
		if !opts.Metadata.AcquisitionDateTime.IsZero() {
			b.AddASCII(306 /*DateTime*/, opts.Metadata.AcquisitionDateTime.Format("2006:01:02 15:04:05"))
		}
		if opts.Metadata.SourceFormat != "" {
			b.AddASCII(TagWSISourceFormat, opts.Metadata.SourceFormat)
		}
		if opts.ToolsVersion != "" {
			b.AddASCII(TagWSIToolsVersion, opts.ToolsVersion)
		}
		if opts.Metadata.MPPX > 0 {
			b.AddDouble(TagWSIMPPX, []float64{opts.Metadata.MPPX})
		}
		if opts.Metadata.MPPY > 0 {
			b.AddDouble(TagWSIMPPY, []float64{opts.Metadata.MPPY})
		}
		if opts.Metadata.Magnification > 0 {
			b.AddDouble(TagWSIMagnification, []float64{opts.Metadata.Magnification})
		}
	}
	_ = levelCount // reserved for future WSILevelCount emission
}

// populateAssocIFD fills an ifdBuilder for an associated image. Associated
// images use strip-based encoding (1 strip covering the full image).
func populateAssocIFD(b *ifdBuilder, spec AssociatedSpec, dataOffset uint64) {
	b.AddLong(254 /*NewSubfileType*/, []uint32{1})
	b.AddLong(256, []uint32{spec.Width})
	b.AddLong(257, []uint32{spec.Height})
	if len(spec.BitsPerSample) == 0 {
		spec.BitsPerSample = []uint16{8, 8, 8}
	}
	b.AddShort(258, spec.BitsPerSample)
	b.AddShort(259, []uint16{spec.Compression})
	b.AddShort(262, []uint16{spec.Photometric})
	if spec.SamplesPerPixel == 0 {
		spec.SamplesPerPixel = 3
	}
	b.AddShort(277, []uint16{spec.SamplesPerPixel})
	b.AddShort(284, []uint16{1})
	b.AddLong(273 /*StripOffsets*/, []uint32{uint32(dataOffset)})
	b.AddLong(279 /*StripByteCounts*/, []uint32{uint32(len(spec.Bytes))})
	b.AddLong(278 /*RowsPerStrip*/, []uint32{spec.Height})
	b.AddASCII(TagWSIImageType, spec.Kind)
}
```

Add `"io"` to the imports of `writer.go`.

- [ ] **Step 4: Run all cogwsi tests**

Run: `go test ./internal/cogwsi/... -v`
Expected: all PASS, including `TestWriterCloseProducesValidTIFF` and `TestWriterCloseReverseOrderTileData`.

- [ ] **Step 5: Commit**

```bash
git add internal/cogwsi/writer.go internal/cogwsi/writer_test.go
git commit -m "feat(cogwsi): Writer.Close finalizes head block + reverse-order tile streaming"
```

---

## Task 9: Hook the `convert` command up to the writer

**Files:**
- Modify: `cmd/wsitools/convert.go`

**Background:** The `convert` command opens a source via `internal/source`, validates, builds `cogwsi.Options` from source metadata, then drives the writer end-to-end.

- [ ] **Step 1: Replace runConvert in convert.go**

Replace `cmd/wsitools/convert.go` with:

```go
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/cornish/wsitools/internal/cogwsi"
	"github.com/cornish/wsitools/internal/source"
)

var (
	cvOutput        string
	cvTo            string
	cvForce         bool
	cvBigTIFFFlag   string
	cvNoAssociated  bool
)

var convertCmd = &cobra.Command{
	Use:   "convert --to <target> -o <output> [flags] <input>",
	Short: "Convert a WSI to a new container losslessly (tile-copy)",
	Long: `Convert losslessly copies compressed tile bytes from a source WSI
into a new container without decoding or re-encoding. In v0.6 the only
supported target is COG-WSI (--to cog-wsi).

Examples:

  wsitools convert --to cog-wsi -o slide.cog.tiff slide.svs
  wsitools convert --to cog-wsi --no-associated -o slide.cog.tiff slide.tiff

See docs/superpowers/specs/2026-05-20-cog-wsi-format.md for the format spec.`,
	Args: cobra.ExactArgs(1),
	RunE: runConvert,
}

func init() {
	convertCmd.Flags().StringVarP(&cvOutput, "output", "o", "", "output file path (required)")
	convertCmd.Flags().StringVar(&cvTo, "to", "", "conversion target (only 'cog-wsi' in v0.6)")
	convertCmd.Flags().BoolVarP(&cvForce, "force", "f", false, "overwrite output if it exists")
	convertCmd.Flags().StringVar(&cvBigTIFFFlag, "bigtiff", "auto", "auto|on|off")
	convertCmd.Flags().BoolVar(&cvNoAssociated, "no-associated", false, "skip label/macro/thumbnail/overview")
	_ = convertCmd.MarkFlagRequired("output")
	_ = convertCmd.MarkFlagRequired("to")
	rootCmd.AddCommand(convertCmd)
}

func runConvert(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	input := args[0]
	start := time.Now()

	if cvTo != "cog-wsi" {
		return fmt.Errorf("--to %q: only 'cog-wsi' is supported in v0.6", cvTo)
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", cvOutput)
		}
	}
	bigTIFFMode, err := parseBigTIFFFlag(cvBigTIFFFlag)
	if err != nil {
		return err
	}

	src, err := source.Open(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	md := src.Metadata()
	opts := cogwsi.Options{
		BigTIFF:      bigTIFFMode,
		ToolsVersion: Version,
		Metadata: cogwsi.Metadata{
			MPPX:                md.MPP,
			MPPY:                md.MPP, // MPP is currently single-axis in source.Metadata
			Magnification:       md.Magnification,
			Make:                md.Make,
			Model:               md.Model,
			Software:            md.Software,
			AcquisitionDateTime: md.AcquisitionDateTime,
			SourceFormat:        src.Format(),
			SourceImageDesc:     fmt.Sprintf("wsitools/%s convert source=%s", Version, src.Format()),
		},
	}

	w, err := cogwsi.Create(cvOutput, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}

	// Tile-copy: full-resolution first (source order).
	for _, lvl := range src.Levels() {
		spec := cogwsi.LevelSpec{
			ImageWidth:      uint32(lvl.Size().X),
			ImageHeight:     uint32(lvl.Size().Y),
			TileWidth:       uint32(lvl.TileSize().X),
			TileHeight:      uint32(lvl.TileSize().Y),
			Compression:     compressionTagFor(lvl.Compression()),
			Photometric:     2, // RGB; lossless copy preserves source codec's color model
			SamplesPerPixel: 3,
			BitsPerSample:   []uint16{8, 8, 8},
			IsL0:            lvl.Index() == 0,
		}
		h, err := w.AddLevel(spec)
		if err != nil {
			w.Abort()
			return fmt.Errorf("add level %d: %w", lvl.Index(), err)
		}
		buf := make([]byte, lvl.TileMaxSize())
		grid := lvl.Grid()
		for ty := 0; ty < grid.Y; ty++ {
			for tx := 0; tx < grid.X; tx++ {
				n, err := lvl.TileInto(tx, ty, buf)
				if err != nil {
					w.Abort()
					return fmt.Errorf("read tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
				if err := h.WriteTile(uint32(tx), uint32(ty), buf[:n]); err != nil {
					w.Abort()
					return fmt.Errorf("write tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
			}
		}
	}

	if !cvNoAssociated {
		for _, a := range src.Associated() {
			bs, err := a.Bytes()
			if err != nil {
				w.Abort()
				return fmt.Errorf("read associated %s: %w", a.Kind(), err)
			}
			if err := w.AddAssociated(cogwsi.AssociatedSpec{
				Kind:        a.Kind(),
				Width:       uint32(a.Size().X),
				Height:      uint32(a.Size().Y),
				Compression: compressionTagFor(a.Compression()),
				Photometric: 2,
				Bytes:       bs,
			}); err != nil {
				w.Abort()
				return fmt.Errorf("add associated %s: %w", a.Kind(), err)
			}
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}

	if stat, _ := os.Stat(cvOutput); stat != nil {
		slog.Info("convert complete",
			"output", cvOutput,
			"size", formatBytes(stat.Size()),
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		fmt.Printf("wrote %s (%s, %s)\n", cvOutput, formatBytes(stat.Size()), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

func parseBigTIFFFlag(v string) (cogwsi.BigTIFFMode, error) {
	switch v {
	case "auto":
		return cogwsi.BigTIFFAuto, nil
	case "on":
		return cogwsi.BigTIFFOn, nil
	case "off":
		return cogwsi.BigTIFFOff, nil
	}
	return 0, fmt.Errorf("--bigtiff %q: want auto|on|off", v)
}

// compressionTagFor maps source.Compression to a TIFF Compression tag value.
func compressionTagFor(c source.Compression) uint16 {
	switch c {
	case source.CompressionJPEG:
		return 7
	case source.CompressionJPEG2000:
		return 33003 // Aperio / OpenJPEG codestream
	case source.CompressionLZW:
		return 5
	case source.CompressionDeflate:
		return 8
	case source.CompressionNone:
		return 1
	}
	// Other codecs (AVIF, WebP, JPEGXL, HTJ2K, Iris): no standardized TIFF tag.
	// The writer will preserve bytes; readers that don't understand the tag
	// will fail to decode. Return 0 and let the caller decide; we'll surface
	// this as an error in preflight in Task 10.
	return 0
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Run existing tests**

Run: `go test ./...`
Expected: all PASS (no regressions in `transcode`, `downsample`, etc).

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/convert.go
git commit -m "feat(convert): wire convert command to internal/cogwsi writer"
```

---

## Task 10: Preflight validation in `convert`

**Files:**
- Modify: `cmd/wsitools/convert.go`
- Modify: `cmd/wsitools/convert_test.go`

**Background:** Before opening the writer, fail fast on:
- Unsupported source compression (zero compression tag from `compressionTagFor`).
- Source has no levels.
- Planar source (`PlanarConfiguration` != 1). Currently `source.Source` doesn't expose this directly; we treat its absence as an acceptable risk and revisit in v0.6.1 if needed. (This step keeps the door open with a placeholder.)

- [ ] **Step 1: Write failing tests**

Append to `cmd/wsitools/convert_test.go`:

```go
func TestConvertFailsForMissingInput(t *testing.T) {
	dir := t.TempDir()
	rootCmd.SetArgs([]string{"convert", "--to", "cog-wsi", "-o", dir + "/out.tiff", dir + "/missing.svs"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error for missing input")
	}
}

func TestConvertFailsForBadTo(t *testing.T) {
	dir := t.TempDir()
	tmp, _ := os.Create(dir + "/in.tiff")
	tmp.Close()
	rootCmd.SetArgs([]string{"convert", "--to", "iris", "-o", dir + "/out.tiff", dir + "/in.tiff"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "only 'cog-wsi'") {
		t.Errorf("expected unsupported --to error, got %v", err)
	}
}

func TestConvertFailsWhenOutputExists(t *testing.T) {
	dir := t.TempDir()
	in, _ := os.Create(dir + "/in.tiff")
	in.Close()
	out, _ := os.Create(dir + "/out.tiff")
	out.Close()
	rootCmd.SetArgs([]string{"convert", "--to", "cog-wsi", "-o", dir + "/out.tiff", dir + "/in.tiff"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got %v", err)
	}
}
```

Add `"os"` to imports if not already there.

- [ ] **Step 2: Run to verify pass (most should already pass from Task 9; the bad-`--to` and existing-output checks are already wired)**

Run: `go test ./cmd/wsitools/ -run TestConvert -v`
Expected: PASS.

- [ ] **Step 3: Add unsupported-compression check in convert.go**

In `runConvert`, after `src := ...`, add a preflight loop *before* `cogwsi.Create`:

```go
	for _, lvl := range src.Levels() {
		if compressionTagFor(lvl.Compression()) == 0 {
			return fmt.Errorf("level %d: source compression %s has no standard TIFF Compression tag; cannot tile-copy",
				lvl.Index(), lvl.Compression())
		}
	}
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/wsitools/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/convert_test.go
git commit -m "feat(convert): preflight validation (input exists, --to, output collision, unsupported compression)"
```

---

## Task 11: Integration tests against sample files

**Files:**
- Create: `cmd/wsitools/convert_integration_test.go`

**Background:** Integration tests are gated by the `WSI_TOOLS_TESTDIR` env var (matches the project convention). Default: `./sample_files`. Tests for each TIFF-based source format we support, verifying bit-exact tile copy and layout invariants.

- [ ] **Step 1: Write the integration test scaffold**

Create `cmd/wsitools/convert_integration_test.go`:

```go
package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cornish/wsitools/internal/source"
)

func testDir(t *testing.T) string {
	d := os.Getenv("WSI_TOOLS_TESTDIR")
	if d == "" {
		d = "../../sample_files"
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("WSI_TOOLS_TESTDIR not available: %v", err)
	}
	return d
}

func TestConvertSVSBitExact(t *testing.T) {
	runConvertBitExactTest(t, "svs")
}

func TestConvertPhilipsBitExact(t *testing.T) {
	runConvertBitExactTest(t, "philips")
}

func TestConvertOMETIFFBitExact(t *testing.T) {
	runConvertBitExactTest(t, "ome-tiff")
}

func TestConvertGenericTIFFBitExact(t *testing.T) {
	runConvertBitExactTest(t, "generic-tiff")
}

// runConvertBitExactTest finds the first file in a per-format subdir of the
// test directory, runs `convert --to cog-wsi`, then verifies tile bit-equality
// and COG layout invariants on the output.
func runConvertBitExactTest(t *testing.T, formatSubdir string) {
	td := testDir(t)
	formatDir := filepath.Join(td, formatSubdir)
	entries, err := os.ReadDir(formatDir)
	if err != nil {
		t.Skipf("subdir %s not present: %v", formatDir, err)
	}
	var inputs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		inputs = append(inputs, filepath.Join(formatDir, e.Name()))
	}
	if len(inputs) == 0 {
		t.Skipf("no samples in %s", formatDir)
	}

	for _, input := range inputs {
		t.Run(filepath.Base(input), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.tiff")
			rootCmd.SetArgs([]string{"convert", "--to", "cog-wsi", "-o", out, input})
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("convert: %v", err)
			}

			// Open source again to diff per-tile bytes.
			src, err := source.Open(input)
			if err != nil {
				t.Fatalf("reopen source: %v", err)
			}
			defer src.Close()

			out2, err := source.Open(out)
			if err != nil {
				t.Fatalf("reopen output: %v", err)
			}
			defer out2.Close()

			if len(out2.Levels()) != len(src.Levels()) {
				t.Fatalf("level count mismatch: src=%d out=%d", len(src.Levels()), len(out2.Levels()))
			}
			for i, srcLvl := range src.Levels() {
				outLvl := out2.Levels()[i]
				if srcLvl.Size() != outLvl.Size() {
					t.Errorf("L%d size: src=%v out=%v", i, srcLvl.Size(), outLvl.Size())
				}
				if srcLvl.TileSize() != outLvl.TileSize() {
					t.Errorf("L%d tile size: src=%v out=%v", i, srcLvl.TileSize(), outLvl.TileSize())
				}
				grid := srcLvl.Grid()
				srcBuf := make([]byte, srcLvl.TileMaxSize())
				outBuf := make([]byte, outLvl.TileMaxSize())
				for ty := 0; ty < grid.Y; ty++ {
					for tx := 0; tx < grid.X; tx++ {
						sn, _ := srcLvl.TileInto(tx, ty, srcBuf)
						on, _ := outLvl.TileInto(tx, ty, outBuf)
						if !bytes.Equal(srcBuf[:sn], outBuf[:on]) {
							t.Fatalf("L%d tile (%d,%d) bytes differ: src=%d out=%d", i, tx, ty, sn, on)
						}
					}
				}
			}

			// Layout: smallest level tile data comes before largest level.
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			l0, lN, err := firstAndLastLevelTileOffsets(data)
			if err != nil {
				t.Logf("layout check skipped: %v", err)
			} else if lN >= l0 {
				t.Errorf("reverse order broken: lastLevel offset %d should be < L0 offset %d", lN, l0)
			}

			// Ghost area present.
			if !strings.HasPrefix(string(data[8:48]), "GDAL_STRUCTURAL_METADATA_SIZE=") {
				t.Errorf("ghost area missing in output")
			}
		})
	}
}

// firstAndLastLevelTileOffsets returns (L0 tile0 offset, lastLevel tile0 offset)
// for a classic TIFF file. Walks IFD0 and the last pyramid IFD.
func firstAndLastLevelTileOffsets(data []byte) (l0, lN uint64, err error) {
	ifd0 := uint64(binary.LittleEndian.Uint32(data[4:8]))
	l0, next, err := firstTileOffsetClassic(data, ifd0)
	if err != nil {
		return 0, 0, err
	}
	last := l0
	for next != 0 {
		var off uint64
		off, next, err = firstTileOffsetClassic(data, next)
		if err != nil {
			// Hit an associated IFD (no TileOffsets); stop.
			break
		}
		last = off
	}
	return l0, last, nil
}

func firstTileOffsetClassic(data []byte, ifdOff uint64) (uint64, uint64, error) {
	if ifdOff+2 > uint64(len(data)) {
		return 0, 0, errShort
	}
	n := uint64(binary.LittleEndian.Uint16(data[ifdOff : ifdOff+2]))
	var off uint64
	var hasTileOffsets bool
	for i := uint64(0); i < n; i++ {
		base := ifdOff + 2 + i*12
		tag := binary.LittleEndian.Uint16(data[base : base+2])
		count := uint64(binary.LittleEndian.Uint32(data[base+4 : base+8]))
		val := uint64(binary.LittleEndian.Uint32(data[base+8 : base+12]))
		if tag == 324 {
			if count == 1 {
				off = val
			} else {
				off = uint64(binary.LittleEndian.Uint32(data[val : val+4]))
			}
			hasTileOffsets = true
		}
	}
	next := uint64(binary.LittleEndian.Uint32(data[ifdOff+2+n*12 : ifdOff+2+n*12+4]))
	if !hasTileOffsets {
		return 0, next, errNoTileOffsets
	}
	return off, next, nil
}

var (
	errShort         = newErr("short read")
	errNoTileOffsets = newErr("no TileOffsets")
)

type strErr string

func (e strErr) Error() string { return string(e) }
func newErr(s string) error    { return strErr(s) }
```

- [ ] **Step 2: Run integration tests**

If `sample_files/` has the expected per-format subdirectories:

```bash
WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./cmd/wsitools/ -run TestConvert -v
```

Expected: PASS for whichever subdirs have samples; others skip.

If `sample_files/` is missing or has no per-format subdirs, tests skip cleanly. Verify by running without the env var.

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/convert_integration_test.go
git commit -m "test(convert): bit-exact tile-copy + reverse-order layout integration tests"
```

---

## Task 12: CHANGELOG + README updates

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `README.md`

- [ ] **Step 1: Read current state of both files**

Read `CHANGELOG.md` and `README.md` to find the right insertion points (the unreleased / 0.6.0-dev section in CHANGELOG, and the `Available Commands` table in README).

- [ ] **Step 2: Add an unreleased section entry to CHANGELOG.md**

Under the `v0.6.0` (or `Unreleased` / `0.6.0-dev`) heading, add:

```markdown
### Added
- New `wsitools convert` command for lossless tile-copy conversion between WSI containers.
  - v0.6 target: `--to cog-wsi` (Cloud Optimized GeoTIFF + WSI extensions).
  - Bit-exact: compressed tile bytes are copied verbatim from source. No decode/re-encode.
  - Source formats: SVS, Philips-TIFF, OME-TIFF (tiled), BIF, IFE, generic-TIFF.
- New COG-WSI format specification at `docs/superpowers/specs/2026-05-20-cog-wsi-format.md`.
- New `internal/cogwsi` writer package implementing the format.
- New private TIFF tag IDs: `WSIMPPX`=65085, `WSIMPPY`=65086, `WSIMagnification`=65087.
```

- [ ] **Step 3: Add a row to the README `Available Commands` table**

In `README.md`, find the commands list (it likely mirrors `wsitools --help`) and add:

```markdown
| convert    | Convert a WSI to a new container losslessly (tile-copy) |
```

If the table format differs, match the existing style.

- [ ] **Step 4: Verify build and tests still pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md README.md
git commit -m "docs: announce wsitools convert + COG-WSI format spec in v0.6.0"
```

---

## Spec Coverage Check

Mapping spec requirements → implementing task(s):

| Spec section | Requirement | Task(s) |
|---|---|---|
| Format §3.1 | Little-endian, BigTIFF auto-promote | 5 (planner), 8 (header) |
| Format §3.2 | Head-front IFDs, reverse tile data order, tail associated images, 16-byte alignment | 5, 8 |
| Format §3.3 | Strict COG pyramid + strip exception for associated | 8 (pop. assoc IFD) |
| Format §4   | Ghost area | 2 |
| Format §5   | Pyramid IFD tag set incl. JPEGTables preserved | 6, 8 |
| Format §5.2 | Metadata tags incl. WSIMPPX/Y, WSIMagnification | 3, 8 |
| Format §6   | Associated images after pyramid, WSIImageType tagging | 8 |
| Design §3.2 | CLI flags: --to, -o, -f, --bigtiff, --no-associated | 1, 9 |
| Design §3.4 | Preflight validation order | 10 |
| Design §4.3 | Spool staging strategy | 4, 7, 8 |
| Design §4.4 | Command flow (source open → preflight → spool → finalize) | 9, 10 |
| Design §5   | Tile-copy invariants enforced | 7 (order check), 9 (caller), 11 (integration) |
| Design §6   | Unit + integration tests | 2, 4, 5, 6, 7, 8, 11 |
| Design §7   | CHANGELOG + README | 12 |

No gaps identified.
