# wsitools — TIFF Compression tag values

TIFF tag 259 (`Compression`) values used by wsitools when writing tiled
pyramidal TIFF / SVS / COG-WSI output. Standard values come from ISO,
Adobe, or community allocation; private values (≥ 32768) are
wsitools-assigned for codecs that lack a recognized tag value.

## Standard / community-allocated

| Codec | Tag | Source | Notes |
|---|---|---|---|
| None / uncompressed | 1 | TIFF 6.0 | |
| LZW | 5 | TIFF 6.0 | Used by Aperio for label associated images. |
| JPEG | 7 | TIFF 6.0 (Tech Note 2) | "New-style" JPEG-in-TIFF; not "OJPEG" (6). |
| Deflate | 8 | TIFF 6.0 + community | Adobe-allocated. |
| JPEG 2000 (Aperio) | 33003 / 33005 | Aperio | 33003 = YCbCr; 33005 = RGB. Aperio-private. |
| JPEG-LS | 34712 | ISO/IEC 14495 | Standard tag value; **encoder not yet shipped** (no `internal/codec/jpegls`). |
| WebP | 50001 | Adobe | Adobe-allocated; libtiff supports. |
| JPEG-XL | 50002 | Adobe (draft) | Allocated; spec finalized (ISO/IEC 18181, 2022). |

## wsitools private (≥ 32768)

| Codec | Tag | Notes |
|---|---|---|
| AVIF | 60001 | No standard TIFF tag. |
| HEIF | 60002 | No standard TIFF tag. |
| HTJ2K | 60003 | Could overlap JP2K (33003/33005); private to disambiguate. |
| JPEG-XR | 60004 | Microsoft has historically used 22610 for HD Photo; we're not bound to that. |
| Basis Universal | 60005 | Wrapped in KTX2 inside the tile bytes. |

These private values are only readable by wsitools-aware viewers and
decoders. `convert --to tiff` / `--to ome-tiff` with a non-standard codec
will produce files that openslide/QuPath won't open — that's the point;
they exist to feed test fixtures into viewers that understand these codecs
natively.

## Verification of standard values

Pinned against:
- `libtiff` `libtiff/tif_dir.h` (COMPRESSION_* constants).
- AwareSystems TIFF tag reference (https://www.awaresystems.be/imaging/tiff/tifftags/compression.html).

When opening any newly-written TIFF in `tiffinfo` (or with `wsitools
dump-ifds --raw`), the Compression line should match these values exactly.

## wsitools private TIFF tags

Tag values in the private range (≥ 32768) used by wsitools to make output
files self-describing. opentile-go reads these tags as authoritative for
WSI image classification when present.

| Tag | Name | Type | Where emitted | Purpose |
|---|---|---|---|---|
| 65080 | WSIImageType | ASCII | every IFD (pyramid + associated) | One of: `pyramid`, `label`, `macro`, `overview`, `thumbnail`, `probability`, `map`, `associated`. Authoritative for image classification. |
| 65081 | WSILevelIndex | LONG | pyramid IFDs only | 0-based pyramid level index (L0 = 0, L1 = 1, …). |
| 65082 | WSILevelCount | LONG | pyramid IFDs only | Total pyramid levels in this file. |
| 65083 | WSISourceFormat | ASCII | L0 only | The source format wsitools converted from (e.g. `svs`, `philips-tiff`, `ome-tiff`, `ndpi`). |
| 65084 | WSIToolsVersion | ASCII | L0 only | The wsitools version that produced this file (e.g. `0.20.0`). |
| 65085 | WSIMPPx | DOUBLE | L0 only | Microns per pixel, X axis. |
| 65086 | WSIMPPy | DOUBLE | L0 only | Microns per pixel, Y axis. |
| 65087 | WSIMagnification | DOUBLE | L0 only | Scanner objective magnification (e.g. `20`, `40`). |

The constants live in `internal/tiff/tags.go`. The reserved namespace is
65080–65087; all eight are assigned, and all have names in the
`internal/tiff/tagnames.go` dictionary so `dump-ifds --raw` shows them by
name.

## SVS writer tag profile

`convert --to svs` aims to produce genuine-Aperio-shaped SVS. The sample
fixtures split by producer, and the two producers emit different L0 tag
sets:

| Tag | Genuine Aperio | Grundium (Aperio-compatible) |
|---|---|---|
| ImageDepth (32997) | yes (= 1) | yes |
| YCbCrSubSampling (530) | yes, for JPEG data | yes |
| ICCProfile (34675) | sometimes | no |
| Orientation (274) | no | yes |
| XResolution/YResolution/ResolutionUnit (282/283/296) | **no** (MPP lives only in the Aperio `ImageDescription`) | yes |
| PageNumber (297) | no | yes |
| ReferenceBlackWhite (532) | no | yes |

The wsitools SVS writer emits, on L0:

- **ImageDepth (32997) = 1** — always. wsitools produces 2D output only.
- **YCbCrSubSampling (530)** — only when the output is JPEG-compressed. The
  value describes the **actual** chroma subsampling of the JPEG bytes
  wsitools writes, not a copied or assumed constant: on the lossless
  tile-copy path it is parsed from a real source tile's SOF marker; on the
  re-encode path it is probed from the encoder's own output. This means the
  emitted value can differ from the source file's declared 530 tag. For
  example, genuine Aperio `CMU-1-Small-Region.svs` declares `530 = [2,2]`
  but its tiles are actually RGB/4:4:4, so a lossless `--to svs` tile-copy
  emits `[1,1]`; re-encoding the same slide with wsitools' YCbCr 4:2:0 JPEG
  encoder (`--codec jpeg`) emits `[2,2]`. Because the writer sets
  `PhotometricInterpretation = RGB (2)` for new-style JPEG-in-TIFF, tag 530
  is **informational** — conformant decoders read color from the JPEG
  codestream and ignore 530 — so describing the real bytes has no effect on
  decode or color, only on fidelity to what was written.
- **ICCProfile (34675)** — when the source carries one (pulled via
  opentile's `Slide.ICCProfile()`).
- Plus the wsitools-generated scale tags (XResolution/YResolution/
  ResolutionUnit from MPP, and the WSI private MPP/magnification tags).

**Non-goals (deliberately not emitted for Aperio conformance):** the
Grundium-only tags Orientation (274), PageNumber (297), and
ReferenceBlackWhite (532). Note wsitools *does* generate resolution tags
(282/283/296) from MPP — a readability aid that genuine Aperio omits; that
deviation is intentional and tracked separately.

## Reading tags from a slide

`wsitools dump-ifds --raw <file>` emits every TIFF tag per IFD with names,
type, count, value, and enum interpretation for well-known fields
(Compression, PhotometricInterpretation, PlanarConfiguration, etc.). The
tag-name dictionary lives in `internal/tiff/tagnames.go` and covers ~100
well-known tags (baseline + extended TIFF, GeoTIFF, EXIF pointers, ICC,
Adobe codec extensions, and wsitools' private range).

## DICOM-WSI alignment

The WSIImageType vocabulary aligns with DICOM Whole Slide Imaging (PS3.3
Sup. 145), which uses VOLUME / LABEL / OVERVIEW / THUMBNAIL as standard
ImageType values for WSI files. We use lowercase + the additional values
`pyramid`, `macro`, `probability`, `map`, `associated` to match
opentile-go's existing AssociatedImage.Kind() vocabulary.
