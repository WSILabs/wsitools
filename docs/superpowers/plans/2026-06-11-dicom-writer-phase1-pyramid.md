# DICOM-WSI writer — Phase 1 slice 2 (full pyramid) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `convert --to dicom -o <dir> <input>` emits the full resolution pyramid as a multi-instance DICOM Series — one conformant WSM VOLUME instance per source level at `<dir>/level-<n>.dcm`, sharing Study/Series/FrameOfReference UIDs, with correct per-level spatial metadata; `--level N` keeps the shipped single-instance path.

**Architecture:** Extract the per-level body of `WriteVolumeInstance` into `writeInstance(w, src, level, shared)`; `WriteVolumeInstance` becomes a thin wrapper (fresh shared UIDs) and a new `WritePyramid(src, opts, newWriter)` generates the shared UIDs once and loops levels, asking a caller-supplied factory for a writer per level. `assembleWSMDataset` is fixed to scale PixelSpacing by each level's downsample and emit a constant ImagedVolume extent. The CLI branches on whether `--level` was set; the pyramid path writes into a temp dir and renames into place atomically.

**Tech Stack:** Go, `github.com/suyashkumar/dicom` v1.1.0, opentile-go, `dciodvfy` (dicom3tools) for conformance.

**Spec:** `docs/superpowers/specs/2026-06-11-dicom-writer-phase1-pyramid-design.md`

**Branch:** create `feat/dicom-writer-phase1-pyramid` off `main`. Never implement on `main`.

**Verified facts (probed this session — do not re-derive):**
- Grundium golden uses the classic multi-instance model: shared `SeriesInstanceUID` + `FrameOfReferenceUID`, distinct `SOPInstanceUID`, sequential `InstanceNumber`, per-level `TotalPixelMatrixColumns` (65536 → 16384 → 4096), **no Pyramid UID**.
- `assembleWSMDataset` (dataset.go:201) already emits `InstanceNumber = level+1`, so `writeInstance` needs NO instanceNumber param — `assembleWSMDataset` keeps deriving it from `level`.
- Current `assembleWSMDataset` spatial block (dataset.go:90-109) uses base MPP for `psX/psY` and `size` (the LEVEL size) for `imagedW/imagedH` — wrong for reduced levels. `size := lvl.Size()`, `md := src.Metadata()` are in scope; `src` is a param.
- Current `WriteVolumeInstance` (dicomwriter.go) generates a full `UIDSet{SOP,Study,Series,FrameOfReference,DimensionOrg}` inline then calls `assembleWSMDataset`. `buildDescriptor` lives below it and is unchanged by this slice.
- `source.Level`: `Size() image.Point`, `Grid()`, `TileSize()`, `TileMaxSize()`, `TileInto`, `Compression()`. `src.Levels() []source.Level`.
- CLI: `convert_dicom.go` `runConvertDICOM(cmd *cobra.Command, input string, start time.Time)`; globals `cvOutput`, `cvForce`, `cvDICOMLevel`; `--level` registered on `convertCmd`. `formatBytes(int64) string` helper exists in the package. `cmd.Flags().Changed("level")` reports whether the user passed `--level`.

