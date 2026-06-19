# SP3c Phase 1 — Slice 3a: `convert --rect` + optional `--to` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `convert` crop a region into any container in one pass — `convert
--to <fmt> --rect X,Y,W,H` — and make `--to` optional (omitted ⇒ source format),
by reusing the existing crop emitters with a parameterized target.

**Architecture:** `runCrop` already opens the source, validates the rect, and
dispatches to per-target crop emitters (`cropEmitSVS`/`cropTo{TIFF,OMETIFF,COGWSI,
DICOM}`) — but it derives `target` from the *source* format (format-preserving).
Slice 3a parameterizes that `target` so `convert --rect` can pass `--to` (or the
source format when `--to` is omitted), threads `force` through `cropEmitParams`
(today `cropToDICOM` reads the `cropForce` global, which would ignore convert's
`--force`), and adds the `--rect`/`--x/--y/--w/--h` flags to `convert`. `convert
--rect` is always lossy in Phase 1 (`--lossless` stays a `crop`-only flag).

**Scope boundary:** This slice ships crop+container-change at **factor 1**, codec
**jpeg**, targets **svs/tiff/ome-tiff/cog-wsi/dicom**. Deferred to later
sub-slices: `--rect`+`--factor` (3b), `--codec` on the transform path (3c),
`--rect` for `dzi|szi` (3b). Those combos **hard-error** here with a "not yet
supported" message naming the slice, so we never silently ignore a flag.

**Tech Stack:** Go, cobra.

**Spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-unified-convert-design.md`.

**Branch:** `feat/retile-engine-sp3c` (Slices 1-2 landed). Continue here.

---

### Task 1: Thread `force` through `cropEmitParams`; parameterize `runCrop` target

**Files:**
- Modify: `cmd/wsitools/crop_formats.go` (`cropEmitParams` struct + `cropToDICOM`)
- Modify: `cmd/wsitools/crop.go` (`runCrop` signature + target resolution; the cobra
  `RunE` closure call)

This is a behavior-preserving refactor: `crop` output is unchanged. Two changes:
(1) add a `force bool` field to `cropEmitParams` and have `cropToDICOM` use
`p.force` instead of the `cropForce` global; (2) give `runCrop` a `target string`
parameter (empty ⇒ derive from source, the current behavior).

- [ ] **Step 1: Add a regression test pinning crop's current dispatch**

Create `cmd/wsitools/crop_target_test.go`:

```go
package main

import "testing"

// TestCropDefaultTargetFromSource documents that an empty target resolves to the
// source format's container (the format-preserving default). Guards the Task 1
// refactor: parameterizing runCrop's target must NOT change crop's behavior.
func TestCropDefaultTargetFromSource(t *testing.T) {
	cases := []struct {
		format string
		want   string
		ok     bool
	}{
		{"svs", "svs", true},
		{"tiff", "tiff", true},
		{"ome-tiff", "ome-tiff", true},
		{"cog-wsi", "cog-wsi", true},
		{"dicom", "dicom", true},
	}
	for _, c := range cases {
		got, ok := downsampleTargetForFormat(c.format)
		if ok != c.ok || got != c.want {
			t.Fatalf("downsampleTargetForFormat(%q) = (%q,%v), want (%q,%v)", c.format, got, ok, c.want, c.ok)
		}
	}
}
```

- [ ] **Step 2: Run it — expect PASS** (documents existing helper; no code change yet)

Run: `go test ./cmd/wsitools/ -run TestCropDefaultTargetFromSource`
Expected: PASS.

- [ ] **Step 3: Add `force` to `cropEmitParams` and use it in `cropToDICOM`**

In `cmd/wsitools/crop_formats.go`, add a field to the `cropEmitParams` struct
(after `noAssociated bool`, ~line 75):

```go
	force        bool
```

In `cropToDICOM`, replace BOTH uses of the global `cropForce` (the `emitDICOM(...,
p.output, cropForce)` call in the lossless branch ~line 428 and the
`runDICOMEngine(..., p.output, cropForce)` call in the lossy branch ~line 449)
with `p.force`.

- [ ] **Step 4: Set `force` where `cropEmitParams` is built, and parameterize `runCrop`**

