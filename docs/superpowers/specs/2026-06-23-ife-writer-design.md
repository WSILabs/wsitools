# IFE (Iris File Extension) writer — design

**Date:** 2026-06-23
**Status:** Approved design, ready for implementation plan.
**Parent:** format-debt **A5** (read-only formats with no format-preserving writer)
+ the B2 discussion (IRIS decode ruled out; a *writer* needs no proprietary codec).

## Goal

Add `convert --to ife`: write conformant **IFE v1.0** files with **JPEG or AVIF**
pyramid tiles — never the IRIS-proprietary codec. Full metadata fidelity (MPP,
magnification, ICC profile, associated images, vendor attributes), with a
verbatim tile-copy fast path for already-256px JPEG/AVIF sources. The writer is
wsitools-side and pure Go; verification uses two oracles — opentile-go's existing
IFE reader (`formats/ife`) and the **official IrisDigitalPathology Iris-Codec
validator** (`Codec.validate_slide_path`, the gold-standard conformance gate). No
opentile-go changes are required.

**Layering:** opentile-go owns the IFE *reader*; this writer is ours, like
`bifwriter`/`cogwsiwriter`. We verify against opentile's reader, not by editing it.

## Why a writer is tractable (where a decoder was not)

IFE tiles carry one of three encodings: `1=IRIS` (proprietary, undecodable
without the Iris C++ codec), `2=JPEG`, `3=AVIF`. wsitools already encodes JPEG and
AVIF, so the writer emits `encoding=2` (or 3) and **never touches IRIS**. No new
dependency, no fixture-generation problem, and a complete read-back oracle exists
(opentile reads IFE today, plus the `cervix_2x_jpeg.iris` fixture). This is the
inverse of B2's decoder, which needed the proprietary codec.

## The IFE v1.0 container (writer's view)

