# OME-TIFF associated-image editing (Slice 2b, lossy) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `wsitools label|macro|thumbnail|overview remove|replace` on OME-TIFF, by rebuilding through the ome-tiff streamwriter with a forced synthetic (minimal) OME-XML. Explicitly **lossy** and **surfaced** (always-on warning + docs + README + Bio-Formats pointer).

**Architecture:** Extract the streamwriter tile-copy core (level loop + associated write) from `runConvertTIFFTileCopy` into a shared `writeTIFFTileCopy(w, src, container, l0Desc, omeSynthetic, plan)`; thread an `omeEditPlan` through it + `writeAssociatedImages` + `omeAssociatedSpecs`. A new `rebuildOMETIFF` creates a temp ome-tiff, forces synthetic OME-XML built from the plan-filtered associated set, copies pyramid tiles verbatim, applies the plan, and atomically renames. Dispatch in `associated.go` routes `ome-tiff` here.

**Tech Stack:** Go, cobra, `internal/tiff/streamwriter`, opentile-go reader, reused Slice-1/2a image helpers.

**Spec:** `docs/superpowers/specs/2026-06-07-ome-tiff-associated-editing-design.md`

**Branch:** create `feat/ome-tiff-associated-editing` off `main`. Never implement on `main`.

**Contract (LOSSY — by necessity):** rebuild emits wsitools' flattened model, so OME-XML is regenerated minimal. Preserved: pyramid pixels (verbatim tiles), other associated images, dims/MPP/magnification/resolution/ICC. NOT preserved: instrument/acquisition/channel/vendor OME annotations. Must be surfaced (Task 5 + the always-on warning in Task 2).

**Verified API shapes:**
- `streamwriter.Create(path, opts) (*Writer, error)`; level loop: `w.AddLevel(LevelSpec{...}) (*LevelHandle, error)`, async drain via `lh.NextReady()/WriteTileAtIndex`, `lh.WriteTile(tx,ty,bytes)`, `lh.CloseInput()`, `lh.Abort(err)`, `w.Abort()`, `w.Close()`. (See current `runConvertTIFFTileCopy`, convert_tiff.go:185–267 — port the loop verbatim.)
- `StrippedSpec{Width,Height,RowsPerStrip,BitsPerSample,SamplesPerPixel,Photometric,Compression,StripBytes,NewSubfileType,WSIImageType,ExtraTags}`; `w.AddStripped(spec)`.
- `OMEAssoc{Name string; W,H uint32}`; `SyntheticOMEDescription(l0W,l0H uint32, mppX,mppY float64, name, srcSoftware string, assoc []OMEAssoc) string`.
- `omeAssociatedSpecs(src) []OMEAssoc`; `writeAssociatedImages(src, w, container, omeSynthetic)`; `omeAssocName(typ) string`; `newSubfileTypeForAssoc(container,typ) uint32`.
- `compressionTagFor(src.Compression) uint16`; `src.Metadata()` (MPP, MPPX/Y, Magnification, Make, Model, ICCProfile, AcquisitionDateTime).
- Reused: `decodeReplacementImage`, `fitImage`, `resolveTargetDims`, `parseHexColor`, the strip/JPEG encoders (`encodeLZWWhole`/`encodeJPEGWhole`/`encodeDeflateWhole`/`rgbStripBytes` from `associated_replace.go`), `removeFlags`/`replaceFlags`.

---

## File Structure

