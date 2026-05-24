# Strategic direction — opentile-go + wsitools

**Date:** 2026-05-23
**Status:** Discussion notes, not a normative spec.

This is the consolidated picture from recent design discussions. It covers
where the Go WSI stack (opentile-go + wsitools) should end up
architecturally, how to dedupe the code currently split awkwardly between
the two repos, how to add the openslide- and libvips-equivalent
functionality the project still lacks, and how to relocate the
repositories to a GitHub organization without breaking consumers.

The goal is a single Go-native pathway from "open a slide file" to "do
anything with it" — no FFI to openslide, no two competing access paths in
the same codebase. opentile-go becomes the library; wsitools becomes the
CLI consumer.

---

## 1. Rounding out opentile-go (becomes the "openslide for Go")

opentile-go today handles every hard part of slide access except returning
decoded pixels. Closing that gap means a meaningful API redesign in
addition to adding the decode layer — the current public surface is built
around a `Tiler` interface that only exposes compressed tile bytes, and
extending it for decoded operations would force every format implementer
to grow in lockstep. Better shape: collapse the public surface to a
single `*Slide` struct that owns the format-specific tile reader as an
*internal* detail and exposes both raw and decoded operations as methods.

### Public API shape (opentile-go v1.0)

The Go-idiomatic pattern: `*Slide` is a concrete struct (not an
interface); the format-specific reader becomes an unexported interface
inside opentile-go. Same architectural pattern as `*tls.Conn` wrapping
`net.Conn`, `*sql.DB` wrapping a driver, etc.

```go
package opentile

type Slide struct { /* unexported */ }

// Construction
func OpenFile(path string) (*Slide, error)
func Open(r io.ReaderAt, size int64, opts ...Option) (*Slide, error)
func (s *Slide) Close() error

// Format detection + basic surface
func (s *Slide) Format() Format
func (s *Slide) Metadata() Metadata
func (s *Slide) Levels() []Level
func (s *Slide) Associated() []AssociatedImage
func (s *Slide) TIFFTags() []TIFFTag         // empty slice for non-TIFF formats

// Raw tile access (compressed bytes — bit-exact paths)
func (s *Slide) RawTile(level, tx, ty int) ([]byte, error)
func (s *Slide) RawTileInto(level, tx, ty int, dst []byte) (n int, err error)

// Decoded tile access (single tile, RGB by default)
func (s *Slide) DecodedTile(level, tx, ty int) (*Image, error)
func (s *Slide) DecodedTileInto(level, tx, ty int, dst *Image) error

// Decoded region read (arbitrary rectangle, in L0 pixel coordinates).
// (x, y) are L0 coords; (w, h) are at the requested level's resolution.
// No implicit resampling — reads exactly at that level. For arbitrary-
// scale output, use ReadRegionScaled or a strip iterator.
func (s *Slide) ReadRegion(level, x, y, w, h int) (*Image, error)
func (s *Slide) ReadRegionInto(level, x, y int, dst *Image) error

// Decoded region read with explicit output sizing. Picks the best source
// pyramid level (via BestLevelForDownsample), reads at that level, then
// resamples to the target output dimensions. The convenience entry
// point for "give me this L0 rectangle at this output resolution."
func (s *Slide) ReadRegionScaled(l0x, l0y, l0w, l0h, outW, outH int) (*Image, error)
func (s *Slide) ReadRegionScaledInto(l0x, l0y, l0w, l0h int, dst *Image) error

// Strip iterator — sequential horizontal strips of the slide at a
// chosen output resolution, with internal parallel decode, IDCT scale
// selection, decoded-tile cache, and pre-fetch lookahead. The right
// primitive for dzsave-class throughput (libvips-comparable speed on
// JPEG sources). Each call to Next() returns an *Image of dimensions
// outW × stripHeight (final strip may be shorter).
func (s *Slide) ScaledStrips(outW, outH, stripHeight int, opts ...StripOption) *StripIterator

type StripIterator struct { /* unexported */ }
func (it *StripIterator) Next() (*Image, error)   // io.EOF when exhausted
func (it *StripIterator) Close() error

// Strip iterator configuration. Sane defaults work for most uses.
type StripOption func(*stripConfig)
func WithDecodeWorkers(n int) StripOption          // parallelism for source-tile decode; default: runtime.NumCPU()
func WithLookahead(strips int) StripOption         // pre-fetch depth; default: 2
func WithIDCTScale(scale int) StripOption          // 1/2/4/8 for JPEG sources; default: auto
func WithKernel(k resample.Kernel) StripOption     // resample quality; default: Lanczos
func WithTileCache(c *Cache) StripOption           // shared cache across iterators (tile-server use)

// Convenience
func (s *Slide) Thumbnail(maxW, maxH int) (*Image, error)              // fits inside (maxW, maxH) preserving aspect
func (s *Slide) BestLevelForDownsample(downsample float64) int

// Bounds / background (some scanners only fill a portion of L0 with
// tissue; the rest is background. Out-of-bounds region reads fill with
// BackgroundColor rather than failing.)
func (s *Slide) Bounds() image.Rectangle                                // tissue region in L0 coords; zero-value if not known
func (s *Slide) BackgroundColor() color.Color                            // usually white for histology

// ICC profile passthrough (color-managed pathology workflows). nil if
// the slide has no embedded profile.
func (s *Slide) ICCProfile() ([]byte, error)
```

