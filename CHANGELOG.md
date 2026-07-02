# Changelog

All notable changes to wsi-tools will be documented here. The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.25.1] - 2026-07-01

### Fixed

- **`YCbCrSubSampling` (tag 530) now emitted on every pyramid level of engine
  transform paths** (crop / downsample / `--factor`), matching genuine Aperio and
  the tiles' actual JPEG SOF subsampling. Previously the tag was absent on these
  paths, so a non-4:2:0 output (e.g. a 4:4:4 source preserved by the subsampling
  fix) made libtiff/OpenSlide auto-correct from the SOF and log a warning on each
  level. The tag is set only for JPEG output, per-level (moved from L0-only in
  streamwriter to `buildLevelEntries`).

### Added

- **Non-RGB8 source guard.** `convert`, `crop`, and `downsample` now error
  clearly when the source's base image isn't 8-bit with 1 (grayscale) or 3 (RGB)
  samples/pixel, instead of silently mis-tagging it — every writer emits an
  8-bit-RGB header, and the verbatim tile-copy path would otherwise pass a
  source's real >8-bit / multichannel tiles through under that header. Non-TIFF
  sources (DICOM / IFE, which opentile normalizes to 8-bit RGB on decode) and
  unreadable IFDs are skipped.

## [0.25.0] - 2026-06-30

### Fixed

