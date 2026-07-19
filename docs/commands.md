# Command reference

Full reference for every `wsitools` command. For the format support matrix see
[formats.md](formats.md); for memory tuning see [memory.md](memory.md).

Global flags (accepted by every command):

- `--max-memory <size|off>` — soft memory limit (default 75% of RAM). See
  [memory.md](memory.md).
- `--quiet` — suppress the progress bar (useful in CI / scripts).
- `--verbose` — per-level timing summaries on stderr.
- `--log-format json` — structured JSON logging for log aggregators.

---

## Inspection

### `info`
Slide summary: format, pyramid levels (dimensions, tile size, compression, and
a per-level quality readout — effective colorspace, bit depth, chroma
subsampling, quality estimate), associated images, and scanner metadata
(make/model/serial/software/MPP/magnification/ICC presence). Analog of
`openslide-show-properties`.

```sh
wsitools info slide.svs
wsitools info --json slide.svs | jq .levels     # machine-readable
wsitools info --properties slide.svs            # full vendor property bag (text mode)
```

### `dump-ifds`
Format-aware per-IFD layout dump. Annotates each IFD with its classification
(pyramid L0/L1/…, label, macro, thumbnail, overview, probability, map) and
reports wsitools private tags (65080–65084). A slim `tiffinfo` analog.

```sh
wsitools dump-ifds slide.svs
wsitools dump-ifds --raw slide.svs        # every TIFF tag: name, type, count, value, enum
wsitools dump-ifds --raw --json slide.svs # same, as JSON
wsitools dump-ifds --raw-full slide.svs   # disable truncation of long arrays / blobs
```

TIFF-only; does not apply to DICOM.

### `region`
Extract a rectangular pixel region as PNG. Analog of `openslide-write-png`.

```sh
wsitools region --x 10000 --y 8000 --w 1024 --h 1024 --level 0 -o tile.png slide.svs
```

### `extract`
Save an associated image (label / macro / thumbnail / overview) as PNG (default)
or JPEG. JPEG output is a byte pass-through when the source is already JPEG. Run
`info` to see which associated images a slide carries.

```sh
wsitools extract --type label -o label.png slide.svs
wsitools extract --type macro --format jpeg -o macro.jpg slide.svs
```

### `hash`
Content hash for cache identity / dedup.

- `--mode file` (default) — SHA-256 of the file bytes (`sha256sum`-equivalent).
- `--mode pixel` — decodes L0 tiles to RGB in raster order and hashes that, so
  the hash is stable across re-encode. Use this for DICOM (file-mode is
  undefined for a multi-file series).

```sh
wsitools hash slide.svs
wsitools hash --mode pixel slide.svs
```

### `validate`
Check a slide's structural conformance: level geometry, tile-grid math, monotone
pyramid downsampling, per-format checks, decodability of the base tile, and
JPEG 2000 colorspace-tag correctness. Prints findings (info / warning / error)
as text or `--json`.

Exit codes: `0` valid · `2` invalid (findings crossed the gate) · `1`
operational error (path missing/unreadable). `--strict` treats warnings as
failures.

```sh
wsitools validate slide.svs
wsitools validate --strict --json slide.svs
```

---

## Associated-image editing

Remove or replace the **label**, **macro**, **thumbnail**, or **overview**. The
pyramid tile bytes are copied verbatim (no decode, no re-encode), so editing is
fast and the pyramid is bit-for-bit preserved. Removing an image deletes it from
the output file.

> **On de-identification:** removing the label is commonly one step in a
> de-identification workflow, but wsitools edits *images only* — it does not
> de-identify a slide by itself. PHI may also live in slide metadata (e.g.
> `ImageDescription`, DICOM attributes) or in associated images you did not
> remove. Treat these commands as building blocks, and verify the result.

```sh
# Remove the label image. Writes <stem>_relabeled<ext> next to the input.
wsitools label remove slide.svs

wsitools label remove --in-place slide.svs      # atomically overwrite (temp + fsync + rename)
wsitools label remove -o deidentified.svs slide.svs

# Replace the label (default LZW + Predictor 2 — lossless, barcode-safe)
wsitools label replace --image new_label.png slide.svs
wsitools label replace --image new_label.png --compression jpeg --bg F5F5E6 slide.svs

# Same mechanics for the other associated images
wsitools macro replace --image new_macro.jpg slide.svs
wsitools thumbnail remove slide.svs
wsitools overview remove slide.svs
```

