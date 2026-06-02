# OME-TIFF writer conformance (sub-project #2, phase 2) — design

**Date:** 2026-06-02
**Status:** approved, ready for planning
**Scope:** `convert --to ome-tiff` only (both tile-copy and re-encode paths).
svs / tiff / cog-wsi / dzi / szi / downsample targets are out of scope and
unchanged.

## Background and the bug

Sub-project #2 is per-target-format metadata conformance. Phase 1 covered
the SVS writer. This phase covers the OME-TIFF writer, where the audit found
a **correctness bug**, not just a tag gap:

`convert --to ome-tiff` on a multi-level slide writes every pyramid level as
a **top-level IFD** and emits no `SubIFDs` (330) tag; the OME-XML in IFD0's
ImageDescription describes only one `<Image>`/`<TiffData IFD="0">`. Reading
the result back, opentile's OME reader sees **only L0** — the other pyramid
levels are orphaned and invisible to any OME-aware reader (Bio-Formats,
QuPath, opentile). Verified: 6-level `CMU-1.svs` → ome-tiff round-trips to a
single 46000×32914 level. Associated images (label/macro/thumbnail) are
likewise written as orphan top-level IFDs that no OME reader surfaces.

Genuine OME-TIFF (Bio-Formats, e.g. `Leica-1.ome.tiff`) stores pyramid
sub-resolutions as **SubIFDs of the full-resolution IFD** (tag 330) — the
OME-TIFF 6.0 sub-resolution convention — and enumerates associated images as
additional `<Image>` elements. It also carries `SampleFormat` (339) and an
OME-XML preamble comment.

## Grounding

This design is validated against the **official OME-TIFF specification** and
the **OME 2016-06 XML schema** (distilled in
`docs/references/ome-tiff-spec-notes.md`, retrieved 2026-06-02) in addition to
the opentile reference reader and a genuine Bio-Formats fixture. The
spec-normative sub-resolution rules (SubIFDs tag 330; offsets ordered largest
to smallest; sub-res excluded from both the primary IFD chain and any
TiffData; `NewSubfileType` bit 0 = 1 on each downsampled level; largest planes
tiled) directly back R1/R2 below. Note the spec does **not** define
associated-image representation — that is a reader/Bio-Formats convention (see
R3).

## How the opentile OME reader works (the rules we must satisfy)

Read from `opentile-go/formats/ometiff/{ome.go,series.go}` (canonical):

1. **Pyramid sub-resolutions are read from SubIFDs.** `buildLevels` takes the
   main image's base page and walks `basePage.SubIFDOffsets()` for L1..Ln
   (`ome.go:117-140`). So writing sub-resolutions as SubIFDs of L0 makes them
   readable as a pyramid.
2. **Image→page mapping is positional.** `classifyImages` walks the OME-XML
   `<Image>` list; `basePage := pages[omeIdx]` and `pages[spec.omeIdx]` index
   the **top-level TIFF pages by the Image's position in the OME-XML**
   (`ome.go:85-135`). The TiffData `IFD` attribute is not used for mapping by
   opentile (Bio-Formats does use it; we set it consistently anyway). →
   **The OME-XML `<Image>` order MUST match the top-level IFD order.**
3. **Associated images are classified by exact `Name`.** `classifyImages`
   (`series.go:42-63`) trims the Image `Name` and exact-matches `"label"` →
   Label, `"macro"` → Macro (surfaced as kind `overview`), `"thumbnail"` →
   Thumbnail. **Anything else (including empty Name) is treated as a main
   pyramid.** → An associated image written with an unrecognized Name would be
   mis-read as a second pyramid. We must only emit associated `<Image>`
   entries with one of these three names.

## Requirements

### R1 — Pyramid as SubIFDs

On `convert --to ome-tiff`, the full-resolution level (L0) is the only
pyramid IFD in the top-level chain; pyramid levels L1..Ln are written as
**SubIFDs of L0** via tag `SubIFDs` (330). Reading the output back through
opentile MUST recover all N pyramid levels (regression test: `CMU-1.svs` →
ome-tiff → `info` reports the same level count as the source).

