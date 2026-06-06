# Associated-image editing (`label`/`macro`/`thumbnail`/`overview` remove & replace) — design

> Status: **approved design** (brainstormed 2026-06-05). Next: writing-plans.
> Branch off `main`; never implement on `main`.
> Spans **two repos**: an opentile-go API addition (lands first), then wsitools Slice 1.

## Goal

Add in-place editing of a WSI's **associated images** — remove (e.g. strip the
PHI-bearing label) or replace/add one — doing so **per-format-efficiently, never
naively**: copy the multi-gigabyte pyramid byte-for-byte and rewrite only the
small tail that holds the associated image. New noun-first commands:
`wsitools label remove|replace` and the same shape for `macro`, `thumbnail`,
`overview`.

## Background

### Existing surface (what we build on)
- **Read side already works:** `source.Open` → `src.Associated()` returns a
  cross-format-classified list; each `AssociatedImage` has `Type()` (`label`,
  `macro`, `thumbnail`, `overview`, and format-specific `map`/`probability`),
  `Size()`, `Compression()`, `Bytes()`. `extract --type label` decodes one; `dump-ifds`
  and `info` inspect. So **only the two write verbs are new** — no `ls`/inspect
  verb needed.
- **`convert` already *writes* associated images** (`writeAssociatedImages` in
  `cmd/wsitools/convert_tiff.go`; `cogwsiwriter.AssociatedSpec`) and can drop
  *all* of them (`--no-associated`). But `convert` re-lays-out the whole pyramid
  through the streamwriter — the **naive** path for a small label edit.
- **Raw TIFF primitives exist:** `internal/tiff` (EntryBuilder, WriteHeader,
  PatchUint32/64, BigTIFF auto-promote, tag constants) and `internal/source`'s
  `WalkIFDsRaw` (raw directory walk backing `dump-ifds --raw`).