---

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `internal/dicomwriter/dataset.go` | modify | per-level downsample-scaled `PixelSpacing` + constant `ImagedVolume` |
| `internal/dicomwriter/dataset_test.go` | modify | per-level spatial-metadata assertions (L0 vs reduced) |
| `internal/dicomwriter/dicomwriter.go` | modify | `sharedUIDs`, `writeInstance`, thin `WriteVolumeInstance`, new `WritePyramid` |
| `internal/dicomwriter/dicomwriter_test.go` | modify | `WritePyramid` unit (shared/distinct UIDs, per-level dims, InstanceNumber) |
| `cmd/wsitools/convert_dicom.go` | modify | `--level`-changed branch; directory output; temp-dir→rename atomicity |
| `cmd/wsitools/convert_dicom_test.go` | modify | full-pyramid CLI integration |
| `Makefile` | modify | `dicom-validate` emits + validates a full pyramid (every level) |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md` | modify | document the slice |

---

## Task 1: Per-level spatial metadata fix

**Files:** modify `internal/dicomwriter/dataset.go`, `internal/dicomwriter/dataset_test.go`.

- [ ] **Step 1: Write the failing test** — append to `internal/dicomwriter/dataset_test.go`.

First ensure these imports are present in the file (add any missing): `math`, `fmt`, `os`, `path/filepath`, `testing`, `github.com/suyashkumar/dicom`, `github.com/suyashkumar/dicom/pkg/tag`, `github.com/wsilabs/wsitools/internal/source`. Then add:

```go
// imagedVolumeWidth reads the top-level ImagedVolumeWidth (FL) from ds.
func imagedVolumeWidth(t *testing.T, ds dicom.Dataset) float64 {
	t.Helper()
	e, err := ds.FindElementByTag(tag.ImagedVolumeWidth)
	if err != nil {
		t.Fatalf("ImagedVolumeWidth: %v", err)
	}
	vs, ok := e.Value.GetValue().([]float64)
	if !ok || len(vs) == 0 {
		t.Fatalf("ImagedVolumeWidth value is %T", e.Value.GetValue())
	}
	return vs[0]
}

// pixelSpacingYX reads PixelSpacing (DS, row\col = Y\X) from the nested
// SharedFunctionalGroupsSequence → PixelMeasuresSequence.
func pixelSpacingYX(t *testing.T, ds dicom.Dataset) (psY, psX float64) {
	t.Helper()
	sfg, err := ds.FindElementByTag(tag.SharedFunctionalGroupsSequence)
	if err != nil {
		t.Fatalf("SharedFunctionalGroupsSequence: %v", err)
	}
	items := sfg.Value.GetValue().([]*dicom.SequenceItemValue)
	for _, el := range items[0].GetValue().([]*dicom.Element) {
		if el.Tag != tag.PixelMeasuresSequence {
			continue
		}
		pm := el.Value.GetValue().([]*dicom.SequenceItemValue)
		for _, pe := range pm[0].GetValue().([]*dicom.Element) {
			if pe.Tag == tag.PixelSpacing {
				vs := pe.Value.GetValue().([]string)
				fmt.Sscanf(vs[0], "%g", &psY)
				fmt.Sscanf(vs[1], "%g", &psX)
				return psY, psX
			}
		}
	}
	t.Fatal("PixelSpacing not found")
	return 0, 0
}

func TestPerLevelSpatialMetadata(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no dicom fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if len(src.Levels()) < 2 {
		t.Skip("need >= 2 levels")
	}
	last := len(src.Levels()) - 1

	desc := ImageDescriptor{
		Photometric: "YBR_FULL_422",
		ImageType:   []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		ICCProfile:  src.Metadata().ICCProfile,
		LossyRatio:  10.0,
	}
	uids := UIDSet{SOP: NewUID(), Study: NewUID(), Series: NewUID(), FrameOfReference: NewUID(), DimensionOrg: NewUID()}

	ds0, err := assembleWSMDataset(src, 0, uids, desc)
	if err != nil {
		t.Fatal(err)
	}
	dsN, err := assembleWSMDataset(src, last, uids, desc)
	if err != nil {
		t.Fatal(err)
	}

	// Physical extent must be CONSTANT across levels.
	iv0 := imagedVolumeWidth(t, ds0)
	ivN := imagedVolumeWidth(t, dsN)
	if iv0 <= 0 {
		t.Fatalf("ImagedVolumeWidth at L0 = %g (want > 0; fixture should carry MPP)", iv0)
	}
	if math.Abs(iv0-ivN) > iv0*1e-6 {
		t.Errorf("ImagedVolumeWidth not constant across levels: L0=%g L%d=%g", iv0, last, ivN)
	}

	// PixelSpacing at a reduced level must scale by its downsample factor.
	_, ps0 := pixelSpacingYX(t, ds0)
	_, psN := pixelSpacingYX(t, dsN)
	downsample := float64(src.Levels()[0].Size().X) / float64(src.Levels()[last].Size().X)
	want := ps0 * downsample
	if math.Abs(psN-want) > want*0.01 {
		t.Errorf("L%d PixelSpacing(X)=%g, want ~%g (L0 %g × downsample %g)", last, psN, want, ps0, downsample)
	}
}
```

- [ ] **Step 2: Run, verify it FAILS** (current code uses base MPP for all levels → reduced-level spacing wrong + ImagedVolume shrinks)

Run: `WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ -run TestPerLevelSpatialMetadata -v 2>&1 | grep -v "duplicate lib"`
Expected: FAIL (ImagedVolumeWidth differs across levels AND/OR PixelSpacing not scaled). If the fixture is absent it SKIPS — in that case note it and proceed; the fix is still required.

- [ ] **Step 3: Implement the fix** — in `internal/dicomwriter/dataset.go`, replace this block (currently dataset.go:95-109):

```go
	// PixelSpacing in mm = MPP(µm) / 1000. Fall back to 0 when unknown; the
	// golden carries a real value, so prefer per-axis when available.
	mppX, mppY := md.MPPX, md.MPPY
	if mppX == 0 {
		mppX = md.MPP
	}
	if mppY == 0 {
		mppY = md.MPP
	}
	psX := mppX / 1000.0 // mm/px, column spacing
	psY := mppY / 1000.0 // mm/px, row spacing

	// ImagedVolume dimensions (mm) = matrix pixels × pixel spacing.
	imagedW := float64(size.X) * psX
	imagedH := float64(size.Y) * psY
