# Scale Metadata Across Transformations — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `convert` and `downsample` outputs carry TIFF resolution tags (derived from MPP) and the WSI private MPP/magnification tags, for **all** source formats, with correctly-scaled values on downsample.

**Architecture:** Fix the source layer to read opentile-go's cross-format per-axis MPP (`MicronsPerPixelX/Y`) instead of SVS-only. Add an `MPPToResolution` helper in the TIFF core. Teach both writers (streamwriter, cogwsiwriter) to emit resolution + WSI MPP/mag tags from per-axis MPP. Wire the callers to pass the (scaled, for downsample) values.

**Tech Stack:** Go 1.26, `internal/tiff` byte primitives, opentile-go reader.

**Spec:** `docs/superpowers/specs/2026-05-31-transformation-scale-metadata-design.md`

---

## File Structure

- Modify `internal/source/source.go` — add `MPPX, MPPY` to `Metadata`.
- Modify `internal/source/opentile.go` — populate per-axis MPP cross-format (SVS fallback).
- Modify `cmd/wsitools/info.go` — display + JSON per-axis MPP.
- Create `internal/tiff/resolution.go` — `MPPToResolution`.
- Modify `internal/tiff/tags.go` — resolution tag constants.
- Create `internal/tiff/resolution_test.go`.
- Modify `internal/tiff/streamwriter/options.go` + `writer.go` — MPP/mag Options + emit.
- Create `internal/tiff/streamwriter/scale_tags_test.go`.
- Modify `internal/tiff/cogwsiwriter/writer.go` — emit resolution.
- Modify `cmd/wsitools/downsample.go`, `convert_tiff.go`, `convert_cogwsi.go` — pass values.
- Create `cmd/wsitools/scale_metadata_test.go` — integration value checks.

---

## Task 1: `internal/tiff` — MPPToResolution helper + constants

**Files:**
- Modify: `internal/tiff/tags.go`
- Create: `internal/tiff/resolution.go`
- Test: `internal/tiff/resolution_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/tiff/resolution_test.go`:

```go
package tiff

import (
	"math"
	"testing"
)

func TestMPPToResolution(t *testing.T) {
	// 0.25 µm/px → 40000 px/cm.
	num, denom := MPPToResolution(0.25)
	if denom == 0 {
		t.Fatal("denom = 0")
	}
	got := float64(num) / float64(denom)
	if math.Abs(got-40000) > 1 {
		t.Errorf("MPPToResolution(0.25) = %d/%d = %g px/cm, want ~40000", num, denom, got)
	}
	// Round-trip: recovered MPP within 0.1%.
	recovered := 10000.0 / got
	if math.Abs(recovered-0.25)/0.25 > 0.001 {
		t.Errorf("round-trip MPP = %g, want ~0.25", recovered)
	}
}

func TestMPPToResolutionUnknown(t *testing.T) {
	for _, mpp := range []float64{0, -1} {
		if n, d := MPPToResolution(mpp); n != 0 || d != 0 {
			t.Errorf("MPPToResolution(%g) = %d/%d, want 0/0", mpp, n, d)
		}
	}
}

func TestMPPToResolutionNoOverflow(t *testing.T) {
	// Very small MPP would overflow the scaled numerator; helper must
	// fall back to a smaller denominator and stay within uint32.
	for _, mpp := range []float64{0.5, 0.25, 0.1, 0.06, 0.001} {
		num, denom := MPPToResolution(mpp)
		if num == 0 || denom == 0 {
			t.Errorf("MPPToResolution(%g) = %d/%d, want nonzero", mpp, num, denom)
		}
		got := float64(num) / float64(denom)
		want := 10000.0 / mpp
		if math.Abs(got-want)/want > 0.01 {
			t.Errorf("MPPToResolution(%g) = %g px/cm, want ~%g", mpp, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tiff/ -run TestMPPToResolution -v`
Expected: FAIL — `undefined: MPPToResolution`.

- [ ] **Step 3: Add tag constants** — in `internal/tiff/tags.go`, add to the standard tag-ID block (near the other `Tag*` constants):

```go
// Standard TIFF resolution tags.
const (
	TagXResolution    uint16 = 282
	TagYResolution    uint16 = 283
	TagResolutionUnit uint16 = 296
)

// ResolutionUnitCentimeter is the ResolutionUnit (296) value for
// pixels-per-centimeter.
const ResolutionUnitCentimeter uint16 = 3
```

