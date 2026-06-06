# Associated-image editing (Slice 1: SVS + generic-TIFF) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `wsitools label|macro|thumbnail|overview remove|replace` — in-place associated-image editing that copies the pyramid byte-for-byte and rewrites only the tail IFD, for SVS and generic single-plane TIFF.

**Architecture:** A ported splice engine (`internal/tiff/edit`) does prefix-copy + tail-re-emit so the pyramid stays bit-identical and removed-label PHI never reaches the output. A `RangeMap` dominance check refuses rather than corrupt. Atomic temp+fsync+rename writes (`internal/atomic`). Target IFD located via a new opentile-go `AssociatedIFDOffset` (directed by a GitHub issue, consumed after release). Replacement images encode per-type (SVS label → LZW+predictor2; macro/overview → JPEG).

**Tech Stack:** Go, cobra CLI, `internal/tiff` core, opentile-go reader, `github.com/hhrutter/lzw` (new dep), `golang.org/x/image/tiff`.

**Reference port source:** clone the proven tool once for reference —
`gh repo clone wsilabs/wsi-label-tools /tmp/wsi-label-ref` — and port the named files into wsitools idioms (wsitools byte order/tag constants, package names). Spec: `docs/superpowers/specs/2026-06-05-associated-image-editing-design.md`.

**Dependency note:** Phase B (engine) is fully independent and is built first. Phase C (integration) consumes the opentile-go release from Phase A; if that release is not yet available, **stop after Phase B** and resume Phase C once the opentile-go bump lands. Do not substitute a heuristic locator — the opentile-go method is the decided design.

**Branch:** create `feat/associated-image-editing` off `main`. Never implement on `main`.

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/atomic/atomic.go` (+ test) | `WriteAtomic`: temp + fsync + rename |
| `internal/tiff/edit/header.go` (+ test) | TIFF/BigTIFF header parse; first-IFD-offset slot |
| `internal/tiff/edit/parse.go` (+ test) | Raw parse → `File{Header, IFDs, Ranges}` |
| `internal/tiff/edit/ifd.go` (+ test) | `IFD`/`IFDEntry` accessors (StringValue, UintArray) |
| `internal/tiff/edit/ranges.go` (+ test) | `RangeMap`: ownership + dominance queries |
| `internal/tiff/edit/splice.go` (+ test) | `Splice` (Remove/Replace/Append/InsertBefore) |
| `internal/source/opentile.go` (modify) | `AssociatedImage.IFDOffset()` delegating method |
| `internal/source/source.go` (modify) | `IFDOffset()` added to the `AssociatedImage` interface |
| `cmd/wsitools/associated_locate.go` (+ test) | type → chain IFD index |
| `cmd/wsitools/associated_replace.go` (+ test) | decode/resize/encode → `ReplacementIFD` |
| `cmd/wsitools/associated.go` (+ test) | cobra factory; flags; output resolve; dispatch |
| `cmd/wsitools/associated_integration_test.go` | gated end-to-end on SVS + generic-TIFF |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md` | docs |

---

## Phase A — opentile-go handoff

### Task 1: File the opentile-go issue for `AssociatedIFDOffset`

**Files:** none in wsitools (coordination task).

- [ ] **Step 1: Create the issue**

Run:

```bash
gh issue create --repo wsilabs/opentile-go \
  --title "Expose AssociatedIFDOffset(a) — source IFD byte offset of an associated image" \
  --body "$(cat <<'EOF'
**Need:** wsitools' new associated-image editor (label/macro remove & replace)
splices the raw TIFF in place. It must map a typed AssociatedImage back to its
source IFD byte offset. opentile-go already classifies the type and internally
holds the associated image's *tiff.Page (which knows its IFD offset); please
surface it.

**Proposed API (Slide method):**

    // AssociatedIFDOffset returns the byte offset of the IFD backing
    // associated image a, for TIFF-family slides. ok is false when the
    // slide is not TIFF-backed or a is not one of s.Associated().
    func (s *Slide) AssociatedIFDOffset(a AssociatedImage) (offset int64, ok bool)

**Scope:** implement for the SVS and generic-TIFF readers first (the formats
wsitools Slice 1 edits). Other TIFF readers may opt in later; non-TIFF returns
ok=false.

**Notes:** the backing *tiff.Page likely needs to retain its directory-record
offset at parse time (add an unexported field if absent).

**Verification:** add a test that opens an SVS + a generic-TIFF fixture and
asserts the returned offset points at a valid IFD; ensure it runs (not SKIP)
under OPENTILE_TESTDIR.

Blocks: wsilabs/wsitools associated-image editing Slice 1.
EOF
)"
```

- [ ] **Step 2: Record the issue URL**

Paste the printed issue URL into the plan here and into the branch's first commit message body so Phase C can reference it:

```
opentile-go issue: <URL>
```

No code change; nothing to test. This task is COMPLETE once the issue exists and its URL is recorded.

---

## Phase B — wsitools engine (independent of Phase A)

### Task 2: `internal/atomic` — atomic file writes

**Files:**
- Create: `internal/atomic/atomic.go`
- Test: `internal/atomic/atomic_test.go`

- [ ] **Step 1: Write the failing test**

