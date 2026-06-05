# wsitools utilities roadmap

Tracks the full set of CLI utilities planned for wsitools, organised
into "shipped" and "planned" sections. The shipped section is updated
as releases land; the planned section is the source of truth for what's
queued, deferred, or under consideration.

## Shipped

### v0.1
- `downsample` — produce a lower-magnification SVS by an integer power-of-2 factor.
- `doctor` — list registered codecs + cgo deps.
- `version` — print version + Go runtime info.

### v0.2
- `transcode` — re-encode pyramid tiles in a different codec (jpeg, jpegxl, avif, webp, htj2k); 6 sane source formats; streaming pipeline.

### v0.3
- (no new utilities — opentile-go v0.14 migration milestone; novel-codec round-trip + sync.Pool + TileInto adoption).

### v0.4
- `info` — slide summary (openslide-show-properties analog).
- `dump-ifds` — format-aware per-IFD layout dump (slim tiffinfo analog).
- `extract` — save associated image (label/macro/thumbnail/overview) as PNG or JPEG.
- `hash` — content hash (file mode default; pixel mode opt-in).

### v0.5
- (no new utilities — project rename: `wsi-tools` → `wsitools`; module path + binary name).

### v0.6
- `convert --to cog-wsi` — lossless, bit-exact tile-copy of a WSI into the new COG-WSI container (Cloud Optimized GeoTIFF + WSI extension tags). Six source formats (SVS, Philips-TIFF, OME-TIFF, BIF, IFE, generic-TIFF). Normative format spec at `docs/superpowers/specs/2026-05-20-cog-wsi-format.md`.

### v0.7
- (no new utilities — TIFF core extraction milestone: shared `internal/tiff` package; `wsiwriter` and `cogwsi` writer packages reorganized as `internal/tiff/streamwriter` and `internal/tiff/cogwsiwriter`. opentile-go upgraded v0.14 → v0.19, bringing the dedicated COG-WSI reader and integer-multiple ratio acceptance — `wsitools info` on COG-WSI output now reports `Format: cog-wsi` and pyramid levels match source counts exactly).

### v0.8
- (no new utilities — repository relocation: module path moved from `github.com/cornish/wsitools` to `github.com/wsilabs/wsitools` under the new WSILabs GitHub organization. opentile-go also relocated to `github.com/wsilabs/opentile-go` at v0.21.0. No behavior change. v0.8.1 corrects the embedded `Version` constant that was missed when v0.8.0 was tagged).

### v0.13
- `region` — openslide-write-png analog: extract `--x --y --w --h --level` rectangle as PNG.

