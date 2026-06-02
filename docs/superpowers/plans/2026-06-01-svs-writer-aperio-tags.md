# SVS writer Aperio-conformance tags Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `convert --to svs` emit the two genuine-Aperio L0 TIFF tags it currently omits — `ImageDepth` (32997) and `YCbCrSubSampling` (530) — matching what real Aperio SVS files carry.

**Architecture:** Two plain TIFF tags routed through new emit-if-set `streamwriter.Options` fields (`ImageDepth`, `YCbCrSubSampling`), emitted on the L0 IFD in `addL0Metadata` alongside ICC. The streamwriter stays format-agnostic; the SVS-only policy (always emit ImageDepth=1; emit 530 only for JPEG output, with a value that matches the JPEG bytes actually written) lives in the two `convert --to svs` callers. The 530 value comes from parsing the JPEG SOF marker of an actual output tile (tile-copy) or the known 4:2:0 of our own encoder (re-encode).

**Tech Stack:** Go, cobra CLI, `internal/tiff` byte-emission core, `internal/tiff/streamwriter`. Reuses the existing pure-Go JPEG SOF parser in `cmd/wsitools/quality/jpeg`.

**Spec:** `docs/superpowers/specs/2026-06-01-svs-writer-aperio-tags-design.md`

---

## File Structure

- `internal/tiff/tags.go` — add `TagImageDepth` (32997), `TagYCbCrSubSampling` (530) constants.
- `internal/tiff/tagnames.go` — add `32997: "ImageDepth"` to the name dictionary (530 already present).
- `internal/tiff/tagnames_test.go` (create if absent) — assert the new name resolves.
- `cmd/wsitools/quality/jpeg/subsampling.go` (create) — exported `LumaSampling([]byte) (h, v uint16, ok bool)` reusing the package's `parseSOF`.
- `cmd/wsitools/quality/jpeg/subsampling_test.go` (create) — unit test.
- `internal/tiff/streamwriter/options.go` — add `ImageDepth uint32`, `YCbCrSubSampling []uint16` fields.
- `internal/tiff/streamwriter/writer.go` — struct fields, Create wiring, `addL0Metadata` emit.
- `internal/tiff/streamwriter/aperio_tags_test.go` (create) — unit test (tiffinfo), mirroring `icc_test.go`.
- `cmd/wsitools/convert_tiff.go` — wire both SVS paths (`runConvertTIFFTileCopy`, `runConvertTIFFReencode`).
- `cmd/wsitools/convert_aperio_tags_test.go` (create) — integration tests.
- `docs/tiff-tags.md` — add "SVS writer tag profile" section.

---

### Task 1: TIFF tag constants + name

**Files:**
- Modify: `internal/tiff/tags.go:30` (after `TagICCProfile`)
- Modify: `internal/tiff/tags.go:27-29` (resolution tags block — add YCbCrSubSampling near other standard tags)
- Modify: `internal/tiff/tagnames.go` (tagNames map)
- Test: `internal/tiff/tagnames_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/tiff/tagnames_test.go`:

```go
package tiff

import "testing"

func TestTagNameImageDepth(t *testing.T) {
	if got := TagName(32997); got != "ImageDepth" {
		t.Fatalf("TagName(32997) = %q, want %q", got, "ImageDepth")
	}
	if got := TagName(530); got != "YCbCrSubSampling" {
		t.Fatalf("TagName(530) = %q, want %q", got, "YCbCrSubSampling")
	}
}

func TestAperioTagConstants(t *testing.T) {
	if TagImageDepth != 32997 {
		t.Errorf("TagImageDepth = %d, want 32997", TagImageDepth)
	}
	if TagYCbCrSubSampling != 530 {
		t.Errorf("TagYCbCrSubSampling = %d, want 530", TagYCbCrSubSampling)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tiff/ -run 'TagName|AperioTag' -v`
Expected: FAIL — `undefined: TagImageDepth` / `TagYCbCrSubSampling`, and `TagName(32997)` returns `""`.

- [ ] **Step 3: Add the constants**

In `internal/tiff/tags.go`, inside the standard-tag `const` block, add `TagYCbCrSubSampling` near the other standard tags (e.g. right after `TagResolutionUnit uint16 = 296`):

```go
	TagYCbCrSubSampling          uint16 = 530
```

And add `TagImageDepth` right after `TagICCProfile`:

