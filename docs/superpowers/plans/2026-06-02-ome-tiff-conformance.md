# OME-TIFF writer conformance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `convert --to ome-tiff` produce conformant multi-resolution OME-TIFF — pyramid sub-resolutions stored as SubIFDs (330) of L0 so all levels round-trip, associated images enumerated in the OME-XML, plus `SampleFormat` (339) and the OME-XML preamble.

**Architecture:** Add SubIFD-pyramid layout to the streaming TIFF writer, gated by a new `Options.SubResolutionPyramid` flag (default off → every other format unchanged). At Close, when set, emit the sub-resolution IFDs first, then L0 with a `SubIFDs` tag listing their offsets, then associated images; the top-level next-IFD chain becomes L0 → associated only. The OME policy (set the flag, emit `SampleFormat=1`, generate the multi-`<Image>` OME-XML) lives in the `convert --to ome-tiff` callers.

**Tech Stack:** Go, cobra CLI, `internal/tiff` byte-emission core, `internal/tiff/streamwriter`, opentile-go OME reader (round-trip oracle).

**Spec:** `docs/superpowers/specs/2026-06-02-ome-tiff-conformance-design.md`
**Spec grounding notes:** `docs/references/ome-tiff-spec-notes.md`

---

## File Structure

- `internal/tiff/tags.go` — add `TagSubIFDs` (330), `TagSampleFormat` (339).
- `internal/tiff/streamwriter/options.go` — add `SubResolutionPyramid bool`, `SampleFormat uint16`.
- `internal/tiff/streamwriter/writer.go` — struct fields + Create wiring; refactor Close's per-IFD emission into `emitIFD`; add the SubIFD Close path.
- `internal/tiff/streamwriter/levelhandle.go` + `stripped.go` — emit `SampleFormat` on every IFD.
- `internal/tiff/streamwriter/subifd_test.go` (create) — SubIFD + SampleFormat unit test.
- `cmd/wsitools/ome_imagedesc.go` — preamble + associated `<Image>` enumeration; `omeAssocName` mapping.
- `cmd/wsitools/ome_imagedesc_test.go` (create) — OME-XML builder unit test.
- `cmd/wsitools/convert_tiff.go` — set the two Options for ome-tiff (both paths); pass associated specs to the OME-XML builder; filter associated IFDs for the synthetic OME path.
- `cmd/wsitools/convert_ome_test.go` (create) — integration tests (the dropped-pyramid regression).
- `docs/tiff-tags.md`, `docs/roadmap.md` — writer note + shipped entry.

---

### Task 1: TIFF tag constants (SubIFDs, SampleFormat)

**Files:**
- Modify: `internal/tiff/tags.go`
- Test: `internal/tiff/tagnames_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/tiff/tagnames_test.go`:

```go
func TestSubIFDAndSampleFormatConstants(t *testing.T) {
	if TagSubIFDs != 330 {
		t.Errorf("TagSubIFDs = %d, want 330", TagSubIFDs)
	}
	if TagSampleFormat != 339 {
		t.Errorf("TagSampleFormat = %d, want 339", TagSampleFormat)
	}
	if got := TagName(TagSubIFDs); got != "SubIFDs" {
		t.Errorf("TagName(330) = %q, want SubIFDs", got)
	}
	if got := TagName(TagSampleFormat); got != "SampleFormat" {
		t.Errorf("TagName(339) = %q, want SampleFormat", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tiff/ -run SubIFDAndSampleFormat -v`
Expected: FAIL — `undefined: TagSubIFDs` / `TagSampleFormat`.

- [ ] **Step 3: Add the constants**

In `internal/tiff/tags.go`, in the standard-tag `const` block, add (place `TagSubIFDs` near the JPEGTables/offset tags and `TagSampleFormat` near the other 3xx tags — keep gofmt alignment):

```go
	TagSubIFDs                   uint16 = 330
	TagSampleFormat              uint16 = 339
```

- [ ] **Step 4: Verify the names already resolve**

Run: `go test ./internal/tiff/ -run SubIFDAndSampleFormat -v`
Expected: PASS. (`330: "SubIFDs"` and `339: "SampleFormat"` are already in `internal/tiff/tagnames.go` — confirm with `grep -nE '330:|339:' internal/tiff/tagnames.go`. If either is missing, add it to the `tagNames` map and re-run.)

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/tags.go internal/tiff/tagnames_test.go internal/tiff/tagnames.go
git commit -m "feat(tiff): add SubIFDs (330) and SampleFormat (339) tag constants

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: streamwriter emits SampleFormat on every IFD

**Files:**
- Modify: `internal/tiff/streamwriter/options.go`, `writer.go`, `levelhandle.go`, `stripped.go`
- Test: covered by Task 3's `subifd_test.go` (this task is verified by build + the existing suite; its own assertion lands in Task 3).

- [ ] **Step 1: Add the Option + writer field + Create wiring**

In `internal/tiff/streamwriter/options.go`, after the `YCbCrSubSampling []uint16` field:

```go
	// SampleFormat, when non-zero, is emitted as tag 339 (SHORT, count 1)
	// on every IFD. OME-TIFF sets it to 1 (unsigned integer).
	SampleFormat uint16

	// SubResolutionPyramid, when true, lays out pyramid levels ≥1 as
	// SubIFDs (330) of L0 instead of a flat top-level IFD chain (the
	// OME-TIFF sub-resolution convention). Default false leaves the flat
	// layout used by svs/tiff/cog-wsi untouched.
	SubResolutionPyramid bool
```

In `internal/tiff/streamwriter/writer.go`, after the `ycbcrSubSampling []uint16` struct field:

```go
	sampleFormat  uint16
	subResPyramid bool
```

In `Create`, after `ycbcrSubSampling: opts.YCbCrSubSampling,`:

```go
		sampleFormat:  opts.SampleFormat,
		subResPyramid: opts.SubResolutionPyramid,
```

- [ ] **Step 2: Emit SampleFormat in the tiled IFD builder**

In `internal/tiff/streamwriter/levelhandle.go` `buildLevelEntries`, immediately after the `b.AddShort(tiff.TagSamplesPerPixel, …)` line (around line 198):

```go
	if w.sampleFormat != 0 {
		b.AddShort(tiff.TagSampleFormat, []uint16{w.sampleFormat})
	}
```

- [ ] **Step 3: Emit SampleFormat in the stripped IFD builder**

In `internal/tiff/streamwriter/stripped.go` `buildStrippedEntries`, immediately after the `b.AddShort(tiff.TagSamplesPerPixel, …)` line (around line 70):

```go
	if w.sampleFormat != 0 {
		b.AddShort(tiff.TagSampleFormat, []uint16{w.sampleFormat})
	}
```

- [ ] **Step 4: Build + run the existing suite (no regression)**

Run: `go build ./... && go test ./internal/tiff/streamwriter/ -count=1`
Expected: PASS (no behavior change yet for callers that leave `SampleFormat=0`).

- [ ] **Step 5: Commit**

```bash
git add internal/tiff/streamwriter/options.go internal/tiff/streamwriter/writer.go internal/tiff/streamwriter/levelhandle.go internal/tiff/streamwriter/stripped.go
git commit -m "feat(streamwriter): emit SampleFormat (339) on every IFD when set

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: streamwriter SubIFD pyramid layout

**Files:**
- Modify: `internal/tiff/streamwriter/writer.go` (refactor Close, add SubIFD path)
- Test: `internal/tiff/streamwriter/subifd_test.go` (create)

**Background:** `Close` currently loops `w.imgs` in order, emitting each as a top-level IFD and patching a linear next-IFD chain (writer.go:177-231). `imageEntry.pyramidLevelIndex` is computed earlier in Close: 0 for L0, 1..n for sub-resolutions, -1 for associated/non-pyramid. We add a branch: when `w.subResPyramid` and an L0 exists, emit sub-resolutions first (capturing offsets), then L0 carrying a `SubIFDs` (330) tag with those offsets, then associated; the top-level chain becomes L0 → associated.

- [ ] **Step 1: Write the failing test**

Create `internal/tiff/streamwriter/subifd_test.go`:

```go
package streamwriter_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// readClassicIFD reads the entry count, a tag→firstValue map (using the
// inline value field), the raw 330 offset list, and the nextIFD pointer of
// the classic-TIFF IFD at byteOffset. Only valid for classic (non-Big) TIFF.
func readClassicIFD(t *testing.T, b []byte, at uint32) (tags map[uint16]uint32, subIFDs []uint32, nextIFD uint32) {
	t.Helper()
	n := binary.LittleEndian.Uint16(b[at:])
	tags = map[uint16]uint32{}
	p := at + 2
	for i := 0; i < int(n); i++ {
		e := b[p : p+12]
		tag := binary.LittleEndian.Uint16(e[0:])
		typ := binary.LittleEndian.Uint16(e[2:])
		cnt := binary.LittleEndian.Uint32(e[4:])
		val := binary.LittleEndian.Uint32(e[8:])
		tags[tag] = val
		if tag == 330 { // SubIFDs: LONG array
			if cnt == 1 {
				subIFDs = []uint32{val}
			} else {
				off := val
				for k := uint32(0); k < cnt; k++ {
					subIFDs = append(subIFDs, binary.LittleEndian.Uint32(b[off+k*4:]))
				}
			}
			_ = typ
		}
		p += 12
	}
	nextIFD = binary.LittleEndian.Uint32(b[p:])
	return tags, subIFDs, nextIFD
}

