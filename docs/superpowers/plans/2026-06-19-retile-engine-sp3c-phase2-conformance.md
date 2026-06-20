# SP3c Phase 2 — conformance gate — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Checkbox (`- [ ]`) steps.

**Goal:** One capability table + `validateCodec` gate replaces the 5 scattered
codec×container checks; `--allow-nonconformant` unlocks writable-but-non-readable
outputs (the real case: OME-TIFF + non-jpeg); valid codecs surface in errors.

**Architecture:** `containerCapabilities(container)` returns the conformant +
nonconformant codec sets (everything else ⇒ unsupported). `validateCodec(container,
codec, allow) → (warning, err)` classifies into the three tiers. `runConvert` and
the re-encode dispatch points call it (replacing their local hardcoded sets). The
table values are **verified by round-trip** (Task 1) before they are committed.

**Spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-phase2-conformance-design.md`.

**Branch:** `feat/retile-engine-sp3c-phase2-conformance` (off main@e089125).

**The 5 checks to consolidate:** `convert.go:142` (png→dzi/szi), `dzi_format.go:22`
(dzi jpeg/png), `crop.go:214` + `convert_factor.go:84` (svs jpeg-only),
`dicom_engine.go:184` (dicom jpeg/jp2k/htj2k), `convert_tiff.go:69` (--codec
required).

---

### Task 1: Round-trip matrix → verified table values (controller-run)

**Files:** none yet (produces the data Task 2 hardcodes).

The "conformant" tier = wsitools writes it AND opentile reads it back. Verify
empirically per (re-encoding container, codec). `hash --mode pixel` decodes every
tile, so a successful hash == readable.

- [ ] **Step 1: Build** — `make build`.

- [ ] **Step 2: Run the matrix.** For container C ∈ {tiff, ome-tiff, cog-wsi} and
codec K ∈ {jpeg, jpeg2000, htj2k, avif, webp, jpegxl}, plus dicom K ∈ {jpeg,
jpeg2000, htj2k}:

```bash
SRC=sample_files/svs/CMU-1-Small-Region.svs
for C in tiff ome-tiff cog-wsi; do
  for K in jpeg jpeg2000 htj2k avif webp jpegxl; do
    out=/tmp/p2-$C-$K
    if ./bin/wsitools convert --to $C --codec $K -o $out.tiff "$SRC" 2>/tmp/p2err; then
      if ./bin/wsitools hash --mode pixel $out.tiff >/dev/null 2>&1; then
        echo "$C $K = CONFORMANT (writes + reads)"
      else
        echo "$C $K = NONCONFORMANT (writes, does NOT read back)"
      fi
    else
      echo "$C $K = WRITE-FAILED: $(head -1 /tmp/p2err)"
    fi
  done
done
# dicom separately (output is a dir):
for K in jpeg jpeg2000 htj2k avif webp jpegxl; do
  if ./bin/wsitools convert --to dicom --codec $K -o /tmp/p2-dcm-$K "$SRC" 2>/tmp/p2err; then
    ./bin/wsitools hash --mode pixel /tmp/p2-dcm-$K >/dev/null 2>&1 && echo "dicom $K = CONFORMANT" || echo "dicom $K = NONCONFORMANT"
  else
    echo "dicom $K = WRITE-FAILED: $(head -1 /tmp/p2err)"
  fi
done
```

- [ ] **Step 3: Record results.** Write the observed conformant / nonconformant /
write-failed (= unsupported) sets per container into this plan (or a scratch note).
These are the **verified** table values Task 2 uses. Expected shape (CONFIRM by
running — do NOT assume): generic-tiff likely conformant for the codecs opentile
decodes; ome-tiff non-jpeg likely NONCONFORMANT (reader jpeg-limited); cog-wsi
conformant for its set; dicom jpeg/jp2k/htj2k conformant, avif/webp/jxl
write-failed. **The committed table must match what Step 2 actually printed.**

- [ ] **Step 4: Clean up** `/tmp/p2-*`.

---

### Task 2: `capabilities.go` — the table + `validateCodec`

**Files:**
- Create: `cmd/wsitools/capabilities.go`
- Test: `cmd/wsitools/capabilities_test.go`

Use the **Task 1 verified values**. The structure below shows the shape; fill the
codec sets from Task 1's output.

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/capabilities_test.go`:

