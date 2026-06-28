# wsitools manual QA plan

A checklist for **manually** exercising wsitools across formats/transforms and
confirming the outputs in real viewers. The helper scripts here generate a broad
matrix of outputs and auto-validate the ones that can be machine-checked; the
rest you open by eye in the viewers you have.

This is deliberately *not* a programmatic test suite (those live in `go test`).
It's the "did we actually break anything a real viewer cares about" pass to run
before a release.

## 0. Workflow

```sh
# 1. Generate the output matrix (builds wsitools from the repo).
scripts/qa/run-matrix.sh                 # add --big to also drive NDPI + IFE/Iris sources
#    -> /tmp/wsitools-qa/cases/*  + manifest.tsv  (override with OUT=/path)

# 2. Auto-validate the machine-checkable outputs.
scripts/qa/check-openslide.sh            # OpenSlide = Aperio-ecosystem gold oracle
scripts/qa/check-bioformats.sh           # Bio-Formats = what QuPath uses underneath
scripts/qa/check-bioformats.sh --pixels  # also decode a 256x256 crop per output

# 3. Eyeball the rendered PNGs from the OpenSlide pass.
open /tmp/wsitools-qa/openslide/*.png

# 4. Hand-open the GUI-only artifacts (QuPath / ImageScope / Hamamatsu / browser)
#    per the tables below.
```

`manifest.tsv` columns: `id  category  description  source  output  status`.
Every row's `output` is under `OUT/cases/`.

## 1. What to look for (the rubric)

These are the failure modes wsitools has actually hit. Check each opened slide
against them:

| # | Symptom | What it means |
|---|---------|---------------|
| R1 | **Colours wrong** (blue/orange swapped, oversaturated) | Photometric/Subsampling tag vs JPEG framing mismatch |
| R2 | **Black blocks / garbled stripes at right or bottom edges** | Edge tiles not padded to full tile size (TIFF/DICOM) |
| R3 | **A pyramid/zoom level missing** (big jump in zoom detail) | SVS thumbnail not at IFD 1 → ImageScope ate a level |
| R4 | **Wrong physical scale** (scale bar / µm wrong) | MPP / magnification metadata dropped or mis-scaled |
| R5 | **Label / macro / overview missing or wrong** | Associated-image copy/edit defect |
| R6 | **Won't open at all** | Structural/container conformance defect |
| R7 | **Truncated tissue / wrong dimensions** | Level dims or crop rect handling defect |

`info` + `validate` (run automatically in matrix section A) cover R4/R5/R6 at the
metadata level; the viewers confirm the pixels.

## 2. Auto-validated (no GUI needed)

| Tool | Reads | Catches | Run |
|------|-------|---------|-----|
| **OpenSlide** | svs, generic tiled tiff, cog-wsi, dicom, ndpi, bif, mrxs, scn, philips | R2 (dimensional mismatch on render), R3 (level count), R5 (associated list), R6 | `check-openslide.sh` |
| **Bio-Formats** | the above + ome-tiff (+ proxy for QuPath) | R6 (parse/IFD/OME-XML errors), R7 (series dims), pixel decode with `--pixels` | `check-bioformats.sh` |

Both print `OK / FAIL / N/A`. `N/A` = that tool can't read that container/codec
(expected — see §4); only `FAIL` needs attention. The OpenSlide pass also writes
a deepest-level PNG per slide to `OUT/openslide/` — flip through them for R1/R2/R7.

## 3. Manual viewers (open by hand)

### ImageScope (Windows — strict Aperio reader; the toughest critic)
Open these from `OUT/cases/` and check R1/R2/R3/R4/R5:

| Artifact | Why it matters |
|----------|----------------|
| `b_svs.svs` | baseline SVS round-trip |
| `e_bif2svs.svs`, `e_ome2svs.svs`, `e_cog2svs.svs`, `e_dicom2svs.svs` | cross-format → SVS (R1/R2/R3 regression set) |
| `e_ife2svs.svs` (needs `--big`) | IFE/Iris → SVS (the 4×-pyramid case) |
| `d_factor2.svs`, `d_rect.svs`, `d_tile512.svs`, `d_crop.svs`, `d_crop_lossless.svs` | transforms — check edges (R2) + scale (R4) |
| `g_label_replaced.svs`, `g_label_removed.svs`, `g_overview_removed.svs`, `g_macro_replaced.svs` | associated edits (R5); open Image → "View Label / View Thumbnail" |

