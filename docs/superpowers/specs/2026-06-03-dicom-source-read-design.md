# DICOM-WSI as a first-class read source — design

> Status: **approved design** (brainstormed 2026-06-03). Next: implementation
> plan via writing-plans. Branch off `main`; never implement on `main`.

## Goal

Make DICOM VL Whole Slide Microscopy (WSM) a fully-supported *read* source
across the wsitools CLI — `info`, `region`, `convert`, `extract`, `hash`,
`dump-ifds` — with honest, safe behavior on the multi-instance / multi-series
realities of DICOM. This is the read side only; the DICOM *writer*
(`convert --to dicom`) is a separate, larger effort (see
`docs/notes/2026-06-03-dicom-writer-scoping.md`).

## Background: most of this already works

opentile-go v0.32.2 ships a DICOM reader (`formats/dicom`), and wsitools already
blank-imports `formats/all` with no per-format allow-list, so DICOM flows
through `source.Open` → `opentile.OpenFile` transparently. Empirically verified
against the `sample_files/dicom/Leica-4` fixture (a 23374×22079, 3-level WSM
series):

| Command | DICOM dir | single `.dcm` | Status |
|---|---|---|---|
| `info` | ✅ | ✅ | full pyramid, MPP, mag, vendor, associated images; sibling-series scan works |
| `region` | ✅ | — | decodes JPEG tiles correctly |
| `convert --to cog-wsi` | ✅ | — | tile-copy in 155 ms; metadata carried; reads back clean (⇒ svs/tiff/ome-tiff/dzi too) |
| `hash --mode pixel` | ✅ | — | source-layer pixel hash works |
| `extract` (overview/thumbnail, JPEG) | ✅ | ✅ | JPEG associated images decode fine |
| **`extract` (label, uncompressed)** | ❌ | ❌ | `tiff: invalid format: malformed header` |
| **`hash` (default file-mode)** | ❌ | — | `is a directory` |
| **`dump-ifds`** | ❌ | ❌ | TIFF IFD walker; inherently N/A for DICOM |

So this is **not** "build a DICOM reader" — it is "close the gaps where wsitools
bypasses the source layer or assumes TIFF / single-file," add tests + a CI
fixture, and make directory ambiguity fail safely.

**Root cause of every gap:** commands that touch the path as a *single TIFF
file* break; everything routed through `source.Open`→`opentile.OpenFile` (which
handles a directory and series) already works.

## Design principles

1. Route through the `source` layer wherever a command needs slide data;
   opentile-go is canonical for all DICOM parsing — wsitools must not
   re-implement DICOM tag reading.
2. CLI is **safe-by-default**: refuse genuine ambiguity with an actionable
   error rather than silently picking a slide. (The library stays permissive.)
3. TIFF-only commands **degrade gracefully** on non-TIFF sources with a clear
   message, never a raw low-level error.
4. Small, isolated changes to the commands that own each gap — no new package.

## Scope (phased)

### Phase A — fix the `extract` bug (lands first, standalone)

`extract` already routes through `source.Associated()`/`Bytes()`; the failure is
in `decodeAssociated` (`cmd/wsitools/extract.go`): the
`CompressionNone`/`CompressionDeflate` branch calls `xtiff.Decode(b)`, assuming
the bytes are an SVS-style TIFF wrapper. DICOM's uncompressed associated images
are not TIFF → "malformed header." JPEG/JPEG2000 associated images already work.

**Fix:** handle the uncompressed (`none`) associated-image case without assuming
a TIFF container. The implementation plan must first confirm what opentile-go's
DICOM `AssociatedImage.Bytes()` returns for an uncompressed image (raw RGB(A) of
the reported `w×h`, vs. some container) and decode accordingly; keep the
existing TIFF-wrapper path for TIFF-dialect sources. This is a correctness
bugfix and is not gated on the rest of the work.

### Phase B — first-class read citizen

- **`hash` file-mode + `dump-ifds`: degrade gracefully.** When the input is a
  directory and/or a DICOM source, emit a clear, actionable message instead of a
  raw error:
  - `hash` (file-mode) on a **directory** → error pointing to
    `hash --mode pixel` (a file-SHA of a multi-file series is ill-defined). A
    single `.dcm` in file-mode still hashes that file's bytes as today (a valid
    SHA of one instance — documented as one-instance, not whole-series; use
    `--mode pixel` for a content hash of the whole slide).
  - `dump-ifds` on a non-TIFF source → "not a TIFF-dialect source; DICOM has no
    TIFF IFDs to dump" (it can never dump DICOM — there are no IFDs).
- **Multi-series ambiguity → actionable error** (see next section).
- **Tests:** a DICOM smoke test covering info / region / convert / extract
  (incl. the uncompressed label) / hash --mode pixel on the Leica-4 fixture.
