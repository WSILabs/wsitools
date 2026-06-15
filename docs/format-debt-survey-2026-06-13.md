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
| A1 | ~~**DICOM transform dead-end**~~ **DONE** (branch feat/dicom-derived-pyramid-adapter) — `convert --to dicom --factor`, `downsample --factor <dicom>` (format-preserving), and `crop <dicom>` (re-encode + `--lossless`) via `internal/derivedsource` → `WritePyramid`. dciodvfy-validated. | `internal/derivedsource/`, `cmd/wsitools/convert_factor.go`, `cmd/wsitools/crop.go` | [confirmed] | L | High |
| A2 | ~~**`convert --to svs --factor` rejects non-SVS sources**~~ **DONE** (merge 7203c00) — `downsampleToSVS` now resolves MPP/mag from the Aperio doc (SVS) or opentile metadata (any other source) and synthesizes an Aperio description via `SyntheticAperioDescription`, matching its siblings. | `convert_factor.go` | [confirmed] | S | Med |
| A3 | **`convert --to dzi/szi --factor` deferred.** Wire `factor` into the descent generator's L0 dims. | `convert.go:92`, `convert_factor.go:86` | [confirmed] | S–M | Low–Med |
| A4a | **`convert --to dicom` frame-copies JPEG/JP2K only, but HTJ2K and JPEG XL *are* DICOM transfer syntaxes** — so they should be **verbatim frame-copied with the correct TransferSyntax UID** (lossless, like JPEG/JP2K today), NOT decode→re-encoded. HTJ2K = `…1.2.4.201`/`.202`/`.203` (Sup 232); JPEG XL = `…1.2.4.110`/`.111`/`.112` (Sup 235). Needs a TS-UID branch + a codestream inspector to derive photometric/reversibility. **HTJ2K** = cheap wsitools-side `jp2kmeta` `SIZ`/`COD` extension (caller already knows it's HTJ2K from `Compression()`) — no upstream dep; **HTJ2K half = B4**. **JPEG XL** wants opentile-go's header-only codestream probe (filed **opentile-go#41**) rather than a hand-rolled JXL parser; consume it once shipped, map → DICOM in `dicomwriter` (like `PhotometricJP2K`). Caveats: DICOM constrains the encapsulated form (HTJ2K wants the raw `.j2c` codestream, not `.jph`-boxed; JXL its codestream form) — confirm source frames match; and `dciodvfy`/downstream readers may not yet recognize these newer syntaxes, so "conformant" is harder to claim. The F1 `DecodedTile` seam is **not** needed here (no decode). | `dicomwriter.go:398`, `:351` | [confirmed] | M (per codec) | Med |
| A4b | **`convert --to dicom` rejects AVIF / WebP / LZW / uncompressed levels** — these are **not** DICOM transfer syntaxes, so they genuinely need **decode→re-encode** (to JPEG/JP2K/HTJ2K) or store-as-native-uncompressed. *This* is where the F1 `DecodedTile` seam helps (decode half is done; needs re-encode wiring in `internal/derivedsource`, like the JPEG-baseline path A1 already built). | `dicomwriter.go:398` | [confirmed] | M | Med |
| A5 | **Read-only formats** (ndpi, leica-scn, bif, philips-tiff, ife, szi) have no format-preserving writer → transforms force a container change (documented in the error text). | `convert_factor.go:50` | [confirmed] | L each | Low |

## B. Codec gaps

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| B1 | **No JPEG2000 encoder** — J2K is decode / tile-copy / DICOM-frame-copy only; never a `--codec` re-encode target. | no `internal/codec/jp2k` | [confirmed] | M–L | Low–Med |
| B2 | **`iris-proprietary`** — `source.Compression` enum slot with no registered decoder → "no decoder" if ever hit (low real risk today). | `internal/source/source.go:99` | [confirmed] | S | Low |
| B3 | ~~**`aperioapp14`** — an `Encoder` that is never registered (orphan).~~ **DONE** (merge 89f06f3) — deleted as speculative dead code (never a Factory, never imported but by its own test, zero callers; no Aperio-identical re-encode planned). | ~~`internal/codec/aperioapp14/`~~ | [confirmed] | S | Low |
| B4 | **HTJ2K not DICOM-writable** — rejected with a clear error (after the 2026-06-13 TS fix). HTJ2K *is* a DICOM transfer syntax (`…1.2.4.201`/`.202`/`.203`), so the fix is **verbatim frame-copy with that TS UID** (lossless), not re-encode. This is the HTJ2K half of **A4a** — see there for the inspector/codestream-form/conformance caveats. Real support is future work. | `dicomwriter.go:351` | [confirmed] | M | Low |

## C. Known live bugs / code debt

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| C1 | ~~**DICOM associated-skip leaves a stray 0-byte `.dcm`**~~ **DONE** (merge 0ede7fd) — `WritePyramid` now buffers each associated instance and only opens the writer once it has a complete instance to commit; a skip opens no file. Guarded by `TestWritePyramid_SkipAssociatedLeavesNoFile`. | `dicomwriter.go` | [confirmed] | S | Med |
| C2 | **`internal/tiff/edit` rejects SubIFD-pyramid TIFFs** ("Slice 2") — so `associated remove/replace` can't operate on the OME-TIFFs the writer itself now produces. | `internal/tiff/edit/parse.go:49` | [confirmed] | M | Med |
| C3 | **`native.go` force-overrides PixelData VR `OW→OB`** to work around a `suyashkumar/dicom` hardcode — brittle; breaks to grayscale silently if upstream changes. | `internal/dicomwriter/native.go:52` | [confirmed] | S (add guard/test) | Low–Med |
| C4 | **`associated replace` on SVS works only for `label`** (thumbnail/macro/overview rejected — abbreviated-JPEG reconstruction). | `cmd/wsitools/associated.go:251` | [confirmed] | M | Low |
| C5 | **Version-stamped error strings (`"v0.2.0"`) rot;** `downsample.go` "v0.1" holds full L0 in RAM (~18 GB on big slides) — streaming deferred. | `convert_tiff.go:40`, `downsample.go:6` | [confirmed] | S / L | Low / Med |

## D. CI, fixture & test backfill (biggest test debt)

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| D1 | ~~**Integration suite never runs in CI**~~ **DONE** (PR #4, merge eea2da4) — added a `go test (integration)` step (`-tags integration ./tests/integration/...`) to the macOS job; tests skip gracefully on fixtures absent from the 3 CI pulls. CI-verified green (`ok … 10.5s`). | `.github/workflows/ci.yml` | [confirmed firsthand] | S–M | High |
| D2 | ~~**No DICOM CI fixture**~~ **DONE** (PR #5, merge c51cd7b) — wsi-fixtures **v5** adds `dicom.tar`: 3DHISTECH-JP2K/HTJ2K (CC0) + scan_621_grundium_dicom (CC-BY-4.0, attribution). CI pulls it + 16 instance SHAs; the DICOM unit + integration tests now RUN in CI (integration 10.5s→33.8s). | `.github/fixtures.sha256`, `ci.yml` | [confirmed firsthand] | M (cross-repo) | High |
| D3 | **No JP2K-SVS / OME-TIFF / Leica-SCN / generic-TIFF CI fixtures** → those paths skip in CI. **PARTIAL** (merge bf5c81f): wsi-fixtures **v7** added an OME-TIFF (`CMU-1-Small-Region.ome.tiff` → OME-TIFF transform CI coverage) + the `590_crop` ImageScope crops (JP2K-SVS + LZW/uncompressed TIFF). Still open: Leica-SCN. | fixtures.sha256 | [confirmed] | M | Med |
| D4 | **Windows CI job runs no tests** (build+vet only); HTJ2K untested on Windows (`-tags nohtj2k`). | `ci.yml` | [confirmed] | M | Med |
| D5 | **dciodvfy not in CI** → DICOM conformance never auto-validated. | `ci.yml` | [confirmed] | S–M | Med |
| D6 | **CI `-timeout 5m`** vs heavy `-race cmd/wsitools` → false-FAIL risk under load (CLAUDE.md suggests 30m). | `ci.yml` | [check] | S | Low |
| D7 | **No cross-implementation conformance check vs `wsidicomizer`** — dciodvfy validates our WSM against the IOD in isolation but not against the ecosystem reference. Convert the CC0 `CMU-1-Small-Region.svs` → DICOM with both our `convert --to dicom` (non-`--factor`) and `wsidicomizer`, then **diff the WSM datasets attribute-by-attribute** (DimensionOrganization/TILED_FULL, TotalPixelMatrix dims+origin, per-frame positions, Optical Path, Shared/Per-Frame Functional Groups, PixelSpacing, ImageType, TransferSyntax, SOP/Series structure). Surfaces metadata-completeness gaps dciodvfy stays silent on. Speed/size = secondary data point only (apples-to-oranges: Go+libjpeg-turbo+parallel vs Python). Only the base `--to dicom` path is comparable — wsidicomizer has no downsample/crop analog. Needs a Python env (wsidicomizer + openslide). | new (one-off study) | [confirmed] | M | Med–High |

## E. Determinism (likely already resolved — verify, then refresh memory)

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| E1 | streamwriter emits tiles in strict strategy order via a bounded reorder buffer → output appears **deterministic**. Contradicts the `pipeline-nondeterminism` memory. | `internal/tiff/streamwriter/reorder.go` | [check] | — | — |
| E2 | morton tile-order test now **passes** — the v0.20 "morton failure" memory is stale. | `internal/tiff/tileorder/` | [likely] | — | — |
| E3 | **No committed byte/pixel-golden harness** (wsitools#2/#3) — determinism is architecturally in place but unguarded by a regression golden. | — | [confirmed] | M | Med |

## F. Read-side notes (mostly fine)

- `dump-ifds` rejects dicom/ife (no TIFF IFDs) — structurally correct.
- `--mode file` rejects DICOM dirs (`hash.go:61`). Minor.
- ~~`F1` | **`convert` / `hash --mode pixel` can't decode LZW / uncompressed / Deflate *source* tiles**~~ — **RESOLVED** (branch `fix/f1-decode-lzw-source`, commits 9446591 + 3e4ef28). Added `source.Level.DecodedTile` routing through opentile-go's level-decode; `hash --mode pixel`, the `convert` re-encode pipeline (`transcodeLevel`), and the downsample/crop materialize path (`downscale.DecodeReducedTile`) now decode every source compression. Integration coverage: `TestConvertReencodeDecodesLZWAndUncompressedSource`, `TestDownsampleDecodesLZWSource`, `TestDecodedTile_LZWSource`.

---

## Candidate first moves (effort/risk vs impact)

| Candidate | Effort/Risk | Impact | Notes |
|---|---|---|---|
| ~~**C1** fix DICOM stray-0-byte-file~~ | — | — | **DONE** (merge 0ede7fd) |
| ~~**A2** lift SVS-only on `--to svs --factor`~~ | — | — | **DONE** (merge 7203c00) |
| ~~**D1** run integration suite in CI~~ | — | — | **DONE** (PR #4, merge eea2da4) |
| ~~**B3** wire-or-delete `aperioapp14` orphan~~ | — | — | **DONE** (merge 89f06f3, deleted) |
| ~~**A1 / DICOM adapter**~~ | — | — | **DONE** (merge 1e0a103) |
| ~~**D2** DICOM CI fixture~~ | — | — | **DONE** (PR #5, merge c51cd7b; wsi-fixtures v5) |

**Suggested order:** ~~lowest-risk correctness first (C1, A2)~~ → ~~the high-impact
CI unlock (D1)~~ → ~~the `aperioapp14` cleanup (B3)~~ → ~~the big DICOM adapter
(A1)~~ → ~~the DICOM CI fixture (D2)~~ **all the top candidates done.**
