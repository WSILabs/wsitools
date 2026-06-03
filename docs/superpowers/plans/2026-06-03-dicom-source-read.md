# DICOM-WSI Read Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the gaps that stop DICOM-WSI from being a fully-supported read source across the wsitools CLI, with safe behavior on multi-instance/multi-series inputs.

**Architecture:** DICOM already flows through `source.Open`→`opentile.OpenFile` (opentile-go v0.32.2). The only failures are commands that bypass the source layer or assume TIFF/single-file: `extract` (uncompressed associated images), `hash` file-mode (directory), `dump-ifds` (non-TIFF). Fix each locally; add a safe-by-default ambiguity error for multi-series directories (gated on a new opentile-go enumeration API); add tests + a CI fixture.

**Tech Stack:** Go, opentile-go v0.32.2 (DICOM reader), cobra CLI, `golang.org/x/image/tiff`. Integration tests gated by `WSI_TOOLS_TESTDIR` (fixture: `dicom/Leica-4`).

**Spec:** `docs/superpowers/specs/2026-06-03-dicom-source-read-design.md`

---

## File structure

| File | Responsibility | Phase |
|---|---|---|
| `cmd/wsitools/extract.go` | `decodeAssociated`: handle uncompressed DICOM associated images (raw RGB, not TIFF) | A |
| `cmd/wsitools/extract_dicom_test.go` (new) | extract-on-DICOM integration test | A |
| `cmd/wsitools/hash.go` | `runHash`: reject file-mode on a directory with an actionable error | B |
| `cmd/wsitools/dump_ifds.go` | `runDumpIFDs`: friendly error for non-TIFF-dialect sources | B |
| `cmd/wsitools/dicom_smoke_test.go` (new) | smoke test across info/region/convert/extract/hash | B |
| `cmd/wsitools/hash_test.go`, `dump_ifds_test.go` | graceful-degradation unit tests | B |
| `README.md` | document DICOM input (instance or series dir; multi-series) | B |
| `.github/workflows/ci.yml` + wsi-fixtures | wire a small DICOM fixture into CI | B |
| `internal/source/opentile.go` (+ opentile-go bump) | multi-series ambiguity preflight (BLOCKED on opentile-go API) | B-deferred |
| `cmd/wsitools/dicom_fidelity_test.go` (new) | DICOM→{cog-wsi,ome-tiff} round-trip fidelity | C |

---

## Phase A — fix `extract` on uncompressed DICOM associated images

### Task A1: Decode uncompressed DICOM associated images as raw RGB

**Files:**
- Modify: `cmd/wsitools/extract.go` (`decodeAssociated`, the `CompressionNone`/`CompressionDeflate` case ~line 170)
- Test: `cmd/wsitools/extract_dicom_test.go` (new)

**Background:** opentile-go's DICOM `AssociatedImage.Bytes()` for an uncompressed (`none`) label returns **raw interleaved 8-bit RGB** (`w*h*3` bytes), not a TIFF. The current `CompressionNone` branch calls `xtiff.Decode(b)` → "malformed header". JPEG/JPEG2000 associated images already decode fine; this is only the uncompressed case.

- [ ] **Step 1: Write the failing test**

```go
// cmd/wsitools/extract_dicom_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// extract of the uncompressed DICOM label must succeed and produce a PNG.
func TestExtractDICOMUncompressedLabel(t *testing.T) {
	bin := stripedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	out := filepath.Join(t.TempDir(), "label.png")
	if cmdOut, err := runBin(bin, "extract", "--kind", "label", "-o", out, dir); err != nil {
		t.Fatalf("extract label: %v\n%s", err, cmdOut)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty %s: %v", out, err)
	}
}
```

> Note: `stripedBinary`, `testDir`, and a `runBin` helper exist or mirror the patterns in `scale_metadata_test.go` (`stripedBinary`, `stripedSample`, `exec.Command(bin, ...).CombinedOutput()`). If `testDir`/`runBin` are not present, add thin local helpers: `testDir` returns `os.Getenv("WSI_TOOLS_TESTDIR")` (skip if empty); `runBin` wraps `exec.Command(bin, args...).CombinedOutput()`.

