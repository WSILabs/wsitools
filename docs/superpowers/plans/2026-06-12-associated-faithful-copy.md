# Faithful associated-image copy across TIFF writers — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop corrupting associated images (LZW labels / abbreviated-JPEG thumbnails) in `convert --to {cog-wsi,svs,tiff,ome-tiff}` + `--factor` by re-emitting them byte-faithfully via opentile-go v0.39.0 `Slide.AssociatedSourceOf` (verbatim source strips + Predictor/JPEGTables/multi-strip tags), instead of the abbreviated `a.Bytes()` passthrough.

**Architecture:** Bump opentile-go to v0.39.0; expose `AssociatedSourceOf` through the source layer; teach `streamwriter` and `cogwsiwriter` to write multi-strip associated images carrying Predictor (317) + JPEGTables (347); a shared faithful-builder (Source-or-decode) feeds both; rewire all TIFF-family associated consumers; regenerate the 5 corrupt cog-wsi fixtures.

**Tech Stack:** Go, opentile-go v0.39.0 (`AssociatedSource`/`AssociatedSourceOf`), `internal/tiff` EntryBuilder.

**Spec:** `docs/superpowers/specs/2026-06-12-associated-faithful-copy-design.md`

**Branch:** create `feat/associated-faithful-copy` off `main`. Never implement on `main`.

**Verified facts (read this session):**
- opentile-go v0.39.0: `type AssociatedSource struct { Strips [][]byte; Compression Compression; Predictor int; JPEGTables []byte; RowsPerStrip int; Samples int; Photometric int }`; `func (s *Slide) AssociatedSourceOf(a AssociatedImage) (AssociatedSource, bool)`. `ok=false` for synthesized/tiled/non-TIFF associated. Implemented by svs/generictiff/ometiff/philipstiff/bif/ndpi.
- `internal/source/opentile.go`: `opentileAssociated{a opentile.AssociatedImage, slide *opentile.Slide}` — **already holds the slide** (line ~135). `opentileSource.Associated()` builds `&opentileAssociated{a: a, slide: s.t}`.
- `internal/tiff/streamwriter/stripped.go`: `StrippedSpec` is single-strip (`StripBytes []byte`) with an `ExtraTags []tiff.RawTag` escape hatch; `AddStripped` writes one strip via `w.appendBytes`; `buildStrippedEntries` emits single StripOffsets/StripByteCounts via `b.AddTileOffsets`.
- `internal/tiff/cogwsiwriter/writer.go`: `AssociatedSpec` single-strip (`Bytes []byte`); `AddAssociated` stages in memory; `Close` builds `associatedLayoutInput{Bytes: uint32(len(a.spec.Bytes)), ...}` → `planLayout` → `populateAssocIFD(b, bigtiff, spec, dataOffset)` (writer.go:546) emits single StripOffsets/StripByteCounts, RowsPerStrip=Height, **no 317/347**. The **level** path already carries `JPEGTables` (precedent). `layout.go`: `associatedLayoutInput{Bytes, Width, Height, Compression, Type}`, `associatedLayoutPlan{IFDOffset, Reserved, DataOffset}`, `ifdSizeForAssociated(a, useBig)`.
- `internal/tiff` tag consts exist: `TagStripOffsets`, `TagStripByteCounts`, `TagRowsPerStrip`, `TagPredictor` (317), `TagJPEGTables` (347) — VERIFY exact names via `internal/tiff/tags.go` before use; add any missing const.

---

## Task 1: opentile-go v0.39.0 + `source.AssociatedImage.Source()` passthrough

**Files:** `go.mod`, `go.sum`, `internal/source/source.go`, `internal/source/opentile.go`, `internal/source/*_test.go`.

- [ ] **Step 1: Bump.** `go get github.com/wsilabs/opentile-go@v0.39.0 && go mod tidy`; `grep "opentile-go v" go.mod` → v0.39.0.

