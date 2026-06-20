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
| A3 | ~~**`convert --to dzi/szi --factor` deferred**~~ **DONE** (branch `feat/a3-dzi-szi-factor`) — `--factor`/`--target-mag` now downsample during dzi/szi conversion (was rejected). The descent feeds the full source L0 region to opentile `ScaledStrips` at the reduced output size (Box kernel when scaling, Nearest at factor 1 — identity path byte-unchanged); `resolveFactor` derives the factor from `--target-mag` via source AppMag. No metadata to scale (dzi/szi manifests carry only dims). Verified: CMU `--factor 2`/`--target-mag 10` → manifest 1110×1483, L0 tile decodes; szi likewise. | `convert_dzi.go`, `convert_szi.go`, `convert_dzi_descent.go` | [confirmed firsthand] | S–M | Low–Med |
| A4a | **HTJ2K frame-copy: DONE** (branch `feat/a4a-htj2k-dicom`). HTJ2K and JPEG XL *are* DICOM transfer syntaxes, so they're **verbatim frame-copied with the correct TransferSyntax UID**, not decode→re-encoded. HTJ2K (`…4.201`/`.203`, Sup 232) now ships: `codecColor` + the dicom-source switch reuse `InspectJP2K` (HTJ2K shares JP2K's SIZ/COD; the walker length-skips the extra CAP marker) → `.201` reversible / `.203` lossy, photometric via `PhotometricJP2K`. **Blocker found + cleared:** `suyashkumar/dicom` v1.1.0's UID dict predates these TSes and has no registration API, so `dicom.Write` refused them; resolved via the **`WSILabs/dicom` fork** (`go.mod` direct dep, `v1.1.0-wsilabs.1`; adds HTJ2K + JXL UIDs; weekly upstream-sync workflow). Verified: full pyramid → 5 HTJ2K instances, opentile read-back pixel-**identical** to source; **dciodvfy 0 errors** on every instance (HTJ2K TS `…4.201` validated). **JPEG XL** is now also wired (source side via opentile-go#41's `CodestreamInspector`, shipped in **opentile-go v0.43.0**; TS `…4.112`) — **UNTESTED** (no JXL source fixture). The DICOM writer's codestream inspection now uses the upstream `CodestreamInspector` for all four codecs, retiring wsitools' hand-rolled `jpegmeta`/`jp2kmeta` parsers. | `dicomwriter.go`, `codecinspect.go`, `WSILabs/dicom` | [confirmed firsthand] | M (per codec) | Med |
| A4b | ~~**`convert --to dicom` rejects AVIF / WebP / LZW / uncompressed levels**~~ **DONE** (branch `feat/a4b-dicom-codec-jpeg`) — these aren't DICOM transfer syntaxes, so per the no-silent-assumptions rule (which the TIFF family already enforces) `convert --to dicom` keeps **erroring** for them *unless* the user passes **`--codec jpeg`** to explicitly opt into a lossy re-encode (the only re-encode target available — no JP2K/HTJ2K encoder, B1). Re-encode via `derivedsource.TranscodeToJPEG` (`transcodeLevel` = F1 `DecodedTile` → JPEG `EncodeStandalone` on demand); `codecColor` inspects the re-encoded frame for photometric. Verified: LZW + uncompressed `590_crop` sources → JPEG DICOM, **dciodvfy 0 errors** (TS `.50`, `YBR_FULL_422`); no-codec still errors (message now suggests `--codec jpeg`); `--codec` other than jpeg rejected. AVIF/WebP untested (no fixture). | `convert_dicom.go`, `derivedsource/transcode.go` | [confirmed firsthand] | M | Med |
| A5 | **Read-only formats** (ndpi, leica-scn, bif, philips-tiff, ife, szi) have no format-preserving writer → transforms force a container change (documented in the error text). | `convert_factor.go:50` | [confirmed] | L each | Low |

