# Per-format crop thumbnail regeneration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Non-SVS crops regenerate the thumbnail (only when the source has one) instead of passing the whole-slide thumbnail through.

**Architecture:** Extract a writer-agnostic `renderCropThumbnail` (downscale cropped L0 → baseline JPEG). `regenCropThumbnail` (streamwriter, reused by tiff/ome-tiff) and a new `regenCropThumbnailCOGWSI` both call it. Each emitter's associated loop replaces the source thumbnail **in place** with the regenerated one; OME-TIFF lists the regenerated dims in its OME-XML.

**Spec:** `docs/superpowers/specs/2026-06-13-crop-thumbnail-regen-design.md`

---

## File Structure

| File | Action |
|---|---|
| `cmd/wsitools/crop_thumbnail.go` | Extract `renderCropThumbnail`; refactor `regenCropThumbnail`; add `regenCropThumbnailCOGWSI` |
| `cmd/wsitools/crop_thumbnail_test.go` | Unit test for `renderCropThumbnail` |
| `cmd/wsitools/crop_formats.go` | Wire in-place thumbnail replacement into `cropToTIFF`/`cropToOMETIFF`/`cropToCOGWSI` |
| `tests/integration/crop_test.go` | cog-wsi thumbnail-regen assertion |

---

## Task 1: Thumbnail render helpers

**Files:** `cmd/wsitools/crop_thumbnail.go`, `cmd/wsitools/crop_thumbnail_test.go`.

- [ ] **Step 1: Write the failing test** — append to `cmd/wsitools/crop_thumbnail_test.go`:

```go
func TestRenderCropThumbnail(t *testing.T) {
	// 2000×1000 RGB L0 → longest side 1024 → 1024×512, valid JPEG.
	l0 := make([]byte, 2000*1000*3)
	for i := range l0 {
		l0[i] = byte(i)
	}
	jpegBytes, tw, th, err := renderCropThumbnail(l0, 2000, 1000, 80)
	if err != nil {
		t.Fatalf("renderCropThumbnail: %v", err)
	}
	if tw != 1024 || th != 512 {
		t.Errorf("dims = %dx%d, want 1024x512", tw, th)
	}
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xFF || jpegBytes[1] != 0xD8 {
		t.Errorf("not a JPEG (no SOI marker), %d bytes", len(jpegBytes))
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./cmd/wsitools/ -run TestRenderCropThumbnail -count=1` → FAIL (`undefined: renderCropThumbnail`).

- [ ] **Step 3: Extract `renderCropThumbnail` + refactor `regenCropThumbnail`.**

In `cmd/wsitools/crop_thumbnail.go`, add `renderCropThumbnail` (the body of the current `regenCropThumbnail` up to and including `jpeg.Encode`):

```go
// renderCropThumbnail box-downscales the cropped L0 to a thumbnail (longest side
// thumbLongSide, aspect preserved) and returns the encoded baseline-JPEG bytes
// and its dimensions. Writer-agnostic.
func renderCropThumbnail(l0 []byte, l0W, l0H, quality int) (jpegBytes []byte, tw, th int, err error) {
	tw, th = thumbDims(l0W, l0H, thumbLongSide)
	src := &otdecoder.Image{Width: l0W, Height: l0H, Stride: l0W * 3, Format: otdecoder.PixelFormatRGB, Pix: l0}
	dst := otdecoder.NewImageFormat(tw, th, otdecoder.PixelFormatRGB)
	if err = otresample.ImageInto(src, dst, otresample.Box); err != nil {
		return nil, 0, 0, fmt.Errorf("thumbnail downscale: %w", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		for x := 0; x < tw; x++ {
			o := y*dst.Stride + x*3
			p := y*img.Stride + x*4
			img.Pix[p] = dst.Pix[o]
			img.Pix[p+1] = dst.Pix[o+1]
			img.Pix[p+2] = dst.Pix[o+2]
			img.Pix[p+3] = 255
		}
	}
	var buf bytes.Buffer
	if err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, fmt.Errorf("thumbnail encode: %w", err)
	}
	return buf.Bytes(), tw, th, nil
}
```

Replace the body of `regenCropThumbnail` with:

```go
func regenCropThumbnail(w *streamwriter.Writer, l0 []byte, l0W, l0H, quality int) error {
	jpegBytes, tw, th, err := renderCropThumbnail(l0, l0W, l0H, quality)
	if err != nil {
		return err
	}
	return w.AddStripped(streamwriter.StrippedSpec{
		Width:           uint32(tw),
		Height:          uint32(th),
		RowsPerStrip:    uint32(th),
		BitsPerSample:   []uint16{8, 8, 8},
		SamplesPerPixel: 3,
		Photometric:     6, // YCbCr (stdlib JFIF); cosmetic for opentile decode
		Compression:     tiff.CompressionJPEG,
		StripBytes:      jpegBytes,
		NewSubfileType:  0,
		WSIImageType:    tiff.WSIImageTypeThumbnail,
	})
}
```

