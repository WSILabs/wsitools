# DICOM-WSI writer — Phase 1 slice 3 (JPEG 2000 → DICOM) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `convert --to dicom` to tile-copy JPEG 2000 pyramid-level tiles verbatim into DICOM with the reversibility-driven JP2K transfer syntax (`…4.90` lossless / `…4.91` lossy) and a codestream-derived `PhotometricInterpretation` — for both the single-level and full-pyramid paths.

**Architecture:** A new pure-Go `jp2kmeta` parser reads the J2K codestream's SIZ/COD markers (components, precision, MCT, reversibility). `ImageDescriptor` grows to carry the transfer syntax + lossy-compression fields (today hardcoded); `assembleWSMDataset` reads them and conditionally omits the lossy tags for a lossless instance. `buildDescriptor` gains a JP2K branch. `encapsulatePixelData` is unchanged (raw codestream copy is codec-agnostic).

**Tech Stack:** Go, `github.com/suyashkumar/dicom` v1.1.0, opentile-go (JP2K decode via cgo/openjpeg), `dciodvfy` (dicom3tools).

**Spec:** `docs/superpowers/specs/2026-06-11-dicom-writer-phase1-jp2k-design.md`

**Branch:** create `feat/dicom-writer-phase1-jp2k` off `main`. Never implement on `main`.

**Verified facts (probed this session — do not re-derive):**
- Fixture `sample_files/svs/JP2K-33003-1.svs`: 3-level pyramid, tile 256×256, raw J2K codestreams (tiles start `FF 4F` SOC), **8-bit, 3-component, MCT=0 → RGB, irreversible/lossy → `…4.91`**, ICC present (141992 bytes), MPP 0.2498. Local fixture (not in wsitools CI → validation local, CI skips).
- `source.Level.Compression()` returns `source.CompressionJPEG2000` for these tiles. `source.CompressionJPEG` is the JPEG enum.
- **J2K codestream offsets** (segData = bytes AFTER each marker's 2-byte length field, the convention in `cmd/wsitools/quality/jpeg2000/jpeg2000.go`):
  - **SIZ** (`FF51`): `Csiz` (component count, uint16) at `segData[34:36]`; component-0 `Ssiz` at `segData[36]`, precision = `(Ssiz & 0x7F) + 1`.
  - **COD** (`FF52`): `segData[4]` = multiple-component-transform (0=none, 1=used); `segData[9]` = wavelet transform (1=reversible/lossless, 0=irreversible/lossy).
  - Markers walk from SOC (`FF4F`, no length); stop at SOD (`FF93`) or EOC (`FFD9`). SIZ precedes COD in the main header.
- Current `dataset.go`: `jpegBaselineTS = "1.2.840.10008.1.2.4.50"` const (line 21); file-meta `mk(tag.TransferSyntaxUID, []string{jpegBaselineTS})` (line 177); `mk(tag.SamplesPerPixel, []int{3})` (line 224); `mk(tag.PhotometricInterpretation, []string{desc.Photometric})` (line 225); `mk(tag.LossyImageCompression, []string{"01"})` / `…Ratio, []string{ratioStr}` / `…Method, []string{"ISO_10918_1"}` (lines 235-237); `ratioStr := fmt.Sprintf("%.4g", desc.LossyRatio)` (line 88). The element list is one big `elems := []*dicom.Element{…}` literal; `ImageDescriptor` is at lines 47-56.
- Current `dicomwriter.go` `buildDescriptor(src, level, lossyRatio)` codec-gates `!= source.CompressionJPEG`, probes tile(0,0), `Inspect`→`Photometric`, builds `ImageDescriptor{Photometric, ImageType, ICCProfile, LossyRatio}`. `jpegmeta` exposes `Inspect(j) (JPEGInfo,error)`, `Photometric(JPEGInfo) (string,error)`; `JPEGInfo` has a `Components` field. `srgbICCProfile` exists.
- opentile decode-to-RGB for the pixel round-trip: `opentile.OpenFile(path)` → `slide.ImageReadRegion(img, level, x, y, w, h, opentile.WithFormat(decoder.PixelFormatRGB))` → `*decoder.Image{Pix,Width,Height}`. Imports `opentile "github.com/wsilabs/opentile-go"`, `"github.com/wsilabs/opentile-go/decoder"`.

---

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `internal/dicomwriter/jp2kmeta.go` | new | J2K codestream inspection → components/precision/MCT/reversible + photometric |
| `internal/dicomwriter/jp2kmeta_test.go` | new | parser + photometric-map unit tests (synthetic) + gated real-fixture check |
| `internal/dicomwriter/dataset.go` | modify | JP2K transfer-syntax consts; `ImageDescriptor` fields; transfer-syntax/samples from desc; conditional lossless tags |
| `internal/dicomwriter/dataset_test.go` | modify | update call sites to new fields; lossless-omission test |
| `internal/dicomwriter/dicomwriter.go` | modify | `buildDescriptor` codec switch (JPEG/JP2K) + populate new fields |
| `cmd/wsitools/convert_dicom_test.go` | modify | JP2K SVS pixel round-trip |
| `Makefile` | modify | `dicom-validate` also emits + validates the JP2K-33003-1 pyramid |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md` | modify | document the slice |

---

## Task 1: `jp2kmeta` codestream parser

**Files:** create `internal/dicomwriter/jp2kmeta.go`, `internal/dicomwriter/jp2kmeta_test.go`.

- [ ] **Step 1: Write the failing test** — create `internal/dicomwriter/jp2kmeta_test.go`:

```go
package dicomwriter

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

// j2kStream: SOC + segments + SOD (enough for a main-header parse).
func j2kStream(segs ...[]byte) []byte {
	out := []byte{0xFF, 0x4F}
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, 0xFF, 0x93) // SOD
}

