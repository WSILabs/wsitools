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
decoded pixels. Closing that gap:

- **`opentile/region` subpackage.** Region reading for arbitrary
  `(level, x, y, w, h)` rectangles. Decodes source tiles that intersect
  the region, blits into a destination raster, returns RGB(A). ~500 LOC of
  Go on top of the codec layer.
- **`Slide.Properties() map[string]string`.** Flat string-keyed view over
  the existing typed `Metadata`. Lets pathology code use openslide-shaped
  property keys (`openslide.mpp-x`, `openslide.objective-power`,
  `aperio.AppMag`, etc.) when convenient. ~50 LOC adapter.
- **`Slide.Thumbnail(maxDim) ([]byte, w, h)`.** Decoded thumbnail at
  requested max dimension; picks the best associated image or downsamples
  from a pyramid level.
- **`Slide.BestLevelForDownsample(d)`.** Trivial helper.
- **Cache policy decision.** Whether `region` reads cache decoded tiles.
  Deferrable until the first interactive consumer (tile-server) needs it;
  one-shot consumers (region CLI) don't.

Net result: opentile-go ships a region API + decoded-image surface roughly
equivalent to openslide's, with a coherent Go interface.

---

## 2. Deduping code (the codec lift)

This is the structural simplification that makes Section 1 possible
without bloating opentile-go for current consumers.

- **Lift `wsitools/internal/decoder` + `wsitools/internal/codec/*` into
  opentile-go** as `opentile/codec/{jpeg, jpegxl, avif, webp, htj2k,
  jpeg2000}` subpackages. Each subpackage registers itself against a small
  codec interface via `init()` — same registry pattern wsitools already
  uses internally.
- **Subpackage import means cgo deps are opt-in.** Existing opentile-go
  users who only want tile-byte access don't import `opentile/codec/*` and
  pay no new dependency cost. Users wanting region reads import the
  specific codecs they need (or `opentile/codec/all` for everything).
- **wsitools shrinks** — `internal/decoder` and `internal/codec/*`
  disappear; transcode/downsample import codecs from opentile-go. Single
  set of cgo wrappers, two consumers (opentile-go's region reader +
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
  arbitrary rectangle. First real consumer of the region API.
- `wsitools dump-tile` — single tile's compressed bytes to file/stdout.
  Debug aid.
- `wsitools dump-ifds --raw` — full tiffinfo-style dump per IFD.

### Re-tiling pipeline + DZI / large-output utilities

The DZI / re-tiling use case (e.g., 240×240 SVS → uniform 256×256 DZI
tile tree with a strict 2× pyramid as JPEG) is the bar libvips sets. To
match libvips speed:

- **Strip-based streaming pipeline.** Process the image in horizontal
  strips (`image_width × tile_height` pixels per strip), never
  materialise L0.
- **Pyramid lift on the strip.** As each strip is read, downsample it
  iteratively to produce the corresponding row of tiles at every pyramid
  level in a single pass. Buffering required is `tile_height × 2^N`
  source rows for an N-level pyramid.
- **Demand-driven decode.** When producing the strip for output tile row
  Y, decode only the source tiles that intersect that strip.
- **Aggressive parallelism.** N parallel source-tile decoders, M parallel
  output-tile encoders, downsample sandwiched between.
- **JPEG IDCT scaling** (the killer trick for JPEG-tiled sources).
  libjpeg-turbo can decode at 1/2, 1/4, or 1/8 resolution during the
  IDCT step itself — essentially free. For pyramid levels reachable by
  IDCT scaling, this skips the full decode-then-downsample cycle. Without
  it, you're decoding at full resolution and downsampling 4–8× in
  software.

Honest performance expectations: with all of the above, ~1.2–1.5× of
libvips on JPEG sources. The downsample kernel is the remaining gap
(libvips uses hand-tuned SIMD C; Go's compiler doesn't auto-vectorize
these loops well, and Go assembly is per-arch work). For non-JPEG sources
(JPEG2000, AVIF, JXL, etc.), the IDCT trick doesn't apply and you're
~1.5–2× slower, but libvips' coverage of those codecs isn't great either.

Build deliverables in order:

1. `wsitools dzsave` — DeepZoom pyramid generator. The first real
   consumer of the strip pipeline.
2. `wsitools tile-server` — HTTP DZI/IIIF tile server. Reuses the strip
   pipeline plus a tile cache (the deferred cache from Section 1 lands
   here).
3. `wsitools dicom-wsi` — convert WSI to DICOM-WSI format. Biggest
   single utility; depends on region + pyramid plus a DICOM-WSI writer.
   Its own design cycle.

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

1. **Move the GH repos to the new org** (Section 4). Do this BEFORE the
   codec lift, so the cumulative pain of import-path updates happens
   exactly once.
2. **Codec lift** (Section 2) — opentile-go v0.20 (or v0.21 if v0.20 was
   the org-move release). Pure refactor; wsitools imports change but
   behavior stays. Unblocks everything downstream.
3. **`opentile/region` + properties + thumbnail** (Section 1) —
   opentile-go v0.21 / v1.0 (depending on whether you want to mark API
   stability at this milestone).
4. **`wsitools region` CLI** — first real consumer of the new region
   API. Validates the surface.
5. **JPEG IDCT scale-factor** in the JPEG codec wrapper. Small but high-
   leverage. Could land alongside the codec lift.
6. **Strip-based pyramid lift + `wsitools dzsave`** (Section 3) — the
   big performance piece. Plenty of room to benchmark against libvips and
   iterate.
7. **`wsitools tile-server`** — needs the cache policy decision from
   Section 1; everything else is HTTP plumbing.
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

- **New organization name.** Whichever flavour you pick from Section 4;
  this needs to happen before any sequencing step starts.
- **Module version cut for the codec lift.** Whether to call it `v0.20`
  (minor release with internal restructure) or `v1.0` (signal API
  stability now that opentile-go is feature-complete-ish).
- **`opentile/region` cache policy.** Whether reads cache decoded tiles.
  Acceptable to defer until the tile-server consumer surfaces.
- **DZI command name.** `dzsave` (matches libvips' name) or `dzi`
  (matches the format name). Naming bikeshed worth one minute of
  thought.
- **DICOM-WSI scope** — pure writer, or both read + write? If both,
  reading needs DICOM-WSI integration into opentile-go's format
  dispatch, which is a fair chunk of work.
