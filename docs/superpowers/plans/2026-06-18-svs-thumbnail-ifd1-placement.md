# SVS thumbnail IFD-1 placement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the SVS write path emit the thumbnail at IFD 1 on multi-level slides, fixing `convert --to svs` (which silently drops thumbnail+overview) and unblocking `associated thumbnail replace/remove` on multi-level SVS.

**Architecture:** IFD order equals `AddLevel`/`AddStripped` call order (`w.imgs`). Inject the SVS thumbnail's `AddStripped` right after L0 in both convert level-loops (tile-copy `writeTIFFTileCopy` and re-encode `transcodePyramid`), and have `writeAssociatedImages` skip the SVS thumbnail (already emitted at IFD 1). Re-add `rebuildSVS` so the associated-edit fallback goes through the now-correct writer. SVS-only; no streamwriter change.

**Tech Stack:** Go; `cmd/wsitools/convert_tiff.go`, `cmd/wsitools/associated*.go`; `internal/tiff/streamwriter`; cobra CLI; integration tests gated on `WSI_TOOLS_TESTDIR`.

**Spec:** `docs/superpowers/specs/2026-06-18-svs-thumbnail-ifd1-placement-design.md`

---

## File Structure

- `cmd/wsitools/convert_tiff.go` — extract `emitOneAssociated`; add `emitSVSThumbnailAtL0`; rewrite `writeAssociatedImages` to skip the SVS thumbnail and reuse `emitOneAssociated`; inject the thumbnail after L0 in `writeTIFFTileCopy` and `transcodePyramid`; thread `plan`/`omeSynthetic` into `transcodePyramid`.
- `cmd/wsitools/associated_rebuild_tiff.go` — extract shared `baseRebuildOpts` + `finalizeRebuild`; refactor `rebuildGenericTIFF` onto them.
- `cmd/wsitools/associated_rebuild_svs.go` — **new**: `rebuildSVS`.
- `cmd/wsitools/associated.go` — wire SVS `edit.ErrUnexpectedLayout` to `rebuildSVS` in `runAssociatedRemoveFor` and `runAssociatedReplaceFor`.
- `cmd/wsitools/associated_integration_test.go` — new multi-level SVS integration tests + a `multiLevelSVSFixture` helper.
- `.github/fixtures.sha256` + `wsi-fixtures` — add `239551.svs` (owner action for the release tar).

---

## Task 1: Local multi-level SVS fixture setup

Makes the new integration tests runnable locally before the CI fixture lands. `239551.svs` is the owner's own scan (placeable in wsi-fixtures).

**Files:**
- Copy into the fixture pool that `WSI_TOOLS_TESTDIR` points at.

- [ ] **Step 1: Copy the slide into the local SVS fixture dir**

Run:
```bash
cp /Volumes/Storage/wsi_sample_files/239551.svs "$(cd /Volumes/Ext/GitHub/wsitools && pwd)/sample_files/svs/239551.svs"
```

- [ ] **Step 2: Verify it reads as a multi-level SVS with the three associated images**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && make build && ./bin/wsitools info sample_files/svs/239551.svs | grep -iE 'format|thumbnail|label|overview'
```
Expected: `Format:  svs`, and associated `thumbnail`, `label`, `overview` all listed.

- [ ] **Step 3: Confirm the bug reproduces (baseline)**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && ./bin/wsitools convert --to svs -f -o /tmp/239551-base.svs sample_files/svs/239551.svs && ./bin/wsitools info /tmp/239551-base.svs | grep -iE 'thumbnail|label|overview'; rm -f /tmp/239551-base.svs
```
Expected: only `label` is listed (thumbnail + overview dropped) — the bug, pre-fix.

No commit (fixture file is gitignored via the `sample_files` symlink).

---

## Task 2: Extract `emitOneAssociated` (behavior-preserving refactor)

Pulls the per-image body of `writeAssociatedImages` into a reusable function so the IFD-1 injection (Task 3) and the tail pass share one code path. Pure refactor — guarded by the existing convert/associated suite.

**Files:**
- Modify: `cmd/wsitools/convert_tiff.go` (`writeAssociatedImages`, currently lines 745-814)

- [ ] **Step 1: Add `emitOneAssociated` above `writeAssociatedImages`**

Insert this function immediately before `func writeAssociatedImages`:

```go
// emitOneAssociated emits associated image `a` as a single stripped IFD, applying
// the edit plan and the container's classification tags. Returns whether an IFD
// was written (false when the plan removes it, an OME-unmapped type is filtered
// under synthetic OME output, or the codec can't be faithfully copied). The
// caller owns plan.replace bookkeeping (the `replaced` flag) — this helper does
// not track it.
func emitOneAssociated(src source.Source, w *streamwriter.Writer, a source.AssociatedImage, container string, omeSynthetic bool, plan omeEditPlan) (bool, error) {
	if plan.remove != "" && a.Type() == plan.remove {
		return false, nil
	}
	if plan.replace != "" && a.Type() == plan.replace {
		// Under synthetic OME output, an OME-unmapped type emits no <Image>, so
		// it must emit no IFD either (else OME-XML and IFDs desync).
		if container == "ome-tiff" && omeSynthetic && omeAssocName(plan.replace) == "" {
			return false, nil
		}
		if err := w.AddStripped(*plan.spec); err != nil {
			return false, fmt.Errorf("write associated %s: %w", a.Type(), err)
		}
		return true, nil
	}
	if container == "ome-tiff" && omeSynthetic && omeAssocName(a.Type()) == "" {
		return false, nil
	}
	spec, err := faithfulStrippedSpec(a)
	if err != nil {
		if errors.Is(err, errSkipAssociated) {
			slog.Warn("skipping associated", "type", a.Type(), "reason", err)
			return false, nil
		}
		return false, fmt.Errorf("associated %s: %w", a.Type(), err)
	}
	spec.BitsPerSample = []uint16{8, 8, 8}
	spec.NewSubfileType = newSubfileTypeForAssoc(container, a.Type())
	spec.WSIImageType = a.Type()
	// SVS-shaped output: emit Aperio-flavored NewSubfileType via ExtraTags
	// (macro=9, label=1). Clear spec.NewSubfileType so the writer doesn't also
	// emit a default value — EntryBuilder doesn't dedup, so a duplicate tag
	// would corrupt the IFD.
	if container == "svs" {
		switch a.Type() {
		case "macro", "overview":
			spec.NewSubfileType = 0
			spec.ExtraTags = buildSVSMacroExtraTags()
		case "label":
			spec.NewSubfileType = 0
			spec.ExtraTags = buildSVSLabelExtraTags()
		}
	}
	if err := w.AddStripped(spec); err != nil {
		return false, fmt.Errorf("write associated %s: %w", a.Type(), err)
	}
	return true, nil
}
```

- [ ] **Step 2: Rewrite `writeAssociatedImages` to use it**

Replace the entire body of `writeAssociatedImages` (lines 745-814) with:

```go
func writeAssociatedImages(src source.Source, w *streamwriter.Writer, container string, omeSynthetic bool, plan omeEditPlan) error {
	if plan.dropAll {
		return nil
	}
	replaced := false
	for _, a := range src.Associated() {
		if plan.replace != "" && a.Type() == plan.replace {
			replaced = true
		}
		if _, err := emitOneAssociated(src, w, a, container, omeSynthetic, plan); err != nil {
			return err
		}
	}
	// Upsert: plan.replace was not present in the source set. Skip the IFD for
	// an OME-unmapped type under synthetic output (omeAssociatedSpecs omits its
	// <Image> too), keeping OME-XML and IFDs in sync.
	if plan.replace != "" && !replaced && !(container == "ome-tiff" && omeSynthetic && omeAssocName(plan.replace) == "") {
		if err := w.AddStripped(*plan.spec); err != nil {
			return fmt.Errorf("write associated %s: %w", plan.replace, err)
		}
	}
	return nil
}
```

- [ ] **Step 3: Build and run the convert + associated suites (refactor must be transparent)**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && go build ./... 2>&1 | grep -v 'ld: warning' ; WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run 'Convert|Associated|Replace|Label|Macro|Overview|Thumbnail|AssocMarkers|Ome' -count=1 2>&1 | tail -3
```
Expected: `ok  github.com/wsilabs/wsitools/cmd/wsitools` (all green; behavior unchanged).

- [ ] **Step 4: Commit**

```bash
cd /Volumes/Ext/GitHub/wsitools && git add cmd/wsitools/convert_tiff.go && git commit -m "refactor(convert): extract emitOneAssociated from writeAssociatedImages"
```

---

## Task 3: Inject the SVS thumbnail at IFD 1 (tile-copy path)

**Files:**
- Modify: `cmd/wsitools/convert_tiff.go` (`writeTIFFTileCopy` level loop ~601-678; `writeAssociatedImages`)
- Test: `cmd/wsitools/associated_integration_test.go`

- [ ] **Step 1: Add the `multiLevelSVSFixture` helper + the failing integration test**

Append to `cmd/wsitools/associated_integration_test.go`:

```go
// multiLevelSVSFixture returns a multi-level SVS (tiled pyramid levels follow the
// thumbnail at IFD 1), or skips. 239551.svs is JPEG-tiled with 3 levels +
// thumbnail/label/overview.
func multiLevelSVSFixture(t *testing.T) string {
	p := firstExisting(t, "svs/239551.svs")
	if p == "" {
		t.Skip("no multi-level SVS fixture (svs/239551.svs)")
	}
	return p
}