// siz builds a SIZ marker (FF51) with the given component count + 8-bit-style
// precision (precision byte is encoded as (precision-1)&0x7F in Ssiz).
func siz(components int, precision byte) []byte {
	body := make([]byte, 36) // Rsiz(2)+8×4 fields(32)+Csiz(2) → Csiz at [34:36]
	binary.BigEndian.PutUint16(body[34:36], uint16(components))
	for c := 0; c < components; c++ {
		body = append(body, (precision-1)&0x7F, 0x01, 0x01) // Ssiz, XRsiz, YRsiz
	}
	segLen := 2 + len(body)
	seg := []byte{0xFF, 0x51, byte(segLen >> 8), byte(segLen)}
	return append(seg, body...)
}

// cod builds a COD marker (FF52) with the given MCT (0/1) and transform
// (0=irreversible/lossy, 1=reversible/lossless).
func cod(mct, transform byte) []byte {
	body := make([]byte, 10) // Scod,prog,layers(2),MCT,decomp,cbW,cbH,cbStyle,transform
	body[4] = mct
	body[9] = transform
	segLen := 2 + len(body)
	seg := []byte{0xFF, 0x52, byte(segLen >> 8), byte(segLen)}
	return append(seg, body...)
}

func TestInspectJP2K_RGB(t *testing.T) {
	info, err := InspectJP2K(j2kStream(siz(3, 8), cod(0, 0)))
	if err != nil {
		t.Fatalf("InspectJP2K: %v", err)
	}
	if info.Components != 3 || info.Precision != 8 || info.MCT || info.Reversible {
		t.Errorf("got %+v, want comps=3 prec=8 MCT=false Reversible=false", info)
	}
	if p, _ := PhotometricJP2K(info); p != "RGB" {
		t.Errorf("Photometric = %q, want RGB", p)
	}
}

func TestInspectJP2K_YBR_ICT(t *testing.T) {
	info, _ := InspectJP2K(j2kStream(siz(3, 8), cod(1, 0))) // MCT + irreversible
	if !info.MCT || info.Reversible {
		t.Errorf("got MCT=%v Reversible=%v, want true/false", info.MCT, info.Reversible)
	}
	if p, _ := PhotometricJP2K(info); p != "YBR_ICT" {
		t.Errorf("Photometric = %q, want YBR_ICT", p)
	}
}

func TestInspectJP2K_YBR_RCT(t *testing.T) {
	info, _ := InspectJP2K(j2kStream(siz(3, 8), cod(1, 1))) // MCT + reversible
	if p, _ := PhotometricJP2K(info); p != "YBR_RCT" {
		t.Errorf("Photometric = %q, want YBR_RCT", p)
	}
	if !info.Reversible {
		t.Errorf("Reversible = false, want true")
	}
}

func TestInspectJP2K_Mono(t *testing.T) {
	info, _ := InspectJP2K(j2kStream(siz(1, 8), cod(0, 0)))
	if p, _ := PhotometricJP2K(info); p != "MONOCHROME2" {
		t.Errorf("Photometric = %q, want MONOCHROME2", p)
	}
}

func TestInspectJP2K_Errors(t *testing.T) {
	if _, err := InspectJP2K([]byte{0x00, 0x01}); err == nil {
		t.Error("want error for non-SOC input")
	}
	if _, err := InspectJP2K(j2kStream(siz(3, 8))); err == nil {
		t.Error("want error when COD is missing")
	}
	if _, err := PhotometricJP2K(JP2KInfo{Components: 3, Precision: 12}); err == nil {
		t.Error("want error for precision != 8")
	}
	if _, err := PhotometricJP2K(JP2KInfo{Components: 2, Precision: 8}); err == nil {
		t.Error("want error for component count 2")
	}
}