| Path | Responsibility |
|---|---|
| `cmd/wsitools/convert_tiff.go` (modify) | extract `writeTIFFTileCopy`; thread `omeEditPlan` through it + `writeAssociatedImages` + `omeAssociatedSpecs`; convert passes a no-op plan |
| `cmd/wsitools/associated_rebuild_ometiff.go` (new) | `omeEditPlan`, `rebuildOMETIFF`, remove/replace entry points, the always-on lossy warning |
| `cmd/wsitools/associated.go` (modify) | allow `ome-tiff`; dispatch to the rebuild engine |
| `cmd/wsitools/associated_replace.go` (modify) | `buildReplacementStrippedSpec(img, replaceOpts, container) (*streamwriter.StrippedSpec, error)` |
| `cmd/wsitools/associated_rebuild_test.go` (extend) | unit: omeAssociatedSpecs plan-filtering, spec packaging |
| `cmd/wsitools/associated_integration_test.go` (extend) | gated ome-tiff remove/replace/in-place + warning assertion |
| `docs/ome-tiff-limitations.md` (new) | rudimentary-support statement + Bio-Formats recommendation |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md` | matrix `✓⁹`, limitations note, Slice 2b done |

---

## Task 1: Thread `omeEditPlan`; extract `writeTIFFTileCopy` (refactor, non-regression)

**Files:** Create `cmd/wsitools/associated_rebuild_ometiff.go` (plan type only this task); modify `cmd/wsitools/convert_tiff.go`.

- [ ] **Step 1: Define `omeEditPlan` + plan-aware helpers**

In a new `associated_rebuild_ometiff.go`:

```go
package main

import "github.com/wsilabs/wsitools/internal/tiff/streamwriter"

// omeEditPlan parameterizes the associated-image output of writeTIFFTileCopy /
// writeAssociatedImages / omeAssociatedSpecs. At most one of remove/replace is
// set; the empty plan writes all associated images verbatim; dropAll writes none.
type omeEditPlan struct {
	remove  string
	replace string
	spec    *streamwriter.StrippedSpec // replacement (when replace != "")
	dropAll bool
}
```

- [ ] **Step 2: Thread the plan through `omeAssociatedSpecs` and `writeAssociatedImages`**

Change signatures in `convert_tiff.go`:
- `omeAssociatedSpecs(src source.Source, plan omeEditPlan) []OMEAssoc` — skip `plan.remove`; for `plan.replace` emit the `<Image>` with `plan.spec.Width/Height` and `omeAssocName(plan.replace)`; if `plan.replace` set and the type was absent in `src.Associated()`, append it after the loop (upsert). When `plan.dropAll`, return nil.
- `writeAssociatedImages(src, w, container string, omeSynthetic bool, plan omeEditPlan) error` — in the loop: skip `a.Type()==plan.remove`; if `a.Type()==plan.replace` call `w.AddStripped(*plan.spec)` instead of building from `a`; else build+write verbatim as today. After the loop, if `plan.replace != ""` and the type was absent, `w.AddStripped(*plan.spec)` (upsert). When `plan.dropAll`, write nothing.

Keep the existing OME-synthetic drop rule (`container=="ome-tiff" && omeSynthetic && omeAssocName(a.Type())==""` → skip) for the verbatim branch.

- [ ] **Step 3: Update convert call sites to pass a no-op plan**

`convert_tiff.go:154` and `:404`: `omeAssociatedSpecs(src, omeEditPlan{})`.
`convert_tiff.go:271` and `:433`: wrap unchanged but add the param. Keep the `if !cvNoAssociated` guards; pass `omeEditPlan{}` (NOT dropAll — the guard already handles no-associated). So: `writeAssociatedImages(src, w, container, omeSynthetic, omeEditPlan{})`.

- [ ] **Step 4: Extract `writeTIFFTileCopy`**

Extract the level-copy loop (`convert_tiff.go:185–267`, including the async reorder-drain goroutine — port VERBATIM) plus the associated-write block (`:269–274`) into:

```go
// writeTIFFTileCopy copies src's pyramid into w (verbatim tiles, async drain)
// and writes its associated images per plan. l0Desc is the L0 ImageDescription
// (OME-XML / Aperio header) emitted as an L0-only ExtraTag for svs/ome-tiff.
// Caller owns Create/Close/Abort. Does NOT call w.Abort() on error — returns it.
func writeTIFFTileCopy(w *streamwriter.Writer, src source.Source, container, l0Desc string, omeSynthetic bool, plan omeEditPlan) error {
	// ... port the level loop verbatim, but on error RETURN it (after lh.Abort/<-drainErr)
	//     instead of calling w.Abort()+return; the caller aborts ...
	// ... then: if err := writeAssociatedImages(src, w, container, omeSynthetic, plan); err != nil { return err }
	// return nil
}
```

In `runConvertTIFFTileCopy`, replace lines 185–274 with:
```go
	if err := writeTIFFTileCopy(w, src, container, srcImageDesc, omeSynthetic, plan); err != nil {
		w.Abort()
		return err
	}