- [ ] **Step 2: Write the failing test** (`internal/source/associated_source_test.go`): open CMU-1-Small-Region.svs, for the `label` call `Source()`, assert `ok==true`, `len(Strips)>1`, `Predictor==2`, `Compression==CompressionLZW`.
```go
func TestAssociatedSourceLabel(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" { dir = "../../sample_files" }
	p := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(p); err != nil { t.Skip("no CMU fixture") }
	s, err := Open(p); if err != nil { t.Fatal(err) }
	defer s.Close()
	for _, a := range s.Associated() {
		if a.Type() != "label" { continue }
		src, ok := a.Source()
		if !ok { t.Fatal("label Source() ok=false, want faithful source") }
		if len(src.Strips) < 2 { t.Errorf("label strips=%d, want >1", len(src.Strips)) }
		if src.Predictor != 2 { t.Errorf("label predictor=%d, want 2", src.Predictor) }
	}
}
```

- [ ] **Step 3: Run, verify FAIL** — `go test ./internal/source/ -run TestAssociatedSourceLabel` → FAIL (undefined `Source`).

- [ ] **Step 4: Add to the interface** (`source.go`, after `Decode`):
```go
	// Source returns the faithful on-disk source form (verbatim strips + TIFF
	// tags) for byte-identical re-emission into a new standalone TIFF; ok=false
	// for synthesized / tiled / non-TIFF associated images (delegates to
	// opentile-go's Slide.AssociatedSourceOf, GH opentile-go#22).
	Source() (opentile.AssociatedSource, bool)
```
Ensure `opentile "github.com/wsilabs/opentile-go"` is imported in `source.go`.

- [ ] **Step 5: Implement passthrough** (`opentile.go`, near the other `opentileAssociated` methods):
```go
func (a *opentileAssociated) Source() (opentile.AssociatedSource, bool) {
	return a.slide.AssociatedSourceOf(a.a)
}
```

- [ ] **Step 6: Run + commit.**
```bash
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/source/ -run 'AssociatedSource|Associated' -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l internal/source/ ; go vet ./internal/source/
git add go.mod go.sum internal/source/source.go internal/source/opentile.go internal/source/associated_source_test.go
git commit -m "feat(source): AssociatedImage.Source() via opentile-go v0.39.0 (faithful strips+tags)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `streamwriter` multi-strip associated + Predictor/JPEGTables

**Files:** `internal/tiff/streamwriter/stripped.go`, `internal/tiff/streamwriter/stripped_test.go`. Also verify `internal/tiff/tags.go` has `TagPredictor`/`TagJPEGTables` (add if missing).

- [ ] **Step 1: Confirm/add tag consts.** `grep -n "TagPredictor\|TagJPEGTables\|317\|347" internal/tiff/tags.go`. If absent, add `TagPredictor TagID = 317` and `TagJPEGTables TagID = 347` (match the file's existing const style/type).

- [ ] **Step 2: Write the failing round-trip test** (`stripped_test.go`): build a 2-strip LZW image with Predictor=2 (use the existing `applyPredictor2` logic inline or a small known buffer) and a JPEG+JPEGTables image, write via the new multi-strip `AddStripped`, then open with opentile and assert the decoded pixels equal the input. Skeleton:
```go
func TestStrippedMultiStripFaithful(t *testing.T) {
	// 4x4 RGB, two 2-row strips, predictor 2, LZW — write, reopen via opentile, compare.
	// (Use a tiny deterministic image; encode strips with the same LZW the writer
	// expects to copy verbatim — i.e. feed pre-encoded strips, since AddStripped
	// copies verbatim.)
	// Build StrippedSpec with Strips, Predictor:2, Compression:LZW, RowsPerStrip:2.
	// dst := temp file; w := NewWriter(...); w.AddStripped(spec); w.Close().
	// open via opentile.OpenFile, find associated, Decode -> compare to original RGB.
}
```
NOTE: this test writes a minimal standalone TIFF with one stripped image as an associated. If the streamwriter requires at least one pyramid level, add a 1-tile L0. Keep it minimal; the assertion is decoded-pixels-equal.

- [ ] **Step 3: Run, verify FAIL.**

- [ ] **Step 4: Extend `StrippedSpec` + `AddStripped` + `buildStrippedEntries`** (`stripped.go`). Add multi-strip + predictor + JPEGTables:
```go
type StrippedSpec struct {
	Width, Height   uint32
	RowsPerStrip    uint32
	BitsPerSample   []uint16
	SamplesPerPixel uint16
	Photometric     uint16
	Compression     uint16
	Strips          [][]byte // one or more verbatim strip payloads (document order)
	Predictor       uint16   // tag 317; 0/1 = none, emit only if >1
	JPEGTables      []byte   // tag 347; emit only if non-nil
	NewSubfileType  uint32
	WSIImageType    string
	ExtraTags       []tiff.RawTag
}
```
(Keep a back-compat shim: if a caller still sets a single `StripBytes`, fold it into `Strips`. Simpler: migrate all callers to `Strips` in Task 5 and DELETE `StripBytes`.)

`imageEntry` gains `stripOffsets []uint64` and `stripCounts []uint64`. `AddStripped`:
```go
	if s.RowsPerStrip == 0 { s.RowsPerStrip = s.Height }
	if s.SamplesPerPixel == 0 { s.SamplesPerPixel = 3 }
	offs := make([]uint64, len(s.Strips))
	cnts := make([]uint64, len(s.Strips))
	for i, strip := range s.Strips {
		off, err := w.appendBytes(strip)
		if err != nil { return fmt.Errorf("streamwriter: write strip %d: %w", i, err) }
		offs[i] = off; cnts[i] = uint64(len(strip))
	}
	w.imgs = append(w.imgs, &imageEntry{strippedSpec: &s, stripOffsets: offs, stripCounts: cnts})
