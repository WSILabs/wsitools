# `wsitools convert` — design

**Version:** v0.6.0 introduction
**Status:** Draft
**Date:** 2026-05-20

## 1. Goal

Add a new `wsitools convert` command that performs **lossless, bit-exact
tile-copy** from supported TIFF-based WSI sources into a new COG-WSI output
container.

For v0.6:

- One conversion target: `--to cog-wsi`.
- Compressed tile bytes are copied verbatim from source to output. No decode,
  no re-encode, no color conversion, no retiling.
- Associated images (label, macro, thumbnail, overview) are carried over
  verbatim with their original compression.

The COG-WSI output format is defined in the companion spec:
[`2026-05-20-cog-wsi-format.md`](2026-05-20-cog-wsi-format.md).

## 2. Scope

### 2.1 In scope (v0.6)

- New top-level command `wsitools convert`.
- New writer package `internal/cogwsi`.
- Source formats: SVS, Philips-TIFF, OME-TIFF (tiled), BIF, IFE,
  generic-TIFF (the set already supported by `transcode`).
- All tile compressions opentile-go can enumerate (JPEG, JPEG2000, LZW,
  uncompressed, future codecs) are passed through unchanged.
- Unit tests for `internal/cogwsi`; integration tests for `convert` gated by
  `WSI_TOOLS_TESTDIR`.

### 2.2 Out of scope (v0.6)

- Iris output target (deferred to a later release; the `--to` flag is added
  now so that landing is non-breaking).
- Lossy conversion / codec change (deferred; `transcode` keeps that role).
- Subsuming `transcode`. `transcode` remains a separate command in v0.6.
- NDPI source (requires JPEG restart-marker reshuffling; not bit-copy).
- Leica SCN source.
- A `cog-wsi validate` subcommand and external COG validator integration.
- Parallel tile copy (`--jobs`). Tile copy is I/O-bound; defer until a
  profile shows it helps.

## 3. Command surface

### 3.1 Synopsis

```
wsitools convert --to <target> -o <output> [flags] <input>
```

### 3.2 Flags

| Flag                  | Required | Default  | Notes                                  |
|-----------------------|----------|----------|----------------------------------------|
| `--to <target>`       | yes      | —        | v0.6: only `cog-wsi` accepted.         |
| `-o, --output <path>` | yes      | —        | Output file path.                      |
| `-f, --force`         | no       | false    | Overwrite output if it exists.         |
| `--bigtiff <mode>`    | no       | `auto`   | `auto` \| `on` \| `off`. Same semantics as `transcode`. `auto` uses sum of source tile bytes plus metadata, with a 2 GiB margin. |
| `--no-associated`     | no       | false    | Skip label/macro/thumbnail/overview.   |

Global flags inherited from root: `--log-level`, `--log-format`, `--quiet`,
`--verbose`.

Flags explicitly **not** added in v0.6 (reserved for when lossy/transcode is
folded in): `--codec`, `--quality`, `--codec-opt`, `--container`, `--jobs`.

### 3.3 Examples

```sh
# SVS → COG-WSI, lossless.
wsitools convert --to cog-wsi -o slide.cog.tiff slide.svs

# Philips-TIFF → COG-WSI, skipping associated images.
wsitools convert --to cog-wsi --no-associated -o slide.cog.tiff slide.tiff
```

### 3.4 Exit and validation behavior

All validation happens **before** the writer is opened, so no partial output
file is created on a validation failure:

1. Input file exists and opens via `internal/source`.
2. Source format is in the v0.6-supported set; otherwise return
   `ErrUnsupportedFormat`.
3. Source has at least one tiled pyramid IFD; otherwise return
   `ErrUnsupportedSourceLayout` naming the level and its actual layout.
4. Output does not exist, or `--force` is set.
5. `--to` value is recognized.

Errors during tile copy or finalize trigger writer cleanup (spool files
removed, partial output removed) and propagate to the user.

Context cancellation (Ctrl-C, parent context cancel) propagates through the
tile-copy loop and triggers the same cleanup.

## 4. Architecture

### 4.1 Package layout

New package:

```
internal/cogwsi/
    writer.go        // public Writer + Options + Create/AddLevel/AddAssociated/Close
    spool.go         // per-level scratch spool file management
    layout.go        // IFD layout planner: offset math, BigTIFF/classic switch
    ghost.go         // ghost area serialization
    tags.go          // WSI extension tag emission
    writer_test.go
```

Touched packages:

```
cmd/wsitools/
    convert.go       // new cobra command
    main.go          // register convert command (parallel to transcode)
```

No changes required to `internal/wsiwriter` (it continues to back
`transcode`). No changes to `internal/source`, `internal/decoder`, or
`internal/pipeline` for v0.6.

If, during implementation, sufficient TIFF byte-emission code duplicates
between `wsiwriter` and `cogwsi` to motivate a shared package, hoist into
`internal/tiff`. Do **not** pre-extract; wait for the duplication to show up
in practice.

