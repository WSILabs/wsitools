# DICOM-WSI writer — Phase 0 spike — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `convert --to dicom -o out.dcm <input.dcm>` emits ONE conformant WSM VOLUME instance for one pyramid level, copying JPEG frames verbatim — validated by `dciodvfy` (0 errors) + opentile-go read-back.

**Architecture:** One pure-Go package `internal/dicomwriter` builds the WSM dataset (mirroring the Grundium golden template, values derived from `source.Source`) via `github.com/suyashkumar/dicom`, encapsulates the source's raw JPEG tiles as multi-frame PixelData, and `dicom.Write`s a single `.dcm`. A `convert_dicom.go` cmd file plugs `case "dicom"` into `runConvert`.

**Tech Stack:** Go, cobra, `github.com/suyashkumar/dicom` (promote to direct dep), opentile-go DICOM reader, `dciodvfy` (dicom3tools) for conformance.

**Spec:** `docs/superpowers/specs/2026-06-11-dicom-writer-phase0-design.md`

**Branch:** create `feat/dicom-writer-phase0` off `main`. Never implement on `main`.

**Key API facts (verified):**
- `dicom.Write(out io.Writer, ds dicom.Dataset, opts ...dicom.WriteOption) error` — auto-emits preamble + `DICM` + file-meta (computes `FileMetaInformationGroupLength`); meta = the group-0002 elements present in `ds.Elements`.
- `dicom.NewElement(t tag.Tag, data any) (*dicom.Element, error)` — `data` ∈ `[]string|[]int|[]byte|[]float64|dicom.PixelDataInfo`. Sequences (SQ) are built as nested datasets (confirm the exact SQ-construction helper in `suyashkumar/dicom`; `[]*dicom.SequenceItemValue`-style — verify during impl).
- Encapsulated pixel data: `dicom.NewElement(tag.PixelData, dicom.PixelDataInfo{IsEncapsulated:true, Offsets:[]uint32{...}, Frames:[]*frame.Frame{ {Encapsulated:true, EncapsulatedData: frame.EncapsulatedFrame{Data: jpegBytes}}, ... }})` (package `github.com/suyashkumar/dicom/pkg/frame`). Library writes the Basic Offset Table + per-frame fragments.
- WSM tags exist as named consts: `tag.DimensionOrganizationType`, `tag.NumberOfFrames`, `tag.TotalPixelMatrixColumns/Rows`, `tag.TotalPixelMatrixOriginSequence`, `tag.OpticalPathSequence`, `tag.PixelData`, `tag.Rows`, `tag.Columns`, etc. For any missing tag use `tag.Tag{0xGGGG,0xEEEE}` (the library resolves VR from its dictionary).
- Source: `src.Levels()[n]` → `Size()/TileSize()/Grid()/TileMaxSize()/TileInto(tx,ty,buf)/Compression()/Index()`; `src.Metadata()` (MPPX/Y, Magnification, Make/Model, ICCProfile, AcquisitionDateTime); `src.Format()` == `"dicom"`. For a DICOM source, `TileInto` returns the raw **encapsulated JPEG frame bytes** (no decode) — exactly what we re-encapsulate.
- WSM SOP class UID: `1.2.840.10008.5.1.4.1.1.77.1.6`. JPEG Baseline transfer syntax: `1.2.840.10008.1.2.4.50`.

