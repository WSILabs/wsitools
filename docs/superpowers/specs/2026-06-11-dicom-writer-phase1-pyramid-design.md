# DICOM-WSI writer — Phase 1 slice 2: full pyramid — Design

**Status:** approved design, pre-plan.
**Date:** 2026-06-11
**Predecessors:**
- Phase 0 (`2026-06-11-dicom-writer-phase0-design.md`) — DICOM→DICOM, one WSM VOLUME instance, single level.
- Phase 1 slice 1 (`2026-06-11-dicom-writer-phase1-svs-design.md`) — non-DICOM (SVS) single level; marker-driven photometric, sRGB ICC synthesis.

Both shipped to `main`. This slice extends `convert --to dicom` from one instance (one level) to the **full pyramid** as a multi-instance DICOM Series.

## Goal

`convert --to dicom -o <dir> <input>` (DICOM or non-DICOM JPEG-baseline source)
emits the **entire resolution pyramid** as a multi-instance DICOM Series: one
conformant WSM VOLUME instance per source level, written to
`<dir>/level-<n>.dcm`, all sharing Study / Series / FrameOfReference UIDs, with
correct per-level spatial metadata so the levels co-register. The shipped
single-level path is preserved verbatim via `--level N`.

## Locked scope decisions (from brainstorming)

1. **Full pyramid is the default; `--level N` selects a single level.** Absence of
   `--level` → emit all levels to a directory. Presence of `--level N` → the
   existing single-instance-to-one-file path, unchanged.
2. **Directory output, `level-<n>.dcm` naming** (n = source level index, 0 =
   full resolution).
3. **Classic multi-instance pyramid model — no Pyramid UID.** Confirmed against
   the Grundium golden: shared `SeriesInstanceUID` + `FrameOfReferenceUID`,
   distinct `SOPInstanceUID` per instance, sequential `InstanceNumber`, per-level
   `TotalPixelMatrix`/`PixelSpacing`. No `(0020,0027)` Pyramid UID.
4. **Writer-factory API** — `dicomwriter` stays io-agnostic: the package owns the
   level loop + shared-UID generation and asks the caller for a writer per level.
5. **Per-level spatial-metadata correctness** — fix the latent bug where
   `PixelSpacing`/`ImagedVolume` were derived from base MPP × level size for all
   levels (correct only at L0). Lands in `assembleWSMDataset`, fixing the
   single-level non-L0 path too.
6. **Atomic directory output** — write into a temp dir, rename into place on
   success; remove on any failure (never a half-written pyramid).

## Architecture

### Refactor: extract `writeInstance`

The per-level body of `WriteVolumeInstance` becomes an internal helper:

```
type sharedUIDs struct{ Study, Series, FrameOfReference, DimensionOrg string }

func writeInstance(w io.Writer, src source.Source, level int, shared sharedUIDs, instanceNumber int) error
```

`writeInstance` does what `WriteVolumeInstance` does today (encapsulate-first →
`buildDescriptor` → `assembleWSMDataset` → append PixelData → `dicom.Write`),
except it takes the shared UIDs and the instance number instead of generating a
fresh full `UIDSet` and deriving `InstanceNumber` from `level`.

`UIDSet` (existing) is built per-instance inside `writeInstance` as
`{SOP: NewUID(), Study: shared.Study, Series: shared.Series, FrameOfReference:
shared.FrameOfReference, DimensionOrg: shared.DimensionOrg}` — so each instance
gets a unique SOP while the rest are shared.

### Public APIs

```
// Unchanged signature; now a thin wrapper. Single instance, one writer.
func WriteVolumeInstance(w io.Writer, src source.Source, level int, _ Options) error {
	shared := sharedUIDs{Study: NewUID(), Series: NewUID(), FrameOfReference: NewUID(), DimensionOrg: NewUID()}
	return writeInstance(w, src, level, shared, level+1)
}

// New: full pyramid. The factory supplies a writer per level; the caller closes it.
func WritePyramid(src source.Source, _ Options, newWriter func(level int) (io.WriteCloser, error)) error {
	shared := sharedUIDs{Study: NewUID(), Series: NewUID(), FrameOfReference: NewUID(), DimensionOrg: NewUID()}
	for level := range src.Levels() {
		w, err := newWriter(level)
		if err != nil { return fmt.Errorf("open writer for level %d: %w", level, err) }
		err = writeInstance(w, src, level, shared, level+1)
		cerr := w.Close()
		if err != nil { return fmt.Errorf("write level %d: %w", level, err) }
		if cerr != nil { return fmt.Errorf("close level %d: %w", level, cerr) }
	}
	return nil
}
```

`InstanceNumber = level + 1` (1-based, monotonic across the pyramid). This is
simpler than the golden's interleaved numbering (the golden interleaves
label/overview instances) and is conformant — InstanceNumber need only be unique
within the series, which it is.

### Per-level spatial metadata (`assembleWSMDataset`)

`assembleWSMDataset` already receives `src` and `level`. Add the L0 reference and
per-level scaling:

```
l0 := src.Levels()[0]
li := src.Levels()[level]
dsX := float64(l0.Size().X) / float64(li.Size().X) // downsample factor (≈1,4,16…)
dsY := float64(l0.Size().Y) / float64(li.Size().Y)
// base MPP (µm/px) from metadata, with the existing MPPX/MPPY/MPP fallback.
psX := baseMPPX * dsX / 1000.0 // mm/px at THIS level
psY := baseMPPY * dsY / 1000.0
// Physical slide extent is constant across levels — compute from L0.
imagedW := float64(l0.Size().X) * baseMPPX / 1000.0
imagedH := float64(l0.Size().Y) * baseMPPY / 1000.0
```

