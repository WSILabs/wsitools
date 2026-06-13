# wsitools utilities roadmap

Tracks the full set of CLI utilities planned for wsitools, organised
into "shipped" and "planned" sections. The shipped section is updated
as releases land; the planned section is the source of truth for what's
queued, deferred, or under consideration.

## Shipped

### v0.1
- `downsample` — produce a lower-magnification SVS by an integer power-of-2 factor.
- `doctor` — list registered codecs + cgo deps.
- `version` — print version + Go runtime info.

### v0.2
- `transcode` — re-encode pyramid tiles in a different codec (jpeg, jpegxl, avif, webp, htj2k); 6 sane source formats; streaming pipeline.

### v0.3
- (no new utilities — opentile-go v0.14 migration milestone; novel-codec round-trip + sync.Pool + TileInto adoption).

### v0.4
- `info` — slide summary (openslide-show-properties analog).
- `dump-ifds` — format-aware per-IFD layout dump (slim tiffinfo analog).
- `extract` — save associated image (label/macro/thumbnail/overview) as PNG or JPEG.
- `hash` — content hash (file mode default; pixel mode opt-in).

### v0.5
- (no new utilities — project rename: `wsi-tools` → `wsitools`; module path + binary name).

### v0.6
- `convert --to cog-wsi` — lossless, bit-exact tile-copy of a WSI into the new COG-WSI container (Cloud Optimized GeoTIFF + WSI extension tags). Six source formats (SVS, Philips-TIFF, OME-TIFF, BIF, IFE, generic-TIFF). Normative format spec at `docs/superpowers/specs/2026-05-20-cog-wsi-format.md`.

### v0.7
- (no new utilities — TIFF core extraction milestone: shared `internal/tiff` package; `wsiwriter` and `cogwsi` writer packages reorganized as `internal/tiff/streamwriter` and `internal/tiff/cogwsiwriter`. opentile-go upgraded v0.14 → v0.19, bringing the dedicated COG-WSI reader and integer-multiple ratio acceptance — `wsitools info` on COG-WSI output now reports `Format: cog-wsi` and pyramid levels match source counts exactly).

### v0.8
- (no new utilities — repository relocation: module path moved from `github.com/cornish/wsitools` to `github.com/wsilabs/wsitools` under the new WSILabs GitHub organization. opentile-go also relocated to `github.com/wsilabs/opentile-go` at v0.21.0. No behavior change. v0.8.1 corrects the embedded `Version` constant that was missed when v0.8.0 was tagged).

### v0.13
- `region` — openslide-write-png analog: extract `--x --y --w --h --level` rectangle as PNG.

