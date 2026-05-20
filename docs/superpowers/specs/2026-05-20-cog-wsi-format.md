# COG-WSI Format Specification

**Version:** 0.1
**Status:** Draft
**Date:** 2026-05-20

## 1. Overview

COG-WSI is a file format for whole-slide images (WSI) used in digital pathology.
It is a strict extension of the **Cloud Optimized GeoTIFF (COG)** layout: a valid
COG-WSI file is structured so a standard COG reader can locate, parse, and read
its pyramid tiles, with WSI-specific extension tags ignored as unrecognized
metadata.

The format exists because pathology pyramids need (a) the cloud range-fetch
properties of COG and (b) tagged associated images (label, macro, thumbnail,
overview) plus scanner provenance metadata that the GeoTIFF COG spec does not
address.

This document is the normative specification for COG-WSI v0.1. The
`wsitools convert --to cog-wsi` command is the reference writer; readers
include `opentile-go`'s generic-TIFF reader (which treats COG-WSI as a tagged
pyramidal TIFF).

## 2. Conformance

A file is a **valid COG-WSI v0.1 file** if and only if all requirements in this
specification marked **MUST** are satisfied. Optional requirements are marked
**MAY** or **SHOULD**.

## 3. Container

### 3.1 Byte order and TIFF flavor

- Byte order MUST be little-endian (`II` magic, `0x4949`).
- The file MUST be either:
  - Classic TIFF (`0x002A` version) if total tile bytes + metadata ≤ 2 GiB; or
  - BigTIFF (`0x002B` version, 8-byte offsets) otherwise.
- Writers MUST auto-promote to BigTIFF when the predicted output size exceeds
  the classic-TIFF 4 GiB ceiling (with a 2 GiB safety margin).

### 3.2 File layout

A conforming file MUST be laid out in this order, head to tail:

```
[ TIFF header (8 or 16 bytes) ]
[ Ghost area (COG_GHOST_AREA, see §4)              ]
[ IFD 0 (full-resolution pyramid)                  ]
[ IFD 1..N (pyramid overviews, decreasing res)     ]
[ IFD N+1..M (associated images, see §6)           ]
[ Per-IFD external tag data (TileOffsets,          ]
[   TileByteCounts, JPEGTables, ImageDescription)  ]
[ Tile data — smallest-resolution overview first   ]
[ ...                                              ]
[ Tile data — full-resolution last                 ]
[ Associated-image data                            ]
```

- All IFDs and their external tag arrays (notably `TileOffsets` and
  `TileByteCounts`) MUST be packed contiguously immediately after the ghost
  area. A range request of the first 16 KiB SHOULD suffice to obtain the full
  index for typical pyramids (≤ ~10 levels).
- Tile data MUST appear in the **reverse** of IFD order: the smallest
  overview's tile data comes first, the full-resolution level's tile data
  last. Within a level, tiles are stored in row-major order (y-major, then
  x).
- Associated-image data MUST appear after all pyramid tile data.
- Tile offsets MUST be aligned to 16 bytes (COG convention). Padding bytes
  between tiles, if needed, MUST be zero.

### 3.3 Strict supersedure of COG (with one exception)

The COG layout requirements (header-front IFDs, monotonically increasing tile
offsets within a level, tile alignment, ghost area) MUST be honored for the
**pyramid IFDs**. Pyramid IFDs MUST be tiled and MUST NOT use strips.

**Exception:** associated-image IFDs (§6) MAY use strips when the source
encoded them as strips. This is a deliberate departure from strict COG to
preserve the bit-exact-copy guarantee for label/macro/thumbnail/overview
images, which are commonly strip-encoded in SVS and other source formats.
This exception does not affect pyramid streaming over HTTP because associated
images are placed at the file tail and never touched during pyramid reads.

A COG-WSI file SHOULD pass a standard COG validator restricted to its
pyramid IFDs, with WSI extension tags treated as unrecognized private
metadata and associated IFDs treated as additional reduced-resolution
images.

## 4. Ghost area

Immediately after the TIFF header, the file MUST contain a "ghost area" — a
contiguous block of ASCII key-value metadata that conforming readers can parse
without walking IFDs. The ghost area follows the GDAL convention used by
standard COG.

### 4.1 Format

