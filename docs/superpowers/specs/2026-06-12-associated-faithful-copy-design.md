# Faithful associated-image copy across TIFF-family writers — Design

**Status:** approved design (root cause confirmed, approach chosen), pre-plan.
**Date:** 2026-06-12
**Issue:** wsitools#1. **Upstream dependency:** opentile-go **v0.39.0** (`Slide.AssociatedSourceOf`, GH opentile-go#22, closed).

## Goal

Fix wsitools#1: `convert --to {cog-wsi, svs, tiff, ome-tiff}` (and the `--factor`
variants) emit **corrupt** associated images — LZW **labels** garble/truncate and
abbreviated-JPEG **thumbnails** are undecodable. Every TIFF-family writer copies
opentile's `AssociatedImage.Bytes()` verbatim into a **single standalone strip**,
but `Bytes()` is abbreviated/context-dependent: LZW is `Predictor=2`-differenced
with no predictor metadata, and JPEG strips reference the source IFD's
`JPEGTables (347)`. Written standalone without those tags → corrupt.

Re-emit associated images **byte-faithfully** using opentile-go v0.39.0
`Slide.AssociatedSourceOf(a) (AssociatedSource, bool)`: write the source strips
**verbatim** plus the exact tags it reports (`Compression`, `Predictor`/317,
`JPEGTables`/347, `RowsPerStrip`/278, `SamplesPerPixel`/277, `Photometric`/262).
No re-encode when a faithful source exists.

## Verified facts (this session)

- **Not the LZW encoder, not a regression.** `encodeLZW` round-trips a 537 KB label
  byte-perfect via an independent decoder; `a.Bytes()` is identically abbreviated in
  opentile-go v0.37.0 and v0.38.1. The earlier *truncation* symptom was opentile's
  pre-v0.38.0 `Bytes()` re-encode (fixed upstream).
- **Root cause proven:** reversing predictor-2 on the corrupt cog-wsi label recovers
  the source byte-for-byte; the thumbnail JPEG strip is undecodable standalone
  (missing JPEGTables) but decodes inside the source context.
- **opentile-go v0.39.0** `AssociatedSource{Strips [][]byte, Compression, Predictor int,
  JPEGTables []byte, RowsPerStrip int, Samples int, Photometric int}`;
  `AssociatedSourceOf(a) (AssociatedSource, bool)`. `ok=false` for non-TIFF /
  not-opted-in / synthesized / tiled associated (NDPI synth label, DICOM, OME planar).
  Implemented by **svs, generictiff, ometiff, philipstiff, bif, ndpi** readers.
  `TestAssociatedSourceRoundtrip` PASSES on CMU/generic-tiff/cog-wsi/bif/philips/ndpi.
- **Confirmed-corrupt committed fixtures:** `sample_files/cog-wsi/{CMU-1,
  CMU-1-Small-Region, JP2K-33003-1, scan_617, scan_620}_cog-wsi.tiff`. The
  `.stripped.tiff` fixtures are fine (they carry 317 + multi-strip + JPEGTables).
- **Current writers:** `cogwsiwriter.AssociatedSpec` {Type, Width, Height, Compression,
  Photometric, BitsPerSample, SamplesPerPixel, **Bytes** (single strip), Tiled} →
  `populateAssocIFD` emits a single StripOffsets/StripByteCounts, RowsPerStrip=Height,
  **no 317, no 347**. The cogwsiwriter **level** path already carries `JPEGTables`
  (precedent). `streamwriter.StrippedSpec` similarly single-payload, no 317/347.
- **Safe paths (unchanged):** `associated replace` (re-encodes, emits 317/JPEGTables),
  `associated remove` (structural), `convert --to dicom` (decodes or encapsulates).

## Locked decisions

1. **Approach B — faithful verbatim.** When `AssociatedSourceOf` returns `ok=true`,
   write `Strips` verbatim + the reported tags. **No re-encode.** Codec-agnostic: the
   consumer never branches on compression; it emits 317 iff `Predictor>1` and 347 iff
   `JPEGTables!=nil`.
2. **`ok=false` fallback = decode → re-encode** (self-contained), so synthesized/tiled
   associated images (NDPI label, etc.) still emit correctly rather than corrupt or
   skip. Re-encode reuses the existing correct encoders (LZW-with-predictor strips /
   JPEG), which already emit proper tags.
3. **Multi-strip associated support** is required: source strips are independent LZW
   streams / abbreviated JPEG scans and must NOT be concatenated (concatenation was the
   original bug). Both writers gain per-strip StripOffsets/StripByteCounts.
4. **Spike/test-first:** a faithful round-trip (write `AssociatedSource` → standalone
   TIFF → opentile decode == source decode) is a new writer path — prove it before
   wiring all consumers.
5. **Regenerate all 5 corrupt cog-wsi fixtures** and verify; this is part of the work.

## Architecture

### Component 0 — source layer exposes the faithful source form
`internal/source`: add `AssociatedImage.Source() (opentile.AssociatedSource, bool)`
(mirrors the `Decode` passthrough added for the DICOM work). `opentileAssociated`
implements it by calling its parent slide's `AssociatedSourceOf`. (Carries the
opentile `Slide` ref into `opentileAssociated`, or routes through `opentileSource`.)
Leaks the opentile `AssociatedSource` type through the interface — acceptable, same
precedent as `Decode(decoder.DecodeOptions)`.

