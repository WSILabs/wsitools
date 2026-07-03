# CLI Matrix Invariant-Audit Harness — Design

**Date:** 2026-07-03
**Status:** Approved (design), pending implementation plan

## Goal

A one-off (rerunnable) harness, extending `scripts/qa/`, that exercises a
**tiered representative matrix** of every wsitools CLI command across
inputs → outputs × option combinations, then **automatically catalogues
discrepancies** by checking invariants over the commands' `--json` output plus
external-tool openability. The end product is a triaged catalogue of *new* issues
(the human triages the report with the agent at the end; the harness does not
auto-file issues).

This generalizes the manual reasoning that already caught real bugs this session
(cog-wsi silently ignoring `--codec`; lossless-crop chroma subsampling tag ≠
bytes) into an automated sweep.

## Key decisions (settled)

1. **Breadth:** tiered representative sampling (not exhaustive cross-product) —
   disk is a hard constraint (big fixtures have filled the disk before).
2. **Oracle:** invariants over `info`/`dump-ifds` `--json` **plus** external
   cross-validation (OpenSlide + Bio-Formats, already installed).
3. **Harness:** extend `scripts/qa/` (reuse its case runner, manifest, and the
   `check-openslide.sh` / `check-bioformats.sh` validators) rather than a fresh tool.
4. **Special emphasis (explicit user ask):** scrutinize **all** metadata exposed
   by `info` — (a) does each field make *semantic sense*, and (b) is it
   *consistent across commands* (info vs dump-ifds vs hash) and *across
   containers* (the same source converted to svs/tiff/ome-tiff/dicom/… should
   report identical make/model/software/datetime/mpp/mpp_x/mpp_y/magnification).

## `info --json` schema (the surface under audit)

```
{ path, size_bytes, format,
  metadata: { make, model, software, datetime, mpp, mpp_x, mpp_y, magnification },
  levels: [ { index, width, height, tile_width, tile_height, compression,
              quality: { codec, lossless, quality_estimate, chroma_subsampling, notes } } ],
  associated_images: [ { type, width, height, compression } ] }
```
`dump-ifds --json` exposes the raw per-IFD TIFF tags (dims, Compression,
TileOffsets/ByteCounts, YCbCrSubSampling, Resolution*, ImageDescription).
`hash --json` and `validate --json` expose pixel/file digests and a conformance
report respectively.

## Architecture — four components (data flows left → right)

### 1. Matrix driver → `cases.jsonl`
Extends `run-matrix.sh` to emit, per case, a structured record:
`{ id, cmd_argv, input, input_format, output, output_container, transform_type,
   requested_codec, factor, rect, lossless, expect_error, source_props }`.
`transform_type ∈ {read, container-swap, downsample, factor, crop, transcode,
recodec, roundtrip, associated-edit}`. Tiers:

- **T1 broad-shallow:** every readable input format → `info`, `dump-ifds`,
  `hash --mode pixel`, `validate`, `region` (a fixed rect), `extract` (each
  present associated type); and every input → every *valid* output container at
  defaults (container-swap).
- **T2 deep transforms:** three representative multi-level sources — a 4:4:4 SVS
  (CMU-2), a 4:2:0 SVS (scan_620), a JP2K SVS (JP2K-33003-1) — each ×
  {codec sweep (jpeg/jpeg2000/htj2k/avif/webp, +jpegxl with
  `--allow-nonconformant`), factor 2/4/8, crop rect, `--tile-size`,
  `--lossless` on/off, `--tile-order`}.
- **T3 round-trips:** A→B→A for lossless paths (must be pixel-identical) + lossy
  (dims/levels/metadata preserved); cross-format BIF/OME/COG/DICOM/NDPI → svs.
- **Associated editing:** label/macro/thumbnail/overview remove + replace on
  formats that carry them.

### 2. Invariant checker → `findings.jsonl`
`scripts/qa/check_invariants.py`: for each case, pull source+output
`info --json` / `dump-ifds --json` + a JPEG-SOF subsampling probe, and evaluate
rule families keyed by `transform_type`. Each finding:
`{ case_id, family, invariant, severity, expected, actual, repro }`.