- [ ] **Step 2: Run test to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestExtractDICOMUncompressedLabel -v`
Expected: FAIL with `tiff: invalid format: malformed header`.

- [ ] **Step 3: Implement the fix**

In `decodeAssociated`, replace the `CompressionNone`/`CompressionDeflate` case body with:

```go
	case source.CompressionDeflate, source.CompressionNone:
		// DICOM uncompressed associated images return raw interleaved 8-bit RGB
		// (w*h*3 bytes). SVS-family "none"/"deflate" associated images return a
		// TIFF-wrapped blob. Discriminate by exact length: a w*h*3 match is raw
		// RGB; otherwise fall back to the TIFF-wrapper path.
		if len(b) == w*h*3 {
			return rgbToImage(b, w, h), nil
		}
		return xtiff.Decode(bytes.NewReader(b))
```

(`rgbToImage(pix []byte, w, h int) image.Image` already exists — used by the JPEG case.)

- [ ] **Step 4: Run test to verify it passes**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestExtractDICOMUncompressedLabel -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/extract.go cmd/wsitools/extract_dicom_test.go
git commit -m "fix(extract): decode uncompressed DICOM associated images as raw RGB

decodeAssociated's CompressionNone branch assumed an SVS-style TIFF wrapper;
DICOM uncompressed associated images (e.g. the Leica label) return raw w*h*3
interleaved RGB. Discriminate by length; fall back to the TIFF path otherwise."
```

---

## Phase B — first-class read citizen

### Task B1: `hash` file-mode rejects a directory with an actionable error

**Files:**
- Modify: `cmd/wsitools/hash.go` (`runHash`, `case "file"` ~line 60)
- Test: `cmd/wsitools/hash_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestHashFileModeRejectsDirectory(t *testing.T) {
	bin := stripedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	out, err := runBin(bin, "hash", dir) // default --mode file
	if err == nil {
		t.Fatalf("expected error hashing a directory in file-mode, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "--mode pixel") {
		t.Errorf("error should point to --mode pixel, got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestHashFileModeRejectsDirectory -v`
Expected: FAIL — current error is the raw `hash file: read <dir>: is a directory`, which lacks `--mode pixel`.

- [ ] **Step 3: Implement the guard**

In `runHash`, at the start of `case "file":`:

```go
	case "file":
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			return fmt.Errorf("file-mode hash is undefined for a directory (e.g. a DICOM series); use --mode pixel for a content hash, or pass a single file")
		}
		h, err := hashFile(path)
```

- [ ] **Step 4: Run test to verify it passes**