- [ ] **Step 4: Implement the helper** — create `internal/tiff/resolution.go`:

```go
package tiff

import "math"

// MPPToResolution converts microns-per-pixel to a TIFF XResolution /
// YResolution RATIONAL expressed in pixels-per-centimeter (pair with
// ResolutionUnit = ResolutionUnitCentimeter). Returns (0, 0) when mpp
// is not a usable positive value.
//
// pixels/cm = 10000 µm/cm ÷ mpp µm/px. The numerator is scaled by a
// denominator chosen to keep it within uint32 across the realistic MPP
// range; for extreme (tiny) MPP it falls back to denom=1.
func MPPToResolution(mpp float64) (num, denom uint32) {
	if mpp <= 0 || math.IsNaN(mpp) || math.IsInf(mpp, 0) {
		return 0, 0
	}
	pxPerCm := 10000.0 / mpp
	// Prefer denom=10 for ~0.1 px/cm precision; fall back if it would
	// overflow uint32.
	if scaled := pxPerCm * 10.0; scaled <= float64(math.MaxUint32) {
		return uint32(math.Round(scaled)), 10
	}
	if pxPerCm <= float64(math.MaxUint32) {
		return uint32(math.Round(pxPerCm)), 1
	}
	return math.MaxUint32, 1 // saturate; unreachable for real slides
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/tiff/ -run TestMPPToResolution -v`
Expected: PASS (all three tests).

- [ ] **Step 6: Commit**

```bash
git add internal/tiff/tags.go internal/tiff/resolution.go internal/tiff/resolution_test.go
git commit -m "feat(tiff): MPPToResolution helper + resolution tag constants

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `internal/source` — cross-format per-axis MPP

**Files:**
- Modify: `internal/source/source.go` (Metadata struct, ~line 110)
- Modify: `internal/source/opentile.go` (Metadata(), ~line 67)

- [ ] **Step 1: Add per-axis fields** — in `internal/source/source.go`, change the `Metadata` struct:

```go
// Metadata is the cross-format scanner / acquisition info.
type Metadata struct {
	Make, Model, Software, SerialNumber string
	Magnification                       float64
	MPP                                 float64 // symmetric µm/px (0 if unknown OR asymmetric)
	MPPX                                float64 // µm/px, X axis; 0 if unknown
	MPPY                                float64 // µm/px, Y axis; 0 if unknown
	AcquisitionDateTime                 time.Time
	Raw                                 map[string]string
}
```

- [ ] **Step 2: Populate cross-format with SVS fallback** — in `internal/source/opentile.go`, replace the `Metadata()` body's MPP block (the `if smd, ok := svsfmt.MetadataOf(s.t); ok { m.MPP = smd.MPP ... }` section) with:

```go
	// Cross-format scale: opentile-go normalizes every format's native
	// pixel size into MicronsPerPixelX/Y. Prefer that; fall back to the
	// SVS-specific struct only when the cross-format value is absent.
	m.MPPX = md.MicronsPerPixelX
	m.MPPY = md.MicronsPerPixelY
	m.MPP = md.MicronsPerPixel // opentile's symmetric value (0 if asymmetric)
	if smd, ok := svsfmt.MetadataOf(s.t); ok {
		if m.MPPX == 0 && smd.MPP != 0 {
			m.MPPX, m.MPPY, m.MPP = smd.MPP, smd.MPP, smd.MPP
		}
		if smd.Filename != "" {
			m.Raw["filename"] = smd.Filename
		}
	}
```

(Leave the rest of `Metadata()` — Make/Model/Magnification/etc. — unchanged. `md` is already `s.t.Metadata()`.)

- [ ] **Step 3: Build to verify it compiles**

Run: `go build ./internal/source/`
Expected: builds clean. (`opentile.Metadata` exposes `MicronsPerPixel`, `MicronsPerPixelX`, `MicronsPerPixelY` — confirmed in opentile-go metadata.go.)

- [ ] **Step 4: Commit**

```bash
git add internal/source/source.go internal/source/opentile.go
git commit -m "fix(source): read cross-format per-axis MPP (not SVS-only)

opentile-go normalizes every format's pixel size into MicronsPerPixelX/Y;
wsitools previously surfaced MPP only via the SVS metadata struct, dropping
it for NDPI/Philips/OME/BIF/etc.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `info` — surface per-axis MPP (and prove the NDPI fix)

