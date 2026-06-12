# DICOM-WSI writer — Phase 1 slice 3: JPEG 2000 → DICOM — Design

**Status:** approved design, pre-plan.
**Date:** 2026-06-11
**Predecessors (all shipped to `main`):**
- Phase 0 — DICOM→DICOM single WSM VOLUME instance.
- Phase 1 slice 1 — non-DICOM (SVS) single level; marker-driven photometric, sRGB ICC synth (`2026-06-11-dicom-writer-phase1-svs-design.md`).
- Phase 1 slice 2 — full pyramid, multi-instance Series (`2026-06-11-dicom-writer-phase1-pyramid-design.md`).

## Goal

Extend `convert --to dicom` to accept sources whose pyramid-level tiles are
**JPEG 2000** (today only JPEG-baseline is accepted; other codecs error). Tile-copy
the raw J2K codestreams **verbatim** into DICOM with the correct JPEG 2000 transfer
syntax and a `PhotometricInterpretation` derived from the codestream — both
single-level (`--level N`) and full-pyramid (default), reusing all the shipped
machinery.

## Locked scope decisions (from brainstorming)

1. **Reversibility-driven transfer syntax + lossy signaling:** reversible/lossless
   source → `1.2.840.10008.1.2.4.90` (JPEG 2000 Lossless Only) + `LossyImageCompression
   "00"` (no method/ratio); irreversible/lossy → `1.2.840.10008.1.2.4.91`
   (JPEG 2000) + `LossyImageCompression "01"` + method `ISO_15444_1` + ratio.
2. **Full photometric map** (`MONOCHROME2` / `RGB` / `YBR_ICT` / `YBR_RCT`) derived
   from the codestream; the `RGB` path is e2e-validated against the fixture, the
   `YBR_ICT`/`YBR_RCT` (MCT=1) branches are unit-tested but **not** e2e-validated
   (no MCT=1 fixture available) — documented as such.
3. **Tile-copy verbatim only** (the shipped model) — raw J2K codestream copied
   as-is; non-JPEG/JP2K codecs still error.

## Verified facts (probed this session)

- Fixture `sample_files/svs/JP2K-33003-1.svs`: 3-level pyramid, tile 256×256, **raw
  J2K codestreams** (tile bytes start `FF 4F` SOC — directly DICOM-encapsulatable,
  no `.jp2` box stripping), **8-bit**, **3-component**, **MCT=0** (→ `RGB`),
  **irreversible/lossy** (→ `.91`), carries a **141992-byte ICC profile**, MPP
  0.2498. It is an opentile fixture (local; not in wsitools CI → validation is
  local, CI skips — same as the Grundium DICOM fixture for P0).
- `source.Level.Compression()` returns `source.CompressionJPEG2000` for these tiles.
- opentile can decode JP2K-in-DICOM (`formats/dicom/jp2k_decode_test.go`) → the
  pixel round-trip is feasible (cgo/openjpeg; the default build has it).
- COD/SIZ offsets (confirmed against `cmd/wsitools/quality/jpeg2000/jpeg2000.go`):
  COD payload (after its 2-byte length) — `[2:4]`=layers, `[4]`=multiple-component
  transform (0/1), `[9]`=wavelet (1=reversible, 0=irreversible). SIZ payload —
  `Csiz` (component count, 2 bytes) at payload offset 34; each component's `Ssiz`
  (precision = low 7 bits + 1) follows.
- `encapsulatePixelData` copies raw tile bytes verbatim and pads odd-length
  fragments — codec-agnostic, NO change needed for J2K.

## Architecture

A new pure-Go `jp2kmeta` parser (parallel to `jpegmeta`) reads the codestream;
`buildDescriptor` gains a JP2K branch; the `ImageDescriptor` carries the
transfer syntax + lossy fields so `assembleWSMDataset` stops hardcoding them.
The full-pyramid path composes for free (per-level `buildDescriptor`).

### Component 1 — `internal/dicomwriter/jp2kmeta.go`

```go
type JP2KInfo struct {
	Components int
	Precision  int
	MCT        bool // multiple-component transform used (COD[4] == 1)
	Reversible bool // reversible 5/3 wavelet (COD[9] == 1) → lossless
}

func InspectJP2K(j []byte) (JP2KInfo, error)        // walk SOC→SIZ→COD; error on missing/short/!SOC
func PhotometricJP2K(info JP2KInfo) (string, error) // map below
```

Photometric map:
- `Precision != 8` → error (>8-bit out of scope this slice).
- `Components == 1` → `MONOCHROME2`.
- `Components == 3 && !MCT` → `RGB`.
- `Components == 3 && MCT && Reversible` → `YBR_RCT`.
- `Components == 3 && MCT && !Reversible` → `YBR_ICT`.
- other component counts → error.

### Component 2 — `ImageDescriptor` (extend) + `assembleWSMDataset`

```go
type ImageDescriptor struct {
	TransferSyntax  string   // 1.2.840.10008.1.2.4.{50|90|91}
	Photometric     string
	SamplesPerPixel int      // 1 or 3 (now derived; was hardcoded 3)
	ImageType       []string
	ICCProfile      []byte
	Lossy           bool     // LossyImageCompression "01" (true) vs "00" (false)
	LossyMethod     string   // "ISO_10918_1" | "ISO_15444_1" (empty when lossless)
	LossyRatio      float64  // emitted only when Lossy
}
```