// TestConvertToSVSMultiLevelKeepsThumbnail guards the IFD-1 placement fix: a
// multi-level SVS converted via the tile-copy path must keep the thumbnail and
// overview (they were dropped when the thumbnail stranded after the pyramid).
func TestConvertToSVSMultiLevelKeepsThumbnail(t *testing.T) {
	bin := stripedBinary(t)
	in := multiLevelSVSFixture(t)
	for _, ty := range []string{"thumbnail", "overview", "label"} {
		if _, _, ok := assocOfType(t, in, ty); !ok {
			t.Skipf("fixture lacks %s", ty)
		}
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	if o, err := runBin(bin, "convert", "--to", "svs", "-f", "-o", out, in); err != nil {
		t.Fatalf("convert --to svs: %v\n%s", err, o)
	}
	info, _ := runBin(bin, "info", out)
	for _, ty := range []string{"thumbnail", "label", "overview"} {
		if !strings.Contains(string(info), ty) {
			t.Errorf("converted multi-level SVS dropped %s:\n%s", ty, info)
		}
	}
	// Pyramid must be pixel-identical (verbatim tile-copy).
	if ds, db := pixelDigest(mustRun(t, bin, "hash", "--mode", "pixel", in)), pixelDigest(mustRun(t, bin, "hash", "--mode", "pixel", out)); ds == "" || ds != db {
		t.Errorf("pyramid pixels changed: src=%s out=%s", ds, db)
	}
}

func mustRun(t *testing.T, bin string, args ...string) []byte {
	t.Helper()
	out, _ := runBin(bin, args...)
	return out
}
```

Note: if `stripedBinary`, `runBin`, `pixelDigest` are not visible from this test file's package, they are defined in `cmd/wsitools/*_test.go` (same package `main`) — confirm with `grep -rn "func stripedBinary\|func runBin\|func pixelDigest" cmd/wsitools/*_test.go`. If `mustRun` already exists, drop the duplicate definition.

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run TestConvertToSVSMultiLevelKeepsThumbnail -count=1 -v 2>&1 | grep -E 'PASS|FAIL|SKIP'
```
Expected: FAIL — "converted multi-level SVS dropped thumbnail" (and overview).

- [ ] **Step 3: Add `emitSVSThumbnailAtL0`**

Insert this function immediately after `emitOneAssociated` in `cmd/wsitools/convert_tiff.go`:

```go
// emitSVSThumbnailAtL0 emits the SVS thumbnail as IFD 1, called right after L0 in
// each level loop. opentile classifies the SVS thumbnail positionally (page 1,
// non-tiled), so on a multi-level slide it must precede L1. No-op unless
// container=="svs" && lvlIndex==0. Honors the plan via emitOneAssociated
// (dropAll/remove emit nothing; replace emits plan.spec). Handles the upsert
// (replace a thumbnail the source lacks). Returns whether an IFD was emitted.
func emitSVSThumbnailAtL0(src source.Source, w *streamwriter.Writer, lvlIndex int, container string, omeSynthetic bool, plan omeEditPlan) (bool, error) {
	if container != "svs" || lvlIndex != 0 || plan.dropAll {
		return false, nil
	}
	for _, a := range src.Associated() {
		if a.Type() == "thumbnail" {
			return emitOneAssociated(src, w, a, container, omeSynthetic, plan)
		}
	}
	// Upsert: source has no thumbnail but the plan replaces (adds) one.
	if plan.replace == "thumbnail" {
		if err := w.AddStripped(*plan.spec); err != nil {
			return false, fmt.Errorf("write thumbnail: %w", err)
		}
		return true, nil
	}
	return false, nil
}
```

- [ ] **Step 4: Call it after each level in `writeTIFFTileCopy`**

In `writeTIFFTileCopy`, the level loop ends at the `}` closing `for _, lvl := range src.Levels()` (just before `if err := writeAssociatedImages(...)`). Add the injection as the last statement inside that loop, right after the existing `if err := <-drainErr; err != nil { ... }` block:

```go
		if err := <-drainErr; err != nil {
			return fmt.Errorf("drain level %d: %w", lvl.Index(), err)
		}
		if _, err := emitSVSThumbnailAtL0(src, w, lvl.Index(), container, omeSynthetic, plan); err != nil {
			return err
		}
	}
```

- [ ] **Step 5: Make `writeAssociatedImages` skip the SVS thumbnail**

In `writeAssociatedImages` (from Task 2), change the loop and the upsert guard to skip the SVS thumbnail (already emitted at IFD 1 or intentionally dropped):

```go
	replaced := false
	for _, a := range src.Associated() {
		if container == "svs" && a.Type() == "thumbnail" {
			if plan.replace == "thumbnail" {
				replaced = true // handled at IFD 1 by emitSVSThumbnailAtL0
			}
			continue
		}
		if plan.replace != "" && a.Type() == plan.replace {
			replaced = true
		}
		if _, err := emitOneAssociated(src, w, a, container, omeSynthetic, plan); err != nil {
			return err
		}
	}
	// Upsert: plan.replace absent from source. Skip OME-unmapped synthetic types
	// and the SVS thumbnail (the latter is upserted at IFD 1 by emitSVSThumbnailAtL0).
	if plan.replace != "" && !replaced &&
		!(container == "ome-tiff" && omeSynthetic && omeAssocName(plan.replace) == "") &&
		!(container == "svs" && plan.replace == "thumbnail") {
		if err := w.AddStripped(*plan.spec); err != nil {
			return fmt.Errorf("write associated %s: %w", plan.replace, err)
		}
	}
	return nil
```

- [ ] **Step 6: Run the test to verify it passes + the existing single-level tests stay green**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && go build ./... 2>&1 | grep -v 'ld: warning' ; WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run 'TestConvertToSVSMultiLevelKeepsThumbnail|TestConvertToSVS|TestSVSOverviewReplaceWorks|TestSVSThumbnailReplaceSingleLevel' -count=1 -v 2>&1 | grep -E 'PASS|FAIL|SKIP'
```
Expected: `TestConvertToSVSMultiLevelKeepsThumbnail` PASS; single-level tests still PASS.

- [ ] **Step 7: Commit**

```bash
cd /Volumes/Ext/GitHub/wsitools && git add cmd/wsitools/convert_tiff.go cmd/wsitools/associated_integration_test.go && git commit -m "fix(convert): place SVS thumbnail at IFD 1 (tile-copy path)"
```

---

## Task 4: Inject the SVS thumbnail at IFD 1 (re-encode path)

`transcodePyramid` (the `--codec` path) also tail-places associated images. Thread the plan/omeSynthetic through and inject after L0.

**Files:**
- Modify: `cmd/wsitools/convert_tiff.go` (`transcodePyramid` ~425-432; `runConvertTIFFReencode` ~335-346)
- Test: `cmd/wsitools/associated_integration_test.go`

- [ ] **Step 1: Add a failing re-encode integration test**

Append to `cmd/wsitools/associated_integration_test.go`:

```go
// TestConvertToSVSMultiLevelReencodeKeepsThumbnail: the --codec re-encode path
// must also keep the thumbnail+overview on a multi-level SVS.
func TestConvertToSVSMultiLevelReencodeKeepsThumbnail(t *testing.T) {
	bin := stripedBinary(t)
	in := multiLevelSVSFixture(t)
	for _, ty := range []string{"thumbnail", "overview"} {
		if _, _, ok := assocOfType(t, in, ty); !ok {
			t.Skipf("fixture lacks %s", ty)
		}
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	if o, err := runBin(bin, "convert", "--to", "svs", "--codec", "jpeg", "-f", "-o", out, in); err != nil {
		t.Fatalf("convert --to svs --codec jpeg: %v\n%s", err, o)
	}
	info, _ := runBin(bin, "info", out)
	for _, ty := range []string{"thumbnail", "label", "overview"} {
		if !strings.Contains(string(info), ty) {
			t.Errorf("re-encoded multi-level SVS dropped %s:\n%s", ty, info)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run TestConvertToSVSMultiLevelReencodeKeepsThumbnail -count=1 -v 2>&1 | grep -E 'PASS|FAIL|SKIP'
```
Expected: FAIL — "re-encoded multi-level SVS dropped thumbnail".

- [ ] **Step 3: Thread `plan`/`omeSynthetic` into `transcodePyramid` and inject**

Replace `transcodePyramid` (lines 425-432) with:

```go
func transcodePyramid(ctx context.Context, src source.Source, w *streamwriter.Writer, fac codec.EncoderFactory, knobs map[string]string, workers int, container, srcImageDesc string, plan omeEditPlan, omeSynthetic bool) error {
	for _, lvl := range src.Levels() {
		if err := transcodeLevel(ctx, lvl, w, fac, knobs, workers, container, srcImageDesc); err != nil {
			return fmt.Errorf("level %d: %w", lvl.Index(), err)
		}
		if _, err := emitSVSThumbnailAtL0(src, w, lvl.Index(), container, omeSynthetic, plan); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Update the caller in `runConvertTIFFReencode`**

Replace lines 335-346 (the `transcodePyramid` call through the `if !cvNoAssociated { … }` block) with:

```go
	omeSynthetic := resolvedContainer == "ome-tiff" && src.Format() != string(opentile.FormatOMETIFF)
	if err := transcodePyramid(cmd.Context(), src, w, fac, knobs, workers, resolvedContainer, srcImageDesc, omeEditPlan{dropAll: cvNoAssociated}, omeSynthetic); err != nil {
		w.Abort()
		return err
	}

	if !cvNoAssociated {
		if err := writeAssociatedImages(src, w, resolvedContainer, omeSynthetic, omeEditPlan{}); err != nil {
			w.Abort()
			return err
		}
	}
```

(The SVS thumbnail is emitted at IFD 1 inside `transcodePyramid`; the `writeAssociatedImages` call skips it for SVS. `omeEditPlan{dropAll: cvNoAssociated}` makes the IFD-1 injection honor `--no-associated`.)

- [ ] **Step 5: Run the test + the JP2K-source re-encode path (which also hits this) **

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && go build ./... 2>&1 | grep -v 'ld: warning' ; WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run 'TestConvertToSVSMultiLevelReencodeKeepsThumbnail|Reencode|Codec' -count=1 -v 2>&1 | grep -E 'PASS|FAIL|SKIP'
```
Expected: `TestConvertToSVSMultiLevelReencodeKeepsThumbnail` PASS; existing re-encode/codec tests PASS.

- [ ] **Step 6: Commit**

```bash
cd /Volumes/Ext/GitHub/wsitools && git add cmd/wsitools/convert_tiff.go cmd/wsitools/associated_integration_test.go && git commit -m "fix(convert): place SVS thumbnail at IFD 1 (re-encode path)"
```

---

## Task 5: Re-add `rebuildSVS` and wire the associated-edit fallback

With the writer now placing the thumbnail at IFD 1, the multi-level thumbnail `replace`/`remove` rebuild produces a correctly-classified file.

**Files:**
- Modify: `cmd/wsitools/associated_rebuild_tiff.go` (extract `baseRebuildOpts` + `finalizeRebuild`)
- Create: `cmd/wsitools/associated_rebuild_svs.go`
- Modify: `cmd/wsitools/associated.go` (`runAssociatedRemoveFor` ~211-227; `runAssociatedReplaceFor` ~341-374)
- Test: `cmd/wsitools/associated_integration_test.go`

- [ ] **Step 1: Add failing replace + remove integration tests**

Append to `cmd/wsitools/associated_integration_test.go`:

```go
// TestSVSMultiLevelThumbnailReplace: replacing the thumbnail on a multi-level SVS
// (which can't splice) goes through the rebuild and lands the new thumbnail at
// IFD 1, classified correctly; pyramid pixel-identical; label/overview intact.
func TestSVSMultiLevelThumbnailReplace(t *testing.T) {
	in := copyFile(t, multiLevelSVSFixture(t))
	origSize, origBytes, ok := assocOfType(t, in, "thumbnail")
	if !ok {
		t.Skip("fixture has no thumbnail")
	}
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, origSize.X, origSize.Y, color.RGBA{10, 20, 30, 255})
	out := filepath.Join(t.TempDir(), "out.svs")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedReplaceFor("thumbnail", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, resize: "fit", bgHex: "F5F5E6", force: true,
	}); err != nil {
		t.Fatalf("thumbnail replace: %v", err)
	}
	_, newBytes, ok := assocOfType(t, out, "thumbnail")
	if !ok {
		t.Fatalf("thumbnail missing/unclassified after multi-level replace")
	}
	if bytes.Equal(origBytes, newBytes) {
		t.Errorf("thumbnail content unchanged after replace")
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed after thumbnail replace")
	}
	for _, ty := range []string{"label", "overview"} {
		if _, _, ok := assocOfType(t, out, ty); !ok {
			t.Errorf("%s vanished after thumbnail replace", ty)
		}
	}
}

// TestSVSMultiLevelThumbnailRemove: removing the thumbnail on a multi-level SVS
// succeeds via rebuild; thumbnail gone, label/overview kept, pyramid intact.
func TestSVSMultiLevelThumbnailRemove(t *testing.T) {
	in := copyFile(t, multiLevelSVSFixture(t))
	if _, _, ok := assocOfType(t, in, "thumbnail"); !ok {
		t.Skip("fixture has no thumbnail")
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveFor("thumbnail", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("thumbnail remove: %v", err)
	}
	if _, _, ok := assocOfType(t, out, "thumbnail"); ok {
		t.Errorf("thumbnail still present after remove")
	}
	for _, ty := range []string{"label", "overview"} {
		if _, _, ok := assocOfType(t, out, ty); !ok {
			t.Errorf("%s vanished after thumbnail remove", ty)
		}
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed after thumbnail remove")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run 'TestSVSMultiLevelThumbnail' -count=1 -v 2>&1 | grep -E 'PASS|FAIL|SKIP'
```
Expected: both FAIL with the current `ErrUnsupportedAssoc` (replace) / raw layout error (remove).

- [ ] **Step 3: Extract `baseRebuildOpts` + `finalizeRebuild`; refactor `rebuildGenericTIFF`**

Replace the whole body of `cmd/wsitools/associated_rebuild_tiff.go` with:

```go
package main

import (
	"fmt"
	"os"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// baseRebuildOpts builds the streamwriter.Options shared by the TIFF-family
// associated-edit rebuild fallbacks (generic-TIFF, SVS): row-major tile order,
// carried MPP/mag/ICC/Make/Model/Software/DateTime, container FormatName /
// AcceptedOrders. Container-specific fields (ImageDescription, SVS L0 conformance
// tags) are set by the caller.
func baseRebuildOpts(src source.Source, formatName string) (streamwriter.Options, error) {
	order, err := tileorder.ByName("row-major")
	if err != nil {
		return streamwriter.Options{}, fmt.Errorf("tile order: %w", err)
	}
	md := src.Metadata()
	opts := streamwriter.Options{
		BigTIFF:        resolveBigTIFFMode("auto", src),
		ToolsVersion:   Version,
		SourceFormat:   src.Format(),
		FormatName:     formatName,
		AcceptedOrders: acceptedOrdersForFormat(formatName),
		DefaultOrder:   order,
		MPPX:           md.MPPX,
		MPPY:           md.MPPY,
		Magnification:  md.Magnification,
		ICCProfile:     md.ICCProfile,
	}
	if md.Make != "" {
		opts.Make = md.Make
	}
	if md.Model != "" {
		opts.Model = md.Model
	}
	if md.Software != "" {
		opts.Software = md.Software
	}
	if !md.AcquisitionDateTime.IsZero() {
		opts.DateTime = md.AcquisitionDateTime
	}
	return opts, nil
}

// finalizeRebuild re-finalizes src at outPath via the streamwriter with the
// associated-edit plan applied, tile-copying the pyramid verbatim (pixel-
// identical). Writes a sibling temp then atomically renames (safe for
// --in-place). Shared by the generic-TIFF and SVS rebuild fallbacks.
func finalizeRebuild(src source.Source, outPath, container, l0Desc string, omeSynthetic bool, opts streamwriter.Options, plan omeEditPlan, fsync bool) error {
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
	tmp := fmt.Sprintf("%s.tmp-%d", outPath, os.Getpid())
	w, err := streamwriter.Create(tmp, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	if err := writeTIFFTileCopy(w, src, container, l0Desc, omeSynthetic, plan); err != nil {
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

// rebuildGenericTIFF re-finalizes src as a generic-TIFF at outPath with the
// associated-edit plan applied. Fallback for associated remove/replace when the
// in-place splice engine can't handle the source's byte layout — notably a
// wsitools-produced generic-TIFF whose streamwriter layout puts the L0 directory
// past the splice "cutoff". generic-TIFF carries no OME-XML, so nothing
// descriptive is lost; only byte offsets change.
func rebuildGenericTIFF(src source.Source, outPath string, plan omeEditPlan, fsync bool) error {
	opts, err := baseRebuildOpts(src, "tiff")
	if err != nil {
		return err
	}
	opts.ImageDescription = buildProvenanceDesc(src, "associated-edit", src.Metadata())
	return finalizeRebuild(src, outPath, "tiff", "" /*l0Desc*/, false /*omeSynthetic*/, opts, plan, fsync)
}
```

- [ ] **Step 4: Create `rebuildSVS`**

Create `cmd/wsitools/associated_rebuild_svs.go`:

```go
package main

