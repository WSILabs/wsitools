# Per-format crop thumbnail regeneration — design spec

**Date:** 2026-06-13
**Status:** approved (brainstorming) — ready for implementation plan
**Addresses:** the MUST-ADDRESS follow-up from `2026-06-13-crop-format-preserving-design.md`
(non-SVS crops pass the source thumbnail through, so a cropped file's thumbnail
renders the *whole slide*).

---

## Goal

Make format-preserving crop **regenerate the thumbnail** for non-SVS containers
(generic-TIFF, OME-TIFF, cog-wsi), matching what the SVS path already does, so a
cropped file's thumbnail shows the crop — not the whole slide.

**Decision (locked):** regenerate **only when the source already has a thumbnail**.
If the source has none, do not invent one (stay faithful to the source's
associated-image set). This directly fixes the stale-thumbnail problem without
adding associated images a source didn't have.

The SVS path is unchanged (it already regenerates via `regenCropThumbnail` in a
postL0Hook).

---

## Architecture

The render+encode core of `regenCropThumbnail` is writer-agnostic; only the final
write differs per writer. Refactor + reuse:

### `renderCropThumbnail` (new, writer-agnostic)

```go
// renderCropThumbnail box-downscales the cropped L0 to a thumbnail (longest side
// thumbLongSide, aspect preserved) and returns the encoded baseline-JPEG bytes
// and its dimensions.
func renderCropThumbnail(l0 []byte, l0W, l0H, quality int) (jpegBytes []byte, tw, th int, err error)
```

This is the body of the current `regenCropThumbnail` up to and including the
`jpeg.Encode`. `regenCropThumbnail` (streamwriter) is refactored to:
`render → streamwriter.AddStripped(StrippedSpec{… WSIImageTypeThumbnail …})` —
**unchanged output**, and it already works for tiff + ome-tiff (same writer).

### `regenCropThumbnailCOGWSI` (new, cog-wsi)

```go
// regenCropThumbnailCOGWSI emits a regenerated thumbnail into a cogwsiwriter.
func regenCropThumbnailCOGWSI(w *cogwsiwriter.Writer, l0 []byte, l0W, l0H, quality int) error
```

`render → cogwsiwriter.AddAssociated(AssociatedSpec{Type: WSIImageTypeThumbnail,
Width, Height, Compression: JPEG, Photometric: 6, BitsPerSample: {8,8,8},
SamplesPerPixel: 3, Bytes: jpegBytes, RowsPerStrip: th})`.

### Per-emitter wiring

A small helper to detect a source thumbnail:

```go
// sourceHasThumbnail reports whether src carries a thumbnail associated image.
func sourceHasThumbnail(src *opentile.Slide) bool
```

- **`cropToTIFF`:** in the associated passthrough loop, **skip** the source
  thumbnail (`a.Type() == opentile.AssociatedThumbnail`). After the loop, if
  `sourceHasThumbnail(src)` and `!noAssociated`, call
  `regenCropThumbnail(w, l0, l0W, l0H, quality)`.
- **`cropToCOGWSI`:** in the faithful loop, **skip** the source thumbnail. After,
  if source had one and `!noAssociated`, call `regenCropThumbnailCOGWSI`.
- **`cropToOMETIFF`:** OME-XML enumerates associated dims, so the regenerated
  thumbnail's dims must be in the XML:
  1. Compute the regenerated thumbnail dims first (`thumbDims(l0W, l0H, thumbLongSide)`).
  2. Build `omeAssocs`: for the thumbnail entry use the **regenerated** dims (not
     the source's); other recognized associated keep their source dims.
  3. Build `omeXML`, create writer, build pyramid.
  4. In the write loop, **skip** the source thumbnail passthrough; for the others
     emit via `writeOneAssociated`. Then emit the regenerated thumbnail via
     `regenCropThumbnail`.
  (If the source has no thumbnail, behaviour is unchanged.)

> The Photometric on the regenerated thumbnail is `6` (YCbCr, stdlib JFIF) —
> cosmetic for opentile's decode-via-libjpeg path, same as the SVS thumbnail.

---

## Testing (local-only — large fixtures, not in CI)

The cog-wsi fixture (`CMU-1_cog-wsi.tiff`) carries a thumbnail, so it's the
primary case:

`TestCropThumbnailRegen` (or extend `TestCrop_FormatPreserving`):
- Crop `cog-wsi/CMU-1_cog-wsi.tiff` (re-encode); re-open; find the output
  thumbnail associated image. Assert its **aspect ratio ≈ the crop aspect**
  (within a tolerance), NOT the source thumbnail's aspect — proving it was
  regenerated, not passed through. (Source thumbnail is 1024×732 ≈ slide aspect;
  a 2000×2000 crop → thumbnail aspect ≈ 1.0.)
- Decode the output thumbnail (it must be a valid JPEG that opentile reads).
- Confirm label/overview still pass through (count/types unchanged besides the
  thumbnail being regenerated).

If a thumbnail-carrying OME-TIFF or generic-TIFF fixture is available, add a case;
otherwise the streamwriter path is covered structurally by the SVS thumbnail
tests (same `regenCropThumbnail`) plus the cog-wsi end-to-end test.

Regression: the existing `TestCrop_FormatPreserving` and SVS crop tests stay green.

---

## Out of scope

- Adding a thumbnail to sources that had none (locked decision).
- Lossless-verbatim non-SVS (Phase 2b — orthogonal).
- Changing the SVS thumbnail path.

## Risks / notes

- **OME-XML ↔ IFD consistency** is the main correctness risk: the regenerated
  thumbnail's dims in the OME-XML must equal the written IFD's dims. Compute
  `thumbDims` once and use it for both.
- **cogwsiwriter associated ordering / tile-order finalize:** confirm
  `AddAssociated` with a fresh single-strip JPEG thumbnail composes with the
  cog-wsi finalize pass (it spools associated like the faithful path).
- The SVS `regenCropThumbnail` refactor (extract `renderCropThumbnail`) must keep
  SVS output byte-identical — guarded by the SVS crop byte-identity/parity tests.