### v0.15
- (no new utilities — source-format expansion: NDPI, OME-OneFrame, and Leica SCN (single-image) slides now work across all CLI subcommands. opentile-go synthesizes tile geometry for striped sources; wsitools' source layer trusts the synthesis. Bit-exact tile-copy promise for `convert` applies to natively-tiled sources only; striped sources produce reproducible but synthesized JPEG tiles in the output.)

### v0.16
- `convert --to dzi` — DeepZoom pyramid output (OpenSeadragon-compatible). Defaults: 256×256 tiles, 1px overlap, JPEG Q=85.
- `convert --to szi` — Smart Zoom Image output: DZI pyramid wrapped in a store-method ZIP with optional `scan-properties.xml` populated from source metadata.
- `convert --to {svs,tiff,ome-tiff}` — re-encode + tile-copy targets that subsume the removed `transcode` subcommand.
- Tile-copy fast path generalised: applies to all TIFF-based targets when `--codec` is absent and the source is natively-tiled.
- BREAKING: `transcode` subcommand removed. Migration is mechanical; see CHANGELOG.

### v0.17
- (no new utilities — performance: `convert --to dzi|szi` rewritten as a pyramid-descent generator with parallel libjpeg-turbo encoder pool. CMU-1.ndpi DZI went from 35 minutes → 14 seconds (~150× faster); now faster than libvips `dzsave` on that fixture. JPEG codec reorganised: vanilla YCbCr default; Aperio APP14 quirk preserved in `internal/codec/aperioapp14`. New `make bench-dzi` target for ongoing libvips comparison.)

### v0.18
- (no new utilities — cooperative SIGINT shutdown for `convert --to dzi|szi`. Ctrl-C now produces a clean process exit in ~100-500 ms instead of requiring SIGKILL. v0.17's deferred `TestConvertDZICtxCancel` re-enabled.)

### v0.19
- (no new utilities — CI fixture pipeline. CI downloads CMU-1-Small-Region.svs + CMU-1.ndpi from `wsilabs/wsi-fixtures` v1 and runs the previously-skipped integration tests on every push + PR. Per-platform regressions visible in CI before tagging.)

### v0.20
- `dump-ifds --raw` — full tiffinfo-style tag dump per IFD with name + enum interpretation. Composes with `--json`. `--raw-full` disables smart truncation. ~100-tag dictionary + 11-enum interpreter in `internal/tiff/tagnames.go`; pure Go, no new deps.

### v0.21 (in progress)
- (no new utilities) Default soft memory cap: wsitools sets `GOMEMLIMIT` to 75% of physical RAM at startup so memory-heavy conversions degrade under GC pressure instead of OOM-ing the host. Global `--max-memory` flag + `GOMEMLIMIT` override (precedence `--max-memory` > env > default); `doctor` reports the active limit. New `internal/memlimit` package.
- opentile-go upgraded v0.26.0 → v0.30.0 (NDPI decode-perf + a per-Slide read-memory budget, `OPENTILE_READ_MEMORY_BUDGET`, that byte-bounds the strip/tile decode caches). No wsitools API changes.
- `convert --to ome-tiff` conformance: pyramid sub-resolutions now stored as
  SubIFDs (330) of L0 (previously written as orphan top-level IFDs → readers
  saw only L0); associated images enumerated in the OME-XML; SampleFormat
  (339) + OME-XML preamble added. Grounded in the OME-TIFF spec
  (`docs/references/ome-tiff-spec-notes.md`).
- ✅ **DONE: associated-image editing — Slice 1 (SVS + generic-TIFF).** Four
  command groups (`label`, `macro`, `thumbnail`, `overview`), each with `remove`
  and `replace` subcommands. Pyramid tile bytes are copied verbatim (prefix-copy
  + tail-re-emit splice; no decode, no re-encode); only the tail IFD is rewritten.
  `label remove` is the primary PHI/deidentification path. `label replace` uses
  LZW + Predictor 2 by default (lossless, barcode-safe); `macro`/`thumbnail`/
  `overview replace` default to JPEG. `--in-place` for atomic overwrite. Built on
  opentile-go v0.36.0 (`AssociatedIFDOffset`) + `github.com/hhrutter/lzw`.

## Planned

### Debug aids
- **`dump-tile`** — single tile's compressed bytes to file or stdout. Pure debug aid.

### Operations
- ✅ **DONE (2026-06-07): Associated-image editing — Slice 2a (COG-WSI).** `remove`
  and `replace` (all four types: label/macro/thumbnail/overview) now work on
  COG-WSI via `cogwsiwriter` re-finalize. Engine: full-file rebuild — pyramid tile
  bytes copied verbatim (no re-encode); all other associated images and
  MPP/magnification/ICC preserved; only the target image changes. Replacements
  round-trip cleanly (COG-WSI uses self-contained JPEG/LZW — no abbreviated-JPEG
  limitation). NOT an in-place splice; the rebuilt file is written atomically.
- ✅ **DONE (2026-06-07): Associated-image editing — Slice 2b (OME-TIFF, lossy).**
  `label/macro/thumbnail/overview remove` and `replace` (all four types) now work
  on OME-TIFF via `streamwriter` full-file rebuild. **Explicitly lossy**: the
  rebuild regenerates a minimal OME-XML (dimensions, MPP, magnification, one
  `<Image>` per remaining image) — instrument/objective, acquisition dates, stage
  positions, channel details, and all vendor `OriginalMetadata` + pyramid-resolution
  annotations are discarded, even for the surviving pyramid. Pyramid pixels are
  copied verbatim (no re-encode); geometry/MPP/magnification, ICC, and the other
  associated images are preserved. Always-on runtime warning on every edit.
  Associated replacements are JPEG-only (opentile-go OME-TIFF reader limitation).
  See `docs/ome-tiff-limitations.md`. **This completes associated-image editing
  across all four editable formats: SVS, generic-TIFF, COG-WSI, and OME-TIFF.**
  - **Deferred indefinitely: faithful OME-TIFF engine.** A conformant implementation
    would require a raw IFD-graph re-serializer (SubIFD trees + offset aliasing) +
    OME-XML `<Image>` surgery that carries instrument/channel/acquisition/vendor
    metadata verbatim. This is a substantial undertaking; [Bio-Formats](https://www.openmicroscopy.org/bio-formats/)
    (`bioformats2raw` + `raw2ometiff`) is the recommended interim answer for
    workflows that need full OME metadata fidelity.
  - Additional deferred items (independent of the above): **SVS thumbnail/macro/
    overview `replace`** (Aperio-conformant abbreviated JPEG — entropy-only strips
    + shared `JPEGTables` + APP14); `--rotate {90,180,270}` for label orientation
    correction; `--if-exists {remove,skip,error}` for idempotent scripted remove;
    DICOM-WSI associated-instance drop/swap (separate DICOM series logic).
- **`tagset`** — in-place TIFF tag edit (e.g. ImageDescription, Software). Useful for fixing one bad slide in a pool without full re-encode.
- **`inventory`** — walk a directory; dump CSV/JSON of slide metadata for pool-management UIs.
- **`verify`** — open every IFD, decode every tile, report errors. "fsck for WSI."
- **`diff`** — compare two slides (pixel diff, metadata diff, IFD ordering diff).

### Larger items
- **`tile-server`** — HTTP DZI/IIIF tile server; analog of openslide-python `deepzoom_server.py`. Activates opentile-go v0.13's splice-prefix optimization (TilePrefix / TileBodyInto / SpliceJPEGTile).
- **`convert --to dicom`** (DICOM-WSI writer) — emit a DICOM VL Whole Slide
  Microscopy Image set (Sup. 145). Analog of `wsi2dcm` / `wsidicomizer`.
  **Approach decided (2026-06-03):** pure-Go on `suyashkumar/dicom`, **porting**
  wsi2dcm's WSM-IOD-assembly logic (both Apache-2.0 → direct C++→Go port, no
  clean-room; attribute Google's NOTICE) rather than wrapping it — wrapping would
  drag in OpenSlide (LGPL-2.1, which opentile-go exists to replace) + OpenCV /
  Boost / DCMTK. Port the *logic* into wsitools idioms (pure-Go focused packages;
  reuse `internal/source`, `internal/pipeline`, the tile-copy fast path), NOT a
  foreign code shape. Largest single target; phased (P0 TILED_FULL brightfield
  spike → P1 full pyramid + tile-copy → P2 sparse/label/concatenation → P3
  fluorescence). Rough scoping: `docs/notes/2026-06-03-dicom-writer-scoping.md`.
  - ✅ **DONE (2026-06-11): Phase 0 spike.** `convert --to dicom -o out.dcm
    --level N <input.dcm>` emits ONE conformant WSM **VOLUME** instance from a
    **DICOM source**: the source's compressed JPEG frames are copied **verbatim**
    (byte-identical, no decode/re-encode), re-encapsulated as TILED_FULL
    multi-frame PixelData; one level per invocation (`--level`, default `0`).
    **De-risk result: positive** — `dciodvfy` (dicom3tools) reports **0 errors**
    on both L0 (65536², 16384 frames) and reduced L2 instances, and the output
    round-trips through opentile-go (read back as `Format: dicom`, frames
    byte-identical). Built on `github.com/suyashkumar/dicom` v1.1.0 (pure Go,
    now a direct dep); new `make dicom-validate` target. The Go port is "a few
    hundred lines," **not a swamp.** Known P0 limitation: a source lacking an
    embedded ICC profile would reintroduce a Type 1C gap (ICCProfile in
    OpticalPath) — fine for P0 (DICOM input carries ICC in practice).
  - ✅ **DONE (2026-06-11): Phase 1, first slice — non-DICOM single-level.**
    `convert --to dicom --level N <input.svs>` emits ONE conformant WSM **VOLUME**
    instance from a **non-DICOM source** (SVS etc.), one pyramid level: the level's
    JPEG-baseline tiles are copied **verbatim** (non-JPEG codecs error clearly).
    `PhotometricInterpretation` is **marker-driven** — probed from the first tile's
    Adobe APP14 + chroma-subsampling markers (**RGB** for the Aperio APP14 raw-RGB
    variant, `YBR_FULL_422` / `YBR_FULL` for subsampled / 4:4:4 YCbCr). ICC is
    carried from the source or a canonical **sRGB** profile is **synthesized** when
    absent (closes the P0 Type 1C gap). Validated with `dciodvfy` (**0 errors**)
    **and** a pixel round-trip on the CI fixture CMU-1-Small-Region.svs (decode the
    emitted DICOM honoring its photometric → byte-identical RGB). `make
    dicom-validate` extended to both the DICOM→DICOM and SVS→DICOM paths; DICOM→DICOM
    output unchanged.
  - ✅ **DONE (2026-06-11): Phase 1 slice 2 — full pyramid.** `convert --to dicom
    -o <dir> <input>` now emits the **full resolution pyramid by default** as a
    multi-instance Series: one WSM VOLUME instance per source level written as
    `<dir>/level-<n>.dcm` (n=0 = full resolution), all sharing Study / Series /
    FrameOfReference UIDs with per-instance SOPInstanceUID and
    `InstanceNumber = level+1` (no Pyramid UID, matching the Grundium golden).
    `--level N` still selects the single-instance path. Directory output is
    **atomic** (temp sibling dir → rename on success; failure removes it, never a
    partial pyramid). Per-level **spatial-metadata fix**: `PixelSpacing` scales by
    each level's downsample factor while `ImagedVolumeWidth/Height` stays the
    constant L0-derived physical extent, so levels co-register (also fixes a latent
    base-MPP/shrunken-extent bug in the single-level non-L0 path). `dciodvfy`
    reports **0 errors** on **every** level of the Grundium full pyramid (L0/L1/L2)
    and on the SVS instance.
  - ✅ **DONE (2026-06-11): Phase 1 slice 3 — JPEG 2000.** `convert --to dicom`
    now also accepts **JPEG 2000** sources (single-instance + full pyramid): the
    raw J2K codestream is tile-copied **verbatim**. **Codestream-derived
    photometric** — a new `jp2kmeta` SIZ/COD parser sets `PhotometricInterpretation`
    to RGB / `YBR_ICT` / `YBR_RCT` / `MONOCHROME2`. **Reversibility-driven transfer
    syntax** — reversible/lossless → `…4.90` + `LossyImageCompression "00"`,
    irreversible/lossy → `…4.91` + `"01"` + `ISO_15444_1`. `dciodvfy` reports
    **0 errors** on **every** level of the JP2K-33003-1.svs pyramid (RGB / `.91` /
    lossy) + an RGB pixel round-trip. Also a general **DS-VR PixelSpacing-length
    fix** (`formatDS`): non-power-of-2 level ratios + a non-round MPP previously
    produced 21-char `PixelSpacing` values that exceeded the 16-char DS VR limit.
    The `YBR_ICT`/`YBR_RCT` (MCT=1) and `.90`/lossless branches are unit-tested
    only (no fixture); >8-bit / `.jp2`-boxed JPEG 2000 out of scope.
  - ✅ **DONE (2026-06-12): Phase 2 — associated images.** Full-pyramid mode now
    also emits the slide's **associated images** (label/overview/thumbnail,
    macro→overview) as **same-Series single-frame WSM instances** at
    `<dir>/<type>.dcm` (shared Study/Series/FrameOfReference, `InstanceNumber`
    continuing after the levels). `assembleWSMDataset` was generalized into a pure
    builder over a per-instance **`instanceSpec`** (both the pyramid-level path and
    the new `writeAssociated` build a spec). `ImageType[2]` flavor + per-type
    `SpecimenLabelInImage`; the **SlideLabel module** (`LabelText` + `BarcodeValue`,
    Type 2, empty/anonymous) is emitted for label/overview (dciodvfy required it).
    Default-on, skipped by `--no-associated`; `--level N` emits none. Associated
    images whose codec is neither JPEG nor JPEG 2000 (e.g. an LZW label) are
    **skipped with a logged warning** — no partial file, the pyramid still
    completes. `dciodvfy` reports **0 errors** across all instances (pyramid levels
    + associated); `make dicom-validate` now validates every `<dir>/*.dcm`.
  - ✅ **DONE (2026-06-12): Phase 2 follow-on — associated transcode.** An
    associated image whose codec is **not** a DICOM transfer syntax (e.g. the
    **LZW label** on every Aperio SVS) is no longer skipped — it is **decoded and
    stored as an uncompressed native RGB instance** (Explicit VR LE, VR `OB`,
    `LossyImageCompression "00"`, lossless — keeps the barcode scannable); JPEG /
    JPEG 2000 still tile-copy verbatim-encapsulated. Decode delegated to
    **opentile-go v0.38.1** (`AssociatedImage.Decode`, opentile-go#20); the
    `extract` TIFF-reparse workaround is deleted. The native `label.dcm`
    pixel-round-trips byte-identically to the source decode and passes `dciodvfy`
    (0 errors); `make dicom-validate` emits the full SVS pyramid so the native
    label is covered. Fixed en route: the writer must use VR `OB` (not `OW`) for
    8-bit native pixel data, and **opentile-go#21** (reader's native-RGB
    associated decode — an even-length pad byte broke `SamplesPerPixel`
    inference). Spec/plan: `docs/superpowers/{specs,plans}/2026-06-12-dicom-writer-associated-transcode*`.
  - **Next:** **HTJ2K** / **16-bit** support, the pre-existing DICOM-source
    codec-mislabel bug, and the golden's rotated label
    `ImageOrientationSlide` / faithful label `PixelSpacing` (`.jp2`-boxed
    associated images out of scope). Plus TILED_SPARSE, Concatenations (P2)
    and fluorescence (P3).
- **`convert --to dzi --skip-blanks <threshold>`** — drop tiles whose pixels are within `threshold` of uniform background (e.g. white margin around the tissue). OpenSeadragon treats missing DZI tiles as background. Could cut 30-50% of encodes on tissue slides where slide-background dominates the L_max grid. NOT applicable to `--to szi` (SZI spec forbids sparse tile trees). DZI-only. ~200 LOC. v0.17 confirmation: libvips defaults to NOT skipping blanks either — this is a NEW capability, not catch-up.
- ✅ **DONE (2026-06-12): faithful associated-image copy across TIFF writers**
  (wsitools#1). `convert --to {cog-wsi,svs,tiff,ome-tiff}` + `--factor` corrupted
  associated images: the `AssociatedImage.Bytes()` passthrough wrote a single
  standalone strip that dropped the source's `Predictor (317)` (LZW labels →
  garbage/truncation) and `JPEGTables (347)` (abbreviated-JPEG thumbnails →
  undecodable). Now copied byte-faithfully via opentile-go **v0.39.0**
  `Slide.AssociatedSourceOf` (#22): verbatim source strips + exact tags, through
  new multi-strip support in `cogwsiwriter`/`streamwriter`; ok=false (synthesized/
  tiled) falls back to decode→re-encode. The 5 corrupt `cog-wsi/*_cog-wsi.tiff`
  fixtures were regenerated + verified. Root cause was NOT the LZW encoder (it
  round-trips byte-perfect) and NOT a regression. opentile-go→v0.39.0. Spec/plan:
  `docs/superpowers/{specs,plans}/2026-06-12-associated-faithful-copy*`. (OME-TIFF
  reader still can't decode LZW/multi-strip associated on read-back — separate
  upstream limitation; the written bytes are faithful.)
- ✅ **DONE (2026-06-05): unify downsample/convert scaling.** `convert --factor N`
  / `--target-mag M` ships for `svs|tiff|ome-tiff|cog-wsi` (reduce-then-rebuild
  via the shared `internal/downscale` engine, per-target MPP×N / mag÷N scaling).
  `downsample` is now **format-preserving** (reduces SVS/OME-TIFF/generic-TIFF/
  COG-WSI in place, sharing the same engine; errors with a `convert` pointer for
  non-writable source formats). Spec/plan:
  `docs/superpowers/{specs,plans}/2026-06-05-convert-factor-scaling*`.
  **Still deferred:** `dzi`/`szi --factor` (base-reduced DeepZoom). Possible
  follow-up: split the ~950-line `cmd/wsitools/convert_factor.go` (four target
  paths + cog-wsi pyramid helpers) into focused files / a writer interface.

## Codecs (write-side, separate from utilities)

### Deferred from v0.2
- `jpegli` — blocked on Homebrew libjxl shipping libjpegli OR build-from-source.
- `HEIF`, `JPEG-LS`, `JPEG-XR`, `Basis Universal` — queued.
- `jpeg2000` as a transcode-encoder target — decoder shipped; encoder wrapper queued.

## Source format support

### Deferred from v0.2
- Leica SCN (multi-channel fluorescence) — multi-channel pipeline plumbing deferred.

## Architectural

### Deferred from v0.2
- Streaming retrofit for `downsample` — currently materialises full L0 raster.

### Deferred from v0.21 (added 2026-05-31)
- **Constant-memory `convert --to dzi|szi` cascade.** The pyramid-descent generator holds full-width strip buffers across every level, so peak RSS scales with slide width (~3.5 GB on CMU-1.ndpi, ~5.4 GB on OS-2.ndpi). The v0.21 soft memory cap prevents host OOM but trades throughput under pressure; a true fix needs the cascade to process the source in column bands (or the opentile-go read path to bound its per-frame caches by bytes — partially addressed by v0.30's `OPENTILE_READ_MEMORY_BUDGET`). Tier 2 of the memory work; see `docs/superpowers/specs/2026-05-30-memory-safety-cap-design.md`.

### Deferred from v0.3
- TilePrefix / TileBodyInto / SpliceJPEGTile adoption — only valuable if `tile-server` is built.

### Deferred from v0.17 (added 2026-05-26)
- **Parallel raw-tile fetch + decode for `convert --to svs|tiff|ome-tiff --codec X` on striped sources.** v0.17's ScaledStrips wiring speeds up DZI/SZI rendering but doesn't help TIFF-family re-encode because those targets keep L0 dimensions intact (no scaling). A separate parallel-decode path on the existing `internal/pipeline` worker pool would help striped→TIFF re-encode where opentile-go's per-tile synthesis is the bottleneck. Quantify the gap first — measure striped→TIFF re-encode runtime against a tiled source baseline.

### Deferred from v0.20 (audit complete 2026-05-29; outcomes below)
- ✅ **DZI cascade kernel** — audited. wsitools `convert --to dzi|szi` uses 2×2 box averaging; libvips dzsave does too (`--region-shrink=mean` default). Decoded pixels were bit-identical across three sample levels of CMU-1-Small-Region.svs. No change. Findings: `docs/notes/2026-05-29-dzi-kernel-audit.md`.
- ✅ **Separable Lanczos3 in opentile-go** — DONE. opentile-go v0.32.2 ships a separable, weight-cached two-pass Lanczos (WSILabs/opentile-go#9), now ~7–8× box (was 213×). wsitools bumped to v0.32.2.
- 🚫 **`downsample` CLI spatial `--kernel` flag** — SHELVED (2026-06-03). Reading the code showed the premise was wrong: `downsample` already uses the right tool per stage (JPEG→libjpeg fast-scale, JP2K→box, cascade→box = libvips dzsave parity). A spatial Lanczos kernel in a *tiled* reducer would be slower AND introduce tile-boundary seams. The real win is **codec-domain scaled decode** via `DecodeOptions.Scale` (faster + anti-aliased + seam-free), tracked in opentile-go umbrella #11 (JP2K #10, HTJ2K #12, WebP/JXL queued) + a future codec-agnostic `downsample` refactor. See `docs/notes/2026-05-29-dzi-kernel-audit.md`.

## Quality gates

### Deferred from v0.2
- Visual-fidelity tests via mini decoders — decode v0.2 codec outputs through matching codec library; pixel-compare against source.

### Test coverage
- ✅ **CI fixture pipeline** — shipped v0.19. CI downloads CMU-1-Small-Region.svs + CMU-1.ndpi from `wsilabs/wsi-fixtures` v1 and runs the integration suite on every push + PR.
- ⏳ **Cross-version pixel parity check.** Compare v(N) convert/downsample output's decoded pixels to v(N-1) output's decoded pixels. File-SHA comparison won't work (embedded WSIToolsVersion tag changes with each release), but pixel-equality should hold if no decoder/encoder/resample logic changed. Would catch silent regressions in the decode-resample-encode chain across version bumps.
- ⏳ **`make ci-full` target.** A comprehensive per-release sweep that runs every fixture-gated test and refuses to pass on ENOSPC (instead of silently skipping). Today's pattern of "allow ENOSPC as environmental" is too forgiving — regressions hiding specifically in the largest-sample path can slip through.
- ⏳ **Expanded fixture coverage.** Per-format CI coverage beyond SVS + NDPI (Philips, OME-TIFF, BIF, IFE, SCN, MRXS, DICOM, SZI, generic-TIFF, COG-WSI). Audit + add incrementally.