// Real-fixture sanity: JP2K-33003-1.svs L0 is 3-component, 8-bit, MCT=0, lossy.
func TestInspectJP2K_RealFixture(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no JP2K fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	lvl := src.Levels()[0]
	buf := make([]byte, lvl.TileMaxSize())
	n, err := lvl.TileInto(0, 0, buf)
	if err != nil {
		t.Fatal(err)
	}
	info, err := InspectJP2K(buf[:n])
	if err != nil {
		t.Fatalf("InspectJP2K(real tile): %v", err)
	}
	if info.Components != 3 || info.Precision != 8 || info.MCT {
		t.Errorf("real fixture: got %+v, want comps=3 prec=8 MCT=false", info)
	}
	if p, _ := PhotometricJP2K(info); p != "RGB" {
		t.Errorf("real fixture Photometric = %q, want RGB", p)
	}
}
```

- [ ] **Step 2: Run, verify FAIL**

`WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ -run 'JP2K' -v 2>&1 | grep -v "duplicate lib"` → FAIL (undefined `InspectJP2K`/`PhotometricJP2K`/`JP2KInfo`).

- [ ] **Step 3: Implement** — create `internal/dicomwriter/jp2kmeta.go`:

```go
package dicomwriter

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// JP2KInfo is what InspectJP2K extracts from a JPEG 2000 codestream's main header.
type JP2KInfo struct {
	Components int
	Precision  int  // bit depth of component 0
	MCT        bool // multiple-component transform used (COD multi-comp byte == 1)
	Reversible bool // reversible 5/3 wavelet (COD transform byte == 1) → lossless
}

// InspectJP2K parses a raw J2K codestream's SIZ + COD markers. It expects the
// codestream to start at SOC (FF4F) — exactly what opentile's TileInto returns
// for a JPEG-2000 tile.
func InspectJP2K(j []byte) (JP2KInfo, error) {
	if len(j) < 4 || j[0] != 0xFF || j[1] != 0x4F {
		return JP2KInfo{}, errors.New("jp2kmeta: not a J2K codestream (missing SOC)")
	}
	var (
		info   JP2KInfo
		sawSIZ bool
		sawCOD bool
	)
	i := 2
	for i+2 <= len(j) {
		if j[i] != 0xFF {
			return JP2KInfo{}, fmt.Errorf("jp2kmeta: expected marker at offset %d, got %#x", i, j[i])
		}
		m := j[i+1]
		i += 2
		if m == 0x93 || m == 0xD9 { // SOD or EOC → main header done
			break
		}
		if i+2 > len(j) {
			return JP2KInfo{}, errors.New("jp2kmeta: truncated marker length")
		}
		segLen := int(binary.BigEndian.Uint16(j[i : i+2]))
		if segLen < 2 || i+segLen > len(j) {
			return JP2KInfo{}, fmt.Errorf("jp2kmeta: invalid segment length %d", segLen)
		}
		seg := j[i+2 : i+segLen]
		i += segLen
		switch m {
		case 0x51: // SIZ
			if len(seg) < 37 {
				return JP2KInfo{}, errors.New("jp2kmeta: short SIZ")
			}
			info.Components = int(binary.BigEndian.Uint16(seg[34:36]))
			info.Precision = int(seg[36]&0x7F) + 1
			sawSIZ = true
		case 0x52: // COD
			if len(seg) < 10 {
				return JP2KInfo{}, errors.New("jp2kmeta: short COD")
			}
			info.MCT = seg[4] == 1
			info.Reversible = seg[9] == 1
			sawCOD = true
		}
		if sawSIZ && sawCOD {
			break
		}
	}
	if !sawSIZ {
		return JP2KInfo{}, errors.New("jp2kmeta: no SIZ marker found")
	}
	if !sawCOD {
		return JP2KInfo{}, errors.New("jp2kmeta: no COD marker found")
	}
	return info, nil
}

// PhotometricJP2K maps a JP2KInfo to the DICOM PhotometricInterpretation for a
// verbatim tile-copy of that codestream.
func PhotometricJP2K(info JP2KInfo) (string, error) {
	if info.Precision != 8 {
		return "", fmt.Errorf("jp2kmeta: unsupported precision %d (want 8)", info.Precision)
	}
	switch info.Components {
	case 1:
		return "MONOCHROME2", nil
	case 3:
		if !info.MCT {
			return "RGB", nil
		}
		if info.Reversible {
			return "YBR_RCT", nil
		}
		return "YBR_ICT", nil
	default:
		return "", fmt.Errorf("jp2kmeta: unsupported component count %d (want 1 or 3)", info.Components)
	}
}
```

- [ ] **Step 4: Run, verify PASS + clean**

`WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ -run 'JP2K' -v 2>&1 | grep -v "duplicate lib"` → all PASS (incl. the real-fixture check). `gofmt -l internal/dicomwriter/jp2kmeta.go internal/dicomwriter/jp2kmeta_test.go` empty; `go vet ./internal/dicomwriter/`.

- [ ] **Step 5: Commit**
```bash
git add internal/dicomwriter/jp2kmeta.go internal/dicomwriter/jp2kmeta_test.go
git commit -m "feat(dicomwriter): JPEG 2000 codestream inspection (SIZ/COD -> photometric)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `ImageDescriptor` extension + `assembleWSMDataset` + `buildDescriptor` JP2K branch