```go
	TagICCProfile                uint16 = 34675 // embedded ICC color profile (UNDEFINED)
	TagImageDepth                uint16 = 32997 // z-depth; Aperio writes 1 (LONG)
```

- [ ] **Step 4: Add the name**

In `internal/tiff/tagnames.go`, add to the `tagNames` map (place near other extended-range entries, e.g. just before the `65080: "WSIImageType"` block):

```go
	32997: "ImageDepth",
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/tiff/ -run 'TagName|AperioTag' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/tiff/tags.go internal/tiff/tagnames.go internal/tiff/tagnames_test.go
git commit -m "feat(tiff): add ImageDepth (32997) and YCbCrSubSampling (530) tag constants"
```

---

### Task 2: JPEG luma-sampling helper

**Files:**
- Create: `cmd/wsitools/quality/jpeg/subsampling.go`
- Test: `cmd/wsitools/quality/jpeg/subsampling_test.go`

This reuses the existing `parseSOF` and `sofComponent` already defined in `cmd/wsitools/quality/jpeg/jpeg.go` (same package). The marker-walk skeleton mirrors the one in `(*inspector).Inspect` but stops at the first SOF.

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/quality/jpeg/subsampling_test.go`:

```go
package jpeg

import "testing"

// sofJPEG builds a minimal SOI + SOF0(3 components) + EOI bytestream.
// yHV is component-0's packed (Hi<<4 | Vi) sampling byte. Cb and Cr are
// fixed at 1x1 (0x11). The SOF segment length is 0x0011 (17 bytes).
func sofJPEG(yHV byte) []byte {
	return []byte{
		0xFF, 0xD8, // SOI
		0xFF, 0xC0, 0x00, 0x11, // SOF0, length=17
		0x08,       // precision
		0x00, 0x01, // height
		0x00, 0x01, // width
		0x03,             // num components
		0x01, yHV, 0x00,  // comp 1 (Y)
		0x02, 0x11, 0x01, // comp 2 (Cb) 1x1
		0x03, 0x11, 0x01, // comp 3 (Cr) 1x1
		0xFF, 0xD9, // EOI
	}
}