**Files:**
- Modify: `cmd/wsitools/info.go` (JSON struct ~line 61; display ~line 147)
- Test: `cmd/wsitools/scale_metadata_test.go` (create)

- [ ] **Step 1: Write the failing integration test** — create `cmd/wsitools/scale_metadata_test.go`:

```go
package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestInfoReportsMPPForNDPI proves the cross-format MPP fix: info on an
// NDPI fixture now prints an MPP line (previously dropped — NDPI carries
// MPP in its TIFF resolution tags, which opentile-go reads).
func TestInfoReportsMPPForNDPI(t *testing.T) {
	bin := stripedBinary(t)               // from striped_formats_test.go
	sample := stripedSample(t, "ndpi/CMU-1.ndpi")
	out, err := exec.Command(bin, "info", sample).CombinedOutput()
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "MPP:") {
		t.Errorf("info NDPI output missing 'MPP:' line:\n%s", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `make build && WSI_TOOLS_TESTDIR="$PWD/sample_files" go test ./cmd/wsitools/ -run TestInfoReportsMPPForNDPI -v`
Expected: FAIL — no `MPP:` line (until Task 2's binary change is built in; if Task 2 is already merged, this passes — in that case proceed, the test still guards the behavior).

- [ ] **Step 3: Per-axis display + JSON** — in `cmd/wsitools/info.go`:

Add `MPPX`/`MPPY` to the JSON metadata struct (near `MPP float64 \`json:"mpp"\``):

```go
	MPP           float64 `json:"mpp"`
	MPPX          float64 `json:"mpp_x"`
	MPPY          float64 `json:"mpp_y"`
```

Populate them where the struct is built (near `MPP: md.MPP`):

```go
			MPP:           md.MPP,
			MPPX:          md.MPPX,
			MPPY:          md.MPPY,
```

Replace the human MPP display block:

```go
	if r.Metadata.MPPX > 0 && r.Metadata.MPPX == r.Metadata.MPPY {
		fmt.Fprintf(w, "MPP:     %g\n", r.Metadata.MPPX)
	} else if r.Metadata.MPPX > 0 || r.Metadata.MPPY > 0 {
		fmt.Fprintf(w, "MPP:     %g × %g (x,y)\n", r.Metadata.MPPX, r.Metadata.MPPY)
	}
```

- [ ] **Step 4: Run to verify it passes**

Run: `make build && WSI_TOOLS_TESTDIR="$PWD/sample_files" go test ./cmd/wsitools/ -run TestInfoReportsMPPForNDPI -v`
Expected: PASS. Eyeball: `./bin/wsitools info sample_files/ndpi/CMU-1.ndpi | grep MPP` shows a value.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/info.go cmd/wsitools/scale_metadata_test.go
git commit -m "feat(info): report per-axis MPP; now populated for all formats

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: `streamwriter` — emit resolution + WSI MPP/mag tags

**Files:**
- Modify: `internal/tiff/streamwriter/options.go`
- Modify: `internal/tiff/streamwriter/writer.go`
- Test: `internal/tiff/streamwriter/scale_tags_test.go` (create)

- [ ] **Step 1: Write the failing test** — create `internal/tiff/streamwriter/scale_tags_test.go`:

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