```go
package atomic

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.bin")
	err := WriteAtomic(target, func(w *os.File) error {
		_, e := w.Write([]byte("hello"))
		return e
	}, true)
	if err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}
	// No leftover temp files.
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("dir has %d entries, want 1 (no temp leftover)", len(ents))
	}
}

func TestWriteAtomicWriteErrorLeavesTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.bin")
	os.WriteFile(target, []byte("original"), 0o644)
	want := errors.New("boom")
	err := WriteAtomic(target, func(w *os.File) error { return want }, true)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want boom", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "original" {
		t.Fatalf("target modified: %q", got)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp file leftover: %d entries", len(ents))
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/atomic/ -run TestWriteAtomic -v`
Expected: FAIL (package/func undefined).

- [ ] **Step 3: Implement**

Port `/tmp/wsi-label-ref/internal/atomic/atomic.go` verbatim (it is already wsitools-compatible — package `atomic`, no external deps). Confirm `WriteAtomic(target string, write func(w *os.File) error, fsync bool) error` matches the test. The write callback must return its error so a write failure removes the temp and leaves the target untouched.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/atomic/ -run TestWriteAtomic -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/atomic/
git commit -m "feat(atomic): WriteAtomic (temp + fsync + rename)"
```

### Task 3: `internal/tiff/edit` — header + IFD parse

**Files:**
- Create: `internal/tiff/edit/header.go`, `internal/tiff/edit/ifd.go`, `internal/tiff/edit/parse.go`
- Test: `internal/tiff/edit/parse_test.go`

Port from `/tmp/wsi-label-ref/internal/tifflow/{header,ifd,parse}.go` with these adaptations: package `edit`; keep its `Header`, `File`, `IFD`, `IFDEntry`, `TagType`, and `Tag*` constants self-contained (do NOT depend on `internal/tiff` constants yet — the engine is standalone and byte-level). Each parsed `IFD` MUST record its own directory-record `Offset uint64` and `NextPointerOffset uint64` (the splice patches the latter; locate matches the former). Each `IFDEntry` records `Tag, Type, Count, Inline bool, RawValueField []byte, DataOffset, DataSize uint64`.

- [ ] **Step 1: Write the failing test**

```go
package edit

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildClassicTIFF returns a minimal little-endian classic TIFF with `n`
// IFDs, each carrying ImageWidth (256) + ImageDescription (out-of-line),
// chained in order. Returns the bytes and the per-IFD record offsets.
func buildClassicTIFF(t *testing.T, descs []string) ([]byte, []uint64) {
	t.Helper()
	var buf bytes.Buffer
	le := binary.LittleEndian
	w16 := func(v uint16) { b := make([]byte, 2); le.PutUint16(b, v); buf.Write(b) }
	w32 := func(v uint32) { b := make([]byte, 4); le.PutUint32(b, v); buf.Write(b) }
	// Header: II, 42, firstIFD offset (filled later).
	buf.WriteString("II")
	w16(42)
	firstIFDLoc := uint32(buf.Len())
	w32(0) // placeholder

	type patch struct{ at, val uint32 }
	var nextPatches []patch
	var descPatches []struct {
		entryValLoc uint32
		data        []byte
	}
	ifdOffsets := make([]uint64, len(descs))

	// First, write all description blobs up front (out-of-line), record offsets.
	descOffsets := make([]uint32, len(descs))
	for i, d := range descs {
		descOffsets[i] = uint32(buf.Len())
		buf.WriteString(d)
		buf.WriteByte(0)
		if buf.Len()%2 != 0 {
			buf.WriteByte(0)
		}
	}
	_ = descPatches

	// Patch header first-IFD to point at the first IFD we are about to write.
	for i, d := range descs {
		if buf.Len()%2 != 0 {
			buf.WriteByte(0)
		}
		ifdOffsets[i] = uint64(buf.Len())
		entryCount := uint16(2)
		w16(entryCount)
		// Entry 1: ImageWidth (256) LONG count1 inline 256.
		w16(256)
		w16(4) // LONG
		w32(1)
		w32(256)
		// Entry 2: ImageDescription (270) ASCII count len+1 out-of-line.
		w16(270)
		w16(2) // ASCII
		w32(uint32(len(d) + 1))
		w32(descOffsets[i])
		// next-IFD pointer.
		nextLoc := uint32(buf.Len())
		w32(0)
		nextPatches = append(nextPatches, patch{at: nextLoc, val: 0}) // value patched below
		_ = i
	}

	out := buf.Bytes()
	le.PutUint32(out[firstIFDLoc:], uint32(ifdOffsets[0]))
	// Link IFDs: next pointer of i -> ifdOffsets[i+1], last -> 0.
	for i := range descs {
		var next uint32
		if i+1 < len(descs) {
			next = uint32(ifdOffsets[i+1])
		}
		le.PutUint32(out[nextPatches[i].at:], next)
	}
	return out, ifdOffsets
}