### Component 1 — writer support for faithful multi-strip associated images
- `cogwsiwriter.AssociatedSpec`: add `Strips [][]byte`, `Predictor int`,
  `JPEGTables []byte`, `RowsPerStrip int` (keep `Bytes` for the single-strip fallback,
  or treat `Bytes` as `Strips` of length 1). `populateAssocIFD` emits array
  StripOffsets/StripByteCounts over all strips, `RowsPerStrip` (278), `Predictor` (317)
  iff `>1`, `JPEGTables` (347) iff non-nil, `SamplesPerPixel` (277), `Photometric` (262).
  The Close/layout path spools all strips contiguously and records per-strip offsets.
- `streamwriter.StrippedSpec` + `buildStrippedEntries`: same additions (multi-strip,
  317, 347). Mirror the level path's JPEGTables handling.

### Component 2 — a shared associated-spec builder (faithful-or-fallback)
A helper (e.g. in `cmd/wsitools`) that, given the source + an `AssociatedImage`,
returns a writer spec:
- `src, ok := a.Source()`; if `ok` → build the faithful spec from `AssociatedSource`
  (Strips/Compression/Predictor/JPEGTables/RowsPerStrip/Samples/Photometric).
- else → `a.Decode(RGB)` → re-encode (self-contained) into a spec.
Two thin adapters map the result to `cogwsiwriter.AssociatedSpec` and
`streamwriter.StrippedSpec`.

### Component 3 — rewire consumers
`writeCOGWSI` (associated_rebuild.go), `writeAssociatedImages` (convert_tiff.go,
covers svs/tiff/ome-tiff tile-copy + ome rebuild), `writeOneAssociated`
(downsample.go, covers `--factor`), and the cog-wsi `--factor` assoc loop
(convert_factor.go) all route through the Component-2 builder instead of `a.Bytes()`.

### Component 4 — regenerate + verify fixtures
Regenerate the 5 corrupt `cog-wsi/*_cog-wsi.tiff` from their sources with the fixed
binary; re-run the associated-decode audit (all green). (Shared pool — coordinated
with opentile-go, which flips its known-corrupt cog-wsi-label skip to an assertion.)

## Error handling

| Condition | Behavior |
|---|---|
| `AssociatedSourceOf` ok=true | write strips verbatim + tags (no re-encode) |
| ok=false (synth/tiled/non-TIFF) | decode → re-encode self-contained |
| decode fallback fails | skip with warning (existing behavior), pyramid completes |
| associated codec already self-contained JPEG (overview) | ok=true path handles uniformly (Predictor=1, JPEGTables nil) |

## Testing

1. **Writer round-trip unit** (cogwsiwriter + streamwriter): synth a faithful
   multi-strip LZW+Predictor2 associated and a JPEG+JPEGTables associated; write; open
   via opentile; decode == input pixels. (De-risks the new multi-strip/317/347 path.)
2. **CLI faithful copy** (cmd/wsitools, CMU-1-Small-Region.svs): `convert --to
   {cog-wsi,svs,tiff,ome-tiff}` → open output → every associated (label LZW, thumbnail
   JPEG, overview JPEG) decode byte-identical to the source decode. (The audit harness
   from the investigation.)
3. **`--factor` path**: same faithful copy for `convert --factor 2 --to cog-wsi`.
4. **Regression:** pyramid tiles byte-identical (hash --mode pixel); `associated
   replace`/`remove` and DICOM unchanged.
5. **Fixture audit:** the 5 regenerated cog-wsi fixtures — every associated decodes ==
   source.
6. **opentile-go version-bump verification** (CLAUDE.md): v0.39.0 `AssociatedSource`
   tests PASS (done: `TestAssociatedSourceRoundtrip`).

## Success criteria

- Fresh `convert --to {cog-wsi, svs, tiff, ome-tiff}` and `--factor` from an Aperio SVS
  produce associated images (label + thumbnail + overview) that decode **byte-identical
  to the source** — no re-encode on the ok=true path.
- The 5 corrupt `cog-wsi/*_cog-wsi.tiff` fixtures are regenerated and pass the audit.
- Safe paths and pyramid output unchanged.

## Out of scope

- OME-TIFF `associated replace` with forced LZW (opentile OME reader limitation — JPEG
  default + warning, unchanged).
- Changing opentile `a.Bytes()` semantics (orthogonal; `AssociatedSourceOf` is the path).
- The exact codec of the `ok=false` re-encode fallback beyond "self-contained + correct".

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `go.mod` / `go.sum` | modify | opentile-go v0.38.1 → v0.39.0 |
| `internal/source/source.go`, `opentile.go` | modify | `AssociatedImage.Source() (opentile.AssociatedSource, bool)` passthrough |
| `internal/tiff/cogwsiwriter/writer.go` (+ `layout.go`) | modify | `AssociatedSpec` Strips/Predictor/JPEGTables/RowsPerStrip; `populateAssocIFD` 317/347/multi-strip; spool all strips |
| `internal/tiff/streamwriter/stripped.go` | modify | `StrippedSpec` + `buildStrippedEntries` multi-strip + 317/347 |
| `cmd/wsitools/associated_rebuild.go` | modify | `writeCOGWSI` → faithful builder |
| `cmd/wsitools/convert_tiff.go` | modify | `writeAssociatedImages` → faithful builder |
| `cmd/wsitools/downsample.go`, `convert_factor.go` | modify | `writeOneAssociated` / cog-wsi `--factor` → faithful builder |
| `cmd/wsitools/associated_faithful.go` (new) | new | Component-2 builder (Source-or-decode) + adapters |
| `internal/tiff/{cogwsiwriter,streamwriter}/*_test.go` | new/modify | round-trip units |
| `cmd/wsitools/convert_*_test.go` | modify | CLI faithful-copy + audit |
| `sample_files/cog-wsi/{CMU-1,CMU-1-Small-Region,JP2K-33003-1,scan_617,scan_620}_cog-wsi.tiff` | regenerate | fixed fixtures |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md` | modify | document |