```
`buildStrippedEntries`: replace the single StripOffsets/StripByteCounts with the arrays, and emit 317/347:
```go
	b.AddLong(tiff.TagRowsPerStrip, []uint32{s.RowsPerStrip})
	if err := b.AddTileOffsets(tiff.TagStripOffsets, entry.stripOffsets); err != nil { return nil, err }
	if err := b.AddTileOffsets(tiff.TagStripByteCounts, entry.stripCounts); err != nil { return nil, err }
	b.AddShort(tiff.TagPlanarConfiguration, []uint16{1})
	if s.Predictor > 1 { b.AddShort(tiff.TagPredictor, []uint16{s.Predictor}) }
	if len(s.JPEGTables) > 0 { b.AddUndefined(tiff.TagJPEGTables, s.JPEGTables) } // VERIFY EntryBuilder has an UNDEFINED/byte-blob adder; else use AddRaw
```
(Confirm the EntryBuilder method for an UNDEFINED byte array — grep `func (b *EntryBuilder)` in `internal/tiff/`. Use the matching adder; JPEGTables is TIFF type UNDEFINED (7).)

- [ ] **Step 5: Run, verify PASS** (`go test ./internal/tiff/streamwriter/ -run Faithful -v`). gofmt + vet.

- [ ] **Step 6: Commit.**
```bash
git add internal/tiff/streamwriter/stripped.go internal/tiff/streamwriter/stripped_test.go internal/tiff/tags.go
git commit -m "feat(streamwriter): multi-strip associated images with Predictor(317)+JPEGTables(347)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `cogwsiwriter` multi-strip associated + Predictor/JPEGTables

**Files:** `internal/tiff/cogwsiwriter/writer.go`, `internal/tiff/cogwsiwriter/layout.go`, `internal/tiff/cogwsiwriter/writer_test.go`.

- [ ] **Step 1: Write the failing round-trip test** (`writer_test.go`): same shape as Task 2 step 2 but via `cogwsiwriter` — one L0 tile + one associated `label` built from multi-strip LZW+Predictor2; `Close`; reopen via opentile; decoded label == input. Assert FAIL first.