```

with:

```go
	// Base (level-0) MPP in µm/px, with the per-axis → symmetric fallback.
	mppX, mppY := md.MPPX, md.MPPY
	if mppX == 0 {
		mppX = md.MPP
	}
	if mppY == 0 {
		mppY = md.MPP
	}
	// PixelSpacing must scale by THIS level's downsample factor relative to L0 (a
	// reduced level has coarser spacing). The physical ImagedVolume extent is
	// CONSTANT across levels (same slide area) — derive both from L0 so every
	// pyramid instance is spatially co-registered. When MPP is unknown (0),
	// spacing and extent fall back to 0 as before.
	l0Size := src.Levels()[0].Size()
	dsX, dsY := 1.0, 1.0
	if size.X > 0 {
		dsX = float64(l0Size.X) / float64(size.X)
	}
	if size.Y > 0 {
		dsY = float64(l0Size.Y) / float64(size.Y)
	}
	psX := mppX * dsX / 1000.0 // mm/px at this level, column spacing
	psY := mppY * dsY / 1000.0 // mm/px at this level, row spacing

	// ImagedVolume (mm) = L0 matrix pixels × base MPP — the constant slide extent.
	imagedW := float64(l0Size.X) * mppX / 1000.0
	imagedH := float64(l0Size.Y) * mppY / 1000.0
```

- [ ] **Step 4: Run, verify PASS + regression**

```bash
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ -count=1 2>&1 | grep -v "duplicate lib"
```
Expected: PASS (the new test + all existing dicomwriter tests, incl. `TestAssembleWSMDataset` which uses the smallest level — note its assertions don't check spacing, so they're unaffected). `gofmt -l internal/dicomwriter/dataset.go internal/dicomwriter/dataset_test.go` empty; `go vet ./internal/dicomwriter/`.

- [ ] **Step 5: Commit**
```bash
git add internal/dicomwriter/dataset.go internal/dicomwriter/dataset_test.go
git commit -m "fix(dicomwriter): per-level PixelSpacing + constant ImagedVolume extent

PixelSpacing now scales by each level's downsample factor and ImagedVolume
is the constant L0-derived physical extent, so reduced pyramid levels are
spatially co-registered (was: base MPP + level-size extent — correct only at L0).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `writeInstance` extraction + `WritePyramid`

**Files:** modify `internal/dicomwriter/dicomwriter.go`, `internal/dicomwriter/dicomwriter_test.go`.

- [ ] **Step 1: Refactor `dicomwriter.go`** — replace the current `WriteVolumeInstance` function (the whole func, NOT `buildDescriptor` which stays below) with:

