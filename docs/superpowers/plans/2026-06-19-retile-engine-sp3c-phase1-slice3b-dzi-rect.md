# SP3c Phase 1 — Slice 3b-DZI: `--rect` for DZI/SZI — Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Checkbox (`- [ ]`) steps.

**Goal:** `convert --to dzi|szi --rect X,Y,W,H [--factor N]` — build a Deep Zoom
pyramid of a cropped (and optionally downsampled) region.

**Architecture:** `emitDZIPyramid` already drives `retile.Run` with a `SrcRegion`
(hardcoded to the full L0). Threading a `srcRegion` (origin+size) makes `--rect`
fall out: `SrcRegion = rect`, `OutL0 = rect/factor`. `runConvertDZI`/`runConvertSZI`
resolve the rect (default = full L0); convert's `--rect` block lets dzi/szi fall
through to the switch (they own their rect handling), so no crop-emitter routing is
involved.

**Branch:** `feat/retile-engine-sp3c-2` (off main@da4e80f, after the Phase-1 merge).

---

### Task 1: `resolveConvertRect` + `emitDZIPyramid(srcRegion)` + DZI/SZI rect-aware

**Files:**
- Modify: `cmd/wsitools/convert_dzi.go` (`emitDZIPyramid` signature; `runConvertDZI`)
- Modify: `cmd/wsitools/convert_szi.go` (`runConvertSZI`)
- Create: `cmd/wsitools/convert_rect_resolve.go` (the shared helper)
- Test: `cmd/wsitools/convert_rect_resolve_test.go`

- [ ] **Step 1: Failing test for the rect resolver**

Create `cmd/wsitools/convert_rect_resolve_test.go`:

```go
package main

import (
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/spf13/cobra"
)

func TestResolveConvertRect_NoFlags(t *testing.T) {
	cmd := &cobra.Command{}
	registerRectFlags(cmd)
	got, err := resolveConvertRect(cmd, 2220, 2967)
	if err != nil {
		t.Fatal(err)
	}
	want := opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: opentile.Size{W: 2220, H: 2967}}
	if got != want {
		t.Fatalf("no rect flags must be full L0: got %+v want %+v", got, want)
	}
}

func TestResolveConvertRect_Rect(t *testing.T) {
	cmd := &cobra.Command{}
	registerRectFlags(cmd)
	_ = cmd.Flags().Set("rect", "10,20,100,200")
	got, err := resolveConvertRect(cmd, 2220, 2967)
	if err != nil {
		t.Fatal(err)
	}
	want := opentile.Region{Origin: opentile.Point{X: 10, Y: 20}, Size: opentile.Size{W: 100, H: 200}}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestResolveConvertRect_OutOfBounds(t *testing.T) {
	cmd := &cobra.Command{}
	registerRectFlags(cmd)
	_ = cmd.Flags().Set("rect", "0,0,9999,9999")
	if _, err := resolveConvertRect(cmd, 2220, 2967); err == nil {
		t.Fatal("expected out-of-bounds error")
	}
}
```

NOTE: this test needs a `registerRectFlags(cmd)` helper so the test cmd has the
`--rect/--x/--y/--w/--h` flags bound to the `cvRect…` globals. Currently those
flags are registered inline in `convert`'s `init()`. Extract that registration
into a `registerRectFlags(cmd *cobra.Command)` function (called from `init()`), so
both production and the test use it. (If extraction is awkward, the implementer may
instead build the region from explicit values — but prefer the shared helper.)

- [ ] **Step 2: Run — expect FAIL** (`resolveConvertRect`/`registerRectFlags` undefined)

Run: `go test ./cmd/wsitools/ -run TestResolveConvertRect`

- [ ] **Step 3: Implement the resolver**

Create `cmd/wsitools/convert_rect_resolve.go`:

