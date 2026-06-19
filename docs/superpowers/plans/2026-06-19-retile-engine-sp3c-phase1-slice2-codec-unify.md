# SP3c Phase 1 — Slice 2: `--codec` unification + flag reconcile — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `--codec` the single tile-codec flag across all targets (DZI/SZI
accept `--codec jpeg|png`), deprecate `--dzi-format` to a back-compat alias, gate
`--codec png` to `dzi|szi` only, and reconcile `--jobs`/`--workers` as aliases.

**Architecture:** Add a pure helper `resolveDZIFormat(codec, codecSet, dziFormat,
dziFormatSet)` that picks the effective DZI/SZI tile format (`--codec` wins over
the deprecated `--dzi-format`, validated to `jpeg|png`). Wire it into
`runConvertDZI`/`runConvertSZI`, replace the old `--codec`-rejects-dzi validation
in `runConvert` with a `png`-only-for-`dzi|szi` gate, and `MarkDeprecated` the
`--dzi-format` flag. Separately, add `--jobs` as an alias of `--workers` on
`convert`/`crop` and `--workers` as an alias of `--jobs` on `downsample`, resolved
by "explicitly-set wins."

**Tech Stack:** Go, cobra (flag deprecation + `Changed()`).

**Spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-unified-convert-design.md`
(correction #3 gating paragraph + the `--jobs`/`--workers` reconciliation section).

**Branch:** `feat/retile-engine-sp3c` (Slice 1 already landed on it). Continue here.

**Depends on:** Slice 1 (PNG is a registered codec, so `codec.Lookup("png")`
resolves and `--codec png` is a meaningful value).

---

### Task 1: `--codec` for DZI/SZI + `png` gating + deprecate `--dzi-format`

**Files:**
- Create: `cmd/wsitools/dzi_format.go` (the resolver helper)
- Test: `cmd/wsitools/dzi_format_test.go`
- Modify: `cmd/wsitools/convert.go:97-102` (validation), `cmd/wsitools/convert.go:80`
  (deprecate `--dzi-format`)
- Modify: `cmd/wsitools/convert_dzi.go` (`runConvertDZI`: use the resolver) and the
  SZI entry (`runConvertSZI`, wherever it builds `dzi.Config{Format: cvDZIFormat}`)

Current behavior (to replace):
```go
// convert.go runConvert, lines ~97-102
if (cvTo == "dzi" || cvTo == "szi") && cvCodec != "" {
	return fmt.Errorf("--codec is not valid with --to %s (use --dzi-format)", cvTo)
}
if (cvTo == "dzi" || cvTo == "szi") && cvDZIFormat != "jpeg" && cvDZIFormat != "png" {
	return fmt.Errorf("--dzi-format must be jpeg or png, got %q", cvDZIFormat)
}
```
`cvDZIFormat` currently flows into `dzi.Config{Format: cvDZIFormat}` inside
`runConvertDZI` (`convert_dzi.go:67`) and the matching SZI path.

- [ ] **Step 1: Write the failing test for the resolver**

Create `cmd/wsitools/dzi_format_test.go`:

```go
package main

import "testing"