// TestScaleTagsEmitted verifies the writer emits XResolution (282),
// YResolution (283), ResolutionUnit (296), and the WSI MPP/mag tags
// (65085/65086/65087) when MPP/magnification are set. Presence-checked
// via tiffinfo (value math is covered by tiff.MPPToResolution tests and
// the cmd integration tests).
func TestScaleTagsEmitted(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "scale.tiff")
	w, _ := streamwriter.Create(path, streamwriter.Options{
		BigTIFF:       tiff.BigTIFFOn,
		MPPX:          0.5,
		MPPY:          0.5,
		Magnification: 20,
	})
	l, _ := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: tiff.CompressionNone, Photometric: 2,
		WSIImageType: tiff.WSIImageTypePyramid,
	})
	l.WriteTile(0, 0, make([]byte, 8*8*3))
	w.Close()

	out, _ := exec.Command("tiffinfo", path).CombinedOutput()
	got := strings.ToLower(string(out))
	for _, want := range []string{"resolution", "65085", "65087"} {
		if !strings.Contains(got, want) {
			t.Errorf("tiffinfo output missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tiff/streamwriter/ -run TestScaleTagsEmitted -v`
Expected: FAIL to compile — `unknown field 'MPPX' in struct literal`.

- [ ] **Step 3: Add Options fields** — in `internal/tiff/streamwriter/options.go`, add to `Options` (after the `wsitools private tags` block):

```go
	// Physical scale, emitted on L0 when > 0: XResolution/YResolution
	// (derived from MPP, pixels-per-cm) + ResolutionUnit, and the WSI
	// private tags WSIMPPx/WSIMPPy/WSIMagnification.
	MPPX          float64
	MPPY          float64
	Magnification float64
```

- [ ] **Step 4: Store + emit in writer.go**

Add fields to the `Writer` struct (after `toolsVersion string`):

```go
	mppX          float64
	mppY          float64
	magnification float64
```

Assign them in `Create` (after `toolsVersion: opts.ToolsVersion,`):

```go
		mppX:             opts.MPPX,
		mppY:             opts.MPPY,
		magnification:    opts.Magnification,
```

Append to `addL0Metadata` (after the `toolsVersion` block, before the closing brace):

```go
	if w.mppX > 0 {
		n, d := tiff.MPPToResolution(w.mppX)
		b.AddRational(tiff.TagXResolution, []uint32{n}, []uint32{d})
		b.AddDouble(tiff.TagWSIMPPX, []float64{w.mppX})
	}
	if w.mppY > 0 {
		n, d := tiff.MPPToResolution(w.mppY)
		b.AddRational(tiff.TagYResolution, []uint32{n}, []uint32{d})
		b.AddDouble(tiff.TagWSIMPPY, []float64{w.mppY})
	}
	if w.mppX > 0 || w.mppY > 0 {
		b.AddShort(tiff.TagResolutionUnit, []uint16{tiff.ResolutionUnitCentimeter})
	}
	if w.magnification > 0 {
		b.AddDouble(tiff.TagWSIMagnification, []float64{w.magnification})
	}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/tiff/streamwriter/ -run TestScaleTagsEmitted -v`
Expected: PASS (or SKIP if tiffinfo missing — if skipped, also run `go build ./...` to confirm compile).

- [ ] **Step 6: Commit**

```bash
git add internal/tiff/streamwriter/options.go internal/tiff/streamwriter/writer.go internal/tiff/streamwriter/scale_tags_test.go
git commit -m "feat(streamwriter): emit resolution + WSI MPP/magnification tags

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: `cogwsiwriter` — emit resolution tags

**Files:**
- Modify: `internal/tiff/cogwsiwriter/writer.go` (the MPP-emit block, ~line 476)

- [ ] **Step 1: Add resolution emission** — in `internal/tiff/cogwsiwriter/writer.go`, inside the block that emits the WSI MPP tags, add resolution emission. Replace:

```go
		if opts.Metadata.MPPX > 0 {
			b.AddDouble(tiff.TagWSIMPPX, []float64{opts.Metadata.MPPX})
		}
		if opts.Metadata.MPPY > 0 {
			b.AddDouble(tiff.TagWSIMPPY, []float64{opts.Metadata.MPPY})
		}
```

with:

```go
		if opts.Metadata.MPPX > 0 {
			b.AddDouble(tiff.TagWSIMPPX, []float64{opts.Metadata.MPPX})
			n, d := tiff.MPPToResolution(opts.Metadata.MPPX)
			b.AddRational(tiff.TagXResolution, []uint32{n}, []uint32{d})
		}
		if opts.Metadata.MPPY > 0 {
			b.AddDouble(tiff.TagWSIMPPY, []float64{opts.Metadata.MPPY})
			n, d := tiff.MPPToResolution(opts.Metadata.MPPY)
			b.AddRational(tiff.TagYResolution, []uint32{n}, []uint32{d})
		}
		if opts.Metadata.MPPX > 0 || opts.Metadata.MPPY > 0 {
			b.AddShort(tiff.TagResolutionUnit, []uint16{tiff.ResolutionUnitCentimeter})
		}
```

- [ ] **Step 2: Build + run existing cogwsi tests**

Run: `go build ./internal/tiff/cogwsiwriter/ && go test ./internal/tiff/cogwsiwriter/ -count=1`
Expected: builds clean; existing tests PASS (no regression).

- [ ] **Step 3: Commit**

```bash
git add internal/tiff/cogwsiwriter/writer.go
git commit -m "feat(cogwsiwriter): emit resolution tags derived from MPP

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Wire the callers

**Files:**
- Modify: `cmd/wsitools/downsample.go` (Options literal, ~line 193)
- Modify: `cmd/wsitools/convert_tiff.go` (two Options literals, ~lines 90 and 283)
- Modify: `cmd/wsitools/convert_cogwsi.go` (Metadata literal, ~line 60)

- [ ] **Step 1: downsample — pass scaled values**

In `cmd/wsitools/downsample.go`, the `streamwriter.Create(dsOutput, streamwriter.Options{...})` literal: add the three fields. `desc` has already been mutated by `desc.MutateForDownsample(...)` above, so `desc.MPP` and `desc.AppMag` are the scaled output values:

```go
	w, err := streamwriter.Create(dsOutput, streamwriter.Options{
		BigTIFF:          bigtiffMode,
		ImageDescription: desc.Encode(),
		ToolsVersion:     Version,
		SourceFormat:     string(src.Format()),
		FormatName:       "svs",
		AcceptedOrders:   acceptedOrdersForFormat("svs"),
		DefaultOrder:     order,
		MPPX:             desc.MPP,
		MPPY:             desc.MPP,
		Magnification:    desc.AppMag,
	})
```

- [ ] **Step 2: convert_tiff — pass source values (both literals)**

In `cmd/wsitools/convert_tiff.go`, both `streamwriter.Options{...}` literals (the one near line 90 and the one near line 283) — add the three fields to each. `md := src.Metadata()` is in scope in both functions:

```go
		DefaultOrder:   order,
		MPPX:           md.MPPX,
		MPPY:           md.MPPY,
		Magnification:  md.Magnification,
	}
```

(Values are unchanged from source; dimensions don't change. The writer gates on `> 0`, so unknown MPP emits nothing.)

- [ ] **Step 3: convert_cogwsi — per-axis MPP**

In `cmd/wsitools/convert_cogwsi.go`, change the `Metadata` literal (lines ~61–63):

```go
			MPPX:                md.MPPX,
			MPPY:                md.MPPY,
			Magnification:       md.Magnification,
```

(was `MPPX: md.MPP, MPPY: md.MPP`.)

- [ ] **Step 4: Build**

Run: `make build`
Expected: builds clean (ignore the `duplicate libraries` linker warning).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/downsample.go cmd/wsitools/convert_tiff.go cmd/wsitools/convert_cogwsi.go
git commit -m "feat(cli): pass MPP/magnification into writers (scaled on downsample)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Integration value tests + end-to-end verification

**Files:**
- Modify: `cmd/wsitools/scale_metadata_test.go` (add cases)

- [ ] **Step 1: Add the value-assertion integration tests** — append to `cmd/wsitools/scale_metadata_test.go`:

```go
import (
	// add to the existing import block:
	"os"
	"path/filepath"
)

// dumpRaw runs `dump-ifds --raw` and returns the output text.
func dumpRaw(t *testing.T, bin, file string) string {
	t.Helper()
	out, err := exec.Command(bin, "dump-ifds", "--raw", file).CombinedOutput()
	if err != nil {
		t.Fatalf("dump-ifds --raw %s: %v\n%s", file, err, out)
	}
	return string(out)
}

// TestDownsampleScalesMPPAndMag: factor-2 output's WSIMagnification is
// half the source AppMag and WSIMPPx is double the source MPP, emitted as
// the dedicated WSI private tags.
func TestDownsampleScalesMPPAndMag(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/scan_620_.svs") // AppMag=40, MPP≈0.2506
	out := filepath.Join(t.TempDir(), "ds.svs")
	cmdOut, err := exec.Command(bin, "downsample", "--factor", "2", "-f", "-o", out, src).CombinedOutput()
	if err != nil {
		t.Fatalf("downsample: %v\n%s", err, cmdOut)
	}
	raw := dumpRaw(t, bin, out)
	// Source 40x → output 20x; source MPP ~0.25 → output ~0.50.
	if !strings.Contains(raw, "65087") || !strings.Contains(raw, "WSIMagnification") {
		t.Errorf("downsample output missing WSIMagnification tag:\n%s", tail(raw))
	}
	if !strings.Contains(raw, "value=20") {
		t.Errorf("WSIMagnification should be 20 (40/2); not found:\n%s", grepLines(raw, "65087"))
	}
	if !strings.Contains(raw, "65085") {
		t.Errorf("downsample output missing WSIMPPx tag:\n%s", tail(raw))
	}
	_ = os.Stdout
}

// TestConvertCogWSICarriesScaleNDPI: cog-wsi from an NDPI source carries
// the WSI MPP/mag tags + resolution (cross-format MPP path).
func TestConvertCogWSICarriesScaleNDPI(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "ndpi/CMU-1.ndpi")
	out := filepath.Join(t.TempDir(), "o.cog.tiff")
	cmdOut, err := exec.Command(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, src).CombinedOutput()
	if err != nil {
		if strings.Contains(string(cmdOut), "no space left on device") {
			t.Skipf("disk full: %s", cmdOut)
		}
		t.Fatalf("convert: %v\n%s", err, cmdOut)
	}
	raw := dumpRaw(t, bin, out)
	for _, want := range []string{"65085", "65087", "XResolution", "ResolutionUnit"} {
		if !strings.Contains(raw, want) {
			t.Errorf("cog-wsi(NDPI) output missing %q", want)
		}
	}
}

func tail(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}

func grepLines(s, sub string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
```

NOTE: if `scan_620_.svs` is absent in CI's fixture set, `stripedSample` self-skips — these are local/extended-fixture tests. The Magnification value assertion (`value=20`) depends on the DOUBLE being rendered as `20`; if `dump-ifds --raw` renders it as `20` for a whole number this passes — verify in Step 2 and adjust the literal to match actual rendering (e.g. `value=20` vs `value=20.0`) if needed.

- [ ] **Step 2: Run the integration tests; calibrate the value literal**

Run:
```bash
make build
WSI_TOOLS_TESTDIR="$PWD/sample_files" go test ./cmd/wsitools/ -run 'TestDownsampleScalesMPPAndMag|TestConvertCogWSICarriesScaleNDPI|TestInfoReportsMPPForNDPI' -v
```
Expected: PASS. If the WSIMagnification value renders differently than `value=20` (check the failure output), update the literal in the test to the exact rendering and re-run. Manually confirm:
```bash
./bin/wsitools downsample --factor 2 -f -o /tmp/ds.svs sample_files/svs/scan_620_.svs
./bin/wsitools dump-ifds --raw /tmp/ds.svs | grep -E '6508[567]|Resolution'
rm -f /tmp/ds.svs
```
Expected: WSIMPPx ≈ 0.501, WSIMagnification = 20, XResolution present.

- [ ] **Step 3: Full suite + vet**

Run:
```bash
WSI_TOOLS_TESTDIR="$PWD/sample_files" go test ./... -race -count=1
make vet
```
Expected: all `ok`; vet clean. (Note: absolute `WSI_TOOLS_TESTDIR` is required — relative paths break sub-package integration tests.)

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/scale_metadata_test.go
git commit -m "test(cli): integration checks for scaled/cross-format scale metadata

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review notes

- **Spec coverage:** D1 resolution-from-MPP (Task 1 helper + Tasks 4/5 emit) ✓; D2 cross-format MPP w/ SVS fallback (Task 2) ✓; D3 per-axis (Task 2 fields, Tasks 4/5/6 per-axis) ✓; D4 cog-wsi description unchanged (not touched) ✓; D5 secondary banner untouched ✓; D6 MPP=0 → no tags (writer `>0` gates, Task 4/5) ✓. §3.1 info per-axis (Task 3) ✓. §5 tests: MPPToResolution (Task 1), streamwriter presence (Task 4), cogwsi no-regression (Task 5), source/info NDPI (Task 3), downsample-scaled + cog-NDPI values (Task 7) ✓.
- **Type consistency:** `Options.MPPX/MPPY/Magnification` (Task 4) ↔ writer fields `mppX/mppY/magnification` (Task 4) ↔ callers (Task 6). `source.Metadata.MPPX/MPPY` (Task 2) ↔ consumers `md.MPPX/md.MPPY` (Tasks 3, 6). `tiff.MPPToResolution`, `tiff.TagXResolution/TagYResolution/TagResolutionUnit`, `tiff.ResolutionUnitCentimeter` (Task 1) used identically in Tasks 4/5. `AddRational(tag, []uint32, []uint32)` / `AddDouble(tag, []float64)` / `AddShort(tag, []uint16)` match the real signatures.
- **Calibration flag:** Task 7's `value=20` literal is verified/adjusted in Step 2 against actual `dump-ifds --raw` rendering — the only deliberately-confirmed-at-runtime value.
```