```
where `omeSynthetic := container == "ome-tiff" && src.Format() != string(opentile.FormatOMETIFF)` and `plan := omeEditPlan{dropAll: cvNoAssociated}` (preserves `--no-associated`). Move the `omeSynthetic` computation above the call. NOTE: the level loop currently calls `w.Abort()` inline on errors — inside `writeTIFFTileCopy` change those to `return fmt.Errorf(...)` (keep the `lh.Abort(err)`/`<-drainErr` cleanup); the single caller does `w.Abort()`.

- [ ] **Step 5: Build + non-regression**

Run: `go build ./...` (benign linker warning OK), `go vet ./cmd/wsitools/`.
Run convert tile-copy tests: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'Convert' -count=1` → PASS.
Pixel-exact spot check (ome-tiff + svs tile-copy unchanged):
```bash
go build -o /tmp/wsit ./cmd/wsitools
/tmp/wsit convert --to ome-tiff sample_files/svs/CMU-1-Small-Region.svs -o /tmp/a.ome.tiff --force 2>/dev/null
/tmp/wsit convert --to svs sample_files/svs/CMU-1-Small-Region.svs -o /tmp/a.svs --force 2>/dev/null
diff <(/tmp/wsit hash --mode pixel sample_files/svs/CMU-1-Small-Region.svs 2>/dev/null|cut -d' ' -f1) <(/tmp/wsit hash --mode pixel /tmp/a.ome.tiff 2>/dev/null|cut -d' ' -f1) && echo OME-PIXEL-OK
```
Expect `OME-PIXEL-OK` and the convert tests green.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert_tiff.go cmd/wsitools/associated_rebuild_ometiff.go
git commit -m "refactor(convert): extract writeTIFFTileCopy + thread omeEditPlan (no-op for convert)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `rebuildOMETIFF` + remove dispatch + always-on lossy warning

**Files:** modify `associated_rebuild_ometiff.go`, `associated.go`.

- [ ] **Step 1: Add `rebuildOMETIFF` + the warning + remove entry point**

In `associated_rebuild_ometiff.go`:

```go
import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// omeTIFFLossyWarning is emitted on EVERY OME-TIFF edit, regardless of flags.
const omeTIFFLossyWarning = "OME-TIFF editing rebuilds the file with a regenerated, minimal OME-XML — instrument, acquisition, channel, and vendor annotations are NOT preserved (pixels, geometry/MPP/magnification, and the other associated images are). wsitools' OME-TIFF support is rudimentary; for serious OME-TIFF work use Bio-Formats. See docs/ome-tiff-limitations.md."

func warnOMETIFFLossy() { slog.Warn(omeTIFFLossyWarning) }

// rebuildOMETIFF re-finalizes src as an OME-TIFF at outPath with plan applied,
// forcing a synthetic (minimal) OME-XML built from the plan-edited associated
// set. Writes a sibling temp then atomically renames (safe for --in-place).
func rebuildOMETIFF(src source.Source, outPath string, plan omeEditPlan, fsync bool) error {
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
	md := src.Metadata()
	l0 := src.Levels()[0]
	srcSoft := strings.TrimSpace(md.Make + " " + md.Model)
	// Forced synthetic OME-XML reflecting the edit.
	l0Desc := SyntheticOMEDescription(
		uint32(l0.Size().X), uint32(l0.Size().Y),
		md.MPP, md.MPP, "Image", srcSoft,
		omeAssociatedSpecs(src, plan),
	)
	opts := streamwriter.Options{
		SubResolutionPyramid: true,
		SampleFormat:         1,
	}
	if !md.AcquisitionDateTime.IsZero() {
		opts.DateTime = md.AcquisitionDateTime
	}
	if len(md.ICCProfile) > 0 {
		opts.ICCProfile = md.ICCProfile // confirm field name in streamwriter.Options
	}
	tmp := fmt.Sprintf("%s.tmp-%d", outPath, os.Getpid())
	w, err := streamwriter.Create(tmp, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	if err := writeTIFFTileCopy(w, src, "ome-tiff", l0Desc, true /*omeSynthetic*/, plan); err != nil {
		w.Abort()
		os.Remove(tmp)
		return err
	}
	if err := w.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("finalize output: %w", err)
	}
	if fsync {
		f, e := os.Open(tmp)
		if e != nil {
			os.Remove(tmp)
			return fmt.Errorf("fsync open: %w", e)
		}
		syncErr := f.Sync()
		closeErr := f.Close()
		if syncErr != nil {
			os.Remove(tmp)
			return fmt.Errorf("fsync: %w", syncErr)
		}
		if closeErr != nil {
			os.Remove(tmp)
			return fmt.Errorf("fsync close: %w", closeErr)
		}
	}
	if err := os.Rename(tmp, outPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func runAssociatedRemoveForOMETIFF(typ, input, outPath string, fl removeFlags) error {
	src, err := source.Open(input)
	if err != nil {
		return err
	}
	defer src.Close()
	found := false
	for _, a := range src.Associated() {
		if a.Type() == typ {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no %s image in slide", typ)
	}
	warnOMETIFFLossy()
	if err := rebuildOMETIFF(src, outPath, omeEditPlan{remove: typ}, fl.fsync); err != nil {
		return err
	}
	if !fl.quiet {
		fmt.Printf("wsitools: removed %s: %s -> %s\n", typ, input, outPath)
	}
	return nil
}
```