```go
package main

import (
	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
)

// rectFlagsSet reports whether any of --rect/--x/--y/--w/--h was provided.
func rectFlagsSet(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("rect") || cmd.Flags().Changed("x") ||
		cmd.Flags().Changed("y") || cmd.Flags().Changed("w") || cmd.Flags().Changed("h")
}

// resolveConvertRect returns the source region for a convert operation: the full
// L0 (srcW×srcH) when no rect flag is set, else the validated crop rect. Bounds
// are checked against srcW/srcH.
func resolveConvertRect(cmd *cobra.Command, srcW, srcH int) (opentile.Region, error) {
	if !rectFlagsSet(cmd) {
		return opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: opentile.Size{W: srcW, H: srcH}}, nil
	}
	rx, ry, rw, rh, err := resolveRectValues(cmd, cvRect, cvRectX, cvRectY, cvRectW, cvRectH)
	if err != nil {
		return opentile.Region{}, err
	}
	if err := validateCropBounds(rx, ry, rw, rh, srcW, srcH); err != nil {
		return opentile.Region{}, err
	}
	return opentile.Region{Origin: opentile.Point{X: rx, Y: ry}, Size: opentile.Size{W: rw, H: rh}}, nil
}
```

Extract the rect-flag registration from `convert`'s `init()` into a shared
function in `cmd/wsitools/convert.go` (replace the 5 inline
`convertCmd.Flags().…("rect"/"x"/"y"/"w"/"h", …)` lines with a call):

```go
// registerRectFlags binds --rect/--x/--y/--w/--h on cmd to the cv* rect globals.
func registerRectFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cvRect, "rect", "", "crop rectangle X,Y,W,H (level-0 coords)")
	cmd.Flags().IntVar(&cvRectX, "x", 0, "crop X (level-0 coords; with --y/--w/--h)")
	cmd.Flags().IntVar(&cvRectY, "y", 0, "crop Y (level-0 coords)")
	cmd.Flags().IntVar(&cvRectW, "w", 0, "crop width (level-0 pixels)")
	cmd.Flags().IntVar(&cvRectH, "h", 0, "crop height (level-0 pixels)")
}
```
and in `convert`'s `init()` call `registerRectFlags(convertCmd)`.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./cmd/wsitools/ -run TestResolveConvertRect`

- [ ] **Step 5: `emitDZIPyramid` takes a `srcRegion`**

In `cmd/wsitools/convert_dzi.go`, change `emitDZIPyramid`'s signature from `(ctx,
slide, w, cfg, srcW, srcH int)` to `(ctx, slide, w dziTileSink, cfg dzi.Config,
srcRegion opentile.Region)`. Inside:
- the kernel check becomes `if srcRegion.Size.W != cfg.Width || srcRegion.Size.H != cfg.Height { kernel = resample.Box }` (else Nearest).
- the `retile.Run` `SrcRegion:` field becomes `srcRegion` (delete the inline
  `opentile.Region{Origin:{0,0}, Size:{srcW,srcH}}`).

- [ ] **Step 6: `runConvertDZI`/`runConvertSZI` resolve the rect**

In both `runConvertDZI` (convert_dzi.go) and `runConvertSZI` (convert_szi.go),
replace the `srcW, srcH := l0.Size.W, l0.Size.H` → `reducedDims(srcW, srcH,
factor)` → `emitDZIPyramid(..., srcW, srcH)` sequence with:

```go
	srcW, srcH := l0.Size.W, l0.Size.H
	srcRegion, err := resolveConvertRect(cmd, srcW, srcH)
	if err != nil {
		return err
	}
	outW, outH, err := reducedDims(srcRegion.Size.W, srcRegion.Size.H, factor)
	if err != nil {
		return err
	}