In `cmd/wsitools/crop.go`, change `runCrop`'s signature to take a `target string`
(insert before `start time.Time`):

```go
func runCrop(ctx context.Context, input, output string, x, y, w, h, quality, workers int, tileOrderName, bigtiffFlag string, force, noAssociated, lossless bool, target string, start time.Time) error {
```

Replace the source-format target resolution (~lines 166-169):

```go
	srcTarget, ok := downsampleTargetForFormat(string(src.Format()))
	if !ok {
		return fmt.Errorf("crop: unsupported source format %q (supported: svs, ome-tiff, tiff, cog-wsi, dicom)", src.Format())
	}
	if target == "" {
		target = srcTarget
	}
```

Add the `force: force` field when building `cropEmitParams` (~line 221-227 block):

```go
		lossless: lossless, stx0: stx0, sty0: sty0, outTilesX: outTilesX, outTilesY: outTilesY,
		force: force,
		start: start,
```

In the cobra `RunE` closure (~lines 60-67), pass `""` as the new `target` arg (crop
preserves the source format), keeping the resolved `workers` from Slice 2:

```go
		workers := resolveWorkers(cropWorkers, cmd.Flags().Changed("workers"), cropJobs, cmd.Flags().Changed("jobs"))
		return runCrop(cmd.Context(), args[0], cropOutput, x, y, w, h,
			cropQuality, workers, cropTileOrder, cropBigTIFF, cropForce, cropNoAssoc, cropLossless, "", time.Now())
```

- [ ] **Step 5: Build and run the crop tests**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'Crop|crop'`
Expected: PASS (crop behavior unchanged). `gofmt -l cmd/wsitools/crop.go cmd/wsitools/crop_formats.go` → clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/crop.go cmd/wsitools/crop_formats.go cmd/wsitools/crop_target_test.go
git commit -m "$(cat <<'EOF'
refactor(crop): thread force through cropEmitParams; parameterize target

runCrop gains a target arg (empty = source format, unchanged crop
behavior); cropToDICOM uses p.force instead of the cropForce global, so
the crop emitters are reusable by convert --rect with the right --force.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Make `--to` optional in `convert`

**Files:**
- Modify: `cmd/wsitools/convert.go` (`init()` drops `MarkFlagRequired("to")`;
  `runConvert` resolves an empty `--to` to the source format)
- Create: `cmd/wsitools/convert_target.go` (the resolver helper)
- Test: `cmd/wsitools/convert_target_test.go`

When `--to` is omitted, the output container is the source's own format. We resolve
it from the opened source via the existing `downsampleTargetForFormat` mapping.

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/convert_target_test.go`:

```go
package main

import "testing"

func TestResolveConvertTarget(t *testing.T) {
	// Explicit --to wins; empty --to falls back to the source format's container.
	cases := []struct {
		to        string
		srcFormat string
		want      string
		wantErr   bool
	}{
		{"dicom", "svs", "dicom", false},      // explicit wins
		{"", "svs", "svs", false},             // infer from source
		{"", "ome-tiff", "ome-tiff", false},   // infer from source
		{"", "bogus-format", "", true},        // un-inferable source
	}
	for _, c := range cases {
		got, err := resolveConvertTarget(c.to, c.srcFormat)
		if (err != nil) != c.wantErr {
			t.Fatalf("to=%q src=%q: err=%v wantErr=%v", c.to, c.srcFormat, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Fatalf("to=%q src=%q: got %q want %q", c.to, c.srcFormat, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run it — expect FAIL** (`resolveConvertTarget` undefined)

Run: `go test ./cmd/wsitools/ -run TestResolveConvertTarget`

- [ ] **Step 3: Write the resolver**

Create `cmd/wsitools/convert_target.go`:

```go
package main

import "fmt"

// resolveConvertTarget picks the output container for `convert`. An explicit --to
// wins; an empty --to is inferred from the source format (the format-preserving
// default that makes `convert in out` and `convert --rect` subsume crop), using
// the same source→container mapping as downsample/crop. An un-inferable source
// errors, asking the user to specify --to.
func resolveConvertTarget(to, srcFormat string) (string, error) {
	if to != "" {
		return to, nil
	}
	target, ok := downsampleTargetForFormat(srcFormat)
	if !ok {
		return "", fmt.Errorf("cannot infer output container for source format %q; specify --to", srcFormat)
	}
	return target, nil
}
```

- [ ] **Step 4: Run it — expect PASS**

Run: `go test ./cmd/wsitools/ -run TestResolveConvertTarget`

- [ ] **Step 5: Drop the required flag + resolve in `runConvert`**

In `cmd/wsitools/convert.go` `init()`, delete the line:

```go
	_ = convertCmd.MarkFlagRequired("to")