Confirm `streamwriter.Options` has an `ICCProfile` field; if not, drop that block (ICC carry for ome-tiff can be a follow-up — note it). Confirm `SyntheticOMEDescription` arg order against `ome_imagedesc.go`.

- [ ] **Step 2: Allow + dispatch ome-tiff in `associated.go`**

Add `opentile.FormatOMETIFF` (string `"ome-tiff"`) to `assocFormatSupported`. In `runAssociatedRemoveFor`, mirror the cog-wsi dispatch (single-close discipline): after `gateFormat`, before the splice path:
```go
	if src.Format() == "ome-tiff" {
		src.Close()
		return runAssociatedRemoveForOMETIFF(typ, input, outPath, fl)
	}
```
(Place alongside the existing `cog-wsi` branch; keep the close-exactly-once structure Task-2a established.)

- [ ] **Step 3: Gated integration test (synth fixture)**

Add to `associated_integration_test.go`:
```go
func ometiffFixture(t *testing.T) string {
	if p := firstExisting(t, "ome-tiff/Leica-1.ome.tiff", "ome-tiff/Leica-2.ome.tiff"); p != "" {
		return p
	}
	// Synthesize a small ome-tiff from the SVS fixture so tests run in CI.
	svs := firstExisting(t, "svs/CMU-1-Small-Region.svs", "svs/CMU-1.svs")
	if svs == "" {
		t.Skip("no ome-tiff or svs fixture")
	}
	src, err := source.Open(svs)
	if err != nil {
		t.Fatalf("open svs: %v", err)
	}
	defer src.Close()
	out := filepath.Join(t.TempDir(), "synth.ome.tiff")
	if err := rebuildOMETIFF(src, out, omeEditPlan{}, false); err != nil {
		t.Fatalf("synthesize ome-tiff: %v", err)
	}
	return out
}

func TestOMETIFFLabelRemove(t *testing.T) {
	in := copyFile(t, ometiffFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("fixture has no label")
	}
	out := filepath.Join(t.TempDir(), "out.ome.tiff")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveForOMETIFF("label", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("ome-tiff label remove: %v", err)
	}
	osrc, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if osrc.Format() != "ome-tiff" {
		t.Errorf("output format = %q, want ome-tiff", osrc.Format())
	}
	osrc.Close()
	if _, _, ok := assocOfType(t, out, "label"); ok {
		t.Errorf("label still present")
	}
	if _, _, ok := assocOfType(t, out, "overview"); !ok {
		t.Errorf("overview vanished (contract: only target changes)")
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed")
	}
}
```