### v0.15
- (no new utilities — source-format expansion: NDPI, OME-OneFrame, and Leica SCN (single-image) slides now work across all CLI subcommands. opentile-go synthesizes tile geometry for striped sources; wsitools' source layer trusts the synthesis. Bit-exact tile-copy promise for `convert` applies to natively-tiled sources only; striped sources produce reproducible but synthesized JPEG tiles in the output.)

### v0.16
- `convert --to dzi` — DeepZoom pyramid output (OpenSeadragon-compatible). Defaults: 256×256 tiles, 1px overlap, JPEG Q=85.
- `convert --to szi` — Smart Zoom Image output: DZI pyramid wrapped in a store-method ZIP with optional `scan-properties.xml` populated from source metadata.
- `convert --to {svs,tiff,ome-tiff}` — re-encode + tile-copy targets that subsume the removed `transcode` subcommand.
- Tile-copy fast path generalised: applies to all TIFF-based targets when `--codec` is absent and the source is natively-tiled.
- BREAKING: `transcode` subcommand removed. Migration is mechanical; see CHANGELOG.

### v0.17
- (no new utilities — performance: `convert --to dzi|szi` rewritten as a pyramid-descent generator with parallel libjpeg-turbo encoder pool. CMU-1.ndpi DZI went from 35 minutes → 14 seconds (~150× faster); now faster than libvips `dzsave` on that fixture. JPEG codec reorganised: vanilla YCbCr default; Aperio APP14 quirk preserved in `internal/codec/aperioapp14`. New `make bench-dzi` target for ongoing libvips comparison.)

### v0.18
- (no new utilities — cooperative SIGINT shutdown for `convert --to dzi|szi`. Ctrl-C now produces a clean process exit in ~100-500 ms instead of requiring SIGKILL. v0.17's deferred `TestConvertDZICtxCancel` re-enabled.)

### v0.19
- (no new utilities — CI fixture pipeline. CI downloads CMU-1-Small-Region.svs + CMU-1.ndpi from `wsilabs/wsi-fixtures` v1 and runs the previously-skipped integration tests on every push + PR. Per-platform regressions visible in CI before tagging.)

### v0.20
- `dump-ifds --raw` — full tiffinfo-style tag dump per IFD with name + enum interpretation. Composes with `--json`. `--raw-full` disables smart truncation. ~100-tag dictionary + 11-enum interpreter in `internal/tiff/tagnames.go`; pure Go, no new deps.

### v0.21 (in progress)
- (no new utilities) Default soft memory cap: wsitools sets `GOMEMLIMIT` to 75% of physical RAM at startup so memory-heavy conversions degrade under GC pressure instead of OOM-ing the host. Global `--max-memory` flag + `GOMEMLIMIT` override (precedence `--max-memory` > env > default); `doctor` reports the active limit. New `internal/memlimit` package.
- opentile-go upgraded v0.26.0 → v0.30.0 (NDPI decode-perf + a per-Slide read-memory budget, `OPENTILE_READ_MEMORY_BUDGET`, that byte-bounds the strip/tile decode caches). No wsitools API changes.
- `convert --to ome-tiff` conformance: pyramid sub-resolutions now stored as
  SubIFDs (330) of L0 (previously written as orphan top-level IFDs → readers
  saw only L0); associated images enumerated in the OME-XML; SampleFormat
  (339) + OME-XML preamble added. Grounded in the OME-TIFF spec
  (`docs/references/ome-tiff-spec-notes.md`).

## Planned

### Debug aids
- **`dump-tile`** — single tile's compressed bytes to file or stdout. Pure debug aid.

### Operations
- **`tagset`** — in-place TIFF tag edit (e.g. ImageDescription, Software). Useful for fixing one bad slide in a pool without full re-encode.
- **`inventory`** — walk a directory; dump CSV/JSON of slide metadata for pool-management UIs.
- **`verify`** — open every IFD, decode every tile, report errors. "fsck for WSI."
- **`diff`** — compare two slides (pixel diff, metadata diff, IFD ordering diff).

### Larger items
- **`tile-server`** — HTTP DZI/IIIF tile server; analog of openslide-python `deepzoom_server.py`. Activates opentile-go v0.13's splice-prefix optimization (TilePrefix / TileBodyInto / SpliceJPEGTile).
- **`convert --to dicom`** (DICOM-WSI writer) — emit a DICOM VL Whole Slide
  Microscopy Image set (Sup. 145). Analog of `wsi2dcm` / `wsidicomizer`.
  **Approach decided (2026-06-03):** pure-Go on `suyashkumar/dicom`, **porting**
  wsi2dcm's WSM-IOD-assembly logic (both Apache-2.0 → direct C++→Go port, no
  clean-room; attribute Google's NOTICE) rather than wrapping it — wrapping would
  drag in OpenSlide (LGPL-2.1, which opentile-go exists to replace) + OpenCV /
  Boost / DCMTK. Port the *logic* into wsitools idioms (pure-Go focused packages;
  reuse `internal/source`, `internal/pipeline`, the tile-copy fast path), NOT a
  foreign code shape. Largest single target; phased (P0 TILED_FULL brightfield
  spike → P1 full pyramid + tile-copy → P2 sparse/label/concatenation → P3
  fluorescence). Rough scoping: `docs/notes/2026-06-03-dicom-writer-scoping.md`.
- **`convert --to dzi --skip-blanks <threshold>`** — drop tiles whose pixels are within `threshold` of uniform background (e.g. white margin around the tissue). OpenSeadragon treats missing DZI tiles as background. Could cut 30-50% of encodes on tissue slides where slide-background dominates the L_max grid. NOT applicable to `--to szi` (SZI spec forbids sparse tile trees). DZI-only. ~200 LOC. v0.17 confirmation: libvips defaults to NOT skipping blanks either — this is a NEW capability, not catch-up.
- ✅ **DONE (2026-06-05): unify downsample/convert scaling.** `convert --factor N`
  / `--target-mag M` ships for `svs|tiff|ome-tiff|cog-wsi` (reduce-then-rebuild
  via the shared `internal/downscale` engine, per-target MPP×N / mag÷N scaling).
  `downsample` is now **format-preserving** (reduces SVS/OME-TIFF/generic-TIFF/
  COG-WSI in place, sharing the same engine; errors with a `convert` pointer for
  non-writable source formats). Spec/plan:
  `docs/superpowers/{specs,plans}/2026-06-05-convert-factor-scaling*`.
  **Still deferred:** `dzi`/`szi --factor` (base-reduced DeepZoom). Possible
  follow-up: split the ~950-line `cmd/wsitools/convert_factor.go` (four target
  paths + cog-wsi pyramid helpers) into focused files / a writer interface.

## Codecs (write-side, separate from utilities)

### Deferred from v0.2
- `jpegli` — blocked on Homebrew libjxl shipping libjpegli OR build-from-source.
- `HEIF`, `JPEG-LS`, `JPEG-XR`, `Basis Universal` — queued.
- `jpeg2000` as a transcode-encoder target — decoder shipped; encoder wrapper queued.

## Source format support

### Deferred from v0.2
- Leica SCN (multi-channel fluorescence) — multi-channel pipeline plumbing deferred.

## Architectural

### Deferred from v0.2
- Streaming retrofit for `downsample` — currently materialises full L0 raster.

### Deferred from v0.21 (added 2026-05-31)
- **Constant-memory `convert --to dzi|szi` cascade.** The pyramid-descent generator holds full-width strip buffers across every level, so peak RSS scales with slide width (~3.5 GB on CMU-1.ndpi, ~5.4 GB on OS-2.ndpi). The v0.21 soft memory cap prevents host OOM but trades throughput under pressure; a true fix needs the cascade to process the source in column bands (or the opentile-go read path to bound its per-frame caches by bytes — partially addressed by v0.30's `OPENTILE_READ_MEMORY_BUDGET`). Tier 2 of the memory work; see `docs/superpowers/specs/2026-05-30-memory-safety-cap-design.md`.

### Deferred from v0.3
- TilePrefix / TileBodyInto / SpliceJPEGTile adoption — only valuable if `tile-server` is built.

### Deferred from v0.17 (added 2026-05-26)
- **Parallel raw-tile fetch + decode for `convert --to svs|tiff|ome-tiff --codec X` on striped sources.** v0.17's ScaledStrips wiring speeds up DZI/SZI rendering but doesn't help TIFF-family re-encode because those targets keep L0 dimensions intact (no scaling). A separate parallel-decode path on the existing `internal/pipeline` worker pool would help striped→TIFF re-encode where opentile-go's per-tile synthesis is the bottleneck. Quantify the gap first — measure striped→TIFF re-encode runtime against a tiled source baseline.

### Deferred from v0.20 (audit complete 2026-05-29; outcomes below)
- ✅ **DZI cascade kernel** — audited. wsitools `convert --to dzi|szi` uses 2×2 box averaging; libvips dzsave does too (`--region-shrink=mean` default). Decoded pixels were bit-identical across three sample levels of CMU-1-Small-Region.svs. No change. Findings: `docs/notes/2026-05-29-dzi-kernel-audit.md`.
- ✅ **Separable Lanczos3 in opentile-go** — DONE. opentile-go v0.32.2 ships a separable, weight-cached two-pass Lanczos (WSILabs/opentile-go#9), now ~7–8× box (was 213×). wsitools bumped to v0.32.2.
- 🚫 **`downsample` CLI spatial `--kernel` flag** — SHELVED (2026-06-03). Reading the code showed the premise was wrong: `downsample` already uses the right tool per stage (JPEG→libjpeg fast-scale, JP2K→box, cascade→box = libvips dzsave parity). A spatial Lanczos kernel in a *tiled* reducer would be slower AND introduce tile-boundary seams. The real win is **codec-domain scaled decode** via `DecodeOptions.Scale` (faster + anti-aliased + seam-free), tracked in opentile-go umbrella #11 (JP2K #10, HTJ2K #12, WebP/JXL queued) + a future codec-agnostic `downsample` refactor. See `docs/notes/2026-05-29-dzi-kernel-audit.md`.

## Quality gates

### Deferred from v0.2
- Visual-fidelity tests via mini decoders — decode v0.2 codec outputs through matching codec library; pixel-compare against source.

### Test coverage
- ✅ **CI fixture pipeline** — shipped v0.19. CI downloads CMU-1-Small-Region.svs + CMU-1.ndpi from `wsilabs/wsi-fixtures` v1 and runs the integration suite on every push + PR.
- ⏳ **Cross-version pixel parity check.** Compare v(N) convert/downsample output's decoded pixels to v(N-1) output's decoded pixels. File-SHA comparison won't work (embedded WSIToolsVersion tag changes with each release), but pixel-equality should hold if no decoder/encoder/resample logic changed. Would catch silent regressions in the decode-resample-encode chain across version bumps.
- ⏳ **`make ci-full` target.** A comprehensive per-release sweep that runs every fixture-gated test and refuses to pass on ENOSPC (instead of silently skipping). Today's pattern of "allow ENOSPC as environmental" is too forgiving — regressions hiding specifically in the largest-sample path can slip through.
- ⏳ **Expanded fixture coverage.** Per-format CI coverage beyond SVS + NDPI (Philips, OME-TIFF, BIF, IFE, SCN, MRXS, DICOM, SZI, generic-TIFF, COG-WSI). Audit + add incrementally.