`assembleWSMDataset` changes (everything else unchanged):
- File-meta `TransferSyntaxUID` ← `desc.TransferSyntax` (was the `jpegBaselineTS` const).
- `SamplesPerPixel` ← `desc.SamplesPerPixel` (was `[]int{3}`).
- `LossyImageCompression` ← `desc.Lossy ? "01" : "00"`.
- `LossyImageCompressionRatio` + `LossyImageCompressionMethod` (group 0028) are
  emitted **only when `desc.Lossy`** — for a lossless instance both are omitted
  (they are Type 1C, conditional on `LossyImageCompression == "01"`). This is a
  new conditional in the ordered element list (like the optional `ICCProfile`).

### Component 3 — `buildDescriptor` codec branch

```
icc := md.ICCProfile; if empty → srgbICCProfile

DICOM source (Format=="dicom"):
    TransferSyntax=.50, Photometric="YBR_FULL_422", SamplesPerPixel=3,
    ImageType DERIVED…NONE, Lossy=true, LossyMethod="ISO_10918_1", LossyRatio
    → byte-identical to slice-2 output.

non-DICOM, lvl.Compression() == CompressionJPEG:
    info = jpegmeta.Inspect(tile0); photo = jpegmeta.Photometric(info)
    TransferSyntax=.50, SamplesPerPixel=info.Components, Lossy=true,
    LossyMethod="ISO_10918_1", ImageType per level (ORIGINAL@0 else DERIVED/RESAMPLED)

non-DICOM, lvl.Compression() == CompressionJPEG2000:
    info = jp2kmeta.InspectJP2K(tile0); photo = jp2kmeta.PhotometricJP2K(info)
    SamplesPerPixel=info.Components, ImageType per level
    if info.Reversible:  TransferSyntax=.90, Lossy=false, LossyMethod="", LossyRatio computed but unused
    else:                TransferSyntax=.91, Lossy=true,  LossyMethod="ISO_15444_1"

else: error "--to dicom: level N is <codec>; Phase 1 supports JPEG-baseline or JPEG 2000 tile-copy only"
```

The DICOM-source and JPEG paths keep their current emitted output (the new fields
are populated with the previously-hardcoded values). `LossyRatio` continues to be
computed in `writeInstance` from the real compressed byte total and passed in.

## Data flow (unchanged shape)

`writeInstance` → `encapsulatePixelData` (verbatim, codec-agnostic) → compute
`lossyRatio` → `buildDescriptor` (codec gate + probe + derive) → `assembleWSMDataset(desc)`
→ append PixelData → `dicom.Write`. `WritePyramid` loops it per level.

## Testing

1. **`jp2kmeta` unit tests** (`jp2kmeta_test.go`, synthetic codestreams built from
   SOC+SIZ+COD bytes): `RGB` (3-comp, MCT=0), `YBR_ICT` (MCT=1, irreversible),
   `YBR_RCT` (MCT=1, reversible), `MONOCHROME2` (1-comp), the `Reversible` flag
   (lossless vs lossy), and errors (not-SOC, no COD, precision≠8, bad component
   count).
2. **Pixel round-trip** (gated, JP2K-33003-1.svs): emit level 0 → DICOM; decode
   tile (0,0) from the emitted DICOM via opentile (honoring photometric) and from
   the source; assert equal — validates the **RGB** path end-to-end. (JP2K is lossy
   but tile-copy is verbatim, so both decode the identical codestream → identical
   pixels; this catches a colorspace/transfer-syntax mismatch.)
3. **dciodvfy** (`make dicom-validate` extended): emit the JP2K-33003-1 full
   pyramid; validate every `level-<n>.dcm` → 0 errors (covers `.91`/lossy/RGB +
   the per-level path).
4. **Regression:** the JPEG-baseline single-instance, SVS pixel round-trip,
   full-pyramid, and P0 DICOM paths stay green / structurally unchanged (the new
   descriptor fields reproduce their prior emitted values).

## Success criteria

- `convert --to dicom -o <dir> JP2K-33003-1.svs` emits a full pyramid where every
  `level-<n>.dcm`: passes `dciodvfy` (0 errors); has `TransferSyntaxUID` `.91`,
  `PhotometricInterpretation RGB`, `LossyImageCompression "01"`, method
  `ISO_15444_1`; opens via `source.Open` as `dicom`; and decodes to pixels matching
  the source (pixel round-trip green on level 0).
- A lossless/reversible J2K source would produce `.90` + `LossyImageCompression
  "00"` with the lossy ratio/method omitted (unit-tested mapping; no fixture).
- Non-JPEG/JP2K sources error clearly. JPEG and P0 DICOM output unchanged.

## Out of scope (later slices)

- >8-bit JP2K (16-bit), and `.jp2`-boxed (non-raw-codestream) inputs.
- Label / overview / thumbnail as separate DICOM instances (P2).
- HTJ2K (`.201/.202`), TILED_SPARSE, Concatenations, fluorescence.
- An e2e-validated YBR_ICT/YBR_RCT path (needs an MCT=1 fixture).

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `internal/dicomwriter/jp2kmeta.go` | new | J2K codestream inspection → components/precision/MCT/reversible + photometric |
| `internal/dicomwriter/jp2kmeta_test.go` | new | parser + photometric-map unit tests (synthetic codestreams) |
| `internal/dicomwriter/dataset.go` | modify | `ImageDescriptor` fields (transfer syntax, samples, lossy); conditional lossless tags |
| `internal/dicomwriter/dataset_test.go` | modify | descriptor-field + lossless-omission assertions |
| `internal/dicomwriter/dicomwriter.go` | modify | `buildDescriptor` JP2K branch + JPEG/DICOM fields populated explicitly |
| `cmd/wsitools/convert_dicom_test.go` | modify | JP2K SVS pixel round-trip |
| `Makefile` | modify | `dicom-validate` also emits + validates the JP2K-33003-1 pyramid |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md` | modify | document the slice |
