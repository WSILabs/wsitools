# D7 — Cross-implementation DICOM WSM conformance: wsitools vs wsidicomizer

**Date:** 2026-06-20
**Scope:** format-debt survey item **D7**. `dciodvfy` validates our WSM against
the IOD in isolation; it does not compare against an ecosystem reference. This
study converts the same source slides to DICOM WSM with both
`wsitools convert --to dicom` and **wsidicomizer** (the de-facto reference
Python implementation) and diffs the datasets attribute-by-attribute to surface
metadata-completeness gaps `dciodvfy` stays silent on.

## Method

- **Reference:** `wsidicomizer 0.26.1` / `wsidicom 0.31.0` / `pydicom 3.0.2`
  (python 3.12 venv), openslide 4.0.0.
- **Sources:** `CMU-1-Small-Region.svs` (CC0, single pyramid level) and
  `239551.svs` (CC-BY, 3-level pyramid) — both from the wsi-fixtures pool.
- Only the base `convert --to dicom` path is comparable (wsidicomizer has no
  `--factor`/crop analog). JPEG-baseline transfer syntax both sides.
- Diff scripts: `pydicom`, comparing top-level WSM attributes + functional
  groups + pyramid linkage. (Reproduction at the end.)

## Result: strong structural conformance

On **every load-bearing WSM attribute**, wsitools matches the reference:

| Attribute | wsitools | wsidicomizer |
|---|---|---|
| SOPClass / Modality | VL Whole Slide Microscopy / SM | same |
| TransferSyntax | JPEG Baseline `…1.2.4.50` | same |
| DimensionOrganizationType | TILED_FULL | same |
| ImageType | `ORIGINAL\PRIMARY\VOLUME\…` | same |
| TotalPixelMatrix (+ OriginSequence) | 2220×2967, present | same |
| Tile Rows×Columns | 240×240 | same |
| NumberOfFrames | 130 (base) | same |
| Samples / Photometric / Planar / Bits | 3 / RGB / 0 / 8·8 | same |
| PixelSpacing (Shared FG → PixelMeasures) | 0.0004967 | same |
| OpticalPathSequence (+ ICCProfile) | 1 path, ICC present | same |
| SharedFunctionalGroupsSequence | present | present |
| PerFrameFunctionalGroupsSequence | absent (correct for TILED_FULL) | absent |
| SpecimenDescriptionSequence | present | present |
| AcquisitionContextSequence | present | present |
| LossyImageCompression / Method | 01 / ISO_10918_1 | same |
| Study / Series / FrameOfReference | 1 / 1 / 1 | 1 / 1 / 1 |

Instance inventory is identical in shape: VOLUME + LABEL + OVERVIEW + THUMBNAIL
(single-level), and 3 VOLUME levels + the three associated images (multi-level).
Frame counts per level match exactly (3400 / 225 / 65 for `239551`). The base
VOLUME's top-level tag set differs from the reference by exactly **one** tag
(PyramidUID, below) — wsitools emits no tag the reference lacks.

## Findings (what dciodvfy does not catch)

### 1. PyramidUID — completeness gap (the substantive one)

wsidicomizer stamps every VOLUME (pyramid) level of a multi-level slide with a
**shared `PyramidUID` (0020,0027)** — the DICOM Pyramid IOD's explicit
level-grouping mechanism — and leaves it off the associated images. wsitools
emits **no PyramidUID** on any instance.

Both implementations still group the levels correctly via **shared
SeriesInstanceUID + shared FrameOfReferenceUID**, which is valid and was
wsitools' deliberate choice (the DICOM-writer design explicitly went
"multi-instance Series, shared UIDs, no Pyramid UID"). So there is no
*correctness* defect — a conforming reader resolves the pyramid either way — but
PyramidUID is the modern, explicit linkage the ecosystem reference provides, and
adding it would tighten ecosystem alignment (newer pyramid-aware viewers prefer
it). **Candidate follow-up:** synthesize one PyramidUID per pyramid, stamp it on
all VOLUME instances (associated images excluded).

### 2. PixelSpacing at downsampled levels — anisotropy divergence

At the base level both are isotropic (`[0.0004967, 0.0004967]`). At the
**downsampled** levels they diverge:

| Level | wsitools | wsidicomizer |
|---|---|---|
| L1 | `[0.0019875, 0.0019870]` (X≠Y) | `[0.0019870, 0.0019870]` (isotropic) |
| L2 | `[0.0039771, 0.0039748]` (X≠Y) | `[0.0039748, 0.0039748]` (isotropic) |

wsitools recomputes per-axis spacing from each level's integer-rounded matrix
dimensions (23999/4 = 5999.75 → stored 5999, so the effective X scale ≠ exactly
4×), yielding marginally **anisotropic** spacing. wsidicomizer scales the base
spacing by the nominal downsample factor and keeps it **isotropic**. Magnitude
is tiny (~0.02%), and wsitools' value is arguably *more literally accurate* for
the actually-stored level — but isotropic-scaled spacing is the conventional WSM
expectation, and a strict consumer that assumes square pixels could flag the
anisotropy. **Candidate follow-up:** consider scaling the base PixelSpacing by
the integer factor (isotropic) at derived levels rather than recomputing
per-axis from rounded dims.

### 3. Cosmetic default differences (no action needed)

- **ImagedVolumeDepth:** wsitools `1.0` vs wsidicomizer `0.5`. The source SVS
  carries no real tissue depth; both are placeholders.
- **ContainerIdentifier:** wsitools `"WSITOOLS"` vs wsidicomizer `"Unknown"`.
  Both are placeholders (the source has no container ID). wsidicomizer's
  `"Unknown"` is marginally more honest; wsitools' tool-stamp is harmless.

## Verdict

`wsitools convert --to dicom` produces WSM that is **structurally equivalent to
the wsidicomizer ecosystem reference** across all conformance-critical
attributes — consistent with the clean `dciodvfy` record. Two substantive,
low-severity divergences are worth a deliberate decision: **PyramidUID**
(omitted by design; the reference includes it) and **derived-level PixelSpacing
anisotropy** (per-axis recompute vs isotropic base×factor). Neither is a
correctness defect; both are ecosystem-alignment refinements.

## Reproduction

```sh
python3.12 -m venv /tmp/d7venv
/tmp/d7venv/bin/pip install wsidicomizer pydicom         # + openslide (brew)
SRC=sample_files/svs/239551.svs
bin/wsitools convert --to dicom -f -o /tmp/wt/out "$SRC"
/tmp/d7venv/bin/wsidicomizer -i "$SRC" -o /tmp/wd/out
# diff with pydicom: compare ImageType==VOLUME instances' top-level tags,
# SharedFunctionalGroups → PixelMeasures, and PyramidUID across levels.
```