## B. Codec gaps

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| B1 | ~~**No JPEG 2000 encoder**~~ **DONE** (branch `feat/b1-jp2k-encoder`) — `internal/codec/jp2k` (OpenJPEG cgo, mirrors htj2k; `q` + `reversible` knobs; raw J2K codestream). `jpeg2000` is now a `--codec` re-encode target. Was parked on **opentile-go#53** (JP2K decoder force-assumed YCbCr); fixed in **opentile-go v0.45.1** (decoder now honors the codestream MCT/colorspace), so the encoder's RGB output round-trips byte-exact. | `internal/codec/jp2k/` | [confirmed firsthand] | M–L | Low–Med |
| B2 | **`iris-proprietary`** — `source.Compression` enum slot with no registered decoder → "no decoder" if ever hit (low real risk today). | `internal/source/source.go:99` | [confirmed] | S | Low |
| B3 | ~~**`aperioapp14`** — an `Encoder` that is never registered (orphan).~~ **DONE** (merge 89f06f3) — deleted as speculative dead code (never a Factory, never imported but by its own test, zero callers; no Aperio-identical re-encode planned). | ~~`internal/codec/aperioapp14/`~~ | [confirmed] | S | Low |
| B4 | ~~**HTJ2K not DICOM-writable**~~ **DONE** (= the HTJ2K half of **A4a**, branch `feat/a4a-htj2k-dicom`) — HTJ2K DICOM sources now frame-copy verbatim with TS `…4.201`/`.203` via the `WSILabs/dicom` fork (which supplies the UIDs upstream's dict lacked). Pixel-identical round-trip + dciodvfy 0-errors verified. | `dicomwriter.go` | [confirmed firsthand] | M | Low |

## C. Known live bugs / code debt

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| C1 | ~~**DICOM associated-skip leaves a stray 0-byte `.dcm`**~~ **DONE** (merge 0ede7fd) — `WritePyramid` now buffers each associated instance and only opens the writer once it has a complete instance to commit; a skip opens no file. Guarded by `TestWritePyramid_SkipAssociatedLeavesNoFile`. | `dicomwriter.go` | [confirmed] | S | Med |
| C2 | ~~**`internal/tiff/edit` rejects SubIFD-pyramid TIFFs ("Slice 2")**~~ **DONE / re-scoped** (branch `fix/c2-generic-tiff-rebuild`). Investigation: the SubIFD framing was a red herring — wsitools' tiff/svs/cog-wsi pyramids are **flat chains, no SubIFDs**. The real gap: a wsitools-produced **generic-TIFF** can't be associated-edited because the splice engine's tail-removal model rejects the streamwriter layout (L0 directory past the associated data → `"past cutoff"`). COG-WSI/OME-TIFF already route to rebuild (COG-WSI **pixel-identical**, OME-TIFF metadata-lossy but fine — we don't recommend wsitools for OME-TIFF). Fix: generic-TIFF now falls back to a faithful **pixel-identical** `rebuildGenericTIFF` (tile-copy verbatim) when splice hits `ErrUnexpectedLayout`; SVS/tail-layout still splice byte-identically. SubIFD support deliberately **not** pursued (no wsitools format uses SubIFD pyramids; rebuild already covers the SubIFD-pyramid formats). | `associated_rebuild_tiff.go`, `associated.go` | [confirmed firsthand] | M | Med |
| C3 | ~~**`native.go` force-overrides PixelData VR `OW→OB`** to work around a `suyashkumar/dicom` hardcode — brittle.~~ **DONE** (merge 8495c31) — moved to the source: `WSILabs/dicom` v1.1.0-wsilabs.2 `pixelDataVR` selects `OB` for 8-bit native PixelData (else `OW`); the brittle override is removed. **NOTE: this is a convention/robustness improvement, NOT a corruption fix.** Verified 2026-06-18: both OB and OW are conformant for 8-bit native in Explicit VR (PS3.5 §8.2 — OB "may also be used"); dciodvfy accepts both (0 errors) and pydicom + opentile decode them to byte-identical RGB — the earlier "OW silently breaks to grayscale" claim did not reproduce. Upstream `suyashkumar/dicom#299` is a *can't-emit-OB limitation*, not a corruption bug (#316 half-addressed it: tag dict lists OW+OB but `NewElement` never chose). Guarded by fork `TestNewElement_PixelDataVR` + `TestNativePixelDataRoundTrip`. | `internal/dicomwriter/native.go` | [confirmed] | S | Low |
| C4 | ~~**`associated replace` on SVS works only for `label`** (thumbnail/macro/overview rejected — abbreviated-JPEG reconstruction).~~ **DONE** (merge 6ae3698) — the survey's "abbreviated-JPEG" reason was wrong: opentile reads a standalone single-strip JPEG fine (`decodeStripStack` fallback). Real blocker is IFD position. `label`/`macro`/`overview` trail the tiled pyramid → splice tail-rewrite works (pyramid byte-identical). `thumbnail` works too when no tiled level follows it (single-level slides); on multi-level slides it can't relocate the tiled levels and errors clearly (`ErrUnexpectedLayout`→`ErrUnsupportedAssoc`). Blanket gate removed; honest error retained. **C4 #2 now also DONE** (branch `feat/svs-thumbnail-ifd1`): multi-level thumbnail `replace`/`remove` works via `rebuildSVS` (re-stream the pyramid through the streamwriter), unblocked by the SVS-thumbnail-at-IFD-1 writer fix — see the `2026-06-18-svs-thumbnail-ifd1-placement` spec/plan. That fix also closes a latent bug: `convert --to svs` of a multi-level slide was silently dropping the thumbnail+overview. | `cmd/wsitools/associated.go` | [confirmed] | S | Low |
| C5 | ~~**Version-stamped error strings (`"v0.2.0"`) rot;** `downsample.go` "v0.1" holds full L0 in RAM~~ **DONE** (branch `chore/c5-stale-version-prose`) — DOCS/STRINGS only. The "full L0 in RAM" claim was already obsolete: downsample streams the source region through the retile engine (`buildPyramid`→`buildEnginePyramid`, bounded memory) since SP2 M3 — only the stale header still said otherwise; rewrote it. Also rewrote `convert.go` help ("v0.6 only COG-WSI" → the unified transform hub) and dropped meaningless version stamps from `convert_tiff.go`/`source.go`/`region.go`/`convert_shared.go`/`downscale.go`. Left real external/spec versions (COG-WSI v0.1 format, wsiwriter v0.6.0 provenance, opentile-go v0.48.0 pin). No behavior change. | `convert_tiff.go`, `downsample.go`, `convert.go`, … | [confirmed firsthand] | S | Low |