// TestSubIFDPyramidLayout: a 3-level pyramid + 1 associated image written
// with SubResolutionPyramid=true puts L1/L2 in L0's SubIFDs (330), keeps
// only L0→associated in the top-level chain, and tags every IFD with
// SampleFormat=1.
func TestSubIFDPyramidLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "o.tiff")
	w, err := streamwriter.Create(path, streamwriter.Options{
		BigTIFF:              tiff.BigTIFFOff,
		SubResolutionPyramid: true,
		SampleFormat:         1,
		FormatName:           "ome-tiff",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dims := []uint32{16, 8, 4}
	for _, d := range dims {
		l, err := w.AddLevel(streamwriter.LevelSpec{
			ImageWidth: d, ImageHeight: d, TileWidth: d, TileHeight: d,
			Compression: tiff.CompressionNone, Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			WSIImageType: tiff.WSIImageTypePyramid,
		})
		if err != nil {
			t.Fatalf("AddLevel %d: %v", d, err)
		}
		l.WriteTile(0, 0, make([]byte, int(d*d*3)))
	}
	if err := w.AddStripped(streamwriter.StrippedSpec{
		Width: 8, Height: 8, RowsPerStrip: 8, BitsPerSample: []uint16{8, 8, 8},
		SamplesPerPixel: 3, Photometric: 2, Compression: tiff.CompressionNone,
		StripBytes: make([]byte, 8*8*3), WSIImageType: "label",
	}); err != nil {
		t.Fatalf("AddStripped: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	firstIFD := binary.LittleEndian.Uint32(b[4:]) // classic TIFF header
	l0tags, subIFDs, l0next := readClassicIFD(t, b, firstIFD)

	if l0tags[256] != 16 {
		t.Errorf("first IFD ImageWidth = %d, want 16 (L0)", l0tags[256])
	}
	if l0tags[339] != 1 {
		t.Errorf("L0 SampleFormat = %d, want 1", l0tags[339])
	}
	if len(subIFDs) != 2 {
		t.Fatalf("L0 SubIFDs count = %d, want 2", len(subIFDs))
	}
	// Sub-resolutions ordered largest→smallest: 8 then 4.
	s1, _, _ := readClassicIFD(t, b, subIFDs[0])
	s2, _, _ := readClassicIFD(t, b, subIFDs[1])
	if s1[256] != 8 || s2[256] != 4 {
		t.Errorf("SubIFD widths = %d,%d, want 8,4", s1[256], s2[256])
	}
	if s1[339] != 1 || s2[339] != 1 {
		t.Errorf("SubIFD SampleFormat = %d,%d, want 1,1", s1[339], s2[339])
	}
	// Top-level chain: L0 → associated → end. Exactly one hop, to the label.
	if l0next == 0 {
		t.Fatalf("L0 nextIFD = 0, want the associated IFD")
	}
	assoc, _, assocNext := readClassicIFD(t, b, l0next)
	if assoc[256] != 8 {
		t.Errorf("associated ImageWidth = %d, want 8", assoc[256])
	}
	if assoc[339] != 1 {
		t.Errorf("associated SampleFormat = %d, want 1", assoc[339])
	}
	if assocNext != 0 {
		t.Errorf("associated nextIFD = %d, want 0 (end of chain)", assocNext)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tiff/streamwriter/ -run SubIFDPyramidLayout -v`
Expected: FAIL — with the flat layout, the first IFD has no tag 330 (`SubIFDs count = 0`), and the top-level chain still threads L1/L2.

- [ ] **Step 3: Refactor per-IFD emission into `emitIFD`**

In `internal/tiff/streamwriter/writer.go`, add this method (place just before `Close`):

```go
// emitIFD builds, encodes, and writes one IFD at the current offset and
// returns its start offset plus the file offset of its next-IFD pointer
// field (for chain patching). When subIFDs is non-empty, a SubIFDs (330)
// tag listing those child offsets is added before encoding (LONG8 on
// BigTIFF, LONG on classic).
func (w *Writer) emitIFD(entry *imageEntry, isL0 bool, subIFDs []uint64) (ifdStart uint64, nextPatchAt int64, err error) {
	b, err := w.buildEntryBuilder(entry, isL0)
	if err != nil {
		return 0, 0, err
	}
	if len(subIFDs) > 0 {
		if w.bigtiff {
			b.AddLong8(tiff.TagSubIFDs, subIFDs)
		} else {
			v := make([]uint32, len(subIFDs))
			for i, o := range subIFDs {
				v[i] = uint32(o)
			}
			b.AddLong(tiff.TagSubIFDs, v)
		}
	}
	ifdStart = uint64(w.off)
	ifd, ext, err := b.Encode(ifdStart)
	if err != nil {
		return 0, 0, err
	}
	if _, err = w.appendBytes(ifd); err != nil {
		return 0, 0, err
	}
	if len(ext) > 0 {
		if _, err = w.appendBytes(ext); err != nil {
			return 0, 0, err
		}
	}
	nextPtrSize := int64(4)
	if w.bigtiff {
		nextPtrSize = 8
	}
	nextPatchAt = int64(ifdStart) + int64(len(ifd)) - nextPtrSize
	return ifdStart, nextPatchAt, nil
}
```

- [ ] **Step 4: Branch Close between the flat and SubIFD layouts**

In `internal/tiff/streamwriter/writer.go` `Close`, replace the existing emission+chaining block (the `ifdStarts`/`nextIFDPatchAt` loop at lines ~177-231, through the header first-IFD patch) with a dispatch to two helpers. Replace from the comment `// Emit each IFD in order; record the offset…` down to (but NOT including) the `if err = w.f.Sync(); …` line with:

```go
	var firstIFD uint64
	var perr error
	if w.subResPyramid && pyramidCount > 0 {
		firstIFD, perr = w.closeSubIFDLayout()
	} else {
		firstIFD, perr = w.closeFlatLayout()
	}
	if perr != nil {
		return perr
	}
	firstAt := int64(4)
	if w.bigtiff {
		firstAt = 8
	}
	if err = w.patchOffset(firstAt, firstIFD); err != nil {
		return fmt.Errorf("streamwriter: patch first-IFD: %w", err)
	}
```

Then add the two helpers right after `Close`:

```go
// closeFlatLayout emits every image as a top-level IFD in w.imgs order and
// chains them linearly. Returns the first IFD offset. This is the layout
// for every format except OME (SubResolutionPyramid off).
func (w *Writer) closeFlatLayout() (uint64, error) {
	ifdStarts := make([]uint64, len(w.imgs))
	nextPatchAt := make([]int64, len(w.imgs))
	for i, entry := range w.imgs {
		start, patchAt, err := w.emitIFD(entry, i == 0, nil)
		if err != nil {
			return 0, fmt.Errorf("streamwriter: emit IFD %d: %w", i, err)
		}
		ifdStarts[i] = start
		nextPatchAt[i] = patchAt
	}
	for i := 0; i < len(w.imgs)-1; i++ {
		if err := w.patchOffset(nextPatchAt[i], ifdStarts[i+1]); err != nil {
			return 0, fmt.Errorf("streamwriter: patch next-IFD for %d: %w", i, err)
		}
	}
	if len(ifdStarts) == 0 {
		return 0, nil
	}
	return ifdStarts[0], nil
}

// closeSubIFDLayout emits sub-resolution pyramid levels as SubIFDs of L0
// (OME-TIFF convention): children first (capturing offsets), then L0 with a
// SubIFDs (330) tag, then associated images. The top-level next-IFD chain is
// L0 → associated…; sub-resolution IFDs are reachable only via 330 and keep
// nextIFD = 0. Returns L0's offset (the first IFD).
func (w *Writer) closeSubIFDLayout() (uint64, error) {
	var l0 *imageEntry
	var subRes, assoc []*imageEntry
	for _, e := range w.imgs {
		switch {
		case e.pyramidLevelIndex == 0:
			l0 = e
		case e.pyramidLevelIndex > 0:
			subRes = append(subRes, e) // w.imgs order = L1..Ln = largest→smallest
		default:
			assoc = append(assoc, e)
		}
	}
	if l0 == nil {
		// No full-resolution pyramid image; fall back to the flat layout.
		return w.closeFlatLayout()
	}
	subOffsets := make([]uint64, 0, len(subRes))
	for _, e := range subRes {
		start, _, err := w.emitIFD(e, false, nil) // not chained; nextIFD stays 0
		if err != nil {
			return 0, fmt.Errorf("streamwriter: emit sub-resolution IFD: %w", err)
		}
		subOffsets = append(subOffsets, start)
	}
	l0Start, l0Patch, err := w.emitIFD(l0, true, subOffsets)
	if err != nil {
		return 0, fmt.Errorf("streamwriter: emit L0 IFD: %w", err)
	}
	topStarts := []uint64{l0Start}
	topPatch := []int64{l0Patch}
	for _, e := range assoc {
		start, patch, err := w.emitIFD(e, false, nil)
		if err != nil {
			return 0, fmt.Errorf("streamwriter: emit associated IFD: %w", err)
		}
		topStarts = append(topStarts, start)
		topPatch = append(topPatch, patch)
	}
	for i := 0; i < len(topStarts)-1; i++ {
		if err := w.patchOffset(topPatch[i], topStarts[i+1]); err != nil {
			return 0, fmt.Errorf("streamwriter: patch top-level next-IFD %d: %w", i, err)
		}
	}
	return l0Start, nil
}
```

Note: `pyramidCount` is the local already computed at the top of `Close`; keep it in scope. The `ifdStarts`/`nextIFDPatchAt` local declarations that preceded the replaced block are removed (now inside `closeFlatLayout`).

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/tiff/streamwriter/ -run SubIFDPyramidLayout -v`
Expected: PASS.

- [ ] **Step 6: Run the full streamwriter suite (flat layout unchanged)**

Run: `go test ./internal/tiff/streamwriter/ -count=1 && go vet ./internal/tiff/streamwriter/ && gofmt -l internal/tiff/streamwriter/`
Expected: all PASS; gofmt prints nothing for the files you touched.

- [ ] **Step 7: Commit**

```bash
git add internal/tiff/streamwriter/writer.go internal/tiff/streamwriter/subifd_test.go
git commit -m "feat(streamwriter): SubIFD pyramid layout for OME-TIFF sub-resolutions

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: OME-XML preamble + associated-image enumeration

**Files:**
- Modify: `cmd/wsitools/ome_imagedesc.go`
- Test: `cmd/wsitools/ome_imagedesc_test.go` (create)

**Background:** `SyntheticOMEDescription(l0W, l0H uint32, mppX, mppY float64, name, srcSoftware string) string` builds a single-`<Image>` document. We add the spec preamble comment and a slice of associated images, each rendered as an `<Image Name="…">` whose `<TiffData IFD="k">` points at its top-level IFD position (k = 1 + index, since L0 is IFD 0).

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/ome_imagedesc_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestSyntheticOMEDescriptionAssociated(t *testing.T) {
	assoc := []OMEAssoc{
		{Name: "label", W: 100, H: 80},
		{Name: "macro", W: 600, H: 400},
	}
	xml := SyntheticOMEDescription(2220, 2967, 0.5, 0.5, "Image", "Aperio", assoc)

	if !strings.Contains(xml, "<!-- Warning: this comment is an OME-XML metadata block") {
		t.Errorf("missing OME preamble comment:\n%s", xml)
	}
	if !strings.HasSuffix(strings.TrimSpace(xml), "OME>") {
		t.Errorf("OME-XML must end with OME> for detection:\n%s", xml)
	}
	// Main image at IFD 0.
	if !strings.Contains(xml, `Name="Image"`) || !strings.Contains(xml, `IFD="0"`) {
		t.Errorf("missing main image / IFD=0:\n%s", xml)
	}
	// Associated images at IFD 1 and 2, in order.
	if !strings.Contains(xml, `Name="label"`) || !strings.Contains(xml, `IFD="1"`) {
		t.Errorf("missing label at IFD=1:\n%s", xml)
	}
	if !strings.Contains(xml, `Name="macro"`) || !strings.Contains(xml, `IFD="2"`) {
		t.Errorf("missing macro at IFD=2:\n%s", xml)
	}
	if got := strings.Count(xml, "<Image "); got != 3 {
		t.Errorf("Image count = %d, want 3 (main + 2 associated)", got)
	}
	// Associated Pixels carry their own dims.
	if !strings.Contains(xml, `SizeX="600" SizeY="400"`) {
		t.Errorf("macro Pixels missing its dims:\n%s", xml)
	}
}

func TestSyntheticOMEDescriptionNoAssociated(t *testing.T) {
	xml := SyntheticOMEDescription(10, 10, 0, 0, "Image", "", nil)
	if got := strings.Count(xml, "<Image "); got != 1 {
		t.Errorf("Image count = %d, want 1", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(xml), "OME>") {
		t.Errorf("must end with OME>:\n%s", xml)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/wsitools/ -run SyntheticOMEDescription -v`
Expected: FAIL — `SyntheticOMEDescription` has 6 params (no `assoc`), and `OMEAssoc` is undefined.

- [ ] **Step 3: Add the OMEAssoc type + preamble + associated rendering**

In `cmd/wsitools/ome_imagedesc.go`, add the type and a preamble constant near the top (after the imports):

```go
// OMEAssoc describes one associated image (label/macro/thumbnail) to
// enumerate in the OME-XML. Name MUST be one of "label"/"macro"/"thumbnail"
// (the reader classifies any other Name as a main pyramid). W/H are the
// associated image's pixel dimensions.
type OMEAssoc struct {
	Name string
	W, H uint32
}

// omePreamble is the OME-TIFF spec's recommended ImageDescription comment.
const omePreamble = `<!-- Warning: this comment is an OME-XML metadata block, which contains crucial dimensional parameters and other important metadata. Please edit cautiously (if at all), and back up the original data before doing so. -->`
```

Change the signature and body of `SyntheticOMEDescription` to accept `assoc []OMEAssoc`, emit the preamble after the XML declaration, and append an `<Image>` per associated entry with `IFD = 1 + index`. Replace the whole function with:

```go
func SyntheticOMEDescription(l0W, l0H uint32, mppX, mppY float64, name, srcSoftware string, assoc []OMEAssoc) string {
	if name == "" {
		name = "Image"
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(omePreamble + "\n")
	b.WriteString(`<OME xmlns="http://www.openmicroscopy.org/Schemas/OME/2016-06"`)
	b.WriteString(` Creator="wsitools/` + Version)
	if srcSoftware != "" {
		b.WriteString(` (from ` + xmlEscape(srcSoftware) + `)`)
	}
	b.WriteString(`">` + "\n")
	// Main pyramid image at IFD 0.
	writeOMEImage(&b, 0, name, l0W, l0H, mppX, mppY)
	// Associated images at IFD 1..n, in order.
	for i, a := range assoc {
		writeOMEImage(&b, 1+i, a.Name, a.W, a.H, 0, 0)
	}
	b.WriteString(`</OME>`)
	return b.String()
}

// writeOMEImage writes one <Image>/<Pixels> block mapping to top-level IFD
// ifd. mppX/mppY are emitted as PhysicalSize only when non-zero.
func writeOMEImage(b *strings.Builder, ifd int, name string, w, h uint32, mppX, mppY float64) {
	fmt.Fprintf(b, `  <Image ID="Image:%d" Name="%s">`+"\n", ifd, xmlEscape(name))
	fmt.Fprintf(b, `    <Pixels ID="Pixels:%d:0" DimensionOrder="XYCZT" Type="uint8"`, ifd)
	fmt.Fprintf(b, ` SizeX="%d" SizeY="%d" SizeZ="1" SizeC="3" SizeT="1"`, w, h)
	if mppX != 0 {
		fmt.Fprintf(b, ` PhysicalSizeX="%g" PhysicalSizeXUnit="µm"`, mppX)
	}
	if mppY != 0 {
		fmt.Fprintf(b, ` PhysicalSizeY="%g" PhysicalSizeYUnit="µm"`, mppY)
	}
	b.WriteString(`>` + "\n")
	b.WriteString(`      <Channel ID="Channel:` + fmt.Sprint(ifd) + `:0" Name="Red" SamplesPerPixel="1"/>` + "\n")
	b.WriteString(`      <Channel ID="Channel:` + fmt.Sprint(ifd) + `:1" Name="Green" SamplesPerPixel="1"/>` + "\n")
	b.WriteString(`      <Channel ID="Channel:` + fmt.Sprint(ifd) + `:2" Name="Blue" SamplesPerPixel="1"/>` + "\n")
	fmt.Fprintf(b, `      <TiffData FirstC="0" FirstZ="0" FirstT="0" IFD="%d" PlaneCount="1"/>`+"\n", ifd)
	b.WriteString(`    </Pixels>` + "\n")
	b.WriteString(`  </Image>` + "\n")
}
```

(`fmt` is already imported in this file.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/wsitools/ -run SyntheticOMEDescription -v`
Expected: PASS. (The two existing callers in `convert_tiff.go` will not compile yet — that's fixed in Task 5. To check just this file's logic now, the test compiles the whole package, so temporarily expect a build error from `convert_tiff.go`'s old 6-arg calls; you fix those in Task 5. If you prefer green here, do Step 3 of Task 5 first, then return.)

- [ ] **Step 5: Commit** (after Task 5's call sites compile — commit Task 4 + Task 5 together if needed, or commit now if you applied the Task 5 call-site change)

```bash
git add cmd/wsitools/ome_imagedesc.go cmd/wsitools/ome_imagedesc_test.go
git commit -m "feat(convert): OME-XML preamble + associated-image enumeration

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Wire `convert --to ome-tiff` + integration tests

**Files:**
- Modify: `cmd/wsitools/convert_tiff.go`
- Test: `cmd/wsitools/convert_ome_test.go` (create)

**Background:** Two callers build OME output: `runConvertTIFFTileCopy` (~line 124-148, `streamwriter.Options` at ~90) and `runConvertTIFFReencode` (~line 349-373, Options at ~320). Both call `SyntheticOMEDescription(...)` for non-OME sources and `writeAssociatedImages(...)`. We (a) set `SubResolutionPyramid` + `SampleFormat` for the ome-tiff container, (b) compute the recognized associated list and pass it to the OME-XML builder, and (c) for the synthetic OME path, write only those recognized associated IFDs in the same order.

- [ ] **Step 1: Write the failing integration tests**

Create `cmd/wsitools/convert_ome_test.go`:

```go
package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestConvertOMEPyramidRoundTrips is the dropped-pyramid regression: a
// multi-level source converted to ome-tiff must read back with the SAME
// number of pyramid levels (sub-resolutions stored as SubIFDs), not 1.
func TestConvertOMEPyramidRoundTrips(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1.svs")
	out := filepath.Join(t.TempDir(), "out.ome.tiff")

	if o, err := exec.Command(bin, "convert", "--to", "ome-tiff", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}

	// Source level count.
	srcInfo, err := exec.Command(bin, "info", src).CombinedOutput()
	if err != nil {
		t.Fatalf("info src: %v\n%s", err, srcInfo)
	}
	outInfo, err := exec.Command(bin, "info", out).CombinedOutput()
	if err != nil {
		t.Fatalf("info out: %v\n%s", err, outInfo)
	}
	srcLevels := strings.Count(string(srcInfo), "\n  L")
	outLevels := strings.Count(string(outInfo), "\n  L")
	if outLevels != srcLevels {
		t.Errorf("ome-tiff level count = %d, want %d (source):\nSOURCE:\n%s\nOUT:\n%s",
			outLevels, srcLevels, srcInfo, outInfo)
	}
	if outLevels < 2 {
		t.Fatalf("expected a multi-level pyramid, got %d", outLevels)
	}
}

// TestConvertOMEStructure: L0 carries SubIFDs (330) + SampleFormat (339), and
// the OME-XML enumerates the associated images.
func TestConvertOMEStructure(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1.svs")
	out := filepath.Join(t.TempDir(), "out.ome.tiff")

	if o, err := exec.Command(bin, "convert", "--to", "ome-tiff", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ifd0 := dumpIFD0Raw(t, bin, out) // helper from convert_aperio_tags_test.go
	if !strings.Contains(ifd0, "SubIFDs") {
		t.Errorf("L0 missing SubIFDs:\n%s", ifd0)
	}
	if !strings.Contains(ifd0, "SampleFormat") {
		t.Errorf("L0 missing SampleFormat:\n%s", ifd0)
	}
	// Associated images visible through opentile.
	info, _ := exec.Command(bin, "info", out).CombinedOutput()
	s := strings.ToLower(string(info))
	if !strings.Contains(s, "label") && !strings.Contains(s, "macro") {
		t.Errorf("associated images not surfaced by reader:\n%s", info)
	}
}
```

- [ ] **Step 2: Build and run to verify failure**

Run: `make build && WSI_TOOLS_TESTDIR=/Users/cornish/GitHub/wsitools/sample_files go test ./cmd/wsitools/ -run 'ConvertOME' -v`
Expected: FAIL — `TestConvertOMEPyramidRoundTrips` reports `level count = 1, want 6`; `TestConvertOMEStructure` reports missing SubIFDs. (If the package doesn't compile because of Task 4's 7-arg `SyntheticOMEDescription` vs the old 6-arg call sites, do Step 3 first.)

- [ ] **Step 3: Add the associated-name mapping + spec helper**

In `cmd/wsitools/convert_tiff.go`, add near `writeAssociatedImages` (which is around line 663):

```go
// omeAssocName maps a wsitools associated-image kind to the OME-XML Image
// Name the reader recognizes ("label"/"macro"/"thumbnail"), or "" if the
// kind has no OME equivalent and must be omitted from OME output (otherwise
// the reader would mis-classify it as a second pyramid).
func omeAssocName(kind string) string {
	switch kind {
	case "label":
		return "label"
	case "macro", "overview":
		return "macro"
	case "thumbnail":
		return "thumbnail"
	}
	return ""
}

// omeAssociatedSpecs returns the recognized associated images for OME output,
// in src.Associated() order — the SAME order writeAssociatedImages writes
// them, so OME-XML <Image> positions line up with top-level IFD positions.
func omeAssociatedSpecs(src source.Source) []OMEAssoc {
	var out []OMEAssoc
	for _, a := range src.Associated() {
		name := omeAssocName(a.Kind())
		if name == "" {
			slog.Debug("ome: dropping associated image with no OME mapping", "kind", a.Kind())
			continue
		}
		out = append(out, OMEAssoc{Name: name, W: uint32(a.Size().X), H: uint32(a.Size().Y)})
	}
	return out
}
```

- [ ] **Step 4: Filter associated IFDs for the synthetic OME path**

In `writeAssociatedImages` (`cmd/wsitools/convert_tiff.go`), add a skip at the top of the loop body so the synthetic OME path writes only the recognized kinds (keeping IFD order aligned with `omeAssociatedSpecs`). Change the loop header region:

```go
func writeAssociatedImages(src source.Source, w *streamwriter.Writer, container string, omeSynthetic bool) error {
	for _, a := range src.Associated() {
		if container == "ome-tiff" && omeSynthetic && omeAssocName(a.Kind()) == "" {
			continue
		}
		bs, err := a.Bytes()
```

(Leave the rest of the function unchanged. The new `omeSynthetic` parameter is threaded from the callers below.)

- [ ] **Step 5: Wire the tile-copy path**

In `runConvertTIFFTileCopy` (`cmd/wsitools/convert_tiff.go`):

(a) In the `opts := streamwriter.Options{…}` literal (~line 90), the ome path needs the two new options. After the literal, before `streamwriter.Create`, add:

```go
	if container == "ome-tiff" {
		opts.SubResolutionPyramid = true
		opts.SampleFormat = 1
	}
```

(b) In the ImageDescription `switch`, the `case "ome-tiff":` synthetic branch currently calls `SyntheticOMEDescription(…)` with 6 args and no associated images. Update both the OME-source-verbatim and synthetic branches:

```go
	case "ome-tiff":
		if src.Format() == string(opentile.FormatOMETIFF) {
			srcImageDesc = src.SourceImageDescription()
		} else {
			srcImageDesc = SyntheticOMEDescription(
				uint32(l0.Size().X), uint32(l0.Size().Y),
				md.MPP, md.MPP, "Image", srcSoft,
				omeAssociatedSpecs(src),
			)
		}
```

(c) Update the `writeAssociatedImages` call (~line 240) to pass `omeSynthetic`. The synthetic OME path is when container is ome-tiff and the source is NOT OME:

```go
	if !cvNoAssociated {
		omeSynthetic := container == "ome-tiff" && src.Format() != string(opentile.FormatOMETIFF)
		if err := writeAssociatedImages(src, w, container, omeSynthetic); err != nil {
			w.Abort()
			return err
		}
	}
```

- [ ] **Step 6: Wire the re-encode path**

In `runConvertTIFFReencode`, mirror Step 5:

(a) After the `opts := streamwriter.Options{…}` literal (~line 320):

```go
	if resolvedContainer == "ome-tiff" {
		opts.SubResolutionPyramid = true
		opts.SampleFormat = 1
	}
```

(b) Update the `case "ome-tiff":` synthetic branch (~line 386) to pass `omeAssociatedSpecs(src)` as the 7th arg (same as Step 5b but with `resolvedContainer`).

(c) Update the `writeAssociatedImages` call (~line 386) to pass `omeSynthetic := resolvedContainer == "ome-tiff" && src.Format() != string(opentile.FormatOMETIFF)`.

- [ ] **Step 7: Build and run to verify pass**

Run: `make build && WSI_TOOLS_TESTDIR=/Users/cornish/GitHub/wsitools/sample_files go test ./cmd/wsitools/ -run 'ConvertOME|SyntheticOMEDescription' -v`
Expected: PASS — pyramid round-trips with the source's level count; L0 has SubIFDs + SampleFormat; associated images surface.

- [ ] **Step 8: No-regression sweep + cleanliness**

Run: `go vet ./cmd/wsitools/ && gofmt -l cmd/wsitools/*.go` (clean), then `WSI_TOOLS_TESTDIR=/Users/cornish/GitHub/wsitools/sample_files go test ./cmd/wsitools/ -run 'Convert' -count=1` and report the result (svs/tiff conversions unaffected).

- [ ] **Step 9: Commit**

```bash
git add cmd/wsitools/convert_tiff.go cmd/wsitools/convert_ome_test.go cmd/wsitools/ome_imagedesc.go cmd/wsitools/ome_imagedesc_test.go
git commit -m "feat(convert): conformant OME-TIFF (SubIFD pyramid + associated images + SampleFormat)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Documentation

**Files:**
- Modify: `docs/tiff-tags.md`, `docs/roadmap.md`

- [ ] **Step 1: Add the OME-TIFF writer note**

Append to `docs/tiff-tags.md` (after the "## SVS writer tag profile" section):

```markdown
## OME-TIFF writer

`convert --to ome-tiff` follows the OME-TIFF sub-resolution convention
(grounding: `docs/references/ome-tiff-spec-notes.md`):

- **Pyramid sub-resolutions as SubIFDs (330).** The full-resolution level is
  the only pyramid IFD in the top-level chain; levels L1..Ln are stored as
  SubIFDs of L0, ordered largest→smallest, each with `NewSubfileType` (254)
  bit 0 = 1. This is what makes a multi-level OME-TIFF read back as a pyramid
  (a flat top-level pyramid would be seen as a single level).
- **Positional Image↔IFD invariant.** Readers map the k-th OME-XML `<Image>`
  to the k-th top-level IFD. wsitools emits `<Image>` in top-level IFD order:
  the main pyramid (IFD 0), then associated images.
- **Associated images** are enumerated as `<Image Name="label|macro|
  thumbnail">` with `<TiffData IFD="k">` at their top-level position. Only
  those three reserved names are recognized; any other Name would be read as a
  second pyramid, so wsitools maps `overview`→`macro` and drops kinds with no
  OME equivalent.
- **SampleFormat (339) = 1** on every IFD, and the OME-XML carries the spec's
  preamble comment.
```

- [ ] **Step 2: Update the roadmap**

In `docs/roadmap.md`, under the most recent "Shipped" version block (or add a new dated bullet in the in-progress section), add:

```markdown
- `convert --to ome-tiff` conformance: pyramid sub-resolutions now stored as
  SubIFDs (330) of L0 (previously written as orphan top-level IFDs → readers
  saw only L0); associated images enumerated in the OME-XML; SampleFormat
  (339) + OME-XML preamble added. Grounded in the OME-TIFF spec
  (`docs/references/ome-tiff-spec-notes.md`).
```

- [ ] **Step 3: Commit**

```bash
git add docs/tiff-tags.md docs/roadmap.md
git commit -m "docs(ome): document OME-TIFF writer conformance (SubIFD pyramid)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage:**
- R1 (pyramid as SubIFDs, largest→smallest, NewSubfileType bit 0) → Task 3 (`closeSubIFDLayout`, sub-res emitted in L1..Ln order; NewSubfileType already produced by `newSubfileTypeForLevel` for non-svs). ✓
- R2 (top-level chain L0→associated; sub-res not chained) → Task 3. ✓
- R3 (OME-XML Image order = IFD order; recognized Names; associated `<Image>` with TiffData IFD=position) → Task 4 (builder) + Task 5 (`omeAssociatedSpecs` order, synthetic-path filter). ✓
- R4 (SampleFormat 339 = 1 on every IFD) → Task 2 (both IFD builders) + Task 5 (Options). ✓
- R5 (OME-XML preamble) → Task 4. ✓
- R6 (scope isolation) → flag-gated; Task 3 leaves `closeFlatLayout` for all other formats; Task 5 sets options only for ome-tiff; verified by Task 5 Step 8 regression sweep. ✓
- Testing/Docs → Tasks 3/5 tests, Task 6 docs. ✓

**2. Placeholder scan:** No TBD/TODO; every code step has complete code and exact commands. The Task 4↔5 compile-order coupling is called out explicitly (the 6→7-arg signature change breaks the call sites until Task 5 Step 5b/6b) rather than left implicit.

**3. Type consistency:** `OMEAssoc{Name string; W, H uint32}` defined in Task 4, constructed in Task 5 (`omeAssociatedSpecs`). `SyntheticOMEDescription(…, assoc []OMEAssoc)` 7-arg signature used consistently in Task 4 test and Task 5 call sites. `emitIFD`/`closeFlatLayout`/`closeSubIFDLayout` defined and used in Task 3. `writeAssociatedImages(src, w, container, omeSynthetic bool)` — new 4th param defined in Task 5 Step 4 and passed in Steps 5c/6c. `omeAssocName` used by both `omeAssociatedSpecs` and the synthetic-path filter. `dumpIFD0Raw` reused from the existing `convert_aperio_tags_test.go` (same package). `Options.SubResolutionPyramid`/`SampleFormat` defined in Task 2, set in Task 5.

**Note for implementer:** Tasks 4 and 5 are compile-coupled (the `SyntheticOMEDescription` arity change). If you implement strictly task-by-task, the package won't build between Task 4 Step 3 and Task 5 Step 5b. Either implement Task 4 Step 3 and Task 5 Step 5b/6b back-to-back before running tests, or commit Task 4 and Task 5 together. The tests themselves are independent and both must pass at the end of Task 5.
