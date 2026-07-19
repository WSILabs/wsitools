# wsitools

[![CI](https://github.com/wsilabs/wsitools/actions/workflows/ci.yml/badge.svg)](https://github.com/wsilabs/wsitools/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/wsilabs/wsitools.svg)](https://pkg.go.dev/github.com/wsilabs/wsitools)

A command-line toolkit for whole-slide imaging (WSI) files in digital
pathology — **inspect, edit, and convert** slides across every major vendor
format, from a single static binary with no runtime dependencies.

wsitools is built for pathology pipelines: edit associated images (like the
label) without re-encoding the pyramid, transcode between containers and codecs,
generate DeepZoom tiles for web viewers, downsample or crop slides, and check
them for structural conformance.

> [!WARNING]
> **Pre-1.0 — expect breaking changes.** CLI flags, JSON fields, and output
> details may change between releases without deprecation. Pin a version and
> review [`CHANGELOG.md`](./CHANGELOG.md) before upgrading.

## What it does

- **Inspect** — slide summary (levels, codecs, colorspace, metadata), per-IFD
  and per-tag dumps, content hashing, and structural validation.
  → `info`, `dump-ifds`, `hash`, `validate`
- **Extract a pixel region** — save a rectangular region as a standalone PNG.
  → `region`
- **Extract an associated image** — save the label, macro, thumbnail, or
  overview as a standalone PNG or JPEG.
  → `extract`
- **Edit** — remove or replace an associated image. The pyramid tile bytes are
  copied verbatim (no decode/re-encode).
  → `label|macro|thumbnail|overview  remove|replace`
- **Convert & transform** — reshape a slide, streaming (no full-resolution
  raster in memory):
    - *Re-container* into another format — a lossless tile-copy when the source
      codec is compatible with the target.
      → `convert --to {cog-wsi, svs, tiff, ome-tiff, dzi, szi, dicom, bif, ife}`
    - *Transcode* the pyramid to a different codec.
      → `convert --codec {jpeg, jpeg2000, jpegxl, avif, webp, htj2k}`
    - *Downsample* to a lower magnification — during a convert, or in place
      (same container out).
      → `convert --factor N` / `--target-mag M`, or `downsample`
    - *Crop* a region — during a convert, or in place.
      → `convert --rect X,Y,W,H`, or `crop`
- **App info** — report installed codecs and memory limits, or print the version.
  → `doctor`, `version`

Full per-command reference: **[docs/commands.md](docs/commands.md)**.

> [!NOTE]
> Editing out the label is often a step in a **de-identification** workflow, but
> wsitools edits *images* — it does not, on its own, de-identify a slide. Other
> PHI may remain in slide metadata or in images you didn't remove. Removing an
> image deletes it from the output file; the rest of the slide is your
> responsibility.

## Supported formats

**Reads:** Aperio SVS · Hamamatsu NDPI · Philips-TIFF · OME-TIFF · DICOM-WSI ·
Ventana/Roche BIF · Iris IFE · Leica SCN · generic tiled TIFF · COG-WSI

**Writes:** SVS · generic TIFF · OME-TIFF · COG-WSI · DICOM-WSI · DeepZoom (DZI) ·
SZI · BIF · IFE

| Source format | `info` | `region` | `dump-ifds` | `extract` | `hash` | convert *from* | convert *to* | downsample / crop | edit |
|---|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
| SVS           | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | ✓ | ✓ |
| Philips-TIFF  | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | — | — | — |
| OME-TIFF      | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | ✓ | ✓ (lossy) |
| BIF           | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | — | — | — |
| generic-TIFF  | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | ✓ | ✓ |
| NDPI          | ✓ | ✓ | ✓ | ✓ | ✓ | ✓* | — | — | — |
| Leica SCN     | ✓ | ✓ | ✓ | ✓ | ✓ | ✓* | — | — | — |
| COG-WSI       | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | ✓ | ✓ |
| IFE           | ✓ | ✓ | — | ✓ | ✓ | ✓  | — | — | — |
| DICOM-WSI     | ✓ | ✓ | — | ✓ | ✓ | ✓  | ✓ | ✓ | — |

<sub>**✓\*** stripped source — decoded and re-encoded into reproducible JPEG tiles
rather than bit-exact tile-copied. DICOM-WSI writing is experimental. Full matrix
(including OME-OneFrame), convert targets, and per-format caveats:
**[docs/formats.md](docs/formats.md)**.</sub>

## Install

### Prebuilt binaries (recommended)

Download the archive for your platform from the
[latest release](https://github.com/WSILabs/wsitools/releases/latest), extract,
and run `wsitools`. Every binary is statically linked and bundles all codecs
(jpeg, jpeg2000, htj2k, jpegxl, avif, webp) — no toolchain or libraries needed.
Verify with `sha256sum -c SHA256SUMS`.

| Platform | Asset |
|---|---|
| Linux x86-64 / arm64 | `wsitools-linux-{amd64,arm64}.tar.gz` |
| macOS Apple Silicon / Intel | `wsitools-darwin-{arm64,amd64}.tar.gz` |
| Windows x86-64 | `wsitools-windows-amd64.zip` |

> macOS binaries are **not yet signed or notarized**. On first run, clear the
> quarantine flag with `xattr -d com.apple.quarantine wsitools`, or allow the
> binary in **System Settings → Privacy & Security**. Signing + notarization is
> planned — see the [roadmap](docs/roadmap.md).

### From source

With **Go 1.26+** and the image codec libraries installed (JPEG is the only
required one; the rest are optional):

```sh
go install github.com/wsilabs/wsitools/cmd/wsitools@latest
```

Step-by-step instructions for macOS, Linux, and Windows — including a JPEG-only
minimal build and how to skip individual codecs — are in
**[docs/INSTALL.md](docs/INSTALL.md)**.

## Quick start

```sh
# Slide summary (add --json for scripting)
wsitools info slide.svs

# Extract a pixel region as PNG
wsitools region --x 10000 --y 8000 --w 1024 --h 1024 --level 0 -o tile.png slide.svs

# Remove the label image — pyramid bytes untouched, no re-encode
wsitools label remove slide.svs            # → slide_relabeled.svs

# Re-container into COG-WSI (lossless tile-copy when the source codec fits the target)
wsitools convert --to cog-wsi -o slide.cog.tiff slide.svs

# Generate DeepZoom tiles for a web viewer (OpenSeadragon-compatible)
wsitools convert --to dzi -o slide.dzi slide.svs

# Crop a region into the same container (--lossless keeps L0 tiles byte-identical)
wsitools crop --rect 20000,15000,8192,8192 --lossless -o region.svs slide.svs

# Downsample a 40× slide to 20× (same container out)
wsitools downsample -o slide-20x.svs slide-40x.svs
```

More examples and every flag: **[docs/commands.md](docs/commands.md)**.

## Documentation

- **[Command reference](docs/commands.md)** — every command, flag, and output layout
- **[Format support](docs/formats.md)** — full format × command matrix and caveats
- **[Installation](docs/INSTALL.md)** — per-platform builds, codec selection
- **[Memory & performance](docs/memory.md)** — footprint and the `--max-memory` limit
- **[OME-TIFF limitations](docs/ome-tiff-limitations.md)** — what the minimal writer carries
- **[Roadmap](docs/roadmap.md)** — planned utilities, formats, and signed macOS builds
- **[Changelog](CHANGELOG.md)** — release notes

## Contributing & testing

```sh
make test   # unit + integration tests, race detector
make vet
```

Integration tests are fixture-gated by `WSI_TOOLS_TESTDIR`; CI pulls fixtures
from [`wsilabs/wsi-fixtures`](https://github.com/wsilabs/wsi-fixtures) on every
push and PR.

## License

Apache 2.0. See [`LICENSE`](./LICENSE).