Add `regenCropThumbnailCOGWSI` (needs the `cogwsiwriter` import in this file):

```go
// regenCropThumbnailCOGWSI emits a regenerated thumbnail into a cogwsiwriter.
func regenCropThumbnailCOGWSI(w *cogwsiwriter.Writer, l0 []byte, l0W, l0H, quality int) error {
	jpegBytes, tw, th, err := renderCropThumbnail(l0, l0W, l0H, quality)
	if err != nil {
		return err
	}
	return w.AddAssociated(cogwsiwriter.AssociatedSpec{
		Type:            tiff.WSIImageTypeThumbnail,
		Width:           uint32(tw),
		Height:          uint32(th),
		Compression:     tiff.CompressionJPEG,
		Photometric:     6,
		BitsPerSample:   []uint16{8, 8, 8},
		SamplesPerPixel: 3,
		Bytes:           jpegBytes,
		RowsPerStrip:    uint32(th),
	})
}
```

Add `"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"` to `crop_thumbnail.go` imports. (`bytes`, `image`, `image/jpeg`, `otdecoder`, `otresample`, `streamwriter`, `tiff`, `fmt` are already there.)

- [ ] **Step 4: Run tests + build + vet** — `go test ./cmd/wsitools/ -run 'TestRenderCropThumbnail|TestThumbDims|Crop' -count=1` PASS; `go build ./...` clean; `go vet ./cmd/wsitools/` clean. (`regenCropThumbnail` output unchanged → SVS crop unaffected; confirmed in Step 5.)

- [ ] **Step 5 (controller): SVS crop byte-identity/parity regression** — confirm the `regenCropThumbnail` refactor kept SVS output identical:
`WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run 'TestCropLossless_ByteIdentity|TestCrop_CMU2ParityOracle' -count=1 -timeout 30m` → PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/crop_thumbnail.go cmd/wsitools/crop_thumbnail_test.go
git commit -m "feat(crop): writer-agnostic renderCropThumbnail + cog-wsi thumbnail emit"
```

---

## Task 2: Wire in-place thumbnail replacement into the emitters

**Files:** `cmd/wsitools/crop_formats.go`.

In each emitter's associated loop, when the source image is a thumbnail, emit the regenerated thumbnail **in place** (instead of passthrough). This naturally satisfies "only when the source has one" and keeps emission order consistent.

- [ ] **Step 1: `cropToTIFF`** — replace its associated loop with:

```go
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if a.Type() == opentile.AssociatedThumbnail {
				if err := regenCropThumbnail(w, l0, l0W, l0H, quality); err != nil {
					return fmt.Errorf("regenerate thumbnail: %w", err)
				}
				continue
			}
			if err := writeOneAssociated(w, a); err != nil {
				return fmt.Errorf("write associated %s: %w", a.Type(), err)
			}
		}
	}
```

- [ ] **Step 2: `cropToOMETIFF`** — (a) compute regenerated thumbnail dims and use them in `omeAssocs`; (b) replace the thumbnail in the write loop.

Replace the `omeAssocs` build with:

```go
	ttw, tth := thumbDims(l0W, l0H, thumbLongSide)
	var omeAssocs []OMEAssoc
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			name := omeAssocName(string(a.Type()))
			if name == "" {
				continue
			}
			aw, ah := uint32(a.Size().W), uint32(a.Size().H)
			if a.Type() == opentile.AssociatedThumbnail {
				aw, ah = uint32(ttw), uint32(tth) // regenerated dims must match the written IFD
			}
			omeAssocs = append(omeAssocs, OMEAssoc{Name: name, W: aw, H: ah})
		}
	}
```

Replace the associated write loop with:

```go
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if omeAssocName(string(a.Type())) == "" {
				continue
			}
			if a.Type() == opentile.AssociatedThumbnail {
				if err := regenCropThumbnail(w, l0, l0W, l0H, quality); err != nil {
					return fmt.Errorf("regenerate thumbnail: %w", err)
				}
				continue
			}
			if err := writeOneAssociated(w, a); err != nil {
				return fmt.Errorf("write associated %s: %w", a.Type(), err)
			}
		}
	}
```

> Order invariant: `omeAssocs` and the write loop iterate `src.AssociatedImages()` in the same order and treat the thumbnail at the same position, so the OME-XML Image list matches the written IFD order.

- [ ] **Step 3: `cropToCOGWSI`** — replace the thumbnail in its faithful loop:

```go
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if a.Type() == opentile.AssociatedThumbnail {
				if err := regenCropThumbnailCOGWSI(w, l0, l0W, l0H, quality); err != nil {
					aborted = true
					return fmt.Errorf("regenerate thumbnail: %w", err)
				}
				continue
			}
			spec, err := faithfulCOGWSISpecOT(a)
			if err != nil {
				if errors.Is(err, errSkipAssociated) {
					slog.Warn("skipping associated image", "type", a.Type(), "reason", err)
					continue
				}
				aborted = true
				return err
			}
			if err := w.AddAssociated(spec); err != nil {
				aborted = true
				return fmt.Errorf("add associated %s: %w", a.Type(), err)
			}
		}
	}