```
GDAL_STRUCTURAL_METADATA_SIZE=NNNNNN bytes
LAYOUT=IFDS_BEFORE_DATA
BLOCK_ORDER=ROW_MAJOR
BLOCK_LEADER=SIZE_AS_UINT4
BLOCK_TRAILER=LAST_4_BYTES_REPEATED
KNOWN_INCOMPATIBLE_EDITION=NO
COG_WSI_VERSION=0.1
```

- The size declared in `GDAL_STRUCTURAL_METADATA_SIZE` MUST equal the byte
  length of the ghost area excluding the size line itself, formatted as
  six ASCII digits followed by ` bytes` (matching GDAL).
- The `COG_WSI_VERSION` line is mandatory and identifies this file as a
  COG-WSI file conforming to the named version.
- Additional keys MAY appear; readers MUST ignore unknown keys.

### 4.2 Versioning

`COG_WSI_VERSION` follows semver-style numbering. The minor version (0.x) is
bumped for backward-compatible additions (new optional tags, new associated
image kinds). The major version is bumped only for breaking changes (new
required tags, layout changes that break existing readers).

## 5. Pyramid IFDs

Each pyramid IFD MUST be tiled (no strips) and MUST carry the following tags:

| Tag                            | TIFF ID | Required value / constraint                |
|--------------------------------|---------|--------------------------------------------|
| `ImageWidth`                   | 256     | Level width in pixels.                     |
| `ImageLength`                  | 257     | Level height in pixels.                    |
| `BitsPerSample`                | 258     | Per source.                                |
| `Compression`                  | 259     | Preserved verbatim from source.            |
| `PhotometricInterpretation`    | 262     | Preserved verbatim from source.            |
| `SamplesPerPixel`              | 277     | Per source.                                |
| `PlanarConfiguration`          | 284     | 1 (chunky). Planar sources rejected.       |
| `TileWidth`                    | 322     | Preserved from source.                     |
| `TileLength`                   | 323     | Preserved from source.                     |
| `TileOffsets`                  | 324     | Computed during finalize.                  |
| `TileByteCounts`               | 325     | Per-tile byte length from source.          |
| `NewSubfileType`               | 254     | 0 for the full-resolution IFD; 1 for overview IFDs (reduced-resolution image). |
| `WSIImageType` (private)       | 65080   | ASCII `pyramid`.                           |
| `WSILevelIndex` (private)      | 65081   | LONG; 0-based pyramid level index.         |
| `WSILevelCount` (private)      | 65082   | LONG; total pyramid level count.           |

`JPEGTables` (tag 347) MUST be preserved when the source IFD used it
(abbreviated-JPEG mode). Tile streams MUST remain abbreviated; writers MUST
NOT promote them to standalone JPEG.

> **Note on cloud optimization.** Preserving `JPEGTables` does not defeat
> COG's range-fetch model. A COG-aware client always performs an initial
> head-range fetch (~16 KiB) to read the IFD index; the `JPEGTables` value
> lives inside that block and is parsed once and cached. Subsequent tile
> range fetches return abbreviated tile bytes which the client decodes by
> prepending the cached `JPEGTables`. No additional network round-trip per
> tile is incurred. Standalone tiles would actually waste bandwidth by
> repeating ~500–700 bytes of JPEG headers per tile (significant for
> pyramids with tens of thousands of tiles). GDAL's COG driver itself
> emits abbreviated tiles with `JPEGTables` by default; every TIFF-aware
> WSI reader (Aperio, OpenSlide, libtiff, opentile-go) handles this
> mode. COG-WSI follows the same convention.

### 5.1 IFD ordering

- IFD 0 is the full-resolution pyramid level.
- IFDs 1..N are overviews, ordered by **decreasing resolution** (largest
  overview first, smallest last).
- Pyramid IFD count and per-level dimensions MUST equal the source's.

### 5.2 Metadata tags (L0 / pyramid IFD 0 only)

Standard TIFF metadata tags MAY appear on the L0 IFD when known from
source; readers MUST treat absence as "unknown":