(NOTE: the synth ome-tiff from CMU-1-Small-Region.svs carries label+thumbnail+overview. If `assocOfType` for `overview` differs, adjust to a type the synth fixture has.)

- [ ] **Step 4: Run + smoke-test**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'TestOMETIFFLabelRemove' -v` → PASS.
Smoke (warning visible): `/tmp/wsit convert --to ome-tiff sample_files/svs/CMU-1-Small-Region.svs -o /tmp/s.ome.tiff --force 2>/dev/null; /tmp/wsit label remove /tmp/s.ome.tiff -o /tmp/s2.ome.tiff 2>&1 | grep -i "rudimentary"` (expect the warning line), and `/tmp/wsit info /tmp/s2.ome.tiff 2>/dev/null | grep -iE "format|label|overview"` (no label).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_rebuild_ometiff.go cmd/wsitools/associated.go cmd/wsitools/associated_integration_test.go
git commit -m "feat(cmd): ome-tiff associated remove via streamwriter rebuild (lossy, warned)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: ome-tiff replace (image → StrippedSpec)

**Files:** modify `associated_replace.go`, `associated_rebuild_ometiff.go`, `associated.go`.

- [ ] **Step 1: Add `buildReplacementStrippedSpec`**

In `associated_replace.go`, add a packager that mirrors `writeAssociatedImages`'s spec construction but for a fresh image. Reuse the codec helpers (`encodeJPEGWhole`/`encodeLZWWhole`/`encodeDeflateWhole`/`rgbStripBytes`, default label→lzw else jpeg; resize via `fitImage`). Build a single-strip `StrippedSpec` (RowsPerStrip = height):

```go
func buildReplacementStrippedSpec(img image.Image, o replaceOpts) (*streamwriter.StrippedSpec, error) {
	codec := o.compression
	if codec == "" {
		if o.typ == "label" { codec = "lzw" } else { codec = "jpeg" }
	}
	if o.targetW != 0 || o.targetH != 0 {
		p, err := fitImage(img, o); if err != nil { return nil, err }; img = p
	}
	b := img.Bounds(); w, h := b.Dx(), b.Dy()
	var payload []byte; var comp uint16
	switch codec {
	case "jpeg":   buf, err := encodeJPEGWhole(img); if err != nil { return nil, err }; payload, comp = buf, 7
	case "lzw":    payload, comp = encodeLZWWhole(img), 5
	case "deflate":payload, comp = encodeDeflateWhole(img), 8
	case "none":   payload, comp = rgbStripBytes(img), 1
	default: return nil, fmt.Errorf("unknown compression %q", codec)
	}
	return &streamwriter.StrippedSpec{
		Width: uint32(w), Height: uint32(h), RowsPerStrip: uint32(h),
		BitsPerSample: []uint16{8,8,8}, SamplesPerPixel: 3, Photometric: 2,
		Compression: comp, StripBytes: payload,
		NewSubfileType: newSubfileTypeForAssoc("ome-tiff", o.typ),
		WSIImageType: o.typ,
	}, nil
}
```

(Confirm `image`/`fmt`/`streamwriter` imports. `newSubfileTypeForAssoc` is in convert_tiff.go.)

- [ ] **Step 2: Add `runAssociatedReplaceForOMETIFF`** (in `associated_rebuild_ometiff.go`), mirroring the cog-wsi replace entry: open src, find existing typ (dims; absent=upsert), `decodeReplacementImage` → `resolveTargetDims` → `parseHexColor(fl.bgHex)` → default resize "fit" → `buildReplacementStrippedSpec` → `warnOMETIFFLossy()` → `rebuildOMETIFF(src, outPath, omeEditPlan{replace: typ, spec: spec}, fl.fsync)` → print `replaced`/`added`.

- [ ] **Step 3: Dispatch replace** in `associated.go` `runAssociatedReplaceFor`: cog-wsi-style branch for `ome-tiff` before the splice/SVS-gate path.

- [ ] **Step 4: Build + integration test**

Add `TestOMETIFFOverviewReplaceRoundTrips` (mirror the cog-wsi replace test, using `ometiffFixture`, `writeSolidPNG`, `image.Decode` on the read-back `overview` bytes, assert pyramid digest unchanged; set `bgHex: "F5F5E6"`).
Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'TestOMETIFF|TestBuildReplacement|TestLabel|TestSVS|TestGenericTIFF|TestCOGWSI' -count=1 -v` → PASS (Slice-1/2a regression too).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_replace.go cmd/wsitools/associated_rebuild_ometiff.go cmd/wsitools/associated.go cmd/wsitools/associated_integration_test.go
git commit -m "feat(cmd): ome-tiff associated replace (image -> StrippedSpec)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: in-place + warning + unit tests