```

- [ ] **Step 4: Build + vet + crop unit tests** — `go build ./...` clean; `go vet ./cmd/wsitools/` clean; `go test ./cmd/wsitools/ -run Crop -count=1` PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/crop_formats.go
git commit -m "feat(crop): regenerate thumbnail (in place) for tiff/ome-tiff/cog-wsi crops"
```

---

## Task 3: Integration test (cog-wsi thumbnail regenerated)

**Files:** `tests/integration/crop_test.go`.

- [ ] **Step 1: Write the test** — append:

```go
// TestCrop_ThumbnailRegen verifies a non-SVS crop regenerates the thumbnail to
// the crop's aspect ratio (not the whole-slide thumbnail passed through). Uses
// the cog-wsi fixture (it carries a thumbnail). Local-only.
func TestCrop_ThumbnailRegen(t *testing.T) {
	td := testdir(t)
	bin := buildOnce(t)
	src := filepath.Join(td, "cog-wsi", "CMU-1_cog-wsi.tiff")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	hasThumb := false
	for _, a := range srcTlr.AssociatedImages() {
		if a.Type() == opentile.AssociatedThumbnail {
			hasThumb = true
		}
	}
	srcTlr.Close()
	if !hasThumb {
		t.Skip("source has no thumbnail")
	}

	// Square crop → regenerated thumbnail aspect ≈ 1.0 (the slide thumbnail is wide).
	out := filepath.Join(t.TempDir(), "crop.tiff")
	if b, err := exec.Command(bin, "crop", "--rect", "500,500,2000,2000", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("crop: %v\n%s", err, b)
	}
	outTlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open out: %v", err)
	}
	defer outTlr.Close()

	var thumb opentile.AssociatedImage
	for _, a := range outTlr.AssociatedImages() {
		if a.Type() == opentile.AssociatedThumbnail {
			thumb = a
		}
	}
	if thumb == nil {
		t.Fatal("output has no thumbnail (regeneration dropped it)")
	}
	aspect := float64(thumb.Size().W) / float64(thumb.Size().H)
	if aspect < 0.9 || aspect > 1.1 {
		t.Errorf("thumbnail aspect %.3f not ≈ 1.0 (square crop) — likely passed through, not regenerated", aspect)
	}
	// Must be a decodable JPEG.
	if _, err := thumb.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB}); err != nil {
		t.Errorf("output thumbnail does not decode: %v", err)
	}
	// Other associated still present (label/overview pass through).
	var haveLabel, haveOverview bool
	for _, a := range outTlr.AssociatedImages() {
		switch a.Type() {
		case opentile.AssociatedLabel:
			haveLabel = true
		case opentile.AssociatedOverview:
			haveOverview = true
		}
	}
	if !haveLabel || !haveOverview {
		t.Errorf("passthrough associated missing: label=%v overview=%v", haveLabel, haveOverview)
	}
}
```

> Confirm `thumb.Decode(decoder.DecodeOptions{...})` is the correct opentile API for decoding an associated image (the SVS/parity tests use `decoder.DecodeOptions`); if the signature differs (e.g. variadic `opentile.WithFormat`), match the existing usage in `crop_test.go`.

- [ ] **Step 2 (controller): run it** — `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run TestCrop_ThumbnailRegen -count=1 -timeout 30m -v` → PASS (thumbnail aspect ≈ 1.0, decodes, label/overview present). Implementer compile-checks only (`-run XXX_NONE`).

- [ ] **Step 3: Commit**

```bash
git add tests/integration/crop_test.go
git commit -m "test(crop): cog-wsi crop regenerates thumbnail to crop aspect"
```

---

## Final verification (controller)

- [ ] `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -race ./internal/... ./cmd/wsitools/ -count=1 -timeout 30m` green.
- [ ] Integration: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test -tags integration ./tests/integration/ -run 'TestCrop|TestDownsample' -count=1 -timeout 30m` green (incl. format-preserving + thumbnail regen + SVS regressions).
- [ ] `gofmt -l` clean; final whole-branch review; then superpowers:finishing-a-development-branch. MERGE-VERIFY: grep the merged files + re-run a key test on merged HEAD.

## Notes / risks

- **`regenCropThumbnail` refactor must keep SVS output identical** — guarded by Task 1 Step 5 (SVS byte-identity/parity).
- **OME-XML ↔ IFD dim consistency** — the regenerated thumbnail dims are computed once (`thumbDims`) and used for both the OME-XML entry and the written IFD.
- **cog-wsi `AddAssociated` with a fresh single-strip JPEG** — composes with the finalize pass (spooled like the faithful path); the integration test confirms it decodes.