**Files:** modify `internal/dicomwriter/dataset.go`, `internal/dicomwriter/dataset_test.go`, `internal/dicomwriter/dicomwriter.go`. (Committed together — the assembler change and the descriptor producer must move in lockstep.)

- [ ] **Step 1: Add JP2K transfer-syntax consts** — in `internal/dicomwriter/dataset.go`, find:
```go
	jpegBaselineTS = "1.2.840.10008.1.2.4.50"         // JPEG Baseline (Process 1)
```
and add two lines after it (inside the same const block):
```go
	jpegBaselineTS = "1.2.840.10008.1.2.4.50"         // JPEG Baseline (Process 1)
	jp2kLosslessTS = "1.2.840.10008.1.2.4.90"         // JPEG 2000 (Lossless Only)
	jp2kTS         = "1.2.840.10008.1.2.4.91"         // JPEG 2000 Image Compression
```

- [ ] **Step 2: Extend `ImageDescriptor`** — replace the struct (dataset.go:47-56) with:

```go
// ImageDescriptor carries the codec/colorspace-dependent attributes that vary by
// source. The caller (writeInstance, via buildDescriptor) derives these — probing
// a non-DICOM source's codestream, or using P0's fixed values for a DICOM source —
// so the assembler stays a pure dataset builder.
type ImageDescriptor struct {
	TransferSyntax  string   // 1.2.840.10008.1.2.4.{50|90|91}
	Photometric     string   // RGB | YBR_FULL_422 | YBR_FULL | YBR_ICT | YBR_RCT | MONOCHROME2
	SamplesPerPixel int      // 1 or 3
	ImageType       []string // ImageType + FrameType value (4 elements)
	ICCProfile      []byte   // carried or synthesized; non-empty for color
	Lossy           bool     // LossyImageCompression "01" (true) vs "00" (false)
	LossyMethod     string   // "ISO_10918_1" | "ISO_15444_1" (empty when lossless)
	LossyRatio      float64  // emitted only when Lossy
}
```

- [ ] **Step 3: Use the new fields in `assembleWSMDataset`** — three edits in dataset.go:

(a) Find `ratioStr := fmt.Sprintf("%.4g", desc.LossyRatio)` and add the lossy flag right after it:
```go
	// LossyImageCompressionRatio (Type 1C, DS, ≤16 chars) — %.4g keeps it compact.
	ratioStr := fmt.Sprintf("%.4g", desc.LossyRatio)
	lossyFlag := "00"
	if desc.Lossy {
		lossyFlag = "01"
	}
```

(b) In the `elems` literal: change the TransferSyntaxUID, SamplesPerPixel, and the three LossyImageCompression lines.
Find:
```go
		mk(tag.TransferSyntaxUID, []string{jpegBaselineTS}),
```
Replace with:
```go
		mk(tag.TransferSyntaxUID, []string{desc.TransferSyntax}),
```
Find:
```go
		mk(tag.SamplesPerPixel, []int{3}),
```
Replace with:
```go
		mk(tag.SamplesPerPixel, []int{desc.SamplesPerPixel}),
```
Find:
```go
		mk(tag.LossyImageCompression, []string{"01"}),
		mk(tag.LossyImageCompressionRatio, []string{ratioStr}),
		mk(tag.LossyImageCompressionMethod, []string{"ISO_10918_1"}),
```
Replace with:
```go
		mk(tag.LossyImageCompression, []string{lossyFlag}),
		mk(tag.LossyImageCompressionRatio, []string{ratioStr}),
		mk(tag.LossyImageCompressionMethod, []string{desc.LossyMethod}),
```

(c) Conditionally drop the lossy ratio+method for a lossless instance. Find the tail of the function (where `firstErr` is checked and the Dataset returned):
```go
	if firstErr != nil {
		return dicom.Dataset{}, firstErr
	}
	return dicom.Dataset{Elements: elems}, nil
}
```
Replace with:
```go
	if firstErr != nil {
		return dicom.Dataset{}, firstErr
	}
	// LossyImageCompressionRatio + Method are Type 1C — present only when
	// LossyImageCompression == "01". For a lossless instance, omit them.
	if !desc.Lossy {
		kept := elems[:0]
		for _, e := range elems {
			if e.Tag == tag.LossyImageCompressionRatio || e.Tag == tag.LossyImageCompressionMethod {
				continue
			}
			kept = append(kept, e)
		}
		elems = kept
	}
	return dicom.Dataset{Elements: elems}, nil
}
```

