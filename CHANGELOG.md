# Changelog

All notable changes to wsi-tools will be documented here. The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

- Module path: `github.com/cornish/wsi-tools` → `github.com/cornish/wsitools`.
- Repo URL: `cornish/wsi-tools` → `cornish/wsitools` (GitHub auto-redirects old URLs).
- Binary name: `wsi-tools` → `wsitools`.
- Install: `go install github.com/cornish/wsitools/cmd/wsitools@latest`.

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
