# ICC Profile Carry-Through Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Emit the source ICC color profile (TIFF tag 34675) on the output L0 of every TIFF write target (`cog-wsi`, `svs`, `tiff`, `ome-tiff`) and `downsample`, pulled from opentile-go v0.31's `Slide.ICCProfile()`.

**Architecture:** Surface ICC bytes through `source.Metadata.ICCProfile` (convert) / `Slide.ICCProfile()` directly (downsample); both writers gain an `ICCProfile []byte` option and emit `AddUndefined(34675, …)` on L0. The cog-wsi spool-and-finalize layout must budget the ICC blob's *external bytes* (~142 KB), not just its tag slot.

**Tech Stack:** Go 1.26, `internal/tiff` primitives, opentile-go v0.31 (`Slide.ICCProfile()`).

**Spec:** `docs/superpowers/specs/2026-06-01-icc-profile-carry-design.md`

---

## File Structure
- Modify `internal/tiff/tags.go` — add `TagICCProfile`.
- Modify `internal/source/source.go` (+ `opentile.go`) — `Metadata.ICCProfile`.
- Modify `internal/tiff/streamwriter/options.go` + `writer.go` — ICC option + emit.
- Modify `internal/tiff/cogwsiwriter/writer.go` + `layout.go` — exact L0-metadata layout sizing (replaces the 2 KiB guess) + Close-time bounds-check + ICC emit.
- Modify `cmd/wsitools/convert_tiff.go`, `convert_cogwsi.go`, `downsample.go` — pass ICC.
- Modify `cmd/wsitools/scale_metadata_test.go` (add ICC integration cases) and add writer unit tests.

---

## Task 1: `source.Metadata.ICCProfile`

**Files:**
- Modify: `internal/source/source.go` (Metadata struct)
- Modify: `internal/source/opentile.go` (Metadata())
- Test: `internal/source/icc_test.go` (create)

- [ ] **Step 1: Write the failing test** — create `internal/source/icc_test.go`:

```go
package source_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

// TestMetadataICCProfile: an SVS fixture with an embedded ICC profile
// surfaces it via Metadata().ICCProfile.
func TestMetadataICCProfile(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	path := filepath.Join(dir, "svs", "CMU-1.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	src, err := source.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()
	icc := src.Metadata().ICCProfile
	if len(icc) == 0 {
		t.Fatal("expected non-empty ICCProfile for CMU-1.svs")
	}
	if len(icc) != 141992 {
		t.Errorf("ICCProfile len = %d, want 141992", len(icc))
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `WSI_TOOLS_TESTDIR="$PWD/sample_files" go test ./internal/source/ -run TestMetadataICCProfile -v`
Expected: FAIL — `src.Metadata().ICCProfile` undefined (compile error).

- [ ] **Step 3: Add the field** — in `internal/source/source.go`, add to the `Metadata` struct after the `MPPY` field:

```go
	MPPY                                float64 // µm/px, Y axis; 0 if unknown
	ICCProfile                          []byte  // embedded color profile; nil if none
```

- [ ] **Step 4: Populate it** — in `internal/source/opentile.go`, inside `Metadata()`, after the cross-format MPP block (and before `return m`), add:

```go
	m.ICCProfile = s.t.ICCProfile()
```

(`s.t` is the `*opentile.Slide`; `ICCProfile() []byte` exists in opentile-go v0.31. It returns nil when the source has none.)

- [ ] **Step 5: Run, expect PASS**

Run: `WSI_TOOLS_TESTDIR="$PWD/sample_files" go test ./internal/source/ -run TestMetadataICCProfile -v`
Expected: PASS (or SKIP if CMU-1.svs absent — on this machine it's present, so it runs).
Run: `go build ./internal/source/ && go vet ./internal/source/`.

- [ ] **Step 6: Commit**

```bash
git add internal/source/source.go internal/source/opentile.go internal/source/icc_test.go
git commit -m "feat(source): surface ICCProfile from opentile Slide.ICCProfile()

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: streamwriter emits ICC (backs svs/tiff/ome-tiff + downsample)

**Files:**
- Modify: `internal/tiff/tags.go` (constant)
- Modify: `internal/tiff/streamwriter/options.go`, `writer.go`
- Test: `internal/tiff/streamwriter/icc_test.go` (create)