```

In `runConvert`, the empty-`--to` case currently errors (`case "": return
fmt.Errorf("--to is required")`). Replace the whole `switch cvTo` lead-in: BEFORE
the `guardStitchedSource` call (~line 106), resolve the target when omitted by
peeking the source format. Add:

```go
	if cvTo == "" {
		f, err := opentile.OpenFile(input)
		if err != nil {
			return fmt.Errorf("open source: %w", err)
		}
		srcFormat := string(f.Format())
		_ = f.Close()
		resolved, err := resolveConvertTarget("", srcFormat)
		if err != nil {
			return err
		}
		cvTo = resolved
	}
```

Add the `opentile` import to `convert.go` if not present:
`opentile "github.com/wsilabs/opentile-go"`. Then in the `switch cvTo`, change the
`case "":` arm to a defensive guard (it should now be unreachable):

```go
	case "":
		return fmt.Errorf("internal: --to unresolved")
```

- [ ] **Step 6: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ResolveConvertTarget|Convert'`
Expected: PASS. (Existing `TestConvertFailsForBadTo`/missing-input tests must still
pass; a bad explicit `--to` still hits the `default:` unknown-target arm.)

- [ ] **Step 7: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/convert_target.go cmd/wsitools/convert_target_test.go
git commit -m "$(cat <<'EOF'
feat(convert): make --to optional (default = source format)

Omitting --to infers the output container from the source format via
resolveConvertTarget, so `convert in out` and `convert --rect` preserve
the source container (subsuming crop/downsample). Explicit --to wins.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Add `--rect` to `convert`, routing to the crop core

**Files:**
- Modify: `cmd/wsitools/convert.go` (flags + `runConvert` `--rect` routing + combo
  guards)
- Test: `cmd/wsitools/convert_rect_test.go`

`convert --rect` reuses `runCrop` (now target-parameterized). Phase 1 scope:
factor 1, jpeg, lossy, targets svs/tiff/ome-tiff/cog-wsi/dicom. Unsupported combos
hard-error.

- [ ] **Step 1: Add the flags**

In `cmd/wsitools/convert.go`, add package vars (in the `var (...)` block):

```go
	cvRect string
	cvRectX int
	cvRectY int
	cvRectW int
	cvRectH int
```

In `init()`, register them:

```go
	convertCmd.Flags().StringVar(&cvRect, "rect", "", "crop rectangle X,Y,W,H (level-0 coords); crops before container/codec change")
	convertCmd.Flags().IntVar(&cvRectX, "x", 0, "crop X (level-0 coords; with --y/--w/--h)")
	convertCmd.Flags().IntVar(&cvRectY, "y", 0, "crop Y (level-0 coords)")
	convertCmd.Flags().IntVar(&cvRectW, "w", 0, "crop width (level-0 pixels)")
	convertCmd.Flags().IntVar(&cvRectH, "h", 0, "crop height (level-0 pixels)")
```

- [ ] **Step 2: Write the failing test for combo guards**

Create `cmd/wsitools/convert_rect_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

// TestConvertRectComboGuards checks that --rect rejects the combos deferred past
// Slice 3a (factor, codec, dzi/szi) with a clear message, rather than silently
// ignoring the flag.
func TestConvertRectComboGuards(t *testing.T) {
	cases := []struct {
		name    string
		rect    string
		factor  int
		codec   string
		to      string
		wantSub string
	}{
		{"rect+factor", "0,0,10,10", 2, "", "tiff", "--rect with --factor"},
		{"rect+codec", "0,0,10,10", 1, "avif", "tiff", "--rect with --codec"},
		{"rect+dzi", "0,0,10,10", 1, "", "dzi", "--rect with --to dzi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRectCombo(c.rect, c.factor, c.codec, c.to)
			if err == nil || !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("err=%v, want substring %q", err, c.wantSub)
			}
		})
	}
}

func TestConvertRectComboAllowed(t *testing.T) {
	if err := validateRectCombo("0,0,10,10", 1, "", "tiff"); err != nil {
		t.Fatalf("plain rect should be allowed, got %v", err)
	}
	if err := validateRectCombo("", 1, "", "tiff"); err != nil {
		t.Fatalf("no rect should be allowed, got %v", err)
	}
}
```