- **CI fixture:** wire a small DICOM WSM series into the CI fixture set
  (advances the roadmap's "expanded fixture coverage").
- **Docs:** input may be a `.dcm` instance or a series directory; document the
  multi-series behavior.

### Phase C — convert-fidelity validation

Round-trip tests asserting `DICOM → {cog-wsi, ome-tiff}` carry pixels and
metadata faithfully (MPP, magnification, ICC, vendor fields); audit the metadata
mapping for completeness. Pairs naturally with the future DICOM writer, where
round-trip fidelity matters most.

## Multi-series directories — safe-by-default

**Current opentile-go behavior:** a directory with >1 WSM `SeriesUID` is resolved
by `selectDominantSeries` — the series with the most VOLUME instances wins (ties
broken by sorted UID; deterministic), and the other slide is **silently
ignored**. A single-instance path anchors to that instance's `SeriesUID`
(disambiguation escape hatch).

**Decision:** wsitools must **not** silently pick. When a directory resolves to
more than one WSM series, fail with an error that *names the ambiguity and the
fix*, e.g.:

```
error: <dir> contains 2 distinct WSM series:
  • <UID-A>  (Leica GT450, 3 levels, 40×)
  • <UID-B>  (Leica GT450, 3 levels, 20×)
Specify one by passing the path to a .dcm instance of the series you want.
```

Single-series directories are unaffected (the common case opens normally). The
escape hatch — pointing at a specific `.dcm` — already works, so no new flag is
required; a `--series <UID>` selector is deferred (out of scope).

**The ambiguity check keys off the input *type*, not the folder contents:**

| Input | Folder has | Behavior |
|---|---|---|
| directory | 1 series | opens it |
| directory | >1 series | **ambiguity error** |
| single `.dcm` | 1 series | opens that series |
| single `.dcm` | >1 series | opens **that instance's** series; others ignored — **no error** |

A named `.dcm` is never ambiguous regardless of what else shares its folder
(opentile-go anchors to the instance's `SeriesUID` and assembles only its
same-series siblings). The ambiguity error fires **only** for *directory* inputs
that resolve to >1 series — the implementation must not error on a named
instance merely because its folder is mixed.

### Required opentile-go enhancement (file as part of this work)

To produce that error without re-parsing DICOM in wsitools (which would violate
"opentile-go is canonical"), opentile-go must surface the set of WSM series under
a path. **A GitHub issue on `WSILabs/opentile-go` is an explicit deliverable of
this spec** (same pattern as #7/#9/#10/#11/#12) — filed as
**WSILabs/opentile-go#13**. Requested capability:

> Enumerate WSM series under a directory/instance path without fully opening a
> slide — e.g. `dicom.ListWSMSeries(path) ([]SeriesInfo, error)` returning per
> series `{SeriesUID, levelCount, make, model, magnification, instanceCount}`;
> *or* an `OpenFile` option / typed `AmbiguousSeriesError` that carries the
> candidate list. wsitools uses this to render the actionable ambiguity error.

**Sequencing:** Phase A, the graceful-degradation work, tests, fixture, and the
issue itself ship without the new API. The **ambiguity error flips on once the
opentile-go API lands** and wsitools bumps to that version; until then, a
directory opens the dominant series as today, documented as interim behavior.

## Architecture & isolation

No new package. Changes are localized to the commands that own each gap:

- `cmd/wsitools/extract.go` — `decodeAssociated` uncompressed-case fix (A).
- `cmd/wsitools/hash.go` — file-mode dir/DICOM guard → graceful error (B).
- `cmd/wsitools/dump-ifds` command — non-TIFF-source graceful error (B).
- The ambiguity check belongs at the `internal/source` boundary (a small
  preflight using the new opentile-go enumeration API), so every command that
  opens a slide inherits the safe behavior uniformly rather than each
  re-checking.
- Tests under `cmd/wsitools/` + `internal/source/`; CI fixture wiring in the
  existing CI fixture pipeline.

## Error handling

- Ambiguous directory → non-zero exit, actionable multi-series message.
- `dump-ifds`/`hash file-mode` on DICOM → non-zero exit, message pointing to the
  right tool/mode.
- Empty/garbage directory → opentile-go's existing "no WSM instances" error,
  surfaced verbatim.

## Testing

- **Smoke (B):** info / region / convert→cog-wsi / extract (all kinds incl.
  uncompressed label) / hash --mode pixel on `dicom/Leica-4`; assert success +
  expected dimensions/metadata.
- **Ambiguity (B, once API lands):** a fixture dir with two series → assert the
  actionable error + non-zero exit; a single-`.dcm` input into that dir → assert
  it opens the anchored series.
- **Fidelity (C):** DICOM → {cog-wsi, ome-tiff} round-trip — decoded-pixel and
  metadata equality (pixel hash, MPP/mag/ICC).
- Integration tests gated by `WSI_TOOLS_TESTDIR`; run heavy `-race` suites
  uncontended (default 600 s test timeout can false-fail under concurrent load).

## Out of scope

- DICOM *writer* (`convert --to dicom`) — separate effort.
- `--series <UID>` selector flag — deferred; the `.dcm`-anchor escape hatch
  covers disambiguation for now.
- Fluorescence / multi-channel / multi-frame-per-focal-plane semantics beyond
  what opentile-go already synthesizes.

## Open questions (resolved during brainstorming)

- *Scope?* → A (bugfix, first) → B → C.
- *Multi-series dir?* → actionable ambiguity error, not silent pick; enabled by
  a new opentile-go enumeration API (filed as part of this work).
- *New reader needed?* → no; the read path largely works via opentile-go v0.32.2.