**Files:** extend `associated_integration_test.go`, `associated_rebuild_test.go`.

- [ ] **Step 1: In-place + warning-emitted + remove-absent tests**

```go
func TestOMETIFFRemoveInPlace(t *testing.T) {
	in := copyFile(t, ometiffFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok { t.Skip("no label") }
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveForOMETIFF("label", in, in, removeFlags{assocCommonFlags{inPlace: true, fsync: true}}); err != nil {
		t.Fatalf("in-place: %v", err)
	}
	if _, _, ok := assocOfType(t, in, "label"); ok { t.Errorf("label present after in-place remove") }
	if level0Digest(t, in) != digestBefore { t.Errorf("pyramid changed") }
	ents, _ := os.ReadDir(filepath.Dir(in))
	for _, e := range ents {
		if bytes.Contains([]byte(e.Name()), []byte(".tmp")) { t.Errorf("leftover temp %s", e.Name()) }
	}
}

func TestOMETIFFRemoveAbsentErrors(t *testing.T) {
	in := copyFile(t, ometiffFixture(t))
	err := runAssociatedRemoveForOMETIFF("no-such-type", in, filepath.Join(t.TempDir(), "o.ome.tiff"), removeFlags{assocCommonFlags{fsync: false}})
	if err == nil { t.Fatal("expected error for absent type") }
}
```

For the warning assertion, capture `slog` output: install a `slog` handler writing to a `bytes.Buffer` via `slog.SetDefault`, run a remove, assert the buffer contains "rudimentary" / "Bio-Formats", then restore. (Keep it isolated; restore the default handler with `t.Cleanup`.)

- [ ] **Step 2: Unit — omeAssociatedSpecs plan filtering** (no fixture)

In `associated_rebuild_test.go`, build a tiny fake `source.Source` (or reuse an existing fake) with associated types {label, overview}; assert `omeAssociatedSpecs(src, omeEditPlan{remove:"label"})` omits "label"; `omeEditPlan{replace:"label", spec:&StrippedSpec{Width:9,Height:9}}` yields a label entry with W/H 9; empty plan yields both. If constructing a fake `source.Source` is heavy, instead unit-test `buildReplacementStrippedSpec` codec/dims (like the cog-wsi spec test) and cover plan-filtering via the gated integration tests — note the choice.