- [ ] **Step 3: Run it — expect FAIL** (`validateRectCombo` undefined)

Run: `go test ./cmd/wsitools/ -run TestConvertRectCombo`

- [ ] **Step 4: Implement the guard + routing**

Add to `cmd/wsitools/convert.go` (a new helper near `runConvert`):

```go
// validateRectCombo rejects the --rect combinations deferred past SP3c Slice 3a.
// Slice 3a ships crop+container-change at factor 1, codec jpeg, into
// svs/tiff/ome-tiff/cog-wsi/dicom. factor (Slice 3b), codec (Slice 3c), and
// dzi/szi (Slice 3b) come later.
func validateRectCombo(rect string, factor int, codec, to string) error {
	if rect == "" {
		return nil
	}
	if factor != 1 {
		return fmt.Errorf("--rect with --factor is not yet supported (coming in a later release); crop then downsample separately for now")
	}
	if codec != "" {
		return fmt.Errorf("--rect with --codec is not yet supported (coming in a later release)")
	}
	if to == "dzi" || to == "szi" {
		return fmt.Errorf("--rect with --to %s is not yet supported (coming in a later release)", to)
	}
	return nil
}
```

In `runConvert`, AFTER the `--to` resolution (Task 2) and the factor validation,
but BEFORE `guardStitchedSource`, add the `--rect` handling. Note the rect flags
must be read via the shared `resolveRectValues` helper (used by `crop`):

```go
	rectSet := cmd.Flags().Changed("rect") || cmd.Flags().Changed("x") ||
		cmd.Flags().Changed("y") || cmd.Flags().Changed("w") || cmd.Flags().Changed("h")
	if rectSet {
		if err := validateRectCombo(maybeRect(cmd), cvFactor, cvCodec, cvTo); err != nil {
			return err
		}
		rx, ry, rw, rh, err := resolveRectValues(cmd, cvRect, cvRectX, cvRectY, cvRectW, cvRectH)
		if err != nil {
			return err
		}
		workers := resolveWorkers(cvWorkers, cmd.Flags().Changed("workers"), cvJobs, cmd.Flags().Changed("jobs"))
		// convert --rect is always lossy in Phase 1 (--lossless stays on crop).
		return runCrop(cmd.Context(), input, cvOutput, rx, ry, rw, rh,
			qualityIntForConvert(), workers, cvTileOrder, cvBigTIFFFlag, cvForce, cvNoAssociated, false, cvTo, start)
	}
```