- [ ] **Step 4: Rewrite `buildDescriptor`** — in `internal/dicomwriter/dicomwriter.go`, replace the whole `buildDescriptor` function with:

```go
// buildDescriptor derives the codec/colorspace-dependent attributes for src
// level `level`. DICOM sources reuse P0's fixed values; non-DICOM sources are
// gated to JPEG-baseline or JPEG 2000 tiles and their photometric + transfer
// syntax are derived from the tile's codestream. ICC is carried, or sRGB-synthesized.
func buildDescriptor(src source.Source, level int, lossyRatio float64) (ImageDescriptor, error) {
	md := src.Metadata()
	icc := md.ICCProfile
	if len(icc) == 0 {
		icc = srgbICCProfile
	}

	if src.Format() == "dicom" {
		// P0 path: Grundium-mirrored values, unchanged output.
		return ImageDescriptor{
			TransferSyntax:  jpegBaselineTS,
			Photometric:     "YBR_FULL_422",
			SamplesPerPixel: 3,
			ImageType:       []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
			ICCProfile:      icc,
			Lossy:           true,
			LossyMethod:     "ISO_10918_1",
			LossyRatio:      lossyRatio,
		}, nil
	}

	lvl := src.Levels()[level]
	comp := lvl.Compression()
	if comp != source.CompressionJPEG && comp != source.CompressionJPEG2000 {
		return ImageDescriptor{}, fmt.Errorf(
			"--to dicom: level %d is %s; Phase 1 supports JPEG-baseline or JPEG 2000 tile-copy only",
			level, comp)
	}

	buf := make([]byte, lvl.TileMaxSize())
	n, err := lvl.TileInto(0, 0, buf)
	if err != nil {
		return ImageDescriptor{}, fmt.Errorf("read tile (0,0) for codec probe: %w", err)
	}
	tile := buf[:n]

	// Level 0 of a non-DICOM slide is the native acquisition (ORIGINAL); reduced
	// levels are downsampled (DERIVED / RESAMPLED).
	imageType := []string{"ORIGINAL", "PRIMARY", "VOLUME", "NONE"}
	if level > 0 {
		imageType = []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"}
	}
	desc := ImageDescriptor{ImageType: imageType, ICCProfile: icc, LossyRatio: lossyRatio}

	switch comp {
	case source.CompressionJPEG:
		info, err := Inspect(tile)
		if err != nil {
			return ImageDescriptor{}, fmt.Errorf("inspect source JPEG: %w", err)
		}
		photo, err := Photometric(info)
		if err != nil {
			return ImageDescriptor{}, fmt.Errorf("derive photometric from source JPEG: %w", err)
		}
		desc.TransferSyntax = jpegBaselineTS
		desc.Photometric = photo
		desc.SamplesPerPixel = info.Components
		desc.Lossy = true
		desc.LossyMethod = "ISO_10918_1"
	case source.CompressionJPEG2000:
		info, err := InspectJP2K(tile)
		if err != nil {
			return ImageDescriptor{}, fmt.Errorf("inspect source JPEG 2000: %w", err)
		}
		photo, err := PhotometricJP2K(info)
		if err != nil {
			return ImageDescriptor{}, fmt.Errorf("derive photometric from source JPEG 2000: %w", err)
		}
		desc.Photometric = photo
		desc.SamplesPerPixel = info.Components
		if info.Reversible {
			desc.TransferSyntax = jp2kLosslessTS
			desc.Lossy = false
			desc.LossyMethod = ""
		} else {
			desc.TransferSyntax = jp2kTS
			desc.Lossy = true
			desc.LossyMethod = "ISO_15444_1"
		}
	}
	return desc, nil
}
```

- [ ] **Step 5: Update the existing `dataset_test.go` call sites** — both construct an `ImageDescriptor` and now need the new fields.

In `TestAssembleWSMDataset`, find:
```go
	ds, err := assembleWSMDataset(src, level, uids, ImageDescriptor{
		Photometric: "YBR_FULL_422",
		ImageType:   []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		ICCProfile:  src.Metadata().ICCProfile, // Grundium fixture carries an ICC profile
		LossyRatio:  10.0,
	})
```
Replace with:
```go
	ds, err := assembleWSMDataset(src, level, uids, ImageDescriptor{
		TransferSyntax:  jpegBaselineTS,
		Photometric:     "YBR_FULL_422",
		SamplesPerPixel: 3,
		ImageType:       []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		ICCProfile:      src.Metadata().ICCProfile, // Grundium fixture carries an ICC profile
		Lossy:           true,
		LossyMethod:     "ISO_10918_1",
		LossyRatio:      10.0,
	})
```