All little-endian. Every block opens with a `DATA_BLOCK` prefix: `u64 validation`
(== the block's own byte offset) + `u16 recovery` (a block-type magic). Spec:
`sample_files/ife/ife-format-spec-for-opentile-go.md`; metadata sub-block layouts
reversed from opentile-go's `formats/ife/metadata.go` (the canonical parser).

| Block | Size | Key fields the writer emits |
|---|---|---|
| `FILE_HEADER` @0 | 38 B | magic `0x49726973`, `file_size`, ext 1.0, `tile_table_offset`, `metadata_offset` |
| `TILE_TABLE` | 44 B | `encoding` (2=JPEG/3=AVIF), `format` (2=R8G8B8), `cipher_offset`=NULL, `tile_offsets_offset`, `layer_extents_offset`, `x_extent`/`y_extent` (native dims) |
| `LAYER_EXTENTS` | 16 B hdr + 12 B×layers | per layer: `x_tiles`, `y_tiles`, `scale` (f32). **Stored COARSEST-FIRST**; native layer last. `scale` set so reader's `downsample = max_scale/scale` yields the octave ratios |
| `TILE_OFFSETS` | 16 B hdr + 8 B×tiles | per tile: `u40 offset` + `u24 size`; `NULL_TILE` (0xFFFFFFFFFF) for sparse. Iteration = layers coarsest-first, row-major within layer |
| tile blobs | — | the compressed 256×256 JPEG/AVIF tiles, written in arrival order anywhere in the file |
| `METADATA` | 56 B | `recovery`=0x5504, codec version, NULL-able pointers to ATTRIBUTES/IMAGE_ARRAY/ICC_PROFILE/ANNOTATIONS, `f32 microns_per_pixel`, `f32 magnification` |
| `ICC_PROFILE` | hdr + bytes | `recovery`=0x550C; the slide's ICC blob (exact field layout pinned from the parser at plan time) |
| `IMAGE_ARRAY` | 16 B hdr + 20 B×images | per image: `bytes_offset`, `width`, `height`, `encoding` (1=PNG/2=JPEG/3=AVIF), `format`, `orientation`(=0) |
| `IMAGE_BYTES` | 16 B hdr + title + blob | `recovery`=0x550B, `title_size`, `image_size`, UTF-8 title (the associated type), then the compressed image |
| `ATTRIBUTES` (+`SIZES`+`BYTES`) | 29 B / 16+6×N / hdr+bytes | free-text k/v: ATTRIBUTES (format=1 FreeText) → ATTRIBUTES_SIZES (recovery 0x5508, `u16 KeySize`+`u32 ValueSize` per entry) → ATTRIBUTES_BYTES (recovery 0x5509, concatenated key then value UTF-8) |

**Tiles are 256×256 everywhere** (IFE v1.0 hard-codes this). Edge tiles are
full-framed 256×256 with partial content — which matches how TIFF-family sources
already pad edge tiles, so verbatim copy of a 256px source needs no re-framing.

## Architecture: direct-write + header backpatch (no spool)

cogwsiwriter spools to a temp file because COG-WSI must reorder tiles
(overview-first) and front-load IFDs. **IFE has no ordering constraint** — its
`TILE_OFFSETS` is an indirection table, so tile blobs may sit anywhere in any
order. The writer therefore writes a placeholder `FILE_HEADER`, streams tile blobs
straight to the output as they arrive (recording each `(layer,col,row)→(offset,
size)`), then emits the ordered tables + metadata at the end and backpatches the
header's forward pointers and `file_size`. This is the `bifwriter` backpatch
pattern, simpler than a spool.

### Components

| Unit | Responsibility | Depends on |
|---|---|---|
| **`internal/ife`** (new, pure Go) | `Writer`: placeholder header; `AddLevel`/`WriteTile(layer,col,row,blob)` recording offsets; `Finalize` emits TILE_TABLE/LAYER_EXTENTS/TILE_OFFSETS + METADATA + sub-blocks, backpatches header + file_size. Owns all byte layout. Carries the LE u40/u24 helpers. | `encoding/binary`, `io` |
| **`ifeSink`** (cmd/wsitools) | Implements `retile.TileSink`; routes the engine's `WriteTile(level,col,row,bytes)` into the `ife.Writer` (the re-encode path) | `internal/retile`, `internal/ife` |
| **verbatim-copy path** (cmd/wsitools) | For eligible sources: pull verbatim compressed tiles level-by-level (opentile tile access, as cog-wsi does) → `ife.Writer`, no decode | opentile source layer, `internal/ife` |
| **`convert --to ife` driver** (cmd/wsitools) | eligibility → dispatch (verbatim vs engine); map source metadata → writer; associated/ICC/attribute assembly; `containerCapabilities("ife")` entry | the above |

This mirrors the existing `streamwriter` + `streamwriterSink` + crop-verbatim
split: one binary core, two feed paths.

## Metadata mapping (full fidelity)

| IFE target | Source | Policy |
|---|---|---|
| `microns_per_pixel` / `magnification` | `source.Metadata().MPP.X` / `.Magnification` | `f32`; 0.0 when unknown |
| `codec_major/minor/build` | wsitools version | stamps wsitools as encoder |
| `ICC_PROFILE` | `source.ICCProfile()` | write the blob; METADATA pointer NULL when the source has no ICC |
| `IMAGE_ARRAY`/`IMAGE_BYTES` | `source.AssociatedImages()` | one entry per label/macro/thumbnail/overview; **title = the associated type string** (round-trips via opentile's `normaliseAssociatedType`; exact title strings pinned at plan time) |
| `ATTRIBUTES` | source vendor Properties + wsitools provenance | free-text k/v: pass through opentile-exposed Properties (e.g. `aperio.*`/`tiff.*`); add `source-format`, `wsitools-version` |

**Associated-image encoding policy:** verbatim-copy the blob as `encoding=2`
(JPEG) when the source associated image is JPEG (lossless byte-copy via opentile's
associated-source access, identical to cog-wsi/svs). Otherwise **decode→PNG**
(`encoding=1`, lossless, via the existing `internal/codec/png`) so a LZW/text
**label is never re-compressed lossily** — consistent with the DICOM writer's
labels-stay-lossless stance. (AVIF associated sources copy verbatim as
`encoding=3`.)

## Codec scope

- **Pyramid tiles:** JPEG (default) and AVIF, selected via the existing `--codec`
  (both already plumbed through the retile engine). `containerCapabilities("ife")`
  = conformant {jpeg, avif}; everything else rejected with a redirect.
- **Associated images:** PNG / JPEG / AVIF per the policy above.
- IRIS is never written.

**Known limitation (PNG associated read-back).** Non-JPEG/AVIF associated images
(e.g. an Aperio LZW label) are stored as lossless **PNG** (`encoding=1`). This is
spec-conformant — the official Iris-Codec validator accepts it — but **opentile-go
has no PNG-associated decoder**: it maps `IMAGE_ENTRY.encoding==1` to
`CompressionUnknown`, so on our own output `info` shows the label codec as
`unknown`, `extract --type label` fails, and an `ife→ife` verbatim re-convert
drops the PNG label. The file is correct; the gap is in opentile's reader. Per the
opentile-go boundary, this is filed upstream as opentile-go#74 (PNG-associated read support) for
upstream to implement; the lossless-PNG label is kept deliberately (barcodes stay
crisp). JPEG/AVIF associated images round-trip fine.

## Data flow

`convert --to ife`:
1. Open source; read metadata, ICC, associated images, vendor properties.
2. **Eligibility for verbatim-copy:** pure `--to ife` (no `--factor`/`--target-mag`
   /`--rect`, no `--codec` that differs from the source codec), source pyramid is
   256px-tiled, source tile codec ∈ {jpeg, avif}. Any transform or mismatch ⇒
   re-encode.
3. **Verbatim path:** for each source level (native-first; the writer stores
   coarsest-first), pull verbatim tiles → `ife.Writer.WriteTile`.
   **Re-encode path:** run the retile engine to 256px tiles with the selected
   codec → `ifeSink` → `ife.Writer.WriteTile`.
4. Assemble associated images + ICC + attributes into the writer.
5. `Finalize()` → tables + metadata + backpatch. Write to a temp path; atomic
   rename on success so a failure leaves no partial `.iris`.

`--factor`/`--target-mag`/`--rect` compose as for every other `--to` target: they
force the engine path and scale the pyramid before tiling (the engine already does
this; no IFE-specific transform code).

## Error handling

- **Capability gate:** non-{jpeg,avif} pyramid codec ⇒ hard error + redirect, via
  `containerCapabilities("ife")` (consistent with the Phase-2 table).
- **Format caps:** tile blob > 16 MB (24-bit `size`) or file > 1 TB (40-bit
  `offset`) ⇒ clear error. Neither is reachable in practice (a 256×256 tile is
  ~200 KB uncompressed) but both are guarded so a violation fails loud, not
  silently truncated.
- **No partial output:** temp file + atomic rename; `Writer` aborts/removes the
  temp on any error.
- Source with no readable pyramid ⇒ error before any output is created.

## Testing — two oracles: opentile-go's reader + the official Iris-Codec validator

The strong correctness gate is **IrisDigitalPathology's own validator** — the IFE
equivalent of `dciodvfy`. `Iris-Codec` (MIT — "Iris Codec Community License" is
verbatim MIT; PyPI `pip install Iris-Codec`, also conda-forge) exposes
`Codec.validate_slide_path(path) -> result` with `result.success()` /
`result.message()`, running `validate_file_structure`'s full `validate_full()`
chain over every data block against the published IFE spec. We gate on it
**in addition** to the opentile round-trip.

This validator is **stricter than opentile's reader**, which ignores the
`recovery` magic on the tile-path blocks. So the writer must set the correct
`RECOVERY` tags from Iris-Headers `IrisCodecExtension.hpp`: FILE_HEADER `0x5501`,
TILE_TABLE `0x5502`, LAYER_EXTENTS `0x5506`, TILE_OFFSETS `0x5507`, plus the
metadata sub-block magics METADATA `0x5504`, ATTRIBUTES `0x5505`,
ATTRIBUTES_SIZES `0x5508`, ATTRIBUTES_BYTES `0x5509`, IMAGE_ARRAY `0x550A`,
IMAGE_BYTES `0x550B`, ICC_PROFILE `0x550C` — **not** the `0` opentile would
tolerate.

- **Iris-Codec validation (gold standard):** every emitted `.iris` in the test
  suite is passed through `Codec.validate_slide_path` (a Python helper invoked
  from the integration tests / a `make ife-validate` target); a non-success
  result fails the build. CI installs Iris-Codec (pip/conda) and runs it on the
  fixtures, mirroring the `make dicom-validate` + dciodvfy gate (format-debt D5).
- **opentile round-trip (re-encode):** `convert --to ife` on
  `CMU-1-Small-Region.svs` → re-open via opentile-go → assert format=ife, level
  count + per-level dims, MPP/magnification, total tile count, associated images
  (types via title + byte-equality), ICC bytes, attribute k/v. Pixel parity via
  `hash --mode pixel` (decode both sides) — file SHA is not byte-stable (see the
  pipeline-nondeterminism note); use the pixel oracle.
- **Verbatim-copy:** a 256px-JPEG source → IFE → assert pyramid tile bytes are
  **byte-identical** to the source's compressed tiles (the lossless promise), and
  that opentile re-opens it with the same dims/metadata.
- **Synthetic unit tests** in `internal/ife`: hand-build a 1-layer 2×2 file and a
  multi-layer file, parse back via opentile's reader; assert every block's
  `validation == offset`, the coarsest-first ordering, and `NULL_TILE` handling.
- **Capability gate:** `--to ife --codec jpegxl` (etc.) errors with the redirect;
  jpeg/avif pass.
- Full `-race` on `internal/ife` + the cmd/wsitools paths.

## Boundaries / deferred

**In v1:** `convert --to ife` (jpeg/avif pyramid), verbatim-copy fast path, full
metadata (MPP/mag/ICC/associated/attributes), `--factor`/`--rect` via the engine,
capability-table entry, opentile-reader round-trip + verbatim byte-identity tests,
**and the official Iris-Codec validator gate (dev + CI)**.

**Deferred:** the `ANNOTATIONS` block (no source produces them); the `cipher`/
encryption block (always NULL); IFE v2.0 fields; reading IRIS-encoded IFE (B2 —
explicitly ruled out). (Cross-validation against the reference implementation is
now **in v1** via the Iris-Codec validator, not deferred.)

## Build phasing (for the implementation plan)

A natural slice order that keeps each step round-trippable:
1. `internal/ife.Writer` core + synthetic unit tests (bare pyramid: header, tables,
   tile blobs, 56-byte METADATA with NULL sub-blocks + MPP/mag) — round-trips
   through opentile.
2. `ifeSink` + `convert --to ife` engine path + capability gate — re-encode any
   source to IFE.
3. Metadata sub-blocks: ICC_PROFILE, then IMAGE_ARRAY/IMAGE_BYTES (associated,
   with the verbatim-JPEG / decode-PNG policy), then ATTRIBUTES.
4. Verbatim tile-copy fast path + byte-identity test.