import (
	qualityjpeg "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
)

// rebuildSVS re-finalizes src as an SVS at outPath with the associated-edit plan
// applied, tile-copying the pyramid verbatim (pixel-identical). Fallback for SVS
// associated remove/replace when the in-place splice can't handle the layout —
// specifically the thumbnail, which Aperio stores at IFD 1 (before the tiled
// pyramid). writeTIFFTileCopy now re-emits the thumbnail at IFD 1, so the rebuilt
// file classifies it correctly. The Aperio L0 ImageDescription is carried
// verbatim so the output re-detects as SVS; ImageDepth/YCbCrSubSampling mirror
// runConvertTIFFTileCopy's svs branch.
func rebuildSVS(src source.Source, outPath string, plan omeEditPlan, fsync bool) error {
	opts, err := baseRebuildOpts(src, "svs")
	if err != nil {
		return err
	}
	opts.ImageDepth = 1
	l0 := src.Levels()[0]
	if compressionTagFor(l0.Compression()) == tiff.CompressionJPEG {
		buf := make([]byte, l0.TileMaxSize())
		if n, terr := l0.TileInto(0, 0, buf); terr == nil {
			if h, v, ok := qualityjpeg.LumaSampling(buf[:n]); ok {
				opts.YCbCrSubSampling = []uint16{h, v}
			}
		}
	}
	l0Desc := src.SourceImageDescription()
	return finalizeRebuild(src, outPath, "svs", l0Desc, false /*omeSynthetic*/, opts, plan, fsync)
}
```

- [ ] **Step 5: Wire the SVS remove fallback in `associated.go`**

In `runAssociatedRemoveFor`, replace the splice error handler (the `if src.Format() == string(opentile.FormatGenericTIFF) && errors.Is(err, edit.ErrUnexpectedLayout) { … } return err` block, ~lines 217-226) with:

```go
		if errors.Is(err, edit.ErrUnexpectedLayout) {
			var rerr error
			switch src.Format() {
			case string(opentile.FormatGenericTIFF):
				rerr = rebuildGenericTIFF(src, outPath, omeEditPlan{remove: typ}, fl.fsync)
			case "svs":
				rerr = rebuildSVS(src, outPath, omeEditPlan{remove: typ}, fl.fsync)
			default:
				return err
			}
			if rerr != nil {
				return rerr
			}
			if !fl.quiet {
				fmt.Printf("wsitools: removed %s: %s -> %s\n", typ, input, outPath)
			}
			return nil
		}
		return err