```go
// sharedUIDs are the UIDs shared by every instance in a pyramid Series: the
// Study, Series, FrameOfReference, and DimensionOrganization. Each instance still
// gets its own SOPInstanceUID.
type sharedUIDs struct {
	Study, Series, FrameOfReference, DimensionOrg string
}

// WriteVolumeInstance emits ONE conformant DICOM WSM VOLUME instance for src
// level `level` to w, copying the source's compressed JPEG tiles verbatim. The
// source's selected level must carry JPEG-baseline tiles (DICOM sources always
// do; non-DICOM sources are codec-gated in buildDescriptor).
func WriteVolumeInstance(w io.Writer, src source.Source, level int, _ Options) error {
	shared := sharedUIDs{
		Study:            NewUID(),
		Series:           NewUID(),
		FrameOfReference: NewUID(),
		DimensionOrg:     NewUID(),
	}
	return writeInstance(w, src, level, shared)
}

// WritePyramid emits the full resolution pyramid as a multi-instance Series: one
// WSM VOLUME instance per source level, all sharing the Study/Series/
// FrameOfReference/DimensionOrganization UIDs. newWriter supplies the destination
// writer for each level (0-based); WritePyramid closes each writer after writing.
func WritePyramid(src source.Source, _ Options, newWriter func(level int) (io.WriteCloser, error)) error {
	shared := sharedUIDs{
		Study:            NewUID(),
		Series:           NewUID(),
		FrameOfReference: NewUID(),
		DimensionOrg:     NewUID(),
	}
	for level := range src.Levels() {
		w, err := newWriter(level)
		if err != nil {
			return fmt.Errorf("open writer for level %d: %w", level, err)
		}
		werr := writeInstance(w, src, level, shared)
		cerr := w.Close()
		if werr != nil {
			return fmt.Errorf("write level %d: %w", level, werr)
		}
		if cerr != nil {
			return fmt.Errorf("close level %d: %w", level, cerr)
		}
	}
	return nil
}

// writeInstance assembles + writes one WSM VOLUME instance for src level `level`
// to w, using the supplied shared UIDs and a fresh SOPInstanceUID. The level's
// InstanceNumber (level+1) is emitted by assembleWSMDataset.
func writeInstance(w io.Writer, src source.Source, level int, shared sharedUIDs) error {
	if level < 0 || level >= len(src.Levels()) {
		return fmt.Errorf("level %d out of range (0..%d)", level, len(src.Levels())-1)
	}
	// Encapsulate first: the compressed byte total feeds the lossy compression
	// ratio (LossyImageCompressionRatio is Type 1C, required when
	// LossyImageCompression is "01").
	pd, compressedBytes, err := encapsulatePixelData(src, level)
	if err != nil {
		return err
	}
	lvl := src.Levels()[level]
	tileSize := lvl.TileSize()
	grid := lvl.Grid()
	uncompressed := int64(grid.X) * int64(grid.Y) * int64(tileSize.X) * int64(tileSize.Y) * 3
	lossyRatio := 1.0
	if compressedBytes > 0 {
		lossyRatio = float64(uncompressed) / float64(compressedBytes)
	}

	desc, err := buildDescriptor(src, level, lossyRatio)
	if err != nil {
		return err
	}

	uids := UIDSet{
		SOP:              NewUID(),
		Study:            shared.Study,
		Series:           shared.Series,
		FrameOfReference: shared.FrameOfReference,
		DimensionOrg:     shared.DimensionOrg,
	}
	ds, err := assembleWSMDataset(src, level, uids, desc)
	if err != nil {
		return err
	}
	ds.Elements = append(ds.Elements, pd) // PixelData last
	return dicom.Write(w, ds)
}
```

(`buildDescriptor` is unchanged and remains below this in the file. Confirm `io` and `fmt` are still imported — they are.)

- [ ] **Step 2: Build + run existing tests (regression: single-instance path unchanged)**

```bash
go build ./... 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ ./cmd/wsitools/ -run 'DICOM|Dicom|WSM|Assemble|Encapsulat|NewUID|ConvertDICOM|Inspect|SRGB|PixelRoundTrip|PerLevel' -count=1 2>&1 | grep -v "duplicate lib"
```
Expected: PASS — the single-instance round-trip/readback/pixel tests still pass (the extraction preserves the emitted attribute set + frames; only the order of `NewUID()` calls changed, which is immaterial since UIDs are random).