The 330 value type is **LONG8 on BigTIFF output, LONG on classic TIFF**,
count = number of sub-resolution levels, values = file offsets of the
sub-resolution IFDs **ordered by plane size from largest to smallest** (L1,
L2, …, Ln — which is largest-to-smallest for a standard descending pyramid),
per the OME-TIFF spec. Each sub-resolution IFD sets `NewSubfileType` (254)
bit 0 = 1 (reduced-resolution); L0 keeps bit 0 = 0. wsitools'
`newSubfileTypeForLevel` already produces this for non-SVS containers, so the
ome-tiff path inherits it.

### R2 — Top-level IFD order

The top-level IFD chain is: **L0, then the associated images in a fixed
order** (label, macro, thumbnail — whichever are present). Sub-resolution
IFDs are NOT in the top-level next-IFD chain (their NextIFD = 0; they are
reached only via L0's 330 tag). The header's first-IFD points at L0.

### R3 — OME-XML describes images positionally

The OME-XML `<Image>` list order MUST equal the top-level IFD order from R2:
Image 0 = the main pyramid (Name not in {label,macro,thumbnail}); then one
`<Image Name="label|macro|thumbnail">` per associated image, in the same
order they appear as top-level IFDs. Each associated `<Image>` carries a
`<Pixels>` with its own SizeX/SizeY (SizeC=3, SizeZ=1, SizeT=1, Type=uint8)
and a `<TiffData IFD="k" FirstC="0" FirstZ="0" FirstT="0" PlaneCount="1"/>`
where k is its top-level IFD position. The main image keeps `IFD="0"`.

Only associated images whose wsitools kind maps to a recognized OME name are
included: `label`→`label`, `macro`→`macro`, `thumbnail`→`thumbnail`,
`overview`→`macro`. Associated images of any other kind are **omitted from
both the OME-XML and the top-level IFD chain** (writing them would risk
mis-classification as a second pyramid). This is a deliberate, logged drop.

### R4 — SampleFormat tag

Every IFD wsitools writes for OME-TIFF output (L0, sub-resolutions, and
associated) carries `SampleFormat` (339) = 1 (unsigned integer), matching
genuine OME-TIFF. (wsitools only ever writes 8-bit unsigned RGB.)

### R5 — OME-XML preamble

The generated OME-XML includes the Bio-Formats preamble comment
(`<!-- Warning: this comment is an OME-XML metadata block, … -->`) after the
XML declaration, matching genuine OME files. (Detection still relies on the
`OME>` suffix, which is unaffected.)

### R6 — Scope isolation

No change to svs/tiff/cog-wsi/dzi/szi/downsample output. The SubIFD layout
and the SampleFormat/OME-XML changes are gated to the ome-tiff container.

## Design

### Components

1. **`internal/tiff/tags.go`** — add `TagSubIFDs uint16 = 330` (the name is
   already in the `tagnames.go` dictionary).

2. **`internal/tiff/streamwriter`** — SubIFD layout + SampleFormat:
   - `Options.SubResolutionPyramid bool` — when true, Close lays out pyramid
     levels ≥1 as SubIFDs of L0 (see "Close layout" below). Default false →
     today's flat top-level chain, untouched.
   - `Options.SampleFormat uint16` — when non-zero, every IFD emits tag 339
     with this value (generic emit-if-set, like ICC).
   - **Close layout (SubResolutionPyramid = true):** partition `w.imgs` by the
     already-computed pyramid index into L0 (index 0), sub-res (index ≥1),
     and associated (non-pyramid). Emit the sub-res IFDs first, capturing each
     IFD's start offset; then build L0's EntryBuilder and add the 330 tag with
     those offsets (`AddLong8` if `w.bigtiff` else `AddLong`) before encoding
     it; then emit the associated IFDs. Set the top-level next-IFD chain to
     L0 → associated[0] → … (sub-res NextIFD = 0). Patch the header first-IFD
     to L0. This "children before parent" emission means the offsets are known
     when L0 is encoded — **no offset patching is needed**, reusing the
     existing `Encode`/`appendBytes` path.
   - The existing non-SubIFD Close path is preserved for every other format.

3. **`cmd/wsitools/convert_tiff.go`** — for the ome-tiff container (both
   `runConvertTIFFTileCopy` and `runConvertTIFFReencode`): set
   `Options.SubResolutionPyramid = true` and `Options.SampleFormat = 1`.
   The associated images that are written (and their order) must match the
   `<Image>` order the OME-XML builder produces (R3); centralize the
   recognized-kind filter + ordering so the IFD writer and the XML builder
   agree.

4. **`cmd/wsitools/ome_imagedesc.go`** — `SyntheticOMEDescription` gains:
   - the R5 preamble comment;
   - an associated-image parameter (ordered list of {name, sizeX, sizeY,
     ifdIndex}) producing the extra `<Image Name="…">` entries from R3.
   The main `<Image>` keeps `Name="Image"` (not a reserved name) and
   `TiffData IFD="0"`.
   For an OME **source** on the tile-copy path, wsitools currently preserves
   the source OME-XML verbatim. That remains correct as long as wsitools emits
   the same top-level IFD order the source OME-XML lists (main pyramid then
   associated). The SubIFD pyramid restructure is orthogonal to the OME-XML
   text (sub-resolutions are never enumerated in OME-XML). We keep verbatim
   for ome→ome and regenerate for non-OME sources; both rely on the R2/R3
   order invariant.

### Data flow

`convert --to ome-tiff` → resolves recognized associated images + their order
→ builds OME-XML (main + associated `<Image>` entries, preamble) → sets
`Options.{SubResolutionPyramid:true, SampleFormat:1}` → AddLevel for L0..Ln
(pyramid) and writeAssociatedImages (in the agreed order) → Close emits
sub-res as SubIFDs of L0, associated as top-level after L0 → file.

### Why children-before-parent (not patch)

The streamwriter writes all tile data during AddLevel/WriteTile and only
emits IFDs at Close, so IFD emission order is free. Emitting sub-res IFDs
before L0 lets L0 embed their real offsets directly, avoiding a
placeholder-then-patch round and any new EntryBuilder offset-exposure API.

## Testing

1. **Unit (streamwriter):** build a 3-level pyramid + one associated image
   with `SubResolutionPyramid=true`, `SampleFormat=1`. Assert: L0's IFD has a
   `SubIFDs` (330) entry with 2 offsets that resolve to valid IFDs; walking
   the top-level next-IFD chain yields exactly L0 then the associated IFD (not
   the sub-res); every IFD carries `SampleFormat=1`. Verify via `tiffinfo`
   (reports "SubIFDs" / sub-directory count) and/or by parsing the chain.

2. **Integration — the bug regression:** `convert --to ome-tiff` on multi-
   level `CMU-1.svs`; `dump-ifds --raw` shows IFD0 with `SubIFDs` (330) listing
   the sub-resolution offsets and only L0 in the top-level chain; `wsitools
   info` reads back the **same number of pyramid levels as the source** (not
   1). `SampleFormat=1` present.

3. **Integration — associated images:** the same output exposes the source's
   label/macro/thumbnail as associated images through opentile (`info` /
   associated listing), with correct names; an unrecognized-kind associated
   image is dropped (logged), not mis-read as a pyramid.

4. **Integration — OME-XML validity:** the L0 ImageDescription ends with
   `OME>` (detection), contains the preamble comment, and the `<Image>` count
   /order matches the top-level IFD count/order.

5. **Regression — other formats untouched:** `convert --to svs|tiff` output is
   byte-for-byte unchanged by this work (SubResolutionPyramid/SampleFormat
   default off for them); existing pixel-equivalence checks still pass.

## Documentation

Extend `docs/tiff-tags.md` with an "OME-TIFF writer" note: pyramid
sub-resolutions via SubIFDs (330), the positional Image↔IFD invariant, the
recognized associated-image names, and `SampleFormat`/preamble. The normative
grounding lives in `docs/references/ome-tiff-spec-notes.md` (added 2026-06-02);
link to it from the writer note. Update `docs/roadmap.md` shipped section when
this lands.

## Out of scope / follow-ups

- Multi-channel / fluorescence OME (we only write single 3-sample RGB images).
- Rich OME-XML metadata passthrough (StructuredAnnotations, original-metadata
  MapAnnotations) — we emit the minimal valid document plus preamble.
- Planar-configuration or striped OME output (Bio-Formats writes planar=2,
  striped; wsitools stays chunky/tiled — a writer-style difference, not a
  conformance requirement).
- SubIFD pyramids for the `tiff` container (generic TIFF readers expect flat
  top-level pyramids; only OME uses SubIFD sub-resolutions).