In the ImageScope **Image Information** panel verify: all pyramid levels present
with sensible ratios (R3), MPP + AppMag correct (R4), Label/Thumbnail tabs
populated (R5).

### QuPath (cross-platform — Bio-Formats + OpenSlide)
Open the same SVS set plus `b_ome.ome.tiff`, `b_tiff.tiff`, `b_cog.tiff`,
`b_dicom/` (point at a `.dcm`). Check R1/R2/R4/R7 at multiple zooms; QuPath's
status bar shows µm/px (R4). OME-TIFF is QuPath's strong suit — confirm
`b_ome.ome.tiff` and `e_ome2svs.svs` look right.

### Hamamatsu viewer (NDPI)
Hamamatsu's viewer is for native NDPI. Use it on the **source** `ndpi/*.ndpi`
fixtures to confirm the source reads (sanity), and on any NDPI you produce. (wsitools
does not write NDPI, so this is mostly source-side / read-side confirmation.)

### Browser / OpenSeadragon (DZI, SZI)
OpenSlide/Bio-Formats can't read DZI/SZI. Validate them as tiled web pyramids:
- `b_dzi.dzi` + `b_dzi_files/` — load in any DZI viewer (OpenSeadragon demo page,
  or VIPS `vipsdisp`). Check R2 (tile seams/edges), R7 (full extent), and that
  deep zoom levels all load.
- `b_szi.szi` — the zipped DZI; unzip and inspect, or use an SZI-aware viewer.

### Iris validator (IFE / `.iris`)
The official gold gate for IFE. In a venv: `pip install Iris-Codec`, then
`make ife-validate` (or the snippet in the Makefile). Validate `b_ife.iris` and,
with `--big`, the round-trip of `425248_JPEG.iris`. CI also runs this.

## 4. Expected `N/A` / known gaps (NOT failures)

- **Novel codecs in TIFF** (`c_avif.tiff`, `c_htj2k.tiff`, `c_jpegxl.tiff`,
  `c_webp.tiff`): no standard TIFF compression tag → OpenSlide & Bio-Formats
  can't read them. They are **wsitools/opentile-only**; validate with
  `wsitools info <f>` / `wsitools validate <f>` / `wsitools region`. (JPEG and
  JPEG-2000 in TIFF/SVS *are* standard and read everywhere.)
- **DZI / SZI / IFE**: not readable by OpenSlide or Bio-Formats (see §3 for their
  validators).
- **`b_bif.bif` in OpenSlide**: OpenSlide errors with "Bad direction attribute".
  This is a **known OpenSlide-side limitation** — its Ventana reader does not
  honor the `TileJointInfo Direction` attribute. Our BIF is correct and is read
  fine by **Bio-Formats / QuPath / opentile** (round-trips pixel-identical).
  Don't treat the OpenSlide error as a wsitools defect; use those readers for BIF.

## 5. Pixel-equivalence spot checks (optional, exact)

For conversions that should be pixel-faithful, compare against the source with
wsitools' own pixel hash (geometry-independent within a level):

```sh
# Lossless / tile-copy conversions should match the source pixel hash:
wsitools hash --mode pixel sample_files/svs/CMU-1.svs
wsitools hash --mode pixel /tmp/wsitools-qa/cases/d_crop_lossless.svs   # same region
# Render the same region from source and output and diff visually:
wsitools region --level 0 --rect 2000,2000,1024,1024 -o /tmp/src.png  sample_files/svs/CMU-1.svs
wsitools region --level 0 --rect 1000,1000,1024,1024 -o /tmp/out.png  /tmp/wsitools-qa/cases/d_crop.svs
```