Run: same command. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/hash.go cmd/wsitools/hash_test.go
git commit -m "feat(hash): actionable error for file-mode hash of a directory"
```

### Task B2: `dump-ifds` friendly error on non-TIFF-dialect sources

**Files:**
- Modify: `cmd/wsitools/dump_ifds.go` (`runDumpIFDs`, top ~line 76)
- Test: `cmd/wsitools/dump_ifds_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestDumpIFDsRejectsDICOM(t *testing.T) {
	bin := stripedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	out, err := runBin(bin, "dump-ifds", dir)
	if err == nil {
		t.Fatalf("expected error, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "TIFF-dialect") {
		t.Errorf("error should explain DICOM is not a TIFF-dialect source, got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestDumpIFDsRejectsDICOM -v`
Expected: FAIL — current raw error is `walk IFDs: ifdwalk: read header: ... is a directory`.

- [ ] **Step 3: Implement the guard**

At the very top of `runDumpIFDs`, before any walk (`source.WalkIFDs`/`runDumpIFDsRaw`):

```go
	// dump-ifds reads raw TIFF IFDs; non-TIFF-dialect sources (DICOM, IFE) have none.
	if s, err := source.Open(path); err == nil {
		f := s.Format()
		s.Close()
		if f == "dicom" || f == "ife" {
			cmd.SilenceUsage = true
			return fmt.Errorf("dump-ifds requires a TIFF-dialect source; %s is %s (no TIFF IFDs to dump)", path, f)
		}
	}
```

> The two non-TIFF source formats opentile-go exposes are `dicom` and `ife`; all others are TIFF dialects. Keep the check explicit — it's a stable, short list.

- [ ] **Step 4: Run test to verify it passes**

Run: same command. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/dump_ifds.go cmd/wsitools/dump_ifds_test.go
git commit -m "feat(dump-ifds): friendly error for non-TIFF-dialect sources (DICOM/IFE)"
```

### Task B3: DICOM read smoke test

**Files:**
- Test: `cmd/wsitools/dicom_smoke_test.go` (new)

- [ ] **Step 1: Write the test (it should pass immediately — these paths already work + A/B fixes)**

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDICOMReadSmoke(t *testing.T) {
	bin := stripedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	one, _ := filepath.Glob(filepath.Join(dir, "*.dcm"))
	if len(one) == 0 {
		t.Skip("no .dcm instances in fixture")
	}

	t.Run("info-dir", func(t *testing.T) {
		out, err := runBin(bin, "info", dir)
		if err != nil || !strings.Contains(string(out), "Format:  dicom") {
			t.Fatalf("info dir: %v\n%s", err, out)
		}
	})
	t.Run("info-instance", func(t *testing.T) {
		if out, err := runBin(bin, "info", one[0]); err != nil {
			t.Fatalf("info instance: %v\n%s", err, out)
		}
	})
	t.Run("region", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "r.png")
		if o, err := runBin(bin, "region", "--x", "8000", "--y", "8000", "--w", "256", "--h", "256", "--level", "0", "-o", out, dir); err != nil {
			t.Fatalf("region: %v\n%s", err, o)
		}
	})
	t.Run("convert-cogwsi", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "o.cog.tiff")
		if o, err := runBin(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, dir); err != nil {
			t.Fatalf("convert: %v\n%s", err, o)
		}
	})
	t.Run("hash-pixel", func(t *testing.T) {
		if o, err := runBin(bin, "hash", "--mode", "pixel", dir); err != nil {
			t.Fatalf("hash pixel: %v\n%s", err, o)
		}
	})
}
```

- [ ] **Step 2: Run**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestDICOMReadSmoke -v`
Expected: PASS (all subtests).

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/dicom_smoke_test.go
git commit -m "test(dicom): read smoke test (info/region/convert/hash) over Leica-4 fixture"
```

### Task B4: File the opentile-go WSM-series enumeration issue

This is the explicit deliverable enabling the ambiguity error (Task B7). It has no code in wsitools.

- [ ] **Step 1: File the issue**

```bash
gh issue create --repo WSILabs/opentile-go \
  --title "dicom: enumerate WSM series under a path (for safe multi-series disambiguation)" \
  --body 'Downstream (wsitools) needs to detect when a **directory** input resolves to more than one WSM `SeriesUID` so it can refuse with an actionable error instead of silently opening the dominant series. Today `OpenSeries` picks the dominant series (most VOLUME instances) with no signal that others existed.

**Request:** surface the set of WSM series under a path without fully opening a slide. Either:
- `dicom.ListWSMSeries(path) ([]SeriesInfo, error)` returning per series `{SeriesUID, levelCount, make, model, magnification, instanceCount}`; or
- an `OpenFile` option / typed `AmbiguousSeriesError` carrying the candidate list when a directory has >1 series.

Keep `OpenSeries` dominant-pick as the permissive library default (programmatic callers); this is purely to let a CLI render a safe ambiguity error. A single-instance path must remain unambiguous (anchored to its own `SeriesUID`).

Sibling of #10/#11/#12. Consumer: wsitools DICOM read support (`docs/superpowers/specs/2026-06-03-dicom-source-read-design.md`).'
```

- [ ] **Step 2: Record the issue number** in this plan (edit Task B7's blocker reference) and in the spec's "Required opentile-go enhancement" section.

### Task B5: Wire a small DICOM fixture into CI

**Files:** `.github/workflows/ci.yml`; `wsilabs/wsi-fixtures` (external repo)

> **External dependency:** CI fetches fixtures from `wsilabs/wsi-fixtures`. A real WSM series can be large; this task requires sourcing or creating a **small** WSM series (a low-level-count slide, or a synthesized minimal one) and adding it to a new fixtures tag, then updating the CI fetch.

- [ ] **Step 1:** Add a small DICOM WSM series to `wsilabs/wsi-fixtures` (new tag, e.g. v2); confirm total size is CI-appropriate (target < ~50 MB).
- [ ] **Step 2:** Update `.github/workflows/ci.yml` to download the DICOM fixture into `sample_files/dicom/<name>` alongside the existing CMU fixtures.
- [ ] **Step 3:** Point `TestDICOMReadSmoke`/`TestExtractDICOMUncompressedLabel` at the CI fixture name (or keep `Leica-4` and add it to fixtures). Run CI; confirm the DICOM tests execute (not skipped).
- [ ] **Step 4: Commit** the workflow change.

> If sourcing a small fixture proves hard, split this into its own follow-up issue and keep the DICOM tests `WSI_TOOLS_TESTDIR`-gated (local-only) for now — do not block Phase A/B shipping on it.

### Task B6: Document DICOM input

**Files:** `README.md`

- [ ] **Step 1:** Add a short "DICOM input" note to the README: input may be a single `.dcm` instance or a directory containing a WSM series; a directory with >1 distinct series errors and asks you to pass a specific `.dcm`; a named `.dcm` always opens its own series.
- [ ] **Step 2: Commit.**

### Task B7 (BLOCKED on Task B4's API): multi-series ambiguity error

> **Do not implement until the opentile-go enumeration API from Task B4 ships and wsitools is bumped to that version.** Until then, a directory with >1 series opens the dominant series as today (documented in Task B6). When the API lands, write the concrete TDD steps then, following this approach:

**Approach:**
- Add a preflight in `internal/source/Open` (so every command inherits it): when `path` is a **directory**, call the new opentile-go enumeration; if it reports >1 WSM series, return a typed error (`ErrAmbiguousDICOMSeries`) listing each series' `SeriesUID` + level count + make/model/mag and instructing the user to pass a specific `.dcm`. Single-instance inputs skip the check (never ambiguous).
- Test (`internal/source` + a CLI integration test): a fixture directory containing two series → assert the actionable error + non-zero exit; pointing at one `.dcm` in that directory → opens that series successfully.

---

## Phase C — convert-fidelity validation

### Task C1: DICOM → cog-wsi round-trip pixel + metadata fidelity

**Files:**
- Test: `cmd/wsitools/dicom_fidelity_test.go` (new)

- [ ] **Step 1: Write the test**

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cog-wsi tile-copy must preserve source pixels (pixel-hash equality) and
// key metadata (MPP, magnification) on a DICOM source.
func TestDICOMConvertCogWSIFidelity(t *testing.T) {
	bin := stripedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	out := filepath.Join(t.TempDir(), "o.cog.tiff")
	if o, err := runBin(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, dir); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	srcHash, err := runBin(bin, "hash", "--mode", "pixel", dir)
	if err != nil {
		t.Fatalf("hash src: %v\n%s", err, srcHash)
	}
	outHash, err := runBin(bin, "hash", "--mode", "pixel", out)
	if err != nil {
		t.Fatalf("hash out: %v\n%s", err, outHash)
	}
	if pixelDigest(srcHash) != pixelDigest(outHash) {
		t.Errorf("pixel hash mismatch:\n src=%s\n out=%s", srcHash, outHash)
	}
	// Metadata carried.
	info, err := runBin(bin, "info", out)
	if err != nil || !strings.Contains(string(info), "MPP:") || !strings.Contains(string(info), "Magnification:") {
		t.Errorf("expected MPP+Magnification in output info:\n%s", info)
	}
}

// pixelDigest extracts the sha256-pixel:<hex> token from a hash line.
func pixelDigest(out []byte) string {
	for _, f := range strings.Fields(string(out)) {
		if strings.HasPrefix(f, "sha256-pixel:") {
			return f
		}
	}
	return ""
}
```

- [ ] **Step 2: Run**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestDICOMConvertCogWSIFidelity -v`
Expected: PASS (cog-wsi is a lossless tile-copy, so source and output L0 pixel hashes match).

> If the hashes legitimately differ (e.g. cog-wsi re-tiles), weaken to a structural check (same level count + dimensions) and file a follow-up to investigate, rather than asserting false equality.

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/dicom_fidelity_test.go
git commit -m "test(dicom): cog-wsi round-trip pixel + metadata fidelity"
```

---

## Self-review

**Spec coverage:**
- extract uncompressed fix → Task A1 ✓
- hash file-mode graceful → Task B1 ✓
- dump-ifds graceful → Task B2 ✓
- smoke test → B3 ✓; CI fixture → B5 ✓; docs → B6 ✓
- opentile-go enumeration issue (explicit deliverable) → B4 ✓
- multi-series ambiguity error (safe-by-default, input-type scoped) → B7 (blocked on B4) ✓
- convert fidelity → C1 ✓

**Placeholders:** B7 is intentionally deferred (blocked on an external API that doesn't exist yet) — its concrete steps are written once the API lands; this is sequencing, not a hand-wave. B5 names an external-fixture dependency with a documented fallback. All code tasks contain complete code.

**Type consistency:** test helpers `stripedBinary`/`testDir`/`runBin`/`pixelDigest` used consistently; `rgbToImage(pix, w, h)` matches its existing signature; `source.Open(...).Format()` and `Close()` match the `Source` interface.

**Sequencing:** A and B1/B2/B3/B4/B6 ship independently. B5 (CI fixture) and B7 (ambiguity error) have external dependencies and must not block A/B shipping. C1 depends only on the read path (already working).
