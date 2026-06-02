# OME-TIFF specification — local reference notes

Distilled, normative-quote reference for OME-TIFF writing in wsitools. Use
this as the offline grounding for `convert --to ome-tiff` work instead of
reasoning from first principles.

**Sources (retrieved 2026-06-02):**
- OME-TIFF specification — <https://ome-model.readthedocs.io/en/stable/ome-tiff/specification.html>
- OME 2016-06 XML schema (XSD) — <https://www.openmicroscopy.org/Schemas/OME/2016-06/ome.xsd>
- Reference reader (canonical for round-trip): `opentile-go/formats/ometiff/`

OME documentation and schemas are © the Open Microscopy Environment,
released CC-BY 4.0. Quotations below are from the specification text; this
file paraphrases/excerpts for engineering reference.

---

## 1. Sub-resolutions (pyramids) — NORMATIVE

Verbatim requirements from the spec's "Sub-resolutions" section:

- "The offsets of all sub-resolutions IFDs must be referenced from the IFD of
  the full-resolution plane using the **SubIFDs TIFF extension tag (330)**" (as
  defined in Adobe TIFF Tech Note 1).
- "The list of sub-resolution offsets must be ordered by plane size **from
  largest to smallest**."
- The sub-resolution IFD offsets "must **neither** be referenced in the
  primary chain of IFDs derived from the first IFD of the TIFF file **nor** be
  referenced in a TiffData element of the OME-XML metadata."
- "The **bit 0 of the NewSubFileType TIFF tag (254)** for each pyramidal level
  should be set to **1**" (to distinguish full-resolution from downsampled
  planes). I.e. full-res L0 = 0; each sub-resolution = 1.
- "The planes of largest resolutions **should be organized into tiles** rather
  than strips," and may use LZW / JPEG / JPEG2000. Sub-resolutions may use a
  different compression than the full-resolution plane.

Downsampling guidance (SHOULD, not MUST): "should be an integer value,"
"identical along the X and the Y dimensions," and "stay the same between each
consecutive pyramid level."

## 2. OME-XML metadata storage — NORMATIVE

- Stored in the **ImageDescription tag (270) of the first IFD**.
- "The XML string must be **UTF-8** encoded."
- Recommended preamble comment (exact text):
  ```
  <!-- Warning: this comment is an OME-XML metadata block, which contains
  crucial dimensional parameters and other important metadata. Please edit
  cautiously (if at all), and back up the original data before doing so. -->
  ```
- Detection (per tifffile / opentile `is_ome`): the first IFD's
  ImageDescription, trimmed, must end with `OME>`.

## 3. TiffData → IFD mapping

`<TiffData>` maps image planes to TIFF IFDs. Attributes (all optional):
- `IFD` — the IFD index, **indexed from 0, default 0**.
- `FirstZ` / `FirstT` / `FirstC` — plane position, indexed from 0, default 0.
- `PlaneCount` — number of IFDs affected; default = number of IFDs in the
  file, unless `IFD` is specified, in which case default = 1.
- Optional child `<UUID FileName="…">` for multi-file sets.

The `IFD` index counts the **primary (top-level) IFD chain** — sub-resolution
IFDs are excluded (§1), so they are never addressed by a TiffData `IFD`.

## 4. Minimal valid single-image OME-XML (from the 2016-06 XSD)

- Root `<OME xmlns="http://www.openmicroscopy.org/Schemas/OME/2016-06">`.
  `Creator` optional; `UUID` optional for single-file (required only for a
  MetadataOnly companion).
- `<Image ID=… >` — `ID` required, `Name` optional; must contain one
  `<Pixels>`.
- `<Pixels ID=… DimensionOrder=… Type=… SizeX=… SizeY=… SizeZ=… SizeC=…
  SizeT=… >` — all those attributes **required**. Must contain one of
  `<BinData>`, `<TiffData>`, or `<MetadataOnly>`.
  - `Type` ∈ {int8, int16, int32, uint8, uint16, uint32, float, double,
    complex, double-complex, bit}.
  - `DimensionOrder` ∈ {XYZCT, XYZTC, XYCTZ, XYCZT, XYTCZ, XYTZC}.
  - `PhysicalSizeX/Y` optional (PositiveFloat); `PhysicalSize*Unit` optional,
    default `µm`.
- `<Channel ID=… >` — `ID` required; `SamplesPerPixel` optional.

wsitools' RGB output document (`DimensionOrder="XYCZT"`, `Type="uint8"`,
`SizeC=3`, `SizeZ=1`, `SizeT=1`, three named Channels, one TiffData
`IFD="0"`) is schema-valid against the above.

## 5. NOT specified by the OME-TIFF spec (conventions, not normative)

The OME-TIFF specification does **not** mandate:
- `PhotometricInterpretation` or `PlanarConfiguration` values.
- Strip vs tile beyond "largest resolutions should be tiled."
- **Associated-image (label / macro / thumbnail) representation.** Treating an
  `<Image>` whose `Name` is `label` / `macro` / `thumbnail` as an associated
  image is a **reader convention** (Bio-Formats; opentile
  `formats/ometiff/series.go classifyImages`), not spec text. opentile maps an
  `<Image>` to a top-level page **positionally** (`pages[imageIndex]`), and
  exact-matches the trimmed `Name`; any other Name is treated as a *main
  pyramid*. wsitools must therefore keep the `<Image>` order aligned with the
  top-level IFD order and only use those three reserved names for associated
  images.

## 6. BigTIFF + multi-file (informational)

- BigTIFF OME-TIFF "should" use extensions `.ome.tf2` / `.ome.tf8` /
  `.ome.btf`; `.ome.tif(f)` is widely used regardless. wsitools does not force
  an extension (output path is user-supplied via `-o`).
- Multi-file sets use the root `UUID` + `<TiffData><UUID FileName=…>` and
  `BinaryOnly`/`MetadataFile` companions — **out of scope** for wsitools
  (single-file output only).

---

See `docs/superpowers/specs/2026-06-02-ome-tiff-conformance-design.md` for how
wsitools applies these rules.
