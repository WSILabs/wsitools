# DICOM-WSI writer — Phase 1 first slice (SVS→DICOM, single level) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `convert --to dicom` to emit ONE conformant, **pixel-correct** WSM VOLUME instance for one level of a **non-DICOM** source (SVS etc.) whose level tiles are JPEG-baseline — verbatim tile-copy, with `PhotometricInterpretation` + ICC derived to match the actual bytes.

**Architecture:** A new pure-Go `jpegmeta` helper inspects a level's first JPEG tile (markers) to derive the colorspace; `WriteVolumeInstance` builds an `ImageDescriptor{Photometric, ImageType, ICCProfile, LossyRatio}` (probing for non-DICOM sources, preserving P0's values for DICOM sources) and threads it into `assembleWSMDataset`, which stops hardcoding those fields. ICC is carried from the source or synthesized as sRGB. Verbatim frame encapsulation is unchanged from P0.

**Tech Stack:** Go, `github.com/suyashkumar/dicom` v1.1.0, opentile-go (`opentile.Open` + `decoder` for the pixel round-trip), `dciodvfy` (dicom3tools) for conformance.

**Spec:** `docs/superpowers/specs/2026-06-11-dicom-writer-phase1-svs-design.md`

**Branch:** create `feat/dicom-writer-phase1-svs` off `main`. Never implement on `main`.

**Verified facts (probed this session — do not re-derive):**
- `CMU-1-Small-Region.svs` (the CI fixture) L0 tile (0,0): SOI, then **SOF0 baseline** (precision 8, 240×240, 3 comps, all sampling `0x11` → **not subsampled / 4:4:4**), then **APP14 "Adobe"** payload `41 64 6F 62 65 00 64 80 00 00 00 00` (ColorTransform byte = `0x00` → **RGB**), then SOS. **Marker order is SOF-before-APP14** — the parser MUST scan to SOS and not return on SOF. CMU has **no embedded ICC** (`md.ICCProfile` is empty) → exercises the sRGB-synthesis path. L0 `Compression()` = `source.CompressionJPEG`, tile 240×240.
- P0 current signatures (in `internal/dicomwriter/`):
  - `func assembleWSMDataset(src source.Source, level int, uids UIDSet, lossyRatio float64) (dicom.Dataset, error)` — emits the WSM IOD; hardcodes `PhotometricInterpretation "YBR_FULL_422"`, `ImageType {DERIVED,PRIMARY,VOLUME,NONE}`, `FrameType {DERIVED,PRIMARY,VOLUME,NONE}`, and `LossyImageCompressionRatio` from `lossyRatio`; ICC carried from `md.ICCProfile` only when non-empty.
  - `func encapsulatePixelData(src source.Source, level int) (*dicom.Element, int64, error)` — verbatim frames + compressed byte total. **Unchanged by this slice.**
  - `func WriteVolumeInstance(w io.Writer, src source.Source, level int, _ Options) error` — guards `src.Format() != "dicom"`, encapsulates-first, computes `lossyRatio`, assembles, appends PixelData, writes.
  - `source.Level`: `Compression() source.Compression`, `TileSize() image.Point`, `Grid() image.Point`, `TileMaxSize() int`, `TileInto(x,y int, dst []byte) (int, error)`. `source.CompressionJPEG` is the JPEG enum value; `Compression` has a `String()`.
  - `source.Metadata`: `ICCProfile []byte`, `SerialNumber string`, `AcquisitionDateTime time.Time`, `MPPX/MPPY float64`, `Make/Model string`.
- Decode-to-pixels (for the pixel round-trip): `opentile.OpenFile(path) (*opentile.Slide, error)` (NOT `opentile.Open`, which takes a `ReaderAt`+size); `slide.ImageReadRegion(image, level, x, y, w, h int, opts ...opentile.DecodeOption) (*decoder.Image, error)` with `opentile.WithFormat(decoder.PixelFormatRGB)`; `decoder.Image{Pix []byte, Stride, Width, Height int, Format}`. Import paths: `github.com/wsilabs/opentile-go` (pkg `opentile`) and `github.com/wsilabs/opentile-go/decoder`.

---

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `internal/dicomwriter/jpegmeta.go` | new | JPEG marker inspection → `JPEGInfo` + `Photometric()` |
| `internal/dicomwriter/jpegmeta_test.go` | new | parser + photometric-map unit tests |
| `internal/dicomwriter/srgb.icc` | new | checked-in canonical sRGB ICC profile (embedded) |
| `internal/dicomwriter/srgb.go` | new | `//go:embed srgb.icc` → `srgbICCProfile []byte` |
| `internal/dicomwriter/srgb_test.go` | new | sanity test (non-empty, `acsp` signature) |
| `internal/dicomwriter/dataset.go` | modify | `ImageDescriptor` param replaces hardcoded photometric/ImageType/ICC/ratio |
| `internal/dicomwriter/dataset_test.go` | modify | update call site to new signature |
| `internal/dicomwriter/dicomwriter.go` | modify | `buildDescriptor` (codec gate + probe + derive); drop DICOM-only guard |
| `cmd/wsitools/convert_dicom.go` | modify | refresh doc comment (no longer DICOM-only) |
| `cmd/wsitools/convert_dicom_test.go` | modify | SVS→DICOM pixel round-trip + ICC + readback |
| `Makefile` | modify | `dicom-validate` also emits + validates the SVS fixture |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md` | modify | document the slice |

---

## Task 1: `jpegmeta` — colorspace inspection

**Files:** create `internal/dicomwriter/jpegmeta.go`, `internal/dicomwriter/jpegmeta_test.go`.

- [ ] **Step 1: Write failing tests** (`jpegmeta_test.go`)

```go
package dicomwriter