- [ ] **Step 3: Write the `WritePyramid` unit test** — append to `internal/dicomwriter/dicomwriter_test.go`. Ensure imports include: `bytes`, `io`, `os`, `path/filepath`, `strconv`, `testing`, `github.com/suyashkumar/dicom`, `github.com/suyashkumar/dicom/pkg/tag`, `github.com/wsilabs/wsitools/internal/source`. Add:

```go
type nopWriteCloser struct{ *bytes.Buffer }

func (nopWriteCloser) Close() error { return nil }

func TestWritePyramid(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no dicom fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	n := len(src.Levels())
	if n < 2 {
		t.Skip("need >= 2 levels")
	}

	bufs := make([]*bytes.Buffer, n)
	factory := func(level int) (io.WriteCloser, error) {
		bufs[level] = &bytes.Buffer{}
		return nopWriteCloser{bufs[level]}, nil
	}
	if err := WritePyramid(src, Options{}, factory); err != nil {
		t.Fatalf("WritePyramid: %v", err)
	}

	firstStr := func(ds dicom.Dataset, tg tag.Tag) string {
		e, err := ds.FindElementByTag(tg)
		if err != nil {
			t.Fatalf("missing %v: %v", tg, err)
		}
		return e.Value.GetValue().([]string)[0]
	}
	firstInt := func(ds dicom.Dataset, tg tag.Tag) int {
		e, err := ds.FindElementByTag(tg)
		if err != nil {
			t.Fatalf("missing %v: %v", tg, err)
		}
		return e.Value.GetValue().([]int)[0]
	}

	var series, frameOfRef string
	sops := map[string]bool{}
	for level := 0; level < n; level++ {
		if bufs[level] == nil {
			t.Fatalf("level %d was never written", level)
		}
		ds, err := dicom.Parse(bytes.NewReader(bufs[level].Bytes()), int64(bufs[level].Len()), nil)
		if err != nil {
			t.Fatalf("parse level %d: %v", level, err)
		}
		s := firstStr(ds, tag.SeriesInstanceUID)
		fr := firstStr(ds, tag.FrameOfReferenceUID)
		sop := firstStr(ds, tag.SOPInstanceUID)
		inst := firstStr(ds, tag.InstanceNumber)
		cols := firstInt(ds, tag.TotalPixelMatrixColumns)

		if level == 0 {
			series, frameOfRef = s, fr
		} else {
			if s != series {
				t.Errorf("level %d SeriesInstanceUID %q != L0 %q", level, s, series)
			}
			if fr != frameOfRef {
				t.Errorf("level %d FrameOfReferenceUID %q != L0 %q", level, fr, frameOfRef)
			}
		}
		if sops[sop] {
			t.Errorf("duplicate SOPInstanceUID %q at level %d", sop, level)
		}
		sops[sop] = true
		if inst != strconv.Itoa(level+1) {
			t.Errorf("level %d InstanceNumber = %q, want %d", level, inst, level+1)
		}
		if want := src.Levels()[level].Size().X; cols != want {
			t.Errorf("level %d TotalPixelMatrixColumns = %d, want %d", level, cols, want)
		}
	}
	if len(sops) != n {
		t.Errorf("got %d distinct SOPInstanceUIDs, want %d", len(sops), n)
	}
}
```

- [ ] **Step 4: Run, verify PASS + clean + commit**