where `maybeRect` returns a non-empty marker when any rect flag is set (so
`validateRectCombo`'s `rect == ""` early-return is correct):

```go
// maybeRect returns "set" if any rect flag was provided, else "". It feeds
// validateRectCombo's presence check (the actual geometry comes from
// resolveRectValues).
func maybeRect(cmd *cobra.Command) string {
	if cmd.Flags().Changed("rect") || cmd.Flags().Changed("x") || cmd.Flags().Changed("y") ||
		cmd.Flags().Changed("w") || cmd.Flags().Changed("h") {
		return "set"
	}
	return ""
}
```

`convert`'s `--quality` is a string (codec knobs); `crop`/`runCrop` take an int
JPEG quality. Add a small adapter that parses convert's `--quality` to an int,
defaulting to 0 (which `runCrop` maps to "source Q for SVS else 90"):

```go
// qualityIntForConvert maps convert's string --quality to runCrop's int quality.
// Empty or unparseable → 0 (runCrop applies its source-Q/90 default). A bare
// integer (e.g. "85") is honored; k=v knob forms are ignored here (codec on the
// crop path is a later slice).
func qualityIntForConvert() int {
	if cvQuality == "" {
		return 0
	}
	if q, err := strconv.Atoi(strings.TrimSpace(cvQuality)); err == nil {
		return q
	}
	return 0
}
```

Add imports to `convert.go` as needed: `strconv`, `strings`.

- [ ] **Step 5: Run the combo-guard tests + build**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ConvertRect|Convert'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert.go cmd/wsitools/convert_rect_test.go
git commit -m "$(cat <<'EOF'
feat(convert): --rect crops into any --to container (one pass)

convert --rect routes through the (now target-parameterized) crop
emitters, so a region can be cropped and re-containered in a single
decode/rebuild. Factor-1, jpeg, lossy; svs/tiff/ome-tiff/cog-wsi/dicom.
--rect with --factor/--codec/dzi|szi hard-errors (later slices).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Integration gate (controller-run)

**Files:** none. Needs binary + fixture; the **controller** runs it.

- [ ] **Step 1: Build** — `make build`.

- [ ] **Step 2: `convert --rect` into a different container**

```bash
./bin/wsitools convert --to tiff --rect 0,0,2048,2048 -o /tmp/sp3c-s3a.tiff sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools info /tmp/sp3c-s3a.tiff
```
Expected: writes a TIFF whose L0 is ~2048×2048 (the cropped extent).

- [ ] **Step 3: `convert --rect` ≡ `crop` (same container) pixel parity**

```bash
./bin/wsitools convert --rect 0,0,2048,2048 -o /tmp/sp3c-s3a-conv.svs sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools crop --rect 0,0,2048,2048 -o /tmp/sp3c-s3a-crop.svs sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools hash --mode pixel /tmp/sp3c-s3a-conv.svs
./bin/wsitools hash --mode pixel /tmp/sp3c-s3a-crop.svs
```
Expected: identical pixel hashes (same code path; `--to` omitted = source format
svs).

- [ ] **Step 4: combo guards reject clearly**

```bash
./bin/wsitools convert --to tiff --rect 0,0,512,512 --factor 2 -o /tmp/x.tiff sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
./bin/wsitools convert --to dzi --rect 0,0,512,512 -o /tmp/x.dzi sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
```
Expected: both non-zero with the "not yet supported" messages.

- [ ] **Step 5: Clean up** the `/tmp/sp3c-s3a*` outputs.

---

## Self-review

**Spec coverage (Slice 3a scope):**
- `convert --rect` composing crop+container-change in one pass → Task 3 (routes to
  the crop emitters with target = `--to`). ✓
- `--to` optional ⇒ source format → Task 2. ✓
- Reuse, not duplicate, the crop machinery → Task 1 (parameterized `runCrop`). ✓
- Deferred combos (factor/codec/dzi-szi) explicitly hard-error, not silently
  ignored → Task 3 (`validateRectCombo`). ✓
- `--lossless` stays crop-only → Task 3 passes `lossless=false`. ✓

**Out of Slice 3a (later):** `--rect`+`--factor` (3b), `--codec` on the transform
path (3c), `--rect` for dzi/szi (3b), the full `downsampleTo*`/`cropTo*` →
`transformTo*` source merge (the metadata/desc/thumbnail unification lands when
factor support forces it, 3b). Phase 1b (lossless dzi), Phase 2 (conformance gate).

**Placeholder scan:** none — every code step is complete. The `resolveRectValues`
helper already exists (used by `crop`'s `RunE`); `downsampleTargetForFormat`
already exists (used by `runCrop`/`runDownsample`).

**Type consistency:** `runCrop` gains a `target string` param before `start`; both
call sites updated (crop `RunE` passes `""`, convert passes `cvTo`).
`cropEmitParams` gains `force bool`. `resolveConvertTarget(to, srcFormat string)
(string, error)`, `validateRectCombo(rect string, factor int, codec, to string)
error`, `qualityIntForConvert() int`, `maybeRect(cmd) string` — all used with
matching signatures.

## Boundaries

**In Slice 3a:** parameterized `runCrop` target + `force` threading; optional
`--to`; `convert --rect` (factor 1, jpeg, lossy) into svs/tiff/ome-tiff/cog-wsi/
dicom; combo hard-errors.

**Not in Slice 3a:** factor+rect, codec-on-transform, dzi/szi rect, the
emitter-family merge — Slices 3b/3c.
