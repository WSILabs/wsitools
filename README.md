# wsitools

[![CI](https://github.com/wsilabs/wsitools/actions/workflows/ci.yml/badge.svg)](https://github.com/wsilabs/wsitools/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/wsilabs/wsitools.svg)](https://pkg.go.dev/github.com/wsilabs/wsitools)

A Swiss-army knife of utilities for whole-slide imaging (WSI) files used in
digital pathology.

See [`CHANGELOG.md`](./CHANGELOG.md) for release notes.

## What's here (v0.20)

**Inspection**

- `wsitools info` — slide summary: format, levels (dimensions + tile
  size + compression), associated images, scanner metadata. Text or
  `--json`. Analog of `openslide-show-properties`.
- `wsitools dump-ifds` — format-aware per-IFD layout dump. Annotates each
  IFD with its classification (pyramid L0/L1/…/label/macro/thumbnail/
  overview/probability/map) and reports wsitools private tags (65080–
  65084). Slim tiffinfo analog. Use `--raw` for a full per-tag dump
  (every TIFF tag with name + type + count + value + enum interpretation;
  composes with `--json`); `--raw-full` disables smart truncation of long
  arrays and binary blobs.
- `wsitools region --x --y --w --h --level -o out.png` — extract a
  rectangular pixel region as PNG (analog of `openslide-write-png`).
- `wsitools hash` — content hash. `--mode file` (default,
  `sha256sum`-equivalent) or `--mode pixel` (L0 RGB tiles in raster order,
  stable across re-encode).
- `wsitools extract --kind <k> -o <path>` — save an associated image
  (label / macro / thumbnail / overview) as PNG (default) or JPEG. JPEG
  output is byte-pass-through when the source is already JPEG.

**Conversion**

- `wsitools convert --to cog-wsi` — losslessly copy a WSI into the COG-WSI
  container (Cloud Optimized GeoTIFF + WSI extension tags). Tile bytes are
  copied verbatim; no decode, no re-encode. See
  `docs/superpowers/specs/2026-05-20-cog-wsi-format.md` for the format spec.
- `wsitools convert --to {svs, tiff, ome-tiff}` — tile-copy re-container
  (no `--codec` set) or re-encode (`--codec {jpeg, jpegxl, avif, webp,
  htj2k}`). Streaming pipeline; no L0 raster materialisation.
- `wsitools convert --to dzi` — DeepZoom pyramid output, OpenSeadragon-
  compatible (256×256 tiles, 1 px overlap, JPEG Q=85 by default). Single-
  pass pyramid-descent generator with parallel libjpeg-turbo encoders;
  ~150× faster than v0.16, faster than libvips `dzsave` on CMU-1.ndpi.
- `wsitools convert --to szi` — Smart Zoom Image: DZI pyramid wrapped in a
  store-method ZIP, plus an optional `scan-properties.xml` populated from
  source metadata.
- `wsitools downsample` — downsample a WSI by a power-of-2 factor (e.g.
  40x → 20x). Regenerates the full pyramid from the new base. Passes
  through associated images verbatim. SVS-only.

Source formats accepted: SVS, Philips-TIFF, OME-TIFF (tiled), BIF, IFE,
generic-TIFF, NDPI, OME-OneFrame, Leica SCN (single-image), and COG-WSI.
Striped sources produce reproducible but synthesized JPEG tiles in the
output (bit-exact tile-copy applies only to natively-tiled sources).

**Diagnostics**

- `wsitools doctor` — report installed codec libraries.
- `wsitools version` — print version + Go runtime info.

## Roadmap

See [`docs/roadmap.md`](./docs/roadmap.md) for the full list of planned
utilities (`dump-tile`, `tagset`, `inventory`, `verify`, `diff`,
`tile-server`, `dicom-wsi`, more codecs) and architectural items still
queued.

## Build prerequisites

cgo dependencies (macOS via Homebrew):

```sh
brew install jpeg-turbo openjpeg jpeg-xl libavif webp openjph
```

`pkg-config` resolves all of them at build time. Linux equivalents
(Debian/Ubuntu):

```sh
apt install libturbojpeg0-dev libopenjp2-7-dev libjxl-dev libavif-dev libwebp-dev
# OpenJPH (HTJ2K) typically requires source build on Linux as of 2026-05.
```

Build a slim binary that skips selected codecs via build tags:

```sh
go build -tags 'noavif nowebp nohtj2k' ./cmd/wsitools   # only JPEG-XL + JPEG
go build -tags 'nojxl noavif nowebp nohtj2k' ./cmd/wsitools   # only JPEG
```

## Install

```sh
go install github.com/wsilabs/wsitools/cmd/wsitools@latest
```

## Usage

### Inspection

```sh
# Slide summary (analog of openslide-show-properties)
wsitools info slide.svs

# Same data as JSON for scripting
wsitools info --json slide.svs | jq .levels

# Format-aware per-IFD layout dump (slim)
wsitools dump-ifds slide.svs

# Full per-tag dump with names + enum interpretation (tiffinfo analog)
wsitools dump-ifds --raw slide.svs

# Same content as JSON
wsitools dump-ifds --raw --json slide.svs | jq .

# Extract a rectangular pixel region as PNG
wsitools region --x 10000 --y 8000 --w 1024 --h 1024 --level 0 -o tile.png slide.svs

# Save the slide's label as a standalone PNG
wsitools extract --kind label -o label.png slide.svs

# Content hash for cache identity / dedup (default: SHA-256 of file bytes)
wsitools hash slide.svs

# Pixel-stable hash (decodes L0 tiles → SHA-256 of RGB raster)
wsitools hash --mode pixel slide.svs
```

### Conversion

```sh
# Lossless tile-copy into a different container
wsitools convert --to cog-wsi -o slide.cog.tiff slide.svs
wsitools convert --to ome-tiff -o slide.ome.tiff slide.svs

# Re-encode to a different codec (still SVS-shaped)
wsitools convert --to svs --codec jpegxl -o slide-jxl.svs slide.svs

# Force BigTIFF (default `auto` promotes when predicted output > 2 GiB)
wsitools convert --to cog-wsi --bigtiff on -o slide.cog.tiff slide.svs

# Skip label/macro/thumbnail/overview
wsitools convert --to cog-wsi --no-associated -o slide.cog.tiff slide.svs

# DeepZoom output (OpenSeadragon-compatible)
wsitools convert --to dzi -o slide.dzi slide.svs

# Smart Zoom Image (DZI in a store-method ZIP)
wsitools convert --to szi -o slide.szi slide.svs

# Downsample a 40x SVS to 20x (factor 2 default)
wsitools downsample -o slide-20x.svs slide-40x.svs

# Or via target magnification
wsitools downsample --target-mag 10 -o slide-10x.svs slide-40x.svs
```

### Other

```sh
# Check installed codec libs
wsitools doctor

# Suppress progress bar (useful in CI / scripts)
wsitools --quiet convert --to cog-wsi -o out.tiff in.svs

# Per-level timing summaries on stderr
wsitools --verbose convert --to dzi -o out.dzi in.svs

# Structured JSON logging (for log aggregators)
wsitools --log-format json convert --to cog-wsi -o out.tiff in.svs
```

### Example output

```
$ wsitools convert --to dzi -o CMU-1.dzi CMU-1.ndpi
encoding  100% 15847/15847 tiles  1132 tiles/s  ETA 0s
wrote CMU-1.dzi (47.3 MB, 14s)
```

## Memory

`downsample` (v0.1) still holds the full L0 raster in memory during
pyramid build:

- A 20x slide L0: ~50K × 30K × 3 ≈ 4.5 GB
- A 40x slide L0: ~100K × 60K × 3 ≈ 18 GB

This fits on most workstations but is tight on laptops. `convert` and
`convert --to dzi|szi` stream per-tile with a constant-memory ceiling
regardless of slide size. A streaming retrofit for `downsample` is queued
— see [`docs/roadmap.md`](./docs/roadmap.md).

## Testing

```sh
make test     # unit tests + integration, race-detector
make vet
```

Integration tests are fixture-gated by `WSI_TOOLS_TESTDIR` (default
`./sample_files`). CI downloads CMU-1-Small-Region.svs + CMU-1.ndpi from
[`wsilabs/wsi-fixtures`](https://github.com/wsilabs/wsi-fixtures) on every
push and PR. For local work, soft-link to your fixture pool:

```sh
ln -s $HOME/GitHub/opentile-go/sample_files sample_files
```

## License

Apache 2.0. See [`LICENSE`](./LICENSE).
