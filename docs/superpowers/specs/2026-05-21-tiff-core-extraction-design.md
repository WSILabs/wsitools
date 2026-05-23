# TIFF Core Extraction — Design

**Version:** v0.7.0 refactor
**Status:** Draft
**Date:** 2026-05-21

## 1. Goal

Extract a shared TIFF byte-emission core from `internal/wsiwriter` and
`internal/cogwsi` into a new `internal/tiff` package. Replace both
existing writer packages with new ones built on top of the core:

- `internal/tiff/streamwriter` — replaces `internal/wsiwriter` (streaming
  orchestration; backs `transcode` + `downsample`).
- `internal/tiff/cogwsiwriter` — replaces `internal/cogwsi` (spool-and-
  finalize orchestration; backs `convert`).

The refactor produces no user-visible behavior changes. v0.6.0 output
from `transcode`, `downsample`, and `convert` is byte-identical before
and after the refactor (golden-master verified).

## 2. Scope

### 2.1 In scope (v0.7.0)

- New `internal/tiff` package containing TIFF type constants, tag IDs
  (standard + WSI private), `WSIImageType` validation, header writer,
  IFD entry builder, JPEGTables helpers, BigTIFF auto-promote logic,
  and in-place patch helpers.
- New `internal/tiff/streamwriter` package replacing
  `internal/wsiwriter`.
- New `internal/tiff/cogwsiwriter` package replacing `internal/cogwsi`.
- `cmd/wsitools/transcode.go`, `cmd/wsitools/downsample.go`,
  `cmd/wsitools/convert.go` updated to import the new packages.
- SVS-shape tag emission moves from `internal/wsiwriter/svs.go` to a
  caller-side helper near `transcode.go`.
- Golden-master hash fixtures for transcode + downsample outputs
  captured before landing 3, verified after.

### 2.2 Out of scope (v0.7.0)

- No new commands.
- No new output formats (no `--to svs` flag, no Iris writer).
- No new flags on existing commands.
- No public API changes to flag surface, exit codes, or log format.
- No behavior changes — pre- and post-refactor binary outputs are
  byte-identical for the same inputs and flags.