func TestResolveDZIFormat(t *testing.T) {
	cases := []struct {
		name         string
		codec        string
		codecSet     bool
		dziFormat    string
		dziFormatSet bool
		want         string
		wantErr      bool
	}{
		{"default jpeg", "", false, "jpeg", false, "jpeg", false},
		{"codec png", "png", true, "jpeg", false, "png", false},
		{"codec jpeg", "jpeg", true, "jpeg", false, "jpeg", false},
		{"deprecated dzi-format png", "", false, "png", true, "png", false},
		{"codec wins over dzi-format", "png", true, "jpeg", true, "png", false},
		{"codec invalid for dzi", "avif", true, "jpeg", false, "", true},
		{"dzi-format invalid", "", false, "tiff", true, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveDZIFormat(c.codec, c.codecSet, c.dziFormat, c.dziFormatSet)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestResolveDZIFormat`
Expected: FAIL — `resolveDZIFormat` undefined.

- [ ] **Step 3: Write the resolver**

Create `cmd/wsitools/dzi_format.go`:

```go
package main

import "fmt"

// resolveDZIFormat picks the effective DZI/SZI tile codec. --codec is the
// canonical flag; --dzi-format is the deprecated alias. When --codec is set it
// wins; otherwise --dzi-format (default "jpeg") applies. The result must be a
// Deep Zoom tile format (jpeg or png) — browser deep-zoom viewers render nothing
// else.
func resolveDZIFormat(codec string, codecSet bool, dziFormat string, dziFormatSet bool) (string, error) {
	format := dziFormat
	if format == "" {
		format = "jpeg"
	}
	if codecSet {
		format = codec
	}
	switch format {
	case "jpeg", "png":
		return format, nil
	default:
		return "", fmt.Errorf("DZI/SZI tiles must be jpeg or png, got %q", format)
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestResolveDZIFormat`
Expected: PASS (all sub-cases).

- [ ] **Step 5: Wire the resolver into `runConvert` validation**

In `cmd/wsitools/convert.go`, replace the two `if` blocks at lines ~97-102 with:

```go
	codecSet := cmd.Flags().Changed("codec")
	dziFormatSet := cmd.Flags().Changed("dzi-format")
	if cvTo == "dzi" || cvTo == "szi" {
		// DZI/SZI: --codec (or the deprecated --dzi-format) selects the tile
		// format; validated to jpeg|png by the resolver in runConvertDZI/SZI.
		if _, err := resolveDZIFormat(cvCodec, codecSet, cvDZIFormat, dziFormatSet); err != nil {
			return err
		}
	} else if cvCodec == "png" {
		// PNG is a Deep Zoom tile format only; it is not a readable WSI-container
		// tile codec (opentile does not read PNG-compressed TIFF tiles).
		return fmt.Errorf("--codec png is only valid with --to dzi|szi (not %q)", cvTo)
	}
```

- [ ] **Step 6: Use the resolver in `runConvertDZI` and `runConvertSZI`**

In `cmd/wsitools/convert_dzi.go` `runConvertDZI`, replace the `cfg` construction's
`Format: cvDZIFormat` with the resolved value. Just before building `cfg`:

```go
	dziFormat, err := resolveDZIFormat(cvCodec, cmd.Flags().Changed("codec"), cvDZIFormat, cmd.Flags().Changed("dzi-format"))
	if err != nil {
		return err
	}
```
then `cfg := dzi.Config{Name: name, Width: outW, Height: outH, Format: dziFormat, TileSize: cvDZITileSize, Overlap: cvDZIOverlap}`.

Find the SZI equivalent: `grep -n "cvDZIFormat\|dzi.Config\|szi.Config" cmd/wsitools/*.go`.
Apply the same resolver substitution in the SZI entry (`runConvertSZI`). If SZI
shares `emitDZIPyramid`/`dzi.Config`, the same two-line resolve + `Format:
dziFormat` applies. (`runConvertSZI` has access to `cmd` for `Changed(...)`; if it
does not take `cmd`, thread `cmd.Flags().Changed` results in as a `string` param —
prefer passing `cmd` through if the signature already has it.)

- [ ] **Step 7: Deprecate `--dzi-format`**

In `cmd/wsitools/convert.go` `init()`, immediately AFTER the
`convertCmd.Flags().StringVar(&cvDZIFormat, "dzi-format", ...)` line (~80), add:

```go
	_ = convertCmd.Flags().MarkDeprecated("dzi-format", "use --codec jpeg|png")
```

This keeps `--dzi-format` working (back-compat) but hides it from help and prints
a deprecation notice when used. Update the `--codec` flag usage string (~73) to
note DZI/SZI:

```go
	convertCmd.Flags().StringVar(&cvCodec, "codec", "", "output tile codec (jpeg|jpeg2000|jpegxl|avif|webp|htj2k; jpeg|png for dzi|szi); absent = tile-copy when eligible")
```

- [ ] **Step 8: Build and run the convert/DZI tests**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ResolveDZIFormat|DZI|Dzi|Convert'`
Expected: PASS. (No behavior change for existing `--dzi-format jpeg|png` usage; new
`--codec jpeg|png` for dzi/szi now accepted; `--codec png --to tiff` now errors.)

- [ ] **Step 9: Commit**

```bash
git add cmd/wsitools/dzi_format.go cmd/wsitools/dzi_format_test.go cmd/wsitools/convert.go cmd/wsitools/convert_dzi.go
# plus the SZI file if separate:
git add -A
git commit -m "$(cat <<'EOF'
feat(convert): --codec unifies DZI/SZI tile format; deprecate --dzi-format

--codec is now the single tile-codec flag: DZI/SZI accept --codec
jpeg|png (resolveDZIFormat picks it, --codec winning over the deprecated
--dzi-format alias). --codec png is gated to --to dzi|szi (PNG is a Deep
Zoom tile format, not a readable WSI-container codec). --dzi-format kept
as a hidden back-compat alias via MarkDeprecated.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `--jobs` ⇄ `--workers` reconciliation

**Files:**
- Modify: `cmd/wsitools/convert.go` (add `--jobs` alias), `cmd/wsitools/crop.go`
  (add `--jobs` alias), `cmd/wsitools/downsample.go` (add `--workers` alias)
- Create: `cmd/wsitools/workers.go` (the resolver helper)
- Test: `cmd/wsitools/workers_test.go`

`convert`/`crop` expose `--workers` (`cvWorkers`/`cropWorkers`); `downsample`
exposes `--jobs` (`dsJobs`). Make each accept both names; "explicitly-set wins,
else the command's native default." Keep a single resolver.

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/workers_test.go`:

```go
package main

import "testing"

func TestResolveWorkers(t *testing.T) {
	// primary set → primary; alias set → alias; both set → primary wins;
	// neither set → primary's value (the default).
	cases := []struct {
		name                    string
		primary                 int
		primarySet              bool
		alias                   int
		aliasSet                bool
		want                    int
	}{
		{"neither set uses primary default", 8, false, 0, false, 8},
		{"primary set", 4, true, 0, false, 4},
		{"alias set", 8, false, 6, true, 6},
		{"both set primary wins", 4, true, 6, true, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveWorkers(c.primary, c.primarySet, c.alias, c.aliasSet); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/wsitools/ -run TestResolveWorkers`
Expected: FAIL — `resolveWorkers` undefined.

- [ ] **Step 3: Write the resolver**

Create `cmd/wsitools/workers.go`:

```go
package main

// resolveWorkers reconciles a command's primary worker flag with its alias
// (--workers ⇄ --jobs). An explicitly-set primary wins; otherwise an
// explicitly-set alias applies; otherwise the primary's value (its default) is
// used.
func resolveWorkers(primary int, primarySet bool, alias int, aliasSet bool) int {
	if primarySet {
		return primary
	}
	if aliasSet {
		return alias
	}
	return primary
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestResolveWorkers`
Expected: PASS.

- [ ] **Step 5: Add the alias flags + resolve in each command**

In `cmd/wsitools/convert.go` `init()`, after the `--workers` flag (~75), add a
`--jobs` alias bound to a new package var `cvJobs int`:

```go
	convertCmd.Flags().IntVar(&cvJobs, "jobs", 0, "alias of --workers")
```
Declare `cvJobs int` in the `var (...)` block (~22). At the TOP of `runConvert`
(after `start := time.Now()`), resolve:

```go
	cvWorkers = resolveWorkers(cvWorkers, cmd.Flags().Changed("workers"), cvJobs, cmd.Flags().Changed("jobs"))
```
(Re-assigning `cvWorkers` means every downstream reader uses the reconciled value
with no further changes.)

In `cmd/wsitools/crop.go` `init()`, add the same `--jobs` alias bound to a new
`cropJobs int` var, and resolve inside the cobra `RunE` BEFORE calling `runCrop`:

```go
	cropCmd.Flags().IntVar(&cropJobs, "jobs", 0, "alias of --workers")
```
```go
		workers := resolveWorkers(cropWorkers, cmd.Flags().Changed("workers"), cropJobs, cmd.Flags().Changed("jobs"))
		return runCrop(cmd.Context(), args[0], cropOutput, x, y, w, h,
			cropQuality, workers, cropTileOrder, cropBigTIFF, cropForce, cropNoAssoc, cropLossless, time.Now())
```

In `cmd/wsitools/downsample.go` `init()`, add a `--workers` alias bound to a new
`dsWorkers int` var, and resolve at the top of `runDownsample`:

```go
	downsampleCmd.Flags().IntVar(&dsWorkers, "workers", 0, "alias of --jobs")
```
```go
	dsJobs = resolveWorkers(dsJobs, cmd.Flags().Changed("jobs"), dsWorkers, cmd.Flags().Changed("workers"))
```
(Here `--jobs` is the primary — its default is `runtime.NumCPU()` — and
`--workers` is the alias.)

- [ ] **Step 6: Build and smoke-test the flags**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ResolveWorkers'`
Expected: PASS. Confirm help still lists the primary flags:
`./bin/wsitools convert --help 2>&1 | grep -E "workers|jobs"` after a `make build`
should show both (the alias usage string says "alias of --workers").

- [ ] **Step 7: Commit**

```bash
git add cmd/wsitools/workers.go cmd/wsitools/workers_test.go cmd/wsitools/convert.go cmd/wsitools/crop.go cmd/wsitools/downsample.go
git commit -m "$(cat <<'EOF'
feat(cli): reconcile --jobs and --workers as aliases

convert/crop gain --jobs; downsample gains --workers. resolveWorkers
picks the explicitly-set flag (primary wins ties), so both names work
everywhere with no behavior change to existing invocations.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Integration gate (controller-run)

**Files:** none (verification only). Needs the built binary; the **controller**
runs it.

- [ ] **Step 1: Build**

Run: `make build`.

- [ ] **Step 2: `--codec` for DZI works; `--dzi-format` still works + warns**

```bash
./bin/wsitools convert --to dzi --codec png -o /tmp/sp3c-s2a.dzi sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools convert --to szi --codec jpeg -o /tmp/sp3c-s2b.szi sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools convert --to dzi --dzi-format png -o /tmp/sp3c-s2c.dzi sample_files/svs/CMU-1-Small-Region.svs 2>&1 | grep -i deprecat
```
Expected: first two succeed (PNG/JPEG tiles); the third succeeds AND prints a
deprecation notice for `--dzi-format`.

- [ ] **Step 3: `--codec png` rejected for WSI containers**

```bash
./bin/wsitools convert --to tiff --codec png -o /tmp/sp3c-s2d.tiff sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
```
Expected: non-zero exit + message "--codec png is only valid with --to dzi|szi".

- [ ] **Step 4: `--jobs`/`--workers` both accepted**

```bash
./bin/wsitools convert --to dzi --jobs 3 -o /tmp/sp3c-s2e.dzi sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools downsample --workers 3 -o /tmp/sp3c-s2f.svs sample_files/svs/CMU-1-Small-Region.svs
```
Expected: both succeed.

- [ ] **Step 5: Clean up** the `/tmp/sp3c-s2*` outputs.

---

## Self-review

**Spec coverage (Slice 2 scope):**
- "`--codec` is unified across all targets; DZI/SZI accept `--codec jpeg|png`" →
  Task 1 (resolver + runConvertDZI/SZI). ✓
- "`--dzi-format` becomes a deprecated back-compat alias" → Task 1 Step 7
  (`MarkDeprecated`). ✓
- "`--codec png` gated to `dzi|szi` only (ad-hoc checks in Phase 1)" → Task 1 Step 5
  (the `else if cvCodec == "png"` gate). ✓
- "`--jobs`/`--workers` reconciliation" → Task 2. ✓

**Placeholder scan:** none — resolver code, wiring, and commands are complete; the
one lookup the implementer must do (`runConvertSZI`'s exact location/signature) is
a named grep with the substitution spelled out, not a placeholder.

**Type consistency:** `resolveDZIFormat(codec string, codecSet bool, dziFormat
string, dziFormatSet bool) (string, error)` and `resolveWorkers(primary int,
primarySet bool, alias int, aliasSet bool) int` are used with matching signatures
in every call site (runConvert, runConvertDZI/SZI, runConvert, runCrop RunE,
runDownsample). New package vars: `cvJobs`, `cropJobs`, `dsWorkers` (all `int`).

## Boundaries

**In Slice 2:** `--codec` for dzi/szi, png gating, `--dzi-format` deprecation,
`--jobs`/`--workers` aliases.

**Not in Slice 2:** `--rect`/`--to` optional/`transformTo*` convergence (Slice 3);
`transcode` + crop/downsample alias re-pointing (Slice 4). The PNG→TIFF
non-conformant-but-writable case stays rejected (Phase 2 capability table is where
"writable-but-nonconformant" lands; Phase 1 simply rejects png outside dzi|szi).
