# DICOM-WSI writer — transcode non-tile-copyable associated images — Design

**Status:** approved design, pre-plan.
**Date:** 2026-06-12
**Predecessor:** Phase 2 (`2026-06-12-dicom-writer-phase2-associated-images-design.md`) — shipped associated images as same-Series instances, but **skips** any associated image whose codec isn't JPEG/JP2K.

## Goal

Emit the associated images that Phase 2 currently drops — primarily the **LZW
label present on every Aperio SVS** (the most clinically important associated
image: barcode / specimen ID) — by **decoding** them and storing them as
**uncompressed native-pixel-data** DICOM instances (lossless). LZW is not a DICOM
transfer syntax, so verbatim tile-copy can't handle it; decode + native re-store
is the conformant, lossless answer.

## Locked scope decisions (from brainstorming)

1. **Uncompressed native pixel data** (Explicit VR Little Endian,
   `1.2.840.10008.1.2.1`, `LossyImageCompression "00"`) — lossless, universally
   readable, keeps a barcode scannable. NOT a lossy re-encode.
2. **Decode is opentile-go's job** (read-side). opentile-go **v0.38.0** added
   `AssociatedImage.Decode(opts) (*decoder.Image, error)` (GH #20, closed) —
   faithful decode for every associated type + codec incl. **LZW+Predictor=2**,
   and fixed the root-cause `tifflzw` truncation. wsitools consumes it; the
   wsitools TIFF-reparse workaround is **deleted**.
3. **General rule:** tile-copyable associated images (JPEG/JP2K) stay verbatim
   encapsulated (Phase 2, unchanged); everything else is decoded → native.
4. **Spike first:** the native (non-encapsulated) PixelData write is a new
   library path — de-risk it before building on it (P0-style).

## Verified facts (probed this session)

- opentile-go v0.38.0 `AssociatedImage.Decode(opts decoder.DecodeOptions)
  (*decoder.Image, error)`; honors `Format` (`decoder.PixelFormatRGB`/`RGBA`);
  returns `decoder.ErrCodecUnavailable` when a codec isn't compiled (`nojp2k`).
  `decoder.Image{Pix []byte, Width, Height, Stride int, Format}`. The `internal/
  tifflzw` large-LZW writer bug (the #20 "decodes only a fraction" root cause) is
  fixed.
- wsitools `cmd/wsitools/extract.go` currently hand-rolls `decodeAssociated` +
  `readLZWFromTIFF` (re-reads the source TIFF strips for LZW+Predictor=2) +
  `rgbToImage`, with an `xtiff` (golang.org/x/image/tiff) dependency. These exist
  only because opentile's pre-0.38 `Bytes()` was lossy for LZW.
- wsitools `source.AssociatedImage` exposes `{Type, Size, Compression, Bytes,
  IFDOffset}`; `opentileAssociated` wraps `a opentile.AssociatedImage`.
- suyashkumar/dicom: `frame.NativeFrame[I]` + `dicom.PixelDataInfo{IsEncapsulated:
  false}` is the native pixel-data path (`pkg/frame/native.go`).
- `assembleWSMDataset(src, uids, spec instanceSpec)` already reads
  `TransferSyntax`/`Photometric`/`SamplesPerPixel`/`Lossy` from the spec and emits
  the Type-1C omissions; the **caller** appends the PixelData element. So a native
  instance needs no assembler change beyond the spec values + a different appended
  PixelData element.

## Architecture

Three pieces: (1) surface opentile's decode in the wsitools source layer + delete
the workaround; (2) a native-PixelData writer (de-risked by a spike); (3)
`writeAssociated` branches verbatim-vs-decode.

### Component 1 — `source.AssociatedImage.Decode()` + workaround deletion

- Bump `go.mod` to `github.com/wsilabs/opentile-go v0.38.0`; `go mod tidy`.
- Add `Decode(opts decoder.DecodeOptions) (*decoder.Image, error)` to the
  `source.AssociatedImage` interface; `opentileAssociated.Decode` is a pass-through
  to `a.a.Decode(opts)`. (Import `github.com/wsilabs/opentile-go/decoder` in
  `internal/source`.)
- **Delete** `readLZWFromTIFF`, the codec-dispatch in `decodeAssociated`, and the
  now-unused `xtiff` import from `cmd/wsitools/extract.go`; refactor `extract`'s
  decode call site to `match.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})`
  (+ convert `*decoder.Image` → `image.Image` for the PNG/JPEG encoders, reusing
  the existing `rgbToImage` if still needed). Extract's existing tests prove
  behavior-preserving.

### Component 2 — native (uncompressed) PixelData writer (SPIKE FIRST)

- New const `explicitVRLE = "1.2.840.10008.1.2.1"` (Explicit VR Little Endian).
- `nativePixelData(rgb []byte, rows, cols, samples int) (*dicom.Element, error)` —
  builds a `frame.NativeFrame[uint8]` (RawData = interleaved samples, Rows=rows,
  Cols=cols, SamplesPerPixel=samples, BitsPerSample=8) wrapped in
  `dicom.PixelDataInfo{IsEncapsulated:false, Frames:[…]}`, returned as the
  `tag.PixelData` element.
- **Spike (first task):** confirm the exact construction — whether
  `dicom.NewElement(tag.PixelData, info)` works for the native case (P0 found it
  SIGSEGVs for the *encapsulated* case; native is its default branch and should
  work with non-nil native data) or a hand-built element is needed — and that
  `dicom.Write` with `TransferSyntaxUID = explicitVRLE` emits valid Explicit-VR-LE
  native pixel data that `dciodvfy` accepts and opentile reads back. Record the
  working construction (like the P0 `smoke_test.go`).

### Component 3 — `writeAssociated` branch + `WritePyramid`

`writeAssociated` branches on `associatedSupported(comp)`:
- **JPEG/JP2K → verbatim encapsulated** (current path, unchanged).
- **else → decode → native:** `img, err := a.Decode(decoder.DecodeOptions{Format:
  decoder.PixelFormatRGB})`; on error return `errSkipAssociated` (wraps
  `decoder.ErrCodecUnavailable` etc.). Build the spec with `TransferSyntax =
  explicitVRLE`, `Photometric = "RGB"`, `SamplesPerPixel = 3`, `Lossy = false`,
  `LossyMethod = ""`, geometry from `a.Size()` (single frame), ICC carried/sRGB.
  Append `nativePixelData(rgbFrom(img), img.Height, img.Width, 3)`.
  (`rgbFrom` strips any row padding via `img.Stride`/`Width` to a tight
  `Height*Width*3` buffer.)

`WritePyramid`: **remove the pre-open skip** for unsupported codecs — every
associated image now emits (verbatim or decoded). Skip-with-warning survives only
as the fallback when `writeAssociated` returns `errSkipAssociated` (a genuine
decode failure, e.g. a JP2K associated image under `nojp2k`).

## Error handling

| Condition | Behavior |
|---|---|
| associated codec JPEG/JP2K | verbatim encapsulated (unchanged) |
| associated codec LZW / Deflate / none / other | decode → native uncompressed instance |
| `a.Decode` fails (`ErrCodecUnavailable`, corrupt) | `errSkipAssociated` → `slog.Warn` + skip; pyramid completes |
| pyramid level fails | fail-fast (unchanged) |

## Testing

1. **Spike test** (`dicomwriter`): a native-PixelData round-trip — build a small
   native instance, `dicom.Write`, `dicom.Parse` back, assert the pixels survive +
   `TransferSyntaxUID == explicitVRLE`. (De-risks the library path; mirrors P0.)
2. **`writeAssociated` native path unit** (Grundium or SVS via the writer): an LZW
   associated image emits with `TransferSyntaxUID = explicitVRLE`,
   `PhotometricInterpretation RGB`, `LossyImageCompression "00"`, NumberOfFrames 1,
   shared Series UID, correct ImageType flavor.
3. **CLI pixel round-trip** (`cmd/wsitools`, CMU-1-Small-Region.svs — its label is
   LZW): `convert --to dicom -o <dir>` now writes **`label.dcm`**; assert it exists,
   opens as `dicom`, and its decoded pixels equal the source label decode
   (`a.Decode` vs the emitted instance read back) — proving faithful LZW transcode.
4. **dciodvfy** (`make dicom-validate`): the native LZW-label instance passes with
   0 errors (extend a fixture emit to include the SVS label).
5. **`extract` regression:** extract tests stay green after the decode move
   (the LZW-label extract now goes through opentile's `Decode`).
6. **opentile-go suite (version-bump verification):** per CLAUDE.md, run
   opentile-go's own tests with its fixture gate to confirm `Decode` works
   (PASS, not SKIP) — `OPENTILE_TESTDIR=$(pwd)/sample_files go test
   ./decoder/... ./formats/...` in the opentile-go checkout.
7. **Regression:** tile-copyable associated + pyramid + single-instance + P0
   output unchanged.

## Success criteria

- `convert --to dicom -o <dir> CMU-1-Small-Region.svs` writes `label.dcm` (was
  dropped) as an uncompressed native RGB WSM instance that passes `dciodvfy`
  (0 errors), shares the Series, and decodes to pixels matching the source LZW
  label. The JPEG `overview`/`thumbnail` still emit verbatim-encapsulated.
- wsitools' `readLZWFromTIFF`/TIFF-reparse workaround is deleted; `extract` still
  works (via opentile `Decode`).
- A decode failure (e.g. JP2K associated under `nojp2k`) is skipped with a
  warning, not a crash.

## Out of scope (later)

- >8-bit / non-RGB native associated images (assume 8-bit RGB; mono is a later edge).
- Lossless *re-encode* (JPEG 2000 lossless) of decoded associated images — we use
  uncompressed.
- The pre-existing P0 DICOM-source codec-mislabel bug; the post-open-skip stray-file
  edge (largely moot once skips are rare).

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `go.mod` / `go.sum` | modify | opentile-go v0.37.0 → v0.38.0 |
| `internal/source/source.go` | modify | `AssociatedImage.Decode(opts) (*decoder.Image, error)` |
| `internal/source/opentile.go` | modify | `opentileAssociated.Decode` pass-through |
| `cmd/wsitools/extract.go` | modify | delete `readLZWFromTIFF` + codec-dispatch + `xtiff`; use `a.Decode()` |
| `internal/dicomwriter/encapsulate.go` (or new `native.go`) | modify/new | `nativePixelData` + `explicitVRLE` |
| `internal/dicomwriter/native_test.go` | new | spike round-trip |
| `internal/dicomwriter/dicomwriter.go` | modify | `writeAssociated` decode branch; `WritePyramid` drop pre-skip |
| `internal/dicomwriter/associated_test.go` | modify | native LZW-label unit |
| `cmd/wsitools/convert_dicom_test.go` | modify | LZW-label CLI pixel round-trip |
| `Makefile` | modify | `dicom-validate` covers the native label |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md` | modify | document |
