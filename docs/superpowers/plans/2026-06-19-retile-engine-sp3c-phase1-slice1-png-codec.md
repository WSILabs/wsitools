# SP3c Phase 1 — Slice 1: PNG codec promotion — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote PNG from an inline DZI-local encoder to a first-class
`internal/codec` registry codec, and re-point the DZI/SZI encoder onto it, with
byte-identical output.

**Architecture:** Add `internal/codec/png` (a `codec.EncoderFactory` +
`codec.Encoder` over stdlib `image/png`), register it via `init()` and the
`internal/codec/all` aggregator, then replace the inline `png.Encode` branch in
`cmd/wsitools/convert_dzi.go`'s `dziStandaloneEncoder` with a call to the
registered codec. PNG is encode-only and lossless RGB888; it is **not** a TIFF
tile codec, so `TIFFCompressionTag()` returns 0 and it is conformant only for
DZI/SZI tiles (enforced at the CLI in Slice 2).

**Tech Stack:** Go, `image/png` (stdlib), the `internal/codec` registry.

**Spec:** `docs/superpowers/specs/2026-06-19-retile-engine-sp3c-unified-convert-design.md`
(correction #3 + the `internal/codec/png` component row).

**Branch:** `feat/retile-engine-sp3c` (already created; the SP3c spec is its first
commit). Work proceeds directly on this branch.

---

### Task 1: `internal/codec/png` encoder package

**Files:**
- Create: `internal/codec/png/png.go`
- Test: `internal/codec/png/png_test.go`

PNG is pure Go (no cgo, no build tag) — it joins the base codec set alongside
`jpeg`. The encoding logic is lifted verbatim from `convert_dzi.go`'s inline PNG
branch (`png.Encode` over an `NRGBA`-alpha-255 wrapper) so output is
byte-identical.

- [ ] **Step 1: Write the failing test**

Create `internal/codec/png/png_test.go`:

```go
package png

import (
	"bytes"
	stdpng "image/png"
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

func TestPNGEncoderRoundTrip(t *testing.T) {
	const w, h = 64, 48
	rgb := make([]byte, w*h*3)
	for i := range rgb {
		rgb[i] = byte(i * 7)
	}
	enc, err := (Factory{}).NewEncoder(
		codec.LevelGeometry{TileWidth: w, TileHeight: h, PixelFormat: codec.PixelFormatRGB8},
		codec.Quality{},
	)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	tile, err := enc.EncodeTile(rgb, w, h, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	// PNG 8-byte signature: 0x89 'P' 'N' 'G'.
	if len(tile) < 8 || string(tile[1:4]) != "PNG" {
		t.Fatalf("not a PNG: % X", tile)
	}
	// PNG is lossless: decode back and assert every pixel survives.
	img, err := stdpng.Decode(bytes.NewReader(tile))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("dims: got %v, want %dx%d", img.Bounds(), w, h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			i := y*w*3 + x*3
			if byte(r>>8) != rgb[i] || byte(g>>8) != rgb[i+1] || byte(b>>8) != rgb[i+2] {
				t.Fatalf("pixel (%d,%d) mismatch: got %d,%d,%d want %d,%d,%d",
					x, y, byte(r>>8), byte(g>>8), byte(b>>8), rgb[i], rgb[i+1], rgb[i+2])
			}
		}
	}
}

func TestPNGRegisteredAndNotTIFF(t *testing.T) {
	fac, err := codec.Lookup("png")
	if err != nil {
		t.Fatalf("png not registered: %v", err)
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{}, codec.Quality{})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	if got := enc.TIFFCompressionTag(); got != 0 {
		t.Errorf("TIFFCompressionTag = %d, want 0 (PNG is not a TIFF tile codec)", got)
	}
	if hdr := enc.LevelHeader(); hdr != nil {
		t.Errorf("LevelHeader = %v, want nil (PNG tiles are self-contained)", hdr)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/codec/png/`
Expected: FAIL — `png.go` does not exist yet (build error: undefined `Factory`).

- [ ] **Step 3: Write the implementation**

Create `internal/codec/png/png.go`:

```go
// Package png provides a stdlib image/png-backed PNG tile encoder. PNG tiles are
// lossless RGB888 and self-contained (no shared tables), used for Deep Zoom
// (DZI/SZI) output. PNG is ENCODE-ONLY and is NOT a TIFF tile codec — opentile
// does not read PNG-compressed TIFF tiles — so TIFFCompressionTag returns 0 and
// callers must restrict it to DZI/SZI (enforced at the CLI).
package png

import (
	"bytes"
	"image"
	"image/color"
	stdpng "image/png"

	"github.com/wsilabs/wsitools/internal/codec"
)

func init() { codec.Register(Factory{}) }

// Factory builds PNG encoders. Registered under the name "png".
type Factory struct{}

func (Factory) Name() string { return "png" }

func (Factory) NewEncoder(_ codec.LevelGeometry, _ codec.Quality) (codec.Encoder, error) {
	return &Encoder{}, nil
}

// Encoder encodes RGB888 tiles as standalone PNG. Stateless and safe to reuse.
type Encoder struct{}

// LevelHeader returns nil: PNG tiles carry no shared header/tables.
func (e *Encoder) LevelHeader() []byte { return nil }

// EncodeTile encodes w×h RGB888 pixels as a complete PNG. The dst hint is
// ignored (image/png allocates its own buffer).
func (e *Encoder) EncodeTile(rgb []byte, w, h int, _ []byte) ([]byte, error) {
	var b bytes.Buffer
	if err := stdpng.Encode(&b, &rgbImage{pix: rgb, stride: w * 3, w: w, h: h}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// TIFFCompressionTag returns 0: PNG is not a TIFF tile codec (DZI/SZI only).
func (e *Encoder) TIFFCompressionTag() uint16 { return 0 }

func (e *Encoder) Close() error { return nil }

// rgbImage wraps a raw RGB888 byte buffer as image.Image. It reports NRGBA with
// alpha hard-coded to 255 (opaque) — identical to convert_dzi.go's prior inline
// wrapper, so PNG output bytes are unchanged.
type rgbImage struct {
	pix    []byte
	stride int
	w, h   int
}

func (r *rgbImage) ColorModel() color.Model { return color.NRGBAModel }
func (r *rgbImage) Bounds() image.Rectangle { return image.Rect(0, 0, r.w, r.h) }
func (r *rgbImage) At(x, y int) color.Color {
	i := y*r.stride + x*3
	return color.NRGBA{R: r.pix[i+0], G: r.pix[i+1], B: r.pix[i+2], A: 0xFF}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/codec/png/`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/codec/png/png.go internal/codec/png/png_test.go
git commit -m "$(cat <<'EOF'
feat(codec): first-class PNG encoder (internal/codec/png)

Lossless RGB888 PNG over stdlib image/png, registered in the codec
registry. Encode-only; not a TIFF tile codec (TIFFCompressionTag=0) —
conformant for DZI/SZI tiles only. Encoding logic lifted verbatim from
convert_dzi.go's inline PNG branch (byte-identical output).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Register PNG in the `all` aggregator

**Files:**
- Modify: `internal/codec/all/all.go`

`internal/codec/all` blank-imports every codec so it self-registers when
`cmd/wsitools` imports the aggregator. PNG is pure Go (like `jpeg`), so it goes in
the base import block, **not** behind a `!no<name>` build tag.

- [ ] **Step 1: Write the failing test**

Create `internal/codec/all/png_registered_test.go`:

```go
package all

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

// TestPNGRegisteredViaAll confirms importing the aggregator registers png.
func TestPNGRegisteredViaAll(t *testing.T) {
	if _, err := codec.Lookup("png"); err != nil {
		t.Fatalf("png not registered via internal/codec/all: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/codec/all/ -run TestPNGRegisteredViaAll`
Expected: FAIL — `codec.Lookup("png")` returns `ErrUnknownCodec` (the `all`
package does not import the png subpackage yet).

(Note: it would pass spuriously if another test in the package already imported
png transitively; it does not — `all.go` only imports jpeg in its base block.)

- [ ] **Step 3: Add the import**

In `internal/codec/all/all.go`, change the base import block from:

```go
import (
	_ "github.com/wsilabs/wsitools/internal/codec/jpeg"
)
```

to:

```go
import (
	_ "github.com/wsilabs/wsitools/internal/codec/jpeg"
	_ "github.com/wsilabs/wsitools/internal/codec/png"
)
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/codec/all/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codec/all/all.go internal/codec/all/png_registered_test.go
git commit -m "$(cat <<'EOF'
feat(codec): register png in the all aggregator

PNG is pure Go, so it joins the base import block (no build tag), making
--codec png resolvable in cmd/wsitools.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Re-point `dziStandaloneEncoder` onto the registered PNG codec

**Files:**
- Modify: `cmd/wsitools/convert_dzi.go:161-222` (the `dziStandaloneEncoder` type,
  `newDZIStandaloneEncoder`, `EncodeTile`, `Close`, and the now-unused
  `rgbBytesAsImage` helper)

Replace the inline `png.Encode` branch with a call to the registered codec, so
there is one PNG encode path. The JPEG branch is unchanged. After this, the
`bytes`, `image`, `image/color`, and `image/png` imports in `convert_dzi.go` are
unused (they existed only for the inline PNG path) and `rgbBytesAsImage` is dead —
both removed.

- [ ] **Step 1: Add a characterization test for the DZI PNG encoder**

Create `cmd/wsitools/convert_dzi_png_test.go`:

```go
package main

import (
	"bytes"
	stdpng "image/png"
	"testing"
)

// TestDZIStandalonePNGEncoder verifies the DZI PNG encoder (now backed by the
// registered codec) emits a decodable lossless PNG of the right dims. Guards the
// re-point in Task 3 against a regression.
func TestDZIStandalonePNGEncoder(t *testing.T) {
	const w, h = 32, 24
	rgb := make([]byte, w*h*3)
	for i := range rgb {
		rgb[i] = byte(i * 3)
	}
	enc, err := newDZIStandaloneEncoder("png", w, 0)
	if err != nil {
		t.Fatalf("newDZIStandaloneEncoder: %v", err)
	}
	defer enc.Close()
	tile, err := enc.EncodeTile(rgb, w, h)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	img, err := stdpng.Decode(bytes.NewReader(tile))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("dims: got %v, want %dx%d", img.Bounds(), w, h)
	}
	r, g, b, _ := img.At(1, 0).RGBA()
	i := 1 * 3
	if byte(r>>8) != rgb[i] || byte(g>>8) != rgb[i+1] || byte(b>>8) != rgb[i+2] {
		t.Fatalf("pixel (1,0) mismatch: got %d,%d,%d want %d,%d,%d",
			byte(r>>8), byte(g>>8), byte(b>>8), rgb[i], rgb[i+1], rgb[i+2])
	}
}
```

- [ ] **Step 2: Run the test to verify it passes on the CURRENT (inline) code**

Run: `go test ./cmd/wsitools/ -run TestDZIStandalonePNGEncoder`
Expected: PASS — this characterizes existing behavior before the re-point. (It
should pass both before and after Task 3; that is the point — the re-point must
not change behavior.)

- [ ] **Step 3: Re-point the encoder onto the registered codec**

In `cmd/wsitools/convert_dzi.go`, replace the `dziStandaloneEncoder` struct, its
constructor, `EncodeTile`, and `Close` (lines ~161-206) with:

```go
// dziStandaloneEncoder produces self-contained tiles: JPEG via libjpeg-turbo
// (EncodeStandalone) or PNG via the registered internal/codec/png encoder.
// Implements retile.TileEncoder.
type dziStandaloneEncoder struct {
	format string
	jpeg   *jpeg.Encoder // non-nil for jpeg
	png    codec.Encoder // non-nil for png
}