In `TestPerLevelSpatialMetadata`, find:
```go
	desc := ImageDescriptor{
		Photometric: "YBR_FULL_422",
		ImageType:   []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		ICCProfile:  src.Metadata().ICCProfile,
		LossyRatio:  10.0,
	}
```
Replace with:
```go
	desc := ImageDescriptor{
		TransferSyntax:  jpegBaselineTS,
		Photometric:     "YBR_FULL_422",
		SamplesPerPixel: 3,
		ImageType:       []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		ICCProfile:      src.Metadata().ICCProfile,
		Lossy:           true,
		LossyMethod:     "ISO_10918_1",
		LossyRatio:      10.0,
	}
```

- [ ] **Step 6: Add a lossless-omission test** — append to `dataset_test.go` (uses the Grundium fixture as a source for geometry; the descriptor drives the lossy tags):

```go
func TestAssembleWSMDatasetLosslessOmitsLossyTags(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no dicom fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	uids := UIDSet{SOP: NewUID(), Study: NewUID(), Series: NewUID(), FrameOfReference: NewUID(), DimensionOrg: NewUID()}
	ds, err := assembleWSMDataset(src, 0, uids, ImageDescriptor{
		TransferSyntax:  jp2kLosslessTS,
		Photometric:     "RGB",
		SamplesPerPixel: 3,
		ImageType:       []string{"ORIGINAL", "PRIMARY", "VOLUME", "NONE"},
		ICCProfile:      src.Metadata().ICCProfile,
		Lossy:           false, // lossless → ratio + method must be omitted
		LossyMethod:     "",
		LossyRatio:      1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if e, err := ds.FindElementByTag(tag.LossyImageCompression); err != nil {
		t.Errorf("LossyImageCompression missing: %v", err)
	} else if v := e.Value.GetValue().([]string); len(v) == 0 || v[0] != "00" {
		t.Errorf("LossyImageCompression = %v, want 00", e.Value.GetValue())
	}
	if _, err := ds.FindElementByTag(tag.LossyImageCompressionRatio); err == nil {
		t.Error("LossyImageCompressionRatio present on a lossless instance (must be omitted)")
	}
	if _, err := ds.FindElementByTag(tag.LossyImageCompressionMethod); err == nil {
		t.Error("LossyImageCompressionMethod present on a lossless instance (must be omitted)")
	}
	if e, err := ds.FindElementByTag(tag.TransferSyntaxUID); err != nil {
		t.Errorf("TransferSyntaxUID missing: %v", err)
	} else if v := e.Value.GetValue().([]string); v[0] != jp2kLosslessTS {
		t.Errorf("TransferSyntaxUID = %v, want %s", e.Value.GetValue(), jp2kLosslessTS)
	}
}
```

- [ ] **Step 7: Build, test, commit**