```bash
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ -run 'TestWritePyramid|WriteVolumeInstance|RoundTrip' -v -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l internal/dicomwriter/dicomwriter.go internal/dicomwriter/dicomwriter_test.go
go vet ./internal/dicomwriter/
```
Expected: PASS, gofmt empty, vet clean.
```bash
git add internal/dicomwriter/dicomwriter.go internal/dicomwriter/dicomwriter_test.go
git commit -m "feat(dicomwriter): WritePyramid (multi-instance Series, shared UIDs)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: CLI full-pyramid output

**Files:** modify `cmd/wsitools/convert_dicom.go`, `cmd/wsitools/convert_dicom_test.go`.

- [ ] **Step 1: Rewrite `runConvertDICOM` + add the two branch helpers** — replace the whole `runConvertDICOM` function in `cmd/wsitools/convert_dicom.go` and add `writeDICOMSingle` + `writeDICOMPyramid`. Add imports `"io"` and `"path/filepath"` to the file's import block (keep the existing `errors`, `fmt`, `log/slog`, `os`, `time`, cobra, dicomwriter, source).

```go
// runConvertDICOM emits DICOM WSM VOLUME instance(s) from a DICOM or non-DICOM
// JPEG-baseline source. Without --level it emits the full pyramid (one instance
// per level) into the -o directory as level-<n>.dcm; with --level it emits one
// instance to the -o file.
func runConvertDICOM(cmd *cobra.Command, input string, start time.Time) error {
	if cvOutput == "" {
		return fmt.Errorf("-o/--output is required")
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", cvOutput)
		}
	}
	src, err := source.Open(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	if cmd.Flags().Changed("level") {
		return writeDICOMSingle(src, input, start)
	}
	return writeDICOMPyramid(src, start)
}

