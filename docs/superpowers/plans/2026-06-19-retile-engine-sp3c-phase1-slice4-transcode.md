# SP3c Phase 1 — Slice 4: revive `transcode` — Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Checkbox (`- [ ]`) steps.

**Goal:** Re-introduce `transcode` as an independent single-axis verb that
re-encodes a WSI's tiles to a different codec **in the same container, same
geometry** — e.g. `transcode --codec avif slide.tiff`.

**Architecture:** `convert --codec X` (with `--to` omitted ⇒ source format, no
`--rect`, no `--factor`) already performs a format-preserving transcode via the
M4 select-octave path. So `transcode` is a thin command that binds the same
`cv*` flag globals as `convert`, neutralizes the transform axes (`--to`=source,
no rect, factor 1), and calls `runConvert`. Same spec, same dispatch — provably one
code path. (Mirrors how `downsample` is a thin front-end over the shared dispatch.)

**Branch:** `feat/retile-engine-sp3c-2` (continues after DZI/SZI `--rect`).

---

### Task 1: `transcode` command

**Files:**
- Create: `cmd/wsitools/transcode.go`
- Test: `cmd/wsitools/transcode_test.go`

`transcode` binds `--codec`/`--quality`/`-o`/`--workers`/`--jobs`/`--no-associated`/
`--tile-order`/`--bigtiff`/`--force` to the **same `cv*` globals** `convert` uses
(only one cobra command runs per invocation, so sharing is safe). Its `RunE`
neutralizes the transform axes and calls `runConvert`. `runConvert` reads
`cmd.Flags().Changed(...)`; for flags `transcode` doesn't register (e.g. `rect`,
`dzi-format`, `level`), pflag's `Changed` returns false safely, so the rect block
is skipped and the transcode falls out.

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/transcode_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

// TestTranscodeRegistered confirms the transcode command exists with --codec.
func TestTranscodeRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"transcode"})
	if err != nil || cmd.Name() != "transcode" {
		t.Fatalf("transcode command not found: %v", err)
	}
	if cmd.Flags().Lookup("codec") == nil {
		t.Fatal("transcode missing --codec flag")
	}
	if cmd.Flags().Lookup("rect") != nil {
		t.Fatal("transcode must NOT expose --rect (single-axis: codec only)")
	}
	if cmd.Flags().Lookup("to") != nil {
		t.Fatal("transcode must NOT expose --to (format-preserving)")
	}
}

// TestTranscodeRequiresCodec confirms --codec is required.
func TestTranscodeRequiresCodec(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"transcode"})
	ann := cmd.Flags().Lookup("codec").Annotations
	if _, ok := ann["cobra_annotation_bash_completion_one_required_flag"]; !ok {
		// MarkFlagRequired sets this annotation; presence => required.
		t.Errorf("--codec should be marked required (annotations=%v)", ann)
	}
	_ = strings.TrimSpace // keep import if unused above
}
```

(If the required-flag annotation key differs in this cobra version, the
implementer may instead assert via a help/usage check; the intent is "`--codec` is
required".)

- [ ] **Step 2: Run — expect FAIL** (no transcode command)

Run: `go test ./cmd/wsitools/ -run TestTranscode`

- [ ] **Step 3: Implement `transcode`**

Create `cmd/wsitools/transcode.go`:

```go
package main

import (
	"time"

	"github.com/spf13/cobra"
)

// transcodeCmd re-encodes a WSI's tiles to a different codec in the SAME
// container with the SAME geometry. It is the codec-axis single-op alias of
// convert: it binds convert's cv* flag globals and delegates to runConvert with
// the transform axes neutralized (--to = source format, no crop, no downsample).
var transcodeCmd = &cobra.Command{
	Use:   "transcode --codec <codec> -o <output> [flags] <input>",
	Short: "Re-encode a WSI's tiles to a different codec (same container, same geometry)",
	Long: `transcode re-encodes the tiles of a WSI to a different codec while
preserving the container and the pyramid geometry. It is the codec-axis sibling of
crop (space) and downsample (resolution); to combine axes, use convert.

Examples:

  wsitools transcode --codec avif -o slide.avif.tiff slide.tiff
  wsitools transcode --codec jpeg2000 --quality reversible=true -o out.svs in.svs`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Codec axis only: preserve container + geometry.
		cvTo = ""
		cvRect, cvRectX, cvRectY, cvRectW, cvRectH = "", 0, 0, 0, 0
		cvFactor, cvTargetMag = 1, 0
		return runConvert(cmd, args)
	},
}