import "testing"

// jpeg builds a minimal marker stream: SOI, the given segments, then SOS+EOI.
// Each segment is {marker, payload}; length bytes are computed.
func jpegStream(segs ...[]byte) []byte {
	out := []byte{0xFF, 0xD8} // SOI
	for _, s := range segs {
		out = append(out, s...)
	}
	out = append(out, 0xFF, 0xDA, 0x00, 0x02) // SOS (len 2, no body)
	out = append(out, 0xFF, 0xD9)             // EOI
	return out
}

// sof0 builds an SOF0 segment (FF C0) with the given precision, 1 component
// block per (h,v) sampling pair.
func sof0(prec byte, comps [][2]byte) []byte {
	body := []byte{prec, 0x00, 0x10, 0x00, 0x10, byte(len(comps))} // prec, h=16,w=16, ncomp
	for i, c := range comps {
		body = append(body, byte(i+1), c[0]<<4|c[1], 0x00) // id, sampling, qtable
	}
	seg := []byte{0xFF, 0xC0, 0x00, byte(2 + len(body))}
	return append(seg, body...)
}

// app14 builds an APP14 Adobe segment with the given transform byte.
func app14(transform byte) []byte {
	body := []byte("Adobe")
	body = append(body, 0x00, 0x64, 0x80, 0x00, 0x00, 0x00, transform) // ver,flags0,flags1,transform
	seg := []byte{0xFF, 0xEE, 0x00, byte(2 + len(body))}
	return append(seg, body...)
}

func TestInspectAPP14RGB(t *testing.T) {
	// Aperio order: SOF before APP14, transform=0 → RGB, 3 comps 1x1 (not subsampled).
	j := jpegStream(sof0(8, [][2]byte{{1, 1}, {1, 1}, {1, 1}}), app14(0))
	info, err := Inspect(j)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Color != ColorRGB {
		t.Errorf("Color = %v, want ColorRGB", info.Color)
	}
	if info.Components != 3 || info.Precision != 8 || info.Subsampled {
		t.Errorf("got comps=%d prec=%d sub=%v, want 3/8/false", info.Components, info.Precision, info.Subsampled)
	}
	if p, _ := Photometric(info); p != "RGB" {
		t.Errorf("Photometric = %q, want RGB", p)
	}
}

func TestInspectYCbCrSubsampled(t *testing.T) {
	// No APP14, luma 2x2 + chroma 1x1 → subsampled YCbCr → YBR_FULL_422.
	j := jpegStream(sof0(8, [][2]byte{{2, 2}, {1, 1}, {1, 1}}))
	info, err := Inspect(j)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Color != ColorYCbCr || !info.Subsampled {
		t.Errorf("got Color=%v sub=%v, want YCbCr/true", info.Color, info.Subsampled)
	}
	if p, _ := Photometric(info); p != "YBR_FULL_422" {
		t.Errorf("Photometric = %q, want YBR_FULL_422", p)
	}
}

func TestInspectYCbCr444(t *testing.T) {
	// No APP14, all 1x1 → JFIF default YCbCr, not subsampled → YBR_FULL.
	j := jpegStream(sof0(8, [][2]byte{{1, 1}, {1, 1}, {1, 1}}))
	info, _ := Inspect(j)
	if p, _ := Photometric(info); p != "YBR_FULL" {
		t.Errorf("Photometric = %q, want YBR_FULL", p)
	}
}

func TestInspectMonochrome(t *testing.T) {
	j := jpegStream(sof0(8, [][2]byte{{1, 1}}))
	info, _ := Inspect(j)
	if p, _ := Photometric(info); p != "MONOCHROME2" {
		t.Errorf("Photometric = %q, want MONOCHROME2", p)
	}
}

func TestInspectErrors(t *testing.T) {
	if _, err := Inspect([]byte{0x00, 0x01}); err == nil {
		t.Error("want error for non-JPEG (no SOI)")
	}
	if _, err := Inspect([]byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x02}); err == nil {
		t.Error("want error when no SOF before SOS")
	}
	if _, err := Photometric(JPEGInfo{Precision: 12, Components: 3}); err == nil {
		t.Error("want error for precision != 8")
	}
}
```

- [ ] **Step 2: Run, verify FAIL**

Run: `go test ./internal/dicomwriter/ -run 'Inspect' -v`
Expected: FAIL (undefined: `Inspect`, `JPEGInfo`, `ColorRGB`, …).

- [ ] **Step 3: Implement** (`jpegmeta.go`)

```go
package dicomwriter

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// JPEGColor is the encoded colorspace of a JPEG stream's pixel samples.
type JPEGColor int