```go
package main

import "testing"

func TestValidateCodec(t *testing.T) {
	cases := []struct {
		container, codec string
		allow            bool
		wantErr          bool
		wantWarn         bool
	}{
		// conformant → ok, no warn (use a pair Task 1 confirmed conformant)
		{"tiff", "jpeg2000", false, false, false},
		{"cog-wsi", "jpeg", false, false, false},
		{"dzi", "png", false, false, false},
		// nonconformant → error by default, warn under --allow
		{"ome-tiff", "avif", false, true, false},
		{"ome-tiff", "avif", true, false, true},
		// unsupported → hard error regardless of --allow
		{"svs", "avif", false, true, false},
		{"svs", "avif", true, true, false},
		{"dicom", "avif", true, true, false},
		{"dzi", "avif", false, true, false},
	}
	for _, c := range cases {
		t.Run(c.container+"/"+c.codec, func(t *testing.T) {
			warn, err := validateCodec(c.container, c.codec, c.allow)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if (warn != "") != c.wantWarn {
				t.Fatalf("warn=%q wantWarn=%v", warn, c.wantWarn)
			}
		})
	}
}
```

(Adjust the conformant/nonconformant example pairs to match Task 1's verified
results — e.g. if Task 1 found `tiff avif` NONCONFORMANT, use a different
conformant pair.)

- [ ] **Step 2: Run — expect FAIL** (`validateCodec` undefined)

Run: `go test ./cmd/wsitools/ -run TestValidateCodec`

- [ ] **Step 3: Implement the table + gate**

Create `cmd/wsitools/capabilities.go`:

```go
package main

import (
	"fmt"
	"strings"
)

// containerCaps describes a container's codec support. Codecs not in either set
// are unsupported (no encoder / no slot / emitter is codec-limited).
type containerCaps struct {
	conformant    []string // wsitools writes it AND opentile reads it back
	nonconformant []string // writable bytes, NOT readable as this format
	redirect      string   // hint appended to an unsupported-codec error
}

// containerCapabilities is the single source of truth for codec×container
// support. Values are VERIFIED by the Phase-2 round-trip matrix (see the plan).
// Forward-looking: this is the seam to delegate to an opentile capability API.
func containerCapabilities(container string) containerCaps {
	switch container {
	case "tiff":
		return containerCaps{conformant: []string{ /* Task 1 */ }}
	case "svs":
		return containerCaps{conformant: []string{"jpeg"}, redirect: "wsitools writes SVS tiles as jpeg; use --to tiff"}
	case "ome-tiff":
		return containerCaps{conformant: []string{"jpeg"}, nonconformant: []string{ /* Task 1: jpeg2000, htj2k, avif, webp, jpegxl that WRITE but don't read back */ }}
	case "cog-wsi":
		return containerCaps{conformant: []string{ /* Task 1 */ }}
	case "dicom":
		return containerCaps{conformant: []string{"jpeg", "jpeg2000", "htj2k"}, redirect: "DICOM has no transfer syntax for that codec; use jpeg, jpeg2000, or htj2k"}
	case "dzi", "szi":
		return containerCaps{conformant: []string{"jpeg", "png"}, redirect: "Deep Zoom tiles are jpeg or png"}
	case "bif":
		return containerCaps{conformant: []string{"jpeg"}, redirect: "BIF is written by verbatim tile-copy only"}
	default:
		return containerCaps{}
	}
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// validateCodec classifies a (container, codec) pair. Returns a non-empty warning
// for nonconformant-but-allowed; a non-nil error to abort before any I/O.
func validateCodec(container, codec string, allowNonconformant bool) (string, error) {
	caps := containerCapabilities(container)
	if contains(caps.conformant, codec) {
		return "", nil
	}
	if contains(caps.nonconformant, codec) {
		if allowNonconformant {
			return fmt.Sprintf("--codec %s into %s is non-conformant: the bytes are valid but this tool's reader cannot open them as %s", codec, container, container), nil
		}
		return "", fmt.Errorf("--codec %s produces a non-conformant %s (not readable as %s); pass --allow-nonconformant to write it anyway", codec, container, container)
	}
	msg := fmt.Sprintf("--codec %s is not supported for --to %s", codec, container)
	if caps.redirect != "" {
		msg += " (" + caps.redirect + ")"
	}
	if len(caps.conformant) > 0 {
		msg += "; supported: " + strings.Join(caps.conformant, ", ")
	}
	return "", fmt.Errorf("%s", msg)
}
```