Invariant families:
- **Geometry:** container-swap → L0 dims == source; downsample/factor N → L0
  dims ≈ source ÷ N (±2px); crop rect → L0 dims ≈ rect (tile-snapped for lossless).
- **Pyramid:** level count & ratios preserved per honor-source; dims strictly
  decreasing; each step ≈ ½ or a source ratio.
- **Codec:** output codec == requested (`--codec`) or == source (preserved
  single-axis paths); uniform across all levels.
- **Subsampling:** per level, `YCbCrSubSampling` tag == actual JPEG SOF, AND
  consistent across the pyramid (the lossless-crop bug class).
- **Metadata sanity (the emphasis, part a):** `mpp`,`mpp_x`,`mpp_y` > 0 and
  mutually consistent (mpp == mpp_x when isotropic); `magnification` in a
  plausible range; mpp↔magnification not wildly contradictory; level widths
  monotonic; tile sizes > 0; `quality_estimate` ∈ [0,100]; `chroma_subsampling`
  ∈ a known set; associated image dims > 0; `datetime` parseable; `format`
  matches the container written.
- **Metadata consistency (the emphasis, part b):**
  - *cross-command:* `info` level dims/compression == `dump-ifds` IFD
    dims/Compression tag; `info` mpp consistent with `dump-ifds` Resolution tags.
  - *cross-container:* for one source fanned out to every container, the
    metadata block (make/model/software/datetime/mpp/mpp_x/mpp_y/magnification)
    and L0 dims must be identical; flag any field that drifts or is dropped.
- **Round-trip:** lossless A→B→A pixel hash identical; lossy preserves
  dims/levels/metadata.
- **Openability / unexpected error:** `validate` clean; a command that errors
  when `expect_error` is false is itself a finding (and vice-versa: an expected
  rejection that *succeeds* is a finding).

### 3. External oracle → appends conformance findings
Reuse `check-openslide.sh` + `check-bioformats.sh`: open each TIFF-family / DICOM
output in OpenSlide and Bio-Formats. Anything wsitools produced that they
reject or read with different dims/level-count == a **conformance discrepancy**
(the class opentile is lenient about — e.g. the earlier Aperio-reject bugs).
DZI/SZI/IFE/BIF have no OpenSlide/BF reader → skip external (self-validate via
their own tooling where available).

### 4. Report generator → `report.md` (+ `findings.jsonl`)
Aggregate findings, dedup by `(family, invariant, transform_type)`, group by
severity: **silent-wrong-output > conformance > metadata-inconsistency >
metadata-sanity > cosmetic**. Each entry carries the repro command and
expected/actual. Plus a coverage summary (matrix cells run / skipped / errored).

## Data flow

`driver → cases.jsonl + outputs → checker (reads cases + outputs) → findings.jsonl
→ external oracle (appends) → report generator → report.md`

## Error handling & disk guard

- A command erroring unexpectedly (or an expected rejection succeeding) is a
  finding, not a harness crash.
- Missing fixtures skip gracefully (log a coverage gap).
- **Disk:** run under `/Volumes/Ext/tmp`; big sources (NDPI/IFE) behind `--big`;
  delete each case's output immediately after its checks run (keep only
  `cases.jsonl`, `findings.jsonl`, logs) so total disk stays bounded.

## Scope / YAGNI

- One-off rerunnable script, **not** a CI gate (could graduate later).
- No new fixtures — use the existing pool.
- Invariant rules target the known bug classes + obvious sane-value properties +
  the metadata sanity/consistency emphasis; **not** a formal spec of every TIFF tag.
- The harness catalogues; it does **not** auto-file issues. Final step: triage
  `report.md` with the human, dedup against #24 / #25 and the shipped fixes, and
  surface the genuinely new problems.

## Success criteria

- Runs end-to-end under `/Volumes/Ext/tmp` without filling disk.
- Produces `report.md` cataloguing every invariant violation with a repro command.
- Re-finds the *already-known* bug classes as a self-check (jpegxl undecodable →
  round-trip/openability finding; DZI slow-cancel is out of scope of static
  invariants) — demonstrating the harness has teeth — and surfaces new ones.
- The metadata sanity + cross-command/cross-container consistency checks run over
  every `info` field.