`PixelSpacing` (in SharedFunctionalGroups → PixelMeasures) emits `psY\psX`;
`ImagedVolumeWidth/Height` emit the constant extent. `TotalPixelMatrixColumns/
Rows = li.Size()`, `NumberOfFrames = li.Grid() product`, `Rows/Columns =
li.TileSize()` — all already per-level. When MPP is unknown (0), spacing/extent
fall back to 0 as today (no scaling applied to 0).

### CLI (`cmd/wsitools/convert_dicom.go`)

`runConvertDICOM` branches on `cmd.Flags().Changed("level")`:

- **single-level** (`--level` set): exactly today's code — one file at `cvOutput`.
- **full pyramid** (`--level` absent): `cvOutput` is a directory.
  1. Overwrite guard: if `cvOutput` exists and `!cvForce` → error.
  2. Create a temp dir as a sibling of `cvOutput` (e.g. `cvOutput + ".tmp-<pid>"` via `os.MkdirTemp(parent, ...)`).
  3. `WritePyramid(src, Options{}, factory)` where `factory(level)` creates
     `<tmp>/level-<level>.dcm` and returns the `*os.File`.
  4. On success: remove any existing `cvOutput` (when `--force`), `os.Rename(tmp, cvOutput)`.
  5. On any error: `os.RemoveAll(tmp)`, return wrapped error.
  6. Report: number of instances + total bytes + elapsed.

## Components / file structure

| Path | Change | Responsibility |
|---|---|---|
| `internal/dicomwriter/dicomwriter.go` | modify | `sharedUIDs`, `writeInstance`, `WriteVolumeInstance` (thin wrapper), `WritePyramid` |
| `internal/dicomwriter/dataset.go` | modify | per-level `PixelSpacing` (downsample-scaled) + constant `ImagedVolume` |
| `internal/dicomwriter/dicomwriter_test.go` | modify | `WritePyramid` unit tests (shared/distinct UIDs, per-level dims/spacing) |
| `internal/dicomwriter/dataset_test.go` | modify | per-level spacing assertions (L0 vs reduced level) |
| `cmd/wsitools/convert_dicom.go` | modify | `--level`-changed branch; directory output; temp-dir→rename atomicity |
| `cmd/wsitools/convert_dicom_test.go` | modify | full-pyramid CLI integration (files exist, each opens as dicom) |
| `Makefile` | modify | `dicom-validate` also emits + validates a full pyramid (every level) |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md` | modify | document the slice |

## Error handling

| Condition | Behavior |
|---|---|
| `-o` exists, no `--force` (pyramid) | error before writing anything |
| A level isn't JPEG-baseline (codec gate in `buildDescriptor`) | fail the whole conversion (clear error), remove temp dir — no partial pyramid |
| per-level write / close failure | remove temp dir, wrapped error |
| single-level path | unchanged (P1 behavior, partial-file cleanup) |

Rationale for fail-fast on a non-JPEG level: a pyramid with holes is incoherent;
in practice all resolution levels of an SVS/DICOM source share the codec.

## Testing

1. **`WritePyramid` unit** (gated, in-memory factory) for DICOM (Grundium) and
   SVS sources: instance count == `len(src.Levels())`; all share
   Study/Series/FrameOfReference UID; SOPInstanceUIDs distinct; `InstanceNumber`
   1..N; per-level `TotalPixelMatrixColumns` == each level's `Size().X`;
   `PixelSpacing` scales by the level's downsample; `ImagedVolumeWidth` identical
   across all instances.
2. **Per-level spacing unit** (`dataset_test.go`): assemble L0 and a reduced
   level; assert reduced-level `PixelSpacing` == L0 spacing × downsample and
   `ImagedVolumeWidth` equal for both.
3. **dciodvfy** (`make dicom-validate`): emit a full pyramid from a fixture and
   run dciodvfy on **every** `level-<n>.dcm` → 0 errors each.
4. **CLI integration** (CMU SVS): `convert --to dicom -o <dir>` produces
   `level-0.dcm … level-(N-1).dcm`; each `source.Open`s as `dicom`; the L0
   instance round-trips (reuse the slice-1 pixel check on level 0).
5. **Regression:** the single-level `--level N` path and the DICOM→DICOM
   single-instance output stay **structurally equivalent** — same attribute set,
   same per-level frame bytes, `InstanceNumber = level+1` — verified by the
   existing DICOM round-trip/assembly tests. (Output is never literally
   byte-identical across runs: UIDs are random and dates are `time.Now`; the
   `writeInstance` extraction changes the order of `NewUID()` calls, which is
   immaterial since the values are random regardless.)

## Success criteria

- `convert --to dicom -o out_dir CMU-1-Small-Region.svs` writes one
  `level-<n>.dcm` per source level; every instance passes `dciodvfy` (0 errors);
  all share Series/FrameOfReference UID with distinct SOPInstanceUIDs; per-level
  `PixelSpacing`/`ImagedVolume` are correct (reduced levels coarser spacing,
  constant physical extent).
- `--level N` and the DICOM→DICOM single-instance output are structurally
  unchanged (same attributes/frames; existing tests stay green).
- No partial pyramid is ever left on failure.

## Out of scope (later slices)

- JPEG 2000 / non-JPEG-baseline codecs → DICOM.
- Label / overview / thumbnail as separate DICOM instances (associated images).
- Pyramid UID / explicit pyramid-linkage tags.
- TILED_SPARSE, Concatenations (splitting an oversized level across instances).
- Re-encode path; fluorescence / multi-channel / z-stack.
