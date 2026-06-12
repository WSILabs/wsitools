# DICOM-WSI writer — Phase 1 (first slice): non-DICOM source, single level — Design

**Status:** approved design, pre-plan.
**Date:** 2026-06-11
**Predecessor:** Phase 0 spike (`docs/superpowers/specs/2026-06-11-dicom-writer-phase0-design.md`) — shipped to `main` (`convert --to dicom` emits one conformant WSM VOLUME instance from a **DICOM** source, verbatim JPEG copy, dciodvfy 0 errors).

## Goal

Extend `convert --to dicom` to accept a **non-DICOM source** (SVS and any other
opentile source whose selected level carries JPEG-baseline tiles) and emit **one
conformant WSM VOLUME instance for one pyramid level**, copying the source's JPEG
tiles **verbatim**, with the DICOM `PhotometricInterpretation` and ICC profile
derived to match the *actual* bytes — so the output is both structurally
conformant (`dciodvfy` 0 errors) and **pixel-correct** (no colorspace mismatch).

This is the first slice of Phase 1. Full pyramid (instance-per-level Series) and
non-JPEG codecs (JPEG 2000) are explicitly **later slices**.

## Locked scope decisions (from brainstorming)

1. **Single level, non-DICOM source.** Reuses `--level` (default 0). One VOLUME
   instance per invocation. Full pyramid deferred.
2. **JPEG-baseline tile-copy ONLY.** Verbatim-copy JPEG tiles (lossless, the P0
   path). **Error clearly** on any non-JPEG codec (JPEG 2000, JPEG XL, …). No
   re-encode path in this slice.
3. **Marker-driven photometric** — inspect the source JPEG's own markers to set
   `PhotometricInterpretation` exactly (not assumed).
4. **Pixel-correctness validation** — gate the slice on a *pixel* round-trip
   (decode emitted DICOM tile vs decode source tile), because `dciodvfy`
   validates structure only and is blind to a colorspace swap.
5. **ICC synthesis is mandatory** — carry the source ICC when present; embed a
   canonical **sRGB** profile when absent. (CMU-1-Small-Region.svs — the CI
   fixture — has *no* embedded ICC, so without synthesis its output would fail
   the Type 1C `ICCProfile` requirement.)
6. **Codec-driven, not format-gated** — accept any source whose selected level
   has JPEG-baseline tiles; the codec gate (decision 2) is the only constraint.
   No redundant Aperio/SVS format check. Validate on SVS in this slice.

## Architecture

`WriteVolumeInstance` stops requiring `src.Format() == "dicom"`. The new flow:

1. **Codec gate** — `lvl.Compression()` must be `source.CompressionJPEG`;
   otherwise a clear error (decision 2).
2. **Colorspace probe** — read tile (0,0) and inspect its JPEG markers
   (new `jpegmeta` helper) → colorspace + chroma-subsampling. All tiles in a
   level share an encoding, so one probe sets the photometric for the level.
3. **Derivation** — map the probe result to `PhotometricInterpretation`; derive
   `ImageType` from the level index; pick the ICC (carried or synthesized sRGB).
4. **Assemble + encapsulate** — `assembleWSMDataset` is generalized to take the
   derived `{photometric, imageType, icc}` rather than hardcoding them;
   `encapsulatePixelData` is unchanged (verbatim tile-copy).
5. **DICOM-source path is preserved byte-identically** — it passes the same
   derived struct populated with P0's existing values (YBR_FULL_422 / carried
   ICC / DERIVED…NONE), so P0 output does not change.

### Why a self-contained `jpegmeta` helper (not the source layer)

The marker scan is ~50 lines of pure byte-walking with no second consumer today.
Keeping it inside `internal/dicomwriter` avoids touching `internal/source` /
opentile-go (cross-repo). If JPEG 2000 / other codecs later need shared
colorspace detection, it can be promoted then (YAGNI).

## Components

### `internal/dicomwriter/jpegmeta.go` (new, pure Go, no cgo)