### Why the strip iterator belongs in opentile-go

The naive "loop and call `ReadRegion` for each strip" pattern works for
correctness but is too slow for dzsave-class throughput. Three things
need to happen together to close the gap to libvips:

1. **Parallel decode** — multiple source tiles decoded concurrently across
   N CPU cores.
2. **Decoded-tile cache** — adjacent output strips share source tiles
   (the typical case when source tile-height ≠ output strip-height);
   without caching, tiles get redecoded across strip boundaries.
3. **Pre-fetch lookahead** — while the caller processes strip N, the
   library decodes the source tiles for strip N+1 in background workers.

These three optimizations interact tightly with codec internals (JPEG
IDCT scale-factor selection, the v0.13 tile-prefix optimizations). They
belong inside opentile-go so every consumer benefits, rather than
having each consumer (dzsave, tile-server, region-extract) reimplement
the same worker pool + cache + lookahead pattern.

Consumers who don't need the strip pattern continue to use
`ReadRegion` / `ReadRegionScaled` for ad-hoc rectangle reads. The strip
iterator is opt-in.

`AssociatedImage` keeps its current shape but gains decoded and
ICC-profile accessors alongside the existing raw-bytes accessor:

```go
type AssociatedImage struct { /* ... */ }
func (a AssociatedImage) Type() string                                  // label | macro | thumbnail | overview | ...
func (a AssociatedImage) Size() image.Point
func (a AssociatedImage) Compression() Compression
func (a AssociatedImage) RawBytes() ([]byte, error)                     // bit-exact passthrough
func (a AssociatedImage) Decoded() (*Image, error)                      // decoded pixels
func (a AssociatedImage) ICCProfile() ([]byte, error)                   // per-image profile; nil if absent
```

`Level` is a value-type struct (no methods); operations take a level
index. Avoids the `lvl.TileInto(tx, ty, dst)` style that lets callers
ignore which slide a level belongs to.

```go
type Level struct {
    Width, Height       int
    TileWidth, TileHeight int
    Compression         Compression
    Downsample          float64       // canonical downsample factor from L0
    // ... other inspection-only fields
}
```