### Reference implementation
`wsilabs/wsi-label-tools` (the author's separate, MIT, Aperio-only proven tool)
does the efficient thing: a **prefix-copy + tail-re-emit splice** that produces
output "bit-identical to the input everywhere except the label," never decoding
pyramid pixel data. This spec **ports that engine** into wsitools idioms and
generalizes it (cross-format type detection via opentile-go; lossless-by-default
replacement encoding). Files studied: `internal/tifflow/{splice,ranges,parse,
ifd,header}.go`, `internal/aperio/{detect,build_label,label}.go`,
`internal/atomic/atomic.go`, `internal/cli/resolve.go`, `internal/wsi/backend.go`.

### Why splice beats the `convert` rewrite
- **Pyramid is bit-identical** — one `io.Copy` of `[0, cutoff)`. No decode, no
  re-encode, no tile re-layout.
- **PHI is genuinely gone** — the removed label's bytes never reach the output
  (compact tail re-emit), so there is **no recoverable slack** and the file is
  *smaller*. This resolves the compact-vs-surgery tension: prefix = surgery-fast,
  tail = compact/no-PHI.
- **Safe-by-default** — a `RangeMap` dominance check refuses
  (`ErrUnexpectedLayout`) rather than corrupt a slide whose layout interleaves
  associated-image data among pyramid data.

## Scope (decided)

### Slice 1 (this spec implements)
- **Formats:** **SVS** (proven) and **generic single-plane TIFF** — both linear
  IFD chains with associated images physically after the pyramid.
- **Verbs:** `remove` and `replace` (`replace` is an **upsert**: substitutes if
  present, appends a new associated IFD if absent).
- **Types:** `label`, `macro`, `thumbnail`, `overview`. The four commands are
  generated from one shared implementation, differing only by the type string
  they target.
- **opentile-go addition:** `AssociatedIFDOffset` (see API below).

### Slice 2 (noted as follow-up, NOT implemented here)
- **OME-TIFF** (SubIFD pyramid hanging off IFD0; associated images are top-level
  IFDs) and **COG-WSI**. These require the `RangeMap` to attribute SubIFD-tree
  byte ranges to their owning top-level IFD before the dominance check is sound.
  Until then these formats **error** with a clear "coming next" pointer.
- **OME-XML sync** (dropping/adjusting the `<Image>` for an edited associated
  image) belongs to Slice 2, since only OME-TIFF carries OME-XML.
- `--rotate` for replacement images; DICOM (separate `.dcm` instance drop/swap —
  a different engine entirely); other read-only formats.

## Repo A — opentile-go: expose the associated IFD offset

**Why:** wsitools must map a typed `AssociatedImage` back to its raw IFD byte
offset to splice it. opentile-go already classifies the type and internally
holds the associated image's `*tiff.Page` (which knows its IFD offset). Exposing
that offset avoids re-deriving per-format classification rules in wsitools
(which would drift from the canonical reader).

**API (new):**

```go
// AssociatedIFDOffset returns the byte offset of the IFD (TIFF directory)
// backing associated image a, for TIFF-family slides. ok is false when the
// slide's format is not TIFF-backed or a is not one of s.Associated().
func (s *Slide) AssociatedIFDOffset(a AssociatedImage) (offset int64, ok bool)
```

- Implemented for the SVS and generic-TIFF readers in Slice 1 (the formats
  wsitools Slice 1 edits). Other TIFF formats may return `ok=true` as they gain
  support; non-TIFF (DICOM/IFE) return `ok=false`.
- The associated image's backing `*tiff.Page` already records its source IFD
  offset; this method surfaces it. (If `Page` does not yet retain the offset,
  add an unexported field populated at parse time.)
- **Verification per CLAUDE.md:** after the change, run opentile-go's *own*
  suite with the fixture gate —
  `OPENTILE_TESTDIR=$(pwd)/sample_files go test ./decoder/... ./formats/...` —
  and confirm the new behavior is exercised (PASS, not SKIP). Then bump wsitools'
  `go.mod` to the new opentile-go version.

## Repo B — wsitools

### Package layout

```
internal/tiff/edit/        # NEW — ported splice engine (pure Go)
  parse.go                 #   raw TIFF/BigTIFF parse → File{Header, IFDs, Ranges}
  ranges.go                #   RangeMap: per-IFD byte-range ownership + dominance
  splice.go                #   Splice(Remove|Replace|Append|InsertBefore)
  ifd.go                   #   IFD/IFDEntry accessors (StringValue, UintArray, …)
  header.go                #   Header parse + first-IFD-offset location
  *_test.go                #   table tests + golden round-trips
internal/atomic/           # NEW — WriteAtomic (temp + fsync + rename)
  atomic.go
cmd/wsitools/
  associated.go            # NEW — cobra: builds label/macro/thumbnail/overview
                           #   command groups (remove|replace) from one factory
  associated_locate.go     # NEW — type → raw IFD index via AssociatedIFDOffset
  associated_replace.go    # NEW — decode --image, resize/letterbox, encode strips,
                           #   build ReplacementIFD
```

`internal/tiff/edit` is a **sibling of `streamwriter`/`cogwsiwriter`** under the
TIFF core: those *emit fresh* TIFFs; `edit` *surgically rewrites an existing*
one. Pure Go; cgo only via `internal/codec` when encoding a replacement.

### The splice engine (`internal/tiff/edit`)

Ported from `wsi-label-tools/internal/tifflow`, re-homed onto wsitools' byte
order / tag constants. Public surface:

```go
type File struct {
    Header *Header
    IFDs   []*IFD     // chain order
    Ranges *RangeMap
}
func Parse(r io.ReaderAt, size int64) (*File, error)

type SpliceMode int
const ( SpliceRemove SpliceMode = iota; SpliceReplace; SpliceAppend; SpliceInsertBefore )

type ReplacementIFD struct {
    Tags      []OutTag    // ordered TIFF entries; StripOffsets resolved at emit
    StripData [][]byte    // compressed strips, written before the IFD record
}

type SpliceParams struct {
    InPath, OutPath string
    File            *File
    Mode            SpliceMode
    TargetIdx       int            // chain-order index of the IFD to edit
    Replacement     *ReplacementIFD
    Fsync           bool
}
func Splice(p SpliceParams) error
```

**Algorithm (unchanged from the proven tool):**
1. `firstTail` and `minTailOwner` derive from `Mode`/`TargetIdx`.
2. `cutoff = Ranges.MinOffsetOfOwnersAtOrAfter(minTailOwner)` (or file size if
   the tail owns nothing).
3. **Dominance check:** for every earlier IFD `i < minTailOwner`, assert
   `!Ranges.AnyRangeOfOwnerAtOrAfter(i, cutoff)`; else `ErrUnexpectedLayout`.
4. Copy `[0, cutoff)` verbatim to a temp output.
5. Re-emit tail IFDs `[firstTail, n)` via `reemitIFD` (rebasing out-of-line tag
   blobs + strip/tile data, 2-byte word alignment), inserting the emitted
   `ReplacementIFD` first for Replace/Append/InsertBefore.
6. Patch emitted IFDs' next-pointers chain-to-chain, then patch the
   predecessor's next-pointer (header first-IFD slot if `TargetIdx == 0`).
7. `Fsync` (optional) → close → `os.Rename` over `OutPath`.

`RangeMap` (`ranges.go`) is ported verbatim: sorted non-overlapping ranges with
`Add` (overlap-detecting), `MinOffsetOfOwner`, `MinOffsetOfOwnersAtOrAfter`,
`AnyRangeOfOwnerAtOrAfter`. `Parse` populates it by attributing each IFD record,
each out-of-line tag blob, and each strip/tile to its chain-order owner.

> **Slice-1 constraint:** linear chains only. SubIFD trees (OME-TIFF/COG) are
> NOT range-attributed yet; those formats are rejected before reaching `Splice`
> (see dispatch below), so the dominance check stays sound.

### Target location (`associated_locate.go`)

```go
// locateAssociated returns the chain-order IFD index of the associated image of
// the given type, plus its source.AssociatedImage (for dims/compression).
func locateAssociated(src source.Source, file *edit.File, typ string) (
    idx int, a source.AssociatedImage, err error)
```

- Find `a` in `src.Associated()` whose `Type() == typ`; if none → `ErrNoSuchAssociated`.
- `off, ok := a.IFDOffset()` (delegates to opentile-go's `AssociatedIFDOffset`);
  `!ok` → `ErrUnsupportedFormat`.
- Translate `off` → chain index by matching `file.IFDs[i].Offset == off` (each
  parsed `IFD` records its own directory-record offset).
- **Slide accessor:** `locateAssociated` needs the underlying `*opentile.Slide`
  to call `AssociatedIFDOffset`. Add one accessor to the `source` layer —
  `func (s *Source) tiffSlide() (*opentile.Slide, bool)` (or surface
  `AssociatedIFDOffset` directly as a `source.Source` method that delegates) —
  rather than leaking opentile types broadly. The delegating method is
  preferred: `source.AssociatedImage` gains `IFDOffset() (int64, bool)`,
  implemented by `opentileAssociated` calling `s.t.AssociatedIFDOffset(a.a)`.

### Replacement build (`associated_replace.go`)

Ported/generalized from `aperio/{build_label,label}.go`:
- Decode `--image` (PNG/JPEG/TIFF via stdlib + `x/image/tiff`) to RGBA.
- **Target dims:** the existing associated image's `Size()` if present; else a
  per-type default (label `1200×848`). `--label-dims WxH` overrides.
- **Resize:** `fit` (aspect-preserving + `--bg` letterbox, default Aperio
  parchment `#F5F5E6`), `stretch`, or `none` (reject mismatched input). Default
  `fit`. Aspect-ratio guard rejects >2× aspect mismatch unless `--force`.
- **Encode (decided):** match the source associated codec / per-type default —
  - `label` → **LZW + predictor 2** (lossless; barcode/text-safe;
    Aperio-faithful; `RowsPerStrip=2`, `ImageDescription` second line
    `label WxH`). Use `github.com/hhrutter/lzw` (early-change variant, libtiff/
    Pillow-compatible) — add as a dep.
  - `macro`/`thumbnail`/`overview` → **JPEG** (via `internal/codec`).
  - `--compression {jpeg,lzw,deflate,none}` overrides.
- Build a `ReplacementIFD` with the encoded strips, preserving the existing
  image's `ImageDescription` where one exists.

### CLI (`associated.go`)

One factory builds four cobra command groups; each has `remove` and `replace`:

```
wsitools label    remove  [flags] <slide>
wsitools label    replace [flags] --image <file> <slide>
wsitools macro     remove|replace …
wsitools thumbnail remove|replace …
wsitools overview  remove|replace …
```

**Shared flags:** `-o/--output <path>` (explicit); `--in-place` (atomic mutate
original); `--overwrite` (clobber existing output); `--fsync` (default true);
`-q`.
**`replace`-only:** `--image <file>` (required); `--compression`; `--resize
fit|stretch|none` (default `fit`); `--bg RRGGBB`; `--label-dims WxH`; `--force`.

**Output resolution** (ported from `resolve.go`): if neither `-o` nor
`--in-place`, derive `<stem>_relabeled<ext>` (numbered suffix if it exists);
`--in-place` writes to a sibling temp then renames over the input; error if
resolved output == input.

**Dispatch & errors:**
- Open via `source.Open`; reject non-SVS/non-generic-TIFF formats (incl.
  OME-TIFF/COG-WSI) with `not yet supported for <format> — coming next; for now
  use convert` (Slice-2 pointer).
- `remove` when the type is absent → error `no <type> image in slide` (no silent
  no-op; `--if-exists` deferred).
- `replace` on a slide without that type → upsert (append a new associated IFD).
- `--image` missing/undecodable, `-o`+`--in-place` together, cross-filesystem
  `--in-place` → clear errors.

## Data flow

```
remove:
  source.Open → Associated()         (confirm type exists + dims)
  edit.Parse(file)                   (raw chain + RangeMap)
  locateAssociated → TargetIdx
  edit.Splice{Remove, TargetIdx}     (prefix copy + tail re-emit, omit target)
  atomic rename → out

replace:
  source.Open → Associated()         (existing dims/desc if present)
  decode --image → resize/letterbox → encode strips → ReplacementIFD
  edit.Parse(file); locate (idx or append)
  edit.Splice{Replace|Append, …}     (substitute / append target)
  atomic rename → out
```

## Error handling

| Condition | Behavior |
|---|---|
| Format not SVS/generic-TIFF | error + Slice-2 / `convert` pointer |
| `remove`, type absent | error `no <type> image in slide` |
| Layout interleaves assoc data among pyramid | `ErrUnexpectedLayout`, no write |
| `--image` missing/undecodable | error |
| `-o` and `--in-place` both set | error |
| resolved output == input | error |
| aspect mismatch >2× without `--force` | error |
| `--in-place` temp on different filesystem | error (rename would fail) |

## Testing

`make test` runs `-race -count=1`; integration gated by `WSI_TOOLS_TESTDIR`.

**Unit (`internal/tiff/edit`, `internal/atomic`):**
- `RangeMap`: overlap rejection; `MinOffsetOfOwnersAtOrAfter`;
  `AnyRangeOfOwnerAtOrAfter` dominance.
- `Splice` golden round-trips on a small synthetic 3-IFD classic-TIFF *and* a
  BigTIFF: Remove middle IFD → chain re-links, prefix bytes identical, target
  bytes absent from output; Append → new IFD linked last; Replace → substituted.
- `Splice` refusal: synthetic file with an early IFD owning bytes past the
  cutoff → `ErrUnexpectedLayout`, output not created.
- `WriteAtomic`: success renames; mid-write error removes temp, leaves target
  untouched.

**Integration (gated; `CMU-1-Small-Region.svs` = SVS 20×, plus a generic-TIFF
fixture):**
- `label remove`: output opens; `src.Associated()` no longer lists `label`;
  **pyramid pixels bit-identical** — assert the copied prefix region is
  byte-for-byte equal to the input over `[0, cutoff)` *and* cross-check
  `hash --mode pixel` on the main image is unchanged; output file size < input.
- `label replace --image <png>`: `extract --type label` on the output decodes to
  the supplied image (dims = target dims); stored codec = LZW for SVS label.
- `macro remove` on a slide with a macro; `overview`/`thumbnail` as fixtures
  allow.
- `remove` then `extract --type label` → "not found" error.
- `--in-place`: original path holds the edited slide; no leftover temp files.
- non-SVS/non-generic input (e.g. `*.ome.tiff`) → Slice-2 error message.

## Out of scope (Slice 2 / later)

- OME-TIFF + COG-WSI (SubIFD-range attribution) and OME-XML `<Image>` sync.
- DICOM associated-instance drop/swap.
- `--rotate`; batch driver; `--if-exists` idempotent remove.

## Open questions

None blocking. SubIFD-range attribution (Slice 2) is the main known unknown and
is deliberately excluded here.