```bash
go build ./... 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ ./cmd/wsitools/ -run 'DICOM|Dicom|WSM|Assemble|Encapsulat|NewUID|ConvertDICOM|Inspect|SRGB|PixelRoundTrip|PerLevel|WritePyramid|JP2K|Lossless' -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l internal/dicomwriter/dataset.go internal/dicomwriter/dataset_test.go internal/dicomwriter/dicomwriter.go
go vet ./internal/dicomwriter/ ./cmd/wsitools/
```
Expected: PASS — the JPEG/DICOM regression tests (`TestConvertDICOMReadBack`, `TestSVSToDICOMPixelRoundTrip`, `TestWriteVolumeInstanceRoundTrip`, `TestWritePyramid`, `TestPerLevelSpatialMetadata`) stay green (the new fields reproduce their prior emitted values: `.50`/3-samples/`01`/`ISO_10918_1`), plus the new lossless-omission test. gofmt empty; vet clean.
```bash
git add internal/dicomwriter/dataset.go internal/dicomwriter/dataset_test.go internal/dicomwriter/dicomwriter.go
git commit -m "feat(dicomwriter): JPEG 2000 descriptor (transfer syntax + lossy tags) + codec branch

ImageDescriptor now carries TransferSyntax/SamplesPerPixel/Lossy/LossyMethod;
assembleWSMDataset reads them and omits LossyImageCompressionRatio/Method for a
lossless instance. buildDescriptor gains a JPEG-2000 branch (reversibility-driven
.90/.91). JPEG + DICOM paths reproduce their prior output.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: JP2K SVS pixel round-trip

**Files:** modify `cmd/wsitools/convert_dicom_test.go`.

- [ ] **Step 1: Add the test** — append to `cmd/wsitools/convert_dicom_test.go`. It mirrors `TestSVSToDICOMPixelRoundTrip` but on the JP2K fixture and emitting level 0 to a temp file. Confirm the file already imports `bytes`, `os`, `path/filepath`, `testing`, `dicomwriter`, `source`, `opentile "github.com/wsilabs/opentile-go"`, `"github.com/wsilabs/opentile-go/decoder"` (the slice-1 pixel test added them; reuse).

```go
func TestJP2KToDICOMPixelRoundTrip(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	svs := filepath.Join(dir, "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skip("no JP2K SVS fixture")
	}

	src, err := source.Open(svs)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "jp2k.dcm")
	f, err := os.Create(out)
	if err != nil {
		src.Close()
		t.Fatal(err)
	}
	if err := dicomwriter.WriteVolumeInstance(f, src, 0, dicomwriter.Options{}); err != nil {
		f.Close()
		src.Close()
		t.Fatalf("WriteVolumeInstance: %v", err)
	}
	f.Close()
	src.Close()

	back, err := source.Open(out)
	if err != nil {
		t.Fatalf("source.Open(emitted dicom): %v", err)
	}
	if back.Format() != "dicom" {
		t.Errorf("emitted Format = %q, want dicom", back.Format())
	}
	back.Close()

	const w, h = 256, 256 // JP2K-33003-1 L0 tile size
	decodeRGB := func(path string) *decoder.Image {
		s, err := opentile.OpenFile(path)
		if err != nil {
			t.Fatalf("opentile.OpenFile(%s): %v", path, err)
		}
		defer s.Close()
		img, err := s.ImageReadRegion(0, 0, 0, 0, w, h, opentile.WithFormat(decoder.PixelFormatRGB))
		if err != nil {
			t.Fatalf("ImageReadRegion(%s): %v", path, err)
		}
		return img
	}
	srcImg := decodeRGB(svs)
	dcmImg := decodeRGB(out)
	if srcImg.Width != dcmImg.Width || srcImg.Height != dcmImg.Height {
		t.Fatalf("dim mismatch: src=%dx%d dcm=%dx%d", srcImg.Width, srcImg.Height, dcmImg.Width, dcmImg.Height)
	}
	if !bytes.Equal(srcImg.Pix, dcmImg.Pix) {
		n := len(srcImg.Pix)
		if len(dcmImg.Pix) < n {
			n = len(dcmImg.Pix)
		}
		for i := 0; i < n; i++ {
			if srcImg.Pix[i] != dcmImg.Pix[i] {
				t.Fatalf("pixel mismatch at byte %d: src=%d dcm=%d (photometric/transfer-syntax likely wrong)", i, srcImg.Pix[i], dcmImg.Pix[i])
			}
		}
		t.Fatalf("pixel buffers differ in length: src=%d dcm=%d", len(srcImg.Pix), len(dcmImg.Pix))
	}
}
```

- [ ] **Step 2: Run, verify PASS**

```bash
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run TestJP2KToDICOMPixelRoundTrip -v 2>&1 | grep -v "duplicate lib"
```
Expected: PASS — verbatim J2K codestream copy + correct RGB photometric/`.91` transfer syntax ⇒ byte-identical decoded RGB. **If it FAILS on a pixel mismatch**, do NOT weaken the assertion: dump the emitted `PhotometricInterpretation`/`TransferSyntaxUID` (`dcmdump`), confirm `RGB` + `…4.91`, and report. If opentile cannot decode the emitted JP2K-DICOM at all (e.g. a `nojp2k` build), note it — the default build has openjpeg.

- [ ] **Step 3: Commit**
```bash
git add cmd/wsitools/convert_dicom_test.go
git commit -m "test(convert): JP2K SVS->DICOM pixel round-trip (RGB path)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: dciodvfy on the JP2K pyramid (the de-risk)

**Files:** modify `Makefile`. The controller runs the validator (`/tmp/dciodvfy`).

- [ ] **Step 1: Extend `make dicom-validate`** — add a JP2K block mirroring the existing SVS block. Insert inside the recipe, after the SVS single-instance block and before the final `exit $$RC`:

```make
	JP2K="$$WSI_TOOLS_TESTDIR/svs/JP2K-33003-1.svs"; \
	if [ -f "$$JP2K" ]; then \
		DIR2=$$(mktemp -d -t wsm-jp2k.XXXXXX); \
		./bin/wsitools convert --to dicom -f -o "$$DIR2/pyr" "$$JP2K"; \
		for L in "$$DIR2"/pyr/level-*.dcm; do \
			echo "=== dciodvfy (JP2K pyramid) $$L ==="; \
			"$(DCIODVFY)" "$$L" || RC=$$?; \
		done; \
		rm -rf "$$DIR2"; \
	else echo "missing $$JP2K; skipping JP2K pyramid"; fi; \
```

- [ ] **Step 2: Run (CONTROLLER step — needs dciodvfy)**