**Golden reference:** `sample_files/dicom/scan_621_grundium_dicom/` — `dcmdump scan_621__pyr04.dcm` is the attribute template to mirror.

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/dicomwriter/uid.go` (new) | `NewUID()` → `2.25.<uuid-int>` |
| `internal/dicomwriter/dataset.go` (new) | `assembleWSMDataset(src, level, uids) (dicom.Dataset, error)` — IOD attribute assembly |
| `internal/dicomwriter/encapsulate.go` (new) | `encapsulatePixelData(src, level) (*dicom.Element, int, error)` — TILED_FULL frames |
| `internal/dicomwriter/dicomwriter.go` (new) | `WriteVolumeInstance(w, src, level, opts) error` |
| `internal/dicomwriter/*_test.go` (new) | unit (uid, attrs) + gated (Grundium round-trip) |
| `cmd/wsitools/convert_dicom.go` (new) | `runConvertDICOM` + `--level` flag |
| `cmd/wsitools/convert.go` (modify) | `case "dicom"` dispatch |
| `Makefile` (modify) | `dicom-validate` target |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md` | document P0 |

---

## Task 1: Package scaffold + UID generation + library round-trip smoke

**Files:** create `internal/dicomwriter/uid.go`, `internal/dicomwriter/uid_test.go`, a throwaway `smoke_test.go`.

- [ ] **Step 1: Promote suyashkumar/dicom to a direct dep**

Run: `go get github.com/suyashkumar/dicom@v1.1.0 && go mod tidy`. Confirm it's in `go.mod` require block.

- [ ] **Step 2: UID generation (TDD)**

`uid_test.go`:
```go
package dicomwriter

import (
	"regexp"
	"testing"
)

func TestNewUID(t *testing.T) {
	re := regexp.MustCompile(`^2\.25\.[0-9]+$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		u := NewUID()
		if !re.MatchString(u) {
			t.Fatalf("UID %q not 2.25.<int>", u)
		}
		if len(u) > 64 {
			t.Fatalf("UID %q exceeds 64 chars (DICOM UI limit)", u)
		}
		if seen[u] {
			t.Fatalf("duplicate UID %q", u)
		}
		seen[u] = true
	}
}
```
Run `go test ./internal/dicomwriter/ -run TestNewUID` → FAIL.

- [ ] **Step 3: Implement `uid.go`**

```go
package dicomwriter

import (
	"crypto/rand"
	"math/big"
)

// NewUID returns a DICOM UID under the UUID-derived root 2.25.<128-bit-int>
// (PS3.5 B.2). Always ≤ 64 chars. No registered org root needed.
func NewUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	n := new(big.Int).SetBytes(b[:])
	return "2.25." + n.String()
}
```
Run → PASS.

- [ ] **Step 4: Library round-trip smoke (de-risk the plumbing)**

`smoke_test.go` — build a tiny dataset (file-meta + a few attrs + a 1-frame encapsulated PixelData), `dicom.Write` to a buffer, then `dicom.Parse` it back and assert it reads. This proves the encapsulated-write API before we build the real assembler. Use the real tag/PixelDataInfo/frame types; if the exact construction differs from the plan's API notes, fix here and record the correct form in a comment for Tasks 2–3.
```go
package dicomwriter

import (
	"bytes"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/frame"
	"github.com/suyashkumar/dicom/pkg/tag"
	"github.com/suyashkumar/dicom/pkg/uid"
)

func TestEncapsulatedWriteRoundTrip(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xD9} // minimal SOI/EOI
	mk := func(t tag.Tag, v any) *dicom.Element { e, err := dicom.NewElement(t, v); if err != nil { panic(err) }; return e }
	ds := dicom.Dataset{Elements: []*dicom.Element{
		mk(tag.MediaStorageSOPClassUID, []string{"1.2.840.10008.5.1.4.1.1.77.1.6"}),
		mk(tag.MediaStorageSOPInstanceUID, []string{NewUID()}),
		mk(tag.TransferSyntaxUID, []string{uid.JPEGBaseline8Bit}), // confirm const name
		mk(tag.Rows, []int{2}),
		mk(tag.Columns, []int{2}),
		mk(tag.NumberOfFrames, []string{"1"}),
		mk(tag.PixelData, dicom.PixelDataInfo{
			IsEncapsulated: true,
			Offsets:        []uint32{0},
			Frames:         []*frame.Frame{{Encapsulated: true, EncapsulatedData: frame.EncapsulatedFrame{Data: jpeg}}},
		}),
	}}
	var buf bytes.Buffer
	if err := dicom.Write(&buf, ds); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := dicom.Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	if err != nil {
		t.Fatalf("parse back: %v", err)
	}
	if _, err := got.FindElementByTag(tag.PixelData); err != nil {
		t.Fatalf("PixelData missing on read-back: %v", err)
	}
}
```
Run `go test ./internal/dicomwriter/ -run TestEncapsulatedWriteRoundTrip -v`. **If it fails on an API mismatch** (const names like `uid.JPEGBaseline8Bit`, `NumberOfFrames` VR expecting `[]string` vs `[]int`, `frame.Frame` field names, or `Write` needing `ImplementationClassUID`), fix to the real API and keep iterating until it passes — this step exists to nail the library surface. Record the working construction patterns in comments.

- [ ] **Step 5: Commit**
```bash
git add go.mod go.sum internal/dicomwriter/
git commit -m "feat(dicomwriter): scaffold + UID gen + verified encapsulated-write round-trip

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: WSM dataset assembly (`assembleWSMDataset`)

**Files:** create `internal/dicomwriter/dataset.go`, `dataset_test.go`.

- [ ] **Step 1: Implement `assembleWSMDataset(src source.Source, level int, uids UIDSet) (dicom.Dataset, error)`**

Build the element list mirroring the **Grundium golden** (`dcmdump scan_621__pyr04.dcm` — read it as you implement). Derive values from the source level + metadata. Cover the modules in the spec's table. Key derivations:
- `lvl := src.Levels()[level]`; `grid := lvl.Grid()`.
- `tag.Rows = lvl.TileSize().Y`, `tag.Columns = lvl.TileSize().X`.
- `tag.TotalPixelMatrixColumns = lvl.Size().X`, `tag.TotalPixelMatrixRows = lvl.Size().Y`.
- `tag.NumberOfFrames = grid.X * grid.Y` (string per DICOM IS VR).
- `tag.NumberOfOpticalPaths = 1`, `tag.TotalPixelMatrixFocalPlanes = 1`.
- PixelSpacing (mm) = `md.MPPX/1000`, `md.MPPY/1000` — emit inside SharedFunctionalGroupsSequence → PixelMeasuresSequence (DS, `"row\col"` = `Y\X`).
- `ImagedVolumeWidth = TotalPixelMatrixColumns * pixelSpacingX_mm` (FL), height analogous, depth 1.
- `tag.ImageOrientationSlide = []float64{0,1,0,1,0,0}`.
- `tag.DimensionOrganizationType = "TILED_FULL"`; a DimensionOrganizationSequence with a generated DimensionOrganizationUID.
- ImageType `DERIVED\PRIMARY\VOLUME\NONE`; Modality `SM`; SamplesPerPixel 3; PhotometricInterpretation from the source (`YBR_FULL_422` for subsampled JPEG — read `md`/the source; default `YBR_FULL_422`); PlanarConfiguration 0; BitsAllocated/Stored/HighBit 8/8/7; PixelRepresentation 0.
- Identity: anonymous — `PatientName` empty, `PatientID` = `"WSITOOLS"` or source-derived; empty `PatientBirthDate`/`PatientSex`/`AccessionNumber`/`ReferringPhysicianName`.
- Equipment: Manufacturer `wsitools`, Model/Software = `Version`.
- OpticalPathSequence: one item — OpticalPathIdentifier `"0"`, IlluminationTypeCodeSequence (brightfield, code `111744`/SCT) + IlluminationColorCodeSequence, ObjectiveLensNumericalAperture from source mag if available (else omit, Type-3).
- Specimen: ContainerIdentifier, SpecimenDescriptionSequence with SpecimenTypeCodeSequence `Microscope slide` (SCT `433466003`).
- File-meta group-0002: MediaStorageSOPClassUID, MediaStorageSOPInstanceUID (= SOPInstanceUID), TransferSyntaxUID, ImplementationClassUID (`uids` set carries the SOP/Study/Series/FrameOfRef UIDs).
- LossyImageCompression `01`, method `ISO_10918_1`.

Define `UIDSet{SOP, Study, Series, FrameOfReference, DimensionOrg string}` populated by the caller via `NewUID()`.

NOTE: building SQ (sequence) elements — confirm `suyashkumar/dicom`'s sequence-item API (likely nested `dicom.SequenceItemValue` / `[]*dicom.Element` per item). If SQ construction is awkward for some coded sequences, the validator-required minimum may allow empty sequences (Grundium has several empty SQs) — prefer empty/minimal over wrong.

- [ ] **Step 2: Unit test** (`dataset_test.go`, gated on a DICOM fixture)

Open `sample_files/dicom/scan_621_grundium_dicom` via `source.Open`, assemble for the smallest level, and assert key attribute values match the source geometry: SOPClassUID = WSM, DimensionOrganizationType `TILED_FULL`, NumberOfFrames = grid product, TotalPixelMatrixColumns/Rows = level size, Rows/Columns = tile size, Modality `SM`. (Find elements via `ds.FindElementByTag`.)

- [ ] **Step 3: Build + run + commit**
`go build ./... && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/dicomwriter/ -v` → PASS (or SKIP without fixture).
```bash
git add internal/dicomwriter/dataset.go internal/dicomwriter/dataset_test.go
git commit -m "feat(dicomwriter): WSM IOD dataset assembly (mirrors Grundium golden)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Frame encapsulation + `WriteVolumeInstance`

**Files:** create `internal/dicomwriter/encapsulate.go`, `internal/dicomwriter/dicomwriter.go`, extend the gated test.

- [ ] **Step 1: `encapsulatePixelData(src source.Source, level int) (*dicom.Element, error)`**

```go
// TILED_FULL frame order: row-major, column fastest → frameIndex = ty*gridX + tx.
func encapsulatePixelData(src source.Source, level int) (*dicom.Element, error) {
	lvl := src.Levels()[level]
	grid := lvl.Grid()
	buf := make([]byte, lvl.TileMaxSize())
	frames := make([]*frame.Frame, 0, grid.X*grid.Y)
	for ty := 0; ty < grid.Y; ty++ {
		for tx := 0; tx < grid.X; tx++ {
			n, err := lvl.TileInto(tx, ty, buf)
			if err != nil {
				return nil, fmt.Errorf("read frame (%d,%d): %w", tx, ty, err)
			}
			data := append([]byte(nil), buf[:n]...) // copy out of the reused buffer
			frames = append(frames, &frame.Frame{Encapsulated: true, EncapsulatedData: frame.EncapsulatedFrame{Data: data}})
		}
	}
	// Offsets: Basic Offset Table may be empty (all-zero / single 0) — most
	// readers accept an empty BOT. Use a zeroed offset per frame or a single 0;
	// confirm what suyashkumar/dicom's writer expects (Task 1 smoke informs this).
	offsets := make([]uint32, len(frames))
	return dicom.NewElement(tag.PixelData, dicom.PixelDataInfo{IsEncapsulated: true, Offsets: offsets, Frames: frames})
}
```

- [ ] **Step 2: `WriteVolumeInstance`**

```go
type Options struct{} // reserved (P0)

func WriteVolumeInstance(w io.Writer, src source.Source, level int, _ Options) error {
	if src.Format() != "dicom" {
		return fmt.Errorf("--to dicom requires a DICOM source (P0)")
	}
	if level < 0 || level >= len(src.Levels()) {
		return fmt.Errorf("level %d out of range (0..%d)", level, len(src.Levels())-1)
	}
	uids := UIDSet{SOP: NewUID(), Study: NewUID(), Series: NewUID(), FrameOfReference: NewUID(), DimensionOrg: NewUID()}
	ds, err := assembleWSMDataset(src, level, uids)
	if err != nil {
		return err
	}
	pd, err := encapsulatePixelData(src, level)
	if err != nil {
		return err
	}
	ds.Elements = append(ds.Elements, pd) // PixelData last
	return dicom.Write(w, ds)
}
```

- [ ] **Step 3: Gated round-trip test** (`dicomwriter_test.go`)

Emit from the Grundium fixture's **smallest level** to a temp `.dcm`, then `dicom.Parse` it back and assert: SOPClassUID = WSM, NumberOfFrames = grid product, and **the first frame's bytes equal the source's first raw tile** (verbatim copy). Helper to pick the smallest level: `len(src.Levels())-1`.

- [ ] **Step 4: Build + run + commit**
`WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./internal/dicomwriter/ -v` → PASS.
```bash
git add internal/dicomwriter/encapsulate.go internal/dicomwriter/dicomwriter.go internal/dicomwriter/dicomwriter_test.go
git commit -m "feat(dicomwriter): encapsulate level tiles (TILED_FULL) + WriteVolumeInstance

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: CLI `convert --to dicom` + opentile read-back test

**Files:** create `cmd/wsitools/convert_dicom.go`; modify `cmd/wsitools/convert.go`; extend integration test.

- [ ] **Step 1: `runConvertDICOM` + `--level` flag**

`convert_dicom.go`:
```go
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/internal/dicomwriter"
	"github.com/wsilabs/wsitools/internal/source"
)

var cvDICOMLevel int

func runConvertDICOM(_ *cobra.Command, input string, _ time.Time) error {
	src, err := source.Open(input)
	if err != nil {
		return err
	}
	defer src.Close()
	if cvOutput == "" {
		return fmt.Errorf("-o/--output is required")
	}
	f, err := os.Create(cvOutput)
	if err != nil {
		return fmt.Errorf("create %s: %w", cvOutput, err)
	}
	defer f.Close()
	if err := dicomwriter.WriteVolumeInstance(f, src, cvDICOMLevel, dicomwriter.Options{}); err != nil {
		return err
	}
	fmt.Printf("wsitools: wrote DICOM WSM instance: %s -> %s (level %d)\n", input, cvOutput, cvDICOMLevel)
	return nil
}
```
Register `--level` on `convertCmd` in `init()` (in convert.go or here): `convertCmd.Flags().IntVar(&cvDICOMLevel, "level", 0, "pyramid level to emit (--to dicom, P0)")`. (Confirm `cvOutput` is the existing convert output global.)

- [ ] **Step 2: Dispatch** — in `runConvert`'s `switch cvTo`, add:
```go
	case "dicom":
		return runConvertDICOM(cmd, input, start)
```

- [ ] **Step 3: Build + smoke + integration test**

`go build ./...`. Smoke:
```bash
go build -o /tmp/wsit ./cmd/wsitools
SM=$(ls sample_files/dicom/scan_621_grundium_dicom/*pyr04.dcm)
/tmp/wsit convert --to dicom --level 0 -o /tmp/out.dcm "$SM" 2>&1 | grep -v duplicate
/tmp/wsit info /tmp/out.dcm 2>/dev/null | grep -iE "format|level" | head
```
Add a gated integration test (in `cmd/wsitools/` or the dicomwriter package) that runs `WriteVolumeInstance` from the Grundium fixture and then **`source.Open`s the output**, asserting `Format()=="dicom"`, one level with the expected dims/tile size, and the level's first raw tile is byte-identical to the source. (This is the automatable half of the success bar.)

- [ ] **Step 4: Commit**
```bash
git add cmd/wsitools/convert_dicom.go cmd/wsitools/convert.go
git commit -m "feat(convert): --to dicom (P0 single WSM VOLUME instance) + opentile read-back

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Conformance validation (the de-risk) — `dciodvfy` to 0 errors

**Files:** modify `Makefile`; possibly iterate on `dataset.go`.

This is the crux of the spike. The external validator IS the conformance spec; expect to **loop back to Task 2's attribute set** until clean.

- [ ] **Step 1: Install the validator**
`brew install dicom3tools` (provides `dciodvfy`). Confirm `which dciodvfy`. (Fallback: `pip install highdicom pydicom` and validate by parsing as `highdicom.SM` / `hd.imread`.)

- [ ] **Step 2: `make dicom-validate` target**

Add to `Makefile`:
```make
# Emit a DICOM WSM instance from the Grundium fixture and validate conformance.
# Requires dciodvfy (brew install dicom3tools).
dicom-validate: build
	@SM=$$(ls sample_files/dicom/scan_621_grundium_dicom/*pyr04.dcm); \
	$(BIN) convert --to dicom --level 0 -o /tmp/wsitools-wsm.dcm "$$SM"; \
	echo "=== dciodvfy ==="; dciodvfy /tmp/wsitools-wsm.dcm
```

- [ ] **Step 3: Run + fix to zero errors**

Run `make dicom-validate`. Read every `dciodvfy` Error (warnings are acceptable initially; **Errors must reach 0**). Common gaps and fixes (iterate in `dataset.go`):
- missing Type-1/Type-2 attributes for a required module → add them (empty for Type-2);
- wrong VR / value multiplicity → fix the element data type;
- missing FrameOfReference / Dimension / Optical Path / Specimen module attributes → add per the WSM IOD;
- PhotometricInterpretation vs SamplesPerPixel/PlanarConfiguration mismatch → align to the JPEG.
Re-run until **0 Errors**. Capture the final `dciodvfy` output in the commit message / a note. **If a conformance gap proves structurally hard (e.g. suyashkumar/dicom can't express a required SQ), STOP and report it** — that's exactly the swamp-vs-tractable signal this spike exists to surface.

- [ ] **Step 4: Cross-check + commit**
Optional: `python3 -c "import highdicom, pydicom; ds=pydicom.dcmread('/tmp/wsitools-wsm.dcm'); print(ds.SOPClassUID, ds.NumberOfFrames)"`.
```bash
git add Makefile internal/dicomwriter/
git commit -m "feat(dicomwriter): pass dciodvfy with 0 errors; make dicom-validate target

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Docs

**Files:** `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md`.

- [ ] **Step 1: Document P0**
- README: note `convert --to dicom` exists as a **Phase 0 spike** — emits a single WSM VOLUME instance from a **DICOM source only**, one level, verbatim JPEG tile-copy; not yet a full converter (no full pyramid / non-DICOM sources / sparse / fluorescence). Point at the limitations.
- CHANGELOG `[Unreleased]`: "Experimental `convert --to dicom` (DICOM-WSI writer, **Phase 0**): emits one conformant WSM VOLUME instance from a DICOM source (verbatim JPEG frame copy), validated with `dciodvfy`. DICOM→DICOM only; full pyramid / non-DICOM sources / colorspace reconciliation are later phases."
- roadmap: mark DICOM writer **Phase 0 DONE**; P1 (full pyramid + non-DICOM sources + colorspace) next, with the spike's `dciodvfy` outcome noted.
- scoping note: add a "P0 outcome" line (tractable vs swamp; key learnings; the working suyashkumar/dicom construction patterns).

- [ ] **Step 2: Verify + commit**
`go build ./... && go test ./internal/dicomwriter/ ./cmd/wsitools/ -run 'DICOM|Dicom|dicom' -count=1` → PASS.
```bash
git add README.md CHANGELOG.md docs/roadmap.md docs/notes/2026-06-03-dicom-writer-scoping.md
git commit -m "docs: DICOM-WSI writer Phase 0 (convert --to dicom, single WSM instance)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final review

Dispatch a final reviewer (focus: the dataset mirrors the WSM IOD correctly; frames are byte-identical / verbatim; TILED_FULL frame order is right; UIDs valid; the `dciodvfy` 0-error result is genuine; errors clear for out-of-scope inputs). Then use `superpowers:finishing-a-development-branch`.

## Self-review notes (author)

- **Spec coverage:** scaffold+UID+library round-trip (T1), IOD assembly (T2), encapsulation+WriteVolumeInstance (T3), CLI+read-back (T4), conformance validation/de-risk (T5), docs (T6). The success bar (dciodvfy 0 errors + opentile read-back + byte-identical frames) is covered by T5 + T3/T4 tests.
- **Spike honesty:** T1 step 4 and T5 explicitly nail the library API and the validator-required attribute set empirically — appropriate for a conformance spike (the validator is the spec). T5 carries the explicit "if structurally hard, STOP and report" swamp signal.
- **Type consistency:** `NewUID`, `UIDSet`, `assembleWSMDataset(src,level,uids)`, `encapsulatePixelData(src,level)`, `WriteVolumeInstance(w,src,level,Options)`, `runConvertDICOM`, `--level`/`cvDICOMLevel` used consistently.
- **Scope:** DICOM→DICOM single VOLUME instance only; every harder axis is explicitly a later phase.
- **Flagged confirmations:** suyashkumar/dicom const names (`uid.JPEGBaseline8Bit`), SQ-item construction, IS-VR value typing, BOT/offsets expectation, `ImplementationClassUID` requirement — all resolved empirically in T1/T2, not guessed.