```
type JPEGColor int
const ( ColorYCbCr JPEGColor = iota; ColorRGB )

type JPEGInfo struct {
    Color        JPEGColor // from APP14 Adobe ColorTransform / JFIF convention
    Components   int       // SOF0 component count (expect 1 or 3)
    Precision    int       // SOF0 bit precision (expect 8)
    Subsampled   bool      // any chroma component Hi/Vi < luma's
}

func Inspect(jpeg []byte) (JPEGInfo, error)
```

Marker walk: require SOI (`FF D8`); scan segments; on `APP14` (`FF EE`) with an
`"Adobe"` payload, read the ColorTransform byte (0 → RGB, 1 → YCbCr, 2 → YCCK);
on `SOF0` (`FF C0`) read precision, component count, and per-component sampling
factors (`Hi<<4 | Vi`) to compute `Subsampled`. Stop at SOF (enough to decide).
Errors: missing SOI, truncation, no SOF.

Decision rule (encapsulated in a `Photometric(JPEGInfo) (string, error)`):
- `Components == 1` → `MONOCHROME2` (defensive; not expected for brightfield WSI).
- `Color == ColorRGB` → `RGB`.
- `Color == ColorYCbCr && Subsampled` → `YBR_FULL_422`.
- `Color == ColorYCbCr && !Subsampled` → `YBR_FULL`.
- `Precision != 8` or `Components ∉ {1,3}` → error (out of scope).

### `internal/dicomwriter/srgb.go` (new, pure data)

