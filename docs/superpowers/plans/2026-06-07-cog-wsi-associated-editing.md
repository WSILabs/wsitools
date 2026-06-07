# COG-WSI associated-image editing (Slice 2a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `wsitools label|macro|thumbnail|overview remove|replace` work on COG-WSI files via a `cogwsiwriter` re-finalize, preserving everything except the targeted associated image.

**Architecture:** A shared `writeCOGWSI(w, src, plan)` core (extracted from `runConvertCOGWSI`) copies pyramid tiles verbatim and writes associated images per an `assocEditPlan` (skip/replace/append/dropAll). A new `rebuildCOGWSI` creates the writer at a temp path, runs `writeCOGWSI`, and atomically renames over the output. The `associated.go` dispatch routes `cog-wsi` to this engine; `svs`/`generic-tiff` keep the Slice-1 splice.

**Tech Stack:** Go, cobra, `internal/tiff/cogwsiwriter`, opentile-go reader, reused Slice-1 image helpers.

**Spec:** `docs/superpowers/specs/2026-06-07-cog-wsi-associated-editing-design.md`

**Branch:** create `feat/cog-wsi-associated-editing` off `main`. Never implement on `main`.

**Key API facts (verified):**
- `cogwsiwriter.Create(path, Options{BigTIFF, ToolsVersion, DefaultOrder, Metadata})`; for the rebuild use `BigTIFF: cogwsiwriter.BigTIFFAuto`, `DefaultOrder: nil` (→ row-major).
- `Metadata{MPPX, MPPY, Magnification, Make, Model, Software, AcquisitionDateTime, SourceFormat, SourceImageDesc, ICCProfile}`.
- `w.AddLevel(LevelSpec{ImageWidth, ImageHeight, TileWidth, TileHeight, Compression, Photometric, BitsPerSample, SamplesPerPixel, IsL0})` → `*LevelHandle`; `h.WriteTile(tx, ty, compressed)`.
- `w.AddAssociated(AssociatedSpec{Type, Width, Height, Compression, Photometric, BitsPerSample, SamplesPerPixel, Bytes})`; returns `ErrInvalidAssocType` for unknown types.
- `w.Abort()`, `w.Close()`.
- `compressionTagFor(src Compression) uint16` (existing helper in cmd/wsitools).
- Source: `src.Levels()` (each `Size()/TileSize()/Grid()/TileMaxSize()/TileInto(tx,ty,buf)/Compression()/Index()`), `src.Associated()` (each `Type()/Size()/Bytes()/Compression()`), `src.Metadata()`.

---

## File Structure

| Path | Responsibility |
|---|---|
| `cmd/wsitools/associated_rebuild.go` (new) | `assocEditPlan`, `writeCOGWSI`, `rebuildCOGWSI`, cog-wsi remove/replace entry points |
| `cmd/wsitools/convert_cogwsi.go` (modify) | call shared `writeCOGWSI` (was inline level+assoc loops) |
| `cmd/wsitools/associated.go` (modify) | allow `cog-wsi` in `assocFormatSupported`; dispatch cog-wsi → rebuild |
| `cmd/wsitools/associated_replace.go` (modify) | add `buildReplacementAssocSpec` (image → `cogwsiwriter.AssociatedSpec`) |
| `cmd/wsitools/associated_rebuild_test.go` (new) | unit: plan application + spec packaging |
| `cmd/wsitools/associated_integration_test.go` (extend) | gated cog-wsi remove/replace/in-place |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md` | docs |

---

## Task 1: Extract shared `writeCOGWSI` + `assocEditPlan` (refactor, non-regression)

**Files:**
- Create: `cmd/wsitools/associated_rebuild.go`
- Modify: `cmd/wsitools/convert_cogwsi.go` (replace the inline level+associated loops with a call to `writeCOGWSI`)

- [ ] **Step 1: Add the plan type + shared writer in `associated_rebuild.go`**

```go
package main

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