- [ ] **Step 1: Write the failing test** — create `internal/tiff/streamwriter/icc_test.go`:

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

// TestICCEmitted: a streamwriter given an ICC profile emits tag 34675 on
// L0; absent ICC emits nothing.
func TestICCEmitted(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	write := func(icc []byte) string {
		path := filepath.Join(t.TempDir(), "o.tiff")
		w, err := streamwriter.Create(path, streamwriter.Options{
			BigTIFF: tiff.BigTIFFOn, ICCProfile: icc,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		l, _ := w.AddLevel(streamwriter.LevelSpec{
			ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
			Compression: tiff.CompressionNone, Photometric: 2,
			WSIImageType: tiff.WSIImageTypePyramid,
		})
		l.WriteTile(0, 0, make([]byte, 8*8*3))
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		out, _ := exec.Command("tiffinfo", path).CombinedOutput()
		return string(out)
	}
	withICC := strings.ToLower(write(make([]byte, 5000)))
	if !strings.Contains(withICC, "34675") && !strings.Contains(withICC, "iccprofile") && !strings.Contains(withICC, "icc profile") {
		t.Errorf("ICC tag 34675 not reported by tiffinfo:\n%s", withICC)
	}
	noICC := strings.ToLower(write(nil))
	if strings.Contains(noICC, "34675") || strings.Contains(noICC, "icc profile") {
		t.Errorf("unexpected ICC tag with nil profile:\n%s", noICC)
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/tiff/streamwriter/ -run TestICCEmitted`
Expected: FAIL — `unknown field 'ICCProfile' in struct literal`.

- [ ] **Step 3: Add the tag constant** — in `internal/tiff/tags.go`, add near `TagXResolution`:

```go
// TagICCProfile (34675) holds an embedded ICC color profile (UNDEFINED).
const TagICCProfile uint16 = 34675
```

- [ ] **Step 4: Add the Option** — in `internal/tiff/streamwriter/options.go`, add to `Options` after `Magnification`:

```go
	// ICCProfile is the embedded color profile, emitted on L0 as tag
	// 34675 (UNDEFINED) when non-empty.
	ICCProfile []byte
```

- [ ] **Step 5: Store + emit** — in `internal/tiff/streamwriter/writer.go`:

Add a field to `Writer` after `magnification float64`:

```go
	iccProfile []byte
```

Assign in `Create` after `magnification: opts.Magnification,`:

```go
		iccProfile:       opts.ICCProfile,
```

Append to `addL0Metadata` (after the magnification block, before the closing brace):

```go
	if len(w.iccProfile) > 0 {
		b.AddUndefined(tiff.TagICCProfile, w.iccProfile)
	}
```

- [ ] **Step 6: Run, expect PASS**

Run: `go test ./internal/tiff/streamwriter/ -run TestICCEmitted -race -count=1 -v`
Expected: PASS (or SKIP without tiffinfo — then also `go build ./internal/tiff/streamwriter/`).
Run: `go test ./internal/tiff/streamwriter/ -count=1` (no regression) and `go vet ./internal/tiff/streamwriter/`.

- [ ] **Step 7: Commit**

```bash
git add internal/tiff/tags.go internal/tiff/streamwriter/options.go internal/tiff/streamwriter/writer.go internal/tiff/streamwriter/icc_test.go
git commit -m "feat(streamwriter): emit ICC profile (tag 34675) on L0

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: cogwsiwriter — exact L0 layout sizing + bounds-check, then emit ICC

**Files:**
- Modify: `internal/tiff/cogwsiwriter/writer.go` (Metadata, populateLevelIFD, `l0MetadataExternalBytes` helper, builder, Close bounds-check)
- Modify: `internal/tiff/cogwsiwriter/layout.go` (levelLayoutInput, plan structs, countTagsForLevel, ifdSizeForLevel, planLayout)
- Test: `internal/tiff/cogwsiwriter/icc_test.go` (create)

The cog-wsi writer pre-computes the file layout in two phases: `planLayout` reserves `ifdSize + externalSize` per IFD and places the next one right after; `Close` builds the real `EntryBuilder`, `Encode`s it, and `WriteAt`s the bytes into the reserved slot — **with no check that actual ≤ reserved.** The L0 metadata is reserved with a fixed 2 KiB *guess*, so a 142 KB ICC (or a >2 KB ImageDescription) overruns into the next IFD = silent corruption. This task **replaces the guess with an exact upper-bound sum** and **adds a Close-time bounds-check**, then emits ICC. (Steps 1–4 add the field/emit; Steps 5–6 do the layout robustness.)

- [ ] **Step 1: Write the failing test** — create `internal/tiff/cogwsiwriter/icc_test.go`. Mirror an existing cogwsiwriter test for writer construction (read `internal/tiff/cogwsiwriter/*_test.go` for the exact `Create` / `AddLevel` / tile-write / `Close` pattern used there, and reuse it). The test must:
  1. Build a cog-wsi writer with `Metadata{ICCProfile: make([]byte, 141992)}` plus the minimal required fields the existing tests use.
  2. Write one small pyramid level + close.
  3. Re-read and assert tag 34675 is present with length 141992 (use `tiffinfo` gated on `t.Skip`, or the same read-back mechanism the sibling tests use).

```go
package cogwsiwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
	// add any imports the sibling tests use for tile bytes / specs
)

// TestCOGWSIEmitsICC: a 142 KB ICC profile is budgeted and emitted (tag
// 34675) without corrupting the pre-computed layout.
func TestCOGWSIEmitsICC(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	path := filepath.Join(t.TempDir(), "o.cog.tiff")
	icc := make([]byte, 141992)
	// --- construct writer + 1 small L0 level mirroring an existing
	// --- cogwsiwriter test, with opts.Metadata.ICCProfile = icc, then Close.
	// (Fill in from the sibling test pattern.)
	_ = icc
	_ = path
	out, _ := exec.Command("tiffinfo", path).CombinedOutput()
	got := strings.ToLower(string(out))
	if !strings.Contains(got, "34675") && !strings.Contains(got, "icc profile") {
		t.Errorf("ICC tag not in cog-wsi output:\n%s", out)
	}
}
```

NOTE: this is the one test whose construction boilerplate must be copied from a sibling test (the cogwsiwriter test API is verbose). Read `internal/tiff/cogwsiwriter/writer_test.go` (or `resolution_tags_test.go` added in the scale-metadata work) and replicate its minimal-writer setup exactly, adding `ICCProfile: icc` to the `Metadata`. If you can't find a clear sibling pattern, report NEEDS_CONTEXT rather than guessing the API.

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/tiff/cogwsiwriter/ -run TestCOGWSIEmitsICC`
Expected: FAIL — `unknown field 'ICCProfile'` (compile error), or the assertion fails.

- [ ] **Step 3: Add the Metadata field** — in `internal/tiff/cogwsiwriter/writer.go`, add to the `Metadata` struct (near `MPPX, MPPY`):

```go
	ICCProfile []byte // embedded color profile; emitted on L0 as tag 34675
```

- [ ] **Step 4: Emit on L0** — in `populateLevelIFD`, inside the L0 metadata block (right after the `Magnification` emission), add:

```go
		if len(opts.Metadata.ICCProfile) > 0 {
			b.AddUndefined(tiff.TagICCProfile, opts.Metadata.ICCProfile)
		}
```

- [ ] **Step 5: Replace the 2048 metadata guess with an exact upper-bound sum**

The L0 external reserve is a fixed `external += 2048` guess that can't fit a 142 KB ICC (and is latently fragile for a >2 KB ImageDescription). Replace it with the *actual* summed byte size of the L0 metadata values, ICC included.

(a) Add a helper in `internal/tiff/cogwsiwriter/writer.go` (near `populateLevelIFD`):

```go
// l0MetadataExternalBytes is a safe upper bound on the external bytes the
// L0 metadata tags consume. It sums each value's full byte length as if
// external (inline values only make this an over-estimate, never under),
// so the layout never under-reserves. Mirror populateLevelIFD's L0 block:
// when you add/remove an L0 metadata tag, update this too. The Close-time
// bounds-check (Step 6) is the backstop if they ever drift.
func l0MetadataExternalBytes(opts Options) uint64 {
	asciiLen := func(s string) uint64 {
		if s == "" {
			return 0
		}
		return uint64(len(s)) + 1 // NUL terminator
	}
	var n uint64
	n += asciiLen(opts.Metadata.SourceImageDesc)
	n += asciiLen(opts.Metadata.Make)
	n += asciiLen(opts.Metadata.Model)
	n += asciiLen(opts.Metadata.Software)
	if !opts.Metadata.AcquisitionDateTime.IsZero() {
		n += 20 // "YYYY:MM:DD HH:MM:SS\0"
	}
	n += asciiLen(opts.Metadata.SourceFormat)
	n += asciiLen(opts.ToolsVersion)
	n += 3 * 8 // WSIMPPX, WSIMPPY, WSIMagnification (DOUBLE, 8 bytes each)
	n += 2 * 8 // XResolution, YResolution (RATIONAL, 8 bytes each)
	n += uint64(len(opts.Metadata.ICCProfile))
	return n
}
```

(b) Add a field to `levelLayoutInput` (`layout.go`) and drop reliance on the 2048 constant:

```go
	IsL0          bool   // true for pyramid index 0 — gets the L0 metadata tags
	L0MetaExternal uint64 // exact upper-bound external bytes for L0 metadata (incl. ICC); 0 for non-L0
```

(c) Where `levelLayoutInput{...}` is built in `writer.go`, set it for L0 (match the existing `IsL0:` assignment in that literal; `opts` is in scope):

```go
			L0MetaExternal: func() uint64 {
				if isL0 { // use the same predicate the IsL0 field uses
					return l0MetadataExternalBytes(opts)
				}
				return 0
			}(),
```

(d) In `ifdSizeForLevel` (`layout.go`), replace the whole `if lv.IsL0 { external += 2048 }` block with:

```go
	if lv.IsL0 {
		// Exact upper-bound external size for the L0 metadata tags
		// (ImageDescription, scanner strings, MPP/mag doubles, resolution,
		// ICC). Replaces the old fixed 2 KiB guess.
		external += lv.L0MetaExternal
	}
```

(e) In `countTagsForLevel`, bump the L0 allowance by one for the ICC entry — change `n += 13` to `n += 14` and update its comment to add `ICCProfile`. (Always reserving the slot is harmless: the record is written at its real entry count, so an unused slot is slack, never overflow.)

- [ ] **Step 6: Add the Close-time bounds-check (the backstop)**

Reserve sizes are now exact, but add a hard guard so any future drift fails loudly instead of silently corrupting.

(a) In `layout.go`, add a `Reserved uint64` field to the per-IFD plan structs (`levelLayoutPlan` and `associatedLayoutPlan`). In `planLayout`, where the cursor advances, record it:

```go
		ifdSize, externalSize := ifdSizeForLevel(lv, useBig)
		plan.Levels[i].IFDOffset = cursor
		plan.Levels[i].Reserved = ifdSize + externalSize
		cursor += ifdSize + externalSize
```
(and the analogous two lines in the associated loop using `ifdSizeForAssociated`.)

(b) In `writer.go` `Close`, immediately after each `ifd, ext, err := b.Encode(...)` for a level and for an associated image, add:

```go
		if got := uint64(len(ifd) + len(ext)); got > plan.Levels[i].Reserved {
			return fmt.Errorf("cogwsi: level %d IFD+external %d bytes exceeds reserved %d (layout sizing bug)", i, got, plan.Levels[i].Reserved)
		}
```
(use `plan.Associated[i].Reserved` in the associated loop).

- [ ] **Step 7: Run, expect PASS — including a long-ImageDescription regression**

Run: `go test ./internal/tiff/cogwsiwriter/ -run TestCOGWSIEmitsICC -race -count=1 -v` → PASS (or SKIP without tiffinfo).
Add one more test in `icc_test.go` that writes a cog-wsi with `Metadata{SourceImageDesc: strings.Repeat("x", 5000)}` (no ICC) and asserts `Close` succeeds and the file re-reads — this would have silently corrupted under the old 2048 guess (>2 KB description). Run it; expect PASS.
Run: `go test ./internal/tiff/cogwsiwriter/ -count=1` (all existing layout/round-trip tests pass — they validate well-formedness) and `go vet ./internal/tiff/cogwsiwriter/`.

- [ ] **Step 8: Commit**

```bash
git add internal/tiff/cogwsiwriter/writer.go internal/tiff/cogwsiwriter/layout.go internal/tiff/cogwsiwriter/icc_test.go
git commit -m "feat(cogwsiwriter): exact L0-metadata layout sizing + bounds-check; emit ICC

Replaces the fixed 2 KiB L0-metadata reserve with an exact upper-bound
sum (fits ICC + long ImageDescriptions), and adds a Close-time guard that
emitted IFD+external never exceeds the reserved slot. Emits ICC (34675).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: wire the callers

**Files:**
- Modify: `cmd/wsitools/convert_tiff.go` (two `streamwriter.Options` literals)
- Modify: `cmd/wsitools/convert_cogwsi.go` (`cogwsiwriter.Metadata` literal)
- Modify: `cmd/wsitools/downsample.go` (`streamwriter.Options` literal)

- [ ] **Step 1: convert_tiff — both literals**

In `cmd/wsitools/convert_tiff.go`, both `streamwriter.Options{...}` literals (functions around lines 90 and 283; `md := src.Metadata()` is in scope in each) — add:

```go
		MPPX:           md.MPPX,
		MPPY:           md.MPPY,
		Magnification:  md.Magnification,
		ICCProfile:     md.ICCProfile,
	}
```

(Insert `ICCProfile: md.ICCProfile,` alongside the existing scale fields in each literal.)

- [ ] **Step 2: convert_cogwsi**

In `cmd/wsitools/convert_cogwsi.go`, the `cogwsiwriter.Metadata{...}` literal — add `ICCProfile: md.ICCProfile,`:

```go
			MPPX:                md.MPPX,
			MPPY:                md.MPPY,
			Magnification:       md.Magnification,
			ICCProfile:          md.ICCProfile,
```

- [ ] **Step 3: downsample — directly from the opentile Slide**

`downsample.go` opens via `opentile.OpenFile` (`src *opentile.Slide`), not `internal/source`, so ICC comes straight from `src.ICCProfile()`. In the `streamwriter.Create(dsOutput, streamwriter.Options{...})` literal, add:

```go
		MPPX:             desc.MPP,
		MPPY:             desc.MPP,
		Magnification:    desc.AppMag,
		ICCProfile:       src.ICCProfile(),
	}
```

(Insert `ICCProfile: src.ICCProfile(),` alongside the existing fields. `src` is the `*opentile.Slide` in scope in `runDownsample`.)

- [ ] **Step 4: Build**

Run: `make build` (ignore the `duplicate libraries` linker warning); `go vet ./cmd/wsitools/`.
Expected: clean.

- [ ] **Step 5: Quick smoke**

```bash
./bin/wsitools convert --to cog-wsi -f -o /tmp/i.cog.tiff sample_files/svs/CMU-1.svs && \
  ./bin/wsitools dump-ifds --raw /tmp/i.cog.tiff | grep -i 34675
rm -f /tmp/i.cog.tiff
```
Expected: shows `34675 (ICCProfile) UNDEFINED count=141992`. Paste it in your report.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert_tiff.go cmd/wsitools/convert_cogwsi.go cmd/wsitools/downsample.go
git commit -m "feat(cli): pass ICC profile into writers (convert + downsample)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: integration acceptance + full verification

**Files:**
- Modify: `cmd/wsitools/scale_metadata_test.go` (add ICC cases)

- [ ] **Step 1: Add the acceptance tests** — append to `cmd/wsitools/scale_metadata_test.go` (the `dumpRaw`, `stripedBinary`, `stripedSample` helpers already exist there):

```go
// iccLen returns the byte count of tag 34675 in `dump-ifds --raw` output,
// or -1 if absent. Parses the "34675 (ICCProfile) UNDEFINED count=NNN" line.
func iccLen(raw string) int {
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(l, "34675") {
			i := strings.Index(l, "count=")
			if i < 0 {
				return -1
			}
			n := 0
			for _, c := range l[i+6:] {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return -1
}

// TestICCByteIdenticalAcrossTargets: CMU-1.svs's 141,992-byte ICC profile
// is present with the same length in every TIFF target + downsample.
func TestICCByteIdenticalAcrossTargets(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1.svs")
	const wantLen = 141992
	cases := []struct{ args []string; out string }{
		{[]string{"convert", "--to", "svs"}, "o.svs"},
		{[]string{"convert", "--to", "tiff"}, "o.tiff"},
		{[]string{"convert", "--to", "ome-tiff"}, "o.ome.tiff"},
		{[]string{"convert", "--to", "cog-wsi"}, "o.cog.tiff"},
		{[]string{"downsample", "--factor", "2"}, "o.ds.svs"},
	}
	for _, c := range cases {
		t.Run(c.out, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), c.out)
			args := append(append([]string{}, c.args...), "-f", "-o", out, src)
			if cmdOut, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
				if strings.Contains(string(cmdOut), "no space left on device") {
					t.Skipf("disk full: %s", cmdOut)
				}
				t.Fatalf("%v: %v\n%s", c.args, err, cmdOut)
			}
			if got := iccLen(dumpRaw(t, bin, out)); got != wantLen {
				t.Errorf("ICC length in %s = %d, want %d", c.out, got, wantLen)
			}
		})
	}
}

// TestNoICCWhenSourceLacksIt: a source with no ICC emits no tag 34675.
func TestNoICCWhenSourceLacksIt(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/scan_620_.svs") // no ICC
	out := filepath.Join(t.TempDir(), "o.cog.tiff")
	if cmdOut, err := exec.Command(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, cmdOut)
	}
	if got := iccLen(dumpRaw(t, bin, out)); got != -1 {
		t.Errorf("expected no ICC tag, got length %d", got)
	}
}
```

- [ ] **Step 2: Run the ICC integration tests**

Run:
```bash
make build
WSI_TOOLS_TESTDIR="$PWD/sample_files" go test ./cmd/wsitools/ -run 'TestICCByteIdenticalAcrossTargets|TestNoICCWhenSourceLacksIt' -v
```
Expected: PASS (all 5 target subcases show count 141992; the no-ICC case has none). If CMU-1.svs `convert --to svs/tiff` is slow (177 MB), allow up to a few minutes; if a subcase times out, note it but the cog-wsi/downsample/svs paths must pass.

- [ ] **Step 3: Full suite + vet**

Run:
```bash
WSI_TOOLS_TESTDIR="$PWD/sample_files" go test ./... -race -count=1 -timeout 600s 2>&1 | grep -v 'duplicate librar' | grep -E 'FAIL|panic|^ok'
make vet
```
Expected: every package `ok`; vet clean. (Absolute `WSI_TOOLS_TESTDIR` is required.)

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/scale_metadata_test.go
git commit -m "test(cli): ICC profile byte-identical across all targets + downsample

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review notes
- **Spec coverage:** D1 `Slide.ICCProfile()` (Task 1) ✓; D2 L0/34675/UNDEFINED (Tasks 2,3) ✓; D3 via `source.Metadata.ICCProfile` (Task 1, convert callers Task 4) ✓; D4 carry-only/no override (no generated 34675 anywhere) ✓; D5 empty → nothing (`len>0` guards in Tasks 2,3) ✓; D6 downsample carries (Task 4 Step 3) ✓. §3.3 cog-wsi external-byte budget (Task 3 Steps 5–6) ✓. §5 tests: source (T1), streamwriter presence (T2), cogwsi layout/valid (T3), integration byte-identical + no-ICC (T5) ✓.
- **Type consistency:** `Options.ICCProfile`/`Writer.iccProfile` (T2) ↔ callers (T4). `Metadata.ICCProfile` (cogwsi, T3) ↔ `convert_cogwsi` (T4). `source.Metadata.ICCProfile` (T1) ↔ convert callers (T4). `levelLayoutInput.ICCLen` (T3 Step 5) ↔ `countTagsForLevel`/`ifdSizeForLevel` (T3 Step 6). `tiff.TagICCProfile` (T2 Step 3) used in T2/T3.
- **Task 3 scope note:** beyond ICC, Task 3 reworks the cog-wsi layout — replaces the fixed 2 KiB L0-metadata reserve with an exact upper-bound sum (`l0MetadataExternalBytes`, threaded as `levelLayoutInput.L0MetaExternal`) and adds a `Close`-time bounds-check (`plan.Levels[i].Reserved`). This fixes a pre-existing latent corruption (a >2 KB ImageDescription), regression-tested in Step 7. The `l0MetadataExternalBytes` sum must mirror `populateLevelIFD`'s L0 block; the bounds-check is the backstop if they drift.
- **Known soft spot (flagged in the task):** Task 3's test boilerplate must be copied from a sibling cogwsiwriter test — the implementer is told to read one and replicate, or report NEEDS_CONTEXT.
- **Downsample asymmetry:** ICC via `src.ICCProfile()` (raw opentile Slide), not `md.ICCProfile`, because downsample doesn't use `internal/source` — verified in code.
```