A canonical, freely-redistributable **sRGB IEC61966-2.1** ICC profile as a
`[]byte` constant (`var srgbICCProfile []byte`), plus a doc comment recording its
provenance/license. Used when `md.ICCProfile` is empty. (Implementation note: a
compact, standards-published sRGB v2 profile; verify it parses and that the
emitted instance passes dciodvfy's `ICCProfile` Type 1C check.)

### `internal/dicomwriter/dataset.go` (modified)

`assembleWSMDataset` gains a parameter carrying the derived values:

```
type ImageDescriptor struct {
    Photometric string   // "YBR_FULL_422" | "RGB" | "YBR_FULL" | "MONOCHROME2"
    ImageType   []string // e.g. {"ORIGINAL","PRIMARY","VOLUME","NONE"}
    ICCProfile  []byte   // carried or synthesized; never empty for color
    LossyRatio  float64  // unchanged from P0
}
```

`PhotometricInterpretation`, the `ImageType`/`FrameType` values, and the
OpticalPath `ICCProfile` are taken from this struct instead of the P0 hardcodes.
Everything else (geometry, dates, specimen, UIDs, ordering) is unchanged.
`FrameType` (in WholeSlideMicroscopyImageFrameTypeSequence) mirrors `ImageType`.

### `internal/dicomwriter/dicomwriter.go` (modified)

`WriteVolumeInstance` drops the DICOM-only guard, runs the codec gate, probes the
first tile, builds the `ImageDescriptor`, and threads it through. ICC selection:

```
icc := md.ICCProfile
if len(icc) == 0 { icc = srgbICCProfile }
```

`ImageType[0]` = `ORIGINAL` when `level == 0`, else `DERIVED`; the 4th value is
`NONE` for level 0, `RESAMPLED` for level > 0 (matching DICOM WSM frame-type
conventions and the P0 Grundium golden).

### `cmd/wsitools/convert_dicom.go` (modified)

Remove the "requires a DICOM source (P0)" rejection. The handler is otherwise
unchanged (overwrite guard, `os.Remove` on failure, success line). `--level`
already exists.

## Data flow (summary)

```
source.Open → lvl = Levels()[level]
  ├─ gate: lvl.Compression() == JPEG  (else error)
  ├─ probe: jpegmeta.Inspect(tile(0,0)) → JPEGInfo
  ├─ encapsulatePixelData(src, level) → pd, compressedBytes   // verbatim, unchanged
  ├─ derive: Photometric(info), ImageType(level), ICC = md.ICC || srgb, LossyRatio
  ├─ assembleWSMDataset(src, level, uids, ImageDescriptor{...})   // ratio folded into descriptor
  └─ dicom.Write
```

## Error handling

| Condition | Behavior |
|---|---|
| `lvl.Compression() != JPEG` | error: `--to dicom: level N is <codec>; P1 supports JPEG-baseline tile-copy only` |
| JPEG parse failure (tile 0,0) | wrapped error, abort (never guess a photometric) |
| Precision ≠ 8 or components ∉ {1,3} | error (16-bit / CMYK out of scope) |
| write failure | remove partial output (P0 pattern), return wrapped error |

## Testing

1. **`jpegmeta_test.go`** — hand-built minimal JPEG byte streams: APP14
   ColorTransform=0 (→ RGB), YCbCr 4:2:0 subsampled (→ YBR_FULL_422), YCbCr 4:4:4
   (→ YBR_FULL), 1-component (→ MONOCHROME2), and malformed (truncated / no SOF →
   error). Assert `Inspect` fields + `Photometric` output.
2. **Pixel round-trip** (gated, **CMU-1-Small-Region.svs**, CI-available): emit
   SVS→DICOM level 0; decode tile (0,0) from the emitted DICOM and tile (0,0)
   from the source via the codec layer; assert decoded pixels match (exact for a
   true verbatim copy). This is the colorspace-mismatch safety net.
3. **dciodvfy 0 errors** — extend `make dicom-validate` to also emit + validate
   the SVS fixture (exercises the synthesized-sRGB ICC path; confirms 0 errors,
   benign Study-ID warning only).
4. **ICC assertions** — emitted instance has an `ICCProfile` in OpticalPath
   (synthesized when the source has none); unit-assert via the existing
   dataset-test pattern.
5. **Regression** — the existing DICOM-source tests stay green and P0 output is
   byte-identical (the descriptor passes P0's values for DICOM sources).

## Success criteria

- `convert --to dicom -o out.dcm --level 0 CMU-1-Small-Region.svs` produces a
  WSM VOLUME instance that (a) passes `dciodvfy` with **0 errors**, (b)
  round-trips through opentile-go (`Format: dicom`), and (c) decodes to pixels
  matching the source (pixel round-trip test green).
- Non-JPEG sources error clearly.
- The DICOM-source path (P0) is unchanged (byte-identical output, tests green).

## Out of scope (later slices / phases)

- Full pyramid (instance per level, shared Study/Series/FrameOfReference).
- JPEG 2000 (and other) codecs → DICOM (per-codec transfer syntax, J2K
  conformance pass).
- Re-encode path (decode + recompress) for non-tile-copyable sources.
- `TILED_SPARSE`, label/overview as separate instances, Concatenations.
- 16-bit / fluorescence / multi-channel / z-stack.
- Richer (non-anonymous) Patient/Study/Specimen identity.

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `internal/dicomwriter/jpegmeta.go` | new | JPEG marker inspection → colorspace/photometric |
| `internal/dicomwriter/jpegmeta_test.go` | new | unit tests for the marker parser + photometric map |
| `internal/dicomwriter/srgb.go` | new | embedded canonical sRGB ICC profile (`[]byte`) |
| `internal/dicomwriter/dataset.go` | modify | accept `ImageDescriptor` (photometric/ImageType/ICC) instead of hardcodes |
| `internal/dicomwriter/dicomwriter.go` | modify | codec gate + probe + derivation + ICC selection |
| `internal/dicomwriter/dicomwriter_test.go` | modify | SVS→DICOM pixel round-trip + ICC assertions |
| `cmd/wsitools/convert_dicom.go` | modify | drop the DICOM-only guard |
| `Makefile` | modify | `dicom-validate` also runs the SVS fixture |
| `README.md` / `CHANGELOG.md` / roadmap / scoping note | modify | document the slice |
