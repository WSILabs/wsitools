# Format-debt & backfill survey

**Date:** 2026-06-13
**Status:** DRAFT for review — a survey, not a verdict. Items are tagged with a
confidence level; some need confirmation before acting. Disagree freely and edit.

**Confidence legend:** `[confirmed]` = verified at the cited file:line · `[likely]`
= strong evidence, not fully traced · `[check]` = needs verification before acting.

**Effort/Impact:** rough T-shirt sizes (S/M/L) for a focused effort.

---

## A. Transform / format gaps (write side)

The core fact: reads are near-universal (opentile reads ~11 formats); transforms
are gated to the **4 writable formats** (svs, ome-tiff, generic-tiff, cog-wsi) via
`downsampleTargetForFormat` (`cmd/wsitools/convert_factor.go:50`). `convert --to`
additionally supports dzi/szi/dicom as one-shot targets.

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| A1 | **DICOM transform dead-end** — no `downsample`, no `crop`, no `--factor` into DICOM. Shared enabler = a *derived-pyramid `source.Source` adapter* feeding `WritePyramid`; unlocks DICOM downsample **and** DICOM re-encode crop together. | `downsample.go:137`, `crop.go:177`, `convert.go:92` | [confirmed] | L | High |
| A2 | **`convert --to svs --factor` rejects non-SVS sources** (asymmetric vs tiff/ome-tiff/cog-wsi, which accept any source). Fix: fall back to `SyntheticAperioDescription` like `downsampleToTIFF` does. | `convert_factor.go:163` | [confirmed] | S | Med |
| A3 | **`convert --to dzi/szi --factor` deferred.** Wire `factor` into the descent generator's L0 dims. | `convert.go:92`, `convert_factor.go:86` | [confirmed] | S–M | Low–Med |
| A4 | **`convert --to dicom` frame-copies JPEG/JP2K only** — AVIF/WebP/JXL/HTJ2K/LZW levels rejected. Needs decode→re-encode-to-JPEG fallback. | `dicomwriter.go:398` | [confirmed] | M | Med |
| A5 | **Read-only formats** (ndpi, leica-scn, bif, philips-tiff, ife, szi) have no format-preserving writer → transforms force a container change (documented in the error text). | `convert_factor.go:50` | [confirmed] | L each | Low |

## B. Codec gaps

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| B1 | **No JPEG2000 encoder** — J2K is decode / tile-copy / DICOM-frame-copy only; never a `--codec` re-encode target. | no `internal/codec/jp2k` | [confirmed] | M–L | Low–Med |
| B2 | **`iris-proprietary`** — `source.Compression` enum slot with no registered decoder → "no decoder" if ever hit (low real risk today). | `internal/source/source.go:99` | [confirmed] | S | Low |
| B3 | **`aperioapp14`** — an `Encoder` that is never registered (orphan). Wire it or delete it. | `internal/codec/aperioapp14/` | [confirmed] | S | Low |
| B4 | **HTJ2K not DICOM-writable** — rejected with a clear error (after the 2026-06-13 TS fix). Real support is future work. | `dicomwriter.go:351` | [confirmed] | M | Low |

## C. Known live bugs / code debt

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| C1 | **DICOM associated-skip leaves a stray 0-byte `.dcm`** — `newWriter(name)` creates the file before the skip check; on skip it `continue`s without removing it. | `dicomwriter.go:88-101` | [likely] | S | Med |
| C2 | **`internal/tiff/edit` rejects SubIFD-pyramid TIFFs** ("Slice 2") — so `associated remove/replace` can't operate on the OME-TIFFs the writer itself now produces. | `internal/tiff/edit/parse.go:49` | [confirmed] | M | Med |
| C3 | **`native.go` force-overrides PixelData VR `OW→OB`** to work around a `suyashkumar/dicom` hardcode — brittle; breaks to grayscale silently if upstream changes. | `internal/dicomwriter/native.go:52` | [confirmed] | S (add guard/test) | Low–Med |
| C4 | **`associated replace` on SVS works only for `label`** (thumbnail/macro/overview rejected — abbreviated-JPEG reconstruction). | `cmd/wsitools/associated.go:251` | [confirmed] | M | Low |
| C5 | **Version-stamped error strings (`"v0.2.0"`) rot;** `downsample.go` "v0.1" holds full L0 in RAM (~18 GB on big slides) — streaming deferred. | `convert_tiff.go:40`, `downsample.go:6` | [confirmed] | S / L | Low / Med |