`Downsample` is computed at slide-open time from vendor metadata when
available (e.g., Aperio's `AppMag` line), falling back to L0/Lk
dimension ratios. Storing it explicitly avoids the floor/ceil rounding
hazards we saw in the opentile-go pyramid classifier (issue #5).

`Metadata` already covers most of openslide's typed surface plus a
properties map (added in opentile-go v0.17). The v1.0 cut clarifies
some naming, adds `Vendor` (distinct from `SourceFormat`) at the
top level, and pulls a few standard openslide-equivalent fields into
the typed surface that were previously absent or only in `Properties`:

```go
type Metadata struct {
    // Existing typed fields (already in opentile-go v0.17+).
    Magnification       float64
    ScannerManufacturer string
    ScannerModel        string
    ScannerSoftware     []string
    ScannerSerial       string
    AcquisitionDateTime time.Time

    // Pixel size — split X/Y axes with an aggregate convenience.
    // MicronsPerPixel is set when X == Y (the common case for WSI);
    // zero indicates either "unknown" OR "asymmetric pixels"; check
    // MicronsPerPixelX/Y to disambiguate.
    MicronsPerPixel  float64
    MicronsPerPixelX float64
    MicronsPerPixelY float64

    // ImageDescription is the structured per-format description
    // (Aperio SVS ImageDescription string, OME-XML, BIF iSyntax
    // section, etc.). Empty when the format has no equivalent.
    ImageDescription string

    // NEW in v1.0 — fields previously absent or only in Properties:
    Vendor       string  // "aperio" | "hamamatsu" | "philips" | ... (scanner-shape, distinct from format)
    SourceFormat string  // file format identifier
    Comment      string  // openslide.comment equivalent — free-text slide comment
                         // (distinct from ImageDescription, which is the structured per-format block)

    // Properties is a flat key/value map for additional metadata that
    // doesn't fit the typed fields. Two key conventions:
    //
    //   - opentile-go-canonical keys (lowercase-with-hyphens):
    //     PropertyCaseNumber, PropertyUserName, PropertyScannedAreaMM2,
    //     PropertyScanDurationSec, PropertyComments. Populated by
    //     format readers when their format exposes the equivalent.
    //
    //   - Vendor-namespaced keys (vendor.<key>): vendor-specific fields
    //     surfaced as-is. e.g., "aperio.AppMag", "hamamatsu.SourceLens",
    //     "philips.iSyntax.AcquisitionParameters".
    //
    //   - TIFF tag passthrough keys (tiff.<TagName>): every TIFF tag the
    //     format reader parses is mirrored here under its canonical
    //     name. e.g., "tiff.XResolution", "tiff.ResolutionUnit",
    //     "tiff.ImageDescription", "tiff.Make", "tiff.Model". Vendor
    //     readers populate this so downstream code calibrated to
    //     openslide's tiff.* property keys works without translation.
    //
    // Typed fields above are the authoritative source where they exist;
    // Properties is the long tail.
    Properties map[string]string
}
```

### TIFF tag passthrough

For TIFF-based slides (SVS, Philips-TIFF, OME-TIFF, BIF, generic-TIFF,
COG-WSI), every TIFF tag the format reader parses is mirrored into
`Metadata.Properties` under `tiff.<TagName>` keys. This matches
openslide's convention and gives consumers calibrated to openslide's
property-key vocabulary a direct path without translation.

Two access tiers:

1. **Common TIFF tags via `Properties`** — `tiff.ImageWidth`,
   `tiff.ImageLength`, `tiff.Make`, `tiff.Model`, `tiff.Software`,
   `tiff.DateTime`, `tiff.ImageDescription`, `tiff.XResolution`,
   `tiff.YResolution`, `tiff.ResolutionUnit`, `tiff.Compression`,
   `tiff.PhotometricInterpretation`, etc. Populated by the format
   reader during open.

2. **Exhaustive raw-tag access via `Slide.TIFFTags()`** (TIFF-based
   formats only) — returns every TIFF tag on every IFD as typed
   entries. For consumers that want completeness including unknown
   private vendor tags. Returns an empty slice for non-TIFF formats
   (IFE, future formats).

   ```go
   type TIFFTag struct {
       IFD      int            // 0-based IFD index in file order
       Tag      uint16         // TIFF tag ID
       Type     uint16         // TIFF type (BYTE/ASCII/SHORT/LONG/etc.)
       Count    uint64         // number of values
       RawBytes []byte         // raw bytes; caller decodes per Type
   }

   func (s *Slide) TIFFTags() []TIFFTag  // empty for non-TIFF formats
   ```

   This is a deliberate non-cache approach — `TIFFTags()` re-walks the
   IFD chain on call. Consumers wanting the typed fields should use
   `Metadata()` (faster, cached); `TIFFTags()` is for completeness
   when you need every tag.

### Pixel format

Decoded operations default to **RGB** (3 bytes per pixel, no alpha).
Reasoning: WSI imagery is opaque, so alpha is wasted memory; libjpeg-turbo
decodes RGB natively (no intermediate copy); strip-based pipelines (DZI)
save ~25 % memory at RGB vs RGBA. Callers who want stdlib `image.NRGBA`
interop can opt in via the `*Into` variants with an RGBA destination, or
use a helper conversion.

**BGRA is not supported.** openslide returns BGRA because of a C-era
little-endian byte-swap optimization for 32-bit reads. Go has no need to
inherit that.

```go
type PixelFormat int

const (
    PixelFormatRGB   PixelFormat = iota  // 3 bytes per pixel, no alpha
    PixelFormatRGBA                      // 4 bytes per pixel, alpha = 0xFF
)

type Image struct {
    Width, Height int
    Stride        int           // bytes per row; can over-allocate for SIMD alignment
    Format        PixelFormat
    Pix           []byte        // len(Pix) == Stride * Height
}

func NewImage(w, h int) *Image                                // RGB
func NewImageFormat(w, h int, fmt PixelFormat) *Image         // explicit
```

Decoded-method default returns are RGB; pass an RGBA-formatted `*Image`
to `ReadRegionInto` / `DecodedTileInto` to get RGBA output.

### Resample subpackage

Pure pixel resample primitives, lifted from `wsitools/internal/resample`.
Free functions; no state. Used internally by `ReadRegionScaled` and the
strip iterator, also available for ad-hoc use.

```go
package resample

type Kernel int

const (
    Nearest Kernel = iota
    Bilinear
    Lanczos
    Box   // area-averaging; fast for downsampling
)

func Image(src *opentile.Image, outW, outH int, k Kernel) *opentile.Image
func ImageInto(src, dst *opentile.Image, k Kernel) error  // dst dims determine output
```

Pure-Go for v1.0. cgo-accelerated kernels (via libvips' resampler or
hand-tuned assembly) are a future optimization if profiling demands it.

### Coordinate convention for `ReadRegion`

`(x, y)` are in **L0 pixel coordinates**, regardless of the requested
`level`. This matches openslide's convention so pathology code calibrated
to that convention works without translation, and it makes the "give me
this rectangle at this magnification" intent unambiguous (you specify
where on the slide independent of which pyramid level provides the
sampled output).

### Out-of-bounds region reads

`ReadRegion` accepts any `(x, y, w, h)` and fills the off-slide portion
of the rectangle with `BackgroundColor()` rather than returning an
error. This matches openslide's `read_region` semantics and is critical
for tile-server / DZI generation: boundary tiles routinely overhang the
edge of the slide, and pathology viewers expect those overhangs to be
filled with background (typically white), not to fail. Same applies
when a slide has `Bounds()` smaller than its L0 dimensions — pixels
inside the L0 rectangle but outside the bounds rectangle are filled
with background.

### What's NOT in the surface (intentionally)

- **No `Slide.Properties() map[string]string` flat-string adapter.**
  `Slide.Metadata()` already returns typed metadata. Vendor-specific
  properties grow on the typed struct (sub-structs or a future
  `Metadata.Vendor map[string]string` escape hatch) rather than
  flattening everything to strings.
- **No "Slide" interface.** The format-specific reader is unexported.
  Consumers see only the concrete `*Slide` struct.
- **No streaming/iterator API for tiles.** `for _, lvl := range slide.Levels()`
  + nested tile loops handle the use case. Add iterators if a real
  consumer materialises.
- **No `DetectVendor(filename)` pre-open helper.** Just call `OpenFile`
  and check `s.Format()` / `s.Metadata().Vendor`. openslide exposed
  this for fast pre-open dispatch in C; Go's open-then-inspect pattern
  is the equivalent.
- **No `(*Slide).Err()` error accessor.** Go's idiomatic `error` returns
  on every call cover what openslide's `openslide_get_error` did for
  C-shaped error handling.
- **No multi-region API** (`openslide.region[N].*`). Rare in practice
  (mostly 3DHistech multi-tissue slides). Defer until a real consumer
  needs it.
- **No `Quickhash` content hash** in `Metadata`. openslide computed this
  as a cache identity helper. wsitools already provides `wsitools hash`
  for the analogous use case; opentile-go doesn't need to compute it
  internally.

### Cache policy

Whether `ReadRegion` and `DecodedTile` cache decoded tiles between calls
is deferrable. Tile-server consumers want it; one-shot CLI consumers
don't. Add as an `Option` to `Open` / `OpenFile` when the first
interactive consumer needs it.

### Net result

opentile-go v1.0 ships a single-concrete-type API surface that handles
the full slide-reading workflow (raw bytes, decoded tiles, region reads,
thumbnails, metadata) in one place. The format dispatch infrastructure
(currently the public `Tiler` interface) becomes an unexported
implementation detail. Consumers think about "the slide," not about
which interface defines which operation.

---

## 2. Deduping code (the codec lift)

This is the structural simplification that makes Section 1's decoded
operations possible without bloating opentile-go for consumers that only
need raw-tile access.

- **Lift `wsitools/internal/decoder` + `wsitools/internal/codec/*` into
  opentile-go** as `opentile/codec/{jpeg, jpegxl, avif, webp, htj2k,
  jpeg2000}` subpackages. Each subpackage registers itself against a small
  unexported codec interface via `init()` — same registry pattern
  wsitools already uses internally.
- **Subpackage import means cgo deps are opt-in.** Consumers who only
  need `Slide.RawTile` / `Slide.Levels` / `Slide.Metadata` don't import
  `opentile/codec/*` and pay no new dependency cost. Calling
  `Slide.DecodedTile` / `Slide.ReadRegion` / `Slide.Thumbnail` without
  having imported the relevant codec returns a clear "no decoder
  registered for compression X" error at call time (similar to how
  `image.Decode` requires importing `_ "image/jpeg"`). For ergonomics,
  `opentile/codec/all` blanket-imports every codec.