const (
	ColorYCbCr JPEGColor = iota // default per JFIF when no Adobe APP14 marker
	ColorRGB                    // Adobe APP14 ColorTransform = 0 (e.g. classic Aperio SVS)
)

// JPEGInfo is what Inspect extracts from a JPEG stream's header.
type JPEGInfo struct {
	Color      JPEGColor
	Components int
	Precision  int
	Subsampled bool
}

// Inspect parses a JPEG stream's markers far enough to determine its colorspace
// and chroma subsampling. It scans the whole header up to SOS — NOT stopping at
// SOF — because Aperio SVS tiles place the Adobe APP14 marker AFTER SOF0, and the
// APP14 ColorTransform flag is what distinguishes raw-RGB tiles from YCbCr ones.
func Inspect(j []byte) (JPEGInfo, error) {
	if len(j) < 2 || j[0] != 0xFF || j[1] != 0xD8 {
		return JPEGInfo{}, errors.New("jpegmeta: not a JPEG (missing SOI)")
	}
	var (
		info       JPEGInfo
		sawSOF     bool
		sawAPP14   bool
		app14Color JPEGColor
	)
	i := 2
	for i+1 < len(j) {
		if j[i] != 0xFF {
			i++
			continue
		}
		m := j[i+1]
		if m == 0xFF { // fill byte
			i++
			continue
		}
		// Standalone markers (no length): TEM, RSTn.
		if m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			i += 2
			continue
		}
		if m == 0xD9 || m == 0xDA { // EOI or SOS → header done
			break
		}
		if i+4 > len(j) {
			return JPEGInfo{}, errors.New("jpegmeta: truncated before segment length")
		}
		segLen := int(binary.BigEndian.Uint16(j[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(j) {
			return JPEGInfo{}, fmt.Errorf("jpegmeta: bad segment length %d", segLen)
		}
		payload := j[i+4 : i+2+segLen]
		switch {
		case m == 0xEE: // APP14 Adobe
			if len(payload) >= 12 && string(payload[:5]) == "Adobe" {
				sawAPP14 = true
				if payload[11] == 0 {
					app14Color = ColorRGB
				} else {
					app14Color = ColorYCbCr // 1 = YCbCr, 2 = YCCK
				}
			}
		case m == 0xC0 || m == 0xC1: // SOF0 baseline / SOF1 extended sequential
			if len(payload) < 6 {
				return JPEGInfo{}, errors.New("jpegmeta: short SOF")
			}
			info.Precision = int(payload[0])
			nc := int(payload[5])
			info.Components = nc
			maxH, maxV := 0, 0
			hv := make([][2]int, 0, nc)
			for c := 0; c < nc; c++ {
				off := 6 + c*3
				if off+1 >= len(payload) {
					return JPEGInfo{}, errors.New("jpegmeta: short SOF component table")
				}
				h := int(payload[off+1] >> 4)
				v := int(payload[off+1] & 0x0F)
				hv = append(hv, [2]int{h, v})
				if h > maxH {
					maxH = h
				}
				if v > maxV {
					maxV = v
				}
			}
			for _, s := range hv {
				if s[0] < maxH || s[1] < maxV {
					info.Subsampled = true
				}
			}
			sawSOF = true
		case m >= 0xC2 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC:
			// SOF2/3/5/6/7/9/.. = progressive/lossless/arithmetic: not baseline.
			return JPEGInfo{}, fmt.Errorf("jpegmeta: non-baseline SOF marker 0xFF%02X", m)
		}
		i += 2 + segLen
	}
	if !sawSOF {
		return JPEGInfo{}, errors.New("jpegmeta: no SOF marker found")
	}
	if sawAPP14 {
		info.Color = app14Color
	} else {
		info.Color = ColorYCbCr // JFIF convention
	}
	return info, nil
}

// Photometric maps a JPEGInfo to the DICOM PhotometricInterpretation value for a
// verbatim tile-copy of that stream.
func Photometric(info JPEGInfo) (string, error) {
	if info.Precision != 8 {
		return "", fmt.Errorf("jpegmeta: unsupported precision %d (want 8)", info.Precision)
	}
	switch info.Components {
	case 1:
		return "MONOCHROME2", nil
	case 3:
		if info.Color == ColorRGB {
			return "RGB", nil
		}
		if info.Subsampled {
			return "YBR_FULL_422", nil
		}
		return "YBR_FULL", nil
	default:
		return "", fmt.Errorf("jpegmeta: unsupported component count %d (want 1 or 3)", info.Components)
	}
}
```

- [ ] **Step 4: Run, verify PASS**

Run: `go test ./internal/dicomwriter/ -run 'Inspect' -v` → all PASS. Then `gofmt -l internal/dicomwriter/jpegmeta.go` (empty) and `go vet ./internal/dicomwriter/`.

- [ ] **Step 5: Commit**

```bash
git add internal/dicomwriter/jpegmeta.go internal/dicomwriter/jpegmeta_test.go
git commit -m "feat(dicomwriter): JPEG marker inspection for colorspace/photometric

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Embedded sRGB ICC profile

**Files:** create `internal/dicomwriter/srgb.icc`, `internal/dicomwriter/srgb.go`, `internal/dicomwriter/srgb_test.go`.

- [ ] **Step 1: Generate the profile bytes** (checked-in binary asset)

Generate a valid, freely-redistributable sRGB v2 ICC profile with Little-CMS via Python/Pillow (the produced bytes are an lcms-built profile, not a vendor profile):

```bash
python3 - <<'PY'
from PIL import ImageCms
prof = ImageCms.createProfile("sRGB")
data = ImageCms.ImageCmsProfile(prof).tobytes()
open("internal/dicomwriter/srgb.icc", "wb").write(data)
print("wrote", len(data), "bytes; signature:", data[36:40])
PY
```
Expected: prints a byte count (typically ~400–600) and `signature: b'acsp'`. If Pillow is unavailable, install it (`pip3 install pillow`) or obtain any standards-published sRGB IEC61966-2.1 profile and place its bytes at that path; the only hard requirements are that it is a valid ICC (`acsp` at offset 36) and that `dciodvfy` accepts it in Task 6.

- [ ] **Step 2: Write failing test** (`srgb_test.go`)

```go
package dicomwriter

import "testing"

func TestSRGBProfileValid(t *testing.T) {
	if len(srgbICCProfile) < 128 {
		t.Fatalf("srgbICCProfile too small: %d bytes", len(srgbICCProfile))
	}
	// ICC profiles carry the 'acsp' signature at byte offset 36.
	if got := string(srgbICCProfile[36:40]); got != "acsp" {
		t.Errorf("ICC signature = %q, want \"acsp\"", got)
	}
}
```

- [ ] **Step 3: Run, verify FAIL**

Run: `go test ./internal/dicomwriter/ -run TestSRGBProfileValid` → FAIL (undefined `srgbICCProfile`).

- [ ] **Step 4: Implement** (`srgb.go`)

```go
package dicomwriter

import _ "embed"

// srgbICCProfile is a canonical sRGB (IEC 61966-2.1) ICC profile, embedded so a
// WSM instance can satisfy the Type 1C ICCProfile requirement when the source
// carries no embedded profile (e.g. many SVS files). Built with Little-CMS
// (via the build step in the Phase 1 plan) — a generated, freely-redistributable
// profile, not a vendor asset.
//
//go:embed srgb.icc
var srgbICCProfile []byte
```

- [ ] **Step 5: Run, verify PASS + commit**

Run: `go test ./internal/dicomwriter/ -run TestSRGBProfileValid` → PASS.
```bash
git add internal/dicomwriter/srgb.icc internal/dicomwriter/srgb.go internal/dicomwriter/srgb_test.go
git commit -m "feat(dicomwriter): embed sRGB ICC profile for ICC-less sources

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `ImageDescriptor` — parameterize the dataset

**Files:** modify `internal/dicomwriter/dataset.go`, `internal/dicomwriter/dataset_test.go`.

- [ ] **Step 1: Add `ImageDescriptor` + change `assembleWSMDataset` signature** (`dataset.go`)

Add this type above `assembleWSMDataset`:

```go
// ImageDescriptor carries the codec/colorspace-dependent attributes that vary by
// source. The caller (WriteVolumeInstance) derives these — probing a non-DICOM
// source's JPEG, or using P0's fixed values for a DICOM source — so the assembler
// stays a pure dataset builder.
type ImageDescriptor struct {
	Photometric string   // PhotometricInterpretation: RGB | YBR_FULL_422 | YBR_FULL | MONOCHROME2
	ImageType   []string // ImageType + FrameType value (4 elements)
	ICCProfile  []byte   // carried or synthesized; non-empty for color
	LossyRatio  float64  // LossyImageCompressionRatio
}
```

Change the signature from:
```go
func assembleWSMDataset(src source.Source, level int, uids UIDSet, lossyRatio float64) (dicom.Dataset, error) {
```
to:
```go
func assembleWSMDataset(src source.Source, level int, uids UIDSet, desc ImageDescriptor) (dicom.Dataset, error) {
```

- [ ] **Step 2: Use the descriptor fields** (`dataset.go`)

Replace the `ratioStr` derivation. Find:
```go
	// LossyImageCompressionRatio (Type 1C, DS, ≤16 chars) — %.4g keeps it compact.
	ratioStr := fmt.Sprintf("%.4g", lossyRatio)
```
with:
```go
	// LossyImageCompressionRatio (Type 1C, DS, ≤16 chars) — %.4g keeps it compact.
	ratioStr := fmt.Sprintf("%.4g", desc.LossyRatio)
```

Replace the ICC source in the optical-path item. Find:
```go
	if len(md.ICCProfile) > 0 {
		opticalPathItem = append(opticalPathItem, mk(tag.ICCProfile, md.ICCProfile))
	}
```
with:
```go
	if len(desc.ICCProfile) > 0 {
		opticalPathItem = append(opticalPathItem, mk(tag.ICCProfile, desc.ICCProfile))
	}
```

Replace the `ImageType` element. Find:
```go
		// ImageType: VOLUME image; RESAMPLED for derived pyramid levels.
		mk(tag.ImageType, []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"}),
```
with:
```go
		mk(tag.ImageType, desc.ImageType),
```

Replace the `PhotometricInterpretation` element. Find:
```go
		mk(tag.PhotometricInterpretation, []string{"YBR_FULL_422"}),
```
with:
```go
		mk(tag.PhotometricInterpretation, []string{desc.Photometric}),
```

Replace the `FrameType` element. Find:
```go
				{mk(tag.FrameType, []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"})},
```
with:
```go
				{mk(tag.FrameType, desc.ImageType)},
```

(`md := src.Metadata()` and all other uses of `md` — dates, serial, MPP, make/model — stay.)

- [ ] **Step 3: Update the test call site** (`dataset_test.go`)

Find:
```go
	ds, err := assembleWSMDataset(src, level, uids, 10.0)
```
Replace with:
```go
	ds, err := assembleWSMDataset(src, level, uids, ImageDescriptor{
		Photometric: "YBR_FULL_422",
		ImageType:   []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		ICCProfile:  src.Metadata().ICCProfile, // Grundium fixture carries an ICC profile
		LossyRatio:  10.0,
	})
```

The existing assertions (LossyImageCompressionRatio == "10", ICC carried into OpticalPathSequence when the source has one) still hold: the Grundium fixture has an ICC profile, so `src.Metadata().ICCProfile` is non-empty.

- [ ] **Step 4: Build (expect a compile error at the WriteVolumeInstance caller)**

Run: `go build ./internal/dicomwriter/ 2>&1 | grep -v "duplicate lib"`
Expected: FAIL — `dicomwriter.go` still calls `assembleWSMDataset(src, level, uids, lossyRatio)` (old signature). That caller is fixed in Task 4. To verify THIS task in isolation, run just the dataset test compile via the next step after Task 4, OR temporarily confirm only `dataset.go`/`dataset_test.go` type-check by reading them. (Do not commit a broken build — combine the commit with Task 4, OR stub the caller now. Simplest: proceed to Task 4 before committing; this task's commit is folded into Task 4's.)

> NOTE: Tasks 3 and 4 are committed together (Task 4 step 5), because the signature change spans both files. Implement Task 3, then Task 4, then build/test/commit once.

---

## Task 4: `buildDescriptor` + drop the DICOM-only guard

**Files:** modify `internal/dicomwriter/dicomwriter.go`, `cmd/wsitools/convert_dicom.go`.

- [ ] **Step 1: Rewrite `WriteVolumeInstance` + add `buildDescriptor`** (`dicomwriter.go`)

Replace the entire body of `WriteVolumeInstance` and add `buildDescriptor`. The file becomes:

```go
package dicomwriter

import (
	"fmt"
	"io"

	"github.com/suyashkumar/dicom"

	"github.com/wsilabs/wsitools/internal/source"
)

// Options is reserved for future write-side knobs (P0/P1: empty).
type Options struct{}

// WriteVolumeInstance emits ONE conformant DICOM WSM VOLUME instance for src
// level `level` to w, copying the source's compressed JPEG tiles verbatim.
// The source's selected level must carry JPEG-baseline tiles (DICOM sources
// always do; non-DICOM sources are codec-gated in buildDescriptor).
func WriteVolumeInstance(w io.Writer, src source.Source, level int, _ Options) error {
	if level < 0 || level >= len(src.Levels()) {
		return fmt.Errorf("level %d out of range (0..%d)", level, len(src.Levels())-1)
	}
	// Encapsulate first: the compressed byte total feeds the lossy compression
	// ratio (LossyImageCompressionRatio is Type 1C, required when
	// LossyImageCompression is "01").
	pd, compressedBytes, err := encapsulatePixelData(src, level)
	if err != nil {
		return err
	}
	lvl := src.Levels()[level]
	tileSize := lvl.TileSize()
	grid := lvl.Grid()
	uncompressed := int64(grid.X) * int64(grid.Y) * int64(tileSize.X) * int64(tileSize.Y) * 3
	lossyRatio := 1.0
	if compressedBytes > 0 {
		lossyRatio = float64(uncompressed) / float64(compressedBytes)
	}

	desc, err := buildDescriptor(src, level, lossyRatio)
	if err != nil {
		return err
	}

	uids := UIDSet{
		SOP:              NewUID(),
		Study:            NewUID(),
		Series:           NewUID(),
		FrameOfReference: NewUID(),
		DimensionOrg:     NewUID(),
	}
	ds, err := assembleWSMDataset(src, level, uids, desc)
	if err != nil {
		return err
	}
	ds.Elements = append(ds.Elements, pd) // PixelData last
	return dicom.Write(w, ds)
}

// buildDescriptor derives the codec/colorspace-dependent attributes for src
// level `level`. DICOM sources reuse P0's fixed values (preserving byte-identical
// output); non-DICOM sources are gated to JPEG-baseline tiles and their
// PhotometricInterpretation is derived by inspecting the first tile's markers.
// ICC is carried from the source, or synthesized as sRGB when absent.
func buildDescriptor(src source.Source, level int, lossyRatio float64) (ImageDescriptor, error) {
	md := src.Metadata()
	icc := md.ICCProfile
	if len(icc) == 0 {
		icc = srgbICCProfile
	}

	if src.Format() == "dicom" {
		// P0 path: Grundium-mirrored values, unchanged.
		return ImageDescriptor{
			Photometric: "YBR_FULL_422",
			ImageType:   []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
			ICCProfile:  icc,
			LossyRatio:  lossyRatio,
		}, nil
	}

	lvl := src.Levels()[level]
	if lvl.Compression() != source.CompressionJPEG {
		return ImageDescriptor{}, fmt.Errorf(
			"--to dicom: level %d is %s; Phase 1 supports JPEG-baseline tile-copy only",
			level, lvl.Compression())
	}
	buf := make([]byte, lvl.TileMaxSize())
	n, err := lvl.TileInto(0, 0, buf)
	if err != nil {
		return ImageDescriptor{}, fmt.Errorf("read tile (0,0) for colorspace probe: %w", err)
	}
	info, err := Inspect(buf[:n])
	if err != nil {
		return ImageDescriptor{}, fmt.Errorf("inspect source JPEG: %w", err)
	}
	photo, err := Photometric(info)
	if err != nil {
		return ImageDescriptor{}, err
	}
	// Level 0 of a non-DICOM slide is the native acquisition (ORIGINAL); reduced
	// levels are downsampled (DERIVED / RESAMPLED).
	imageType := []string{"ORIGINAL", "PRIMARY", "VOLUME", "NONE"}
	if level > 0 {
		imageType = []string{"DERIVED", "PRIMARY", "VOLUME", "RESAMPLED"}
	}
	return ImageDescriptor{
		Photometric: photo,
		ImageType:   imageType,
		ICCProfile:  icc,
		LossyRatio:  lossyRatio,
	}, nil
}
```

- [ ] **Step 2: Refresh the doc comment** (`cmd/wsitools/convert_dicom.go`)

Find the `runConvertDICOM` doc comment that says it copies frames "verbatim (P0)" / mentions a DICOM source requirement, e.g.:
```go
// level of a DICOM source, copying compressed JPEG frames verbatim (P0).
```
Replace with:
```go
// level of a DICOM or non-DICOM source whose tiles are JPEG-baseline, copying
// the compressed JPEG tiles verbatim (Phase 1).
```
(If the exact comment text differs, update it to reflect that non-DICOM JPEG sources are now accepted. No behavioral code change in this file — the codec gate lives in buildDescriptor.)

- [ ] **Step 3: Build + run existing tests (regression: P0 output unchanged)**

```bash
go build ./... 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ ./cmd/wsitools/ -run 'DICOM|Dicom|WSM|Assemble|Encapsulat|NewUID|ConvertDICOM|Inspect|SRGB' -count=1 2>&1 | grep -v "duplicate lib"
```
Expected: PASS. The DICOM-source tests still pass (descriptor carries P0's YBR_FULL_422 / DERIVED…NONE / carried ICC), and the new jpegmeta/srgb tests pass. `gofmt -l internal/dicomwriter/ cmd/wsitools/convert_dicom.go` empty; `go vet ./internal/dicomwriter/ ./cmd/wsitools/`.

- [ ] **Step 4: Commit (Tasks 3 + 4 together)**

```bash
git add internal/dicomwriter/dataset.go internal/dicomwriter/dataset_test.go internal/dicomwriter/dicomwriter.go cmd/wsitools/convert_dicom.go
git commit -m "feat(dicomwriter): derive photometric/ImageType/ICC per source (ImageDescriptor)

Non-DICOM JPEG sources are now accepted: codec-gated to JPEG-baseline,
PhotometricInterpretation derived by inspecting the first tile's markers,
ICC carried-or-synthesized. DICOM sources keep P0's values (byte-identical).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: SVS→DICOM pixel round-trip + readback test

**Files:** modify `cmd/wsitools/convert_dicom_test.go`.

This is the colorspace-correctness safety net: emit SVS→DICOM, then decode tile-region (0,0) from BOTH the source SVS and the emitted DICOM via opentile and assert the RGB pixels match. dciodvfy (Task 6) cannot see a colorspace swap; this can.

- [ ] **Step 1: Add the test** (`cmd/wsitools/convert_dicom_test.go`)

Append this test. It is gated on the CMU SVS fixture (skips if absent). Confirm the existing file's imports include `os`, `path/filepath`, `testing`, `bytes`, `github.com/wsilabs/wsitools/internal/dicomwriter`, `github.com/wsilabs/wsitools/internal/source`; add `github.com/wsilabs/opentile-go` (as `opentile`) and `github.com/wsilabs/opentile-go/decoder`.

```go
func TestSVSToDICOMPixelRoundTrip(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	svsPath := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svsPath); err != nil {
		t.Skip("no CMU SVS fixture")
	}

	// Emit SVS level 0 → DICOM.
	src, err := source.Open(svsPath)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "svs.dcm")
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

	// Read back as DICOM (regression: it opens + reports as a DICOM slide).
	back, err := source.Open(out)
	if err != nil {
		t.Fatalf("source.Open(emitted dicom): %v", err)
	}
	if back.Format() != "dicom" {
		t.Errorf("emitted Format = %q, want dicom", back.Format())
	}
	back.Close()

	// Pixel round-trip: decode region (0,0,w,h) from both files and compare.
	const w, h = 240, 240 // CMU L0 tile size; a single-tile region exercises frame 0
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
	srcImg := decodeRGB(svsPath)
	dcmImg := decodeRGB(out)

	if srcImg.Width != dcmImg.Width || srcImg.Height != dcmImg.Height {
		t.Fatalf("dim mismatch: src=%dx%d dcm=%dx%d", srcImg.Width, srcImg.Height, dcmImg.Width, dcmImg.Height)
	}
	// Verbatim copy + matching photometric ⇒ byte-identical decoded RGB.
	if !bytes.Equal(srcImg.Pix, dcmImg.Pix) {
		// Find first differing pixel for a useful message.
		n := len(srcImg.Pix)
		if len(dcmImg.Pix) < n {
			n = len(dcmImg.Pix)
		}
		for i := 0; i < n; i++ {
			if srcImg.Pix[i] != dcmImg.Pix[i] {
				t.Fatalf("pixel mismatch at byte %d: src=%d dcm=%d (colorspace/photometric likely wrong)", i, srcImg.Pix[i], dcmImg.Pix[i])
			}
		}
		t.Fatalf("pixel buffers differ in length: src=%d dcm=%d", len(srcImg.Pix), len(dcmImg.Pix))
	}
}
```

- [ ] **Step 2: Run, verify PASS**

```bash
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run TestSVSToDICOMPixelRoundTrip -v 2>&1 | grep -v "duplicate lib"
```
Expected: PASS. If it FAILS on a pixel mismatch, the derived photometric is wrong for CMU (should be `RGB`) OR opentile's DICOM decode does not honor it — **investigate before proceeding**: dump the emitted `PhotometricInterpretation` (`dcmdump out.dcm | grep -i photometric`), confirm it is `RGB`, and check `Inspect` on CMU's tile (0,0) returns `ColorRGB`. This failure is exactly the safety net working — do not weaken the assertion to make it pass; fix the derivation.

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/convert_dicom_test.go
git commit -m "test(convert): SVS->DICOM pixel round-trip (colorspace correctness)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: dciodvfy conformance on the SVS path (the de-risk)

**Files:** modify `Makefile`. Possibly loop back to `buildDescriptor`/`dataset.go` if dciodvfy rejects the RGB-photometric instance.

This confirms the RGB-photometric + synthesized-sRGB SVS→DICOM instance is conformant — the new risk this slice introduces (P0 only proved YBR_FULL_422 from a DICOM source).

- [ ] **Step 1: Extend `make dicom-validate`** (`Makefile`)

The existing `dicom-validate` target emits + validates the Grundium DICOM fixture. Add a second emit + validate for the SVS fixture. Find the recipe body and add, after the existing dciodvfy invocation, a parallel SVS block. The full target should validate BOTH; concretely, append these lines inside the recipe (mirroring the existing DICOM block's style, using the same `$(DCIODVFY)` and gating):

```make
	@SVS="$$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs"; \
	if [ -f "$$SVS" ]; then \
		OUT2=$$(mktemp -t wsm-svs.XXXXXX).dcm; \
		./bin/wsitools convert --to dicom --level 0 -f -o "$$OUT2" "$$SVS"; \
		echo "=== dciodvfy (SVS->DICOM) $$OUT2 ==="; \
		"$(DCIODVFY)" "$$OUT2"; RC2=$$?; \
		rm -f "$$OUT2"; \
		[ $$RC2 -eq 0 ] || exit $$RC2; \
	else echo "no SVS fixture; skipping SVS->DICOM validation"; fi
```
(Adapt variable names to the existing recipe; ensure the SVS dciodvfy exit code is propagated like the DICOM one.)

- [ ] **Step 2: Run the validator (CONTROLLER step — needs dciodvfy)**

> dciodvfy is David Clunie's dicom3tools, NOT brew-installable. The controller obtained it this session at `/tmp/dciodvfy` (precompiled macexe from dclunie.com; executing an agent-downloaded binary needs explicit user authorization). Run via the override:

```bash
go build -o bin/wsitools ./cmd/wsitools 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" make dicom-validate DCIODVFY=/tmp/dciodvfy 2>&1 | grep -vE "duplicate lib|level=INFO"
```
Expected: **0 Errors** for both the DICOM and SVS instances (a benign "Study ID" DICOMDIR warning is acceptable). Count `^Error` lines — must be 0.

- [ ] **Step 3: If dciodvfy reports errors on the SVS instance, fix and re-run**

Likely culprits and fixes (iterate in `buildDescriptor`/`dataset.go`):
- **RGB-photometric rejected with JPEG Baseline** — DICOM PS3.5 permits `PhotometricInterpretation` `RGB` with transfer syntax `…4.50`; if dciodvfy flags a photometric/transfer-syntax/SamplesPerPixel inconsistency, recheck `SamplesPerPixel=3`, `PlanarConfiguration=0`, and that the emitted photometric exactly matches `Inspect`'s result. Do NOT silently switch to YBR (that would mislabel the RGB bytes — the Task 5 pixel test guards this).
- **ICCProfile rejected** — confirm `srgb.icc` is a valid profile (`acsp` at offset 36) and is actually emitted (CMU has no source ICC → must use the embedded one).
- Any other missing Type-1/1C attribute → add it (same loop as P0 Task 5).
Re-run until **0 Errors**. **If RGB-photometric conformance proves structurally impossible** (e.g. dciodvfy mandates YBR for `…4.50` and the bytes are RGB), STOP and report — that's a real finding affecting the slice's approach (it would mean RGB-APP14 SVS needs a re-encode path, which is out of scope).

- [ ] **Step 4: Commit**

```bash
git add Makefile internal/dicomwriter/
git commit -m "feat(dicomwriter): SVS->DICOM passes dciodvfy; validate SVS in make dicom-validate

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Docs

**Files:** modify `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md`.

- [ ] **Step 1: Update docs**

- **CHANGELOG.md** `## [Unreleased]` → `### Added`: `convert --to dicom` now accepts **non-DICOM sources** (SVS etc.) for a single level — verbatim JPEG-baseline tile-copy with `PhotometricInterpretation` derived from the source JPEG's markers (handles the Aperio APP14 RGB variant) and ICC carried-or-synthesized (sRGB). Validated with `dciodvfy` (0 errors) and a pixel round-trip on CMU-1-Small-Region.svs. JPEG-baseline only (JPEG 2000 / other codecs error); full pyramid still pending.
- **README.md**: update the `convert --to dicom` bullet (added in P0) to note non-DICOM JPEG-baseline sources + single level are now supported; update the **Format × command support** table — the SVS / generic-TIFF / OME-TIFF / COG-WSI rows' *convert (to)* note can reference DICOM as a target where the level is JPEG-baseline (keep the `P0`/`P1` footnote accurate: P1 = non-DICOM single-level JPEG-baseline). Adjust footnote ⁶ to describe the Phase 1 state.
- **docs/roadmap.md**: under the DICOM writer item, add a `✅ DONE (2026-06-11): Phase 1 first slice` sub-bullet (non-DICOM single-level SVS→DICOM, marker-driven photometric, sRGB synth, dciodvfy 0 errors, pixel round-trip on the CI fixture); note the next slice = full pyramid (instance-per-level Series) and JPEG 2000 codec.
- **docs/notes/2026-06-03-dicom-writer-scoping.md**: add a `## Phase 1 — slice 1 outcome (2026-06-11)` section: non-DICOM single-level shipped; the CMU finding (APP14-RGB, 4:4:4, SOF-before-APP14, no source ICC); RGB-photometric conformance result from dciodvfy; remaining Phase 1 slices (full pyramid, JPEG 2000).

- [ ] **Step 2: Verify + commit**

```bash
go build ./... 2>&1 | grep -v "duplicate lib"
git add README.md CHANGELOG.md docs/roadmap.md docs/notes/2026-06-03-dicom-writer-scoping.md
git commit -m "docs: DICOM-WSI writer Phase 1 first slice (non-DICOM single-level SVS->DICOM)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final review

Dispatch a final reviewer (focus: photometric derivation matches the source bytes for both the RGB-APP14 and YCbCr cases; the pixel round-trip genuinely guards colorspace; P0 DICOM-source output is byte-identical; the codec gate + clear errors for non-JPEG; sRGB synthesis closes the ICC gap; dciodvfy 0-errors is genuine). Then use `superpowers:finishing-a-development-branch`.

## Self-review notes (author)

- **Spec coverage:** jpegmeta colorspace inspection (T1), sRGB synthesis (T2), descriptor parameterization (T3), codec gate + probe + derivation + drop-guard (T4), pixel round-trip + readback (T5), dciodvfy de-risk on the SVS/RGB path (T6), docs (T7). Marker-driven photometric (decision 3) = T1+T4; pixel-correctness (decision 4) = T5; ICC-synthesis-mandatory (decision 5) = T2+T4; codec-driven gate (decision 6) = T4; JPEG-baseline-only error (decision 2) = T4.
- **Type consistency:** `JPEGInfo`/`JPEGColor`/`Inspect`/`Photometric`, `ImageDescriptor{Photometric,ImageType,ICCProfile,LossyRatio}`, `assembleWSMDataset(...,desc ImageDescriptor)`, `buildDescriptor(src,level,lossyRatio)`, `srgbICCProfile` used consistently across T1–T5. `encapsulatePixelData` 3-return signature unchanged.
- **Critical correctness baked into the plan from probing:** parser scans to SOS (Aperio SOF-before-APP14); CMU = RGB photometric; CMU lacks ICC → srgb path; pixel round-trip decodes via opentile honoring the emitted photometric.
- **Scope:** single level, non-DICOM, JPEG-baseline tile-copy only; full pyramid / JPEG 2000 / re-encode explicitly deferred (T6 step 3 carries the STOP-and-report signal if RGB conformance is structurally impossible).