func TestLumaSampling(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		h, v uint16
		ok   bool
	}{
		{"4:2:0", sofJPEG(0x22), 2, 2, true},
		{"4:2:2", sofJPEG(0x21), 2, 1, true},
		{"4:4:4", sofJPEG(0x11), 1, 1, true},
		{"not-jpeg", []byte{0x00, 0x01, 0x02, 0x03}, 0, 0, false},
		{"no-sof", []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, v, ok := LumaSampling(c.in)
			if h != c.h || v != c.v || ok != c.ok {
				t.Fatalf("LumaSampling = (%d,%d,%v), want (%d,%d,%v)", h, v, ok, c.h, c.v, c.ok)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/wsitools/quality/jpeg/ -run LumaSampling -v`
Expected: FAIL — `undefined: LumaSampling`.

- [ ] **Step 3: Implement the helper**

Create `cmd/wsitools/quality/jpeg/subsampling.go`:

```go
package jpeg

import "encoding/binary"

// LumaSampling parses the first SOF marker of a JPEG bytestream and
// returns the luma (component 0) horizontal and vertical sampling
// factors. These equal the TIFF YCbCrSubSampling [H, V] pair:
// 4:2:0 → (2,2), 4:2:2 → (2,1), 4:4:4 → (1,1). ok is false if the input
// is not a JPEG (no SOI) or has no parseable SOF before SOS.
func LumaSampling(tileBytes []byte) (h, v uint16, ok bool) {
	if len(tileBytes) < 4 || tileBytes[0] != 0xFF || tileBytes[1] != 0xD8 {
		return 0, 0, false
	}
	i := 2 // skip SOI
	for i < len(tileBytes)-1 {
		if tileBytes[i] != 0xFF {
			return 0, 0, false
		}
		for i < len(tileBytes) && tileBytes[i] == 0xFF {
			i++ // skip fill bytes
		}
		if i >= len(tileBytes) {
			break
		}
		marker := tileBytes[i]
		i++
		switch marker {
		case 0xD8, 0xD9, 0xD0, 0xD1, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7:
			continue // standalone markers, no payload
		case 0xDA: // SOS — entropy-coded data follows; stop.
			return 0, 0, false
		}
		if i+2 > len(tileBytes) {
			return 0, 0, false
		}
		segLen := int(binary.BigEndian.Uint16(tileBytes[i : i+2]))
		if segLen < 2 || i+segLen > len(tileBytes) {
			return 0, 0, false
		}
		segData := tileBytes[i+2 : i+segLen]
		i += segLen
		switch marker {
		case 0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF: // SOFn (excl. C4 DHT, C8 reserved)
			comps := parseSOF(segData)
			if len(comps) == 0 {
				return 0, 0, false
			}
			return uint16(comps[0].hSamp), uint16(comps[0].vSamp), true
		}
	}
	return 0, 0, false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/wsitools/quality/jpeg/ -run LumaSampling -v`
Expected: PASS (all 5 sub-cases).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/quality/jpeg/subsampling.go cmd/wsitools/quality/jpeg/subsampling_test.go
git commit -m "feat(quality/jpeg): add LumaSampling SOF parser for YCbCrSubSampling"
```

---

### Task 3: streamwriter emit-if-set Options

**Files:**
- Modify: `internal/tiff/streamwriter/options.go:34` (after `ICCProfile`)
- Modify: `internal/tiff/streamwriter/writer.go:56` (struct fields), `:93` (Create wiring), `:317-319` (addL0Metadata emit)
- Test: `internal/tiff/streamwriter/aperio_tags_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/tiff/streamwriter/aperio_tags_test.go` (mirrors `icc_test.go`):

```go
package streamwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// TestImageDepthAndSubsamplingEmitted: a streamwriter given ImageDepth /
// YCbCrSubSampling emits tags 32997 / 530 on L0; the zero values emit
// nothing.
func TestImageDepthAndSubsamplingEmitted(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	write := func(depth uint32, sub []uint16) string {
		path := filepath.Join(t.TempDir(), "o.tiff")
		w, err := streamwriter.Create(path, streamwriter.Options{
			BigTIFF: tiff.BigTIFFOn, ImageDepth: depth, YCbCrSubSampling: sub,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		l, _ := w.AddLevel(streamwriter.LevelSpec{
			ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
			Compression: tiff.CompressionNone, Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			WSIImageType: tiff.WSIImageTypePyramid,
		})
		l.WriteTile(0, 0, make([]byte, 8*8*3))
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		out, _ := exec.Command("tiffinfo", path).CombinedOutput()
		return strings.ToLower(string(out))
	}
	with := write(1, []uint16{2, 2})
	if !strings.Contains(with, "depth") {
		t.Errorf("ImageDepth not reported by tiffinfo:\n%s", with)
	}
	if !strings.Contains(with, "subsampling") {
		t.Errorf("YCbCrSubSampling not reported by tiffinfo:\n%s", with)
	}
	none := write(0, nil)
	if strings.Contains(none, "subsampling") {
		t.Errorf("unexpected YCbCrSubSampling with zero values:\n%s", none)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tiff/streamwriter/ -run ImageDepthAndSubsampling -v`
Expected: FAIL — `unknown field ImageDepth in struct literal` (Options has no such fields yet).

- [ ] **Step 3: Add Options fields**

In `internal/tiff/streamwriter/options.go`, after the `ICCProfile []byte` field (line 34):

```go
	// ImageDepth, when > 0, is emitted on L0 as tag 32997 (LONG). Genuine
	// Aperio writes 1. wsitools only produces 2D output.
	ImageDepth uint32

	// YCbCrSubSampling, when len == 2, is emitted on L0 as tag 530
	// (SHORT[2]). Only meaningful for JPEG-compressed output; the caller
	// supplies the value that matches the JPEG bytes actually written.
	YCbCrSubSampling []uint16
```

- [ ] **Step 4: Add struct fields + Create wiring**

In `internal/tiff/streamwriter/writer.go`, after `iccProfile []byte` (line 56):

```go
	imageDepth       uint32
	ycbcrSubSampling []uint16
```

In `Create`, after `iccProfile: opts.ICCProfile,` (line 93):

```go
		imageDepth:       opts.ImageDepth,
		ycbcrSubSampling: opts.YCbCrSubSampling,
```

- [ ] **Step 5: Emit in addL0Metadata**

In `internal/tiff/streamwriter/writer.go`, inside `addL0Metadata`, after the ICC block (lines 317-319):

```go
	if w.imageDepth > 0 {
		b.AddLong(tiff.TagImageDepth, []uint32{w.imageDepth})
	}
	if len(w.ycbcrSubSampling) == 2 {
		b.AddShort(tiff.TagYCbCrSubSampling, w.ycbcrSubSampling)
	}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/tiff/streamwriter/ -run ImageDepthAndSubsampling -v`
Expected: PASS (or SKIP if `tiffinfo` is unavailable — acceptable; the integration tests in Task 4/5 cover the real path deterministically).

- [ ] **Step 7: Commit**

```bash
git add internal/tiff/streamwriter/options.go internal/tiff/streamwriter/writer.go internal/tiff/streamwriter/aperio_tags_test.go
git commit -m "feat(streamwriter): emit ImageDepth/YCbCrSubSampling on L0 when set"
```

---

### Task 4: Wire SVS tile-copy path + integration tests

**Files:**
- Modify: `cmd/wsitools/convert_tiff.go` — `runConvertTIFFTileCopy`, after the ImageDescription `switch` (after line 148, before `streamwriter.Create` at line 150)
- Modify: `cmd/wsitools/convert_tiff.go` imports — add `qualityjpeg "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"`
- Test: `cmd/wsitools/convert_aperio_tags_test.go` (create)

These integration tests run the built binary, so they need `make build` first (same pattern as `convert_deadlock_test.go`). They skip cleanly if the binary or fixtures are absent.

- [ ] **Step 1: Write the failing tests**

Create `cmd/wsitools/convert_aperio_tags_test.go`:

```go
package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dumpIFD0Raw runs `dump-ifds --raw` and returns only the IFD 0 section.
func dumpIFD0Raw(t *testing.T, bin, file string) string {
	t.Helper()
	out, err := exec.Command(bin, "dump-ifds", "--raw", file).CombinedOutput()
	if err != nil {
		t.Fatalf("dump-ifds: %v\n%s", err, out)
	}
	s := string(out)
	start := strings.Index(s, "IFD 0")
	if start < 0 {
		t.Fatalf("no IFD 0 in dump:\n%s", s)
	}
	rest := s[start+len("IFD 0"):]
	if end := strings.Index(rest, "\nIFD 1"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestConvertSVSAperioTagsJPEG: tile-copying a genuine-Aperio JPEG SVS
// emits ImageDepth=1 and YCbCrSubSampling=[2,2] on L0.
func TestConvertSVSAperioTagsJPEG(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1-Small-Region.svs")
	out := filepath.Join(t.TempDir(), "out.svs")

	if o, err := exec.Command(bin, "convert", "--to", "svs", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ifd0 := dumpIFD0Raw(t, bin, out)
	if !strings.Contains(ifd0, "ImageDepth") {
		t.Errorf("L0 missing ImageDepth:\n%s", ifd0)
	}
	if !strings.Contains(ifd0, "YCbCrSubSampling") || !strings.Contains(ifd0, "[2, 2]") {
		t.Errorf("L0 missing YCbCrSubSampling [2, 2]:\n%s", ifd0)
	}
}

// TestConvertSVSAperioTagsNonJPEG: tile-copying a JPEG2000 Aperio SVS
// emits ImageDepth=1 but no YCbCrSubSampling (530 is JPEG-only).
func TestConvertSVSAperioTagsNonJPEG(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/JP2K-33003-1.svs")
	out := filepath.Join(t.TempDir(), "out.svs")

	if o, err := exec.Command(bin, "convert", "--to", "svs", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ifd0 := dumpIFD0Raw(t, bin, out)
	if !strings.Contains(ifd0, "ImageDepth") {
		t.Errorf("L0 missing ImageDepth:\n%s", ifd0)
	}
	if strings.Contains(ifd0, "YCbCrSubSampling") {
		t.Errorf("L0 unexpectedly has YCbCrSubSampling for JPEG2000 output:\n%s", ifd0)
	}
}
```

- [ ] **Step 2: Build and run to verify failure**

Run: `make build && go test ./cmd/wsitools/ -run 'ConvertSVSAperioTags' -v`
Expected: FAIL — `TestConvertSVSAperioTagsJPEG` reports L0 missing ImageDepth / YCbCrSubSampling (not yet wired). (`NonJPEG` may already partially pass on the negative 530 check but fails the ImageDepth check.)

- [ ] **Step 3: Add the import**

In `cmd/wsitools/convert_tiff.go`, add to the `internal/...` import group:

```go
	qualityjpeg "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"
```

- [ ] **Step 4: Wire the tile-copy path**

In `runConvertTIFFTileCopy`, immediately after the ImageDescription `switch` block closes (after line 148, before `w, err := streamwriter.Create(...)`):

```go
	// SVS Aperio-conformance L0 tags. ImageDepth is always 1 (2D output).
	// YCbCrSubSampling is emitted only for JPEG output, parsed from the
	// actual source tile we copy verbatim ("match what we are writing");
	// with Photometric=2 (RGB) it is informational, so a parse miss simply
	// omits it rather than risking a wrong value.
	if container == "svs" {
		opts.ImageDepth = 1
		if compressionTagFor(l0.Compression()) == tiff.CompressionJPEG {
			buf := make([]byte, l0.TileMaxSize())
			if n, err := l0.TileInto(0, 0, buf); err == nil {
				if h, v, ok := qualityjpeg.LumaSampling(buf[:n]); ok {
					opts.YCbCrSubSampling = []uint16{h, v}
				}
			}
		}
	}
```

(`l0 := src.Levels()[0]` is already in scope from line 122; `compressionTagFor`, `tiff`, and `streamwriter.Options` are already used in this function.)

- [ ] **Step 5: Build and run to verify pass**

Run: `make build && go test ./cmd/wsitools/ -run 'ConvertSVSAperioTags' -v`
Expected: PASS for both `JPEG` and `NonJPEG` (or SKIP if fixtures/binary absent).

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert_tiff.go cmd/wsitools/convert_aperio_tags_test.go
git commit -m "feat(convert): emit Aperio ImageDepth/YCbCrSubSampling on SVS tile-copy"
```

---

### Task 5: Wire SVS re-encode path + integration test

**Files:**
- Modify: `cmd/wsitools/convert_tiff.go` — `runConvertTIFFReencode`, after the ImageDescription `switch` (after line 373, before `streamwriter.Create` at line 375)
- Test: `cmd/wsitools/convert_aperio_tags_test.go` (add one test)

- [ ] **Step 1: Write the failing test**

Append to `cmd/wsitools/convert_aperio_tags_test.go`:

```go
// TestConvertSVSAperioTagsReencodeJPEG: re-encoding to JPEG (--codec jpeg)
// emits ImageDepth=1 and YCbCrSubSampling=[2,2] (our encoder is 4:2:0).
func TestConvertSVSAperioTagsReencodeJPEG(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1-Small-Region.svs")
	out := filepath.Join(t.TempDir(), "out.svs")

	if o, err := exec.Command(bin, "convert", "--to", "svs", "--codec", "jpeg", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	ifd0 := dumpIFD0Raw(t, bin, out)
	if !strings.Contains(ifd0, "ImageDepth") {
		t.Errorf("L0 missing ImageDepth:\n%s", ifd0)
	}
	if !strings.Contains(ifd0, "YCbCrSubSampling") || !strings.Contains(ifd0, "[2, 2]") {
		t.Errorf("L0 missing YCbCrSubSampling [2, 2]:\n%s", ifd0)
	}
}
```

- [ ] **Step 2: Build and run to verify failure**

Run: `make build && go test ./cmd/wsitools/ -run 'ConvertSVSAperioTagsReencodeJPEG' -v`
Expected: FAIL — L0 missing ImageDepth / YCbCrSubSampling (re-encode path not yet wired).

- [ ] **Step 3: Wire the re-encode path**

In `runConvertTIFFReencode`, immediately after the ImageDescription `switch` block closes (after line 373, before `w, err := streamwriter.Create(...)`):

```go
	// SVS Aperio-conformance L0 tags. ImageDepth is always 1 (2D output).
	// Our JPEG encoder is fixed at YCbCr 4:2:0, so re-encode-to-JPEG output
	// carries YCbCrSubSampling [2,2]; other codecs omit it (not meaningful).
	if resolvedContainer == "svs" {
		opts.ImageDepth = 1
		if fac.Name() == "jpeg" {
			opts.YCbCrSubSampling = []uint16{2, 2}
		}
	}
```

(`fac` is in scope from line 292; `resolvedContainer` from line 307.)

- [ ] **Step 4: Build and run to verify pass**

Run: `make build && go test ./cmd/wsitools/ -run 'ConvertSVSAperioTags' -v`
Expected: PASS for all three tests (or SKIP if fixtures/binary absent).

- [ ] **Step 5: Run the full package test suites touched**

Run: `go test ./internal/tiff/... ./cmd/wsitools/quality/jpeg/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert_tiff.go cmd/wsitools/convert_aperio_tags_test.go
git commit -m "feat(convert): emit Aperio ImageDepth/YCbCrSubSampling on SVS re-encode"
```

---

### Task 6: Documentation — SVS writer tag profile

**Files:**
- Modify: `docs/tiff-tags.md` (append a new section before "## Reading tags from a slide", or at the end of the private-tags section)

- [ ] **Step 1: Add the section**

Append to `docs/tiff-tags.md` (after the "## wsitools private TIFF tags" table section, before "## Reading tags from a slide"):

```markdown
## SVS writer tag profile

`convert --to svs` aims to produce genuine-Aperio-shaped SVS. The sample
fixtures split by producer, and the two producers emit different L0 tag
sets:

| Tag | Genuine Aperio | Grundium (Aperio-compatible) |
|---|---|---|
| ImageDepth (32997) | yes (= 1) | yes |
| YCbCrSubSampling (530) | yes, for JPEG data | yes |
| ICCProfile (34675) | sometimes | no |
| Orientation (274) | no | yes |
| XResolution/YResolution/ResolutionUnit (282/283/296) | **no** (MPP lives only in the Aperio `ImageDescription`) | yes |
| PageNumber (297) | no | yes |
| ReferenceBlackWhite (532) | no | yes |

The wsitools SVS writer emits, on L0:

- **ImageDepth (32997) = 1** — always. wsitools produces 2D output only.
- **YCbCrSubSampling (530)** — only when the output is JPEG-compressed.
  The value matches the chroma subsampling of the JPEG bytes actually
  written: parsed from a source tile's SOF marker on the lossless
  tile-copy path, or `[2,2]` (4:2:0) for the re-encode-to-JPEG path (our
  encoder's fixed subsampling). Because the writer sets
  `PhotometricInterpretation = RGB (2)` for new-style JPEG-in-TIFF, this
  tag is **informational** — conformant decoders read color from the JPEG
  codestream and ignore 530 — so it exists for Aperio look-alike fidelity,
  not color decode.
- **ICCProfile (34675)** — when the source carries one (pulled via
  opentile's `Slide.ICCProfile()`).
- Plus the wsitools-generated scale tags (XResolution/YResolution/
  ResolutionUnit from MPP, and the WSI private MPP/magnification tags).

**Non-goals (deliberately not emitted for Aperio conformance):** the
Grundium-only tags Orientation (274), PageNumber (297), and
ReferenceBlackWhite (532). Note wsitools *does* generate resolution tags
(282/283/296) from MPP — a readability aid that genuine Aperio omits; that
deviation is intentional and tracked separately.
```

- [ ] **Step 2: Commit**

```bash
git add docs/tiff-tags.md
git commit -m "docs(tiff-tags): add SVS writer tag profile (genuine Aperio vs Grundium)"
```

---

## Self-Review

**1. Spec coverage:**
- R1 (ImageDepth=1, L0, SVS, any codec) → Tasks 3 (emit), 4 (tile-copy set), 5 (re-encode set). ✓
- R2 (YCbCrSubSampling, JPEG-only, match-written value; tile-copy parse, re-encode [2,2]) → Tasks 2 (parser), 4 (tile-copy), 5 (re-encode). ✓
- R3 (L0-only, SVS-gated, policy in caller, writer generic) → Task 3 (generic emit-if-set), Tasks 4/5 (`container == "svs"` gate). ✓
- R4 (dump-ifds shows 32997 by name) → Task 1. ✓
- Testing §1 (SOF helper) → Task 2; §2 (streamwriter emit) → Task 3; §3 (genuine Aperio round-trip) → Task 4 JPEG; §4 (non-JPEG omits 530) → Task 4 NonJPEG; §5 (re-encode [2,2]) → Task 5; §6 (pixels unchanged) → covered by the existing pixel-equivalence suite, unaffected since only informational L0 tags are added. ✓
- Documentation section → Task 6. ✓

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code and exact commands. ✓

**3. Type consistency:** `LumaSampling([]byte) (h, v uint16, ok bool)` — defined in Task 2, called identically in Task 4. `Options.ImageDepth uint32` / `YCbCrSubSampling []uint16` — defined Task 3, set Tasks 4/5. `tiff.TagImageDepth` / `tiff.TagYCbCrSubSampling` / `tiff.CompressionJPEG` — consistent. `fac.Name()`, `compressionTagFor`, `l0.TileInto`, `l0.TileMaxSize`, `l0.Compression` — all match existing usage in `convert_tiff.go`. ✓