Replace options: `--compression {jpeg,lzw,deflate,none}` (default is per-type —
**LZW + Predictor 2** for the label, lossless and barcode-safe; **JPEG** for
macro/thumbnail/overview), `--resize {fit,stretch,none}` (default `fit`),
`--bg RRGGBB` letterbox fill (default `F5F5E6`), `--label-dims WxH`, `--force`
to skip the aspect guard.

**Format coverage:**

- **`remove`** works for every associated type on **SVS**, **generic-TIFF**,
  **COG-WSI**, and **OME-TIFF**.
- **`replace`** works for all types on **generic-TIFF**, **COG-WSI**, and
  **OME-TIFF**. On **SVS**, `replace` works for any image that trails the tiled
  pyramid — **label**, **macro**, **overview** — via a tail-IFD rewrite. The SVS
  **thumbnail** (stored before the pyramid) can be replaced only on single-level
  slides; on a multi-level slide it errors with a clear message.
- **OME-TIFF editing is lossy:** the rebuild regenerates a minimal OME-XML, so
  instrument/acquisition/channel/vendor metadata is discarded (pyramid pixels,
  geometry, MPP, magnification, ICC, and the other associated images are kept).
  An always-on warning fires. Associated replacements are JPEG-only. See
  [ome-tiff-limitations.md](ome-tiff-limitations.md).

Other formats (DICOM, NDPI, Philips, BIF, IFE, Leica) are not editable in place —
convert first with `convert --to {svs,tiff}` and edit the result.

---

## Conversion & transformation

### `convert --to <container>`
Re-container or re-encode a slide into another format. With no `--codec`, tiles
are copied verbatim (lossless tile-copy) where the source and target codecs
match; with `--codec {jpeg, jpeg2000, jpegxl, avif, webp, htj2k}` the pyramid is
re-encoded (JPEG 2000 lossless via `--quality reversible=true`). Conversion is
streaming — no full-resolution raster is held in memory.

```sh
wsitools convert --to cog-wsi  -o slide.cog.tiff slide.svs   # lossless tile-copy
wsitools convert --to ome-tiff -o slide.ome.tiff slide.svs
wsitools convert --to svs --codec jpegxl -o slide-jxl.svs slide.svs
wsitools convert --to cog-wsi --bigtiff on -o slide.cog.tiff slide.svs
wsitools convert --to cog-wsi --no-associated -o slide.cog.tiff slide.svs
```

Common flags: `--codec`, `--quality k=v`, `--tile-size`, `--bigtiff
{auto,on,off}` (auto promotes when the predicted output exceeds ~2 GiB),
`--no-associated`, `--factor N` / `--target-mag M` (downsample during
conversion), `--rect X,Y,W,H` (crop during conversion), `--workers`.

**Targets:**

- **`cog-wsi`** — Cloud-Optimized GeoTIFF + WSI extension tags. Lossless
  verbatim tile-copy.
- **`svs` / `tiff` / `ome-tiff`** — Aperio-shaped SVS, generic tiled TIFF, or
  OME-TIFF. OME-TIFF carries only dimensions/MPP/magnification (see
  [ome-tiff-limitations.md](ome-tiff-limitations.md)).
- **`dzi`** — DeepZoom pyramid, OpenSeadragon-compatible (256×256 tiles, 1 px
  overlap, JPEG Q=85 default). Associated images are written as lossless PNG
  sidecars under `<stem>_associated/<type>.png`.
- **`szi`** — Smart Zoom Image: a DZI pyramid wrapped in a store-method ZIP,
  plus an optional `scan-properties.xml` from source metadata.