```bash
go build -o bin/wsitools ./cmd/wsitools 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" make dicom-validate DCIODVFY=/tmp/dciodvfy 2>&1 | grep -vE "duplicate lib|level=INFO" | grep -E "=== dciodvfy|^Error"
```
Expected: **0 Errors** across the Grundium pyramid, the SVS instance, AND every JP2K `level-<n>.dcm`. (Benign per-instance "Study ID" DICOMDIR warning is acceptable.) Count `^Error` → must be 0.

- [ ] **Step 3: If any JP2K level errors, fix and re-run**

dciodvfy validates the JP2K transfer-syntax + photometric + (omitted) lossy tags. Likely gaps and fixes (iterate in `dataset.go`/`buildDescriptor`):
- transfer-syntax / photometric / SamplesPerPixel inconsistency → recheck the `.91`/RGB/3 mapping.
- a complaint about LossyImageCompression for JP2K → confirm `01` + `ISO_15444_1` for the lossy fixture.
Re-run to 0 errors. **If a JP2K instance is structurally non-conformant in a way the slice can't resolve, STOP and report.**

- [ ] **Step 4: Commit**
```bash
git add Makefile
git commit -m "feat(dicomwriter): validate JP2K pyramid in make dicom-validate (dciodvfy 0 errors)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Docs

**Files:** `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md`.

- [ ] **Step 1: Update docs**

- **CHANGELOG.md** `## [Unreleased]`: augment the `convert --to dicom` entry — now also accepts **JPEG 2000** sources (tile-copy raw codestream; reversibility-driven `…4.90` lossless / `…4.91` lossy transfer syntax; codestream-derived photometric RGB/YBR_ICT/YBR_RCT/MONOCHROME2). dciodvfy 0 errors on the JP2K-33003-1 pyramid + RGB pixel round-trip. YBR (MCT=1) branches unit-tested, not yet e2e-validated.
- **README.md**: update the `convert --to dicom` bullet + footnote ⁶ — JPEG-baseline **or JPEG 2000** sources accepted; keep accurate (RGB JP2K e2e-validated).
- **docs/roadmap.md**: add a `✅ DONE (2026-06-11): Phase 1 slice 3 — JPEG 2000` sub-bullet; update "Next" to label/overview/thumbnail as separate instances (P2), then HTJ2K / 16-bit.
- **docs/notes/2026-06-03-dicom-writer-scoping.md**: add a `## Phase 1 — slice 3 outcome (2026-06-11)` section: JP2K tile-copy shipped; SIZ/COD codestream parse → photometric; reversibility-driven transfer syntax + conditional lossless tag omission; dciodvfy 0 errors on JP2K-33003-1 (RGB/lossy/`.91`); YBR + lossless paths unit-tested only (no fixture). Remaining: HTJ2K, 16-bit, associated-image instances.

- [ ] **Step 2: Verify + commit**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
git add README.md CHANGELOG.md docs/roadmap.md docs/notes/2026-06-03-dicom-writer-scoping.md
git commit -m "docs: DICOM-WSI writer Phase 1 slice 3 (JPEG 2000 -> DICOM)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final review

Dispatch a final reviewer (focus: the JP2K photometric/transfer-syntax/lossy mapping is correct per the codestream; lossless tag-omission is conformant; the JPEG + DICOM paths reproduce prior output; the RGB JP2K path is genuinely pixel-verified; dciodvfy 0 errors per JP2K level; no scope creep). Then use `superpowers:finishing-a-development-branch`.

## Self-review notes (author)

- **Spec coverage:** jp2kmeta parser (T1), ImageDescriptor+assembler conditional lossy tags + buildDescriptor JP2K branch (T2), RGB pixel round-trip (T3), dciodvfy per-level de-risk (T4), docs (T5). Reversibility-driven `.90`/`.91` (T2 buildDescriptor); full photometric map (T1); lossless tag omission (T2 step 3c + test step 6).
- **Type consistency:** `JP2KInfo{Components,Precision,MCT,Reversible}`, `InspectJP2K`/`PhotometricJP2K`, `ImageDescriptor{TransferSyntax,Photometric,SamplesPerPixel,ImageType,ICCProfile,Lossy,LossyMethod,LossyRatio}`, consts `jp2kLosslessTS`/`jp2kTS` used consistently. `buildDescriptor(src,level,lossyRatio)` signature unchanged.
- **Regression safety:** JPEG path sets `.50`/Samples=Components(3 for the color fixtures)/`01`/`ISO_10918_1`; DICOM path sets the same as P0 — both reproduce prior emitted values, so existing tests stay green.
- **Scope:** JP2K tile-copy only; >8-bit, `.jp2`-boxed, HTJ2K, associated-image instances explicitly deferred. T4 carries the STOP-and-report signal.