func newDZIStandaloneEncoder(format string, tileSize, quality int) (*dziStandaloneEncoder, error) {
	switch format {
	case "jpeg":
		enc, err := jpeg.New(
			codec.LevelGeometry{TileWidth: tileSize, TileHeight: tileSize},
			codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}},
		)
		if err != nil {
			return nil, fmt.Errorf("jpeg.New: %w", err)
		}
		return &dziStandaloneEncoder{format: "jpeg", jpeg: enc}, nil
	case "png":
		fac, err := codec.Lookup("png")
		if err != nil {
			return nil, fmt.Errorf("png codec unavailable: %w", err)
		}
		enc, err := fac.NewEncoder(
			codec.LevelGeometry{TileWidth: tileSize, TileHeight: tileSize, PixelFormat: codec.PixelFormatRGB8},
			codec.Quality{},
		)
		if err != nil {
			return nil, fmt.Errorf("png.NewEncoder: %w", err)
		}
		return &dziStandaloneEncoder{format: "png", png: enc}, nil
	default:
		return nil, fmt.Errorf("unsupported dzi format %q", format)
	}
}

func (e *dziStandaloneEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	switch e.format {
	case "jpeg":
		return e.jpeg.EncodeStandalone(rgb, w, h)
	case "png":
		return e.png.EncodeTile(rgb, w, h, nil)
	default:
		return nil, fmt.Errorf("unsupported dzi format %q", e.format)
	}
}