- [ ] **Step 2: Extend `AssociatedSpec`** (writer.go):
```go
type AssociatedSpec struct {
	Type            string
	Width, Height   uint32
	Compression     uint16
	Photometric     uint16
	BitsPerSample   []uint16
	SamplesPerPixel uint16
	Strips          [][]byte // verbatim source strips (document order)
	Predictor       uint16   // tag 317; emit only if >1
	JPEGTables      []byte   // tag 347; emit only if non-nil
	RowsPerStrip    uint32   // tag 278; 0 => Height (single strip)
	Tiled           bool
}
```
(Delete the old `Bytes []byte`; migrate callers in Task 5. If a transitional shim is easier, keep `Bytes` and treat as `Strips==[][]byte{Bytes}` when `Strips==nil`.)

- [ ] **Step 3: Extend the layout input/plan** (layout.go). `associatedLayoutInput` carries per-strip counts + extras:
```go
type associatedLayoutInput struct {
	StripBytes    []uint32 // per-strip lengths (sum = total data)
	JPEGTables    uint32   // len of JPEGTables (0 if none)
	Predictor     uint16
	Width, Height uint32
	Compression   uint16
	Type          string
}
```
`associatedLayoutPlan` keeps `DataOffset` = offset of the first strip (strips written contiguously). Update `ifdSizeForAssociated` to size: N StripOffsets + N StripByteCounts entries (external when the array doesn't fit inline per TIFF rules — follow how the level path sizes its tile-offset arrays), the JPEGTables external bytes (if any), and the inline Predictor short. (Grep `ifdSizeForAssociated` and mirror `ifdSizeForLevel`'s array/external accounting.)

- [ ] **Step 4: Build `associatedLayoutInput` in `Close`** (writer.go ~235): compute per-strip lengths from `a.spec.Strips`, pass `JPEGTables`, `Predictor`.

- [ ] **Step 5: Write all strips in `Close`** where associated data is currently written (find where `a.spec.Bytes` is written to `DataOffset`): write each strip contiguously starting at `plan.Associated[i].DataOffset`; track running offset for `populateAssocIFD`.

- [ ] **Step 6: Update `populateAssocIFD`** (writer.go:546) to emit arrays + 317/347:
```go
func populateAssocIFD(b *tiff.EntryBuilder, bigtiff bool, spec AssociatedSpec, dataOffset uint64) error {
	// ... existing 254/256/257/258/259/262/277 ...
	rps := spec.RowsPerStrip; if rps == 0 { rps = spec.Height }
	// per-strip offsets/counts from dataOffset + cumulative lengths:
	offs := make([]uint64, len(spec.Strips)); cnts := make([]uint64, len(spec.Strips))
	cur := dataOffset
	for i, s := range spec.Strips { offs[i] = cur; cnts[i] = uint64(len(s)); cur += uint64(len(s)) }
	if bigtiff {
		b.AddLong8(tiff.TagStripOffsets, offs); b.AddLong8(tiff.TagStripByteCounts, cnts)
	} else { /* AddLong with uint32 conversion + overflow guard, as the existing single-strip code does */ }
	b.AddLong(tiff.TagRowsPerStrip, []uint32{rps})
	if spec.Predictor > 1 { b.AddShort(tiff.TagPredictor, []uint16{spec.Predictor}) }
	if len(spec.JPEGTables) > 0 { b.AddUndefined(tiff.TagJPEGTables, spec.JPEGTables) }
	b.AddASCII(tiff.TagWSIImageType, spec.Type)
	return nil
}
```
(Match the existing classic/bigtiff branching + overflow guards already in `populateAssocIFD`.)

- [ ] **Step 7: Run, verify PASS** (`go test ./internal/tiff/cogwsiwriter/ -run Faithful -v`). gofmt + vet. Also run the full cogwsiwriter suite to confirm no layout regression: `go test ./internal/tiff/cogwsiwriter/ -count=1`.