| Tag                  | TIFF ID | Type   | Meaning                                  |
|----------------------|---------|--------|------------------------------------------|
| `Make`               | 271     | ASCII  | Scanner manufacturer.                    |
| `Model`              | 272     | ASCII  | Scanner model.                           |
| `Software`           | 305     | ASCII  | Scanner software string.                 |
| `DateTime`           | 306     | ASCII  | Acquisition datetime, `YYYY:MM:DD HH:MM:SS`. |
| `ImageDescription`   | 270     | ASCII  | Optional `wsitools/<version> convert source=<fmt>` provenance string. Readers MUST NOT rely on this for machine-readable metadata. |

The following wsitools private tags (range ≥ 65000) MAY appear:

| Tag                | TIFF ID | Type   | IFDs        | Meaning                              |
|--------------------|---------|--------|-------------|--------------------------------------|
| `WSIImageType`     | 65080   | ASCII  | every IFD   | Image kind (see §5, §6).             |
| `WSILevelIndex`    | 65081   | LONG   | pyramid only| 0-based pyramid level index.         |
| `WSILevelCount`    | 65082   | LONG   | pyramid only| Total pyramid level count.           |
| `WSISourceFormat`  | 65083   | ASCII  | L0 only     | Original source container (`svs`, `philips`, etc.). |
| `WSIToolsVersion`  | 65084   | ASCII  | L0 only     | The wsitools version that wrote the file. |
| `WSIMPPX`          | 65085   | DOUBLE | L0 only     | Microns per pixel, X axis.           |
| `WSIMPPY`          | 65086   | DOUBLE | L0 only     | Microns per pixel, Y axis.           |
| `WSIMagnification` | 65087   | DOUBLE | L0 only     | Optical magnification (e.g. 40.0).   |

Tag IDs 65080–65084 are already reserved and emitted by `internal/wsiwriter`
in the current wsitools codebase. Tag IDs 65085–65087 are introduced by this
specification.

The `ImageDescription` tag (270) MAY be present and SHOULD contain a
`wsitools/<version> convert source=<fmt>` provenance string; readers MUST NOT
rely on it for machine-readable metadata.

## 6. Associated-image IFDs

Associated images (label, macro, thumbnail, overview) MUST be encoded as TIFF
IFDs appearing **after** all pyramid IFDs in the IFD chain, with their tile or
strip data placed at the file tail (after all pyramid tile data).

Each associated IFD MUST carry:

| Tag             | Required value                                                |
|-----------------|----------------------------------------------------------------|
| `Compression`   | Preserved verbatim from source.                                |
| `NewSubfileType`| 1 (reduced-resolution image).                                  |
| `WSIImageType`  | ASCII one of: `label`, `macro`, `thumbnail`, `overview`.       |

Associated images MAY be tiled or strip-encoded (both are allowed by COG-WSI,
unlike pyramid IFDs which MUST be tiled). They preserve their source
compression and dimensions bit-exact.

A conforming reader MUST NOT confuse associated IFDs with pyramid overviews;
the `WSIImageType` tag and post-pyramid IFD position both serve to
disambiguate.

## 7. Behavior of conforming readers

Readers that recognize COG-WSI:

1. SHOULD detect the format by parsing the ghost area and looking for
   `COG_WSI_VERSION`.
2. SHOULD walk the IFD chain in order: IFDs with `WSIImageType=pyramid`
   constitute the pyramid (in decreasing resolution order); IFDs with other
   `WSIImageType` values are associated images.
3. MAY treat an unknown `COG_WSI_VERSION` minor as readable but warn; MUST
   refuse to parse an unknown major version.

Readers that do not recognize COG-WSI (e.g. a stock GeoTIFF COG reader) SHOULD
still be able to read the pyramid as a generic COG pyramid by ignoring private
tags and treating associated IFDs as additional reduced-resolution overlays
(or filtering them out via `NewSubfileType`).

## 8. Open questions (non-normative)

The following are deliberately deferred and may be addressed in future
versions:

- **OME-style channel metadata** for multi-channel slides — out of scope for
  v0.1, which targets brightfield RGB.
- **Tile checksums** — not in the ghost area; could be added under a future
  `BLOCK_CHECKSUM=` key.
- **Cross-file references** (e.g., external `.zattrs`) — out of scope.

## 9. References

- GDAL Cloud Optimized GeoTIFF specification.
- TIFF 6.0 specification (Adobe).
- BigTIFF specification.
- Aperio SVS image description conventions (referenced by `opentile-go`).