func init() {
	transcodeCmd.Flags().StringVar(&cvCodec, "codec", "", "output tile codec (jpeg|jpeg2000|jpegxl|avif|webp|htj2k) (required)")
	transcodeCmd.Flags().StringVar(&cvQuality, "quality", "", "codec quality (codec-specific; comma-separated k=v knobs)")
	transcodeCmd.Flags().StringVarP(&cvOutput, "output", "o", "", "output file path (required)")
	transcodeCmd.Flags().IntVar(&cvWorkers, "workers", 0, "pipeline workers (0 = GOMAXPROCS)")
	transcodeCmd.Flags().IntVar(&cvJobs, "jobs", 0, "alias of --workers")
	transcodeCmd.Flags().BoolVarP(&cvForce, "force", "f", false, "overwrite output if it exists")
	transcodeCmd.Flags().BoolVar(&cvNoAssociated, "no-associated", false, "skip label/macro/thumbnail/overview")
	transcodeCmd.Flags().StringVar(&cvTileOrder, "tile-order", "row-major", "tile emission order (row-major|hilbert|morton)")
	transcodeCmd.Flags().StringVar(&cvBigTIFFFlag, "bigtiff", "auto", "auto|on|off")
	_ = transcodeCmd.MarkFlagRequired("codec")
	_ = transcodeCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(transcodeCmd)
}

// _ keeps time imported if a future timestamp is needed; runConvert stamps its own.
var _ = time.Now
```

(If the unused `time` import is awkward, drop it and the `var _ = time.Now` line —
`runConvert` handles timing internally.)

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./cmd/wsitools/ -run TestTranscode`

- [ ] **Step 5: Build + broader test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'Transcode|Convert' -count=1`
Expected: PASS. `gofmt -l cmd/wsitools/transcode.go` → clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/transcode.go cmd/wsitools/transcode_test.go
git commit -m "$(cat <<'EOF'
feat(transcode): revive the transcode verb (codec-axis alias of convert)

transcode re-encodes tiles to a different codec in the same container and
geometry. Thin front-end: binds convert's cv* flags, neutralizes the
transform axes (--to=source, no rect, factor 1), and delegates to
runConvert — provably the same code path as convert --codec.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`.

- [ ] **Step 2: transcode ≡ convert --codec (pixel parity, same container)**

```bash
./bin/wsitools transcode --codec jpeg2000 -o /tmp/sp4-t.tiff sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools convert --codec jpeg2000 -o /tmp/sp4-c.tiff sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools info /tmp/sp4-t.tiff   # container = generic-tiff? NO -> see note
./bin/wsitools hash --mode pixel /tmp/sp4-t.tiff
./bin/wsitools hash --mode pixel /tmp/sp4-c.tiff
```
Expected: pixel hashes match (same code path). NOTE: source is SVS, so `--to`
omitted ⇒ output container = svs for BOTH; the output re-detects as svs with
jpeg2000 tiles, same geometry as the source.

- [ ] **Step 3: geometry + container preserved**

```bash
./bin/wsitools info sample_files/svs/CMU-1-Small-Region.svs | grep -E "Format|L0|L1"
./bin/wsitools info /tmp/sp4-t.tiff | grep -E "Format|L0|L1"
```
Expected: same Format (svs) and same per-level dimensions — only the codec differs.

- [ ] **Step 4: --codec required**

```bash
./bin/wsitools transcode -o /tmp/x.tiff sample_files/svs/CMU-1-Small-Region.svs ; echo "exit=$?"
```
Expected: non-zero exit, "required flag(s) \"codec\" not set".

- [ ] **Step 5: Clean up** `/tmp/sp4-*`.

---

## Self-review

`transcode` binds convert's cv* globals, neutralizes `--to`/rect/factor, delegates
to `runConvert`. `--codec`/`--output` required; no `--to`/`--rect` exposed
(single-axis). Same dispatch as `convert --codec` → pixel-identical (Task 2).
pflag `Changed` on unregistered flags (rect/dzi-format/level) returns false, so
`runConvert`'s rect/dzi/level branches are inert.

**Boundaries:** transcode verb only. Deferred: Slice 3c (`--codec` on the
crop/downsample transform path — the rect+codec combination). This slice does NOT
touch the rect+codec guard; `convert --rect --codec` stays rejected.