## D. CI, fixture & test backfill (biggest test debt)

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| D1 | **Integration suite never runs in CI** — `tests/integration/*.go` are `//go:build integration`; `ci.yml` runs `go test ./...` **without** `-tags integration`. So the crop parity oracle, byte-identity matrix, format-preserving matrix, and downsample regression all run **local-only**. | `.github/workflows/ci.yml`, `tests/integration/` | [confirmed firsthand] | S–M | High |
| D2 | **No DICOM CI fixture** — ~15 tests skip; the entire DICOM read+write surface (largest recent feature) has zero CI coverage. (Already filed as wsi-fixtures backfill.) | `.github/fixtures.sha256` | [confirmed] | M (cross-repo) | High |
| D3 | **No JP2K-SVS / OME-TIFF / Leica-SCN / generic-TIFF CI fixtures** → those paths skip in CI. | fixtures.sha256 | [confirmed] | M | Med |
| D4 | **Windows CI job runs no tests** (build+vet only); HTJ2K untested on Windows (`-tags nohtj2k`). | `ci.yml` | [confirmed] | M | Med |
| D5 | **dciodvfy not in CI** → DICOM conformance never auto-validated. | `ci.yml` | [confirmed] | S–M | Med |
| D6 | **CI `-timeout 5m`** vs heavy `-race cmd/wsitools` → false-FAIL risk under load (CLAUDE.md suggests 30m). | `ci.yml` | [check] | S | Low |

## E. Determinism (likely already resolved — verify, then refresh memory)

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| E1 | streamwriter emits tiles in strict strategy order via a bounded reorder buffer → output appears **deterministic**. Contradicts the `pipeline-nondeterminism` memory. | `internal/tiff/streamwriter/reorder.go` | [check] | — | — |
| E2 | morton tile-order test now **passes** — the v0.20 "morton failure" memory is stale. | `internal/tiff/tileorder/` | [likely] | — | — |
| E3 | **No committed byte/pixel-golden harness** (wsitools#2/#3) — determinism is architecturally in place but unguarded by a regression golden. | — | [confirmed] | M | Med |

## F. Read-side notes (mostly fine)

- `dump-ifds` rejects dicom/ife (no TIFF IFDs) — structurally correct.
- `hash --mode pixel` only decodes JPEG/JP2K L0 (`hash.go:130`); `--mode file` rejects DICOM dirs (`hash.go:61`). Minor.

---

## Candidate first moves (effort/risk vs impact)

| Candidate | Effort/Risk | Impact | Notes |
|---|---|---|---|
| **C1** fix DICOM stray-0-byte-file | **Lowest** (S, isolated) | Med | Clean correctness fix, like the TS bug |
| **A2** lift SVS-only on `--to svs --factor` | Low (S, one fn) | Med | Removes a real asymmetry; needs a test |
| **D1** run integration suite in CI | Low–Med | **High** | The crop oracles currently guard nothing in CI |
| **B3** wire-or-delete `aperioapp14` orphan | Lowest (S) | Low | Pure cleanup |
| **A1 / DICOM adapter** | High (L) | **Highest** | Unlocks DICOM downsample + crop |
| **D2** DICOM CI fixture | Med (cross-repo) | High | Unblocks the largest untested surface |

**Suggested order:** lowest-risk correctness first (C1, A2), then the high-impact CI
unlock (D1), then the big DICOM adapter (A1) when ready.
