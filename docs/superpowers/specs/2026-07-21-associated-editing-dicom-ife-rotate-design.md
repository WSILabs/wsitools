# Associated-image editing for DICOM and IFE, + `label rotate` — Design

**Goal:** Extend associated-image editing (`remove`/`replace`, and a new `rotate`)
to the two writable formats currently excluded — **DICOM-WSI** and **IFE** — and
add a `label rotate {90,180,270}` verb that lands across every editable format at
once.

**Architecture:** Reuse the existing per-format editing dispatch. DICOM uses a
**surgical single-instance** edit (touch only the label `.dcm`, pyramid untouched);
IFE uses a **writer rebuild** (pyramid verbatim, associated re-encoded), modeled on
the COG-WSI path. `rotate` is a type-generic operation layered on the `replace`
path (decode → rotate → re-emit, preserving the lossless encoding).

**Tech stack:** Go; `internal/dicomwriter` (on `WSILabs/dicom`), `internal/ife`,
`cmd/wsitools/associated*.go`, `internal/source`, opentile-go reader.

---

## Background — how editing works today

`cmd/wsitools/associated.go` gates on a format allowlist and dispatches:

- `assocFormatSupported(format)` (`associated.go:61`) — currently `{SVS,
  GenericTIFF, COGWSI, OMETIFF}`; `gateFormat` (`:70`) rejects others with
  `ErrUnsupportedAssoc`.
- `runAssociatedRemoveFor` (`:164`) / `runAssociatedReplaceFor` (`:243`) dispatch
  by format: **splice** (SVS/generic-TIFF — tail-IFD rewrite via `edit.Splice`,
  with a rebuild fallback on `ErrUnexpectedLayout`) or **writer rebuild** (COG-WSI
  → `runAssociated…ForCOGWSI`; OME-TIFF → `runAssociated…ForOMETIFF`).
- Rebuild pattern (`associated_rebuild.go`): open source → iterate
  `src.Associated()` → apply an edit plan (skip removed / swap replacement spec) →
  re-finalize through the format writer. `writeCOGWSIAssociated` (`:103`) is the
  reference; `faithfulCOGWSISpec` preserves an unchanged image's original encoding.
- Replacement image handling (`associated.go` / `associated_replace.go`):
  `decodeReplacementImage` (`:392`) loads PNG/JPEG/TIFF; `resolveTargetDims`
  (`:419`) picks dims (existing size, `--label-dims`, or type default);
  `buildReplacementAssocSpec` (`associated_replace.go:248`) encodes into a
  writer-specific spec (label → LZW+Predictor default; others → JPEG).
- Output path: `resolveAssocOutput` (`:87`) is **file-based** (`<stem>_edited<ext>`,
  or `--in-place`, or `-o`).

Adding a format = one `assocFormatSupported` entry + a `runAssociated…For<FMT>`
implementation. DICOM needs one extra thing the others don't: **directory** I/O.

---

## Locked decisions (from scoping dialogue)

1. **DICOM = surgical, single-instance.** A DICOM series is a directory of
   independent `.dcm` instances; nothing references the label, so a label edit
   touches exactly one file. Never re-emit or re-UID the pyramid. (Rejected:
   rebuild-via-`WritePyramid`, which would rewrite every instance with new
   Study/Series UIDs — wrong to re-stamp identity on unchanged pyramid data.)
2. **IFE = writer rebuild**, pyramid verbatim tile-copy (lossless), associated
   re-encoded to PNG. Modeled on COG-WSI.
3. **`rotate` = type-generic core, label-only exposure.** Built on the `replace`
   path but gated by a `rotatableTypes` allowlist that starts as `{label}`.
   Preserves the label's lossless encoding; 90/270 swap W/H. Extending to another
   type later = add it to `rotatableTypes` + register one subcommand. `macro` is
   intentionally excluded (rotating a whole-slide overview photo is meaningless).
4. Replacement/rotated label encoding stays **lossless & barcode-safe** per
   format: LZW+Predictor (TIFF family), **native RGB** (DICOM), **PNG** (IFE).

---

## Phase 1 — DICOM `remove` / `replace`

### Model

A DICOM source is a directory of UID-named `.dcm` instances (pyramid levels +
associated), optionally with a `DICOMDIR`. The edit operates on the single label
instance:

- **`remove`** → delete the label instance file.
- **`replace`** → delete the old label instance, write a new native-RGB label
  instance carrying the series' shared UIDs.
- **in-place** → mutate the directory; **output-copy** → clone the directory
  (plain file copy of the *other* instances) then apply the edit in the copy.

The pyramid instances are copied byte-for-byte (output-copy) or left untouched
(in-place) — never parsed or re-emitted.

### New components