- [ ] **Step 8: Commit.**
```bash
git add internal/tiff/cogwsiwriter/
git commit -m "feat(cogwsiwriter): multi-strip associated images with Predictor(317)+JPEGTables(347)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: faithful-or-fallback associated-spec builder

**Files:** `cmd/wsitools/associated_faithful.go` (new), `cmd/wsitools/associated_faithful_test.go` (new).

- [ ] **Step 1: Write the failing test**: from CMU-1-Small-Region, for the `label`, `faithfulCOGWSISpec(src, a)` returns a `cogwsiwriter.AssociatedSpec` with `len(Strips)>1`, `Predictor==2`; for the JPEG `overview`, Predictor==0/1 and JPEGTables possibly set. Assert FAIL.

- [ ] **Step 2: Implement** (`associated_faithful.go`). Two builders (cog-wsi + streamwriter) over one core that prefers `a.Source()`, else decodes:
```go
// faithfulAssoc returns the verbatim source form if available, else a decoded
// re-encode (self-contained). compTag maps opentile Compression -> TIFF tag.
func faithfulCOGWSISpec(a source.AssociatedImage) (cogwsiwriter.AssociatedSpec, error) {
	t := a.Type(); sz := a.Size()
	if src, ok := a.Source(); ok {
		return cogwsiwriter.AssociatedSpec{
			Type: t, Width: uint32(sz.X), Height: uint32(sz.Y),
			Compression: tiffTagForOTComp(src.Compression),
			Photometric: uint16(src.Photometric),
			SamplesPerPixel: uint16(src.Samples),
			Strips: src.Strips, Predictor: uint16(src.Predictor),
			JPEGTables: src.JPEGTables, RowsPerStrip: uint32(src.RowsPerStrip),
		}, nil
	}
	// Fallback: decode -> re-encode self-contained (no predictor; single strip).
	di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
	if err != nil { return cogwsiwriter.AssociatedSpec{}, fmt.Errorf("%w: %s decode: %v", errSkipAssociated, t, err) }
	rgb := tightRGBFrom(di) // pack to Height*Width*3
	return cogwsiwriter.AssociatedSpec{
		Type: t, Width: uint32(di.Width), Height: uint32(di.Height),
		Compression: compLZW, Photometric: 2, SamplesPerPixel: 3,
		Strips: [][]byte{encodeLZW(rgb)}, RowsPerStrip: uint32(di.Height), // no predictor => no 317
	}, nil
}
func faithfulStrippedSpec(a source.AssociatedImage) (streamwriter.StrippedSpec, error) { /* same shape -> StrippedSpec */ }
```
- `tiffTagForOTComp(opentile.Compression) uint16` maps None→1, LZW→5, JPEG→7, JP2K→34712, Deflate→8 (reuse any existing `compressionTagFor`/`mapAssociatedCompression` mapping — grep; DON'T duplicate). For the fallback, prefer re-encoding to LZW-no-predictor (decodable everywhere) over JPEG to avoid generation loss; single self-contained strip.
- `tightRGBFrom`/`encodeLZW` already exist (`cmd/wsitools/associated_replace.go`) — reuse.

- [ ] **Step 3: Run PASS; gofmt; vet; commit.**
```bash
git add cmd/wsitools/associated_faithful.go cmd/wsitools/associated_faithful_test.go
git commit -m "feat(convert): faithful-or-fallback associated-image spec builder (Source-or-decode)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: rewire all TIFF-family associated consumers + CLI faithful-copy tests

**Files:** `cmd/wsitools/associated_rebuild.go` (writeCOGWSI), `cmd/wsitools/convert_tiff.go` (writeAssociatedImages), `cmd/wsitools/downsample.go` (writeOneAssociated), `cmd/wsitools/convert_factor.go` (cog-wsi `--factor` assoc loop), `cmd/wsitools/convert_dicom_test.go` or a new `cmd/wsitools/convert_associated_test.go`.

