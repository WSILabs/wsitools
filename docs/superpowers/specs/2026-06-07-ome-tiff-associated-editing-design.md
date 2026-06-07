# OME-TIFF associated-image editing (Slice 2b, lossy) — design

> Status: **approved design** (brainstormed 2026-06-07). Next: writing-plans.
> Branch off `main`; never implement on `main`.
> Completes the associated-image editing feature (Slice 1: SVS+generic-TIFF
> splice; Slice 2a: COG-WSI rebuild) by adding OME-TIFF — **explicitly lossy**.

## Goal

Make `wsitools label|macro|thumbnail|overview remove|replace` work on **OME-TIFF**
by rebuilding the file through the existing ome-tiff streamwriter, dropping or
replacing the target associated image. Because wsitools' OME-TIFF support is
geometry-minimal (see Background), the rebuild **regenerates a minimal OME-XML**
and loses non-geometry OME metadata. This is an accepted, **explicitly surfaced**
limitation — not silent.

## Background

### Why lossy / why a rebuild
OME-TIFF's data model is "N image *series*, each with its own pyramid." wsitools'
model is "*one* pyramid + associated images." When opentile-go reads a multi-image
OME-TIFF (e.g. real Leica: a `macro` series + the main pyramid series), it
**flattens** them to "1 pyramid + 1 associated image." Verified on
`sample_files/ome-tiff/Leica-1.ome.tiff`: opentile presents L0–L4 + an `overview`
associated image; `info`/`convert` work.

Consequences:
- The in-place **splice (Slice 1) cannot edit OME-TIFF** — the associated image
  may be IFD0 (it carries the 9 KB OME-XML), the pyramid lives in SubIFD trees,
  top-level IFDs are multiple, and offsets are aliased.
- A **rebuild emits wsitools' flattened model**, so the OME-XML *must* be
  regenerated to describe what was actually written. Carrying + editing the
  source OME-XML is incoherent with a rebuild (it describes a structure the
  output no longer has). So **synthetic (regenerated) OME-XML is the only coherent
  choice for a rebuild** — and it is lossy.

### What wsitools' OME-TIFF writer does today
`convert --to ome-tiff` (`convert_tiff.go`) writes a **valid but minimal**
OME-TIFF: SubIFD pyramid + a synthetic OME-XML built by string templating
(`ome_imagedesc.go`: `SyntheticOMEDescription*`, `writeOMEImage`) with
`<Image>/<Pixels>` geometry (dims, `PhysicalSize`=MPP, `NominalMagnification`)
and an `<Image>` per associated image (`omeAssociatedSpecs`). It does **not**
emit `<Instrument>/<Objective>/<Channel>/<Plane>/<StructuredAnnotations>`, and
does **not** model OME multi-series. So the lossiness here is inherent to the
existing writer, not new to this feature.

### What "lossy" discards (real Leica example)
Source OME-XML (9.4 KB, written by `OME Bio-Formats 6.0.0-rc1`) carries, in
**standard OME-XML schema elements**: `<Instrument>` (2 objectives w/ NA),
per-`<Image>` acquisition dates + stage `<Plane>` positions + `<Channel>`, and 29
`<XMLAnnotation>` `OriginalMetadata` entries (Leica scan settings, device model)
plus `<MapAnnotation>` pyramid-resolution maps. **All of this is discarded** by
the synthetic regeneration; only dims/MPP/magnification survive — for the
surviving pyramid as well as the removed image.

## Scope (decided)

