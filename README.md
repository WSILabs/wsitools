# wsitools

[![CI](https://github.com/wsilabs/wsitools/actions/workflows/ci.yml/badge.svg)](https://github.com/wsilabs/wsitools/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/wsilabs/wsitools.svg)](https://pkg.go.dev/github.com/wsilabs/wsitools)

A Swiss-army knife of utilities for whole-slide imaging (WSI) files used in
digital pathology.

See [`CHANGELOG.md`](./CHANGELOG.md) for release notes.

## What's here (v0.21)

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
generic-TIFF, NDPI, OME-OneFrame, Leica SCN (single-image), COG-WSI, and
DICOM-WSI.

### Format × command support

| Source format | `info` | `region` | `dump-ifds` | `extract`¹ | `hash`² | convert (from)³ | convert (to)⁴ | `downsample` |
|---|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
| SVS           | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | ✓ |
| Philips-TIFF  | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | — | — |
| OME-TIFF      | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | — |
| BIF           | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | — | — |
| generic-TIFF  | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | — |
| NDPI          | ✓ | ✓ | ✓ | ✓ | ✓ | ✓\* | — | — |
| OME-OneFrame  | ✓ | ✓ | ✓ | ✓ | ✓ | ✓\* | — | — |
| Leica SCN     | ✓ | ✓ | ✓ | ✓ | ✓ | ✓\* | — | — |
| COG-WSI       | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | — |
| IFE           | ✓ | ✓ | — | ✓ | ✓ | ✓  | — | — |
| DICOM-WSI     | ✓ | ✓ | — | ✓ | ✓⁵ | ✓ | —⁶ | — |

¹ `extract` works when the slide carries that associated image (label/macro/thumbnail/overview); run `info` to list which.
² `hash`: `--mode pixel` works for every format; the default file-mode is a single-file SHA-256.
³ **convert (from)** — readable as a convert source. **✓\*** = striped source: opentile-go synthesizes a tile grid over the source strips, so `convert` decodes + re-encodes (reproducible JPEG tiles) rather than doing a bit-exact tile-copy. The lossless tile-copy fast path applies only to natively-tiled sources (plain ✓).
⁴ **convert (to)** — available as a convert output **target**. The full target set is `cog-wsi`, `svs`, `tiff` (→ generic-TIFF), `ome-tiff`, `dzi`, `szi`; **DZI and SZI** are output-only pyramid formats (not readable sources, so not listed as rows).
⁵ DICOM directory input → use `--mode pixel` (file-mode is undefined for a multi-file series; a multi-series directory errors — see below).
⁶ DICOM-WSI **write** is planned (writer scoped, not yet built).

`downsample` is SVS-source-only (it emits a downsampled SVS).

Striped sources produce reproducible but synthesized JPEG tiles in the output
(bit-exact tile-copy applies only to natively-tiled sources).

**DICOM-WSI input.** A DICOM source may be either a single `.dcm` instance
or a directory containing a WSM series — pass the path to either. A named
`.dcm` always opens the series it belongs to (its siblings sharing the same
`SeriesUID`), even when the directory holds other slides. If a directory holds
more than one distinct WSM series, wsitools **refuses with an error** that lists
the candidate series; pass a specific `.dcm` of the slide you want to resolve
the ambiguity. (`dump-ifds` is TIFF-only and does not apply to
DICOM; use `hash --mode pixel` rather than the default file-mode for a DICOM
content hash.)

**Diagnostics**

- `wsitools doctor` — report installed codec libraries, physical RAM, and
  the active soft memory limit (see [Memory](#memory)).
- `wsitools version` — print version + Go runtime info.

A global `--max-memory` flag caps the process's soft memory limit (default
75% of RAM); see [Memory](#memory).

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

Conversion footprint scales with slide **width**, not a fixed ceiling.
`convert --to dzi|szi` streams top-to-bottom but holds full-width strip
buffers across every pyramid level plus the reader's per-tile decode
caches, so peak resident memory grows with the widest level:

- CMU-1.ndpi (L0 51200 × 38144): ~3.5 GB peak
- OS-2.ndpi  (L0 126976 × 73728): ~5.4 GB peak

`downsample` (v0.1) additionally materialises the full L0 raster (a 40x
L0 ≈ 100K × 60K × 3 ≈ 18 GB); a streaming retrofit is queued — see
[`docs/roadmap.md`](./docs/roadmap.md).

To keep a runaway conversion from exhausting the machine, wsitools sets a
**soft memory limit at 75% of physical RAM by default** (via Go's
`GOMEMLIMIT`). Under pressure the garbage collector works harder —
trading some speed — instead of letting the process OOM the host. Tune or
disable it:

```sh
# Cap the soft limit at 4 GiB (slower, lower peak)
wsitools --max-memory 4GiB convert --to dzi -o out.dzi in.ndpi

# Disable the cap entirely
wsitools --max-memory off convert --to dzi -o out.dzi in.ndpi

# GOMEMLIMIT env is respected and takes precedence over the default
GOMEMLIMIT=8GiB wsitools convert --to dzi -o out.dzi in.ndpi
```

Precedence: `--max-memory` > `GOMEMLIMIT` > 75% default. `wsitools doctor`
reports the active limit and its source. The reader's own decode-cache
budget is separately tunable via `OPENTILE_READ_MEMORY_BUDGET` (default
1 GiB; opentile-go v0.30+).

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