- **wsitools shrinks** — `internal/decoder` and `internal/codec/*`
  disappear; transcode/downsample import codecs from opentile-go. Single
  set of cgo wrappers, two consumers (opentile-go's decode layer +
  wsitools' encode pipelines).
- **wsitools becomes purely CLI** on top of a complete library. No more
  codec maintenance in two places.
- **Surface a JPEG IDCT scale-factor parameter** in the JPEG codec
  wrapper while you're already touching it. libjpeg-turbo can decode at
  1/2, 1/4, 1/8 essentially for free during the IDCT step — this is the
  single biggest performance lever for any pyramid generation work later.

This bucket is the prerequisite for Section 3 and the cleanup that
finalizes the v0.7 refactor's intent.

---

## 3. Adding functionality (the wsitools utility surface)

Once Sections 1 + 2 are done, every roadmap utility is a thin CLI command
over the library.

### Convert command expansions

- `convert --to iris` — separate writer (`internal/iris` in wsitools, or
  a sibling library). Iris isn't TIFF.
- `convert --to svs` / `--to bif` — additional TIFF-based output targets,
  naturally fit alongside `cog-wsi` once the streamwriter exists.
- **Fold transcode into convert** (lossy paths). Eventually deprecate the
  standalone transcode command; `convert --codec jpeg --quality 85`
  covers the case.
- **NDPI source support in convert** — requires JPEG restart-marker
  reshuffling (near-lossless, not bit-copy). Documented limitation in
  v0.6.

### Read-side utilities (depend on `region`)

- `wsitools region --x --y --w --h --level -o out.png` — extract an
  arbitrary rectangle. First real consumer of `Slide.ReadRegion`.
- `wsitools dump-tile` — single tile's compressed bytes to file/stdout.
  Debug aid.
- `wsitools dump-ifds --raw` — full tiffinfo-style dump per IFD.

### Re-tiling pipeline + DZI / large-output utilities

The DZI / re-tiling use case (e.g., 240×240 SVS → uniform 256×256 DZI
tile tree with a strict 2× pyramid as JPEG) is the bar libvips sets. The
performance machinery for matching libvips lives in **opentile-go's
`Slide.ScaledStrips` iterator** (described in Section 1), not in wsitools.
The iterator provides parallel source-tile decode, decoded-tile cache,
JPEG IDCT scale-factor selection, and pre-fetch lookahead — all the
expensive plumbing that closes the gap to libvips.

wsitools' role for DZI / re-tiling is the consumer side:

- **Strip iteration through opentile-go.** Configure `ScaledStrips` for
  the desired output level resolution; consume strips sequentially.
- **Pyramid lift across strips.** For multi-level outputs (DZI typically
  generates ~10 levels), either run one `ScaledStrips` iterator per
  output level, or stack them with shared tile caches.
- **Re-tile blitting.** Cut each strip into output tiles
  (256×256 for standard DZI).
- **Encode pool.** N parallel JPEG encoders consume output tiles and
  write them to disk.
- **Output container.** DZI: directory tree + `.dzi` manifest. COG-WSI:
  via cogwsiwriter. DICOM-WSI: via a future dicomwsiwriter.

Honest performance expectations: with the strip iterator + IDCT scaling
working as designed, ~1.2–1.5× of libvips on JPEG sources. The downsample
kernel is the remaining gap (libvips uses hand-tuned SIMD C; Go's
compiler doesn't auto-vectorize these loops well, and Go assembly is
per-arch work). For non-JPEG sources (JPEG2000, AVIF, JXL, etc.), the
IDCT trick doesn't apply and you're ~1.5–2× slower, but libvips' coverage
of those codecs isn't great either.

Build deliverables in order:

1. `wsitools dzsave` — DeepZoom pyramid generator. The first real
   consumer of `Slide.ScaledStrips`.
2. `wsitools tile-server` — HTTP DZI/IIIF tile server. Shares a tile
   cache across requests via `WithTileCache(c)` (Section 1 cache policy
   gets resolved here).
3. `wsitools dicom-wsi` — convert WSI to DICOM-WSI format. Biggest
   single utility; depends on the strip iterator plus a new DICOM-WSI
   writer in wsitools. Its own design cycle.

### Read-side operations and pool-management

- `wsitools tagset` — in-place TIFF tag edit (e.g., fix ImageDescription
  on a bad slide without re-encode).
- `wsitools inventory` — walk a directory, dump CSV/JSON of slide
  metadata.
- `wsitools verify` — "fsck for WSI": open every IFD, decode every tile,
  report errors.
- `wsitools diff` — compare two slides (pixel diff / metadata diff / IFD
  ordering diff).

### Deferred codecs and sources (long tail)

- **Codecs:** jpegli, HEIF, JPEG-LS, JPEG-XR, Basis Universal; jpeg2000
  as a transcode-encoder target.
- **Sources:** Leica SCN (needs multi-image plumbing).

### Smaller cleanups from v0.6/v0.7 design docs

- Drop RGB-only / 8-bit assumption in the writers (multi-channel + 16-bit
  support).
- BigTIFF-aware test helpers in `convert_integration_test.go`.

---

## 4. Moving the repos to a GitHub organization

Both `cornish/opentile-go` and `wsilabs/wsitools` currently live under a
personal account. Moving them to an organization (call the new owner
`<ORG>` — final name TBD) cleans up identity, makes future related
projects natural (`<ORG>/openwsi-go` if region ever spins off,
`<ORG>/wsitools-go-bindings` for any wrapper work, etc.), simplifies
collaborator management, and signals "library" rather than "personal
project" to potential downstream Go consumers.

The mechanics are easy. The Go-module-path implication is the part that
needs care.

### What GitHub does automatically when you transfer ownership

- **Repository contents, branches, tags, releases, issues, PRs, stars,
  watchers** — all transfer cleanly.
- **Issue/PR cross-references** (`#5`, `org/repo#5`) — still resolve.
- **HTTPS + SSH clone URLs** — GitHub installs a permanent redirect from
  `cornish/<repo>` → `<ORG>/<repo>`. Existing clones keep working
  indefinitely (their `git remote -v` still says the old URL, but pushes
  and fetches succeed via the redirect).
- **Branch protection rules, labels, milestones, projects** — transfer.
- **GitHub Actions workflows** — transfer; secrets at the *organization*
  level need to be re-set (repo-level secrets transfer with the repo;
  org-level secrets are a separate concept).
- **gh-release URLs (`github.com/cornish/<repo>/releases/tag/vX`)** —
  redirect to the new owner.
- **Stars/watchers** — transfer. Old GitHub user URLs for the repo
  resolve to the new owner.

### What does NOT transfer cleanly

- **Forks** — existing forks stay on their forkers' accounts but their
  parent reference updates to the new owner. Mostly fine.
- **GitHub Packages** (if you publish any) — explicit cross-org transfer
  process; we don't publish any, so N/A.
- **Personal access tokens** (PATs) — scope to a user, not a repo. If
  you'll be the only maintainer, PATs keep working. If you add org
  collaborators, the org typically requires fine-grained tokens or a
  GitHub App.
- **pkg.go.dev pages** — the page at the old path keeps working (via
  GitHub's redirect), but the Go module proxy caches under the new path
  separately and you'll want fresh module-proxy entries at the new path.
  Fetch with `GOPROXY=https://proxy.golang.org go install <ORG>/<repo>/...`
  to warm the proxy after the move.

### The Go module path problem

This is the part that matters most. Go modules identify themselves by an
*immutable string* in `go.mod` (the `module` directive). When you transfer
`cornish/opentile-go` to `<ORG>/opentile-go`:

- The HTTPS git redirect handles `git clone github.com/cornish/...`
  pointers in scripts. **But Go modules do NOT use the HTTPS redirect for
  resolution** — `go get` validates that the path in `go.mod` matches the
  path you fetched from. If `go.mod` says `module github.com/cornish/opentile-go`
  but you fetched it as `github.com/<ORG>/opentile-go`, `go get` errors
  with `path/repo mismatch`.

This means **the module path must be updated in `go.mod` for any release
fetched at the new path**, and consumers' import statements must update
in lockstep.

### Two paths through this

**Path A: Hard cutover (recommended for active development).**

1. Transfer `cornish/opentile-go` → `<ORG>/opentile-go`.
2. On the new repo's main branch, edit `go.mod` to
   `module github.com/<ORG>/opentile-go`. Commit + tag a new version
   (e.g., `v0.20.0` or `v1.0.0` if you want to mark API stability).
3. Update all consumer `go.mod` files (in wsitools and anywhere else) to
   import from the new path. `find . -name '*.go' | xargs sed -i ''` for
   the import-statement bulk rename, plus `go mod edit -replace` if you
   need a transition period.
4. Repeat for `wsilabs/wsitools` → `<ORG>/wsitools`.
5. Pre-existing versions at the old path keep working (the redirect +
   their `go.mod` self-identifying as the old path means `go get
   github.com/cornish/opentile-go@v0.19.0` still works for legacy
   consumers).
6. Future releases live at the new path only.

This is clean if you control all consumers. You do.

**Path B: Soft transition (only useful if you have downstream consumers
you can't update synchronously).**

1. Transfer the repos.
2. Don't change the `module` line in `go.mod`. Consumers continue to
   fetch via the old path (redirected by GitHub at the HTTPS layer; the
   `go.mod` self-identifies as the old path so `go get` is happy).
3. Wait until you're ready to fully cut over, then do Path A.

You don't have downstream consumers to coordinate with, so Path A is the
right choice. The brief inconvenience of bulk-renaming imports in
wsitools is well worth the clean end state.

### Pre-move checklist

Before flipping the transfer:

- [ ] Decide on the new organization name and create the org on GitHub.
- [ ] Confirm no open in-flight PRs in either repo (or merge/close them
      first — they transfer but the URLs in their bodies become
      old-path-shaped, which is harmless but noisy).
- [ ] Update README badges in both repos that reference the
      `cornish/` path:
  - CI status badge URL (`actions/workflows/...`)
  - go.dev reference badge URL (`pkg.go.dev/...`)
  - Coverage badge if present (codecov / coveralls URL embeds repo path)
- [ ] List internal links in docs that hard-code the GitHub path:
  - `CHANGELOG.md` entries mentioning issue numbers (e.g., wsitools'
    v0.7 entry references `cornish/opentile-go#5`)
  - `docs/strategic-direction.md` (this file) and any peer planning
    docs
  - The vendored spec at `opentile-go/docs/specs/2026-05-20-cog-wsi-format.md`
    (and the canonical at `wsitools/docs/superpowers/specs/...`) both
    cross-reference each other's repo paths.
- [ ] Update `cmd/wsitools/svs_imagedesc.go` and any other source
      comments that reference the wsiwriter source repo path.
- [ ] Snapshot the current pkg.go.dev URL/cache state in case a
      consumer asks "where do I find the old version?" — answer is "at
      the same path; GitHub redirects."

### After-move checklist

- [ ] Transfer `cornish/opentile-go` → `<ORG>/opentile-go` via GitHub
      settings.
- [ ] In the new opentile-go repo: edit `go.mod` to
      `module github.com/<ORG>/opentile-go`; bulk-rename all internal
      imports; commit; tag `v0.20.0` (or whatever the next release is).
- [ ] Push the tag; verify the GH release URL works.
- [ ] In wsitools: bulk-rename all
      `github.com/cornish/opentile-go` imports to
      `github.com/<ORG>/opentile-go`; `go mod edit -require
      github.com/<ORG>/opentile-go@v0.20.0`; `go mod tidy`; `go build
      ./...`; commit.
- [ ] Transfer `wsilabs/wsitools` → `<ORG>/wsitools`.
- [ ] In the new wsitools: edit `go.mod` to
      `module github.com/<ORG>/wsitools`; bulk-rename all internal
      imports; commit; tag the next wsitools release (probably 0.8.0
      if it coincides with the codec lift).
- [ ] Update README badges, CHANGELOG entries, vendored docs.
- [ ] Warm the Go module proxy at the new paths:
      `GOPROXY=https://proxy.golang.org go install
      github.com/<ORG>/wsitools/cmd/wsitools@latest`.
- [ ] Smoke-test fresh install: `go install
      github.com/<ORG>/wsitools/cmd/wsitools@latest` on a clean machine
      (or container).

### Naming the organization

A few candidate flavours, depending on the identity you want to project:

- **Personal-shaped**: `cornish-labs`, `tcornish`. Reads as "this is
  one person's work, housed in an org for tidiness." Lowest friction;
  signals continuity with the current `cornish/` repos.
- **Project-shaped**: `wsi-go`, `gowsi`, `pathology-go`. Reads as "this
  is a Go-pathology umbrella." Future-proof for related projects under
  the same banner.
- **Domain-tied**: institutional name (`jhpathology`, `jhu-pathology`,
  etc.). Useful if you intend to associate the work with an
  organisation officially.

The "right" choice depends on whether you imagine handing off
maintainership to someone else, or attracting collaborators outside your
own circle. Project-shaped names are most welcoming to external
contributors; personal-shaped names are most honest about scope.

---

## 5. Recommended sequencing

Roughly the order of leverage and risk:

1. ~~**Move the GH repos to the new org**~~ — done (WSILabs/opentile-go
   at v0.21.0, WSILabs/wsitools at v0.8.1).
2. **Codec lift + resample lift** (Section 2) — opentile-go v0.22
   (additive: new `opentile/codec/*` and `opentile/resample` subpackages,
   no public API change yet). Pure refactor on the wsitools side; codec
   imports change but behavior stays. Includes the JPEG IDCT scale-factor
   parameter on the JPEG codec wrapper (high-leverage perf knob worth
   surfacing while you're already touching it). Unblocks everything
   downstream.
3. **API redesign: `*Slide` struct + decoded operations + strip
   iterator** (Section 1) — opentile-go **v1.0**. Drops the public
   `Tiler` interface, replaces with `*Slide`; adds raw + decoded tile
   methods, `ReadRegion` / `ReadRegionScaled`, `ScaledStrips` iterator
   with parallel decode + cache + lookahead, openslide-equivalent surface
   (ICCProfile, Bounds, BackgroundColor, Thumbnail with two-axis box fit,
   Level.Downsample, Metadata.Vendor). API stability marker; v1.x
   onward is additive.
4. **`wsitools region` CLI** — first real consumer of `ReadRegionScaled`.
   Validates the region surface.
5. **`wsitools dzsave`** — first real consumer of `ScaledStrips`.
   Validates the strip iterator + benchmarks against libvips.
6. **`wsitools extract`** — ImageScope-equivalent region-at-scale to
   container output (SVS / TIFF / COG-WSI / JPEG / PNG). Reuses the
   strip iterator + writer plumbing from #5.
7. **`wsitools tile-server`** — needs the strip iterator's shared tile
   cache (`WithTileCache`) operational; everything else is HTTP plumbing.
8. **Convert extensions** (`--to iris`, `--to svs`, `--to bif`).
   Independent of Sections 1+2; can run in parallel.
9. **`convert` subsumes `transcode`** — lossy paths land in convert;
   transcode deprecated.
10. **Long-tail utilities** (`tagset`, `inventory`, `verify`, `diff`).
    Straightforward once region + pyramid layers exist.
11. **`dicom-wsi`** — biggest single utility; depends on region +
    pyramid plus a DICOM-WSI writer. Its own design cycle.

The whole arc puts wsitools in a position where it's a complete
openslide/libvips alternative for Go-native pathology workflows, with
opentile-go as the publishable library underneath. None of these depend
on each other in lock-step — you can pick any path and move forward.

---

## 6. Open decisions parked for later

- ~~**New organization name.**~~ Settled: `WSILabs`. Both repos relocated.
- ~~**Module version cut for the codec lift.**~~ Settled: codec lift lands
  in opentile-go v0.22 (additive, no public API change), API redesign
  with `*Slide` lands in v1.0.
- **Decoded-tile cache policy** (Section 1). Whether `Slide.ReadRegion` /
  `Slide.DecodedTile` cache decoded tiles between calls.
  Acceptable to defer until the tile-server consumer surfaces.
- **DZI command name.** `dzsave` (matches libvips' name) or `dzi`
  (matches the format name). Naming bikeshed worth one minute of
  thought.
- **DICOM-WSI scope** — pure writer, or both read + write? If both,
  reading needs DICOM-WSI integration into opentile-go's format
  dispatch, which is a fair chunk of work.
- **Vendor-properties escape hatch on `Metadata`.** Whether to add a
  `Metadata.Vendor map[string]string` field for vendor-specific
  key/value properties (e.g., Aperio's ImageDescription lines like
  `Filtered = 5|StripeWidth = 2040`) that aren't represented in the
  typed surface today. Add when the first caller needs it.