```
(keep the existing `factor`/`resolveFactor` resolution and `cfg` construction with
`Width: outW, Height: outH`); and the emit call becomes:
```go
	if err := emitDZIPyramid(cmd.Context(), slide, w, cfg, srcRegion); err != nil {
```
Confirm `opentile` is imported in convert_szi.go (add if needed).

- [ ] **Step 7: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ResolveConvertRect|DZI|Dzi|Convert'`
Expected: PASS. (No-rect behavior unchanged: full-L0 region == old srcW/srcH.) `gofmt -l` → clean.

- [ ] **Step 8: Commit**

```bash
git add cmd/wsitools/convert_dzi.go cmd/wsitools/convert_szi.go cmd/wsitools/convert.go cmd/wsitools/convert_rect_resolve.go cmd/wsitools/convert_rect_resolve_test.go
git commit -m "$(cat <<'EOF'
feat(convert): DZI/SZI honor a source region (rect plumbing)

emitDZIPyramid takes a srcRegion; runConvertDZI/SZI resolve --rect (default
full L0) via the shared resolveConvertRect and size the deep-zoom output
from the region. No behavior change when no rect flag is set.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Route `convert --rect --to dzi|szi`; drop the dzi/szi guard

**Files:**
- Modify: `cmd/wsitools/convert.go` (`runConvert` rect block; `validateRectCombo`)
- Test: `cmd/wsitools/convert_rect_test.go`

- [ ] **Step 1: Update tests**

In `cmd/wsitools/convert_rect_test.go`: the `rect+dzi`/`rect+szi` rows in
`TestConvertRectComboGuards` are no longer errors — remove them. In
`TestConvertRectComboAllowed` add:

```go
	if err := validateRectCombo(true, 2, 0, "", "dzi"); err != nil {
		t.Fatalf("rect+factor+dzi is now allowed, got %v", err)
	}
```

- [ ] **Step 2: Run — expect FAIL** (guard still rejects dzi/szi rect)

Run: `go test ./cmd/wsitools/ -run TestConvertRectCombo`

- [ ] **Step 3: Drop the dzi/szi rejection in `validateRectCombo`**

Remove the block:
```go
	if to == "dzi" || to == "szi" {
		return fmt.Errorf("--rect with --to %s is not yet supported", to)
	}
```
(`validateRectCombo` now only rejects `--rect` with `--codec` — and that is only
relevant for the crop-emitter targets; see Step 4.)

- [ ] **Step 4: Let dzi/szi fall through in `runConvert`'s rect block**

In `runConvert`, the rect block currently routes ALL rect-set invocations to
`runCrop`. Scope it to the crop-emitter targets; dzi/szi fall through to the switch
(where `runConvertDZI`/`runConvertSZI` now handle the rect via `resolveConvertRect`):

```go
	if rectFlagsSet(cmd) && cvTo != "dzi" && cvTo != "szi" {
		if err := validateRectCombo(true, cvFactor, cvTargetMag, cvCodec, cvTo); err != nil {
			return err
		}
		... (the existing resolveFactor + resolveRectValues + runCrop body, unchanged) ...
	}
```
(Replace the existing `rectSet := …; if rectSet {…}` with the `rectFlagsSet(cmd) &&
cvTo != "dzi" && cvTo != "szi"` guard. Reuse the new `rectFlagsSet` helper instead
of the inline OR chain. The dzi/szi switch arms are unchanged — they already call
`runConvertDZI`/`runConvertSZI`.)

- [ ] **Step 5: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ConvertRect|Convert|DZI|Dzi'`
Expected: PASS. `gofmt -l` → clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/convert_rect_test.go
git commit -m "$(cat <<'EOF'
feat(convert): --rect for --to dzi|szi (deep zoom of a region)

convert --rect with a dzi/szi target falls through to runConvertDZI/SZI
(now region-aware); the crop-emitter routing is scoped to the non-dzi/szi
targets. Drops the dzi/szi --rect guard.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`.
- [ ] **Step 2: deep zoom of a region**
```bash
./bin/wsitools convert --to dzi --rect 0,0,2048,2048 -o /tmp/sp3bdzi.dzi sample_files/svs/CMU-1-Small-Region.svs
head -1 /tmp/sp3bdzi.dzi   # the .dzi manifest declares Width/Height
```
Expected: succeeds; manifest Size = 2048×2048 (the cropped region).
- [ ] **Step 3: region + downsample**
```bash
./bin/wsitools convert --to szi --rect 0,0,2048,2048 --factor 2 -o /tmp/sp3bszi.szi sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
```
Expected: succeeds (region 2048 → 1024 base).
- [ ] **Step 4: no-rect dzi unchanged**
```bash
./bin/wsitools convert --to dzi -o /tmp/sp3bdzi-full.dzi sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
```
Expected: succeeds (full slide, as before).
- [ ] **Step 5: Clean up** `/tmp/sp3bdzi* /tmp/sp3bszi*`.

---

## Self-review

`resolveConvertRect` (full L0 default / validated rect); `emitDZIPyramid(srcRegion)`;
DZI/SZI rect-aware; convert routing (dzi/szi fall through); guard dropped. No-rect
path unchanged (full-L0 region == old srcW/srcH). `registerRectFlags` extraction
keeps the test and production flag set identical.

**Boundaries:** DZI/SZI `--rect`. Deferred: `--codec` on the transform path (3c),
`transcode` revival (Slice 4).