- Iris writer support (Iris isn't TIFF and stays out of this tree).

## 3. Architecture

### 3.1 Package layout

```
internal/tiff/                       — byte-emission primitives (no I/O orchestration)
internal/tiff/streamwriter/          — streaming-write TIFF writer
internal/tiff/cogwsiwriter/          — spool-and-finalize COG-WSI writer
```

Imports read naturally:

```go
import (
    "github.com/wsilabs/wsitools/internal/tiff"
    "github.com/wsilabs/wsitools/internal/tiff/streamwriter"
    "github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

w, _ := streamwriter.Create(path, streamwriter.Options{...})
```

### 3.2 Separation of concerns

| Layer | Owns | Does not own |
|---|---|---|
| `internal/tiff` | Header bytes, IFD entry layout, TIFF type table, BigTIFF detection, JPEGTables blob format, WSI tag IDs, in-place patch helpers | Where to put bytes, when to flush, file management |
| `internal/tiff/streamwriter` | Streaming write order (header → IFD placeholders → tile bytes inline → patch IFDs in place), level handle, strip handle | TIFF byte format, JPEGTables format, tag IDs |
| `internal/tiff/cogwsiwriter` | COG-WSI layout planning (reverse-order tile data, head-block IFDs, ghost area), spool-and-finalize orchestration | TIFF byte format, BigTIFF detection, tag IDs |

### 3.3 Streaming vs spool orchestration

The two writers genuinely need different I/O patterns. The shared TIFF
core does **not** impose a single orchestration model.

- **Streaming** (`streamwriter`): tile bytes write inline to the output
  file as `WriteTile` is called. IFDs are emitted with placeholder
  offsets, patched in place once all tiles for a level are written.
  Optimal when output order matches input order (transcode and
  downsample both produce tiles in source order).

- **Spool-and-finalize** (`cogwsiwriter`): tile bytes go to per-level
  scratch files; nothing is written to the output until `Close`, which
  plans the final layout (reverse tile-data order per COG-WSI spec) and
  streams the spools into the output at pre-computed offsets. Required
  when output tile order differs from input order, as COG-WSI's
  smallest-overview-first layout demands.

Both writers consume `tiff.EntryBuilder`, `tiff.WriteHeader`,
`tiff.AutoPromote`, and other primitives identically.

## 4. `internal/tiff` core surface

### 4.1 Files

| File | Content |
|---|---|
| `doc.go` | Package overview. |
| `types.go` | TIFF type constants (`BYTE`=1, `ASCII`=2, `SHORT`=3, `LONG`=4, `RATIONAL`=5, `DOUBLE`=12, `LONG8`=16, `IFD8`=18). Per-type byte-size table. |
| `tags.go` | Standard TIFF tag ID constants we use; WSI private tag IDs 65080–65087. |
| `wsitags.go` | `WSIImageType*` string constants (8-value canonical set); `ValidateWSIImageType`. |
| `header.go` | `WriteHeader(io.WriterAt, bigtiff bool, firstIFDOffset uint64) error`; `HeaderSize(bigtiff bool) int`. |
| `entry.go` | `EntryBuilder` (extracted from `cogwsi/ifd.go`): `AddShort`, `AddLong`, `AddLong8`, `AddTileOffsets`, `AddASCII`, `AddBytes`, `AddDouble`, `AddRational`, `Encode`. Helpers `EntrySize`, `IFDRecordSize`. LE-only. |
| `jpegtables.go` | JPEG abbreviated-tables blob construction + parsing (moves from `wsiwriter/jpegtables.go`). |
| `bigtiff.go` | `BigTIFFMode` enum (`Auto`/`On`/`Off`), `AutoPromote(dataBytes, metaBytes uint64) bool` (2 GiB threshold + safety margin), `Resolve(mode, predictedBytes) bool`. |
| `patch.go` | `PatchUint32(io.WriterAt, at int64, v uint32) error`, `PatchUint64`. Used by streamwriter for in-place IFD offset finalization. |

Each file has a paired `_test.go` with isolated unit coverage.

Estimated size: ~700 LOC code + ~400 LOC tests. Almost entirely
extracted from existing code with minor cleanup.

### 4.2 Critical invariant

**Little-endian only.** TIFF spec permits both, but WSI files in
practice are universally LE. The v0.6.0 `cogwsi/ifd.go` removed its
`binary.ByteOrder` parameter (commit `12168dd`) for this reason; the
extracted core inherits that contract. No `bo binary.ByteOrder` argument
appears on any function in `internal/tiff`.

### 4.3 What stays out of the core

- **File-handle ownership.** Writers manage `*os.File` themselves; the
  core takes `io.WriterAt`.
- **Layout planning.** The COG-WSI spec's reverse-tile-data-order
  requirement is COG-WSI–specific; the planner stays in
  `cogwsiwriter/layout.go`.
- **Spool management.** The cogwsi spool primitive stays in
  `cogwsiwriter/spool.go`.
- **SVS-shape tag emission.** Aperio-specific tag set construction
  moves to a caller-side helper near `transcode.go`, not into the
  core.
- **`validAssocKinds` (COG-WSI 4-value subset).** Stays in
  `cogwsiwriter/validate.go`; it's COG-WSI format policy. The general
  8-value `ValidateWSIImageType` lives in the core.

## 5. `internal/tiff/streamwriter` (replaces `internal/wsiwriter`)

### 5.1 Public surface

```go
package streamwriter

type Options struct {
    BigTIFF          tiff.BigTIFFMode
    ImageDescription string
    Make, Model      string
    Software         string
    DateTime         time.Time
    SourceFormat     string
    ToolsVersion     string
}

type Writer struct { /* unexported */ }
func Create(path string, opts Options) (*Writer, error)

type LevelSpec struct {
    ImageWidth, ImageHeight uint32
    TileWidth, TileHeight   uint32
    BitsPerSample           []uint16
    SamplesPerPixel         uint16
    Photometric             uint16
    Compression             uint16
    JPEGTables              []byte
    NewSubfileType          uint32
    WSIImageType            string
    ExtraTags               []tiff.RawTag
}

type LevelHandle struct { /* unexported */ }
func (*Writer) AddLevel(LevelSpec) (*LevelHandle, error)
func (*LevelHandle) WriteTile(x, y uint32, compressed []byte) error

type StrippedSpec struct {
    Width, Height       uint32
    RowsPerStrip        uint32
    BitsPerSample       []uint16
    SamplesPerPixel     uint16
    Photometric         uint16
    Compression         uint16
    StripBytes          []byte
    NewSubfileType      uint32
    WSIImageType        string
    ExtraTags           []tiff.RawTag
}
func (*Writer) AddStripped(StrippedSpec) error

func (*Writer) Close() error
func (*Writer) Abort() error
```

### 5.2 API changes from v0.6.0 `wsiwriter`

1. **`AddAssociated` → `AddStripped`.** The current name conflates
   "this is an associated image" with "this is strip-encoded." The new
   name describes the geometry; `WSIImageType` carries the semantic
   role (label/macro/thumbnail/overview).
2. **No `WithLayout(...)` Option.** SVS-shape is no longer a writer
   mode. Callers (transcode.go when `--container svs`) build the
   Aperio-specific tags and pass them via `LevelSpec.ExtraTags` and
   `StrippedSpec.ExtraTags`.
3. **`ExtraTags []tiff.RawTag` on both spec types.** Caller-controlled
   tag emission; lets callers add format-specific tags (Aperio,
   future Philips, etc.) without the writer knowing about them.
4. **`WSIImageType` validated at `AddLevel` / `AddStripped` time** via
   `tiff.ValidateWSIImageType` (full 8-value set). Invalid values
   error early.
5. **Options struct instead of `WithXxx` functional options.** Cleaner
   to read at call sites; v0.6.0's functional-Options pattern is the
   only place in this codebase using that style.

### 5.3 Internal organization

| File | Purpose |
|---|---|
| `doc.go` | Package overview. |
| `options.go` | `Options` struct + validation. |
| `writer.go` | `Writer`, `Create`, `Close`, `Abort`, internal IFD-chain bookkeeping. |
| `levelhandle.go` | `LevelHandle`, `WriteTile`, per-level state. |
| `stripped.go` | `AddStripped` + helpers for strip-encoded IFDs. |
| `extratags.go` | `RawTag` consumption helper (calls into `tiff.EntryBuilder`). |
| `writer_test.go` | Black-box tests (`package streamwriter_test`). |
| `golden_test.go` | Ported SVS round-trip + TIFF format tests from v0.6.0. |

### 5.4 SVS-shape relocation

The Aperio-faithful output that v0.6.0 produces via `wsiwriter.WithLayout(LayoutSVS)`
becomes a caller-side concern. In `cmd/wsitools/`:

- New file `svs_tags.go` (or inline in `transcode.go`, decided during
  implementation) exposes a helper that builds the Aperio-specific
  `[]tiff.RawTag` slice for the L0 pyramid level + the label/macro
  IFDs, including Aperio's `ImageDescription` format and the
  `NewSubfileType=9` macro marker.
- `runTranscode` checks the resolved container; if `svs`, calls the
  helper to populate `ExtraTags` on each spec before handing to
  streamwriter.

Estimated streamwriter size: ~500 LOC (down from ~1700 in
`internal/wsiwriter`). SVS-shape helper at the call site: ~100 LOC.

## 6. `internal/tiff/cogwsiwriter` (replaces `internal/cogwsi`)

### 6.1 Public surface

Largely unchanged from v0.6.0 `internal/cogwsi`. Same struct names,
same method signatures, same behavior. The only changes are import
paths and internal delegation to `internal/tiff` primitives.

### 6.2 File reorganization

| v0.6.0 `internal/cogwsi/` | New `internal/tiff/cogwsiwriter/` |
|---|---|
| `ifd.go` (EntryBuilder) | **moved** to `internal/tiff/entry.go` |
| `tags.go` | **split**: constants → `internal/tiff/tags.go` + `internal/tiff/wsitags.go`; cogwsi-specific `validAssocKinds` + `ErrInvalidAssocKind` → new `validate.go` here |
| `ghost.go` | **stays** (COG-WSI format detail; no other writer needs it) |
| `layout.go` | **stays** (COG-WSI's reverse-tile-order layout planner; uses `tiff.IFDRecordSize` and `tiff.EntrySize`) |
| `spool.go` | **stays** (cogwsi's spool I/O is orchestration-specific) |
| `writer.go` | **stays** (calls `tiff.*` instead of local equivalents) |
| `doc.go` | **stays** |

### 6.3 Consumer change

`cmd/wsitools/convert.go` updates its import:

```go
- "github.com/wsilabs/wsitools/internal/cogwsi"
+ "github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
```

All `cogwsi.X` references become `cogwsiwriter.X`. Functionally
equivalent.

Estimated size: ~700 LOC (down from ~900 today, with the 195-line
`ifd.go` moving out).

## 7. Migration sequence

Three incremental landings, each leaves the codebase in a working
state.

### 7.1 Landing 1: Add `internal/tiff` core

Pure addition. No consumer yet.

- Create `internal/tiff/` per §4.
- Extract `internal/cogwsi/ifd.go` → `internal/tiff/entry.go`.
- Copy JPEGTables helpers from `internal/wsiwriter/jpegtables.go` →
  `internal/tiff/jpegtables.go`.
- Centralize WSI tag IDs (65080–65087) in `internal/tiff/tags.go` +
  `internal/tiff/wsitags.go`.
- Add `header.go`, `bigtiff.go`, `patch.go`.
- Unit tests in `internal/tiff/*_test.go`.
- Existing `internal/wsiwriter` and `internal/cogwsi` are untouched
  and still building. No risk to v0.6.0 behavior.

**Acceptance:** `go test ./internal/tiff/...` clean; full `make test`
suite still passes.

### 7.2 Landing 2: Port cogwsi to cogwsiwriter

Move-and-edit. The smaller, lower-risk port.

- `git mv internal/cogwsi internal/tiff/cogwsiwriter` (history
  preserved).
- Delete `internal/tiff/cogwsiwriter/ifd.go` (content now in
  `internal/tiff/entry.go`).
- Edit `internal/tiff/cogwsiwriter/writer.go` to call `tiff.*` helpers.
- Edit `internal/tiff/cogwsiwriter/layout.go` to use `tiff.IFDRecordSize`,
  `tiff.EntrySize`.
- Extract `validAssocKinds` + `ErrInvalidAssocKind` into new
  `validate.go`; delete the rest of `tags.go` (constants now in core).
- Update `cmd/wsitools/convert.go` import path and references.

**Acceptance:** `go test ./...` clean. Integration tests at
`cmd/wsitools/convert_integration_test.go` continue to pass byte-for-byte
against sample files. Manual diff: convert one CMU SVS via the new
binary and the v0.6.0 binary; outputs must be byte-identical.

### 7.3 Landing 3: Replace wsiwriter with streamwriter

The riskiest step. Apply careful golden-master verification.

**Pre-landing golden-master capture.** Before any code changes, run
v0.6.0 transcode + downsample against the standard fixtures and
capture output hashes:

```sh
for f in sample_files/svs/*.svs sample_files/philips-tiff/*.tiff; do
  out=/tmp/golden-$(basename $f).out
  ./bin/wsitools transcode --codec jpeg --container svs -o "$out" "$f"
  echo "$(sha256sum "$out")  transcode-svs  $f" >> docs/superpowers/golden-masters-v0.6.0-transcode.txt
done

# Similar capture for transcode --container tiff and downsample.
```

Commit `docs/superpowers/golden-masters-v0.6.0-transcode.txt` to the
repo before starting landing 3.

**Then port:**

- Create `internal/tiff/streamwriter/` per §5.
- Port streaming-write orchestration progressively from
  `internal/wsiwriter/tiff.go`, function by function. Watch tests
  green after each move.
- Replace ad-hoc tag encoding with `tiff.EntryBuilder` calls.
- Replace local header bytes with `tiff.WriteHeader`.
- Replace patch helpers with `tiff.PatchUint32/64`.
- Move SVS-shape logic out of `internal/wsiwriter/svs.go` to
  `cmd/wsitools/svs_tags.go` (or inline in transcode.go).
- Update `cmd/wsitools/transcode.go`:
  - Import `internal/tiff/streamwriter`.
  - Build Aperio `ExtraTags` slice when `--container svs`.
- Update `cmd/wsitools/downsample.go` similarly (no SVS path; just
  import + spec field rename).
- Port `internal/wsiwriter/svs_roundtrip_test.go` + `tiff_test.go` to
  `internal/tiff/streamwriter/`.
- Delete `internal/wsiwriter/` once all callers migrate.

**Post-landing verification:**

- Re-run the golden-master capture and diff against the pre-landing
  hashes. **Must match exactly.**
- `go test ./...` clean.
- Manual smoke: each of transcode (jpeg+container svs, jpeg+container
  tiff, jpegxl+container tiff, downsample) runs on a CMU sample
  without error and produces the expected output.

**Acceptance:** all tests green; golden-master hashes byte-identical
pre- vs post-port; manual smokes pass.

## 8. Testing strategy

### 8.1 Unit coverage (new in `internal/tiff`)

- `entry_test.go`: inline vs external placement, classic + BigTIFF
  layouts, tag sort order, ASCII NUL termination, LONG/LONG8 selection
  in `AddTileOffsets`, overflow guard on classic offsets > 4 GiB.
- `header_test.go`: classic header bytes (`II`+`*\0`+IFD0 offset);
  BigTIFF header bytes (`II`+`+\0`+offsetsize+IFD0 offset).
- `bigtiff_test.go`: `AutoPromote` decision at 2 GiB threshold;
  override modes (`On`/`Off`).
- `jpegtables_test.go`: round-trip on real SVS-derived JPEGTables blobs.
- `wsitags_test.go`: `ValidateWSIImageType` accepts all 8 canonical
  values, rejects unknown.
- `patch_test.go`: `PatchUint32` / `PatchUint64` at arbitrary offsets.

### 8.2 Ported tests

- `internal/cogwsi/*_test.go` → `internal/tiff/cogwsiwriter/*_test.go`
  (landing 2). Assertions unchanged.
- `internal/wsiwriter/svs_roundtrip_test.go`,
  `internal/wsiwriter/tiff_test.go`,
  `internal/wsiwriter/jpegtables_test.go` →
  `internal/tiff/streamwriter/*_test.go` (landing 3).

### 8.3 Integration safety nets

- `cmd/wsitools/convert_integration_test.go` (bit-exact tile-copy
  against six source formats) runs after landings 2 and 3.
  Byte-identical outputs verify both ports introduce no regressions.
- Golden-master hash fixtures (committed before landing 3) verify
  transcode + downsample produce byte-identical output post-port.

### 8.4 Cross-cutting

- `make test` (= `go test -race -count=1 ./...`) passes after each
  landing.
- `WSI_TOOLS_TESTDIR=$PWD/sample_files go test ./cmd/wsitools/ -run TestConvert -count=1`
  passes after each landing.

## 9. Risks & open questions

### 9.1 Risks

1. **Byte-level drift in transcode/downsample (landing 3).** Mitigation:
   golden-master hash fixtures, verified at landing 3 acceptance.

2. **Hidden coupling in `internal/wsiwriter/tiff.go` (902 lines).**
   Mitigation: move code progressively, not by rewrite. Watch test
   suite after each move.

3. **JPEGTables format subtleties.** Both writers preserve source
   JPEGTables verbatim; transcode also constructs new tables when
   re-encoding. Mitigation: port existing fixtures (real Aperio
   JPEGTables blobs from sample SVS) and verify round-trip.

4. **SVS-shape relocation.** Aperio-specific knowledge moves caller-
   side. If a future second caller wants SVS-shape, knowledge would
   duplicate. Mitigation: single caller for v0.7.0 (transcode.go); if
   a second consumer appears, hoist to a shared helper package.

5. **Three-landing review effort.** Mitigation: plan accordingly;
   each landing is a 1–2 day effort with its own review cycle.

### 9.2 Open questions deferred to implementation

- Final file split in `internal/tiff` (whether `bigtiff.go` and
  `header.go` stay separate or merge). Decide during landing 1 based
  on what reads cleanly.
- Whether `streamwriter.Options` (drafted as a struct in §5.1) reverts
  to functional `WithXxx` Options if call-site diffs in transcode.go /
  downsample.go are noisier than expected. Default: struct, decided
  during landing 3 only if a real call-site readability problem appears.
- `tiff.RawTag` exported type vs an alias of the unexported entry
  type. Decide during landing 1.
- `JPEGTables` content as a single file or dedicated subpackage.
  Default: single file unless it grows beyond ~200 LOC.

## 10. Release & rollout

Lands in **v0.7.0**. No user-visible behavior changes; v0.6.0 binary
outputs are byte-identical pre- and post-refactor for the same inputs
and flags.

- CHANGELOG v0.7.0 entry: **Changed (internal)** section noting the
  TIFF core extraction. No Added/Removed/Deprecated entries for end
  users.
- No new commands, flags, or output format changes.
- Version tag releases follow existing pattern: `release: bump Version
  to 0.7.0` → tag `v0.7.0` → `post-release: bump Version to
  0.8.0-dev`.
- README unchanged.

If unrelated user-facing features ship in v0.7.0 (e.g., dropping the
RGB-only assumption from `cmd/wsitools/convert.go`), they get their
own CHANGELOG entries in the same release.

## 11. Out-of-scope follow-ups

These are mentioned for context; they are **not** part of this
refactor:

- `--to svs`, `--to bif`, `--to <other TIFF-based>` targets on `convert`.
  Becomes easy once the streamwriter exists, but is its own feature.
- Folding `transcode` into `convert` (lossy paths). Bigger scope.
- Iris writer (`internal/iris`). Iris isn't TIFF; separate tree.
- Dropping the RGB-only assumption in convert/transcode. Independent.
- BigTIFF-aware test helpers in `cmd/wsitools/convert_integration_test.go`.
- Reader-side improvements in opentile-go (issues #5/#6).