Fill the `/* Task 1 */` codec sets from the verified matrix.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./cmd/wsitools/ -run TestValidateCodec`

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/capabilities.go cmd/wsitools/capabilities_test.go
git commit -m "$(cat <<'EOF'
feat(convert): capability table + validateCodec gate

containerCapabilities is the single source of truth for codec×container
support (values verified by round-trip); validateCodec classifies
conformant / nonconformant / unsupported and produces consistent
messages. Wired into the dispatch + the --allow-nonconformant flag next.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `--allow-nonconformant` flag + gate wiring + consolidate the 5 checks

**Files:**
- Modify: `cmd/wsitools/convert.go` (flag + gate call), `cmd/wsitools/transcode.go`
  (flag), `cmd/wsitools/crop.go`, `cmd/wsitools/convert_factor.go`,
  `cmd/wsitools/dicom_engine.go`, `cmd/wsitools/dzi_format.go`,
  `cmd/wsitools/convert_tiff.go` (replace ad-hoc checks)
- Test: extend `cmd/wsitools/capabilities_test.go` / convert tests

- [ ] **Step 1: Add the flag**

In `convert.go`: `cvAllowNonconformant bool` var; in `init()`:
```go
	convertCmd.Flags().BoolVar(&cvAllowNonconformant, "allow-nonconformant", false, "write a valid-but-non-readable output (e.g. non-jpeg OME-TIFF) with a warning")
```
Add the same flag to `transcode.go` (binds `cvAllowNonconformant`).

- [ ] **Step 2: Call the gate in `runConvert`**

After `cvTo` and the dzi/szi codec handling, before dispatch, when an explicit
`--codec` is set:
```go
	if cvCodec != "" {
		warn, err := validateCodec(cvTo, cvCodec, cvAllowNonconformant)
		if err != nil {
			return err
		}
		if warn != "" {
			fmt.Fprintln(os.Stderr, "warning:", warn)
		}
	}
```
This catches the cases EARLY (before the deeper dispatch). The existing
`--codec png` check (`convert.go:142`) is now redundant for the convert path —
remove it (the gate's dzi/szi caps cover png).

- [ ] **Step 3: Replace the SVS guards with the gate**

`crop.go:214` and `convert_factor.go:84` SVS guards → `validateCodec(target,
codecName, cvAllowNonconformant)` (return its err; print its warn). This keeps the
gate firing for `crop --rect`/`downsample` paths too. (SVS is unsupported for
non-jpeg in the table → same hard error, now table-sourced.)

- [ ] **Step 4: Source the DICOM + DZI valid-sets from the table**

`dicom_engine.go:184`: keep `newDicomFrameEncoder`'s switch (it maps codec→encoder)
but ensure the up-front gate already rejected unsupported codecs; the switch's
default error becomes a defensive backstop. `dzi_format.go`: `resolveDZIFormat`'s
jpeg|png valid-set should reference `containerCapabilities("dzi").conformant`
(single source) rather than the hardcoded literal — or leave the literal and add a
test asserting they agree.

- [ ] **Step 5: `convert_tiff.go:69`** (--codec required when no tile-copy path) is
a *different* check (missing codec, not wrong codec) — leave it, but confirm it
doesn't conflict with the gate (it fires when `cvCodec == ""` and no tile-copy;
the gate only fires when `cvCodec != ""`).

- [ ] **Step 6: Build + test**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'ValidateCodec|Convert|Crop|Downsample|DZI|Dicom' -count=1`
Expected: PASS. Update any test asserting the old wording of the 5 messages.
`gofmt -l` → clean.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat(convert): --allow-nonconformant + route checks through validateCodec