1. **`internal/dicomedit`** (new package) — DICOM-file-level utilities that sit
   below opentile's decoded abstraction:
   - `ClassifyInstances(dir string) ([]InstanceInfo, error)` — enumerate `*.dcm`,
     read each one's `ImageType` (0008,0008)[2] with **stop-before-pixels**, and
     classify into `{level, label, overview, thumbnail, macro, other}`. Returns
     path + role per file. (Uses `WSILabs/dicom` read.)
   - `ReadSharedUIDs(path string) (dicomwriter.SharedUIDs, error)` — read
     Study/Series/FrameOfReference/DimensionOrganization UIDs from one existing
     instance, for a replacement instance to join the series.
   - `FindDICOMDIR(dir)` / handling — if present, drop it in the output (an edited
     directory's DICOMDIR is stale; regeneration is out of scope, deletion is
     safe — DICOMDIR is optional media metadata, not required to open the series).

2. **`internal/dicomwriter` exports** (promote existing unexported logic):
   - `type SharedUIDs` — export the existing `sharedUIDs` struct.
   - `WriteAssociatedInstance(w io.Writer, src source.Source, a
     source.AssociatedImage, shared SharedUIDs, instanceNumber int) error` — an
     exported wrapper over `writeAssociated` (`dicomwriter.go:142`) that emits ONE
     associated instance. It takes the full `source.Source` because
     `writeAssociated` reads both `src.Metadata()` (ICC/MPP/magnification) and
     `src.Levels()[0].Size()` (for the label's nominal spatial attributes). It
     already handles native RGB via `a.Decode()` → `nativePixelData`
     (`native.go:35`) and sets the SlideLabel module for label/overview
     (`dataset.go:322`). The caller supplies `shared` (read from the series) and
     `instanceNumber` (reuse the removed label's, or max existing + 1).

3. **`cmd/wsitools`** driver:
   - `runAssociatedRemoveForDICOM(typ, input, outDir string, fl removeFlags)` —
     classify → locate the `typ` instance → (in-place) delete it / (copy) clone
     dir minus that file. Error `ErrNoSuchAssociated` if absent.
   - `runAssociatedReplaceForDICOM(typ, input, outDir string, fl replaceFlags)` —
     classify → clone dir (or in-place) → `ReadSharedUIDs` from any level →
     decode replacement image → wrap it as a synthetic `source.AssociatedImage`
     (native-RGB Decode) → `WriteAssociatedInstance` → write `<newuid>.dcm` into
     the output dir; delete the old label file.
   - A synthetic `source.AssociatedImage` adapter for the replacement RGB (Type()
     = typ; Decode() returns the resized RGB; Bytes()/Encoding() unused →
     native path).

### Output ergonomics (the DICOM-specific wrinkle)

`resolveAssocOutput` is file-based. Add DICOM-directory handling:
- Detect a directory (or single `.dcm` → its series dir) input.
- `--in-place` → operate on the input dir.
- `-o <dir>` → that dir (error if exists && !force, mirroring `convert --to
  dicom`).
- Derived default → `<dirname>_edited/`.
- Write atomically: build into a temp sibling dir → `os.Rename` (reuse the
  `emitDICOM` temp-dir→rename idiom).

### Encoding

Replacement label → **native RGB** (Explicit VR LE, VR OB), lossless and
barcode-safe. This reuses the existing `writeAssociated` native path (already
validated by `TestWriteAssociatedLZWLabelNative`, `associated_test.go:139`).

### Allowlist

`assocFormatSupported` += `FormatDICOM`. Gate message updated.

---

## Phase 2 — IFE `remove` / `replace`

### Model

Rebuild through `ife.Writer`, modeled on COG-WSI. The pyramid copies **verbatim**
when the source is 256px JPEG/AVIF-tiled (always true for a wsitools-written IFE)
→ lossless. Associated images are re-encoded to PNG (the existing non-JPEG/AVIF
IFE path).

### New components

1. **`cmd/wsitools`** driver (parallels `runAssociated…ForCOGWSI`):
   - `runAssociatedRemoveForIFE(typ, input, outPath, fl)` /
     `runAssociatedReplaceForIFE(typ, input, outPath, fl)` — open source → reuse
     the `convert --to ife` machinery (`runConvertIFE` → verbatim pyramid path,
     `convert_ife.go:274`) with an **edit plan** threaded into
     `assembleIFEMetadata`/`addIFEAssociated` (`convert_ife.go:299,315`).
   - `addIFEAssociated` already iterates `src.Associated()` and encodes each
     (JPEG/AVIF verbatim via `a.Bytes()`, else decode→PNG). Extend it to accept an
     `assocEditPlan{remove/replace/img}`: skip the removed type; for replace, use
     the provided image (encode to PNG) instead of the source's.

2. **`image → PNG blob` helper** — IFE's `AddAssociated(title, w, h, encoding,
   blob)` (`ife/writer.go:105`) needs a **pre-encoded** blob (no `AssociatedSpec`
   like COG-WSI). Add `encodeAssocPNG(img image.Image, tw, th int) (blob []byte,
   w, h uint32, err error)` reusing the PNG codec already wired in `convert_ife.go`.

### Reuse

`decodeReplacementImage` / `resolveTargetDims` / `fitImage` are format-agnostic —
reuse as-is. IFE round-trip readability is confirmed (`tests/integration/ife_test.go`).

### Allowlist

`assocFormatSupported` += `FormatIFE`. Gate message updated.

---

## Phase 3 — `label rotate {90,180,270}`

### Model

`rotate` is `replace` where the new image is the **existing** image, rotated:

1. Locate + `Decode()` the target associated image (RGB).
2. Rotate the RGB buffer 90/180/270 (a small pixel op; 90/270 swap W/H).
3. Feed through each format's **replace** path, but **preserve the source's
   lossless encoding** rather than defaulting (LZW+Predictor for TIFF family,
   native RGB for DICOM, PNG for IFE) — so a barcode stays lossless.

Because it terminates in the same per-format replace code, implementing rotate
**after** Phases 1–2 makes it land on all six editable formats at once.

### New components

- `runAssociatedRotateFor(typ, degrees int, input, outPath string, fl
  rotateFlags)` — type-generic; gated by `rotatableTypes = {"label"}`. Rejects
  non-rotatable types and degrees ∉ {90,180,270}.
- `rotateRGB(pix []byte, w, h, degrees int) (out []byte, ow, oh int)` — pure
  rotation helper (in a `cmd/wsitools` util or `internal/imageutil`).
- Wire `resolveTargetDims` for rotate to use **swapped** dims on 90/270 (not
  "reuse existing size").
- CLI: register only the `label rotate <deg>` subcommand (`rotate` sits alongside
  `remove`/`replace` under the `label` command group).

### Encoding-preservation note

The existing replace path defaults label→LZW; for rotate we want to *match the
source's* codec so a JPEG-framed label stays JPEG, an LZW label stays LZW, etc.
Add a "preserve source encoding" mode to the replace-spec builders (or pass the
source `AssociatedImage`'s compression through). For DICOM/IFE the lossless target
(native/PNG) is already the default.

---

## CLI & UX

- `wsitools label remove <dicom-dir|dicom.dcm|slide.ife>` → `<name>_edited`
  (dir for DICOM, file for IFE) / `--in-place` / `-o`.
- `wsitools label replace --image new.png <…>` — same flags as today.
- `wsitools label rotate 90 <…>` (also `180`, `270`). Optional `--cw`/`--ccw`
  sugar deferred; positional degrees only for v1.
- DICOM output is a directory; IFE/TIFF-family output is a file. `resolveAssocOutput`
  branches on whether the source format is directory-shaped (DICOM).

---

## Testing strategy

Integration (fixture-gated), one per format × op:

- **DICOM:** `label remove` on a DICOM series → reopen → assert no label
  associated; every level instance **byte-identical** to the source (surgical
  guarantee); DICOMDIR dropped. `label replace` → reopen → label present, decodes
  to the replacement; levels byte-identical. `label rotate 90` → reopen → label
  dims swapped, pixels = source label rotated; levels byte-identical.
- **IFE:** `label remove`/`replace`/`rotate 90` → reopen via opentile → assert
  associated change; pyramid tiles **pixel-identical** (verbatim path).
- **Rotate correctness:** unit-test `rotateRGB` for 90/180/270 (dims + a known
  pixel pattern, incl. the transpose direction).
- **Gate:** `label rotate macro` (or any non-rotatable type) → clear rejection.
- Reuse the existing `TestUnsupportedFormatRejected` shape; add
  `TestWriteAssociatedInstance` in `internal/dicomwriter` for the exported entry
  point.
- `dciodvfy` (controller-run) on the edited DICOM series — 0 errors on the new
  label instance + unchanged levels.

## Docs

- `docs/formats.md` + README matrix: `edit` column → **DICOM ✓, IFE ✓**.
- `docs/commands.md`: add `rotate` under associated-image editing; note DICOM
  directory I/O and the native/PNG lossless encodings; update the "not editable"
  list (now only NDPI/Philips/Leica/BIF).
- `docs/roadmap.md`: move the DICOM/IFE-editing + label-rotate items from
  backlog to shipped.

---

## Risks / open questions

- **Classifying scanner DICOM.** `ImageType[2]` is the primary label signal, but
  some vendors differ; fall back to `SpecimenLabelInImage` / flavor. Validate
  against the Leica-4 and Grundium fixtures during Phase 1.
- **DICOM in-place safety.** In-place delete/replace mutates the user's series;
  guard with the same temp-then-rename discipline even in-place (build the new
  directory state in a temp sibling, then swap).
- **IFE associated read-back** requires opentile-go ≥ v0.49.0 (PNG associated);
  already the floor. Non-JPEG/AVIF source associated images round-trip via PNG.
- **Rotate encoding-preservation** touches the replace-spec builders; keep it
  behind an explicit "preserve" flag so `replace` behavior is unchanged.
- **DICOMDIR regeneration** deferred (dropped instead) — acceptable since it's
  optional; revisit if a consumer requires it.

## Out of scope

- Whole-slide (pyramid) rotate/flip — separate, larger roadmap item (lossless via
  DCT-coefficient transforms).
- Rotate on macro/thumbnail/overview (machinery ready; not exposed).
- `flip`/`mirror` on any associated image (a mirrored barcode is never valid).
- Editing NDPI/Philips/Leica (no writer) and BIF (label embedded in the overview).
- DICOM series-UID-preserving *rebuild* (surgical chosen instead).