func (e *dziStandaloneEncoder) Close() error {
	if e.jpeg != nil {
		return e.jpeg.Close()
	}
	if e.png != nil {
		return e.png.Close()
	}
	return nil
}
```

Then delete the now-dead `rgbBytesAsImage` type and its three methods
(`ColorModel`/`Bounds`/`At`, lines ~208-222).

- [ ] **Step 4: Remove now-unused imports**

In `cmd/wsitools/convert_dzi.go`, remove imports that were only used by the inline
PNG path and the deleted wrapper: `bytes`, `image`, `image/color`, and the
`image/png` import (commonly aliased `png` — confirm the alias before removing).
Keep `strconv`, `fmt`, and the `codec` / `jpeg` imports (still used).

Run: `goimports -w cmd/wsitools/convert_dzi.go` (or `go build ./cmd/wsitools/`
and remove whatever it reports as unused).

- [ ] **Step 5: Build and run the DZI tests**

Run: `go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'DZI|Dzi'`
Expected: PASS — `TestDZIStandalonePNGEncoder` still green (behavior unchanged),
and any existing DZI tests unaffected.

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/convert_dzi.go cmd/wsitools/convert_dzi_png_test.go
git commit -m "$(cat <<'EOF'
refactor(dzi): re-point PNG tiles onto internal/codec/png

dziStandaloneEncoder's png branch now calls the registered codec instead
of inline image/png; deletes the inline rgbBytesAsImage wrapper. One PNG
encode path. Output byte-identical (same image/png.Encode + same
NRGBA-alpha-255 wrapper).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Integration parity gate (controller-run)

**Files:** none (verification only).

This step needs a built binary + a fixture, so the **controller** runs it (not a
subagent), mirroring the M1 byte-identity gate. It confirms `convert --to dzi
--dzi-format png` output is unchanged by the re-point.

- [ ] **Step 1: Build the binary**

Run: `make build` (produces `./bin/wsitools`).

- [ ] **Step 2: Generate a PNG DZI on a fixture, before/after comparison**

The re-point is byte-identical by construction, so verify against a freshly built
binary on a small fixture:

```bash
./bin/wsitools convert --to dzi --dzi-format png \
  -o /tmp/sp3c-s1.dzi sample_files/svs/CMU-1-Small-Region.svs