- [ ] **Step 3: Full regression (uncontended)**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'TestOMETIFF|TestCOGWSI|TestLabel|TestSVS|TestGenericTIFF|TestBuildReplacement|TestApplyAssoc|TestUnsupported|TestConvert' -race -count=1 -timeout 30m` → PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/associated_integration_test.go cmd/wsitools/associated_rebuild_test.go
git commit -m "test(cmd): ome-tiff in-place + lossy-warning + plan-filtering

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Docs + surfacing

**Files:** create `docs/ome-tiff-limitations.md`; modify `README.md`, `CHANGELOG.md`, `docs/roadmap.md`.

- [ ] **Step 1: `docs/ome-tiff-limitations.md`** — new doc stating plainly:
  - wsitools' OME-TIFF support is **rudimentary/geometry-minimal**: the writer emits a minimal OME-XML (dimensions, MPP, magnification, one `<Image>` per pyramid/associated image) and does **not** model OME's multi-image/series structure, instrument, channels, planes, or annotations.
  - `convert --to ome-tiff` and `label|macro|… remove|replace` both **regenerate** the OME-XML → editing an OME-TIFF **discards** instrument/acquisition/channel/vendor metadata (pixels, geometry, and the other associated images are preserved).
  - **For serious OME-TIFF work, use [Bio-Formats](https://www.openmicroscopy.org/bio-formats/)** (the OME reference implementation; `bioformats2raw`+`raw2ometiff` for pyramids; `tifffile` for a lighter Python option).

- [ ] **Step 2: README** — add a short "OME-TIFF support is rudimentary" note (near conversion/editing) linking `docs/ome-tiff-limitations.md` + Bio-Formats. Matrix: OME-TIFF associated-editing cell `✓⁹`; footnote ⁹ = "lossy — regenerates a minimal OME-XML (instrument/vendor metadata not preserved); see [OME-TIFF limitations](docs/ome-tiff-limitations.md)."

- [ ] **Step 3: CHANGELOG `[Unreleased]`** — "Associated-image editing extended to **OME-TIFF** (remove + replace) via streamwriter rebuild. **Lossy:** regenerates a minimal OME-XML (instrument/acquisition/channel/vendor annotations not preserved; pixels, geometry/MPP/magnification, and other associated images are). Always-on warning + see docs/ome-tiff-limitations.md. wsitools' OME-TIFF support is rudimentary — use Bio-Formats for serious OME-TIFF work."

- [ ] **Step 4: `docs/roadmap.md`** — mark OME-TIFF (Slice 2b) DONE (lossy). The faithful OME-TIFF engine (real OME data model + raw IFD-graph re-serializer) remains a deferred/indefinite future item with the Bio-Formats recommendation as the interim answer. The associated-image-editing feature is now complete across SVS/generic-TIFF/COG-WSI/OME-TIFF.

- [ ] **Step 5: Verify + commit**

Run: `go build ./... && go test ./cmd/wsitools/ -run 'TestAssociated|TestResolveAssoc' -count=1` → PASS.
```bash
git add docs/ome-tiff-limitations.md README.md CHANGELOG.md docs/roadmap.md
git commit -m "docs: OME-TIFF associated editing (lossy) + rudimentary-support note + Bio-Formats pointer

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final review

After all tasks, dispatch a final code reviewer for the branch (focus: writeTIFFTileCopy extraction preserves convert byte/pixel-exactly incl. the async drain; the lossy warning fires on every edit; rebuild atomic temp→rename correct for --in-place; replace round-trips; contract held — only the target associated image changes, pyramid identical). Then use `superpowers:finishing-a-development-branch`. Run the heavy `cmd/wsitools` suite uncontended with `-timeout 30m`.

## Self-review notes (author)

- **Spec coverage:** extraction + plan threading (Task 1), rebuild + remove + always-on warning (Task 2), replace (Task 3), in-place/warning/unit (Task 4), docs + surfacing incl. Bio-Formats (Task 5). Lossy contract + surfacing covered explicitly.
- **Type consistency:** `omeEditPlan{remove,replace,spec *streamwriter.StrippedSpec,dropAll}`, `writeTIFFTileCopy(w,src,container,l0Desc,omeSynthetic,plan)`, `rebuildOMETIFF(src,outPath,plan,fsync)`, `buildReplacementStrippedSpec(img,replaceOpts)→*streamwriter.StrippedSpec`. Reuses `omeAssociatedSpecs`/`writeAssociatedImages` (now plan-aware), `SyntheticOMEDescription`, `newSubfileTypeForAssoc`, Slice-1/2a image+flag helpers, and Slice-2a test helpers (`firstExisting`, `copyFile`, `assocOfType`, `level0Digest`, `writeSolidPNG`).
- **Non-regression first:** Task 1 keeps `convert --to ome-tiff|svs|tiff` byte/pixel-exact (the async-drain loop ported verbatim; convert passes no-op plan) before any new behavior.
- **Flagged confirmations for the implementer:** `streamwriter.Options.ICCProfile` field name (drop the block if absent — note as a follow-up); `SyntheticOMEDescription` exact arg order; the synth fixture's associated types.
