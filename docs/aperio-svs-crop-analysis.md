# How Aperio ImageScope Crops an SVS — reverse-engineering analysis

**Date:** 2026-06-11
**Subject:** `CMU-2.svs` (original) vs `CMU-2_cropped_46492_3599_27836_25633_imagescope.svs`
(cropped in Aperio ImageScope)
**Method:** structural dump, decoded-pixel comparison, and byte-level
passthrough test via `opentile-go`.

---

## TL;DR

**ImageScope crops by full re-encode.** It decodes the exact requested pixel
region and writes a brand-new tiled JPEG pyramid anchored at the crop's own
(0,0). It does **not** snap to tiles and does **not** splice/passthrough any
original tiles. It accepts one JPEG re-encode generation as the price of an
exact-pixel crop with a clean tile-aligned output and a trivial origin story.

This matters for wsitools: if we want **lossless** cropping we cannot match
Aperio's exact-pixel behaviour — we must either snap to tile boundaries
(lossless, approximate extent) or re-encode at least the edge tiles
(exact, partially lossy). See [Design implications](#design-implications-for-wsitools).

---

## The crop request

The filename encodes the crop in original level-0 coordinates:

```
x = 46492   y = 3599   w = 27836   h = 25633
```

Crucially, the **origin is not tile-aligned**:

```
46492 mod 256 = 156      3599 mod 256 = 15
```

So the new tile grid (anchored at the crop's 0,0) is shifted 156px / 15px off
the original grid — **every** crop tile straddles 4 original tiles. Passthrough
of any tile is mathematically impossible at this origin.

---

## Proof it re-encodes (three independent checks)

1. **Origin math (above):** non-tile-aligned origin ⇒ no tile can be reused.

2. **Pixels match, content-identical, with re-encode noise.** Decoded crop
   interior regions vs the mapped original region
   (`crop(x,y)` vs `orig(46492+x, 3599+y)`):

   | crop region | orig region | mean abs diff | max abs diff |
   |---|---|---:|---:|
   | (256,256) | (46748,3855) | 0.595 | 8 |
   | (1024,1024) | (47516,4623) | 0.609 | 8 |
   | (4096,2048) | (50588,5647) | 0.234 | 10 |

   ~0.2–0.6 mean / 8–10 max per channel is the exact signature of **one JPEG
   re-encode generation** of the same content (lossless passthrough would be 0;
   a wrong region would be huge).

3. **Compressed bytes differ.** Crop tile (1,1)'s JPEG bytes match **no**
   nearby original tile — it is a freshly-encoded composite, not a spliced
   original.

---

## Structural changes

| Property | Original | Cropped |
|---|---|---|
| L0 size | 78000 × 30462 | **27836 × 25633** (exact request) |
| Pyramid levels | 4 (L0–L3) | **3 (L0–L2)** — dropped the smallest |
| Downsamples | 1, 4, 16, 32 | 1, 4, 16 |
| Tile size | 256 × 256 | 256 × 256 (unchanged) |
| Tile grid (L0) | 305 × 119 | 109 × 101 |
| Codec / quality | JPEG/RGB Q=30 | JPEG/RGB Q=30 (unchanged) |
| File size | 372 MB | 167 MB |

**Partial edge tiles:** crop L0 grid 109 × 101 over 27836 × 25633 ⇒ the far
column is `27836 − 108·256 = 188` px wide and the bottom row is
`25633 − 100·256 = 33` px tall. The crop is **not** padded out to a tile
multiple — edge tiles are partial (Aperio stores them padded to 256 internally
but the advertised image dimensions are the exact crop).

**Associated images:**

| Image | Original | Cropped | Treatment |
|---|---|---|---|
| thumbnail | 1024 × 399 JPEG | **834 × 768 JPEG** | **regenerated** (aspect 834/768 = 1.086 matches crop 27836/25633 = 1.086) |
| label | 387 × 463 LZW | 387 × 463 LZW | **passthrough** (unchanged) |
| overview | 1280 × 431 JPEG | 1280 × 431 JPEG | **passthrough** (unchanged) |

The label and overview are whole-slide context images and survive a crop
verbatim; the thumbnail is a render of the (now-cropped) main image and is
re-generated to the crop's aspect ratio.

**ICC profile:** 141,992 bytes on L0 of **both** files — preserved /
passed through unchanged ("ScanScope v1").

**Standard TIFF Software / Make / Model tags:** absent in both — Aperio
encodes all provenance in `ImageDescription` (tag 270), not the standard tags.

---

## Metadata changes — `ImageDescription` (tag 270), field by field

Aperio's `ImageDescription` format is:

```
Aperio Image Library v<VER>
<OrigW>x<OrigH> [<x>,<y> <W>x<H>] (<tileW>x<tileH>) <codec> Q=<q>|key = value|key = value|...
```

### Original (v10.0.51)

```
Aperio Image Library v10.0.51
79560x30562 [0,100 78000x30462] (256x256) JPEG/RGB Q=30|AppMag = 20|StripeWidth = 2040|
ScanScope ID = CPAPERIOCS|Filename = CMU-2|Date = 12/29/09|Time = 10:02:42|
User = b414003d-...|Parmset = USM Filter|MPP = 0.4990|Left = 27.409658|Top = 20.522137|
LineCameraSkew = -0.000424|LineAreaXOffset = 0.019265|LineAreaYOffset = -0.000313|
Focus Offset = 0.000000|ImageID = 1004487|OriginalWidth = 79560|Originalheight = 30562|
Filtered = 5|ICC Profile = ScanScope v1
```

(Note the original "capture" was `79560x30562`, with the pyramid base
`78000x30462` sitting at offset `[0,100]` inside it.)

### Cropped (v12.4.7)

```
Aperio Image Library v12.4.7
78000x30462 [46492,3599 27836x25633] (256x256) JPEG/RGB Q=30;Aperio Image Library v10.0.51
79560x30562 [0,100 78000x30462] (256x256) JPEG/RGB Q=30|AppMag = 20|...(original block verbatim)...|
ImageID = 1004487|OriginalWidth = 79560|Originalheight = 30562|Filtered = 5|
OriginalWidth = 78000|OriginalHeight = 30462|ICC Profile = ScanScope v1
```

### What changed

| Field | Original | Cropped | Notes |
|---|---|---|---|
| Library version (header line) | `v10.0.51` | **`v12.4.7`** | identity of the cropping tool, not the scanner |
| **New geometry line (prepended)** | — | **`78000x30462 [46492,3599 27836x25633] (256x256) JPEG/RGB Q=30;`** | first pair = the base it was cropped *from*; `[x,y w×h]` = the crop region; terminated by `;` |
| **Provenance chain** | — | original v10.0.51 header + geometry + full field block appended verbatim after the `;` | crop history is preserved, not discarded |
| `OriginalWidth` / `OriginalHeight` | `79560` / `30562` (`Originalheight`, lower-h) | **adds** `OriginalWidth = 78000` / `OriginalHeight = 30462` (capital H) before `ICC Profile` | the *original* pair is kept; a *new* pair (= pre-crop base dims) is appended |
| `MPP` | `0.4990` | `0.4990` | **unchanged** — resolution preserved |
| `AppMag` | `20` | `20` | unchanged |
| `Left` / `Top` (stage origin, mm) | `27.409658` / `20.522137` | `27.409658` / `20.522137` | **⚠️ UNCHANGED** — the physical stage origin is *not* rewritten for the crop (see caveat below) |
| `ImageID` | `1004487` | `1004487` | unchanged — same image id retained |
| `Filename` | `CMU-2` | `CMU-2` | unchanged |
| `ScanScope ID`, `Date`, `Time`, `User`, `Parmset`, `StripeWidth`, `LineCameraSkew`, `LineAreaXOffset`, `LineAreaYOffset`, `Focus Offset`, `Filtered` | … | … (inherited verbatim) | unchanged |

**⚠️ Caveat on `Left`/`Top`:** Aperio does **not** update `Left`/`Top` (the
slide-coordinate origin in mm) after a crop — they still point at the original
scan origin. The crop offset lives **only** in the new `[46492,3599 …]`
geometry token. Any consumer that needs the crop's true physical origin must
derive it (`Left + 46492·MPP/1000`, `Top + 3599·MPP/1000`) rather than trust
`Left`/`Top`. This is the "origin issue" in concrete form: even Aperio's own
metadata leaves the physical anchor stale and relies on the geometry token.

---

## Downstream consumption — does anything actually read `Left`/`Top`?

Verified against the two dominant consumers (OpenSlide and QuPath, which reads
SVS *through* OpenSlide). **Neither uses `Left`/`Top` for anything.**

### OpenSlide — ignores them
- The Aperio format docs state it outright: *"Currently, OpenSlide does not use
  any of the information present in these key-value fields."*
- Source-confirmed (`openslide-vendor-aperio.c`): there are **no references to
  `Left` or `Top`** in the driver. The only derived properties are
  `aperio.AppMag → openslide.objective-power` and
  `aperio.MPP → openslide.mpp-x / mpp-y`.
- It does **not** set `openslide.bounds-x / bounds-y` for Aperio. `Left`/`Top`
  are surfaced verbatim as `aperio.Left` / `aperio.Top` and never consumed.

### QuPath — also ignores them; relies on `openslide.bounds-*`
- `OpenslideImageServer.java` reads `openslide.mpp-x/y`, `objective-power`,
  level-0 tile size, background color, and the **`bounds-x/y/width/height`**
  set.
- It applies `bounds-x/y` as the **origin offset** for tile reads
  (`tileX = imageX + boundsX`), rebuilding the image dimensions to
  `boundsWidth/Height` when bounds are present.
- It **never references `aperio.Left` / `aperio.Top`.**

### The consequence for cropping

The crop origin is consumable downstream **only** via `openslide.bounds-x/y` —
and **OpenSlide's Aperio driver never sets bounds.** (It only emits bounds for
formats whose own metadata declares a scanned-region offset — MIRAX, Hamamatsu,
Leica SCN — never for SVS.) So:

1. To OpenSlide and QuPath, a cropped SVS is a **standalone image at pixel
   origin (0,0)** with MPP preserved. Aperio's `[46492,3599 …]` geometry token
   and `aperio.Left/Top` are both **dead-ends for geometry** in this toolchain
   — only ImageScope itself parses the geometry token.
2. **Whether wsitools rewrites `Left`/`Top` is therefore cosmetic for
   OpenSlide/QuPath.** Doing it is "more correct" (helps Aperio-native tooling
   and human inspection) but changes nothing for the dominant consumers. The
   only hazard is *half*-updating — keep the geometry token, `Left`/`Top`, and
   the appended `OriginalWidth/Height` mutually consistent or leave them all as
   Aperio does.
3. **There is no SVS field that tells OpenSlide/QuPath a crop came from
   `(46492,3599)` of a parent slide.** If wsitools needs annotation
   round-tripping back to the parent, it cannot live in SVS metadata those
   tools read — it must go in a sidecar or the consuming app's project
   metadata.

Sources: [OpenSlide Aperio format](https://openslide.org/formats/aperio/) ·
[openslide-vendor-aperio.c](https://github.com/openslide/openslide/blob/main/src/openslide-vendor-aperio.c) ·
[QuPath OpenslideImageServer.java](https://github.com/qupath/qupath/blob/main/qupath-extension-openslide/src/main/java/qupath/lib/images/servers/openslide/OpenslideImageServer.java)

---

## Design implications for wsitools

Aperio chose **exactness over losslessness** — it eats one re-encode to get an
exact-pixel crop anchored at (0,0), which makes the tile grid and the output
origin trivial. The wsitools option space:

| Approach | Tiles | Extent | Lossless? | Origin handling |
|---|---|---|---|---|
| **A. Full re-encode** (what Aperio does) | all newly encoded | exact pixels | ❌ one JPEG generation (~0.5 mean / ~10 max abs diff) | clean: output origin = (0,0); record crop offset in metadata |
| **B. Snap origin ↓ + extent ↑ to tile grid** | **all passthrough** | a tile-aligned **superset** (≤255 px slop per edge) | ✅ fully lossless | rewrite origin to the *snapped* boundary |
| **C. Snap origin ↓, keep exact extent** | interior passthrough; **right + bottom** edge tiles re-encoded | exact | mostly (edge tiles only) | rewrite origin to the *snapped* boundary |

Key constraints:

- **You can only passthrough if the origin is snapped *down* to a tile
  boundary.** Otherwise the entire grid shifts and nothing is reusable (exactly
  what happened here). There is no four-sided clean partial-edge crop without
  re-encoding — the top-left tile would itself be mid-tile.
- **B and C must rewrite the origin metadata** to the snapped boundary
  (and, for true correctness, the physical `Left`/`Top` — which, note, Aperio
  itself does not bother to do).
- **A** is the only option that reproduces Aperio's exact dimensions and clean
  origin; these two files are a ready-made **parity oracle** for it (verify
  wsitools' crop decodes to ≤~10 max abs diff against the ImageScope crop).

### Recommended metadata to write on a wsitools crop (mirroring Aperio)

1. Prepend a new geometry line: `<baseW>x<baseH> [<x>,<y> <W>x<H>] (<tileW>x<tileH>) <codec> Q=<q>;`
2. Append the source `ImageDescription` verbatim after `;` (provenance chain).
3. Append a fresh `OriginalWidth`/`OriginalHeight` = pre-crop base dims.
4. Keep `MPP`, `AppMag`, `ImageID`, `Filename`, scanner fields unchanged.
5. Regenerate the thumbnail to the crop aspect; pass label + overview + ICC
   through unchanged.
6. `Left`/`Top`: **safe to leave as Aperio does** — neither OpenSlide nor
   QuPath reads them (see [Downstream consumption](#downstream-consumption--does-anything-actually-read-leftop)).
   Rewriting them to the crop origin is optional polish for Aperio-native
   tooling; if you do, keep them consistent with the geometry token and the
   appended `OriginalWidth/Height` (don't half-update).

---

*Generated from a direct comparison of the two SVS files using opentile-go
(structural dump + decoded-pixel diff + tile-byte passthrough test).*
