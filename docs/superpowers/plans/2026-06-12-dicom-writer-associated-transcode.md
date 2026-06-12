# DICOM-WSI writer — transcode non-tile-copyable associated images — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Emit associated images Phase 2 drops (the LZW label on every Aperio SVS) by decoding them via opentile-go v0.38.0 and storing them as uncompressed native-pixel-data DICOM instances (lossless).

**Architecture:** Bump opentile-go to v0.38.0 and surface its new `AssociatedImage.Decode` in the wsitools source layer (deleting wsitools' TIFF-reparse workaround). Add a native (non-encapsulated) PixelData writer (de-risked by a spike). `writeAssociated` branches: tile-copyable→verbatim encapsulated (Phase 2); else→decode→native uncompressed instance.

**Tech Stack:** Go, opentile-go v0.38.0 (`AssociatedImage.Decode`), `github.com/suyashkumar/dicom` v1.1.0 (`frame.NativeFrame`), `dciodvfy`.

**Spec:** `docs/superpowers/specs/2026-06-12-dicom-writer-associated-transcode-design.md`

**Branch:** create `feat/dicom-associated-transcode` off `main`. Never implement on `main`.

**Verified facts (probed this session):**
- opentile-go v0.38.0: `AssociatedImage.Decode(opts decoder.DecodeOptions) (*decoder.Image, error)` — faithful decode for every associated type/codec incl. LZW+Predictor=2; honors `decoder.DecodeOptions{Format: decoder.PixelFormatRGB}`; returns `decoder.ErrCodecUnavailable` under `nojp2k`. `decoder.Image{Pix []byte, Width, Height, Stride int, Format}`.
- suyashkumar/dicom: `frame.NativeFrame[I]{RawData []I, InternalSamplesPerPixel, InternalRows, InternalCols, InternalBitsPerSample int}`; `frame.Frame{Encapsulated bool, EncapsulatedData EncapsulatedFrame, NativeData INativeFrame}`; `*frame.NativeFrame[uint8]` implements `INativeFrame`. `write.go` native branch (≈640) reads `NativeData.Rows()/Cols()/SamplesPerPixel()/BitsPerSample()`. P0 note: `dicom.NewElement(tag.PixelData, …)` SIGSEGVs for the *encapsulated* case (forces OW + len 0 → native branch on nil NativeData); the native case has non-nil NativeData so the native branch is valid — the spike confirms the exact element construction (NewElement vs hand-built + VR/length).
- wsitools `cmd/wsitools/extract.go`: decode path is one call `img, err := decodeAssociated(path, bytesIn, srcComp, match.Size().X, match.Size().Y)` (extract.go ≈96); `decodeAssociated` dispatches per codec incl. `readLZWFromTIFF(path,…)` (extract.go ≈188) and `rgbToImage(rgb,w,h) image.Image` (≈479); imports `xtiff "golang.org/x/image/tiff"`. The JPEG byte-pass-through path (`--format jpeg` + source JPEG) stays.
- wsitools `internal/source`: `AssociatedImage` interface in `source.go`; `opentileAssociated{a opentile.AssociatedImage, …}` in `opentile.go` (already has `Bytes`/`Compression`/`Size`/`Type`).
- `internal/dicomwriter`: `writeAssociated(w, src, a, shared, instanceNumber)` + `WritePyramid` (the codec pre-gate at the associated loop) + `associatedSupported(comp) bool` + `instanceSpec` (embeds `ImageDescriptor`); `encapsulateOneFrame(body)` builds the encapsulated PixelData element. `assembleWSMDataset(src, uids, spec)` reads TransferSyntax/Photometric/SamplesPerPixel/Lossy from the spec.

---

## File structure

| Path | Change | Responsibility |
|---|---|---|
| `go.mod` / `go.sum` | modify | opentile-go v0.37.0 → v0.38.0 |
| `internal/source/source.go` | modify | `AssociatedImage.Decode` interface method |
| `internal/source/opentile.go` | modify | `opentileAssociated.Decode` pass-through |
| `cmd/wsitools/extract.go` | modify | delete `decodeAssociated`/`readLZWFromTIFF`/`xtiff`; use `a.Decode()` |
| `internal/dicomwriter/native.go` | new | `explicitVRLE` const + `nativePixelData` |
| `internal/dicomwriter/native_test.go` | new | native-write spike round-trip |
| `internal/dicomwriter/dicomwriter.go` | modify | `writeAssociated` decode branch; `WritePyramid` drop pre-skip |
| `internal/dicomwriter/associated_test.go` | modify | native LZW-label unit |
| `cmd/wsitools/convert_dicom_test.go` | modify | LZW-label CLI pixel round-trip |
| `Makefile` | modify | `dicom-validate` covers the native label |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md` | modify | document |

---

## Task 1: opentile-go v0.38.0 + `source.AssociatedImage.Decode()` + delete the workaround

**Files:** `go.mod`, `go.sum`, `internal/source/source.go`, `internal/source/opentile.go`, `cmd/wsitools/extract.go`.

- [ ] **Step 1: Bump opentile-go**
```bash
go get github.com/wsilabs/opentile-go@v0.38.0 && go mod tidy
grep "opentile-go v" go.mod   # expect v0.38.0
```

- [ ] **Step 2: Add `Decode` to the `source.AssociatedImage` interface** (`internal/source/source.go`). Find the interface and add the method (alongside `Bytes`):
```go
	Bytes() ([]byte, error) // self-contained encoded blob

	// Decode returns the faithfully-decoded pixels (delegates to opentile-go,
	// which owns all codec / LZW-predictor / TIFF-strip handling).
	Decode(opts decoder.DecodeOptions) (*decoder.Image, error)
```
Add the import `"github.com/wsilabs/opentile-go/decoder"` to `source.go`.

- [ ] **Step 3: Implement the pass-through** (`internal/source/opentile.go`). Add near the other `opentileAssociated` methods:
```go
func (a *opentileAssociated) Decode(opts decoder.DecodeOptions) (*decoder.Image, error) {
	return a.a.Decode(opts)
}
```
Add the `decoder` import if not already present.

- [ ] **Step 4: Delete the workaround in `extract.go`; use `a.Decode()`.** Replace the decode call site:
```go
	// Decode → re-encode path.
	img, err := decodeAssociated(path, bytesIn, srcComp, match.Size().X, match.Size().Y)
	if err != nil {
		return err
	}
```
with:
```go
	// Decode → re-encode path (opentile-go owns all codec/predictor handling).
	di, err := match.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
	if err != nil {
		return fmt.Errorf("decode associated %s: %w", extractType, err)
	}
	img := rgbToImage(di.Pix, di.Width, di.Height)
```
Then **delete** the now-unused `decodeAssociated` function, the `readLZWFromTIFF` function, and the `xtiff "golang.org/x/image/tiff"` import (plus any other imports only used by those, e.g. `bytes` if now unused — let the compiler tell you). Keep `rgbToImage` (still used) and the JPEG byte-pass-through path. Ensure `decoder` is imported. `bytesIn`/`srcComp` are still used by the JPEG-passthrough branch above; `path` is still used to open the slide — leave those.

- [ ] **Step 5: Build + wsitools regression**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ ./internal/source/ -run 'Extract|Associated|Source|Decode' -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l internal/source/ cmd/wsitools/extract.go
go vet ./internal/source/ ./cmd/wsitools/
```
Expected: PASS — `extract` tests (incl. the LZW-label extract, now via opentile `Decode`) stay green; `go mod tidy` left no stray deps (the `xtiff` dep may still be used by `cmd/wsitools/associated.go`; only remove the import from `extract.go`, not from `go.mod`, unless `go mod tidy` drops it).

- [ ] **Step 6: opentile-go suite (version-bump verification — CONTROLLER may assist)**

Per CLAUDE.md, confirm the new `Decode` actually works (not just that wsitools compiles). In the opentile-go checkout (`/Volumes/Ext/GitHub/opentile-go`):
```bash
cd /Volumes/Ext/GitHub/opentile-go && OPENTILE_TESTDIR="$(pwd)/sample_files" go test ./decoder/... ./formats/... 2>&1 | tail -20
```
Expected: PASS (confirm the associated-Decode tests run, not SKIP). If the local checkout isn't at v0.38.0, `git -C /Volumes/Ext/GitHub/opentile-go fetch --tags && git -C … checkout v0.38.0` first, or note it couldn't be run locally. Return to the wsitools dir afterward.

- [ ] **Step 7: Commit**
```bash
git add go.mod go.sum internal/source/source.go internal/source/opentile.go cmd/wsitools/extract.go
git commit -m "feat(source): AssociatedImage.Decode via opentile-go v0.38.0; delete TIFF-reparse workaround

opentile-go v0.38.0 adds faithful associated-image decode (incl. LZW+Predictor=2,
GH opentile-go#20). Surface it as source.AssociatedImage.Decode and delete
extract.go's readLZWFromTIFF / per-codec decodeAssociated / xtiff workaround.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: native (uncompressed) PixelData — spike + builder

**Files:** create `internal/dicomwriter/native.go`, `internal/dicomwriter/native_test.go`. This is the **de-risk**: pin the exact native-PixelData construction (P0-style).

- [ ] **Step 1: Write the spike test** (`internal/dicomwriter/native_test.go`):
```go
package dicomwriter

import (
	"bytes"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// TestNativePixelDataRoundTrip de-risks the suyashkumar/dicom NATIVE (non-
// encapsulated) PixelData write path: build a tiny uncompressed RGB instance,
// dicom.Write it (Explicit VR LE), dicom.Parse it back, and assert the raw pixels
// survive. The working construction recorded here drives nativePixelData.
func TestNativePixelDataRoundTrip(t *testing.T) {
	const rows, cols = 2, 2
	rgb := []byte{
		10, 20, 30, 40, 50, 60, // row 0: two RGB pixels
		70, 80, 90, 100, 110, 120, // row 1
	}
	pd, err := nativePixelData(rgb, rows, cols, 3)
	if err != nil {
		t.Fatalf("nativePixelData: %v", err)
	}
	mk := func(tg tag.Tag, v any) *dicom.Element {
		e, err := dicom.NewElement(tg, v)
		if err != nil {
			t.Fatalf("NewElement(%v): %v", tg, err)
		}
		return e
	}
	ds := dicom.Dataset{Elements: []*dicom.Element{
		mk(tag.MediaStorageSOPClassUID, []string{wsmSOPClassUID}),
		mk(tag.MediaStorageSOPInstanceUID, []string{NewUID()}),
		mk(tag.TransferSyntaxUID, []string{explicitVRLE}),
		mk(tag.SamplesPerPixel, []int{3}),
		mk(tag.PhotometricInterpretation, []string{"RGB"}),
		mk(tag.PlanarConfiguration, []int{0}),
		mk(tag.Rows, []int{rows}),
		mk(tag.Columns, []int{cols}),
		mk(tag.BitsAllocated, []int{8}),
		mk(tag.BitsStored, []int{8}),
		mk(tag.HighBit, []int{7}),
		mk(tag.PixelRepresentation, []int{0}),
		pd,
	}}
	var buf bytes.Buffer
	if err := dicom.Write(&buf, ds); err != nil {
		t.Fatalf("dicom.Write: %v", err)
	}
	got, err := dicom.Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	if err != nil {
		t.Fatalf("dicom.Parse: %v", err)
	}
	pdBack, err := got.FindElementByTag(tag.PixelData)
	if err != nil {
		t.Fatalf("PixelData missing on read-back: %v", err)
	}
	info, ok := pdBack.Value.GetValue().(dicom.PixelDataInfo)
	if !ok {
		t.Fatalf("PixelData value is %T, want dicom.PixelDataInfo", pdBack.Value.GetValue())
	}
	if info.IsEncapsulated {
		t.Fatalf("read-back PixelData is encapsulated, want native")
	}
	if len(info.Frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(info.Frames))
	}
}
```

- [ ] **Step 2: Run, verify FAIL** — `go test ./internal/dicomwriter/ -run TestNativePixelDataRoundTrip` → FAIL (undefined `nativePixelData`/`explicitVRLE`).

- [ ] **Step 3: Implement** (`internal/dicomwriter/native.go`). Start from this construction; **if `dicom.Write`/`Parse` fails on the native element**, adjust per the library's native path (`write.go` `writePixelData`) — e.g. a hand-built element with VR `OW` and the correct byte length — and record the working form in a comment (this step is the de-risk; iterate until the spike passes):
```go
package dicomwriter

import (
	"fmt"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/frame"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// explicitVRLE is the Explicit VR Little Endian transfer syntax — used for
// uncompressed (native) pixel data.
const explicitVRLE = "1.2.840.10008.1.2.1"

// nativePixelData builds a NATIVE (non-encapsulated) PixelData element from
// interleaved 8-bit samples (rgb, length = rows*cols*samples). Used for an
// associated image whose source codec can't be tile-copied into DICOM (e.g. an
// LZW label) — decoded and stored uncompressed, losslessly.
func nativePixelData(rgb []byte, rows, cols, samples int) (*dicom.Element, error) {
	if len(rgb) != rows*cols*samples {
		return nil, fmt.Errorf("nativePixelData: have %d bytes, want %d (%dx%d×%d)", len(rgb), rows*cols*samples, cols, rows, samples)
	}
	nf := &frame.NativeFrame[uint8]{
		RawData:                 rgb,
		InternalSamplesPerPixel: samples,
		InternalRows:            rows,
		InternalCols:            cols,
		InternalBitsPerSample:   8,
	}
	return dicom.NewElement(tag.PixelData, dicom.PixelDataInfo{
		IsEncapsulated: false,
		Frames:         []*frame.Frame{{Encapsulated: false, NativeData: nf}},
	})
}
```

- [ ] **Step 4: Run, verify PASS** — `go test ./internal/dicomwriter/ -run TestNativePixelDataRoundTrip -v 2>&1 | grep -v "duplicate lib"` → PASS. `gofmt -l internal/dicomwriter/native.go internal/dicomwriter/native_test.go`; `go vet ./internal/dicomwriter/`.

- [ ] **Step 5: Commit**
```bash
git add internal/dicomwriter/native.go internal/dicomwriter/native_test.go
git commit -m "feat(dicomwriter): native (uncompressed) PixelData writer + Explicit-VR-LE spike

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `writeAssociated` decode branch + `WritePyramid` drop pre-skip

**Files:** modify `internal/dicomwriter/dicomwriter.go`, `internal/dicomwriter/associated_test.go`.

- [ ] **Step 1: Branch `writeAssociated` on codec.** Replace the current codec-gate-and-encapsulate body of `writeAssociated` so that: supported codecs keep the verbatim encapsulated path; unsupported codecs decode → native. The function becomes (preserving the existing shared-metadata/spec/UIDs scaffolding — adapt to the current code, this shows the branch):

```go
func writeAssociated(w io.Writer, src source.Source, a source.AssociatedImage, shared sharedUIDs, instanceNumber int) error {
	md := src.Metadata()
	icc := md.ICCProfile
	if len(icc) == 0 {
		icc = srgbICCProfile
	}
	imageType, specimenLabel := associatedFlavor(a.Type())
	mppX, mppY := baseMPP(md)
	psX, psY, imgW, imgH := levelSpatial(src.Levels()[0].Size(), a.Size(), mppX, mppY)

	base := instanceSpec{
		Size:                 a.Size(),
		TileSize:             a.Size(),
		NumFrames:            1,
		ImageType:            imageType,
		SpecimenLabelInImage: specimenLabel,
		InstanceNumber:       instanceNumber,
		PixelSpacingX:        psX,
		PixelSpacingY:        psY,
		ImagedVolumeW:        imgW,
		ImagedVolumeH:        imgH,
	}

	var spec instanceSpec
	var pd *dicom.Element

	if associatedSupported(a.Compression()) {
		// Verbatim encapsulated tile-copy (JPEG / JPEG 2000).
		body, err := a.Bytes()
		if err != nil {
			return fmt.Errorf("%w: %s bytes: %v", errSkipAssociated, a.Type(), err)
		}
		uncompressed := int64(a.Size().X) * int64(a.Size().Y) * 3
		lossyRatio := 1.0
		if len(body) > 0 {
			lossyRatio = float64(uncompressed) / float64(len(body))
		}
		desc, err := codecColor(body, a.Compression(), icc, lossyRatio)
		if err != nil {
			return fmt.Errorf("%w: %s codec probe: %v", errSkipAssociated, a.Type(), err)
		}
		spec = base
		spec.ImageDescriptor = desc
		if pd, err = encapsulateOneFrame(body); err != nil {
			return err
		}
	} else {
		// Decode (opentile owns codec/predictor) → store uncompressed native RGB.
		di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
		if err != nil {
			return fmt.Errorf("%w: %s decode: %v", errSkipAssociated, a.Type(), err)
		}
		rgb := tightRGB(di)
		spec = base
		spec.ImageDescriptor = ImageDescriptor{
			TransferSyntax:  explicitVRLE,
			Photometric:     "RGB",
			SamplesPerPixel: 3,
			ICCProfile:      icc,
			Lossy:           false,
			LossyMethod:     "",
			LossyRatio:      1.0,
		}
		// Geometry from the decoded image (authoritative dims).
		spec.Size = image.Point{X: di.Width, Y: di.Height}
		spec.TileSize = spec.Size
		if pd, err = nativePixelData(rgb, di.Height, di.Width, 3); err != nil {
			return err
		}
	}

	uids := UIDSet{
		SOP:              NewUID(),
		Study:            shared.Study,
		Series:           shared.Series,
		FrameOfReference: shared.FrameOfReference,
		DimensionOrg:     shared.DimensionOrg,
	}
	ds, err := assembleWSMDataset(src, uids, spec)
	if err != nil {
		return err
	}
	ds.Elements = append(ds.Elements, pd)
	return dicom.Write(w, ds)
}

// tightRGB returns a tightly-packed Height*Width*3 RGB buffer from a decoder.Image,
// stripping any row stride padding.
func tightRGB(di *decoder.Image) []byte {
	rowBytes := di.Width * 3
	if di.Stride == rowBytes {
		return di.Pix[:di.Height*rowBytes]
	}
	out := make([]byte, di.Height*rowBytes)
	for y := 0; y < di.Height; y++ {
		copy(out[y*rowBytes:(y+1)*rowBytes], di.Pix[y*di.Stride:y*di.Stride+rowBytes])
	}
	return out
}
```
Add imports to `dicomwriter.go`: `"image"` (now used for `image.Point`) and `"github.com/wsilabs/opentile-go/decoder"`. (`frame`/`tag` already imported from the Phase-2 work; `errSkipAssociated`/`associatedSupported`/`encapsulateOneFrame`/`codecColor`/`baseMPP`/`levelSpatial`/`associatedFlavor` already exist.)

- [ ] **Step 2: `WritePyramid` — drop the pre-open skip.** In `WritePyramid`'s associated loop, remove the pre-gate that skips unsupported codecs before opening the writer (every associated image now emits — verbatim or decoded). Keep the post-write `errors.Is(werr, errSkipAssociated)` handling so a genuine decode failure still skips with a warning. The loop becomes:
```go
	for _, a := range src.Associated() {
		name := a.Type()
		w, err := newWriter(name)
		if err != nil {
			return fmt.Errorf("open writer for %s: %w", name, err)
		}
		werr := writeAssociated(w, src, a, shared, instanceNumber)
		cerr := w.Close()
		if werr != nil {
			if errors.Is(werr, errSkipAssociated) {
				slog.Warn("skipping associated image", "type", name, "reason", werr)
				continue
			}
			return fmt.Errorf("write associated %s: %w", name, werr)
		}
		if cerr != nil {
			return fmt.Errorf("close associated %s: %w", name, cerr)
		}
		instanceNumber++
	}
```
NOTE: this re-introduces the post-open-skip stray-file edge (a decode-failure leaves a 0-byte file). To avoid that, the CLI factory should create the file lazily — but that's the existing follow-up; for this task, the common path (LZW decode succeeds) leaves no stray file, and decode failure is rare (`nojp2k` JP2K associated). Acceptable; note it.

- [ ] **Step 3: Native LZW-label unit test** — append to `internal/dicomwriter/associated_test.go`. Use a fixture whose label is LZW (the SVS fixtures). Since `associated_test.go` is in `internal/dicomwriter` and the SVS open is via `source.Open`, add a helper to open CMU and find the LZW label:
```go
func TestWriteAssociatedLZWLabelNative(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no CMU SVS fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var label source.AssociatedImage
	for _, a := range src.Associated() {
		if a.Type() == "label" {
			label = a
		}
	}
	if label == nil || associatedSupported(label.Compression()) {
		t.Skip("no non-tile-copyable label in fixture")
	}
	var buf bytes.Buffer
	if err := writeAssociated(&buf, src, label, newSharedUIDs(), 5); err != nil {
		t.Fatalf("writeAssociated(label): %v", err)
	}
	ds, err := dicom.Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ts := firstStrA(t, ds, tag.TransferSyntaxUID); ts != explicitVRLE {
		t.Errorf("TransferSyntaxUID = %q, want %q (native uncompressed)", ts, explicitVRLE)
	}
	if ph := firstStrA(t, ds, tag.PhotometricInterpretation); ph != "RGB" {
		t.Errorf("PhotometricInterpretation = %q, want RGB", ph)
	}
	if lc := firstStrA(t, ds, tag.LossyImageCompression); lc != "00" {
		t.Errorf("LossyImageCompression = %q, want 00 (lossless)", lc)
	}
	it, _ := ds.FindElementByTag(tag.ImageType)
	if got := it.Value.GetValue().([]string); len(got) < 3 || got[2] != "LABEL" {
		t.Errorf("ImageType[2] = %v, want LABEL", got)
	}
}
```
(Ensure `bytes`, `os`, `path/filepath`, `dicom`, `tag`, `source` are imported in `associated_test.go` — they are from Phase 2.)

- [ ] **Step 4: Build, test, commit**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./internal/dicomwriter/ ./cmd/wsitools/ -run 'DICOM|Assemble|ConvertDICOM|WritePyramid|Associated|SlideLabel|PerLevel|JP2K|Lossless|Mono|PixelRoundTrip|WriteVolumeInstance|Native' -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l internal/dicomwriter/
go vet ./internal/dicomwriter/ ./cmd/wsitools/
```
Expected: PASS — the new native LZW-label test + the spike + the full regression (tile-copyable associated, pyramid, P0 unchanged). gofmt empty; vet clean.
```bash
git add internal/dicomwriter/dicomwriter.go internal/dicomwriter/associated_test.go
git commit -m "feat(dicomwriter): transcode non-tile-copyable associated images to native DICOM

writeAssociated now decodes a non-JPEG/JP2K associated image (e.g. an LZW label)
via opentile and stores it as an uncompressed native RGB instance, instead of
skipping it. Tile-copyable associated images stay verbatim encapsulated.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: CLI LZW-label pixel round-trip

**Files:** modify `cmd/wsitools/convert_dicom_test.go`.

- [ ] **Step 1: Add the test** — append. `convert --to dicom -o <dir>` on CMU now writes `label.dcm` (was dropped); assert it exists, opens as `dicom`, and its decoded pixels equal the source label decode.
```go
func TestConvertDICOMLZWLabelTranscode(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	svs := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skip("no CMU SVS fixture")
	}
	src, err := source.Open(svs)
	if err != nil {
		t.Fatal(err)
	}
	var label source.AssociatedImage
	for _, a := range src.Associated() {
		if a.Type() == "label" {
			label = a
		}
	}
	if label == nil {
		src.Close()
		t.Skip("fixture has no label")
	}
	want, err := label.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
	if err != nil {
		src.Close()
		t.Fatalf("decode source label: %v", err)
	}
	src.Close()

	out := filepath.Join(t.TempDir(), "pyr")
	convertCmd.Flags().Lookup("level").Changed = false
	cvOutput, cvForce, cvNoAssociated = "", false, false
	rootCmd.SetArgs([]string{"convert", "--to", "dicom", "-o", out, svs})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		convertCmd.Flags().Lookup("level").Changed = false
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("convert --to dicom: %v", err)
	}
	labelPath := filepath.Join(out, "label.dcm")
	ls, err := source.Open(labelPath)
	if err != nil {
		t.Fatalf("open emitted label.dcm: %v", err)
	}
	defer ls.Close()
	if ls.Format() != "dicom" {
		t.Errorf("label.dcm Format = %q, want dicom", ls.Format())
	}
	// Pixel round-trip: the emitted label decodes to the same pixels as the source.
	gotSlide, err := opentile.OpenFile(labelPath)
	if err != nil {
		t.Fatalf("opentile.OpenFile(label.dcm): %v", err)
	}
	defer gotSlide.Close()
	got, err := gotSlide.ImageReadRegion(0, 0, 0, 0, want.Width, want.Height, opentile.WithFormat(decoder.PixelFormatRGB))
	if err != nil {
		t.Fatalf("read emitted label region: %v", err)
	}
	if got.Width != want.Width || got.Height != want.Height {
		t.Fatalf("dim mismatch: src=%dx%d emitted=%dx%d", want.Width, want.Height, got.Width, got.Height)
	}
	if !bytes.Equal(tight(got), tight(want)) {
		t.Error("emitted LZW label pixels differ from source decode (transcode not faithful)")
	}
}

// tight strips any row-stride padding to a packed Height*Width*3 RGB buffer.
// (dicomwriter.tightRGB is unexported and in another package, so cmd/wsitools
// needs its own copy.)
func tight(di *decoder.Image) []byte {
	rowBytes := di.Width * 3
	if di.Stride == rowBytes {
		return di.Pix[:di.Height*rowBytes]
	}
	out := make([]byte, di.Height*rowBytes)
	for y := 0; y < di.Height; y++ {
		copy(out[y*rowBytes:(y+1)*rowBytes], di.Pix[y*di.Stride:y*di.Stride+rowBytes])
	}
	return out
}
```
(Imports: `bytes`, `os`, `path/filepath`, `testing`, `source`, `opentile`, `decoder` — most present from the slice-1 pixel test; add any missing. The `tight` helper above is local to the test file because `dicomwriter.tightRGB` is unexported.)

- [ ] **Step 2: Run + clean + commit**
```bash
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run 'TestConvertDICOM' -v -count=1 2>&1 | grep -v "duplicate lib"
gofmt -l cmd/wsitools/convert_dicom_test.go
go vet ./cmd/wsitools/
git add cmd/wsitools/convert_dicom_test.go
git commit -m "test(convert): LZW label transcodes to native DICOM (pixel round-trip)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: dciodvfy on the native label (the de-risk)

**Files:** modify `Makefile`. Controller runs `/tmp/dciodvfy`.

- [ ] **Step 1: Ensure the SVS block emits a full pyramid (so the label is written).** The current `dicom-validate` SVS block emits a single instance (`--level 0`). Change it to emit the full pyramid into a dir and validate every `*.dcm` (so `label.dcm` is included). Find the SVS block:
```make
	SVS="$$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs"; \
	if [ -f "$$SVS" ]; then \
		OUT2=$$(mktemp -t wsm-svs.XXXXXX).dcm; \
		./bin/wsitools convert --to dicom --level 0 -f -o "$$OUT2" "$$SVS"; \
		echo "=== dciodvfy (SVS->DICOM) $$OUT2 ==="; \
		"$(DCIODVFY)" "$$OUT2" || RC=$$?; \
		rm -f "$$OUT2"; \
	else echo "missing $$SVS; skipping SVS->DICOM"; fi; \
```
Replace with:
```make
	SVS="$$WSI_TOOLS_TESTDIR/svs/CMU-1-Small-Region.svs"; \
	if [ -f "$$SVS" ]; then \
		DIR3=$$(mktemp -d -t wsm-svs.XXXXXX); \
		./bin/wsitools convert --to dicom -f -o "$$DIR3/pyr" "$$SVS"; \
		for L in "$$DIR3"/pyr/*.dcm; do \
			echo "=== dciodvfy (SVS pyramid+assoc) $$L ==="; \
			"$(DCIODVFY)" "$$L" || RC=$$?; \
		done; \
		rm -rf "$$DIR3"; \
	else echo "missing $$SVS; skipping SVS->DICOM"; fi; \
```
(This emits `level-0.dcm` + the JPEG `overview.dcm`/`thumbnail.dcm` + the **native `label.dcm`**, validating all.)

- [ ] **Step 2: Run (CONTROLLER step — needs dciodvfy)**
```bash
go build -o bin/wsitools ./cmd/wsitools 2>&1 | grep -v "duplicate lib"
WSI_TOOLS_TESTDIR="$(pwd)/sample_files" make dicom-validate DCIODVFY=/tmp/dciodvfy 2>&1 | grep -vE "duplicate lib|level=INFO" | grep -E "=== dciodvfy|^Error"
```
Expected: **0 Errors** across all instances, including the SVS `label.dcm` (the native uncompressed RGB instance). Count `^Error` → must be 0.

- [ ] **Step 3: If the native label errors, fix and re-run**
A native (uncompressed) WSM instance has the same attribute set as an encapsulated one minus the lossy tags (`LossyImageCompression "00"`, no ratio/method — already handled by the Type-1C omission). Likely gaps: BitsAllocated/Stored/HighBit (8/8/7 — already emitted), or a transfer-syntax/PixelData VR mismatch. Read every dciodvfy Error, fix in `dataset.go`/`native.go`/`writeAssociated`, re-run to 0. **If the native instance proves structurally non-conformant in a way the slice can't resolve, STOP and report.**

- [ ] **Step 4: Commit**
```bash
git add Makefile
git commit -m "feat(dicomwriter): validate the native (LZW->uncompressed) label in make dicom-validate

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Docs

**Files:** `README.md`, `CHANGELOG.md`, `docs/roadmap.md`, `docs/notes/2026-06-03-dicom-writer-scoping.md`.

- [ ] **Step 1: Update docs**
- **CHANGELOG.md** `## [Unreleased]`: augment the `convert --to dicom` entry — associated images whose codec isn't a DICOM transfer syntax (e.g. the **LZW label** on Aperio SVS) are now **decoded and emitted as uncompressed native DICOM instances** (Explicit VR LE, lossless — preserves the barcode) instead of being skipped. Requires opentile-go **v0.38.0** (`AssociatedImage.Decode`, GH opentile-go#20); deletes wsitools' TIFF-reparse workaround. dciodvfy 0 errors incl. the native label.
- **README.md**: update the `convert --to dicom` bullet/footnote ⁶ — associated images are emitted for all codecs now: JPEG/JP2K verbatim, others decoded → uncompressed.
- **docs/roadmap.md**: note opentile-go bumped to v0.38.0; the LZW-label transcode shipped; remaining DICOM-writer items = HTJ2K / 16-bit / the P0 DICOM-source codec-mislabel bug.
- **docs/notes/2026-06-03-dicom-writer-scoping.md**: add a short `## Associated-image transcode (2026-06-12)` note: LZW labels (every Aperio SVS) decode→uncompressed-native DICOM; decode delegated to opentile-go v0.38 `AssociatedImage.Decode` (#20), wsitools TIFF-reparse deleted; native PixelData via `frame.NativeFrame` + Explicit VR LE; dciodvfy 0 errors.

- [ ] **Step 2: Verify + commit**
```bash
go build ./... 2>&1 | grep -v "duplicate lib"
git add README.md CHANGELOG.md docs/roadmap.md docs/notes/2026-06-03-dicom-writer-scoping.md
git commit -m "docs: transcode non-tile-copyable associated images to native DICOM (LZW labels)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final review

Dispatch a final reviewer (focus: the opentile bump + `Decode` consumption is clean and the wsitools TIFF-reparse workaround is fully deleted; the native PixelData path is conformant (dciodvfy 0 errors) and the LZW label pixel-round-trips faithfully; tile-copyable associated + pyramid + P0 output unchanged; decode-failure skip still works). Then use `superpowers:finishing-a-development-branch`.

## Self-review notes (author)

- **Spec coverage:** opentile bump + source.Decode + delete workaround (T1); native PixelData spike + builder (T2); writeAssociated decode branch + WritePyramid drop-skip (T3); CLI pixel round-trip (T4); dciodvfy de-risk on the native label (T5); docs (T6). opentile-go-suite version-bump verification = T1 step 6.
- **Type consistency:** `AssociatedImage.Decode(decoder.DecodeOptions) (*decoder.Image, error)`, `nativePixelData(rgb,rows,cols,samples)`, `explicitVRLE`, `tightRGB(*decoder.Image)`, `writeAssociated` branch on `associatedSupported`, `instanceSpec.ImageDescriptor` embedding — consistent across tasks.
- **De-risk honesty:** T2 is an explicit spike (native-write library path); T5 carries the STOP-and-report signal for native-instance conformance.
- **Scope:** decode→native for non-tile-copyable associated images; 8-bit RGB assumed; lossless re-encode + the P0 DICOM-source bug explicitly out of scope.
- **Known carry-over:** dropping WritePyramid's pre-skip re-exposes the narrow post-open-skip stray-file edge (decode failure only) — noted in T3.