- [ ] **Step 1: Write the failing CLI test** (`convert_associated_test.go`): for each `fmt` in {cog-wsi, svs, tiff, ome-tiff}, `convert --to <fmt>` CMU-1-Small-Region → open output → every associated (`label`, `thumbnail`, `overview`) decode equals the source decode (reuse the audit harness: open source, decode each associated; open output, decode each; `bytes.Equal` tight RGB). Assert FAIL (current code corrupts).
```go
func TestConvertAssociatedFaithful(t *testing.T) {
	// table: {"cog-wsi",".tiff"},{"svs",".svs"},{"tiff",".tiff"},{"ome-tiff",".ome.tiff"}
	// source assoc map (type->tight RGB) via opentile; for each fmt convert, reopen,
	// decode each assoc, require bytes.Equal to source. label(LZW)+thumbnail(JPEG)+overview.
}
```

- [ ] **Step 2: Rewire `writeCOGWSI`** (associated_rebuild.go ~74-86): replace the `a.Bytes()` + `AssociatedSpec{...Bytes: bs}` block with:
```go
		spec, err := faithfulCOGWSISpec(a)
		if err != nil {
			if errors.Is(err, errSkipAssociated) { slog.Warn("skipping associated", "type", a.Type(), "reason", err); continue }
			return err
		}
		if err := w.AddAssociated(spec); err != nil {
			if errors.Is(err, cogwsiwriter.ErrInvalidAssocType) { slog.Warn("skipping associated image with unsupported type", "type", a.Type(), "reason", err); continue }
			return fmt.Errorf("add associated %s: %w", a.Type(), err)
		}
```

- [ ] **Step 3: Rewire `convert_factor.go` cog-wsi assoc loop** (~604-623): same `faithfulCOGWSISpec` swap.

- [ ] **Step 4: Rewire `writeAssociatedImages`** (convert_tiff.go ~775-807) and `writeOneAssociated` (downsample.go ~519-562): replace the `a.Bytes()` + `StrippedSpec{...StripBytes: bs}` with `faithfulStrippedSpec(a)` and `w.AddStripped(spec)`. Preserve the existing skip-on-error/warn behavior.

- [ ] **Step 5: Run the CLI test + full convert/writer regression.**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ ./internal/tiff/... -run 'ConvertAssociated|Faithful|COGWSI|Stripped|Convert|Associated|Downsample|Factor' -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l cmd/wsitools/ internal/tiff/ ; go vet ./cmd/wsitools/ ./internal/tiff/...
```
Expected: faithful-copy passes for all four formats; pyramid output unchanged; `associated replace`/`remove` + DICOM untouched.

- [ ] **Step 6: Commit.**
```bash
git add cmd/wsitools/associated_rebuild.go cmd/wsitools/convert_tiff.go cmd/wsitools/downsample.go cmd/wsitools/convert_factor.go cmd/wsitools/convert_associated_test.go
git commit -m "fix(convert): faithfully copy associated images across all TIFF writers (wsitools#1)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: regenerate the corrupt cog-wsi fixtures + verify

**Files:** `sample_files/cog-wsi/{CMU-1, CMU-1-Small-Region, JP2K-33003-1, scan_617, scan_620}_cog-wsi.tiff` (shared pool — regenerate from their sources).

- [ ] **Step 1: Build the fixed binary.** `go build -o bin/wsitools ./cmd/wsitools`.

- [ ] **Step 2: Regenerate each fixture from its source** (preserve the existing convert flags those fixtures used — check the cog-wsi format doc `sample_files/cog-wsi/2026-05-20-cog-wsi-format.md` / any generator script; default `convert --to cog-wsi -f -o <fixture> <source.svs>`):
```bash
for n in CMU-1 CMU-1-Small-Region JP2K-33003-1 scan_617 scan_620; do
  src="sample_files/svs/${n}.svs"; [ "$n" = scan_617 ] && src="sample_files/svs/scan_617_.svs"; [ "$n" = scan_620 ] && src="sample_files/svs/scan_620_.svs"
  ./bin/wsitools convert --to cog-wsi -f -o "sample_files/cog-wsi/${n}_cog-wsi.tiff" "$src"
done
```
(NOTE the scan_617_/scan_620_ trailing-underscore source names. Confirm each source exists; skip-with-note any missing.)

