# OME-TIFF limitations in wsitools

wsitools' OME-TIFF support is rudimentary. The writer models only the
geometry-minimal subset of the OME data model (dimensions, MPP,
magnification, one `<Image>` per pyramid or associated image). It does not
model multi-image/series structure, instrument metadata, objective, channel
details, acquisition dates, stage positions, planes, annotations, or any
vendor `OriginalMetadata`. This is not a conformant OME-TIFF implementation
— it is a TIFF container with a minimal OME-XML preamble sufficient for
wsitools' own reader (via opentile-go) to open the result as OME-TIFF.

## What the writer emits (minimal OME-XML)

Every OME-TIFF written or rebuilt by wsitools contains:

- One `<Image>` block per pyramid (and per surviving associated image).
- `<Pixels SizeX/SizeY/SizeC=3/SizeZ=1/SizeT=1>` — RGB, single Z/T.
- `PhysicalSizeX` / `PhysicalSizeY` (MPP) when available from the source.
- `<Channel>` entries (three RGB channels, unnamed).
- `<TiffData IFD="...">` pointing to the correct IFD.
- `Creator` recording wsitools provenance.
- Pyramid sub-resolutions stored as SubIFDs (tag 330) of L0, as required
  by the OME-TIFF spec.

### What is NOT modelled

- Multiple `<Image>` series / multi-well plates.
- `<Instrument>` / `<Objective>` / `<Detector>`.
- Acquisition dates and times.
- Stage positions (`<StageLabel>`, `<Plane>` DeltaX/Y/Z).
- Named or structured `<Channel>` metadata (fluorescence channel names,
  emission wavelengths, etc.).
- `<Annotation>` blocks.
- `<StructuredAnnotations>` / vendor `OriginalMetadata` key-value pairs.
- Arbitrary linked data (ROIs, experiment metadata, etc.).

## Lossy editing: `<type> remove|replace`

The `label`, `macro`, `thumbnail`, and `overview` `remove` and `replace`
commands support OME-TIFF via a full-file rebuild using `streamwriter`.
**This is explicitly lossy.**

Because the rebuild regenerates the OME-XML from scratch using only the
information wsitools can reconstruct from opentile-go's metadata accessors,
**the following metadata is discarded** from the surviving pyramid too:

| Discarded | Preserved |
|---|---|
| Instrument / objective | Pyramid pixels (verbatim tile bytes) |
| Acquisition dates | Other associated images (not the edited one) |
| Stage positions | Image dimensions (SizeX/SizeY) |
| Named channel metadata | Physical pixel size (MPP / PhysicalSizeX/Y) |
| Plane metadata (DeltaX/Y/Z/T) | Magnification |
| `StructuredAnnotations` / `OriginalMetadata` | ICC profile |
| Pyramid-resolution annotations | |
| All other OME `<Image>` children not listed under "Preserved" | |

An **always-on runtime warning** is printed on every OME-TIFF edit to make
this explicit. There is no way to suppress it short of redirecting stderr.

## Associated replacement encoding

**OME-TIFF associated replacements are always JPEG-encoded**, regardless of
the `--compression` flag default.

The reason is a reader limitation: opentile-go's OME-TIFF reader can only
decode JPEG and uncompressed associated images. A replacement written with
LZW or Deflate would be unreadable when the file is re-opened. (Contrast:
SVS and generic-TIFF `label replace` default to LZW + Predictor 2, because
their readers handle LZW.)

Specifying `--compression lzw` or `--compression deflate` on an OME-TIFF
target emits a warning that the result will not round-trip through
opentile-go's reader; for maximum compatibility, leave `--compression`
unset (the default will select JPEG for OME-TIFF).

## Recommendation: Bio-Formats for serious OME-TIFF work

For workflows that require the full OME data model — instrument metadata,
multi-channel fluorescence, acquisition annotations, stage positions, or
vendor `OriginalMetadata` — use
**[Bio-Formats](https://www.openmicroscopy.org/bio-formats/)**, the OME
reference implementation.

Relevant Bio-Formats tools:

- **`bioformats2raw`** + **`raw2ometiff`** — convert any Bio-Formats-readable
  slide into a conformant pyramidal OME-TIFF, carrying all metadata the
  reader understands. See the
  [OME-NGFF → OME-TIFF pipeline](https://bio-formats.readthedocs.io/en/stable/users/comlinetools/conversion.html).
- **`bfconvert`** — Bio-Formats command-line converter; handles the widest
  range of microscopy formats and preserves the OME data model.
- **[tifffile](https://github.com/cgohlke/tifffile)** (Python) — lighter
  option for programmatic OME-TIFF read/write when Bio-Formats' Java
  dependency is inconvenient; models more of the OME schema than wsitools.

wsitools' OME-TIFF tools (`convert --to ome-tiff`, `downsample` on
OME-TIFF sources, associated-image editing on OME-TIFF) are suited for
geometry-preserving tile operations on slides where the OME metadata
content is either minimal or not needed downstream. For anything that
touches the OME schema beyond dimensions and MPP, Bio-Formats is the right
tool.