```

- [ ] **Step 6: Wire the SVS replace fallback in `associated.go`**

In `runAssociatedReplaceFor`, replace the splice error handler (the SVS `ErrUnsupportedAssoc` early-return block + the generic-TIFF rebuild block, ~lines 342-373) with:

```go
		if errors.Is(err, edit.ErrUnexpectedLayout) &&
			(src.Format() == string(opentile.FormatGenericTIFF) || src.Format() == "svs") {
			spec, serr := buildReplacementStrippedSpec(img, replaceOpts{
				typ:         typ,
				compression: fl.compression,
				resize:      resize,
				bg:          bg,
				targetW:     tw,
				targetH:     th,
				force:       fl.force,
			})
			if serr != nil {
				return serr
			}
			plan := omeEditPlan{replace: typ, spec: spec}
			var rerr error
			if src.Format() == "svs" {
				rerr = rebuildSVS(src, outPath, plan, fl.fsync)
			} else {
				rerr = rebuildGenericTIFF(src, outPath, plan, fl.fsync)
			}
			if rerr != nil {
				return rerr
			}
			if !fl.quiet {
				fmt.Printf("wsitools: %s %s: %s -> %s\n", verb, typ, input, outPath)
			}
			return nil
		}
		return err
```

- [ ] **Step 7: Run the new tests + the single-level + generic-TIFF regression tests**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && go build ./... 2>&1 | grep -v 'ld: warning' ; WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -run 'TestSVSMultiLevelThumbnail|TestSVSOverviewReplaceWorks|TestSVSThumbnailReplaceSingleLevel|TestLabelReplace|Rebuild|GenericTIFF' -count=1 -v 2>&1 | grep -E 'PASS|FAIL|SKIP'
```
Expected: both `TestSVSMultiLevelThumbnail*` PASS; all single-level + generic-TIFF tests PASS.