### 4.2 Writer API (internal/cogwsi)

```go
package cogwsi

type Mode int
const (
    BigTIFFAuto Mode = iota
    BigTIFFOn
    BigTIFFOff
)

type Metadata struct {
    MPPX, MPPY          float64
    Magnification       float64
    Make, Model         string
    Software            string
    AcquisitionDateTime time.Time
    SourceFormat        string
    SourceImageDesc     string // optional; copied into ImageDescription as provenance
}

type Options struct {
    BigTIFF      Mode
    ToolsVersion string
    Metadata     Metadata
}

type Writer struct { /* unexported */ }

func Create(path string, opts Options) (*Writer, error)

type LevelSpec struct {
    ImageWidth, ImageHeight uint32
    TileWidth, TileHeight   uint32
    Compression             uint16  // TIFF compression tag, preserved from source
    Photometric             uint16
    BitsPerSample           []uint16
    SamplesPerPixel         uint16
    JPEGTables              []byte  // optional, abbreviated-JPEG mode
    ExtraTags               []ExtraTag
}

type LevelHandle struct { /* unexported */ }

func (*Writer) AddLevel(LevelSpec) (*LevelHandle, error)
func (*LevelHandle) WriteTile(tx, ty uint32, compressed []byte) error

type AssociatedSpec struct {
    Kind         string // "label" | "macro" | "thumbnail" | "overview"
    Width, Height uint32
    Compression   uint16
    Photometric   uint16
    Bytes         []byte // verbatim compressed payload from source
    Tiled         bool   // whether Bytes is a single tile or a strip
}

func (*Writer) AddAssociated(AssociatedSpec) error

// Close finalizes the file: serializes the ghost area, all IFDs, and
// external tag arrays at the file head with patched-up tile offsets;
// streams spool files into the output in reverse level order (smallest
// level first); appends associated-image data; removes spool files.
//
// On error, removes spool files and the partial output.
func (*Writer) Close() error
```

`AddLevel` is called in **source order** (full-res first), matching the IFD
order in the output. The writer internally takes responsibility for emitting
tile *data* in reverse order at `Close` time.

### 4.3 Staging strategy (spool files)

The COG-WSI layout requires `TileOffsets` arrays at the file head, but their
contents (the actual offsets) depend on tile-data placement, which can only
be finalized once all tile bytes have been written. To avoid an in-place
rewrite of multi-GB output files, the writer uses **per-level spool files**:

- `Create` opens the destination file with an unwritten header region (no
  bytes written yet — `os.Create` followed by no writes), and a sibling
  spool directory next to the output path (`<output>.spool/`).
- Each `AddLevel` opens a new spool file in that directory named by level
  index (`L0`, `L1`, ...). Calls to `WriteTile` append the raw bytes and
  record `(tx, ty, len)` index entries in an in-memory per-level table.
- `AddAssociated` appends to a single shared associated-image spool
  (`A`) and records an in-memory associated-IFD table.
- `Close`:
  1. Compute the final file layout: ghost area size, classic vs BigTIFF,
     positions of all IFDs and external tag arrays, total head-block size.
  2. Compute tile offsets: smallest-overview tile data starts at
     `head_block_end`, aligned to 16 bytes; subsequent tiles follow with
     16-byte alignment; level N+1 starts right after level N's last tile.
     Associated-image data follows the largest pyramid level.
  3. Write the TIFF header + ghost area + IFDs + external tag arrays at
     the file head, with all tile offsets resolved.
  4. Stream spool files into the output: smallest level first, then
     decreasing-order overviews, then full-res. Then the associated-image
     spool.
  5. `os.Remove` each spool file; rmdir the spool directory.
  6. `fsync` and close the output.
- On any error after `Create`, `Close` (or a deferred cleanup) removes the
  partial output and the spool directory.

**Disk overhead:** one extra write + one extra read per tile byte. On modern
disks the streaming throughput dominates; expected overhead is small
relative to source read time. A future optimization could ask opentile-go for
random-access level iteration in reverse order and write straight through,
but that is not assumed for v0.6.

**Spool location:** sibling to the output (`<output>.spool/`), not `$TMPDIR`,
to keep the spool on the same filesystem as the output (so the final move-in
is a sequential copy on the same volume).

### 4.4 Command flow (cmd/wsitools/convert.go)