# Spot-check a base-level tile is a valid PNG:
f=$(find /tmp/sp3c-s1_files -name '*.png' | sort | tail -1)
python3 -c "import struct,sys; d=open('$f','rb').read(8); assert d==b'\x89PNG\r\n\x1a\n', d; print('PNG OK', '$f')"
```

Expected: prints `PNG OK …`. (Optional stronger gate: stash the pre-change binary
as `./bin/wsitools.pre`, run the same convert with both, and `diff -r` the two
`_files` trees — they must be identical.)

- [ ] **Step 3: Clean up**

```bash
rm -rf /tmp/sp3c-s1.dzi /tmp/sp3c-s1_files
```

---

## Self-review

**Spec coverage (Slice 1 scope):**
- "PNG promoted to a first-class codec … `internal/codec/png` … registered via
  `init()`" → Task 1 + Task 2. ✓
- "encode-only, lossless RGB888, stdlib `image/png`" → Task 1 (`Encoder`). ✓
- "DZI/SZI encoder uses the registered codec instead of the inline stdlib path" →
  Task 3. ✓
- "byte-identical output" → same `image/png.Encode` + NRGBA-255 wrapper (Task 1
  comment), guarded by Task 3 Step 2 characterization test + Task 4 parity gate. ✓
- Gating (`--codec png` → dzi|szi only) and `--dzi-format` deprecation are **Slice
  2**, not here — Slice 1 makes no CLI change. ✓ (correctly out of scope)

**Placeholder scan:** none — every code step shows complete code; every run step
shows the exact command + expected result.

**Type consistency:** `Factory`/`Encoder` match the `codec.EncoderFactory`/
`codec.Encoder` interfaces (`Name`, `NewEncoder`, `LevelHeader`, `EncodeTile(rgb,
w, h, dst)`, `TIFFCompressionTag`, `Close`). `dziStandaloneEncoder` gains a `png
codec.Encoder` field; `newDZIStandaloneEncoder`/`EncodeTile`/`Close` signatures
unchanged (the DZI sink calls them identically). PNG `EncodeTile` is called with
`nil` dst, matching the registry signature.

## Boundaries

**In Slice 1:** `internal/codec/png`, its registration, the DZI re-point,
byte-identity verification.

**Not in Slice 1 (later slices):** `--codec png` acceptance + dzi|szi gating +
`--dzi-format` deprecation + `--jobs`/`--workers` (Slice 2); `--rect`/`--to`
optional/`transformTo*` convergence (Slice 3); aliases + `transcode` (Slice 4).