- [ ] **Step 8: Commit**

```bash
cd /Volumes/Ext/GitHub/wsitools && git add cmd/wsitools/associated_rebuild_tiff.go cmd/wsitools/associated_rebuild_svs.go cmd/wsitools/associated.go cmd/wsitools/associated_integration_test.go && git commit -m "fix(associated): support multi-level SVS thumbnail replace/remove via rebuildSVS (C4 #2)"
```

---

## Task 6: CI fixture + docs

**Files:**
- Modify: `.github/fixtures.sha256`
- Owner action: `wsi-fixtures` `svs.tar` (add `239551.svs`, cut a release)
- Modify: `docs/format-debt-survey-2026-06-13.md` (D3 + C4 #2)

- [ ] **Step 1: Compute the fixture SHA-256 and add it to `fixtures.sha256`**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && shasum -a 256 sample_files/svs/239551.svs | awk '{print $1"  svs/239551.svs"}'
```
Add that exact line to `.github/fixtures.sha256` (alphabetically within the `svs/` block).

- [ ] **Step 2: Note the owner action**

`239551.svs` must be added to the `wsi-fixtures` `svs.tar` and a new release cut (owner owns the repo + the slide). Until then CI pulls won't contain it and the multi-level tests **skip** in CI (they gate on `svs/239551.svs` via `multiLevelSVSFixture`). The `.github/fixtures.sha256` line is harmless before the tar lands — the CI verify step only checks files that were pulled. Confirm the CI verify logic tolerates an extra sha line:
```bash
cd /Volumes/Ext/GitHub/wsitools && grep -n "fixtures.sha256\|sha256 -c\|shasum" .github/workflows/ci.yml | head
```
If the CI step does a strict `shasum -c` over the whole file (which fails on a missing file), gate the new line behind the tar landing — i.e. add the sha line in the SAME change that ships the tar. Otherwise (verify only the pulled set) the line can land now.

- [ ] **Step 3: Mark D3 + C4 #2 in the survey**

In `docs/format-debt-survey-2026-06-13.md`, update the **D3** row to note the multi-level SVS fixture (`239551.svs`) was added, and add a line under **C4** that #2 (multi-level SVS thumbnail replace) is DONE via `rebuildSVS` + the IFD-1 placement fix.

- [ ] **Step 4: Commit**

```bash
cd /Volumes/Ext/GitHub/wsitools && git add .github/fixtures.sha256 docs/format-debt-survey-2026-06-13.md && git commit -m "test(fixtures): add multi-level SVS 239551.svs; mark D3 + C4 #2 done"
```

---

## Task 7: Final verification

- [ ] **Step 1: Full `cmd/wsitools` suite (uncontended, 30m timeout per the race-suite gotcha)**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && WSI_TOOLS_TESTDIR="$(pwd)/sample_files" go test ./cmd/wsitools/ -race -count=1 -timeout 30m 2>&1 | tail -3
```
Expected: `ok  github.com/wsilabs/wsitools/cmd/wsitools`.

- [ ] **Step 2: `make vet`**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && make vet 2>&1 | grep -v 'ld: warning' | tail -3
```
Expected: clean.

- [ ] **Step 3: End-to-end sanity on the real slide**

Run:
```bash
cd /Volumes/Ext/GitHub/wsitools && ./bin/wsitools convert --to svs -f -o /tmp/e2e.svs sample_files/svs/239551.svs && ./bin/wsitools info /tmp/e2e.svs | grep -iE 'thumbnail|label|overview' && ./bin/wsitools dump-ifds /tmp/e2e.svs | grep -E '^IFD' && rm -f /tmp/e2e.svs
```
Expected: `info` lists thumbnail+label+overview; `dump-ifds` shows IFD 1 = thumbnail (non-tiled) between L0 and L1.

- [ ] **Step 4: Final review subagent for the whole branch** (per subagent-driven-development) then finish via `superpowers:finishing-a-development-branch`.

---

## Self-Review notes

- **Spec coverage:** approach (interleave in both convert paths) → Tasks 3+4; rebuild wiring → Task 5; plan-handling table → `emitSVSThumbnailAtL0` + `writeAssociatedImages` skip (Tasks 3,5); SVS-only scope guard → `container=="svs"` guards everywhere; fixture/tests → Tasks 1,3,4,5,6; unit test → intentionally dropped (documented in spec).
- **Upsert risk:** handled by `emitSVSThumbnailAtL0`'s `plan.replace=="thumbnail"` branch + the `writeAssociatedImages` upsert guard `!(container=="svs" && plan.replace=="thumbnail")`.
- **Both convert paths:** Task 3 (tile-copy) + Task 4 (re-encode) — the re-encode test (`--codec jpeg`) guards the second.
- **No-op safety (single-level / non-SVS):** `emitSVSThumbnailAtL0` returns early; existing single-level + generic-TIFF + OME tests re-run in Tasks 3/5/7.
