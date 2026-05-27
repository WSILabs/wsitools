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

### v0.15
- (no new utilities — source-format expansion: NDPI, OME-OneFrame, and Leica SCN (single-image) slides now work across all CLI subcommands. opentile-go synthesizes tile geometry for striped sources; wsitools' source layer trusts the synthesis. Bit-exact tile-copy promise for `convert` applies to natively-tiled sources only; striped sources produce reproducible but synthesized JPEG tiles in the output.)

### v0.16
- `convert --to dzi` — DeepZoom pyramid output (OpenSeadragon-compatible). Defaults: 256×256 tiles, 1px overlap, JPEG Q=85.
- `convert --to szi` — Smart Zoom Image output: DZI pyramid wrapped in a store-method ZIP with optional `scan-properties.xml` populated from source metadata.
- `convert --to {svs,tiff,ome-tiff}` — re-encode + tile-copy targets that subsume the removed `transcode` subcommand.
- Tile-copy fast path generalised: applies to all TIFF-based targets when `--codec` is absent and the source is natively-tiled.
- BREAKING: `transcode` subcommand removed. Migration is mechanical; see CHANGELOG.

## Planned

### Batch 2 — extends batch 1
- **`region`** — openslide-write-png analog: extract `--x --y --w --h --level` rectangle as PNG. Requires tile decode + stitching across boundaries.
- **`dump-tile`** — single tile's compressed bytes to file or stdout. Pure debug aid.
- **`dump-ifds --raw`** — full tiffinfo-style tag dump per IFD; expansion of v0.4's slim dump-ifds.

### Batch 3 — operations
- **`tagset`** — in-place TIFF tag edit (e.g. ImageDescription, Software). Useful for fixing one bad slide in a pool without full re-encode.
- **`inventory`** — walk a directory; dump CSV/JSON of slide metadata for pool-management UIs.
- **`verify`** — open every IFD, decode every tile, report errors. "fsck for WSI."
- **`diff`** — compare two slides (pixel diff, metadata diff, IFD ordering diff).

### Larger items
- **`tile-server`** — HTTP DZI/IIIF tile server; analog of openslide-python `deepzoom_server.py`. Activates opentile-go v0.13's splice-prefix optimization (TilePrefix / TileBodyInto / SpliceJPEGTile).
- **`dicom-wsi`** — convert WSI to DICOM-WSI format. Analog of `wsi2dcm` (highdicom) and `wsidicomizer`.

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

### Deferred from v0.3
- TilePrefix / TileBodyInto / SpliceJPEGTile adoption — only valuable if `tile-server` is built.

## Quality gates

### Deferred from v0.2
- Visual-fidelity tests via mini decoders — decode v0.2 codec outputs through matching codec library; pixel-compare against source.

### Test coverage (added 2026-05-26)
- **CI fixture pipeline.** wsitools' GH Actions should pull sample slides from a release artifact / S3 / object-store before running fixture-gated tests. Today CI skips every fixture-gated test (samples are gitignored), so the bulk of integration coverage runs only on local pre-release sweeps.
- **Cross-version pixel parity check.** Compare v(N) transcode/downsample output's decoded pixels to v(N-1) output's decoded pixels. File-SHA comparison won't work (embedded TagWSIToolsVersion changes with each release), but pixel-equality should hold if no decoder/encoder/resample logic changed. Would catch silent regressions in the decode-resample-encode chain across version bumps.
- **`make ci-full` target.** A comprehensive per-release sweep that runs every fixture-gated test and refuses to pass on ENOSPC (instead of silently skipping). Today's pattern of "allow ENOSPC as environmental" is too forgiving — regressions hiding specifically in the largest-sample path can slip through.