- [ ] **Step 3: Audit every regenerated fixture's associated images** decode == source (reuse the Task-5 audit logic as a `go test` or a one-off). Require all label/thumbnail/overview green.

- [ ] **Step 4: Commit.**
```bash
git add sample_files/cog-wsi/CMU-1_cog-wsi.tiff sample_files/cog-wsi/CMU-1-Small-Region_cog-wsi.tiff sample_files/cog-wsi/JP2K-33003-1_cog-wsi.tiff sample_files/cog-wsi/scan_617_cog-wsi.tiff sample_files/cog-wsi/scan_620_cog-wsi.tiff
git commit -m "fixtures(cog-wsi): regenerate with faithful associated images (wsitools#1)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```
NOTE: `sample_files` is a symlink to the shared opentile-go pool; these fixtures are committed in that repo. If they are NOT tracked in wsitools, instead report the regenerated paths for the user to commit in opentile-go, and have opentile flip its known-corrupt cog-wsi-label skip to an assertion. **CONTROLLER: confirm fixture ownership before committing.**

---

## Task 7: docs

**Files:** `README.md`, `CHANGELOG.md`, `docs/roadmap.md`.

- [ ] **Step 1: Update docs.**
- **CHANGELOG.md** `## [Unreleased]`: associated images are now copied **byte-faithfully** across `convert --to {cog-wsi,svs,tiff,ome-tiff}` + `--factor` via opentile-go v0.39.0 `AssociatedSourceOf` (#22) — verbatim source strips + `Predictor`/`JPEGTables`/multi-strip tags — fixing corrupt LZW labels and abbreviated-JPEG thumbnails (wsitools#1). Synthesized/tiled associated images fall back to decode→re-encode. opentile-go bumped v0.38.1 → v0.39.0.
- **README.md**: note `convert` preserves associated images faithfully (no re-encode where a faithful source exists).
- **docs/roadmap.md**: add a DONE entry; note opentile-go→v0.39.0 and the #22 dependency.

- [ ] **Step 2: Verify + commit.**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
git add README.md CHANGELOG.md docs/roadmap.md
git commit -m "docs: faithful associated-image copy across TIFF writers (wsitools#1)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final review

Dispatch a final reviewer (focus: the faithful path is truly no-re-encode for ok=true; multi-strip StripOffsets/StripByteCounts/Predictor/JPEGTables are emitted correctly in BOTH writers; the ok=false fallback is self-contained; pyramid output byte-identical; all four formats + `--factor` faithfully copy label+thumbnail+overview; the 5 fixtures are green; safe paths untouched). Then `superpowers:finishing-a-development-branch`.

## Self-review notes (author)

- **Spec coverage:** Component 0 source.Source (T1); streamwriter writer support (T2); cogwsiwriter writer support (T3); faithful builder (T4); consumer rewire + CLI tests (T5); fixtures (T6); docs (T7). opentile version-bump verification done pre-plan.
- **Type consistency:** `Source() (opentile.AssociatedSource, bool)`, `AssociatedSource{Strips,Compression,Predictor,JPEGTables,RowsPerStrip,Samples,Photometric}`, `faithfulCOGWSISpec`/`faithfulStrippedSpec`, `StrippedSpec.Strips`/`AssociatedSpec.Strips` — consistent across tasks.
- **Known intricacy:** cogwsiwriter multi-strip touches `planLayout`/`ifdSizeForAssociated` (T3) — flagged to mirror the level-path array/external accounting; the round-trip test (T3 s1) de-risks it.
- **Verify-before-use:** exact `tiff` tag const names + the EntryBuilder UNDEFINED/byte adder (T2 s1/s4) and the existing compression-tag mapping (T4) must be grepped, not assumed.
- **Fixture ownership** (shared pool) is an explicit controller checkpoint (T6).