// writeDICOMSingle emits one WSM instance for cvDICOMLevel to the cvOutput file.
func writeDICOMSingle(src source.Source, input string, start time.Time) error {
	f, err := os.Create(cvOutput)
	if err != nil {
		return fmt.Errorf("create %s: %w", cvOutput, err)
	}
	if err := dicomwriter.WriteVolumeInstance(f, src, cvDICOMLevel, dicomwriter.Options{}); err != nil {
		f.Close()
		_ = os.Remove(cvOutput)
		return fmt.Errorf("write DICOM instance: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", cvOutput, err)
	}
	if stat, _ := os.Stat(cvOutput); stat != nil {
		slog.Info("convert complete",
			"output", cvOutput, "size", formatBytes(stat.Size()),
			"level", cvDICOMLevel, "elapsed", time.Since(start).Round(time.Millisecond))
		fmt.Printf("wrote %s (%s, %s)\n", cvOutput, formatBytes(stat.Size()), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// writeDICOMPyramid emits the full pyramid into cvOutput (a directory) as
// level-<n>.dcm. It writes into a temp sibling dir and renames into place so a
// failed run never leaves a partial pyramid.
func writeDICOMPyramid(src source.Source, start time.Time) error {
	parent := filepath.Dir(cvOutput)
	tmp, err := os.MkdirTemp(parent, ".wsitools-dcm-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	factory := func(level int) (io.WriteCloser, error) {
		return os.Create(filepath.Join(tmp, fmt.Sprintf("level-%d.dcm", level)))
	}
	if err := dicomwriter.WritePyramid(src, dicomwriter.Options{}, factory); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("write DICOM pyramid: %w", err)
	}
	if cvForce {
		if err := os.RemoveAll(cvOutput); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("remove existing %s: %w", cvOutput, err)
		}
	}
	if err := os.Rename(tmp, cvOutput); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("finalize %s: %w", cvOutput, err)
	}

	n := len(src.Levels())
	var total int64
	if entries, err := os.ReadDir(cvOutput); err == nil {
		for _, e := range entries {
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

Also update the `--level` flag help text in `init()` to clarify the default:
```go
	convertCmd.Flags().IntVar(&cvDICOMLevel, "level", 0, "emit only this pyramid level (--to dicom; omit for the full pyramid)")
```

- [ ] **Step 2: Build + smoke**

```bash
go build -o bin/wsitools ./cmd/wsitools 2>&1 | grep -v "duplicate lib"
rm -rf /tmp/svs-pyr
./bin/wsitools convert --to dicom -o /tmp/svs-pyr sample_files/svs/CMU-1-Small-Region.svs 2>&1 | grep -vE "duplicate lib|level=INFO"
ls /tmp/svs-pyr
./bin/wsitools info /tmp/svs-pyr/level-0.dcm 2>&1 | grep -v "duplicate lib" | grep -iE "format|level"
```
Expected: prints `wrote /tmp/svs-pyr (N instances, …)`, `ls` shows `level-0.dcm … level-(N-1).dcm`, and `info` reads `level-0.dcm` as `Format: dicom`. Confirm the single-level path still works: `./bin/wsitools convert --to dicom --level 0 -f -o /tmp/one.dcm sample_files/svs/CMU-1-Small-Region.svs` writes one file.

- [ ] **Step 3: Add the CLI integration test** — append to `cmd/wsitools/convert_dicom_test.go` (ensure `os`, `path/filepath`, `testing`, `source` imported):

```go
func TestConvertDICOMPyramidCommand(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	svs := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skip("no CMU SVS fixture")
	}

	// Determine the expected level count from the source.
	src, err := source.Open(svs)
	if err != nil {
		t.Fatal(err)
	}
	n := len(src.Levels())
	src.Close()

	out := filepath.Join(t.TempDir(), "pyramid")
	// Reset flag state: pyramid path requires --level NOT set.
	convertCmd.Flags().Lookup("level").Changed = false
	cvOutput = ""
	cvForce = false
	rootCmd.SetArgs([]string{"convert", "--to", "dicom", "-o", out, svs})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		convertCmd.Flags().Lookup("level").Changed = false
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("convert --to dicom (pyramid): %v", err)
	}

	for level := 0; level < n; level++ {
		path := filepath.Join(out, "level-"+strconv.Itoa(level)+".dcm")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
		s, err := source.Open(path)
		if err != nil {
			t.Fatalf("source.Open(%s): %v", path, err)
		}
		if s.Format() != "dicom" {
			t.Errorf("%s Format = %q, want dicom", path, s.Format())
		}
		s.Close()
	}
}
```
(Ensure `strconv` is imported in the test file; add it if absent.)

- [ ] **Step 4: Run + clean + commit**

```bash
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run 'TestConvertDICOM' -v -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l cmd/wsitools/convert_dicom.go cmd/wsitools/convert_dicom_test.go
go vet ./cmd/wsitools/
```
Expected: PASS (pyramid + the existing single-level command/readback/pixel tests), gofmt empty, vet clean.
```bash
git add cmd/wsitools/convert_dicom.go cmd/wsitools/convert_dicom_test.go
git commit -m "feat(convert): --to dicom full pyramid (directory of level-<n>.dcm, atomic)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: dciodvfy on the full pyramid (the de-risk)

**Files:** modify `Makefile`. The controller runs the validator (`/tmp/dciodvfy`).

- [ ] **Step 1: Extend `make dicom-validate`** — add a third block that emits a full pyramid from the SVS fixture into a temp dir and runs dciodvfy on EVERY `level-*.dcm`, combining the exit code into `RC`. Insert this inside the recipe after the existing SVS single-instance block (before the final `exit $$RC`), mirroring its style:

```make
	PYR="$$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs"; \
	if [ -f "$$PYR" ]; then \
		DIR=$$(mktemp -d -t wsm-pyr.XXXXXX); \
		./bin/wsitools convert --to dicom -f -o "$$DIR/pyr" "$$PYR"; \
		for L in "$$DIR"/pyr/level-*.dcm; do \
			echo "=== dciodvfy (pyramid) $$L ==="; \
			"$(DCIODVFY)" "$$L" || RC=$$?; \
		done; \
		rm -rf "$$DIR"; \
	else echo "missing $$PYR; skipping pyramid validation"; fi; \
```
(The `-f -o "$$DIR/pyr"` writes the pyramid into a fresh subdir; `--force` is harmless since `pyr` doesn't pre-exist. Adapt to the recipe's exact line-continuation style.)

- [ ] **Step 2: Run (CONTROLLER step — needs dciodvfy)**

```bash
go build -o bin/wsitools ./cmd/wsitools 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" make dicom-validate DCIODVFY=/tmp/dciodvfy 2>&1 | grep -vE "duplicate lib|level=INFO" | grep -E "=== dciodvfy|^Error" ; echo "Errors: $(WSI_TOOLS_TESTDIR="$(pwd)/sample_files" make dicom-validate DCIODVFY=/tmp/dciodvfy 2>/dev/null | grep -c '^Error')"
```
Expected: **0 Errors** across all pyramid levels (plus the DICOM + SVS single-instance blocks). The benign per-instance "Study ID" DICOMDIR warning is acceptable. `make` exits 0.

- [ ] **Step 3: If any level errors, fix and re-run**

Each level is assembled by the same `assembleWSMDataset`, so a per-level conformance gap (e.g. a reduced level whose `NumberOfFrames`/grid math or `PixelSpacing` is malformed) would surface here. Read the dciodvfy output, fix in `dataset.go`/`buildDescriptor`, re-run to 0 errors. **If a reduced level proves structurally non-conformant in a way the slice can't resolve, STOP and report.**

- [ ] **Step 4: Commit**
```bash
git add Makefile
git commit -m "feat(dicomwriter): validate full pyramid in make dicom-validate (dciodvfy 0 errors)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Docs

**Files:** modify `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md`.

- [ ] **Step 1: Update docs**

- **CHANGELOG.md** `## [Unreleased]`: augment the `convert --to dicom` entry — now emits the **full pyramid** by default (multi-instance Series, `<dir>/level-<n>.dcm`, shared Study/Series/FrameOfReference UIDs); `--level N` selects a single level. Note the per-level spatial-metadata fix (downsample-scaled PixelSpacing + constant ImagedVolume) and dciodvfy 0 errors across all levels.
- **README.md**: update the `convert --to dicom` Conversion bullet (full pyramid default → directory of `level-<n>.dcm`; `--level N` for one level) and footnote ⁶ (single instance OR full pyramid). Keep it accurate; only SVS + DICOM sources are validated.
- **docs/roadmap.md**: add a `✅ DONE (2026-06-11): Phase 1 slice 2 — full pyramid` sub-bullet (multi-instance Series, shared UIDs, per-level spatial metadata, dciodvfy 0 errors per level). Update "Next" to the remaining slices: JPEG 2000 codec, then label/overview as separate instances (P2).
- **docs/notes/2026-06-03-dicom-writer-scoping.md**: add a `## Phase 1 — slice 2 outcome (2026-06-11)` section: full pyramid shipped; classic shared-Series/FrameOfReference model (no Pyramid UID, per the golden); the latent per-level PixelSpacing/ImagedVolume bug found + fixed; temp-dir→rename atomicity; dciodvfy 0 errors per level. Remaining: JPEG 2000, associated-image instances, TILED_SPARSE/Concatenations.

- [ ] **Step 2: Verify + commit**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
git add README.md CHANGELOG.md docs/roadmap.md docs/notes/2026-06-03-dicom-writer-scoping.md
git commit -m "docs: DICOM-WSI writer Phase 1 slice 2 (full pyramid)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final review

Dispatch a final reviewer (focus: shared vs distinct UIDs correct across the Series; per-level spatial metadata co-registers; the single-instance path is structurally unchanged; atomic directory output never leaves a partial pyramid; dciodvfy 0 errors on every level; no scope creep beyond full-pyramid). Then use `superpowers:finishing-a-development-branch`.

## Self-review notes (author)

- **Spec coverage:** per-level spatial fix (T1), `writeInstance`/`WritePyramid` + shared UIDs (T2), CLI dir output + atomicity (T3), dciodvfy de-risk per level (T4), docs (T5). Spec decisions: full-pyramid-default + `--level` single (T3); `level-<n>.dcm` (T3); classic shared-UID model (T2); writer-factory API (T2); per-level metadata fix (T1); temp-dir→rename (T3).
- **Type consistency:** `sharedUIDs{Study,Series,FrameOfReference,DimensionOrg}`, `writeInstance(w,src,level,shared)`, `WritePyramid(src,opts,newWriter func(int)(io.WriteCloser,error))`, `writeDICOMSingle`/`writeDICOMPyramid` used consistently. `InstanceNumber=level+1` stays in `assembleWSMDataset` (no separate param — matches the spec contract).
- **No placeholders:** all steps have complete code/commands.
- **Simplification vs spec:** the spec sketched `writeInstance(..., instanceNumber)`; since `assembleWSMDataset` already emits `level+1`, the param is dropped (same behavior, less churn) — noted in T2.
- **Test isolation gotcha:** the pyramid CLI test resets `convertCmd.Flags().Lookup("level").Changed` (cobra persists Changed across `Execute()` calls) — baked into T3.