- **Honor the source pyramid, codec, and chroma subsampling on transform
  (don't impose defaults).** Several transform paths re-encoded with hardcoded
  defaults instead of matching the source:
  - **crop** now preserves the **source pyramid level ratios + count** (a CMU-2
    `1×/4×/16×/32×` source cropped to `1×/2×/4×/8×/16×/32×/64×` now stays
    `1×/4×/16×/32×`), via a select-octave chain that also handles inconsistent
    ratios (e.g. Grundium's 4×-then-2×).
  - **downsample / `--factor`** now preserve the source level ratios when the
    factor aligns with a source octave (e.g. downsampling a 4×-stepped source by
    4 keeps 4× steps instead of rebuilding a dense full-octave pyramid); a
    non-aligned factor falls back to full octave.
  - **crop / downsample / `--factor`** now preserve the **source codec** instead
    of forcing JPEG (a JPEG2000 SVS stays JPEG2000). These verbs are single-axis
    (use `convert` to change codec); they fall back to JPEG only for source
    codecs with no encoder (LZW / uncompressed / Deflate).
  - **crop / downsample / `--factor` / `convert --codec`** now preserve the
    **source JPEG chroma subsampling** (4:4:4 / 4:2:2 stay; previously forced
    4:2:0). `convert --codec` also emits a matching `YCbCrSubSampling` tag.
  - **Re-encode quality is now a floor, not a fixed default.** When no
    `--quality` is given, the default (85) is treated as a floor: a source whose
    own estimated quality is higher is honored (a Q95 slide re-encodes at ~Q95,
    not 85), so a transform never needlessly degrades a high-quality source. An
    explicit `--quality` still wins. Applies to crop / downsample / `--factor` /
    `convert`.

### Added

- **`extract --force` / `-f`** — overwrite an existing output file (matches the
  other file-writing commands; previously `extract` silently overwrote, now it
  errors unless `--force` is given).
- **Universal progress bars.** Every write path now shows a tile-progress bar,
  not just `downsample` / `crop` / `convert --factor`. Added to all `convert`
  targets (svs/tiff/ome-tiff/cog-wsi tile-copy + transcode, dzi/szi, dicom, ife,
  bif), `transcode`, and `crop --lossless`. The bar is driven by a per-tile hook
  in the retile engine (`retile.Spec.OnTileWritten`) for the engine paths and by
  direct counters in the verbatim/manual-copy loops. It is suppressed by
  `--quiet` and **auto-suppressed when stderr is not a terminal** (piped/CI), so
  it never leaks escape-codes into logs.

### Changed

- **Consistent CLI error/usage output across all commands.** Errors are no longer
  double-printed (cobra's `Error:` line is silenced at the root; `main` prints a
  single `error: <msg>`), and the usage menu is now shown uniformly on
  argument/flag errors (previously `crop` and `downsample` suppressed it) and
  uniformly omitted on runtime errors. Exit codes are unchanged.
- **Harmonized flags on the associated-image edit commands** (`label`/`macro`/
  `overview`/`thumbnail` × `remove`/`replace`) with the rest of the CLI (breaking,
  pre-1.0):
  - Overwrite the output with `-f`/`--force` (was `--overwrite`, now removed) —
    matching `convert`/`crop`/`downsample`/`region`/`extract`.
  - The `replace` aspect-ratio-guard bypass moved from `--force` to
    `--allow-aspect-mismatch`, so `--force` never means two different things.
  - Removed the per-command `-q`/`--quiet` (which shadowed the global flag with a
    different meaning). `--quiet` is now a single global flag that gained a `-q`
    short form and **uniformly suppresses progress *and* success/info output**
    (the `wrote …` / `wsitools: …` lines) across every command.
  - The default output filename suffix is `<stem>_edited<ext>` (was `_relabeled`,
    which was misleading for non-label edits).

## [0.24.2] - 2026-06-28

### Fixed

- **Corrupt edge frames in `convert --to dicom --factor` / `downsample` / `crop`
  to DICOM (the retile-engine DICOM path).** Partial right/bottom edge frames —
  and whole levels smaller than one frame — were encoded at their truncated
  content size, but DICOM TILED_FULL requires every frame to be exactly
  `Rows`×`Columns`; OpenSlide's DICOM reader (and other strict consumers) rejected
  them (`Dimensional mismatch reading JPEG, expected 256x256, got …`). This is the
  same class as the v0.24.1 TIFF edge-tile fix, which only covered the TIFF/IFE
  encoder; `dicomFrameEncoder` now edge-replicates partial frames up to the full
  frame size as well. The verbatim DICOM-source frame-copy path was never
  affected. Added a cross-tool manual QA harness under `scripts/qa/` (matrix
  generator + OpenSlide/Bio-Formats auto-validators + viewer checklist) that
  surfaced this.

## [0.24.1] - 2026-06-28

### Fixed

- **Corrupt edge tiles in re-encoded SVS/TIFF/OME-TIFF/COG-WSI/IFE output (TIFF
  conformance).** The retile engine encoded partial right/bottom edge tiles — and
  whole pyramid levels smaller than one tile — at their truncated content size
  instead of the full declared `TileWidth×TileLength`. TIFF requires uniform
  full-size tiles (pixels beyond `ImageWidth/Length` are padding, ignored by
  readers); OpenSlide and ImageScope reject a sub-full-size JPEG tile as a
  "dimensional mismatch", rendering black/garbled edges (opentile is lenient and
  masked it). Edge tiles are now padded up to the full tile size (last row/column
  edge-replicated to avoid JPEG bleed) before encoding, across every engine-backed
  re-encode path (`convert --to svs|tiff|ome-tiff|cog-wsi`, `--factor`,
  `downsample`, `crop`, `--to ife`). DZI/SZI legitimately use partial edge tiles
  and are unaffected. Verified pixel-faithful against OpenSlide on a Ventana BIF
  source.
- **A pyramid level dropped in ImageScope for `--to svs` from a source without a
  thumbnail (BIF, IFE/Iris, …).** Genuine Aperio SVS always carries the thumbnail
  as the second IFD; ImageScope classifies IFD 1 positionally as the thumbnail, so
  when no thumbnail was emitted the first reduced pyramid level landed at IFD 1 and
  ImageScope dropped it (e.g. a 1×/4×/16×/64× source showed only 1×/16×/64×).
  `convert --to svs` now synthesizes an Aperio thumbnail (longest edge 1024px,
  baseline JPEG, rendered from L0) at IFD 1 when the source has none, matching the
  genuine Aperio layout; sources that already carry a thumbnail are unchanged.

## [0.24.0] - 2026-06-27

### Added

- **`convert --tile-size N`** — output tile-size control for the re-encode
  targets (`svs`/`tiff`/`ome-tiff`/`cog-wsi`, `dzi`/`szi`, `dicom`). When unset
  (`0`), the output tile size **matches the source** — fixing the previously
  hardcoded 256 in `--factor` and `downsample`; pass `N` to override. A
  `--tile-size` equal to the source tiling is a no-op (a lossless tile-copy stays
  a copy); a `--tile-size` that *differs* from the source forces a re-encode,
  whose codec defaults to the **source's own** codec (erroring if that codec has
  no encoder — pass `--codec`). Per-target notes:
  - `--to dicom`: re-tiles **only when a size differing from the source is
    requested** (the DICOM `Rows`/`Columns` frame tags then follow); otherwise
    the existing verbatim frame-copy is unchanged.
  - `--to bif` (verbatim Ventana/Roche DP-200 layout) and `--to ife` (Iris IFE
    mandates fixed 256×256 tiles in v1.0): a `--tile-size` they can't honor is
    **rejected with a clear error** rather than silently ignored. `--to ife`
    always emits 256px tiles.

### Changed

- **Removed `--dzi-tile-size`** — replaced by the unified `--tile-size`. DZI/SZI
  now default to the source tile size when `--tile-size` is unset (was a fixed
  256). Breaking CLI change (pre-1.0).
- **Removed the `--jobs` alias** of `--workers` across `convert`/`crop`/
  `downsample`/`transcode`; `--workers` is the single canonical worker flag
  (`downsample`'s primary flips to `--workers`, default `NumCPU`). Breaking CLI
  change (pre-1.0).

### Fixed

- **Wrong colours in Aperio-ecosystem viewers (ImageScope, OpenSlide) for
  re-encoded / tile-copied JPEG output.** A TIFF wrapping YCbCr JPEG tiles was
  tagged `PhotometricInterpretation=RGB(2)`, so spec-following readers skipped the
  YCbCr→RGB conversion and rendered all colours wrong (opentile is lenient and
  decoded by the JPEG's own markers, which masked the bug). The photometric is now
  derived from the actual tile colour model: re-encoded JPEG tiles (always YCbCr)
  are tagged `YCbCr(6)`, and verbatim tile-copies are tagged from the source JPEG's
  framing — JFIF/standard `Y,Cb,Cr` component IDs → `YCbCr(6)`, Aperio bare-JPEG
  framing → `RGB(2)`. Spans every re-encode/copy path (`convert --to
  svs|tiff|ome-tiff|cog-wsi`, `--factor`, `downsample`, `crop`, associated-image
  rebuild). YCbCrSubSampling now accompanies the YCbCr photometric on all
  containers. Verified pixel-faithful against OpenSlide for SVS/IFE/BIF sources.

## [0.23.0] - 2026-06-27

### Added

- **Prebuilt binaries** — every `vX.Y.Z` release now ships statically-linked,
  download-and-run `wsitools` binaries for **5 targets** (Linux amd64/arm64,
  macOS arm64/amd64, Windows amd64), each bundling all six codecs with no system
  libraries to install. Built on native runners (cgo rules out cross-compiling
  the host) with the codec C libraries sourced **statically via vcpkg**
  (`vcpkg.json` + `.github/vcpkg-triplets/`), a shared `build-static` composite
  action, and a 5-target matrix in `release.yml` (notes → build → `SHA256SUMS`);
  a `release-canary` workflow guards the static build on release-relevant PRs.
  Linux is glibc *mostly-static* (runs on all mainstream distros); Windows is
  fully static; Intel-Mac binaries are **cross-compiled on Apple Silicon** (the
  Intel runner pool is being sunset). **macOS binaries are sign+notarize-ready
  but ship unsigned until Developer-ID signing secrets are provisioned** — see
  `docs/RELEASING.md`. As part of this, `internal/codec/htj2k` now discovers
  OpenJPH via `pkg-config` instead of hardcoded Homebrew paths (also fixes
  Intel-Mac and clean-environment source builds).
- **`convert --to bif`** (experimental) — write a Ventana/Roche **DP 200-shaped
  BIF** from any source (`internal/tiff/bifwriter` + `cmd/wsitools/convert_bif.go`).
  Full pyramid as row-major `level=N` IFDs (verbatim JPEG **tile-copy** for JPEG
  sources; **`--codec jpeg`** decodes+re-encodes non-JPEG sources to self-contained
  JPEG tiles), a whole-slide overview (the source's `overview`/`macro` carried
  through + oriented to portrait when present, else synthesized from the tissue
  at the DP 200 canonical 1251×3685), and synthesized `<iScan>`/`<EncodeInfo>`
  metadata (scanner model, MPP, magnification). **Renders correctly in
  bio-formats / QuPath.** Key finding: real DP 200 stores tiles **row-major**
  (per the file's own `<Frame>` nodes), not serpentine — the whitepaper's
  "serpentine" is the `TileJointInfo` stitch-graph numbering only; opentile-go's
  reader had conflated the two (the BIF read bug we filed, **fixed in
  opentile-go v0.45.3 / #57** — it now reads our output pixel-identically).
  Re-encode runs on a worker pool
  (`--workers`). Limitations: single-AOI, no Z; no separate label/thumbnail or
  probability map carried; no `--factor`/`--target-mag`.
- **`wsitools validate <file>`** — new read-side command that checks a slide's
  structural conformance using opentile-go v0.45.1's `Validate` API
  (`ValidateFile` → `Report` of findings with severities and check codes).
  Human or `--json` output; three-way exit code (`0` valid / `2` invalid / `1`
  operational error); `--strict` promotes warnings to failures. Calls
  `ValidateFile` directly (not `internal/source`), so a malformed file is
  reported as an `unopenable` finding rather than erroring out.

- **JPEG 2000 is now a `--codec` re-encode target** (survey B1) — new
  `internal/codec/jp2k` OpenJPEG-backed encoder (raw J2K codestream). Use
  `convert --to {tiff,cog-wsi,...} --codec jpeg2000`; lossless via the knob
  `--quality reversible=true` (the `--quality` flag now accepts comma-separated
  `k=v` knobs, e.g. `q=85,reversible=true`, in addition to a bare integer). The
  lossless path round-trips **pixel-identical** (verified end-to-end after the
  opentile-go v0.45.1 JP2K-decoder fix below). wsitools now has encoders for
  jpeg / jpeg2000 / htj2k / jpegxl / avif / webp.

- **`convert --to dicom --codec jpeg`** (survey A4b) — explicit opt-in to
  **re-encode** a source whose tiles are not a DICOM transfer syntax (LZW /
  uncompressed / Deflate / AVIF / WebP) to JPEG-baseline, mirroring the TIFF
  family's `--codec`. Without `--codec`, such a source is still **rejected** (no
  silent codec assumptions; the error now suggests `--codec jpeg`); `--codec`
  values other than `jpeg` are rejected (no JPEG 2000 / HTJ2K encoder exists —
  survey B1). Each tile is decoded via opentile-go's level-decode and re-encoded
  on demand (`derivedsource.TranscodeToJPEG`); `codecColor` inspects the
  re-encoded frame so the DICOM photometric matches. Verified: LZW + uncompressed
  `590_crop` ImageScope crops → JPEG DICOM with `dciodvfy` **0 errors**
  (`YBR_FULL_422`). A lossy-re-encode warning is logged.

- **`convert --to dicom` now frame-copies HTJ2K sources** (survey A4a / B4). A
  DICOM source whose frames are **High-Throughput JPEG 2000** is re-emitted
  verbatim with the matching transfer syntax — `…1.2.4.201` (reversible/lossless)
  or `…1.2.4.203` (lossy) — instead of being rejected. HTJ2K shares JPEG 2000's
  SIZ/COD codestream markers (the extra CAP marker is length-skipped), so the
  existing `InspectJP2K` + `PhotometricJP2K` derive components / reversibility /
  photometric unchanged. Verified: a full `3DHISTECH-HTJ2K` pyramid converts to
  5 HTJ2K instances that read back **pixel-identical** to the source, and
  `dciodvfy` reports **0 errors** on every instance (it validates the HTJ2K
  transfer syntax `…4.201`).

- **`convert --to dicom` JPEG XL source frame-copy** (survey A4a) — a source
  whose frames are **JPEG XL** is frame-copied with the general JPEG XL transfer
  syntax `…1.2.4.112`. JPEG XL exposes no header-only reversibility flag, so it
  is marked lossy conservatively. **Untested** — no JPEG XL source fixture exists
  yet; ships behind the same inspector path as the other codecs.

### Changed

- **DICOM writer codestream inspection now uses opentile-go's
  `decoder.CodestreamInspector`** (header-only, no full decode) instead of
  wsitools' hand-rolled `jpegmeta`/`jp2kmeta` marker parsers, which are deleted.
  The inspector parses JPEG SOF / J2K SIZ+COD / HTJ2K / JXL headers and exposes
  `ColorEncoding` + `ChromaSubsampling` + `Lossless`; wsitools keeps only the
  codec→DICOM mapping (`codecinspect.go`). Behavior is unchanged for JPEG
  (incl. Aperio APP14 raw-RGB → `RGB`, and 4:2:2 → `YBR_FULL_422` vs 4:4:4 →
  `YBR_FULL`), JPEG 2000, and HTJ2K — verified by pixel-identical round-trips and
  `dciodvfy` 0-errors across CMU (JPEG + native label + JPEG associated), JP2K,
  and HTJ2K.

- **DICOM writer now depends on the `WSILabs/dicom` fork** of
  `github.com/suyashkumar/dicom` (`v1.1.0-wsilabs.1`, via a direct `require`).
  Upstream v1.1.0's transfer-syntax UID dictionary predates HTJ2K (Sup 232) and
  JPEG XL (Sup 235) and exposes no registration API, so `dicom.Write` refused to
  emit those syntaxes (`UID '…' not found in dictionary`) even though the body
  encoding is identical Explicit VR LE. The fork adds the HTJ2K (`…4.201–.205`)
  and JPEG XL (`…4.110/.111/.112`) UIDs and rebrands the module path so it is
  consumable directly; a weekly GitHub Action tracks upstream and flags when the
  UIDs land upstream so the fork can be retired. Read-side DICOM (via opentile-go)
  still uses upstream `suyashkumar/dicom` as an indirect dependency.

- **opentile-go v0.41.1 → v0.45.3.** Picks up: `decoder.CodestreamInspector`
  (`Inspect(src) → CodestreamInfo{Components, BitDepth, Lossless, ColorEncoding,
  ChromaSubsampling, Boxed}`, opentile-go #41, v0.43.0 — now consumed by the
  DICOM writer); a structural WSI **`Validate` API** (`opentile.ValidateFile`/
  `Validate`/`(*Slide).Validate` → findings-`Report`, v0.45.0); the
  **JPEG 2000 decoder colorspace fix** (opentile-go #53, v0.45.1) — the decoder
  now decides RGB-vs-YCbCr from the codestream (MCT/colorspace) instead of
  force-assuming YCbCr, so wsitools' new JP2K-encoder output round-trips correct
  colors (Aperio 33003 decoding unchanged); and the **`Validate` slide-level MPP
  fix** (opentile-go #55, v0.45.2) — `checkLevelGeometry` now accepts the
  slide-level `Metadata.MPP` and rolls a genuinely-missing MPP into a single
  whole-file finding, instead of false-positiving one `missing-metadata` warning
  per level on ndpi/leica-scn/dicom/generic-tiff/ife/szi (which carry MPP at the
  slide level, not per `Level`). Found via a `wsitools validate` corpus sweep;
  and the **BIF row-major tile-ordering fix** (opentile-go #57, v0.45.3) — the
  reader no longer hardcodes a serpentine `TILE_OFFSETS` remap (real Roche DP 200
  is row-major, per the `<Frame>` nodes), so opentile now reads genuine Roche
  slides AND `convert --to bif` output **correctly** (verified pixel-identical
  round-trip). Filed from the BIF writer work (also #58 docs, #59 test oracle).

- **opentile-go v0.41.0 → v0.41.1** (no API change) — picks up two decode fixes
  for **Aperio ImageScope exports** (which re-encode the pyramid + associated
  images in non-Aperio codecs): tiled **LZW / uncompressed / Deflate** levels now
  decode (opentile-go #28), and **non-JPEG associated images** (e.g. an
  uncompressed thumbnail) now decode (#29). Verified through wsitools `region`
  (tiled-LZW L0) and `extract` (uncompressed thumbnail). CI fixtures bumped to
  **wsi-fixtures v7**, adding the `590_crop` ImageScope export crops (which guard
  the above) and a CC0 **OME-TIFF** fixture (`CMU-1-Small-Region.ome.tiff`) that
  gives the OME-TIFF transform paths CI coverage.

### Removed

- **`internal/codec/aperioapp14`** — the v0.17-era keep-around encoder that
  reproduced Aperio's APP14 + raw-RGB JPEG framing. It was never registered as a
  `codec.Factory`, never imported by anything but its own test, and had zero
  callers; no planned feature needs Aperio-identical re-encode (lossless paths
  copy source tile bodies verbatim, preserving whatever framing the source
  used). Recoverable from git history if a byte-faithful-Aperio path is ever
  wanted.

### Fixed

- **`convert` (re-encode), `downsample`, `crop`, and `hash --mode pixel` could
  not decode LZW / uncompressed / Deflate *source* tiles** (Survey F1). Those
  paths picked a standalone codec by source compression, which covers only
  JPEG / JPEG 2000 — so re-encoding, downsampling, cropping, or pixel-hashing an
  Aperio **ImageScope export** (tiled LZW / uncompressed / Deflate) errored
  `no decoder for source compression lzw|none`, even though `region`/`extract`
  (which decode through opentile-go directly) worked. Added a
  `source.Level.DecodedTile` seam that routes through opentile-go's level-decode
  — handling every source compression with the TIFF tile-dims + predictor
  context a codec-of-bytes decode lacks — and wired `hash --mode pixel`, the
  `convert` re-encode pipeline (`transcodeLevel`), and the downsample/crop
  materialize path (`downscale.DecodeReducedTile`, which keeps codec-domain
  scaled decode where supported, else full-decode + box-halve) through it.
  Decode stays parallel across the worker pool (concurrency-safe `ReadAt` tile
  reads + a mutex/channel decoder pool). Builds on the opentile-go v0.41.1 bump
  above.

- **`convert --to svs --factor` rejected non-SVS sources.** It required a
  parseable Aperio `ImageDescription`, an asymmetry vs the tiff/ome-tiff/cog-wsi
  targets (which accept any source). It now resolves MPP/magnification from the
  Aperio doc when the source is SVS, else from opentile metadata, and synthesizes
  an Aperio-shaped description via `SyntheticAperioDescription` for non-SVS
  sources — so e.g. `convert --to svs --factor 2 <cog-wsi>` works and re-detects
  as SVS. (Survey A2.)

- **`convert --to dicom` left a stray 0-byte `.dcm` when an associated image was
  skipped.** `WritePyramid` created the output file before the skip check, so an
  associated image that couldn't be emitted (bytes/codec/decode failure) left an
  empty `<type>.dcm` in the series. The writer now buffers each associated
  instance and only opens the output once it has a complete instance to commit;
  a skip opens no file. (Survey C1.)

- **`convert --to {cog-wsi, svs, tiff, ome-tiff}` (and `--factor`) corrupted
  associated images** (wsitools#1). The associated-image passthrough copied
  opentile-go's `AssociatedImage.Bytes()` verbatim into a single standalone
  strip, which drops the source's **Predictor (317)** tag for LZW labels and the
  **JPEGTables (347)** for abbreviated-JPEG thumbnails — so a conforming reader
  decoded an LZW label to garbage (or truncated) and an abbreviated thumbnail to
  a broken JPEG. Associated images are now copied **byte-faithfully** via
  opentile-go **v0.39.0** `Slide.AssociatedSourceOf` (#22): the source IFD's
  verbatim strips plus the exact tags (Compression / Predictor / JPEGTables /
  RowsPerStrip / Photometric), emitted through new multi-strip support in
  `cogwsiwriter` and `streamwriter`. Synthesized/tiled associated images (which
  have no faithful single-IFD source form) fall back to decode → re-encode. The
  `associated replace`/`remove` and DICOM paths were unaffected. opentile-go
  bumped v0.38.1 → v0.39.0. (The OME-TIFF *reader* in opentile-go still can't
  decode LZW / multi-strip associated images on read-back — a separate
  limitation; the bytes written are faithful.)

### Added

- **DICOM is now a transform target.** `convert --to dicom --factor N`,
  `downsample --factor N <dicom>` (format-preserving), and `crop <dicom>`
  (re-encode, plus `--lossless` verbatim-L0 frame-copy) emit a reduced/cropped
  DICOM-WSM pyramid. Implemented via a new `internal/derivedsource` adapter that
  presents a derived pyramid as a `source.Source` to the existing
  `dicomwriter.WritePyramid`; re-encoded levels are JPEG-baseline (no JP2K/HTJ2K
  encoder yet). Output `-o` is a pyramid **directory** (as `convert --to dicom`
  already is). The tile re-encode runs on a **worker pool** (`--jobs`), and a
  `crop`'s **thumbnail is regenerated** from the crop region rather than carrying
  the stale whole-slide thumbnail. dciodvfy-validated. (Survey A1.)

- **CI now covers the DICOM read + write/transform surface.** The
  `-tags integration` end-to-end suite runs in CI (previously local-only), and
  `wsilabs/wsi-fixtures` **v5** adds `dicom.tar` (3DHISTECH JP2K/HTJ2K under CC0,
  scan_621 Grundium under CC-BY-4.0) so the DICOM tests execute instead of
  skipping. (Survey D1, D2.)

- Experimental `convert --to dicom` — **DICOM-WSI writer, Phase 0 spike.** Emits
  one conformant DICOM VL Whole Slide Microscopy (WSM) **VOLUME** instance from a
  **DICOM source**: the source's compressed JPEG frames are copied **verbatim**
  (byte-identical, no decode/re-encode) and re-encapsulated as TILED_FULL
  multi-frame PixelData. One pyramid level per invocation (`--level`, default
  `0` = full resolution). Validated with David Clunie's `dciodvfy`
  (dicom3tools) — **0 errors** on both full-resolution (L0, 65536², 16384
  frames) and reduced (L2) instances — and round-trips through opentile-go
  (read back as `Format: dicom`, frames byte-identical to source). New
  `make dicom-validate` target emits + validates against the Grundium fixture
  (needs `dciodvfy` on PATH, `DCIODVFY=` override; gated on `WSI_TOOLS_TESTDIR`).
  Built on `github.com/suyashkumar/dicom` (pure Go, promoted to a direct dep).
  **Phase 1, first slice:** `convert --to dicom` now also accepts a **non-DICOM
  source** (SVS etc.) and emits ONE conformant WSM VOLUME instance from a single
  pyramid level (`--level`, default `0`). The source level's JPEG-baseline tiles
  are copied **verbatim** (no decode/re-encode); non-JPEG codecs (JPEG 2000 etc.)
  error clearly (`Phase 1 supports JPEG-baseline tile-copy only`). The writer
  inspects the first tile's JPEG markers (Adobe APP14 ColorTransform + chroma
  subsampling) and sets `PhotometricInterpretation` to match — **RGB** for the
  classic Aperio APP14 raw-RGB variant (which CMU-1-Small-Region.svs uses),
  `YBR_FULL_422` for subsampled YCbCr, `YBR_FULL` for 4:4:4 YCbCr. The source's
  embedded ICC profile is carried through when present, or a canonical **sRGB**
  profile is synthesized when absent (CMU SVS has none), satisfying DICOM's
  Type 1C `ICCProfile` requirement for color. Validated two ways: `dciodvfy`
  reports **0 errors** on the RGB-photometric SVS→DICOM instance (only a benign
  Study-ID DICOMDIR warning), and a **pixel round-trip** test (decode the emitted
  DICOM honoring its photometric, compare to the source's decode) confirms
  byte-identical RGB — the colorspace is correct, not merely structurally valid.
  `make dicom-validate` now exercises both the DICOM→DICOM and SVS→DICOM paths.
  DICOM→DICOM output is unchanged (byte-identical).
  **Phase 1, second slice:** `convert --to dicom` now emits the **full resolution
  pyramid by default**. `convert --to dicom -o <dir> <input>` writes one WSM
  VOLUME instance **per source level** as `<dir>/level-<n>.dcm` (n=0 = full
  resolution) — a classic multi-instance Series: all instances share Study /
  Series / FrameOfReference / DimensionOrganization UIDs, each carries its own
  SOPInstanceUID and `InstanceNumber = level+1` (no Pyramid UID, matching the
  Grundium golden). `--level N` selects the single-instance path (unchanged).
  Directory output is **atomic** — the pyramid is built in a temp sibling
  directory and renamed into place on success; any failure removes the temp dir,
  never leaving a partial pyramid. Per-level **spatial-metadata fix**:
  `PixelSpacing` now scales by each level's downsample factor and
  `ImagedVolumeWidth/Height` is the constant L0-derived physical extent, so the
  pyramid levels co-register (this also fixes a latent bug in the single-level
  non-L0 path, which previously emitted base-MPP spacing and a shrunken extent).
  `dciodvfy` reports **0 errors** on **every** level of the Grundium full pyramid
  (L0/L1/L2) and on the SVS instance; `make dicom-validate` now emits + validates
  the whole pyramid.
  **Phase 1, third slice:** `convert --to dicom` now also accepts **JPEG 2000**
  sources (previously JPEG-baseline only; other codecs errored). The source
  level's raw J2K codestream is tile-copied **verbatim** (no decode/re-encode),
  on both the single-instance and full-pyramid paths. The transfer syntax is
  **reversibility-driven**: a reversible/lossless source emits `1.2.840.10008.1.2.4.90`
  (JPEG 2000 Lossless Only) with `LossyImageCompression "00"` (ratio/method
  omitted); an irreversible/lossy source emits `1.2.840.10008.1.2.4.91`
  (JPEG 2000) with `LossyImageCompression "01"` + method `ISO_15444_1`. A new
  `jp2kmeta` parser reads the codestream's SIZ/COD markers to derive
  `PhotometricInterpretation` — **RGB** (no multi-component transform),
  `YBR_ICT` / `YBR_RCT` (irreversible / reversible MCT), or **MONOCHROME2**
  (1 component). `dciodvfy` reports **0 errors** on **every** level of the
  JP2K-33003-1.svs full pyramid (RGB / `.91` / lossy) and a pixel round-trip
  confirms the RGB path is colour-correct. Also fixes a general
  **DS-VR PixelSpacing length** bug (new `formatDS` helper): non-power-of-2
  level ratios combined with a non-round MPP previously produced 21-char
  `PixelSpacing` values that exceeded DICOM's DS VR 16-char limit and `dciodvfy`
  rejected; values are now formatted to fit (Grundium/SVS output unchanged, their
  values were already short). **Honestly stated limitation:** the `YBR_ICT` /
  `YBR_RCT` (MCT=1) and `.90` / lossless branches are **unit-tested but not yet
  e2e-validated** (no MCT=1 / lossless JP2K fixture); >8-bit JP2K and `.jp2`-boxed
  inputs remain out of scope.
  **Phase 2 — associated images:** `convert --to dicom -o <dir> <input>`
  (full-pyramid mode) now also emits the slide's **associated images**
  (label/overview/thumbnail, and macro→overview) as **single-frame WSM
  instances in the same Series** — one per image at `<dir>/<type>.dcm` (e.g.
  `label.dcm`, `overview.dcm`, `thumbnail.dcm`), sharing the
  Study/Series/FrameOfReference UIDs and continuing `InstanceNumber` after the
  levels. Each image's whole frame is tile-copied **verbatim** (JPEG or JPEG
  2000); `ImageType[2]` is set per type (`LABEL`/`OVERVIEW`/`THUMBNAIL`) and
  `SpecimenLabelInImage` per type (YES for label/overview, NO for thumbnail),
  with the **SlideLabel module** emitted (empty/anonymous `LabelText` +
  `BarcodeValue`) for label/overview. **Default-on**, skipped by
  `--no-associated`; `--level N` (single-instance) mode emits no associated
  images. Associated images whose codec is neither JPEG nor JPEG 2000 (e.g. an
  **LZW label**, as in CMU-1-Small-Region.svs) are **skipped with a logged
  warning** — no file left behind, the pyramid still completes. `dciodvfy`
  reports **0 errors** including the associated instances; `make dicom-validate`
  now validates every `<dir>/*.dcm`. Still out of scope: the golden's rotated
  label `ImageOrientationSlide` / faithful label `PixelSpacing`, and HTJ2K /
  16-bit / `.jp2`-boxed associated images.

  **Phase 2 follow-on — associated transcode:** associated images whose codec is
  **not** a DICOM transfer syntax (e.g. the **LZW label** on every Aperio SVS —
  the clinically important barcode) are no longer skipped. They are now
  **decoded and stored as uncompressed native DICOM** instances (Explicit VR
  Little Endian, VR `OB`, `LossyImageCompression "00"` — lossless, so the
  barcode stays scannable); JPEG / JPEG 2000 associated images still tile-copy
  verbatim-encapsulated. Decoding is delegated to **opentile-go v0.38.1**
  (`AssociatedImage.Decode`, GH opentile-go#20), so wsitools' former
  TIFF-reparse workaround in `extract` is deleted. The emitted `label.dcm`
  pixel-round-trips byte-identically to the source label decode and passes
  `dciodvfy` (0 errors); `make dicom-validate` now emits the full SVS pyramid so
  the native label is validated too. (Two bugs surfaced and were fixed: the
  writer must use VR `OB` not `OW` for 8-bit native pixel data — `OW` makes a
  conformant reader interpret it as 16-bit and collapse RGB to grayscale; and
  opentile-go#21 fixed the reader's native-RGB associated decode, where an
  even-length pad byte had broken `SamplesPerPixel` inference.)

## [0.22.0] — 2026-06-07

### Added

- Associated-image editing extended to **COG-WSI** — `label/macro/thumbnail/overview remove` and `replace` (all types) via `cogwsiwriter` re-finalize. Pyramid tile bytes are copied verbatim (no re-encode); all other associated images and MPP/magnification/ICC are preserved — only the target image changes.
- Associated-image editing extended to **OME-TIFF** (remove + replace, all types) via `streamwriter` rebuild — **lossy**: rebuilds the file and regenerates a minimal OME-XML (instrument/acquisition/channel/vendor `OriginalMetadata` and pyramid-resolution annotations not preserved; pyramid pixels, geometry/MPP/magnification, ICC, and the other associated images are). Always-on runtime warning on every OME-TIFF edit. Associated replacements are **JPEG-only** (opentile-go's OME-TIFF reader can only decode JPEG/uncompressed associated images; LZW/Deflate would be unreadable). wsitools' OME-TIFF support is rudimentary — use [Bio-Formats](https://www.openmicroscopy.org/bio-formats/) for serious OME-TIFF work; see [docs/ome-tiff-limitations.md](docs/ome-tiff-limitations.md). **This completes associated-image editing across all four editable formats: SVS, generic-TIFF, COG-WSI, and OME-TIFF.**

### Changed

- opentile-go bumped to **v0.37.0** — JPEG 2000 decode is now optional. Build
  with `-tags nojp2k` to drop the OpenJPEG dependency; **libjpeg-turbo is now the
  only required codec library**. See [docs/INSTALL.md](docs/INSTALL.md) for a
  JPEG-only minimal install. JPEG 2000 is a legacy Aperio codec, no longer
  marketed as required.
- CI: a `Release` workflow now auto-creates the GitHub Release on `v*` tag push,
  with notes pulled from the matching `CHANGELOG.md` section and the title from
  the annotated tag's subject (notes-only; no binary artifacts).

## [0.21.0] — 2026-06-06

### Added

- **Associated-image editing** — four command groups, each with `remove` and
  `replace` subcommands: `label`, `macro`, `thumbnail`, `overview`. Supported
  on SVS and generic-TIFF.
  - `remove` strips the target associated image entirely (label PHI removal);
    works for **every** type on both formats.
  - `replace` swaps it with a new image file. Supported for **all** types on
    generic-TIFF; on **SVS**, only **label** replace is supported today
    (opentile-go reads Aperio thumbnail/macro/overview as abbreviated JPEG, so
    re-encoding those is a Slice-2 item — SVS non-label `replace` errors
    clearly). Replacements carry the reader's classification markers
    (SVS `NewSubfileType=9` for macro/overview; `WSIImageType` private tag for
    generic-TIFF) so a replaced image is read back as the intended type.
  - Pyramid tile bytes are **copied verbatim** (no decode, no re-encode); only
    the tail IFD is rewritten via a prefix-copy + tail-re-emit splice. Output
    contains no recoverable label PHI.
  - Output defaults to `<stem>_relabeled<ext>` next to the input (auto-numbered
    if the path exists); `-o/--output` for an explicit path; `--in-place` for
    atomic overwrite (temp + fsync + rename).
  - `label replace` defaults to **LZW + Predictor 2** (lossless, barcode-safe);
    `macro`/`thumbnail`/`overview replace` default to **JPEG**.
    `--compression {jpeg,lzw,deflate,none}` overrides.
  - `--resize fit|stretch|none` (default `fit`), `--bg RRGGBB` letterbox fill
    (default `F5F5E6`), `--force` to skip the aspect guard,
    `--label-dims WxH` to override target dimensions.
  - OME-TIFF and COG-WSI: planned (Slice 2 — SubIFD-range-aware splice +
    OME-XML sync). Other formats (DICOM, NDPI, Philips, BIF, IFE, Leica)
    are rejected with a pointer to `convert`.
- opentile-go bumped to **v0.36.0** — adds `AssociatedIFDOffset` used by the
  splice engine to locate and excise associated-image IFDs without walking the
  full IFD chain.

### Dependencies

- New: `github.com/hhrutter/lzw` — pure-Go LZW encoder used for
  lossless label replacement (LZW + Predictor 2).

- `convert --factor N` / `--target-mag M` — downsample while converting, for
  `--to svs|tiff|ome-tiff|cog-wsi`, with correctly-scaled MPP (×N) and
  magnification (÷N). `dzi`/`szi` not yet supported.
- `downsample` is now **format-preserving** and works on more sources: it
  reduces SVS, OME-TIFF, generic-TIFF, and COG-WSI slides in place (same
  container in/out, MPP/mag scaled), instead of SVS-only. Sources with no
  matching writer error with a pointer to `convert --to … --factor`. Shares the
  reduction engine (`internal/downscale`) with `convert --factor`.

- Default soft memory limit: wsitools now sets `GOMEMLIMIT` to 75% of
  physical RAM at startup so memory-heavy conversions degrade under GC
  pressure instead of OOM-ing the host. Override with the global
  `--max-memory` flag (e.g. `8000`, `12GiB`, `off`) or the `GOMEMLIMIT`
  environment variable; precedence is `--max-memory` > `GOMEMLIMIT` >
  default. `wsitools doctor` now reports physical RAM and the active soft
  limit with its source.
- `scripts/bench-dzi.sh` now reports peak resident memory (via
  `/usr/bin/time -l`) alongside wall-clock time for both wsitools and
  vips, with a memory ratio column.

### Changed

- **BREAKING — associated-image terminology "kind" → "type"** (aligns with
  opentile-go's `AssociatedImage.Type()`):
  - `extract --kind` is renamed to `extract --type` (no alias — the old flag is
    removed).
  - `info --json` associated-image field `kind` → `type`.
  - `dump-ifds --json` IFD-classification field `kind` → `image_type` (named
    `image_type` rather than `type` to avoid colliding with `--raw`'s existing
    per-tag TIFF `type` field).
- opentile-go upgraded v0.26.0 → v0.31.0. v0.27–v0.29 are internal NDPI
  decode-perf work (pixel-frame cache, cross-format decoder-handle pool,
  ReadRegion allocation elimination); v0.30 adds a per-Slide read-memory
  budget (`OPENTILE_READ_MEMORY_BUDGET`, default 1 GiB) that byte-bounds
  the strip/tile decode caches, lowering peak RSS on wide NDPI slides;
  v0.31 exposes raw TIFF tags cross-format (`Slide.LevelTIFFTags` /
  `AssociatedTIFFTags` / `TIFFDirectoriesOf`, typed `TIFFTag`,
  pixel-pointer-filtered) — the foundation for upcoming metadata
  carry-through. Clean drop-in; no wsitools API changes. Later upgraded
  through v0.33.0 (chroma-subsampling JP2K decode fix; separable Lanczos;
  codec-domain scaled decode for JPEG2000/HTJ2K; `dicom.ListWSMSeries`).
- `downsample` primary reduction is now codec-agnostic: it uses codec-domain
  scaled decode (`DecodeOptions.Scale`) where the source codec supports it and
  falls back to full-decode + box otherwise.
  - **JP2K sources now decode via wavelet resolution-reduction** (opentile-go
    v0.33.0) instead of full-decode + box — faster and sharper, but **output
    pixels are no longer byte-identical** to prior releases for JP2K sources.
  - **Fixes** `downsample --factor 16` on JPEG sources (previously errored with
    `scale=16 (want 1,2,4,8)`).
  - **Adds** `downsample` support for AVIF / WebP / HTJ2K sources (previously
    `unsupported compression`).

## [0.20.0] — 2026-05-29

### Added

- `dump-ifds --raw` — full tiffinfo-style dump of every TIFF tag per
  IFD, with well-known tag names, type names, counts, decoded values,
  and enum interpretation for Compression, PhotometricInterpretation,
  PlanarConfiguration, Predictor, Orientation, ResolutionUnit,
  FillOrder, SampleFormat, ExtraSamples, SubfileType, NewSubfileType.
- `dump-ifds --raw --json` — same content in machine-readable JSON.
- `dump-ifds --raw-full` — disable smart truncation of long arrays,
  ASCII strings, and BYTE/UNDEFINED blobs.

### Changed (internal)

- `internal/source.IFDRecord` gains `ByteOrder` and `Entries` fields.
  The `Entries` slice is populated only by the new `WalkIFDsRaw`
  function; existing `WalkIFDs` callers see no behavior change.
- New `internal/tiff/tagnames.go` provides `TagName`, `TypeName`,
  `TypeSize`, `InterpretEnum` for ~100 well-known TIFF tags.

## [0.19.0] — 2026-05-29

### Changed (infrastructure)

- CI now downloads test fixtures from `wsilabs/wsi-fixtures` and
  runs the previously-`t.Skip`'d integration tests on every push
  and PR. Catches per-platform regressions (e.g. the v0.17 Windows
  linkage issue) before tagging.

### Internal

- New `.github/workflows/ci.yml` step downloads + verifies fixture
  SHA-256.
- New `.github/fixtures.sha256` pins expected hashes.
- New sibling repo `wsilabs/wsi-fixtures` hosts the initial corpus:
  CMU-1-Small-Region.svs (Aperio SVS) and CMU-1.ndpi (Hamamatsu
  NDPI). Both CC0; license + provenance artifacts alongside each
  binary; release tagged v1 with per-format tarballs.

### Deferred

- Per-format CI coverage beyond NDPI + SVS (Philips, OME-TIFF, BIF,
  IFE, SCN, MRXS, DICOM, SZI, generic-TIFF, COG-WSI). Audit + add
  incrementally.
- Hamamatsu-1.ndpi (6.43 GB) exceeds GitHub's 2 GB per-file release
  asset limit; chunked-upload or external-host scheme deferred.
- `make bench-dzi` as a CI gate (needs libvips in runner image).

## [0.18.0] — 2026-05-28

### Fixed

- `convert --to dzi|szi` now exits cleanly on SIGINT (Ctrl-C).
  Previously the descent stage's channel send blocked indefinitely
  once encoder workers cancelled on `ctx.Done()` — the test
  `TestConvertDZICtxCancel` had to be `t.Skip`'d in v0.17 because
  of this. Fixed by adding a `ctx` field to the level builders and
  making `emitRow`'s channel send selectable on `ctx.Done()`.
  Process exit after SIGINT now happens in ~100-500 ms, bounded
  only by the slowest encoder worker finishing its current cgo
  encode call.

- `TestConvertDZICtxCancel` re-enabled.

## [0.17.0] — 2026-05-28

### Performance

`convert --to dzi|szi` rewritten from scratch as a pyramid-descent
generator. The v0.16 path was unusable on real WSI sources (NDPI
sources timed out at 5 minutes in CI, took 35 minutes for a full
run). v0.17 is competitive with libvips `dzsave`:

```
fixture                          wsitools(s)   libvips(s)   ratio
------------------------------------------------------------------
CMU-1-Small-Region.svs                  0.06         0.14   0.43x
CMU-1.ndpi   (51200×38144)             14.25        17.23   0.83x
OS-2.ndpi    (126976×73728)           164.23        84.11   1.95x
```

CMU-1.ndpi went from 35 minutes (v0.16) → 14 seconds (v0.17),
~150× faster, and is now faster than libvips. The previously-skipped
`TestConvertNDPIToDZI` / `TestConvertNDPIToSZI` integration tests
re-enabled with 120s budgets and pass.

### Architectural changes

- **Single ScaledStrips iterator** at the largest DZI level reads
  the source once. Previous v0.17 attempt opened one iterator per
  DZI level, re-decoding the source for each — was scrapped (see
  the failed-attempt memory note).
- **Pyramid-descent cascade** with box-filter 2× downsample between
  DZI levels. Each level holds a rolling 3-strip buffer for tile
  overlap; tiles emit as the buffer rotates.
- **Parallel JPEG encoder pool** (sized by `--workers`, default
  GOMAXPROCS) calls libjpeg-turbo via the reorganized
  `internal/codec/jpeg`. Single serialized sink-drain goroutine
  satisfies SZI's `archive/zip.Writer` non-concurrent-safety.
- **sync.Pool of RGB tile buffers** to reduce GC pressure on large
  pyramids (~190K tile-buffers on OS-2.ndpi).
- **3-byte RGB throughout** the cascade (no alpha). opentile-go
  emits RGB; libjpeg-turbo takes RGB; the v0.17-first-attempt's
  `*image.RGBA` was pure overhead.
- **Top-of-cascade resample kernel = Nearest**. At the top, source
  size equals output size — no scaling needed. The default Lanczos
  was burning ~80% CPU on identity scale (found by profiling). Box
  is used for the in-process downsample cascade.

### Changed (BREAKING bytes; pixels equivalent)

- All `--codec jpeg` outputs (DZI, SZI, TIFF, OME-TIFF, **and SVS**)
  now emit vanilla YCbCr+4:2:0 JPEGs with embedded DQT/DHT tables.
  Previously the codec emitted Aperio's APP14 + raw-RGB format on
  every container — correct for SVS-shaped output read by
  APP14-aware decoders (openslide, libjpeg-turbo); incorrect for
  the rest, which silently produced hue-rotated images in stdlib
  + browser decoders.
- New wsitools-produced SVS output matches Grundium's
  third-party-SVS convention (vanilla JPEG with "Aperio" prefix
  in ImageDescription for detection). Every consumer reads both;
  no functional regression.

### Internal

- `internal/codec/jpeg/` rewritten: Factory now produces vanilla
  YCbCr JPEGs. New `EncodeStandalone` entry point used by the DZI
  encoder pool.
- `internal/codec/aperioapp14/` new package containing the
  preserved Aperio APP14 encoder as a direct type (not
  Factory-registered). Zero current callers; preserved for
  forensic emulation.
- New `--cpu-profile <file>` global flag for diagnostic profiling.
- `scripts/bench-dzi.sh` + `make bench-dzi` target for ongoing
  libvips comparison.

### Test changes

- `TestConvertNDPIToDZI` and `TestConvertNDPIToSZI` re-enabled
  (were `t.Skip`'d in v0.16). Run in ~20s under the 120s budget.
- `TestConvertDZICtxCancel` `t.Skip`'d for v0.17 — the pipeline
  doesn't unwind cleanly on SIGINT mid-flight because emitRow's
  channel send is not selectable on ctx.Done(). v0.18 will redesign.

### Deferred to v0.18

- **OS-2.ndpi residual ~2× gap to libvips.** Profile shows 41% of
  CPU in `runtime.cgocall` — libjpeg-turbo per-tile encode overhead
  dominates. Batched encoder API (`tjCompress2` accepting multiple
  tiles per cgo call) can amortize the call overhead. Separate work.
- **Cooperative shutdown on SIGINT** for in-progress conversions.
  Requires `select`-on-`done` at every channel send in the descent.

## [0.16.2] — 2026-05-26

### Fixed

- `convert --to ome-tiff` from a non-OME source now produces output
  that opentile-go's OME-TIFF reader correctly identifies as OME.
  The L0 ImageDescription is synthesized as a minimal OME-XML 2016-06
  document (single `<Image>` + `<Pixels SizeX/SizeY/SizeC=3/SizeZ=1/SizeT=1>`
  + three `<Channel>` entries + `<TiffData IFD="0">`). Ends with the
  literal `OME>` suffix that drives OME detection (mirrors tifffile's
  `is_ome`). PhysicalSizeX/Y emitted from `source.Metadata` when
  available; Creator records wsitools provenance.
- Renamed `buildSVSL0ExtraTags` → `buildL0ImageDescriptionTag` since
  it now serves both SVS and OME-TIFF L0 ImageDescription emission.

## [0.16.1] — 2026-05-26

### Fixed

- `convert --to svs` from a non-SVS source now produces output that
  opentile-go's SVS reader (and tifffile, openslide) correctly
  identifies as SVS. Two issues addressed:
  - L0 ImageDescription is synthesized with an Aperio-shaped header
    (`Aperio Image, wsitools/<ver> (from <source>)`) so the literal
    `Aperio` prefix that drives SVS detection is present. MPP and
    AppMag are surfaced from `source.Metadata` when available.
    Matches the third-party-vendor convention used by Grundium and
    others (`Aperio Image, <vendor>`).
  - Pyramid IFDs now emit `NewSubfileType=0` on all levels (was `1`
    on L1+). Aperio's SVS convention is no reduced-res bit on
    pyramid pages; tifffile's `_series_svs` algorithm terminates the
    baseline walk at the first reduced-res IFD, so the old layout
    caused intermediate pyramid levels to be misclassified as
    Label/Macro and the reader to error.

### Known still-broken

- `convert --to ome-tiff` from a non-OME source produces output that
  opentile-go reads as generic-tiff (no OME-XML synthesis). Tracked
  for v0.16.2.

## [0.16.0] — 2026-05-26

### BREAKING

- `transcode` subcommand removed. Use `convert --to <target>` with
  the same `--codec` / `--quality` / `--workers` flags.
  Migration is mechanical:
  - `wsitools transcode --container svs --codec jpeg IN OUT`
    → `wsitools convert --to svs --codec jpeg IN OUT`
  - `wsitools transcode --container tiff --codec webp IN OUT`
    → `wsitools convert --to tiff --codec webp IN OUT`

### Added

- `convert --to dzi` — DeepZoom pyramid output for OpenSeadragon
  and IIIF consumers. Defaults: 256×256 tiles, 1px overlap,
  JPEG Q=85.
- `convert --to szi` — Smart Zoom Image output: DZI pyramid wrapped
  in an uncompressed-stored ZIP archive with optional
  `scan-properties.xml` populated from source metadata.
- `convert --to {svs,tiff,ome-tiff}` — re-encode targets that
  subsume the former `transcode` flows.
- Tile-copy fast path now applies to `--to svs`, `--to tiff`,
  `--to ome-tiff` when no `--codec` is specified and the source is
  natively-tiled. Previously this was a cog-wsi-only optimisation.

### Internal

- `internal/dzi/` — pure-Go DZI writer (manifest + tile tree +
  level math). No cgo dependency.
- `internal/szi/` — SZI writer wrapping `internal/dzi` with an
  `archive/zip` store-method central directory.

### Deferred to v0.17

- ScaledStrips iterator (opentile-go v0.26) wired into the convert
  pipeline.
- libvips `dzsave` performance-comparison benchmark harness.

### Deferred indefinitely

- `--to dicom-wsi` — separate writer; tracked on roadmap.
- Narrow tile-copy path for DZI / SZI when `--dzi-overlap 0` +
  matching geometry + matching codec.

## [0.15.0] — 2026-05-25

### Added

- NDPI, OME-OneFrame, and Leica SCN (single-image) slides are now
  supported across all CLI subcommands (info, transcode, downsample,
  convert, hash, extract, dump-ifds, region). opentile-go synthesizes
  tile geometry from striped MCU streams (NDPI) and single-frame OME
  (OME-OneFrame); wsitools' tile-pipeline now operates on
  opentile-go-tiled output verbatim.

### Changed

- Dropped the "v0.2 sanity gate" in `internal/source/opentile.go`
  that rejected NDPI / OME-OneFrame / Leica-SCN. Stale since
  opentile-go v0.14+ began synthesizing tile geometry.
- `ErrUnsupportedFormat`'s message updated to drop the "v0.2"
  version marker; the sentinel remains for genuinely-unsupported
  future formats.

### Bit-exact tile-copy caveat (convert)

`convert --to cog-wsi` from natively-tiled sources (SVS, Philips,
OME-tiled, BIF, IFE, generic-TIFF, COG-WSI, SZI, single-image
Leica-SCN) continues to produce bit-exact tile-copy COG-WSI output
— the source's compressed tile bytes appear verbatim in the
destination.

From striped sources (NDPI, OME-OneFrame), the COG-WSI output
contains opentile-go's synthesized JPEG tile bytes. These bytes
decode to the same pixels as the source region and are
deterministic (same input → same output), but they are NOT the
source's on-disk bytes (NDPI / OneFrame source files don't carry
tile bytes — they carry strip bytes).

### Out of scope (deferred)

- Multi-channel fluorescence Leica SCN
  (Leica-Fluorescence-1.scn). transcode/downsample assume RGB
  channels; multi-channel handling is a future release.
- Multi-image OME-TIFF where multiple `<Image>` series each carry
  their own pyramid. `info` shows image 0 only.

### Unchanged

- All other CLI surfaces (region, transcode/downsample/convert
  on natively-tiled sources, etc.).
- Output bytes from natively-tiled-source operations — same bytes
  as v0.14.

## [0.14.0] — 2026-05-25

### Added

- `wsitools info` now includes a per-level codec quality summary
  alongside compression. JPEG levels show estimated Q value +
  chroma subsampling (4:4:4 / 4:2:2 / 4:2:0). JPEG 2000 levels show
  reversible/irreversible transform + layer count. WebP levels show
  lossless flag + estimated Q. Lossless codecs (LZW/Deflate/None)
  surface as "lossless". Other codecs (AVIF, JPEG XL, HTJ2K)
  currently surface compression only; quality inspectors land in
  future releases without info-command changes.
- New `cmd/wsitools/quality/` package with pluggable Inspector
  interface and per-codec subpackages: `quality/jpeg`,
  `quality/jpeg2000`, `quality/webp`. New codecs register via
  `quality.Register` in their init().

### Changed

- Dropped `-tags nohtj2k` from `Makefile`'s default `build` /
  `install` targets. Local builds now exercise the full htj2k cgo
  path against openjph (`brew install openjph` on macOS). Opt-out
  with `go build -tags nohtj2k ./cmd/wsitools` if needed.

### Unchanged

- All other CLI surfaces (transcode, downsample, convert, dump-ifds,
  extract, hash, doctor, version, region).
- Output bytes from `transcode` / `downsample` / `convert` — same
  bytes as v0.13.

## [0.13.0] — 2026-05-25

### Added

- `wsitools region` subcommand — extract a rectangular pixel region
  from a slide at a chosen pyramid level and write as PNG.

  Flags:
  - `--level N` (required) — pyramid level index.
  - `--rect X,Y,W,H` OR `--x X --y Y --w W --h H` (mutually
    exclusive; one form required).
  - `--image N` (default 0) — for multi-image OME-TIFF.
  - `--format rgb|rgba` (default rgb).
  - `-o, --output PATH` (required) — PNG output path.
  - `-f, --force` — overwrite existing output file.

  Out-of-bounds regions are white-filled per opentile-go v0.25's
  ReadRegion semantics.

  Examples:

      wsitools region --level 0 --rect 1000,1000,512,512 -o patch.png slide.svs
      wsitools region --level 2 --x 0 --y 0 --w 512 --h 512 -o thumb.png slide.svs

### Dependencies

- Bumped `github.com/wsilabs/opentile-go` to v0.25.0 (adds the
  `ReadRegion` family the new subcommand consumes).

### Unchanged

- All other CLI surfaces (transcode, downsample, convert, info,
  dump-ifds, extract, hash, doctor, version).
- Output bytes from existing commands — pixel-identical to v0.12
  (verified via `make goldens-byte-stable`).

## [0.12.0] — 2026-05-25

Adopts opentile-go v0.24.0 (Level value-type + DecodedTile). No
behavior change for end-users; this is an internal type migration.

### Dependencies

- Bumped `github.com/wsilabs/opentile-go` to v0.24.0 (BREAKING
  upstream: Level/Image interfaces → value-type structs; tile reads
  moved to *Slide methods).

### Changed (internal)

- Every `slide.Levels()[i].Tile(...)` migrated to
  `slide.RawTile(i, ...)`. Every Level field access migrated from
  method call to struct field. CLI surface unchanged.

### Unchanged

- All CLI surfaces (transcode, downsample, convert, info, dump-ifds,
  extract, hash, doctor, version).
- Output bytes — pixel-identical to v0.11 (verified via
  `make goldens-byte-stable`).

## [0.11.0] — 2026-05-24

Migrates to opentile-go v0.23.0's new `*Slide` API. No behavior change
for end-users; this is an internal type migration. CLI surface
unchanged.

### Dependencies

- Bumped `github.com/wsilabs/opentile-go` to v0.23.0 (BREAKING upstream:
  `opentile.Tiler` interface replaced by `*opentile.Slide` struct).

### Changed (internal)

- Every `opentile.OpenTiler(...)` call site migrated to
  `opentile.OpenFile(...)`. Every `opentile.Tiler` typed variable
  migrated to `*opentile.Slide`. Method-call shape preserved exactly.

### Unchanged

- All CLI surfaces (transcode, downsample, convert, info, dump-ifds,
  extract, hash, doctor, version).
- Output bytes — pixel-identical AND byte-identical to v0.10 (verified
  via `make goldens-byte-stable`).

## [0.10.0] — 2026-05-24

Deterministic tile-write order. `transcode` + `downsample` + `convert`
output is now byte-identical across runs and CPU counts. Adds a
pluggable tile-order strategy (RowMajor default; HilbertCurve, Morton
available on permissive formats; SVS strict row-major).

### Added

- New `--tile-order={row-major|hilbert|morton}` CLI flag on transcode,
  downsample, convert. Format-validated: SVS accepts row-major only;
  COG-WSI, generic-TIFF, OME-TIFF accept all three.
- `internal/tiff/tileorder` package: OrderStrategy interface, RowMajor,
  HilbertCurve, Morton, ByName registry.
- Reorder buffer in streamwriter Sink (bounded; back-pressures workers
  when full).
- cogwsiwriter finalize pass consults the strategy for write order.
- `make goldens-byte-stable` Makefile target asserting deterministic
  output across GOMAXPROCS.

### Changed

- Output bytes are now stable run-to-run. Existing pre-v0.10 file SHAs
  in `docs/superpowers/golden-masters-v0.6.0-transcode.txt` are
  historical-only; see `golden-masters-v0.10.0.txt` for v0.10
  canonical hashes.

### Unchanged

- All CLI surfaces and Go APIs (additive only).
- Default behavior — unflagged invocations produce row-major output as
  before.
- Pixel-equality with v0.9 — only on-disk byte order changes, never
  decoded pixel values.

## [0.9.0] — 2026-05-24

Consumes opentile-go v0.22.0's new `decoder/` and `resample/`
subpackages. Deletes wsitools' own `internal/decoder` +
`internal/resample`; transcode + downsample now source decoders from
opentile-go. No behavior change — transcode + downsample output is
**pixel-identical** to v0.8.1 (verified by `wsitools hash --mode pixel`).
Byte-level file hashes vary run-to-run on both v0.8 and v0.9 due to
wsitools' nondeterministic concurrent tile-writer; the decoded pixel
content is preserved exactly.

### Dependencies

- Bumped `github.com/wsilabs/opentile-go` to v0.22.0.

### Changed (internal)

- Deleted `internal/decoder/` (JPEG + JPEG 2000 decoders moved to
  `github.com/wsilabs/opentile-go/decoder/{jpeg,jpeg2000}`).
- Deleted `internal/resample/` (Lanczos + Box resamplers moved to
  `github.com/wsilabs/opentile-go/resample`).
- transcode.go + downsample.go updated to use the new decoder API
  (registry-based factory lookup; `*decoder.Image` return type instead
  of `[]byte`).
- `cmd/wsitools/main.go` now blank-imports
  `github.com/wsilabs/opentile-go/decoder/all` to register every codec.

### Unchanged

- All command-line surfaces (transcode, downsample, convert, info,
  dump-ifds, extract, hash, doctor, version).
- All output formats; decoded pixel content pixel-identical to v0.8.1.
- `internal/codec/` (encoders) unchanged.

### Install

```sh
go install github.com/wsilabs/wsitools/cmd/wsitools@v0.9.0
```

## [0.8.1] — 2026-05-23

Patch release: corrects the embedded `Version` constant. v0.8.0 was
tagged before the version constant was bumped, so the binary shipped
identifying as `0.8.0-dev`. v0.8.1 is the same content with the
constant set to `0.8.1`. Prefer `@v0.8.1` over `@v0.8.0`.

## [0.8.0] — 2026-05-23

Relocation release: repository moved from `github.com/cornish/wsitools`
to `github.com/wsilabs/wsitools` under the new WSILabs GitHub org. No
behavior change; this release exists so consumers can update their
module path:

```diff
- github.com/cornish/wsitools
+ github.com/wsilabs/wsitools
```

The old path continues to redirect at the HTTPS layer for existing
clones, but Go module consumers must update their `go.mod` and import
statements.

### Dependencies

- Upgraded `github.com/wsilabs/opentile-go` to v0.21.0 (also relocated
  from `cornish/opentile-go`).

### Install

```sh
go install github.com/wsilabs/wsitools/cmd/wsitools@v0.8.0
```

## [0.7.0] — 2026-05-21

### Added (user-visible)

- `wsitools info` on COG-WSI files now reports `Format: cog-wsi`
  (was `generic-tiff` in v0.6). Opentile-go's dedicated COG-WSI
  reader honors the `WSIImageType` / `WSILevelIndex` /
  `WSILevelCount` private tags, so pyramid level counts now match
  the source exactly (resolves the level-drop seen in v0.6 on
  Aperio mixed-ratio pyramids re-read through generic-tiff).

### Dependencies

- Upgraded `github.com/cornish/opentile-go` from v0.14.0 to v0.19.0.
  Brings dedicated COG-WSI reader (closes upstream issues #5 + #6)
  plus integer-multiple ratio acceptance in the generic-TIFF pyramid
  classifier. One internal-API change followed (`opentile.AssociatedImage.Kind()`
  → `Type()`); call sites in `internal/source` and `cmd/wsitools`
  updated.

### Changed (internal)

- **TIFF core extraction.** Shared byte-emission primitives moved from
  `internal/wsiwriter` and `internal/cogwsi` into a new
  `internal/tiff` package (TIFF type constants, tag IDs, WSI tag IDs,
  EntryBuilder, WriteHeader, JPEGTables helpers, BigTIFF auto-promote,
  PatchUint32/64, RawTag/AddRaw, Compression value constants).
- **Writer packages reorganized:**
  - `internal/cogwsi` → `internal/tiff/cogwsiwriter` (same API surface,
    now consumes `internal/tiff` primitives).
  - `internal/wsiwriter` → `internal/tiff/streamwriter` (new public
    API: `Options` struct instead of `WithXxx` functional Options,
    `AddStripped` instead of `AddAssociated`, `ExtraTags []tiff.RawTag`
    on `LevelSpec` and `StrippedSpec` for caller-supplied tags).
- **SVS-shape tags moved caller-side.** The Aperio-specific
  `WithLayout(LayoutSVS)` mode was removed from the writer. transcode
  now assembles Aperio `ImageDescription` + `NewSubfileType=9` macro
  marker as `[]tiff.RawTag` and passes via `ExtraTags`. See
  `cmd/wsitools/svs_tags.go`.
- **Aperio ImageDescription parser moved caller-side.** `ParseImageDescription`
  + `MutateForDownsample` + `AperioDescription.Encode` moved from
  `internal/wsiwriter/svs.go` to `cmd/wsitools/svs_imagedesc.go`.
- **Codec interface simplified.** `Encoder.ExtraTIFFTags()` removed (no
  codec used it). Codecs now import `internal/tiff` instead of
  `internal/wsiwriter`.
- **`internal/wsiwriter` deleted.**

### Compatibility notes

- All three commands (`transcode`, `downsample`, `convert`) produce
  files that are functionally equivalent to v0.6.0 output for the
  same inputs and flags: identical image dimensions, tile geometry,
  tag set, MPP, magnification, ImageDescription, JPEGTables, and
  per-tile compressed bytes (verified by per-tile SHA-256 on the CMU
  fixture: 133/133 tile/strip bytes byte-identical).
- File hashes differ at the byte level due to internal layout
  reordering (the wsiwriter interleaved IFD writes with tile writes;
  streamwriter emits all IFDs + external arrays at the end of the
  file, matching the canonical Aperio SVS layout). All consumers
  tested (`tiffinfo`, `wsitools info`, opentile-go's SVS + generic
  readers) treat the outputs as identical SVS / generic-TIFF files.
- COG-WSI output (from `convert`) is byte-identical to v0.6.0 except
  for the embedded `WSIToolsVersion` tag value (now `0.7.0` instead
  of `0.6.0-dev`). All other bytes match.

## [0.6.0] — 2026-05-20

### Added

- New `wsitools convert` command for lossless, bit-exact tile-copy
  conversion between WSI containers. v0.6 target: `--to cog-wsi`.
  Compressed tile bytes are copied verbatim from source — no decode,
  no re-encode. Associated images (label/macro/thumbnail/overview)
  are passed through with their original compression.
  - Supported source formats: SVS, Philips-TIFF, OME-TIFF (tiled),
    BIF, IFE, generic-TIFF. NDPI / Leica SCN / OME-OneFrame return
    `ErrUnsupportedFormat`.
  - Flags: `--to cog-wsi` (required), `-o/--output` (required),
    `-f/--force`, `--bigtiff auto|on|off` (default `auto`),
    `--no-associated`.
- New COG-WSI v0.1 format specification — extension of GeoTIFF
  Cloud Optimized TIFF with WSI extension tags and an
  associated-image tail section. Defines layout, ghost area,
  pyramid + associated IFD requirements, and conformance rules.
  See `docs/superpowers/specs/2026-05-20-cog-wsi-format.md`.
- New `internal/cogwsi` writer package implementing the COG-WSI
  format using a per-level spool staging strategy.
- New private TIFF tag IDs reserved by wsitools: `WSIMPPX`=65085,
  `WSIMPPY`=65086, `WSIMagnification`=65087 (all DOUBLE).

## [0.5.0] — 2026-05-10

Project rename: `wsi-tools` → `wsitools`. Drops the hyphen everywhere
the project's identity is exposed (module path, repo URL, binary name,
CLI invocation, README/docs prose). Output files are bit-identical to
v0.4 — the ImageDescription provenance string still emits
`wsi-tools/<version>` and will swap in v0.5.1 once opentile-go v0.14.1
ships a parser that accepts both prefixes.

### Breaking (install path + binary name)

- Module path: `github.com/cornish/wsi-tools` → `github.com/wsilabs/wsitools`.
- Repo URL: `cornish/wsi-tools` → `wsilabs/wsitools` (GitHub auto-redirects old URLs).
- Binary name: `wsi-tools` → `wsitools`.
- Install: `go install github.com/wsilabs/wsitools/cmd/wsitools@latest`.

### Unchanged

- Every command and flag works identically.
- Output file format is unchanged. Slides written by v0.5.0 are
  byte-equivalent to slides written by v0.4 at the same options.
- The `WSI*` private TIFF tag namespace (65080–65084) keeps its
  current names and values.
- Historical specs/plans under `docs/superpowers/` and CHANGELOG
  entries v0.1.0–v0.4.1 retain their original "wsi-tools" prose as
  time-capsule artifacts.
- Existing v0.1.0–v0.4.1 binaries continue to work; the rename only
  affects new installs from `@latest`.

### Queued for v0.5.1

- ImageDescription provenance prefix swap from `wsi-tools/<version>`
  to `wsitools/<version>`, coordinated with an opentile-go v0.14.1
  patch that accepts both prefixes.

## [0.4.0] — 2026-05-09

Inspection-utilities milestone. Adds four read-side CLI utilities —
`info`, `dump-ifds`, `extract`, `hash` — analogs of openslide-tools and
slim tiffinfo. Plus a shared `internal/cliout` package for text/JSON
dual rendering, and a top-level `docs/roadmap.md` tracking the full
utilities roadmap.

### Added

- **`wsi-tools info <file>`** — slide summary: file size, format,
  scanner metadata (make/model/software/datetime/MPP/magnification),
  pyramid levels with dimensions+tile size+compression per level, and
  associated images. `--json` emits a structured object.
- **`wsi-tools dump-ifds <file>`** — format-aware per-IFD layout dump.
  Walks every IFD in file order (ClassicTIFF + BigTIFF, main chain +
  SubIFDs), cross-references each against opentile-go's classifier
  (pyramid L0/L1/.../label/macro/thumbnail/overview/probability/map),
  and reports any wsi-tools private tags (65080–65084) present. Not a
  full tiffinfo replacement — does not dump every TIFF tag. A `--raw`
  expansion is reserved for batch 2.
- **`wsi-tools extract --kind <k> -o <path> <file>`** — save an
  associated image (label/macro/thumbnail/overview) as PNG (default) or
  JPEG. When `--format jpeg` and source is already JPEG, bytes pass
  through verbatim. PNG path decodes via `internal/decoder` (jpeg or
  jpeg2000) or `golang.org/x/image/tiff` (lzw/deflate/none).
- **`wsi-tools hash <file>`** — content hash. `--mode file` (default):
  SHA-256 of file bytes, `sha256sum` equivalent. `--mode pixel`:
  SHA-256 of L0 tiles decoded to RGB in raster order, stable across
  re-encode. Output prefix names the algorithm (`sha256:` vs
  `sha256-pixel:`) so any future algorithm change can use a different
  prefix. Not byte-for-byte compatible with openslide-quickhash1.
- **`internal/cliout`** — shared text/JSON dual-rendering helpers:
  `RegisterJSONFlag`, `Render`, `JSON`. Used by all four batch-1
  utilities to avoid per-subcommand format-flag boilerplate.
- **`internal/source.WalkIFDs`** — TIFF IFD walker (ClassicTIFF +
  BigTIFF, main chain + SubIFDs from tag 330) returning per-IFD
  records with the tags `dump-ifds` needs.
- **`docs/roadmap.md`** — durable record of the full utilities roadmap
  (batch 1, batch 2, batch 3, and larger items: dzsave, tile-server,
  DICOM-WSI conversion).

## [0.3.1] — 2026-05-08

Patch release. Fixes the v0.3.0 Windows build: `internal/codec/all`
imported the htj2k codec unconditionally, but Windows CI builds with
`-tags nohtj2k` (OpenJPH isn't packaged for msys2) and the dangling
import broke compilation. Same fix unblocks any `-tags no<codec>`
slim binary on any platform. macOS and Linux runtime behaviour is
unchanged.

### Fixed

- **`internal/codec/all` build tags** — split into one file per
  optional codec, each behind its matching `!no<name>` constraint
  (`!noavif`, `!nowebp`, `!nojxl`, `!nohtj2k`). Always-on `jpeg`
  stays in `all.go`. Verified building under default tags AND
  `-tags 'nohtj2k noavif nowebp nojxl'` (every optional codec
  disabled). `wsi-tools doctor` correctly omits any disabled codec
  from the registered-codecs list.

## [0.3.0] — 2026-05-08

opentile-go v0.14 alignment. Bumps the upstream dep from v0.12 to
v0.14, claims the new capabilities that bump unlocks (novel-codec
recognition + wsi-tools ImageDescription parsing on re-read), and
migrates the streaming transcode hot path to opentile-go's
allocation-free `TileInto` API with a per-level `sync.Pool` of
tile-sized buffers.

### Added

- **`internal/source.Compression`** gains three new values —
  `CompressionWebP`, `CompressionJPEGXL`, `CompressionHTJ2K` —
  matching opentile-go v0.14's new enum values. AVIF was already
  mapped via the v0.8 `CompressionAVIF` constant.
- **`pipeline.Tile.Release func()`** — optional buffer-pool callback,
  invoked by the consumer between decode and encode. Nil-safe; the
  pipeline package itself stays opaque to the field.
- **opentile-go round-trip integration test for the 4 novel codecs**
  — replaces the prior `tiffinfo` shell-out with assertions on
  `Format()`, `Compression()`, `TileSize()`, and
  `Metadata.AcquisitionDateTime`.

### Changed

- **opentile-go bumped from v0.12 → v0.14.** Both v0.13 (additive
  splice-prefix family on `Level`) and v0.14 (additive Compression
  values + wsi-tools ImageDescription parser) are non-breaking.
- **`internal/source.Level` interface** — `Tile()` removed; replaced
  by `TileMaxSize()` and `TileInto(x, y int, dst []byte) (int, error)`.
  `internal/` is private API; no external callers affected. The
  transcode producer now uses a per-level `sync.Pool`; the downsample
  source loop hoists a single tile-sized buffer above the loop.
- **`cmd/wsi-tools/version.go::Version`** bumped to `0.3.0-dev`. The
  literal `"wsi-tools/0.2.0-dev"` strings in the transcode provenance
  builder and the `WithToolsVersion` writer option are now derived
  from `Version`, not hardcoded.

### Not adopted (intentional)

- `opentile.Level.TilePrefix` / `TileBodyInto` / `SpliceJPEGTile` are
  bandwidth-deduplication helpers for client-server byte-passthrough
  scenarios. wsi-tools' transcode pipeline fully decodes every tile,
  so the splice family offers no benefit. The decision is reversible
  if a future feature (e.g., a streaming HTTP tile server) needs it.

## [0.2.0] — 2026-05-08

The transcode milestone. Adds `wsi-tools transcode` with 4 new codec wrappers, expands source format support to 6 sane TIFF dialects, ships a streaming pyramid pipeline that lifts the v0.1 memory ceiling, and bundles a fix for v0.1 downsample's associated-image IFD-ordering bug.

### Added

- **`wsi-tools transcode`** — re-encode the pyramid tiles in a different compression codec while preserving source tile geometry and metadata. Associated images (label, macro, thumbnail, overview) pass through verbatim.
  - **Codec targets**: `jpegxl` (libjxl, JPEG-XL codestream per tile), `avif` (libavif), `webp` (libwebp), `htj2k` (OpenJPH HTJ2K codestream). Plus the v0.1 `jpeg` codec available as a transcode target for re-encoding at a different quality. **Total: 5 `--codec` values accepted at v0.2.0.**
  - **Source formats**: SVS, Philips-TIFF, OME-TIFF (tiled SubIFD path), BIF (Ventana), IFE (Iris), generic-TIFF. NDPI, OME-OneFrame, and Leica SCN error cleanly with `ErrUnsupportedFormat`.
  - **Streaming end-to-end**: per-tile decode → encode → write, no L0 raster materialisation. Memory ceiling drops three orders of magnitude vs. v0.1 downsample (≈ workers × tile_bytes × 2, independent of slide size).
  - **Output container**: SVS-shaped when source is SVS AND codec is `jpeg` (Aperio convention); generic pyramidal TIFF otherwise.
  - **Per-codec quality knobs**: single `--quality 1..100` mapped per-codec; `--codec-opt key=val` for codec-specific tuning (`jxl.distance`, `jxl.effort`, `avif.speed`, `webp.lossless`, etc.).
  - **Per-codec build tags**: `-tags nojxl noavif nowebp nohtj2k` produce slim binaries that skip selected codecs.
- **`internal/source`** — adapter package between the CLI and opentile-go. Encapsulates the sanity gate (NDPI / OME-OneFrame / Leica SCN rejection) and exposes a unified streaming-friendly tile API. The `ReadSourceImageDescription` helper (TIFF tag-270 reader) was promoted from `cmd/wsi-tools/downsample.go` so transcode + downsample share the implementation.
- **`internal/wsiwriter`** — extended with self-describing TIFF tags:
  - `WSIImageType` (private tag 65080, ASCII): one of `pyramid`, `label`, `macro`, `overview`, `thumbnail`, `probability`, `map`, `associated`. Aligns with DICOM-WSI's `ImageType` vocabulary.
  - `WSILevelIndex` (65081, LONG), `WSILevelCount` (65082, LONG): emitted on pyramid IFDs.
  - `WSISourceFormat` (65083, ASCII), `WSIToolsVersion` (65084, ASCII): emitted on L0.
  - Standard TIFF metadata tags now populated from opentile-go's cross-format metadata: 271 Make, 272 Model, 305 Software, 306 DateTime.
  - Documented in `docs/tiff-tags.md` (renamed from `docs/compression-tags.md`).
- **Integration test sweep** (`tests/integration/transcode_test.go`):
  - Per-codec sweep (jpeg + 4 v0.2 codecs) with structural validation via `tiffinfo` for codecs opentile-go doesn't yet decode.
  - Per-source-format sweep across all 6 sane source formats. Includes NDPI + Leica SCN rejection cases.
  - 4.8 GB BigTIFF fixture re-included (v0.1 excluded it for memory reasons; streaming makes it tractable).
- **CI**: macOS workflow installs `jpeg-xl libavif webp openjph` in addition to v0.1's deps. Windows workflow adds `libjxl libavif libwebp` via msys2; OpenJPH is not packaged for msys2 yet, so the Windows build uses `-tags nohtj2k`.

### Fixed

- **`wsi-tools downsample` IFD-ordering bug**: v0.1 wrote `L0, L1, …, LN, thumbnail, label, macro` (pyramid first, all associated at end). opentile-go's SVS classifier (`formats/svs/series.classifyPages`) takes the LAST 2 trailing pages as label/macro, so thumbnail was being misclassified as label and the real label getting reclassified as macro on re-read. v0.2 corrects the ordering to `L0, [thumbnail], L1, …, LN, label, macro` matching Aperio's convention. Verified by `TestDownsample_AssociatedKindRoundTrip`.

### Changed

- **opentile-go bumped from v0.10 → v0.12**. v0.12 renames `FormatPhilips` → `FormatPhilipsTIFF` (`"philips-tiff"`) and `FormatOME` → `FormatOMETIFF` (`"ome-tiff"`), and v0.11 added `FormatLeicaSCN`. wsi-tools now references `opentile.Format*` constants rather than literal strings, insulating future renames.
- `cmd/wsi-tools/downsample.go`'s local `readSourceImageDescription` helper promoted to `internal/source.ReadSourceImageDescription`.

### Deferred to v0.2.x or later

- **`jpegli` codec**: was originally part of v0.2.0 but Homebrew's `jpeg-xl 0.11.2` bottle ships libjxl without `libjpegli` (upstream disables it to avoid libjpeg symbol-conflicts). Defer to v0.2.1+ once we either get an upstream re-enable or stand up a build-from-source path.
- **HEIF, JPEG-LS, JPEG-XR, Basis Universal codecs**: queued for v0.2.x.
- **`jpeg2000` as a transcode target**: decoder is shipped; encoder wrapper is queued for v0.2.x.
- **Streaming retrofit for `downsample`**: v0.2.0 ships streaming for transcode only; downsample still materialises the full L0 raster. v0.2.x.
- **Leica SCN source support**: SCN's multi-image + multi-channel structure requires per-`Image` and per-channel pipeline plumbing not in v0.2.0 scope.
- **Visual-fidelity tests via mini decoders** (read raw tile bytes from opentile-go, decode via the matching codec library): v0.2.x follow-up to validate JPEG-XL / AVIF / WebP / HTJ2K outputs without depending on opentile-go to grow decoders for those compression IDs.
- ~~**opentile-go decoders for JXL / AVIF / WebP / HTJ2K compression tags**~~: **landed in v0.3.0** via opentile-go v0.14's new `Compression` enum values + generic-TIFF tag mappings. opentile-go does not decode the tile bytes (byte-passthrough contract — consumers bring their own codec libraries) but recognises the compression tags and parses the wsi-tools `ImageDescription`.

## [0.1.0] — 2026-05-07

First release. Ships the `downsample` subcommand end-to-end on Aperio SVS sources.

### Added

- **`wsi-tools downsample`** — produce a lower-magnification copy of an Aperio SVS by an integer power-of-2 factor (default 2 = 40x → 20x). Regenerates the entire pyramid from the new L0; passes through associated images (label, macro, thumbnail, overview) verbatim.
  - Source codecs: JPEG (libjpeg-turbo, with 1/N in-decode fast scale) and JPEG 2000 (OpenJPEG, full decode + 2×2 area average chain).
  - Output: Aperio-shaped SVS (or BigTIFF when predicted output > 2 GiB).
  - Flags: `--factor`, `--target-mag`, `--quality`, `--jobs`, `--bigtiff` (auto), `--force`, `--quiet`, `--verbose`, `--log-level`, `--log-format`.
- **`wsi-tools doctor`** — list registered codecs and required cgo libraries.
- **`wsi-tools version`** — print version + Go runtime info.
- **`internal/wsiwriter`** — pure-Go TIFF / BigTIFF / Aperio-SVS writer with stripped + tiled IFDs, atomic close, abbreviated-JPEG tile mode + per-level `JPEGTables`, and `ImageDescription` mutation for downsample.
- **`internal/codec/jpeg`** — libjpeg-turbo encoder writing raw-RGB-storage JPEGs with the Aperio Adobe APP14 marker (matches what real Aperio scanners emit).
- **`internal/decoder/{jpeg,jpeg2000}`** — libjpeg-turbo + OpenJPEG decoders, with libjpeg-turbo's 1/N fast-scale in-decode path for the JPEG case.
- **`internal/resample`** — 2×2 area-average resampler (pure Go); Lanczos plumbed as a stub returning `ErrNotImplemented` until v0.2.
- **`internal/pipeline`** — worker-pool decode → process → encode plumbing with cancellation via `context.WithCancelCause`, atomic on-disk semantics, and SIGINT/SIGTERM handling at the CLI layer.
- Progress bar (`vbauerster/mpb`) + structured logging (`log/slog`, text or JSON).
- Integration test suite (`-tags integration`) gated by `WSI_TOOLS_TESTDIR`, sweeping the standard opentile-go fixture pool.
- CI: macOS (build + test), Windows (build only). Linux untested but expected to work.

### Known limitations

- v0.1 holds the full L0 raster in memory during pyramid build (≈4.5 GiB at 20x sources, ≈18 GiB at 40x sources). Streaming pyramid build is the headline v0.2 deliverable.
- Lanczos resampler is stubbed; `--factor` rejects non-power-of-2 values at the CLI layer until v0.2.
- libjpeg's default `error_exit` calls `exit(1)` on any libjpeg error; production-grade error recovery requires installing a custom `longjmp`-based handler. Acceptable for v0.1 since input validation happens up front.
- No transcode tool yet — that's the v0.2 milestone, with 11 codec targets (jpegli, JPEG-XL, AVIF, WebP, HEIF, JPEG-LS, JPEG-XR, HTJ2K, Basis Universal, plus jpeg/jpeg2000 baselines).
- SVS sources only. NDPI, Philips, OME-TIFF, BIF, IFE deferred to v0.2+.

### Notes

This is a from-scratch v0 release; no prior version history exists. The implementation plan and design spec live at `docs/superpowers/specs/2026-05-06-wsi-tools-v01-design.md` and `docs/superpowers/plans/2026-05-06-wsi-tools-v01-foundation-and-downsample.md` for posterity.