```
1. Parse and validate flags.
2. Open input via internal/source.Open.
3. Pre-flight validation:
   - Source format in v0.6-supported set.
   - Every pyramid level is tiled (else ErrUnsupportedSourceLayout).
   - Output does not exist (or --force).
4. Build cogwsi.Options from source Metadata + flags.
5. cogwsi.Writer.Create(output, opts).
6. For each pyramid level in source order:
   a. Build cogwsi.LevelSpec from source Level (compression, geometry,
      JPEGTables verbatim).
   b. handle, _ := writer.AddLevel(spec)
   c. For each (tx, ty) in row-major order:
      n, _ := level.TileInto(tx, ty, buf)   // raw compressed payload
      handle.WriteTile(tx, ty, buf[:n])
7. If !--no-associated:
   for each src.Associated():
       writer.AddAssociated(spec)
8. writer.Close()
9. Log size + elapsed time. Done.
```

`Level.TileInto` is the existing opentile-go method already used by
`transcode.transcodeLevel`. It writes the raw compressed tile payload into a
caller-provided buffer and returns the number of bytes — which is exactly
what `convert` needs. No opentile-go API addition is required.

### 4.5 BigTIFF prediction

For `--bigtiff auto`:

```
predicted := sum(level.TileByteCountsTotal) over all levels
           + sum(associated.SourceBytes)
           + 64 KiB metadata overhead
useBigTIFF := predicted + 2 GiB margin > (4 GiB - 2 GiB)  // i.e. predicted > 2 GiB
```

This is more accurate than `transcode`'s 1-byte/pixel heuristic because, on
the lossless path, the exact compressed sizes are known up front from
opentile-go's `TileByteCounts`.

## 5. Tile-copy invariants

The writer makes the following guarantees, enforced by integration tests
(see §6):

- For every source tile, `output_tile_bytes == source_tile_bytes` (bit-exact).
- Output `Compression` tag equals source `Compression` tag, per IFD.
- Output `JPEGTables` equals source `JPEGTables` when source has one;
  absent otherwise.
- Output `PhotometricInterpretation` equals source.
- Output `TileWidth` and `TileLength` equal source per level.
- Output pyramid level count equals source.
- Output per-level `ImageWidth` and `ImageLength` equal source.

The writer MUST refuse to write a level whose source layout is unknown
(non-tiled, planar `PlanarConfiguration=2`, or a compression opentile-go
cannot enumerate tiles for). Refusal is reported by `convert.go` as
`ErrUnsupportedSourceLayout`.

## 6. Testing

### 6.1 Unit tests (`internal/cogwsi/writer_test.go`)

- Synthetic 2-level pyramid with mocked tile bytes; verify:
  - Ghost area structure and `COG_WSI_VERSION` line.
  - IFD count and order (pyramid IFDs first, associated last).
  - `TileOffsets` arrays monotonically increasing within a level.
  - Smallest-level tile data appears before full-res tile data in the file.
  - Tile offsets aligned to 16 bytes.
  - BigTIFF auto-promotes at the threshold; honors explicit `on`/`off`.
  - `JPEGTables` round-trip: bytes-equal IFD entry.
  - `NewSubfileType` 0 for IFD 0, 1 for overviews and associated images.
  - `WSIImageType` tag value per IFD type.
  - Cleanup: error mid-write removes spool dir and partial output.

### 6.2 Integration tests (gated by `WSI_TOOLS_TESTDIR`)

For each supported source format with a sample in `sample_files/`:

- Run `convert --to cog-wsi` end-to-end against the sample.
- Re-open output via opentile-go's generic-TIFF reader; assert level count,
  per-level dimensions, MPP, magnification all equal source.
- For each pyramid level, for each tile, assert `bytes.Equal` between
  source raw tile and output raw tile.
- Assert all IFD offsets fall within the first 64 KiB of the file.
- Assert smallest-level tile data offset < full-res tile data offset.
- Re-run with `--no-associated`; assert output has no associated IFDs.

### 6.3 Make targets

`make test` continues to run with `-race -count=1`; integration tests run
under the same target when `WSI_TOOLS_TESTDIR` is set, matching the existing
project convention.

## 7. Release & rollout

- Lands in **v0.6.0**.
- `transcode` is unchanged and not deprecated in v0.6. Release notes flag
  that `convert` will eventually absorb it (lossy paths fold in later).
- `CHANGELOG.md` v0.6.0 section: new `convert` command + new COG-WSI format
  spec; links to both spec documents.
- README `Available Commands` table gains a `convert` row.
- Purely additive; no migration concerns.

## 8. Open follow-ups (post-v0.6)

- `--to iris` and `internal/iris` writer, parallel to `internal/cogwsi`.
- Lossy `convert` (with `--codec`, `--quality`, `--codec-opt`), folding the
  current `transcode` machinery in. At that point, `transcode` is
  deprecated, then removed.
- NDPI source (JPEG restart-marker reshuffling — near-lossless, not bit-copy).
- Leica SCN source.
- Optional `wsitools convert --validate` post-write check (or a separate
  `wsitools cog-wsi validate` subcommand) that runs the conformance checks
  from §6 against an existing file.
- Parallel tile copy (`--jobs`) if profiling justifies it.
- Optional in-flight tile checksums recorded in the ghost area.