- **Format:** OME-TIFF.
- **Ops:** `remove` and `replace` (upsert), all four types.
- **Engine:** rebuild via the ome-tiff streamwriter (the COG-WSI Slice-2a pattern
  applied to `convert_tiff.go`'s path), forcing synthetic OME-XML.
- **Lossy + surfaced** (see Surfacing). Pixels (verbatim tiles), other associated
  images, dims/MPP/magnification/resolution/ICC preserved; other OME metadata not.
- **Out of scope:** preserving vendor/instrument/annotation OME metadata (would
  require a real OME data model + raw IFD-graph re-serializer — a separate, large
  future effort); `--rotate`; `--if-exists`.

## Surfacing the limitation (first-class requirement)

The feature must make the lossiness obvious, in three places:

1. **Runtime warning — always shown** (stderr), on every OME-TIFF `remove`/
   `replace`, regardless of flags:
   > `warning: OME-TIFF editing rebuilds the file with a regenerated, minimal OME-XML — instrument, acquisition, channel, and vendor annotations are NOT preserved (pixels, geometry/MPP/magnification, and the other associated images are). wsitools' OME-TIFF support is rudimentary; for serious OME-TIFF work use Bio-Formats. See docs/ome-tiff-limitations.md.`
2. **`docs/ome-tiff-limitations.md`** (new) — states plainly: wsitools' OME-TIFF
   writer is geometry-minimal, does not model OME multi-series, `convert --to
   ome-tiff` and associated-editing both regenerate a minimal OME-XML, and
   **recommends [Bio-Formats](https://www.openmicroscopy.org/bio-formats/)** (and
   `bioformats2raw`/`raw2ometiff` for pyramids, `tifffile` for Python) for
   serious OME-TIFF work. Linked from the README.
3. **README** — a short "OME-TIFF support is rudimentary" note near the
   conversion/editing sections pointing at `docs/ome-tiff-limitations.md` and
   Bio-Formats; matrix cell `✓⁹` for the associated-editing column with footnote
   ⁹ = "lossy — regenerates a minimal OME-XML; see OME-TIFF limitations."
   **CHANGELOG** records the lossy behavior.

## Architecture

### Command surface (no new commands)
The Slice 1/2a commands gain OME-TIFF by extending the dispatch in
`cmd/wsitools/associated.go`:
```
src.Format():
  "svs" | "generic-tiff" → splice engine          (Slice 1)
  "cog-wsi"              → cogwsiwriter rebuild     (Slice 2a)
  "ome-tiff"            → ome-tiff streamwriter rebuild  (this slice)
  otherwise             → ErrUnsupportedAssoc
```
All shared flags/output rules (`-o`/`--in-place`/`--overwrite`/`--fsync`/`-q`;
replace flags) carry over. `assocFormatSupported` adds `"ome-tiff"`.

### Rebuild engine (`cmd/wsitools/associated_rebuild_ometiff.go`)
`runAssociatedRemoveForOMETIFF` / `runAssociatedReplaceForOMETIFF` call a shared
`rebuildOMETIFF(src source.Source, outPath string, plan omeEditPlan, fsync bool) error`:
1. Emit the **always-on runtime warning**.
2. Compute the **edited associated set**: source associated minus the removed
   type, or with the target substituted (a decoded+encoded replacement). Used for
   BOTH the OME-XML `<Image>` list AND the associated-IFD writes so they stay in
   lockstep (the existing `omeAssociatedSpecs`/`writeAssociatedImages` order
   invariant).
3. `streamwriter.Create(temp, opts)` with the L0 ImageDescription set to a
   **synthetic OME-XML** generated from the edited set (`SyntheticOMEDescription*`
   + `omeAssociatedSpecs` filtered by the plan). Force synthetic regardless of
   source format (do NOT carry the source OME-XML).
4. Copy pyramid levels via verbatim tile-copy (the `runConvertTIFFTileCopy`
   loop — `TileInto`/`AddTile`, no re-encode).
5. Write associated images via `writeAssociatedImages` parameterized with the
   plan (skip removed / substitute replaced / append upsert).
6. `w.Close()`; atomic temp→rename over `outPath` (safe for `--in-place`); fsync
   errors propagated (mirroring Slice 2a's `rebuildCOGWSI`).

`omeEditPlan` mirrors Slice 2a's `assocEditPlan` but the replacement is packaged
as a `streamwriter.StrippedSpec` (what `writeAssociatedImages` consumes), not a
`cogwsiwriter.AssociatedSpec`.

### Shared-core reuse
`convert_tiff.go`'s tile-copy level loop and `writeAssociatedImages` /
`omeAssociatedSpecs` are reused. `writeAssociatedImages` and `omeAssociatedSpecs`
gain an optional edit-plan parameter (skip/substitute/append the target);
`convert` passes a no-op plan so its behavior is unchanged (regression net =
existing convert ome-tiff tests). Keep the change minimal; `convert_tiff.go` is
already large — do not expand its responsibilities beyond threading the plan.

### Replace path
Reuse the Slice-1 image helpers (`decodeReplacementImage`, `fitImage`,
`resolveTargetDims`, `parseHexColor`, the strip/JPEG encoders) and package the
result as a `streamwriter.StrippedSpec` (RGB strip(s), Width/Height, Compression,
Photometric=2, `WSIImageType=typ`, `NewSubfileType` per `newSubfileTypeForAssoc`).
OME-TIFF associated images are self-contained JPEG/LZW → replacements round-trip
(no SVS abbreviated-JPEG limitation). Per-type codec default (label→LZW,
others→JPEG) + `--compression` override, as Slice 1/2a.

## Data flow

```
remove:
  source.Open(ome-tiff) → warn(lossy)
  editedAssoc = Associated() minus typ   (error if typ absent)
  streamwriter ome-tiff @temp:
    L0 ImageDescription = synthetic OME-XML(editedAssoc)
    copy pyramid tiles verbatim
    writeAssociatedImages(plan: skip typ)
  Close → atomic rename → outPath

replace:
  source.Open → warn(lossy); find existing typ (dims; absent = upsert)
  decode --image → resize → encode → StrippedSpec
  streamwriter ome-tiff @temp:
    L0 ImageDescription = synthetic OME-XML(editedAssoc with typ substituted)
    copy pyramid tiles verbatim
    writeAssociatedImages(plan: substitute/append typ)
  Close → atomic rename → outPath
```

## Error handling

| Condition | Behavior |
|---|---|
| `remove`, type absent | error `no <type> image in slide` |
| `replace`, `--image` missing/undecodable | error |
| unsupported `--compression` | error |
| `-o` and `--in-place` both set | error |
| resolved output == input (non-in-place) | error |
| aspect mismatch >2× without `--force` | error |
| writer error mid-build | `w.Abort()` / remove temp; no partial output |

## Testing

`make test` (`-race`); integration gated by `WSI_TOOLS_TESTDIR`.

**Fixtures:** synthesize a small OME-TIFF from the SVS fixture via the existing
ome-tiff convert path (the Slice-2a synth-fixture trick, so tests run in CI where
only the SVS fixture exists); use the real `ome-tiff/Leica-1.ome.tiff` where
present (large — keep such tests light or `t.Short`-skippable).

**Integration (gated):**
- `label remove` ⇒ output reopens as `ome-tiff`; `label` absent; **other
  associated images survive**; **pyramid `hash --mode pixel` identical**;
  output OME-XML parses + lists the remaining images only.
- `overview replace --image <png>` ⇒ overview present at expected dims, decodes
  back via `image.Decode`; pyramid hash identical.
- `--in-place`: original edited, no temp leftover.
- `remove` of an absent type ⇒ error.
- **Warning emitted:** capture stderr and assert the lossy warning appears.
- **convert non-regression:** existing `convert --to ome-tiff` tests pass after
  the edit-plan threading (synthetic-vs-carry behavior unchanged for `convert`).

**Unit:**
- edit-plan filtering of `omeAssociatedSpecs` (removed type absent from `<Image>`
  list; substituted type present with new dims).

## File structure

| Path | Responsibility |
|---|---|
| `cmd/wsitools/associated_rebuild_ometiff.go` (new) | `rebuildOMETIFF`, remove/replace entry points, `omeEditPlan`, the lossy warning |
| `cmd/wsitools/convert_tiff.go` (modify) | thread an edit-plan param through `writeAssociatedImages` + `omeAssociatedSpecs`; `convert` passes a no-op plan |
| `cmd/wsitools/associated.go` (modify) | allow `ome-tiff`; dispatch to the rebuild engine |
| `cmd/wsitools/associated_replace.go` (reuse) | image→`streamwriter.StrippedSpec` packager |
| `cmd/wsitools/associated_integration_test.go` (extend) | gated ome-tiff remove/replace/in-place + warning assertion |
| `docs/ome-tiff-limitations.md` (new) | rudimentary-support statement + Bio-Formats recommendation |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md` | matrix `✓⁹`, limitations note, Slice 2b done |

## Out of scope (future)

- Faithful OME-TIFF editing (preserve instrument/annotation/multi-series
  metadata) — needs a real OME data model + raw IFD-graph re-serializer
  (offset aliasing, IFD0/OME-XML relocation). Large, fragile; deferred
  indefinitely unless demand justifies it. Until then: Bio-Formats.
- DICOM associated-instance drop/swap; `--rotate`; `--if-exists`.

## Open questions

None blocking. The faithful-fidelity engine is explicitly deferred; lossiness is
surfaced rather than solved.