- **`dicom`** *(experimental)* — DICOM-WSI VOLUME instances. See
  [DICOM output](#dicom-output) below.
- **`bif`** *(experimental)* — Ventana/Roche DP 200-shaped BIF. See
  [BIF output](#bif-output) below.
- **`ife`** — [Iris File Extension](https://github.com/IrisDigitalPathology/Iris-File-Extension):
  JPEG/AVIF tiles with full metadata (MPP, magnification, ICC, associated
  images, attributes). A 256px-tiled JPEG/AVIF source tile-copies verbatim;
  otherwise the pyramid is re-encoded.

### `downsample`
Reduce a slide by a power-of-2 `--factor N` (default 2) or to a `--target-mag M`,
**preserving the source container** (SVS→SVS, OME-TIFF→OME-TIFF, generic
TIFF→generic TIFF, COG-WSI→COG-WSI, DICOM→DICOM). Regenerates the full pyramid
from the new base, scales MPP ×N / magnification ÷N, and copies associated
images byte-faithfully.

```sh
wsitools downsample -o slide-20x.svs slide-40x.svs
wsitools downsample --target-mag 10 -o slide-10x.svs slide-40x.svs
```

To downsample *into a different* container, use `convert --to <target>
--factor N`.

### `crop`
Extract a rectangular region (`--rect X,Y,W,H`, level-0 coordinates) into the
**same container** as the source. The default re-encodes the exact extent;
`--lossless` snaps the rect to the source tile grid and copies L0 tiles verbatim
(byte-identical L0 — the output is a tile-aligned superset of the requested
rect). Lower pyramid levels are rebuilt from the cropped base; the thumbnail is
regenerated from the crop region (label/macro/overview pass through).

```sh
wsitools crop --rect 20000,15000,8192,8192 -o region.svs slide.svs
wsitools crop --rect 20000,15000,8192,8192 --lossless -o region.svs slide.svs
```

---

## DICOM output

> **Experimental.** `convert --to dicom` emits conformant DICOM-WSI VOLUME
> instances.

By default it writes the **full-resolution pyramid** — one instance per source
level as `<dir>/level-<n>.dcm` (n=0 = full resolution) — as a multi-instance
Series sharing Study/Series/FrameOfReference UIDs, written atomically (temp dir →
rename, never a partial pyramid). `--level N` selects a single level instead.

```sh
wsitools convert --to dicom -o out_dir/ slide.svs
wsitools convert --to dicom --level 0 -o level0.dcm slide.svs
```

- **Verbatim source (DICOM / JPEG-baseline / JPEG 2000):** compressed tiles are
  copied verbatim and re-encapsulated as TILED_FULL multi-frame PixelData.
  `PhotometricInterpretation` and transfer syntax are derived from the actual
  frame (JPEG-baseline: RGB / `YBR_FULL_422` / `YBR_FULL`; JPEG 2000: RGB /
  `YBR_ICT` / `YBR_RCT` / `MONOCHROME2` with a lossless/lossy transfer syntax).
  The source ICC profile is carried through, or a canonical sRGB profile is
  synthesized when absent.
- **Associated images** (full-pyramid mode) are emitted as single-frame
  instances in the same Series at `<dir>/<type>.dcm`. Ones whose codec is not a
  DICOM transfer syntax (e.g. the LZW label on an Aperio SVS) are decoded and
  stored as uncompressed native RGB so the barcode stays scannable.
  `--no-associated` skips them; `--level N` emits none.
- **As a transform target:** `convert --to dicom --factor N` reduces any source
  into a DICOM pyramid, and `downsample <dicom>` / `crop <dicom>` (± `--lossless`)
  emit a reduced/cropped DICOM directory. Re-encoded levels are JPEG-baseline and
  honor the source's chroma subsampling; a lossless crop's L0 is a verbatim
  frame-copy.

A DICOM **source** may be a single `.dcm` instance or a directory containing a
WSM series. A named `.dcm` opens the series it belongs to (its `SeriesUID`
siblings). If a directory holds more than one distinct series, wsitools refuses
with an error listing the candidates; pass a specific `.dcm` to disambiguate.

## BIF output

> **Experimental.** `convert --to bif` writes a Ventana/Roche DP 200-shaped BIF
> from any source.

The full pyramid is written as row-major `level=N` IFDs, plus a whole-slide
overview (carried from the source's overview/macro when present, else
synthesized) and synthesized scanner metadata (model, MPP, magnification). JPEG
sources are tile-copied verbatim; non-JPEG sources re-encode with `--codec jpeg`.
Output renders in bio-formats / QuPath. Limitations: single-AOI, no Z; no
separate label/thumbnail or probability map; no `--factor`/`--target-mag`.

---

## Diagnostics

### `doctor`
Report installed codec libraries, physical RAM, and the active soft memory
limit (see [memory.md](memory.md)).

### `version`
Print the wsitools version and Go runtime info.