## D. CI, fixture & test backfill (biggest test debt)

| # | Item | Where | Conf | Effort | Impact |
|---|---|---|---|---|---|
| D1 | ~~**Integration suite never runs in CI**~~ **DONE** (PR #4, merge eea2da4) — added a `go test (integration)` step (`-tags integration ./tests/integration/...`) to the macOS job; tests skip gracefully on fixtures absent from the 3 CI pulls. CI-verified green (`ok … 10.5s`). | `.github/workflows/ci.yml` | [confirmed firsthand] | S–M | High |
| D2 | ~~**No DICOM CI fixture**~~ **DONE** (PR #5, merge c51cd7b) — wsi-fixtures **v5** adds `dicom.tar`: 3DHISTECH-JP2K/HTJ2K (CC0) + scan_621_grundium_dicom (CC-BY-4.0, attribution). CI pulls it + 16 instance SHAs; the DICOM unit + integration tests now RUN in CI (integration 10.5s→33.8s). | `.github/fixtures.sha256`, `ci.yml` | [confirmed firsthand] | M (cross-repo) | High |
| D3 | **No JP2K-SVS / OME-TIFF / Leica-SCN / generic-TIFF CI fixtures** → those paths skip in CI. **PARTIAL** (merge bf5c81f): wsi-fixtures **v7** added an OME-TIFF (`CMU-1-Small-Region.ome.tiff` → OME-TIFF transform CI coverage) + the `590_crop` ImageScope crops (JP2K-SVS + LZW/uncompressed TIFF). **Multi-level SVS: DONE** — `239551.svs` (owner's own scan, multi-level Aperio SVS, 3 levels + thumbnail/label/overview) landed as **wsi-fixtures v8** (`svs.tar`, CC-BY-4.0; repo PR #5) and wired into CI (`ci.yml` v7→v8 + cache key, `fixtures.sha256` sha `019946db…`); the 5 previously skip-if-absent multi-level tests (`convert --to svs` thumbnail/overview placement + `rebuildSVS` thumbnail/label/overview remove\|replace) now RUN in CI. **Still open: Leica-SCN.** Note: 239551 happens to splice on thumbnail replace, so CI coverage of the `rebuildSVS` *path* specifically would additionally want a CMU-1-style multi-level slide whose L0 data interleaves the thumbnail offsets. | fixtures.sha256 | [confirmed firsthand] | M | Med |
| D4 | **Windows CI job runs no tests** (build+vet only); HTJ2K untested on Windows (`-tags nohtj2k`). | `ci.yml` | [confirmed] | M | Med |
| D5 | ~~**dciodvfy not in CI**~~ **DONE** (branch `ci/d5-dciodvfy`) — the macOS CI job now downloads a pinned `dicom3tools` macexe snapshot (cached) and runs `make dicom-validate`, which converts the JPEG / JPEG 2000 / HTJ2K fixtures **and** the A4b LZW→JPEG re-encode to WSM and validates every emitted instance with `dciodvfy` (exits non-zero only on errors). DICOM conformance is now gated on every push/PR. | `ci.yml`, `Makefile` | [confirmed firsthand] | S–M | Med |
| D6 | ~~**CI `-timeout 5m`** vs heavy `-race cmd/wsitools` → false-FAIL risk under load~~ **DONE** (branch `ci/d6-test-timeout`) — bumped the unit `go test -race` step to `-timeout 30m`, matching the integration step + CLAUDE.md. | `ci.yml` | [confirmed firsthand] | S | Low |
| D7 | **No cross-implementation conformance check vs `wsidicomizer`** — dciodvfy validates our WSM against the IOD in isolation but not against the ecosystem reference. Convert the CC0 `CMU-1-Small-Region.svs` → DICOM with both our `convert --to dicom` (non-`--factor`) and `wsidicomizer`, then **diff the WSM datasets attribute-by-attribute** (DimensionOrganization/TILED_FULL, TotalPixelMatrix dims+origin, per-frame positions, Optical Path, Shared/Per-Frame Functional Groups, PixelSpacing, ImageType, TransferSyntax, SOP/Series structure). Surfaces metadata-completeness gaps dciodvfy stays silent on. Speed/size = secondary data point only (apples-to-oranges: Go+libjpeg-turbo+parallel vs Python). Only the base `--to dicom` path is comparable — wsidicomizer has no downsample/crop analog. Needs a Python env (wsidicomizer + openslide). | new (one-off study) | [confirmed] | M | Med–High |
| D8 | ~~**dciodvfy = dclunie single point of failure for CI**~~ **DONE** — the pinned dicom3tools macexe (BSD; redistribution OK with attribution) is mirrored on **`WSILabs/dicom3tools-mirror`** release `v1` (+ `dicom3tools-COPYRIGHT.txt`); CI's Download-dciodvfy step `gh release download`s from there instead of dclunie.com, so a dclunie move/outage no longer breaks CI. (First attempt mirrored into `wsi-fixtures` `tools-v1`, but that became the repo's *latest* release and broke opentile-go's fixture pull — moved to a dedicated repo, PR #10.) | `ci.yml`, `WSILabs/dicom3tools-mirror` | [confirmed firsthand] | S | Med |
| D9 | ~~**No proactive signal when David Clunie moves/updates dciodvfy**~~ **DONE** (branch `ci/d8-d9-dciodvfy-mirror`) — `dciodvfy-watch.yml` (weekly) reads the pin from `ci.yml`, checks whether dclunie still serves it and whether a newer snapshot exists, and opens a (deduped) tracking issue on either, prompting a mirror refresh + pin bump. | `.github/workflows/dciodvfy-watch.yml` | [confirmed firsthand] | S | Low–Med |

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
