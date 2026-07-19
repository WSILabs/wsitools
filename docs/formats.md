# Format support

Which formats wsitools reads, writes, and transforms, plus the per-format
caveats. For command details see [commands.md](commands.md).

## Source formats accepted

SVS · Philips-TIFF · OME-TIFF (tiled) · BIF · IFE · generic tiled TIFF · NDPI ·
OME-OneFrame · Leica SCN (single-image) · COG-WSI · DICOM-WSI.

## Full matrix

| Source format | `info` | `region` | `dump-ifds` | `extract`¹ | `hash`² | convert *from*³ | convert *to*⁴ | `downsample` / `crop`⁵ | edit⁶ |
|---|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
| SVS           | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | ✓ | ✓ |
| Philips-TIFF  | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | — | — | — |
| OME-TIFF      | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | ✓ | ✓⁷ |
| BIF           | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | — | — | — |
| generic-TIFF  | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | ✓ | ✓ |
| NDPI          | ✓ | ✓ | ✓ | ✓ | ✓ | ✓* | — | — | — |
| OME-OneFrame  | ✓ | ✓ | ✓ | ✓ | ✓ | ✓* | — | — | — |
| Leica SCN     | ✓ | ✓ | ✓ | ✓ | ✓ | ✓* | — | — | — |
| COG-WSI       | ✓ | ✓ | ✓ | ✓ | ✓ | ✓  | ✓ | ✓ | ✓ |
| IFE           | ✓ | ✓ | — | ✓ | ✓ | ✓  | — | — | — |
| DICOM-WSI     | ✓ | ✓ | — | ✓ | ✓⁸ | ✓ | ✓⁹ | ✓⁹ | — |

## Convert targets

The full output target set is `cog-wsi`, `svs`, `tiff` (→ generic-TIFF),
`ome-tiff`, `dicom`, `dzi`, `szi`, `bif`, `ife`. **DZI, SZI, BIF, and IFE** are
output-only pyramid formats (they are not readable sources).

All targets except `dzi`, `szi`, and `bif` also accept `--factor N` /
`--target-mag M` to downsample during conversion (scaling MPP ×N /
magnification ÷N).

## Footnotes

**¹ `extract`** — works when the slide carries that associated image
(label/macro/thumbnail/overview); run `info` to list which.

**² `hash`** — `--mode pixel` works for every format; the default file-mode is a
single-file SHA-256.

**³ convert *from*** — readable as a convert source. **✓\*** marks a *stripped*
source: a tile grid is synthesized over the source strips, so `convert` decodes
and re-encodes (reproducible JPEG tiles) rather than doing a bit-exact
tile-copy. The lossless tile-copy fast path applies only to natively-tiled
sources (plain ✓).

**⁴ convert *to*** — available as a convert output target. **IFE**
([Iris File Extension](https://github.com/IrisDigitalPathology/Iris-File-Extension))
writes JPEG/AVIF tiles with full metadata (MPP, magnification, ICC, associated
images, attributes); a 256px-tiled JPEG/AVIF source copies tiles verbatim
(lossless), otherwise the pyramid is re-encoded.

**⁵ `downsample` / `crop`** — both are format-preserving transforms sharing one
support set (the ✓ rows). `downsample` reduces by `--factor N` / `--target-mag
M`; `crop` extracts `--rect X,Y,W,H` (level-0 coordinates), default re-encoding
the exact extent or `--lossless` snapping to the tile grid and copying L0 tiles
verbatim (byte-identical L0). Sources with no matching writer error with a
pointer to `convert --to … --factor`. To transform *into a different* container,
use `convert --to <target> --factor N`.

**⁶ edit (label/macro/thumbnail/overview remove|replace)** — pyramid tile bytes
are copied verbatim (no decode/re-encode); only the associated-image IFD is
rewritten. See [commands.md](commands.md#associated-image-editing)
for per-type coverage.

**⁷ OME-TIFF editing is lossy** — the file is rebuilt and a minimal OME-XML is
regenerated. Pyramid pixels, geometry/MPP/magnification, ICC, and the other
associated images are preserved; instrument/acquisition/channel/vendor metadata
is not. An always-on warning fires on every OME-TIFF edit, and associated
replacements are JPEG-only. See [ome-tiff-limitations.md](ome-tiff-limitations.md);
for faithful OME metadata carry-through use
[Bio-Formats](https://www.openmicroscopy.org/bio-formats/).

**⁸ DICOM `hash`** — use `--mode pixel`; file-mode is undefined for a multi-file
series. `dump-ifds` is TIFF-only and does not apply to DICOM.

**⁹ DICOM-WSI write is experimental** — `convert --to dicom` emits conformant WSM
VOLUME instances from a DICOM, JPEG-baseline, or JPEG 2000 source, either a
single instance (`--level N`) or the full pyramid by default. DICOM is also a
transform target (`--factor`, `downsample`, `crop`). See
[commands.md](commands.md#dicom-output) for the full behavior.

## DICOM source input

A DICOM source may be either a single `.dcm` instance or a directory containing a
WSM series — pass the path to either. A named `.dcm` always opens the series it
belongs to (its siblings sharing the same `SeriesUID`), even when the directory
holds other slides. If a directory holds more than one distinct WSM series,
wsitools refuses with an error that lists the candidate series; pass a specific
`.dcm` of the slide you want.