The codec×container gate runs before dispatch; the 5 scattered ad-hoc
checks now source their rules from the capability table.
--allow-nonconformant downgrades a nonconformant error (e.g. non-jpeg
OME-TIFF) to a warning + writes it. png/svs/dzi/dicom checks consolidated.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`.

- [ ] **Step 2: nonconformant gating + escape hatch** (use an OME+codec pair Task 1
found NONCONFORMANT, e.g. avif):
```bash
SRC=sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools convert --to ome-tiff --codec avif -o /tmp/p2.tiff "$SRC" 2>&1 | grep -i "non-conformant"   # errors
./bin/wsitools convert --to ome-tiff --codec avif --allow-nonconformant -o /tmp/p2.tiff "$SRC" 2>&1 | grep -i "warning"  # writes + warns
test -f /tmp/p2.tiff && echo "wrote nonconformant OME with the flag"
```

- [ ] **Step 3: unsupported hard errors (no override)**:
```bash
./bin/wsitools convert --to svs --codec avif --allow-nonconformant -o /tmp/x.svs "$SRC" 2>&1 | grep -i "use --to tiff"   # still errors
./bin/wsitools convert --to dicom --codec avif -o /tmp/x.dcm "$SRC" 2>&1 | grep -i "no transfer syntax\|not supported"
./bin/wsitools convert --to tiff --codec png -o /tmp/x.tiff "$SRC" 2>&1 | grep -i "not supported\|Deep Zoom"
```

- [ ] **Step 4: conformant defaults unchanged**:
```bash
./bin/wsitools convert --to tiff --codec jpeg2000 -o /tmp/p2-ok.tiff "$SRC" && ./bin/wsitools hash --mode pixel /tmp/p2-ok.tiff >/dev/null && echo "conformant codec OK"
./bin/wsitools convert --to tiff -o /tmp/p2-def.tiff "$SRC" && echo "no --codec OK"   # gate skipped
```

- [ ] **Step 5: Clean up** `/tmp/p2*` `/tmp/x.*`.

---

## Self-review

**Spec coverage:** capability table verified by round-trip (Task 1 → Task 2);
`validateCodec` three tiers (Task 2); `--allow-nonconformant` + gate wiring + the 5
checks consolidated (Task 3); integration incl. escape hatch + unsupported errors
(Task 4). Help text via the error's "supported: …" list.

**Placeholder scan:** the `/* Task 1 */` codec sets are intentionally filled from
the round-trip results (Task 1 is a prerequisite, not a placeholder) — the table
structure + every other line is complete.

**Type consistency:** `containerCapabilities(string) containerCaps`;
`validateCodec(container, codec string, allow bool) (string, error)`;
`cvAllowNonconformant bool`.

## Boundaries

**In Phase 2:** the table, `validateCodec`, `--allow-nonconformant`, consolidation,
OME-non-jpeg gating, error-embedded help. **Deferred:** SVS-emitter codec support
(jpeg2000-SVS conformant); the `validate(spec)` unification of lossless/contradiction
checks; an actual opentile capability API.