// assocEditPlan parameterizes writeCOGWSI's associated-image output.
// At most one of remove/replace is set. The empty plan writes all associated
// images verbatim; dropAll writes none (convert's --no-associated).
type assocEditPlan struct {
	remove  string                       // type to drop ("" = none)
	replace string                       // type to substitute/append ("" = none)
	spec    *cogwsiwriter.AssociatedSpec // replacement (when replace != "")
	dropAll bool                         // write no associated images
}

// writeCOGWSI copies the source pyramid (verbatim tiles) into w and writes its
// associated images per plan. It does NOT Abort/Close w — the caller owns the
// writer lifecycle. Pyramid tile bytes are copied unmodified (no re-encode).
func writeCOGWSI(w *cogwsiwriter.Writer, src source.Source, plan assocEditPlan) error {
	for _, lvl := range src.Levels() {
		spec := cogwsiwriter.LevelSpec{
			ImageWidth:      uint32(lvl.Size().X),
			ImageHeight:     uint32(lvl.Size().Y),
			TileWidth:       uint32(lvl.TileSize().X),
			TileHeight:      uint32(lvl.TileSize().Y),
			Compression:     compressionTagFor(lvl.Compression()),
			Photometric:     2,
			SamplesPerPixel: 3,
			BitsPerSample:   []uint16{8, 8, 8},
			IsL0:            lvl.Index() == 0,
		}
		h, err := w.AddLevel(spec)
		if err != nil {
			return fmt.Errorf("add level %d: %w", lvl.Index(), err)
		}
		buf := make([]byte, lvl.TileMaxSize())
		grid := lvl.Grid()
		for ty := 0; ty < grid.Y; ty++ {
			for tx := 0; tx < grid.X; tx++ {
				n, err := lvl.TileInto(tx, ty, buf)
				if err != nil {
					return fmt.Errorf("read tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
				if err := h.WriteTile(uint32(tx), uint32(ty), buf[:n]); err != nil {
					return fmt.Errorf("write tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
			}
		}
	}

	if plan.dropAll {
		return nil
	}

	replaced := false
	for _, a := range src.Associated() {
		if a.Type() == plan.remove {
			continue
		}
		if plan.replace != "" && a.Type() == plan.replace {
			if err := w.AddAssociated(*plan.spec); err != nil {
				return fmt.Errorf("add replacement %s: %w", plan.replace, err)
			}
			replaced = true
			continue
		}
		bs, err := a.Bytes()
		if err != nil {
			return fmt.Errorf("read associated %s: %w", a.Type(), err)
		}
		spec := cogwsiwriter.AssociatedSpec{
			Type:        a.Type(),
			Width:       uint32(a.Size().X),
			Height:      uint32(a.Size().Y),
			Compression: compressionTagFor(a.Compression()),
			Photometric: 2,
			Bytes:       bs,
		}
		if err := w.AddAssociated(spec); err != nil {
			if errors.Is(err, cogwsiwriter.ErrInvalidAssocType) {
				slog.Warn("skipping associated image with unsupported type", "type", a.Type(), "reason", err)
				continue
			}
			return fmt.Errorf("add associated %s: %w", a.Type(), err)
		}
	}
	// Upsert: replace of an absent type appends the new image.
	if plan.replace != "" && !replaced {
		if err := w.AddAssociated(*plan.spec); err != nil {
			return fmt.Errorf("add new %s: %w", plan.replace, err)
		}
	}
	return nil
}
```

- [ ] **Step 2: Rewrite `runConvertCOGWSI` to use `writeCOGWSI`**

In `convert_cogwsi.go`, replace the inline level-copy loop (`convert_cogwsi.go:84–117`) AND the `if !cvNoAssociated { … }` associated block (`:119–143`) with:

```go
	plan := assocEditPlan{dropAll: cvNoAssociated}
	if err := writeCOGWSI(w, src, plan); err != nil {
		w.Abort()
		return err
	}
```

Leave `cogwsiwriter.Create(...)`, the `opts`/`md` block, and `w.Close()` as-is.

- [ ] **Step 3: Build + run convert's cog-wsi non-regression tests**

Run: `go build ./...` (benign linker warning OK), then
`WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'COGWSI|CogWsi|cogwsi' -count=1`
Expected: PASS — convert behavior unchanged.

- [ ] **Step 4: Pixel-exact spot check**

Run:
```bash
go build -o /tmp/wsit ./cmd/wsitools
/tmp/wsit convert --to cog-wsi sample_files/cog-wsi/CMU-1-Small-Region_cog-wsi.tiff -o /tmp/rt.tiff --force
diff <(/tmp/wsit hash --mode pixel sample_files/cog-wsi/CMU-1-Small-Region_cog-wsi.tiff) \
     <(/tmp/wsit hash --mode pixel /tmp/rt.tiff) && echo PIXEL-IDENTICAL
```
Expected: prints `PIXEL-IDENTICAL` (refactor preserved the byte-exact tile copy).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_rebuild.go cmd/wsitools/convert_cogwsi.go
git commit -m "refactor(convert): extract shared writeCOGWSI(w,src,plan) core

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `rebuildCOGWSI` (atomic temp→rename) + cog-wsi remove dispatch

**Files:**
- Modify: `cmd/wsitools/associated_rebuild.go` (add `rebuildCOGWSI`, `runAssociatedRemoveForCOGWSI`)
- Modify: `cmd/wsitools/associated.go` (allow cog-wsi; dispatch remove)

- [ ] **Step 1: Add `rebuildCOGWSI` + remove entry point**

Append to `associated_rebuild.go`:

```go
import (
	"os"
	"time"
	// (merge with existing imports)
)

// rebuildCOGWSI re-finalizes src as a COG-WSI at outPath with plan applied. It
// writes to a sibling temp file then atomically renames over outPath, so both
// -o and --in-place (outPath == input) are crash-safe.
func rebuildCOGWSI(src source.Source, outPath string, plan assocEditPlan, fsync bool) error {
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
	md := src.Metadata()
	tmp := fmt.Sprintf("%s.tmp-%d", outPath, os.Getpid())
	opts := cogwsiwriter.Options{
		BigTIFF:      cogwsiwriter.BigTIFFAuto,
		ToolsVersion: Version,
		Metadata: cogwsiwriter.Metadata{
			MPPX:                md.MPPX,
			MPPY:                md.MPPY,
			Magnification:       md.Magnification,
			ICCProfile:          md.ICCProfile,
			Make:                md.Make,
			Model:               md.Model,
			Software:            md.Software,
			AcquisitionDateTime: md.AcquisitionDateTime,
			SourceFormat:        src.Format(),
			SourceImageDesc:     fmt.Sprintf("wsitools/%s %s source=%s", Version, "associated-edit", src.Format()),
		},
	}
	w, err := cogwsiwriter.Create(tmp, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	if err := writeCOGWSI(w, src, plan); err != nil {
		w.Abort()
		os.Remove(tmp)
		return err
	}
	if err := w.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("finalize output: %w", err)
	}
	if fsync {
		if f, e := os.Open(tmp); e == nil {
			_ = f.Sync()
			_ = f.Close()
		}
	}
	if err := os.Rename(tmp, outPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// runAssociatedRemoveForCOGWSI removes the typ associated image from a COG-WSI.
func runAssociatedRemoveForCOGWSI(typ, input, outPath string, fl removeFlags) error {
	src, err := source.Open(input)
	if err != nil {
		return err
	}
	defer src.Close()
	// Confirm the type is present (contract: removing an absent image is an error).
	found := false
	for _, a := range src.Associated() {
		if a.Type() == typ {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no %s image in slide", typ)
	}
	if err := rebuildCOGWSI(src, outPath, assocEditPlan{remove: typ}, fl.fsync); err != nil {
		return err
	}
	if !fl.quiet {
		fmt.Printf("wsitools: removed %s: %s -> %s\n", typ, input, outPath)
	}
	return nil
}
```

- [ ] **Step 2: Allow cog-wsi + dispatch remove in `associated.go`**

In `assocFormatSupported` (`associated.go:62`) add `"cog-wsi"` to the accepted set. In `runAssociatedRemoveFor` (`:165`), after `gateFormat`, branch by format before the splice path:

```go
	if src.Format() == "cog-wsi" {
		src.Close() // re-opened inside the cog-wsi engine
		return runAssociatedRemoveForCOGWSI(typ, input, outPath, fl)
	}
```

(Place this right after `gateFormat(src)` succeeds and before the `edit.Parse`/splice logic. `src` is already open in `runAssociatedRemoveFor`; close it before delegating since the cog-wsi engine opens its own handle. If the existing function holds `src` via `defer src.Close()`, instead pass the open `src` into the engine — adapt to the actual structure: simplest is to detect format first and delegate before opening the splice path. Verify against the current function body and choose the cleaner of: (a) delegate before `defer`, or (b) hand the open `src` to the engine and drop the engine's own `source.Open`.)**

- [ ] **Step 3: Write the failing integration test**

Add to `cmd/wsitools/associated_integration_test.go`:

```go
func cogwsiFixture(t *testing.T) string {
	p := firstExisting(t, "cog-wsi/CMU-1_cog-wsi.tiff", "cog-wsi/CMU-1-Small-Region_cog-wsi.tiff")
	if p == "" {
		t.Skip("no cog-wsi fixture")
	}
	return p
}

func TestCOGWSILabelRemove(t *testing.T) {
	in := copyFile(t, cogwsiFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("fixture has no label")
	}
	out := filepath.Join(t.TempDir(), "out.tiff")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedRemoveForCOGWSI("label", in, out, removeFlags{assocCommonFlags{fsync: false}}); err != nil {
		t.Fatalf("cog-wsi label remove: %v", err)
	}
	// Output reopens as conformant cog-wsi.
	osrc, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := osrc.Format(); got != "cog-wsi" {
		t.Errorf("output format = %q, want cog-wsi", got)
	}
	osrc.Close()
	// Label gone; other associated images survive.
	if _, _, ok := assocOfType(t, out, "label"); ok {
		t.Errorf("label still present")
	}
	if _, _, ok := assocOfType(t, out, "overview"); !ok {
		t.Errorf("overview vanished (contract: only target changes)")
	}
	// Pyramid pixels identical.
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed")
	}
}
```

- [ ] **Step 4: Run it**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run TestCOGWSILabelRemove -v`
Expected: PASS (or SKIP if no fixture). If the dispatch in Step 2 wasn't reached, the test calls `runAssociatedRemoveForCOGWSI` directly so it still exercises the engine; ALSO smoke-test the CLI path: `/tmp/wsit label remove sample_files/cog-wsi/CMU-1_cog-wsi.tiff -o /tmp/c.tiff && /tmp/wsit info /tmp/c.tiff | grep -i label` (expect no label line).

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/associated_rebuild.go cmd/wsitools/associated.go cmd/wsitools/associated_integration_test.go
git commit -m "feat(cmd): cog-wsi associated remove via cogwsiwriter re-finalize

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: cog-wsi replace (image → AssociatedSpec)

**Files:**
- Modify: `cmd/wsitools/associated_replace.go` (add `buildReplacementAssocSpec`)
- Modify: `cmd/wsitools/associated_rebuild.go` (add `runAssociatedReplaceForCOGWSI`)
- Modify: `cmd/wsitools/associated.go` (dispatch replace)

- [ ] **Step 1: Add the AssociatedSpec packager**

In `associated_replace.go`, add a function that runs the same decode→resize→encode pipeline as `buildReplacementIFD` but returns a `cogwsiwriter.AssociatedSpec`. Reuse `fitImage`, `encodeLZWStrips`, the JPEG/deflate/raw encoders, and the codec-default logic. Concatenate the LZW/deflate/raw strips into a single payload only if the writer expects one blob — COG-WSI associated images are single-strip blobs, so for `lzw`/`deflate`/`none` encode the WHOLE image as one strip (not 2-row strips). Add:

```go
import "github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"

// buildReplacementAssocSpec encodes img as a cogwsiwriter.AssociatedSpec for the
// given type/options. COG-WSI associated images are single-strip self-contained
// payloads, so JPEG is a full JFIF and LZW/deflate/none is one whole-image strip.
func buildReplacementAssocSpec(img image.Image, o replaceOpts) (*cogwsiwriter.AssociatedSpec, error) {
	codec := o.compression
	if codec == "" {
		if o.typ == "label" {
			codec = "lzw"
		} else {
			codec = "jpeg"
		}
	}
	if o.targetW != 0 || o.targetH != 0 {
		prepared, err := fitImage(img, o)
		if err != nil {
			return nil, err
		}
		img = prepared
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	var payload []byte
	var compTag uint16
	switch codec {
	case "jpeg":
		buf, err := encodeJPEGWhole(img) // existing JFIF encoder used by buildJPEGReplacementIFD
		if err != nil {
			return nil, err
		}
		payload, compTag = buf, 7
	case "lzw":
		payload, compTag = encodeLZWWhole(img), 5 // predictor-2 whole-image strip
	case "deflate":
		payload, compTag = encodeDeflateWhole(img), 8
	case "none":
		payload, compTag = rgbStripBytes(img), 1
	default:
		return nil, fmt.Errorf("unknown compression %q (want jpeg, lzw, deflate, none)", codec)
	}

	return &cogwsiwriter.AssociatedSpec{
		Type:            o.typ,
		Width:           uint32(w),
		Height:          uint32(h),
		Compression:     compTag,
		Photometric:     2,
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		Bytes:           payload,
	}, nil
}
```

If `encodeJPEGWhole`/`encodeLZWWhole`/`encodeDeflateWhole` don't exist as single-blob helpers, factor them from the existing `buildJPEGReplacementIFD` (already encodes a whole-image JFIF) and `encodeLZWStrips` (change strip height to the full image, or concatenate — but a single whole-image strip is required for the associated blob; predictor-2 over the whole image, one LZW stream). Verify the resulting `Bytes` decodes (Step 4).

- [ ] **Step 2: Add the cog-wsi replace entry point**

In `associated_rebuild.go`:

```go
func runAssociatedReplaceForCOGWSI(typ, input, outPath string, fl replaceFlags) error {
	src, err := source.Open(input)
	if err != nil {
		return err
	}
	defer src.Close()

	var existing source.AssociatedImage
	for _, a := range src.Associated() {
		if a.Type() == typ {
			existing = a
		}
	}
	img, err := decodeReplacementImage(fl.image)
	if err != nil {
		return err
	}
	tw, th, err := resolveTargetDims(typ, img, existing, existing != nil, fl.labelDims)
	if err != nil {
		return err
	}
	bg, err := parseHexColor(fl.bgHex)
	if err != nil {
		return err
	}
	resize := fl.resize
	if resize == "" {
		resize = "fit"
	}
	spec, err := buildReplacementAssocSpec(img, replaceOpts{
		typ: typ, compression: fl.compression, resize: resize,
		bg: bg, targetW: tw, targetH: th, force: fl.force,
	})
	if err != nil {
		return err
	}
	if err := rebuildCOGWSI(src, outPath, assocEditPlan{replace: typ, spec: spec}, fl.fsync); err != nil {
		return err
	}
	if !fl.quiet {
		verb := "replaced"
		if existing == nil {
			verb = "added"
		}
		fmt.Printf("wsitools: %s %s: %s -> %s\n", verb, typ, input, outPath)
	}
	return nil
}
```

- [ ] **Step 3: Dispatch replace in `associated.go`**

In `runAssociatedReplaceFor` (`:206`), after `gateFormat`, before the SVS gate / splice path:

```go
	if src.Format() == "cog-wsi" {
		src.Close()
		return runAssociatedReplaceForCOGWSI(typ, input, outPath, fl)
	}
```

- [ ] **Step 4: Integration test (replace round-trip)**

Add to `associated_integration_test.go`:

```go
func TestCOGWSIOverviewReplaceRoundTrips(t *testing.T) {
	in := copyFile(t, cogwsiFixture(t))
	ovSize, _, ok := assocOfType(t, in, "overview")
	if !ok {
		t.Skip("fixture has no overview")
	}
	png := filepath.Join(t.TempDir(), "x.png")
	writeSolidPNG(t, png, ovSize.X, ovSize.Y, color.RGBA{200, 30, 40, 255})
	out := filepath.Join(t.TempDir(), "out.tiff")
	digestBefore := level0Digest(t, in)
	if err := runAssociatedReplaceForCOGWSI("overview", in, out, replaceFlags{
		assocCommonFlags: assocCommonFlags{fsync: false}, image: png, compression: "jpeg", bgHex: "F5F5E6", force: true,
	}); err != nil {
		t.Fatalf("cog-wsi overview replace: %v", err)
	}
	src, err := source.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var decoded bool
	for _, a := range src.Associated() {
		if a.Type() == "overview" {
			b, err := a.Bytes()
			if err != nil {
				t.Fatalf("overview bytes: %v", err)
			}
			if _, _, err := image.Decode(bytes.NewReader(b)); err != nil {
				t.Fatalf("replaced overview does not decode: %v", err)
			}
			decoded = true
		}
	}
	if !decoded {
		t.Errorf("overview missing/not classified after replace")
	}
	if level0Digest(t, out) != digestBefore {
		t.Errorf("pyramid changed after replace")
	}
}
```

- [ ] **Step 5: Run + smoke-test, commit**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'TestCOGWSI' -v` → PASS.
Smoke: `/tmp/wsit label replace sample_files/cog-wsi/CMU-1_cog-wsi.tiff --image sample_files/generic-tiff/test_png.png --force -o /tmp/cr.tiff && /tmp/wsit extract /tmp/cr.tiff --type label -o /tmp/crl.png && file /tmp/crl.png` (decodes).
```bash
git add cmd/wsitools/associated_replace.go cmd/wsitools/associated_rebuild.go cmd/wsitools/associated.go cmd/wsitools/associated_integration_test.go
git commit -m "feat(cmd): cog-wsi associated replace (image -> AssociatedSpec)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: In-place + metadata-preservation + unit tests

**Files:**
- Modify: `cmd/wsitools/associated_integration_test.go`, create `cmd/wsitools/associated_rebuild_test.go`

- [ ] **Step 1: In-place + metadata test**

```go
func TestCOGWSIRemoveInPlacePreservesMeta(t *testing.T) {
	in := copyFile(t, cogwsiFixture(t))
	if _, _, ok := assocOfType(t, in, "label"); !ok {
		t.Skip("no label")
	}
	mdBefore := func() (float64, float64) {
		s, _ := source.Open(in); defer s.Close()
		m := s.Metadata()
		return m.MPPX, m.Magnification
	}
	mppBefore, magBefore := mdBefore()
	if err := runAssociatedRemoveForCOGWSI("label", in, in, removeFlags{assocCommonFlags{inPlace: true, fsync: true}}); err != nil {
		t.Fatalf("in-place remove: %v", err)
	}
	if _, _, ok := assocOfType(t, in, "label"); ok {
		t.Errorf("label still present after in-place remove")
	}
	s, _ := source.Open(in)
	defer s.Close()
	m := s.Metadata()
	if m.MPPX != mppBefore || m.Magnification != magBefore {
		t.Errorf("metadata changed: MPPX %v->%v, Mag %v->%v", mppBefore, m.MPPX, magBefore, m.Magnification)
	}
	// No leftover temp files.
	ents, _ := os.ReadDir(filepath.Dir(in))
	for _, e := range ents {
		if bytes.Contains([]byte(e.Name()), []byte(".tmp")) {
			t.Errorf("leftover temp: %s", e.Name())
		}
	}
}

func TestCOGWSIRemoveAbsentErrors(t *testing.T) {
	in := copyFile(t, cogwsiFixture(t))
	out := filepath.Join(t.TempDir(), "o.tiff")
	err := runAssociatedRemoveForCOGWSI("no-such-type", in, out, removeFlags{assocCommonFlags{fsync: false}})
	if err == nil {
		t.Fatal("expected error for absent type")
	}
}
```

- [ ] **Step 2: Unit test — plan application (no fixture)**

`associated_rebuild_test.go`: build a tiny fake `source.Source` with 1 level + 3 associated images (label/macro/overview), call `writeCOGWSI` against a `cogwsiwriter.Create` temp, and assert the output's associated set reflects the plan (remove drops one; replace substitutes; dropAll yields none). If a full fake source is heavy, instead unit-test `buildReplacementAssocSpec` (codec tag + dims) and leave plan-application to the gated integration tests — note which you chose.

- [ ] **Step 3: Run full cog-wsi suite + commit**

Run (uncontended): `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'TestCOGWSI|TestBuildReplacement' -race -count=1 -timeout 30m` → PASS.
```bash
git add cmd/wsitools/associated_integration_test.go cmd/wsitools/associated_rebuild_test.go
git commit -m "test(cmd): cog-wsi in-place + metadata-preservation + plan-application

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Docs

**Files:** `README.md`, `CHANGELOG.md`, `docs/roadmap.md`

- [ ] **Step 1: Update docs**

- README format×command matrix: mark COG-WSI ✓ for `label/macro remove|replace` (footnote ⁷). Update the associated-editing prose: remove/replace now supported on SVS, generic-TIFF, **and COG-WSI**; OME-TIFF still "coming next" (footnote ⁸ → Slice 2b).
- CHANGELOG `[Unreleased]`: "Associated-image editing extended to **COG-WSI** (remove + replace, all types) via `cogwsiwriter` re-finalize — pyramid pixels preserved verbatim, all other images + MPP/mag/ICC carried; only the target image changes."
- `docs/roadmap.md`: mark COG-WSI (Slice 2a) DONE; keep OME-TIFF (Slice 2b) as the remaining planned item (raw IFD-graph re-serializer + OME-XML surgery/relocation + verbatim vendor-tag carry).

- [ ] **Step 2: Verify + commit**

Run: `go build ./... && go test ./cmd/wsitools/ -run 'TestAssociated|TestResolveAssoc' -count=1` → PASS.
```bash
git add README.md CHANGELOG.md docs/roadmap.md
git commit -m "docs: associated-image editing now supports COG-WSI (Slice 2a)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final review

After all tasks, dispatch a final code reviewer for the branch (focus: the `writeCOGWSI` extraction preserves convert byte-exactly; the rebuild's atomic temp→rename is correct for `--in-place`; replace round-trips; contract held — only the target image changes). Then use `superpowers:finishing-a-development-branch`. Run the heavy `cmd/wsitools` suite uncontended with `-timeout 30m`.

## Self-review notes (author)

- **Spec coverage:** shared core + plan (Task 1), rebuild + remove (Task 2), replace (Task 3), in-place/metadata/unit (Task 4), docs (Task 5). Contract (only-target-changes), full fidelity (verbatim tiles + carried MPP/mag/ICC), atomic output, and conformant re-finalize all covered.
- **Type consistency:** `assocEditPlan{remove,replace,spec,dropAll}`, `writeCOGWSI(w,src,plan)`, `rebuildCOGWSI(src,outPath,plan,fsync)`, `buildReplacementAssocSpec(img,replaceOpts)→*cogwsiwriter.AssociatedSpec` used consistently across tasks. Reused Slice-1 helpers (`decodeReplacementImage`, `resolveTargetDims`, `parseHexColor`, `fitImage`, `removeFlags`/`replaceFlags`, `level0Digest`, `assocOfType`, `copyFile`, `firstExisting`, `writeSolidPNG`) are all already defined.
- **Non-regression:** Task 1 keeps `convert --to cog-wsi` behavior identical (existing convert tests + pixel-hash check are the net) before any new behavior is added.
- **Open implementation choice flagged:** Step 2/Task 2 dispatch — delegate-before-open vs hand-the-open-src to the engine; the implementer picks the cleaner against the actual function body.