func TestParseChainAndOffsets(t *testing.T) {
	data, offs := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4", "Aperio\nmacro 8x8"})
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.IFDs) != 3 {
		t.Fatalf("got %d IFDs, want 3", len(f.IFDs))
	}
	for i := range f.IFDs {
		if f.IFDs[i].Offset != offs[i] {
			t.Errorf("IFD %d Offset = %d, want %d", i, f.IFDs[i].Offset, offs[i])
		}
	}
	desc, ok := f.IFDs[1].StringValue(TagImageDescription)
	if !ok || desc != "Aperio\nlabel 4x4" {
		t.Errorf("IFD1 desc = %q ok=%v", desc, ok)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/tiff/edit/ -run TestParse -v`
Expected: FAIL (undefined `Parse`/`TagImageDescription`).

- [ ] **Step 3: Implement**

Port `header.go`, `ifd.go`, `parse.go` from the reference. Ensure `Parse(r io.ReaderAt, size int64) (*File, error)` and `(*IFD).StringValue(tag uint16) (string, bool)` exist, IFDs carry `Offset`, and BigTIFF (magic 43) is handled. Do NOT populate `Ranges` yet (Task 5) — `File.Ranges` may be nil here; the test does not touch it.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/tiff/edit/ -run TestParse -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/edit/
git commit -m "feat(tiff/edit): raw TIFF/BigTIFF parse (header, IFD chain, offsets)"
```

### Task 4: `RangeMap` ownership + dominance

**Files:**
- Create: `internal/tiff/edit/ranges.go`
- Test: `internal/tiff/edit/ranges_test.go`

- [ ] **Step 1: Write the failing test**

```go
package edit

import (
	"errors"
	"testing"
)

func TestRangeMapAddOverlap(t *testing.T) {
	var m RangeMap
	if err := m.Add(Range{Start: 0, End: 10, Owner: 0, What: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(Range{Start: 5, End: 15, Owner: 1, What: "b"}); !errors.Is(err, ErrOverlap) {
		t.Fatalf("want ErrOverlap, got %v", err)
	}
	if err := m.Add(Range{Start: 10, End: 20, Owner: 1, What: "c"}); err != nil {
		t.Fatalf("adjacent non-overlap should add: %v", err)
	}
}

func TestRangeMapDominanceQueries(t *testing.T) {
	var m RangeMap
	_ = m.Add(Range{Start: 100, End: 200, Owner: 0, What: "ifd0-strip"})
	_ = m.Add(Range{Start: 200, End: 260, Owner: 0, What: "ifd0-rec"})
	_ = m.Add(Range{Start: 300, End: 360, Owner: 1, What: "ifd1-rec"}) // label
	_ = m.Add(Range{Start: 360, End: 420, Owner: 2, What: "ifd2-rec"}) // macro

	if got := m.MinOffsetOfOwnersAtOrAfter(1); got != 300 {
		t.Errorf("MinOffsetOfOwnersAtOrAfter(1) = %d, want 300", got)
	}
	// IFD0 owns nothing at/after 300 → clean to splice tail starting at IFD1.
	if _, ok := m.AnyRangeOfOwnerAtOrAfter(0, 300); ok {
		t.Errorf("IFD0 should own nothing >= 300")
	}
	// If IFD0 owned bytes past cutoff, dominance must report it.
	_ = m.Add(Range{Start: 500, End: 520, Owner: 0, What: "ifd0-late"})
	if _, ok := m.AnyRangeOfOwnerAtOrAfter(0, 300); !ok {
		t.Errorf("IFD0 owns 500 >= 300, must be reported")
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/tiff/edit/ -run TestRangeMap -v`
Expected: FAIL (undefined `RangeMap`/`ErrOverlap`).

- [ ] **Step 3: Implement**

Port `ranges.go` from the reference verbatim (package `edit`). Confirm `ErrOverlap` is defined in the package (add to an `errors.go` or reuse the reference's error declarations).

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/tiff/edit/ -run TestRangeMap -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/edit/
git commit -m "feat(tiff/edit): RangeMap ownership + dominance queries"
```

### Task 5: Populate `RangeMap` during `Parse`

**Files:**
- Modify: `internal/tiff/edit/parse.go`
- Test: `internal/tiff/edit/parse_test.go` (add)

- [ ] **Step 1: Write the failing test**

```go
func TestParsePopulatesRanges(t *testing.T) {
	data, offs := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4"})
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Ranges == nil {
		t.Fatal("Ranges not populated")
	}
	// The IFD-1 record range must be owned by IFD 1.
	if got := f.Ranges.MinOffsetOfOwner(1); got > offs[1] {
		t.Errorf("MinOffsetOfOwner(1) = %d, want <= %d (its record)", got, offs[1])
	}
	// IFD 0 owns nothing at/after IFD 1's record start (clean tail boundary).
	if _, ok := f.Ranges.AnyRangeOfOwnerAtOrAfter(0, offs[1]); ok {
		t.Errorf("IFD0 owns bytes >= IFD1 record; expected clean layout")
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/tiff/edit/ -run TestParsePopulatesRanges -v`
Expected: FAIL (`Ranges` nil).

- [ ] **Step 3: Implement**

In `Parse`, after reading each IFD, add ranges to `File.Ranges` (port the range-attribution from the reference's parse): the IFD record `[Offset, Offset+recordSize)`, each out-of-line tag blob `[DataOffset, DataOffset+DataSize)`, and each strip/tile `[off, off+count)` — all `Owner: i`. Skip inline entries and zero-size data. Use the reference's word-alignment and StripOffsets/TileOffsets handling.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/tiff/edit/ -v`
Expected: PASS (all parse + ranges tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/edit/
git commit -m "feat(tiff/edit): attribute byte ranges to owning IFDs during Parse"
```

### Task 6: `Splice` — Remove (with dominance refusal)

**Files:**
- Create: `internal/tiff/edit/splice.go`
- Test: `internal/tiff/edit/splice_test.go`

- [ ] **Step 1: Write the failing test**

```go
package edit

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.tiff")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSpliceRemoveMiddleIFD(t *testing.T) {
	data, _ := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4", "Aperio\nmacro 8x8"})
	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "out.tiff")

	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := Splice(SpliceParams{
		InPath: in, OutPath: out, File: f, Mode: SpliceRemove, TargetIdx: 1, Fsync: false,
	}); err != nil {
		t.Fatalf("Splice: %v", err)
	}

	outData, _ := os.ReadFile(out)
	of, err := Parse(bytes.NewReader(outData), int64(len(outData)))
	if err != nil {
		t.Fatalf("re-parse output: %v", err)
	}
	if len(of.IFDs) != 2 {
		t.Fatalf("output has %d IFDs, want 2", len(of.IFDs))
	}
	// The label description must be gone from the output entirely.
	if bytes.Contains(outData, []byte("label 4x4")) {
		t.Errorf("removed label bytes still present in output (PHI not erased)")
	}
	// Remaining IFDs are image + macro, in order.
	d0, _ := of.IFDs[0].StringValue(TagImageDescription)
	d1, _ := of.IFDs[1].StringValue(TagImageDescription)
	if d0 != "Aperio\nimage" || d1 != "Aperio\nmacro 8x8" {
		t.Errorf("output descs = %q, %q", d0, d1)
	}
}

func TestSpliceRefusesInterleavedLayout(t *testing.T) {
	data, _ := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4", "Aperio\nmacro"})
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	// Forge an IFD0-owned range past IFD1's record to violate dominance.
	cut := f.Ranges.MinOffsetOfOwnersAtOrAfter(1)
	_ = f.Ranges.Add(Range{Start: cut + 4, End: cut + 8, Owner: 0, What: "forged"})

	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "out.tiff")
	err = Splice(SpliceParams{InPath: in, OutPath: out, File: f, Mode: SpliceRemove, TargetIdx: 1})
	if !errors.Is(err, ErrUnexpectedLayout) {
		t.Fatalf("want ErrUnexpectedLayout, got %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Errorf("output created despite refusal")
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/tiff/edit/ -run TestSplice -v`
Expected: FAIL (undefined `Splice`/`SpliceParams`/`ErrUnexpectedLayout`).

- [ ] **Step 3: Implement**

Port `splice.go` from the reference (the full `doSplice`, `reemitIFD`, `emitReplacementIFD`, `convertInlineEndian`, `headerFirstIFDOffsetLoc`, `findEntry`). Package `edit`. Keep all four `SpliceMode`s. The Remove path and the dominance refusal are what these tests exercise; the rest is needed by Task 7.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/tiff/edit/ -run TestSplice -v`
Expected: PASS.

- [ ] **Step 5: Add a BigTIFF Remove test + commit**

Add `TestSpliceRemoveBigTIFF` mirroring `TestSpliceRemoveMiddleIFD` but with a BigTIFF builder (magic 43, 8-byte offsets, 8-byte counts, 20-byte entries). If writing a full BigTIFF builder is heavy, instead assert the classic path and open an explicit follow-up note in the commit body. Then:

```bash
go test ./internal/tiff/edit/ -v
git add internal/tiff/edit/
git commit -m "feat(tiff/edit): Splice remove + dominance refusal (prefix-copy + tail-re-emit)"
```

### Task 7: `Splice` — Append + Replace round-trips

**Files:**
- Modify: `internal/tiff/edit/splice_test.go` (add)

- [ ] **Step 1: Write the failing test**

```go
func makeReplacement(desc string) *ReplacementIFD {
	// One strip of 12 raw RGB bytes (2x2), stored uncompressed for the test.
	strip := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	descBytes := append([]byte(desc), 0)
	le := func(v uint16) []byte { return []byte{byte(v), byte(v >> 8)} }
	le32 := func(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }
	return &ReplacementIFD{
		Tags: []OutTag{
			{Tag: TagImageWidth, Type: TypeLong, Count: 1, Inline: true, Bytes: le32(2)},
			{Tag: TagImageLength, Type: TypeLong, Count: 1, Inline: true, Bytes: le32(2)},
			{Tag: TagImageDescription, Type: TypeASCII, Count: uint64(len(descBytes)), Inline: false, Bytes: descBytes},
			{Tag: TagStripOffsets, Type: TypeLong, Count: 1, Inline: false, Bytes: make([]byte, 4), ResolvesToOffset: true, OffsetRefs: []int{0}},
			{Tag: TagStripByteCounts, Type: TypeLong, Count: 1, Inline: true, Bytes: le32(uint32(len(strip)))},
			{Tag: TagCompression, Type: TypeShort, Count: 1, Inline: true, Bytes: le(1)},
		},
		StripData: [][]byte{strip},
	}
}

func TestSpliceReplace(t *testing.T) {
	data, _ := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4"})
	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "out.tiff")
	f, _ := Parse(bytes.NewReader(data), int64(len(data)))
	if err := Splice(SpliceParams{InPath: in, OutPath: out, File: f,
		Mode: SpliceReplace, TargetIdx: 1, Replacement: makeReplacement("Aperio\nlabel NEW")}); err != nil {
		t.Fatal(err)
	}
	outData, _ := os.ReadFile(out)
	of, _ := Parse(bytes.NewReader(outData), int64(len(outData)))
	if len(of.IFDs) != 2 {
		t.Fatalf("got %d IFDs, want 2", len(of.IFDs))
	}
	if bytes.Contains(outData, []byte("label 4x4")) {
		t.Errorf("old label bytes still present")
	}
	d1, _ := of.IFDs[1].StringValue(TagImageDescription)
	if d1 != "Aperio\nlabel NEW" {
		t.Errorf("replaced desc = %q", d1)
	}
}

func TestSpliceAppend(t *testing.T) {
	data, _ := buildClassicTIFF(t, []string{"Aperio\nimage"})
	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "out.tiff")
	f, _ := Parse(bytes.NewReader(data), int64(len(data)))
	if err := Splice(SpliceParams{InPath: in, OutPath: out, File: f,
		Mode: SpliceAppend, TargetIdx: len(f.IFDs), Replacement: makeReplacement("Aperio\nlabel ADDED")}); err != nil {
		t.Fatal(err)
	}
	outData, _ := os.ReadFile(out)
	of, _ := Parse(bytes.NewReader(outData), int64(len(outData)))
	if len(of.IFDs) != 2 {
		t.Fatalf("got %d IFDs, want 2", len(of.IFDs))
	}
	d1, _ := of.IFDs[1].StringValue(TagImageDescription)
	if d1 != "Aperio\nlabel ADDED" {
		t.Errorf("appended desc = %q", d1)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/tiff/edit/ -run 'TestSpliceReplace|TestSpliceAppend' -v`
Expected: FAIL if `OutTag`/`TypeLong`/`TypeShort`/`TypeASCII` constants are missing — add them to `ifd.go` (port from reference) so the test compiles, then it should pass with the Task-6 `splice.go`.

- [ ] **Step 3: Implement (if needed)**

Ensure `OutTag`, `ReplacementIFD`, and the `Type*`/`Tag*` constants used above are exported from the package (port any missing ones). No new logic beyond Task 6.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/tiff/edit/ -v`
Expected: PASS (all edit tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/edit/
git commit -m "test(tiff/edit): Splice replace + append round-trips"
```

---

## Phase C — integration (consumes Phase A opentile-go release)

> **Gate:** Phase A's `AssociatedIFDOffset` must be released and the wsitools `go.mod` bumped before Task 8 can pass. If the release is not ready, STOP here and resume when it lands. Verify per CLAUDE.md: run opentile-go's own suite with `OPENTILE_TESTDIR=$(pwd)/sample_files go test ./decoder/... ./formats/...` (PASS, not SKIP) before bumping.

### Task 8: Bump opentile-go + add `source.AssociatedImage.IFDOffset()`

**Files:**
- Modify: `go.mod` (opentile-go version bump)
- Modify: `internal/source/source.go` (interface), `internal/source/opentile.go` (impl)
- Test: `internal/source/associated_ifdoffset_test.go`

- [ ] **Step 1: Bump the dep**

Run: `go get github.com/wsilabs/opentile-go@<released-version> && go mod tidy`

- [ ] **Step 2: Write the failing test** (gated, like other source integration tests)

```go
package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssociatedIFDOffset(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var found bool
	for _, a := range s.Associated() {
		if a.Type() == "label" {
			off, ok := a.IFDOffset()
			if !ok || off <= 0 {
				t.Fatalf("label IFDOffset = %d ok=%v", off, ok)
			}
			found = true
		}
	}
	if !found {
		t.Skip("fixture has no label")
	}
}
```

- [ ] **Step 3: Run, verify it fails**

Run: `go test ./internal/source/ -run TestAssociatedIFDOffset -v`
Expected: FAIL (`IFDOffset` undefined).

- [ ] **Step 4: Implement**

In `internal/source/source.go`, add `IFDOffset() (int64, bool)` to the `AssociatedImage` interface. In `internal/source/opentile.go`, implement on `opentileAssociated`:

```go
func (a *opentileAssociated) IFDOffset() (int64, bool) {
	return a.slide.AssociatedIFDOffset(a.a)
}
```

This requires `opentileAssociated` to hold the `*opentile.Slide`. Update `Associated()` to pass it: `&opentileAssociated{a: a, slide: s.t}` and add the field. Adjust any other `AssociatedImage` implementers in `internal/source` (e.g. DICOM) to return `(0, false)`.

- [ ] **Step 5: Run, verify pass + full source suite**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/source/ -v`
Expected: PASS (or SKIP only if fixtures absent).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/source/
git commit -m "feat(source): AssociatedImage.IFDOffset() via opentile-go AssociatedIFDOffset; bump opentile-go"
```

### Task 9: `associated_locate.go` — type → chain IFD index

**Files:**
- Create: `cmd/wsitools/associated_locate.go`
- Test: `cmd/wsitools/associated_locate_test.go`

- [ ] **Step 1: Write the failing test** (gated)

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/edit"
)

func TestLocateAssociatedSVSLabel(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	hasLabel := false
	for _, a := range src.Associated() {
		if a.Type() == "label" {
			hasLabel = true
		}
	}
	if !hasLabel {
		t.Skip("fixture has no label")
	}
	data, _ := os.ReadFile(p)
	f, err := edit.Parse(newReaderAt(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	idx, a, err := locateAssociated(src, f, "label")
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if idx < 0 || idx >= len(f.IFDs) {
		t.Fatalf("idx %d out of range", idx)
	}
	if a.Type() != "label" {
		t.Fatalf("type = %q", a.Type())
	}
}
```

(Add a small `newReaderAt` helper in the test, or use `bytes.NewReader`.)

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./cmd/wsitools/ -run TestLocateAssociated -v`
Expected: FAIL (`locateAssociated` undefined).

- [ ] **Step 3: Implement**

```go
package main

import (
	"errors"
	"fmt"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/edit"
)

var (
	ErrNoSuchAssociated = errors.New("associated image not present")
	ErrUnsupportedAssoc = errors.New("associated editing not supported for this format")
)

// locateAssociated finds the chain-order IFD index of the associated image of
// the given type. Returns ErrNoSuchAssociated if absent.
func locateAssociated(src source.Source, file *edit.File, typ string) (int, source.AssociatedImage, error) {
	var target source.AssociatedImage
	for _, a := range src.Associated() {
		if a.Type() == typ {
			target = a
			break
		}
	}
	if target == nil {
		return -1, nil, fmt.Errorf("%w: %s", ErrNoSuchAssociated, typ)
	}
	off, ok := target.IFDOffset()
	if !ok {
		return -1, nil, fmt.Errorf("%w: %s", ErrUnsupportedAssoc, typ)
	}
	for i := range file.IFDs {
		if file.IFDs[i].Offset == uint64(off) {
			return i, target, nil
		}
	}
	return -1, nil, fmt.Errorf("%w: IFD at offset %d not found in chain", edit.ErrUnexpectedLayout, off)
}
```

- [ ] **Step 4: Run, verify pass**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestLocateAssociated -v`
Expected: PASS (or SKIP if no fixture).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_locate.go cmd/wsitools/associated_locate_test.go
git commit -m "feat(cmd): locateAssociated — type to chain IFD index via IFDOffset"
```

### Task 10: `associated_replace.go` — decode/resize/encode → `ReplacementIFD`

**Files:**
- Create: `cmd/wsitools/associated_replace.go`
- Test: `cmd/wsitools/associated_replace_test.go`
- Modify: `go.mod` (add `github.com/hhrutter/lzw`)

- [ ] **Step 1: Add the LZW dep**

Run: `go get github.com/hhrutter/lzw@latest && go mod tidy`

- [ ] **Step 2: Write the failing test**

```go
package main

import (
	"image"
	"image/color"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/edit"
)

func solidImage(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestBuildReplacementLabelLZW(t *testing.T) {
	img := solidImage(8, 4, color.RGBA{200, 100, 50, 255})
	rep, err := buildReplacementIFD(img, replaceOpts{
		typ: "label", compression: "lzw", desc: "Aperio\nlabel 8x4",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Compression tag must be LZW (5), Predictor 2 present.
	if !hasTagValue(rep, edit.TagCompression, 5) {
		t.Errorf("compression != LZW")
	}
	if !hasTagValue(rep, edit.TagPredictor, 2) {
		t.Errorf("predictor != 2")
	}
	if len(rep.StripData) == 0 {
		t.Errorf("no strip data")
	}
}

func TestBuildReplacementMacroJPEG(t *testing.T) {
	img := solidImage(16, 16, color.RGBA{10, 20, 30, 255})
	rep, err := buildReplacementIFD(img, replaceOpts{typ: "macro", compression: "jpeg"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagValue(rep, edit.TagCompression, 7) { // JPEG (new-style) = 7
		t.Errorf("compression != JPEG")
	}
}
```

(Add a `hasTagValue(rep *edit.ReplacementIFD, tag uint16, val uint64) bool` helper in the test that scans `rep.Tags`.)

- [ ] **Step 3: Run, verify it fails**

Run: `go test ./cmd/wsitools/ -run TestBuildReplacement -v`
Expected: FAIL (`buildReplacementIFD`/`replaceOpts` undefined).

- [ ] **Step 4: Implement**

Port the encoding helpers from `/tmp/wsi-label-ref/internal/aperio/{label,build_label}.go` (`rgbStripBytes`, `applyPredictor2`, `encodeLZW` via `hhrutter/lzw`, `EncodeLabelStrips`, the tag set in `BuildLabelIFD`). Generalize into:

```go
type replaceOpts struct {
	typ         string // label|macro|thumbnail|overview
	compression string // "", jpeg, lzw, deflate, none
	desc        string // ImageDescription to preserve/write
	resize      string // fit|stretch|none
	bg          color.RGBA
	targetW, targetH int // 0 = use image bounds
	force       bool
}

func buildReplacementIFD(img image.Image, o replaceOpts) (*edit.ReplacementIFD, error)
```

- Resolve the codec: explicit `o.compression` wins; else default LZW+predictor2 for `label`, JPEG for the others.
- Resize/letterbox per `o.resize` (port `resize.go`; default `fit` with `o.bg`); aspect-ratio guard unless `o.force`.
- LZW path: predictor-2 + `RowsPerStrip=2` strips, tags from `BuildLabelIFD`.
- JPEG path: encode one strip via `internal/codec` (or `image/jpeg`), `Compression=7`, `RowsPerStrip=height`, no predictor.
- Deflate/none: analogous strip builds.

- [ ] **Step 5: Run, verify pass**

Run: `go test ./cmd/wsitools/ -run TestBuildReplacement -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd/wsitools/associated_replace.go cmd/wsitools/associated_replace_test.go
git commit -m "feat(cmd): build replacement associated IFD (LZW+predictor2 label / JPEG macro)"
```

### Task 11: `associated.go` — cobra commands, flags, output resolution, dispatch

**Files:**
- Create: `cmd/wsitools/associated.go`
- Test: `cmd/wsitools/associated_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"strings"
	"testing"
)

func TestAssociatedCommandsRegistered(t *testing.T) {
	want := map[string]bool{"label": false, "macro": false, "thumbnail": false, "overview": false}
	for _, c := range rootCmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
			subs := map[string]bool{}
			for _, s := range c.Commands() {
				subs[s.Name()] = true
			}
			if !subs["remove"] || !subs["replace"] {
				t.Errorf("%s missing remove/replace subcommands", c.Name())
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("command %q not registered", name)
		}
	}
}

func TestResolveAssocOutputRejectsSameAsInput(t *testing.T) {
	_, err := resolveAssocOutput("/x/slide.svs", "/x/slide.svs", false, false)
	if err == nil || !strings.Contains(err.Error(), "same") {
		t.Fatalf("want same-path error, got %v", err)
	}
}

func TestResolveAssocOutputDerivesName(t *testing.T) {
	got, err := resolveAssocOutput("/x/slide.svs", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "slide_relabeled.svs") {
		t.Errorf("derived = %q, want .../slide_relabeled.svs", got)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./cmd/wsitools/ -run 'TestAssociated|TestResolveAssoc' -v`
Expected: FAIL (commands/`resolveAssocOutput` undefined).

- [ ] **Step 3: Implement**

Create `associated.go`:
- A factory `newAssociatedCmd(typ string) *cobra.Command` returning a parent command (`Use: typ`) with `remove` and `replace` subcommands; register all four in `init()` via `rootCmd.AddCommand`.
- Flags as in the spec (shared: `-o/--output`, `--in-place`, `--overwrite`, `--fsync` default true, `-q`; replace-only: `--image` required, `--compression`, `--resize` default `fit`, `--bg` default `F5F5E6`, `--label-dims`, `--force`).
- `resolveAssocOutput(input, out string, inPlace, overwrite bool) (string, error)` — port `resolve.go`'s logic, deriving `<stem>_relabeled<ext>`; for `--in-place`, return the input path itself (the engine writes a sibling temp and renames over it via `Splice`'s atomic rename). Error if `-o` and `--in-place` both set, or resolved == input (unless in-place).
- `runAssociatedRemove`/`runAssociatedReplace`:
  1. `source.Open(input)`; reject formats other than SVS / generic-TIFF with `ErrUnsupportedAssoc` + Slice-2/`convert` pointer (check `src.Format()`).
  2. Read file bytes; `edit.Parse`.
  3. remove: `locateAssociated` → `Splice{SpliceRemove, idx}`.
  4. replace: `locateAssociated`; if found, get existing dims/desc; build target dims (existing → `--label-dims` → per-type default); decode `--image`; `buildReplacementIFD`; `Splice{SpliceReplace, idx}` if found else `Splice{SpliceAppend, len(IFDs)}`.
  5. `OutPath` from `resolveAssocOutput`; `Splice` writes atomically (temp+rename). For `--in-place`, `OutPath == input`.

- [ ] **Step 4: Run, verify pass + build**

Run: `go test ./cmd/wsitools/ -run 'TestAssociated|TestResolveAssoc' -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated.go cmd/wsitools/associated_test.go
git commit -m "feat(cmd): label/macro/thumbnail/overview remove|replace commands"
```

### Task 12: End-to-end integration tests (gated)

**Files:**
- Create: `cmd/wsitools/associated_integration_test.go`

- [ ] **Step 1: Write the tests**

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func fixtureSVS(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	return p
}

func hasAssoc(t *testing.T, path, typ string) bool {
	src, err := source.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, a := range src.Associated() {
		if a.Type() == typ {
			return true
		}
	}
	return false
}

func TestLabelRemoveEndToEnd(t *testing.T) {
	in := fixtureSVS(t)
	if !hasAssoc(t, in, "label") {
		t.Skip("fixture has no label")
	}
	work := filepath.Join(t.TempDir(), "in.svs")
	data, _ := os.ReadFile(in)
	os.WriteFile(work, data, 0o644)
	out := filepath.Join(t.TempDir(), "out.svs")

	if err := runAssociatedRemoveFor("label", work, out, removeFlags{fsync: false}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if hasAssoc(t, out, "label") {
		t.Errorf("label still present after remove")
	}
	// PHI gone: the removed label's description bytes must not be in the output.
	outData, _ := os.ReadFile(out)
	if bytes.Contains(outData, []byte("\nlabel ")) {
		t.Errorf("label ImageDescription bytes still in output")
	}
	// Output smaller than input.
	si, _ := os.Stat(work)
	so, _ := os.Stat(out)
	if so.Size() >= si.Size() {
		t.Errorf("output not smaller: in=%d out=%d", si.Size(), so.Size())
	}
}

func TestMainPyramidPixelsUnchangedAfterRemove(t *testing.T) {
	// Compare `hash --mode pixel` of the main image before/after label remove.
	// Use the in-process hash helper or invoke runHash; assert equal.
	t.Skip("wire to hash --mode pixel helper during implementation")
}

func TestLabelReplaceEndToEnd(t *testing.T) {
	in := fixtureSVS(t)
	work := filepath.Join(t.TempDir(), "in.svs")
	data, _ := os.ReadFile(in)
	os.WriteFile(work, data, 0o644)
	out := filepath.Join(t.TempDir(), "out.svs")
	// Write a small PNG to use as the new label.
	png := filepath.Join(t.TempDir(), "new.png")
	writeTestPNG(t, png, 1200, 848)

	if err := runAssociatedReplaceFor("label", work, out, replaceFlags{image: png, compression: "lzw", fsync: false}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if !hasAssoc(t, out, "label") {
		t.Fatalf("label missing after replace")
	}
	// extract --type label from out decodes to ~1200x848.
	src, _ := source.Open(out)
	defer src.Close()
	for _, a := range src.Associated() {
		if a.Type() == "label" {
			if a.Size().X != 1200 || a.Size().Y != 848 {
				t.Errorf("label dims = %v", a.Size())
			}
		}
	}
}

func TestUnsupportedFormatRejected(t *testing.T) {
	// An OME-TIFF (or NDPI) input must error with a Slice-2/convert pointer.
	t.Skip("point at an ome-tiff fixture during implementation; assert ErrUnsupportedAssoc")
}
```

Implementation note: expose small testable entry points (`runAssociatedRemoveFor`/`runAssociatedReplaceFor` with plain-struct flags) that the cobra `RunE` handlers also call, so tests don't go through cobra arg parsing. Add `writeTestPNG`.

- [ ] **Step 2: Run, verify fail then implement the entry points + wire pyramid-hash check**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'TestLabel|TestMainPyramid|TestUnsupported' -v`
Iterate until the non-skipped tests PASS. Wire `TestMainPyramidPixelsUnchangedAfterRemove` to the existing `hash --mode pixel` path and unskip it. Point `TestUnsupportedFormatRejected` at an `ome-tiff` fixture and unskip.

- [ ] **Step 3: Full gated suite**

Run (uncontended, per the race-timeout gotcha): `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -race -timeout 30m`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/associated_integration_test.go
git commit -m "test(cmd): end-to-end label remove/replace on SVS (pixel-identical pyramid, PHI gone)"
```

### Task 13: Docs — README matrix, CHANGELOG, roadmap

**Files:**
- Modify: `README.md`, `CHANGELOG.md`, `docs/roadmap.md`

- [ ] **Step 1: Update docs**

- README: add `label`/`macro`/`thumbnail`/`overview` `remove`/`replace` to the format×command matrix (SVS + generic-TIFF supported; OME-TIFF/COG-WSI "coming next"); add a usage block and note the LZW-default/barcode-safe behavior and `--in-place`.
- CHANGELOG: new "Associated-image editing (label/macro/thumbnail/overview remove & replace) — SVS + generic-TIFF" entry; note the opentile-go bump and `hhrutter/lzw` dep.
- `docs/roadmap.md`: add the feature; mark Slice 1 done, Slice 2 (OME-TIFF/COG-WSI SubIFD-aware + OME-XML sync + DICOM instance swap) as planned.

- [ ] **Step 2: Verify build + full suite**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS (gated integration SKIPs without fixtures).

- [ ] **Step 3: Commit**

```bash
git add README.md CHANGELOG.md docs/roadmap.md
git commit -m "docs: associated-image editing (label/macro remove & replace) — matrix, changelog, roadmap"
```

---

## Final review

After all tasks, dispatch a final code reviewer for the whole branch, then use `superpowers:finishing-a-development-branch`. Run the heavy `cmd/wsitools` suite uncontended with `-timeout 30m` (race-suite timeout gotcha).

## Self-review notes (author)

- **Spec coverage:** engine (Tasks 2–7), opentile-go seam (Tasks 1, 8), locate (9), replace encoding (10), CLI/dispatch/output/errors (11), integration incl. pixel-identical + PHI-gone + unsupported-format (12), docs (13). OME-TIFF/COG-WSI/OME-XML/DICOM correctly excluded (Slice 2).
- **Type consistency:** `edit.File/IFD/IFDEntry/RangeMap/Range/SpliceParams/SpliceMode/ReplacementIFD/OutTag` and `Tag*`/`Type*` constants are defined in Tasks 3–7 and reused unchanged in 9–12; `source.AssociatedImage.IFDOffset()` defined in Task 8 and consumed in 9.
- **Dependency ordering:** Phase B is dep-free and built first; Phase C gates on the opentile-go release (explicit STOP). The plan never substitutes the rejected heuristic locator.
- **Ports** name exact reference files + adaptations; all new glue (atomic, locate, replace opts, CLI, tests) is given as complete code.
