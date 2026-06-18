# BIF Writer — Feasibility & Design Spec

**Status:** feasibility (analysis + scoped design + recommendation). NOT a
committed implementation plan.
**Date:** 2026-06-17
**Author context:** Investigation drew on three primary sources — (1) the Roche
Digital Pathology **BIF whitepaper v1.0 (2020)**, the authoritative format spec
(`sample_files/bif/Roche-Digital-Pathology-BIF-Whitepaper.pdf`, 17 pp incl.
Appendix A tag-by-IFD matrix); (2) opentile-go's BIF **reader** as the canonical
reference implementation (`opentile-go@v0.45.2/formats/bif/` + `docs/formats/bif.md`);
(3) byte-level forensics on the five `.bif` fixtures. Whitepaper page citations
below are to that PDF.

## 1. Goal

Determine what it would take for wsitools to **write** conformant BIF
(Biolmagene Image File / Roche Ventana). Two drivers, both first-class:

1. **Originate** — `convert --to bif`: write a BIF from any opentile-readable WSI
   (SVS, generic-TIFF, etc.).
2. **Modify existing BIFs** — BIF→BIF transforms that keep the BIF container:
   downsample, crop, metadata/associated-image edits, and **label removal as one
   subset** of these.

Today wsitools can *read* BIF (via opentile-go) but `convert --to` targets are
only `cog-wsi|svs|tiff|ome-tiff|dzi|szi|dicom` and there is no BIF emitter
anywhere in `internal/`, so neither driver is possible yet.

The two drivers share a writer core but differ sharply in difficulty (§2): the
modify path can often **copy** the source's real scanner metadata (EncodeInfo,
iScan XMP, ICC) verbatim, where origination must **synthesize** it.

## 2. What "compliant BIF" means (and the scope boundary)

The whitepaper **only specifies the VENTANA DP 200 generation** and states
explicitly (p.3) that legacy iScan Coreo/HT BIFs "cannot be reconstructed
correctly based on the information included in this document and should not be
attempted." Therefore:

- **"Compliant" = DP 200-shaped:** BigTIFF, `ScannerModel="VENTANA DP 200"`, the
  fixed IFD-role layout in §4, an `EncodeInfo Ver≥2` stitch graph, serpentine
  tile order.
- The legacy-iScan variant (classic TIFF, shared JPEGTables, `Thumbnail` IFD) is
  **out of spec** and is a non-goal. It is only *readable* because openslide and
  opentile-go are permissive.

All output targets the **spec-compliant DP 200 dialect** (BigTIFF, the IFD roles
in §3.2, `EncodeInfo Ver≥2`, serpentine order). Within that, work splits by
difficulty:

**(A) Modify-existing, L0-preserving — easiest, highest fidelity, recommended
first.** Transforms that do *not* change the level-0 tile grid: label removal,
associated-image edits, metadata edits. These **copy the source BIF's real tiles,
EncodeInfo, iScan XMP, ICC, and probability image verbatim** and rewrite only the
targeted bytes (e.g. blank the overview label band + clear barcode attributes for
label removal). No EncodeInfo synthesis, no re-encode, no overview/probability
fabrication — closest to wsitools' existing SVS `label remove` byte-splice.

**(B) Modify-existing, L0-changing — medium.** Crop and downsample change the
level-0 tile grid, so the EncodeInfo stitch graph and tile-offset arrays must be
**regenerated** for the new geometry (same machinery as origination), but the
iScan XMP / ICC / scanner identity can still be carried from the source.

**(C) Originate (`convert --to bif`) — hardest.** Everything synthesized from a
non-BIF source: EncodeInfo, iScan XMP, overview + probability IFDs, ICC.

The synthesis paths (B, C) are scoped to the **common single-image case**:
single AOI; brightfield, 8-bit RGB, single focal plane (`Z-layers=1`); no tile
overlap (`OverlapX=0, OverlapY=0` — abutting tiles; spec-legal, the natural
choice when the source pyramid is non-overlapping). The copy path (A) inherits
whatever the source has (incl. multi-AOI / overlap) because it doesn't touch the
stitch graph.

### Explicit non-goals (v1)

*Synthesizing* multi-AOI scans, volumetric Z-stacks, or tile overlap (path A
preserves them if already present); the legacy-iScan dialect as an output target;
fluorescence/non-brightfield; bit depths other than 8.

## 3. Authoritative format requirements (from the whitepaper)

### 3.1 Container
- BigTIFF, little-endian (p.2–3). DP-class slides exceed 4 GB so BigTIFF is
  mandatory; wsitools' `internal/tiff` BigTIFF auto-promote covers this.
- No BIF-specific magic. Detection (opentile/openslide) keys **solely** on the
  substring `<iScan` appearing in an XMP packet (tag 700) on some IFD
  (`formats/bif/detection.go`).

### 3.2 IFD roles (Appendix A, p.17 — the authoritative emission checklist)

| IFD | Role | Storage | ImageDescription (270) | XMP (700) | ICC (34675) |
|---|---|---|---|---|---|
| 0 | Overview (whole slide incl. label) | **striped JPEG, sRGB** | `Label_Image` | `<iScan>` block | no |
| 1 | Tissue probability | **striped LZW, 8-bit gray** | `Probability_Image` | `<PreScanData>` block | no |
| 2 | High-res scan (level 0) | **tiled JPEG, YCbCr** | `level=0 mag=M quality=Q` | `<EncodeInfo>` block | **yes** |
| 3+ | Pyramid levels 1..N | **tiled JPEG, YCbCr** | `level=N mag=M quality=Q` | **none** | no |

Tag presence per IFD (Appendix A): striped IFDs carry 256,257,258,259,262,270,
273,277,278,279,284,305,306,700; tiled IFDs carry 256,257,258,259,262,270,277,
284,305,306,322,323,324,325,347(optional),530,532,700(IFD2 only),34675(IFD2
only),32997(volumetric only). **No `Make` (271), no `SubIFDs` (330), no
`NewSubfileType` (254) anywhere** — confirmed against fixtures.

### 3.3 Pyramid geometry & tiles
- Tiles typically 1024×1024 (p.5). Each level halves both dimensions (dyadic, p.6).
- `ImageDescription` grammar (p.12): three space-delimited `key=value` tokens —
  `level=N mag=M quality=Q`. `mag` "accurately describes the resolution of the
  current pyramid layer (do not compute from other data)"; `quality` ∈ [70,100].
- Per-level MPP derives from `<iScan>/@ScanRes` (µm/px at level 0); each level
  doubles (`formats/bif/level.go`).
- Tiles stored at full `TileSize`; edge tiles padded.

### 3.4 Serpentine tile order (p.5 Fig 2, p.15 Fig 4 — MANDATORY)
- Physical/Stage coordinate system: origin **lower-left**, tile index 1 starts
  lower-left, proceeds right, then up and to the left (boustrophedon).
- `TILE_OFFSETS[0]` = the tile named by the **first `<Frame>` node** in
  `EncodeInfo` (p.15). The Frame-node order *defines* the on-disk tile order.
- Image coordinate system (used by `Frame/@XY`): origin top-left, `XY="col,row"`.
- A writer emitting row-major tiles is read **scrambled** (this is the central
  correctness gap). The remap is `formats/bif/serpentine.go::imageToSerpentine`;
  opentile verifies it byte-equal against tifffile.

### 3.5 EncodeInfo stitch graph (p.12–15 — REQUIRED, was wrongly thought optional)
The spec *requires* `<EncodeInfo>` on IFD 2; the reader uses it to place tiles.
`Ver` must be ≥2 ("stop processing if <2", p.12). Structure:
- `<SlideInfo>` → `<AoiInfo XIMAGESIZE YIMAGESIZE NumRows NumCols Pos-X Pos-Y>`
  (XIMAGESIZE/YIMAGESIZE = tile dims).
- `<SlideStitchInfo>` → `<ImageInfo AOIScanned AOIIndex NumRows NumCols Width
  Height Pos-X Pos-Y>` (one per AOI) → `<TileJointInfo>` children (multiplicity
  R(C−1)+C(R−1)): `FlagJoined=1`, `Confidence=100`, `Direction` ∈
  LEFT/RIGHT/UP/DOWN, `Tile1`/`Tile2` (serpentine indices), `OverlapX`,
  `OverlapY`. **Guards:** stop if FlagJoined≠1, Confidence≠100, or OverlapY≠0
  (p.13). DP 200 has horizontal overlap only.
- `<ImageInfo>` also → `<FrameInfo>` → `<Frame XY Z Focus>` (multiplicity
  R×C×Z; **Frame order = TILE_OFFSETS order**; Z≥1 frames may be ignored, p.14).
- `<AoiOrigin>` → `<AOI0 OriginX OriginY>…` (origins are multiples of tile size;
  both 0 for a single AOI, p.14).

**For the v1 single-AOI / no-overlap / no-Z case this is fully deterministic:**
Frame nodes in serpentine order, one TileJointInfo per adjacent pair with
`OverlapX=0 OverlapY=0`, `AoiOrigin` = (0,0).

### 3.6 iScan XMP on IFD 0 (Table 1b, p.7–8 — exact attributes)
`Mode="brightfield"` (const); `Magnification` (20|40); `ScanRes` (0.465|0.25);
`UnitNumber` (unsigned, ≥2,000,000); **`ScannerModel="VENTANA DP 200"` —
mandatory exact match, the reader stops otherwise**; `Z-layers` (odd);
`Z-spacing`; `UserName`; `BuildVersion`; `BuildDate`; optional `Barcode1D/2D`,
`ScanWhitePoint` (white-point pixel value, used to fill empty tiles),
`Anonymization`; plus `<AOI0 Left Top Right Bottom>` child(ren) in physical
(lower-left-origin) coordinates; `<ProcessingParameters>` (NA).

### 3.7 Empty/unscanned tiles (p.16)
`TILEOFFSETS[k]=0 AND TILEBYTECOUNTS[k]=0`; consumers fill with the
`ScanWhitePoint` RGB value. (For a single full-rectangle AOI with no gaps, there
are none — but the writer must support emitting a sparse offset array if the
source pyramid has missing tiles.)

### 3.8 Color
Scan tiles (IFD 2/3+) are device-dependent color requiring the ICC v4.0 in IFD 2
(applied to all pyramid levels, p.12); overview (IFD 0) is sRGB, no ICC.

### 3.9 Spec defects noted (read the primary source, found these)
- **IMAGE_DEPTH tag code self-contradiction:** p.5 prints `0x80BE` in one
  sentence and `0x80E5` in the next. Appendix A and opentile both use **32997 =
  0x80E5**; `0x80BE` (32958) is a typo. (Moot for v1 — no Z-stacks.)
- **Fixture vs spec:** `Ventana-1.bif` stores IFD 0 *uncompressed* though the
  spec says JPEG-striped — real files don't perfectly follow the whitepaper. A
  writer should follow the spec (JPEG-striped sRGB) but readers tolerate both.

## 4. Architecture

Follows the established wsitools pattern proven by the SVS writer: **synthesize
vendor metadata caller-side, emit it through a shared low-level writer.**

### 4.1 Reuse as-is
- **`internal/tiff` core, in full** — BigTIFF header/entries, LONG8 tile-offset
  arrays (`AddTileOffsets`), arbitrary/private tags (`RawTag`/`AddRaw`), XMP via
  `AddUndefined(700,…)`, `PatchUint32/64`, `MPPToResolution`, `ImageDepth`
  (32997). No changes.
- **JPEGTables handling** (`jpegtables.go`) — BIF tiles are YCbCr JPEG with **no
  APP14** marker, which is wsitools' default. Tag 347 is optional; v1 emits
  self-contained tiles (no shared tables), matching DP 200.
- **`faithfulStrippedSpec` / associated-copy** machinery for IFD 0/1 when copying.
- **The SVS "synthesize-caller-side" pattern** (`svs_imagedesc.go`/`svs_tags.go`)
  as the structural template.

### 4.2 New code
The full list below is what *origination* (path C) needs. **Path A (modify,
L0-preserving — Phase 1) needs only item 1** plus a byte-splice: it copies the
source's tiles, EncodeInfo/iScan/ICC, and probability IFD verbatim, so items 2–4
(the synthesis modules) are deferred to Phases 2–3.

1. **`internal/tiff/bifwriter`** — a new **spool-and-finalize** writer modeled on
   `cogwsiwriter` (NOT a streamwriter extension). Rationale: BIF tile file-offsets
   cannot be assigned until emission order (serpentine) is fixed, and the offset
   array may be sparse (empty tiles). Responsibilities: spool tiles → plan
   serpentine-ordered offsets → emit IFD 0/1/2/3+ with the role tags → patch the
   flat IFD chain + header. Reuses `internal/tiff` primitives throughout. Also
   hosts the path-A copy-and-splice mode (carry source IFDs verbatim, rewrite
   targeted bytes, re-patch offsets).
2. **`internal/bifxml` (writer side)** — synthesize the `<iScan>`, `<PreScanData>`,
   and `<EncodeInfo>` XML blobs. The EncodeInfo generator is the substantive
   piece (§3.5); deterministic for single-AOI/no-overlap/no-Z.
3. **`cmd/wsitools/convert_bif.go` + `bif_imagedesc.go`** — the `--to bif` driver
   and per-IFD `ImageDescription` synthesis (`level=N mag=M quality=Q`,
   `Label_Image`, `Probability_Image`).
4. **Overview + probability synthesis** — IFD 0 overview = a downsample of L0 to a
   slide-shaped sRGB JPEG (for transcode) or copied (BIF→BIF); IFD 1 probability =
   a minimal valid LZW grayscale tissue map (e.g. luminance threshold of the
   overview, or a flat map). The probability map is acquisition-time guidance the
   reader only uses to propose AOIs, so a simple synthesized map is conformant.
5. **Routing/plumbing** — `convert.go` `--to` accepts `bif`; `convert_shared.go`
   codec/tile-order acceptance gains a `bif` case allowing the serpentine order.

### 4.3 Serpentine ordering
Add a serpentine remap (`imageToSerpentine`/inverse) — either a new
`internal/tiff/tileorder` strategy or local to `bifwriter`. Mirror opentile-go's
`formats/bif/serpentine.go` exactly (it is the read-side counterpart; round-trip
is the test). Origin lower-left, even stage rows L→R, odd rows R→L.

## 5. Verification strategy

A **multi-oracle** approach — this is the key change from the original draft,
which over-weighted the "no validator" risk. We have several independent
consumers to test against:

**Automated (CI-able):**
1. **opentile-go round-trip pixel identity** — write BIF → reopen → `hash --mode
   pixel` equals the source. Primary functional gate; catches serpentine errors
   (wrong tile order fails the hash). Necessary but, being our own reader, not
   sufficient.
2. **openslide as an independent oracle** — openslide has a Ventana BIF driver.
   Read our output through openslide (Python `openslide` or `openslide-show-
   properties` / region reads) and compare pixels/dims. **Caveat to resolve
   empirically (§7):** opentile-go's notes say openslide *rejects* the DP 200
   fixture `Ventana-1.bif` over `Direction="LEFT"`. Whether our **zero-overlap**
   output trips the same path is unknown and is a Phase-0 question — if openslide
   accepts zero-overlap DP 200, it becomes our best automated third-party gate.
3. **Conformance self-check against Appendix A** — assert the exact per-IFD tag
   set, mandated constants (`ScannerModel="VENTANA DP 200"`, `EncodeInfo Ver≥2`,
   `FlagJoined=1`, `Confidence=100`, `OverlapY=0`, odd `Z-layers`), and the
   `level=N mag=M quality=Q` grammar. Validates against the *spec*, not a reader.
4. **tifffile structural parity (optional)** — opentile-go has a tifffile oracle;
   a tifffile structural read independently checks the BigTIFF/IFD/tag layout.

**Manual (authoritative, owner-driven):**
5. **Roche viewer** — the definitive consumer; if Roche's own viewer renders our
   output correctly, that is the strongest possible conformance signal. (Owner
   has access.)
6. **QuPath** — reads BIF via bio-formats and/or openslide, so it exercises *those*
   readers too (a third independent code path). (Owner has access.)
7. **Possibly Roche's own conformance tooling/SDK** — owner *may* obtain access;
   **do not count on it** for planning.

Build/test posture: gate (1)+(3) in CI from day one; add (2)+(4) once Phase 0
settles the openslide-dialect question; treat (5)+(6) as owner-run acceptance
checkpoints at the end of Phase 0 and each subsequent phase.

## 6. Effort & phasing

Sequenced easiest-first, so each phase ships a usable capability and the hard
synthesis work is de-risked before it's relied on.

- **Phase 0 — de-risk the core (spike + dialect resolution).** Build the
  serpentine remap + a minimal `bifwriter` that re-containers one level into a
  tiled BIF IFD 2 with a hand-built `<iScan>`+`<EncodeInfo>`; prove opentile
  round-trip pixel-hash on one fixture; then **run that output through openslide,
  Roche viewer, and QuPath** to settle the dialect question (§7). Validates the
  riskiest assumptions (serpentine placement *and* which dialect the real
  consumers accept) before any larger commitment.
- **Phase 1 — modify-existing, L0-preserving (path A; includes label removal).**
  The `bifwriter` offset/IFD-chain core + verbatim copy of tiles/EncodeInfo/iScan/
  ICC/probability, rewriting only targeted bytes. Ships `bif label remove` (and
  the other associated/metadata edits) — real scanner data preserved, no
  synthesis. Highest fidelity, smallest new surface.
- **Phase 2 — synthesis core + L0-changing modifies (path B).** EncodeInfo +
  serpentine offset *regeneration* for a new tile grid; wires up BIF→BIF
  `downsample`/`crop` (carry source iScan/ICC, regenerate stitch graph).
- **Phase 3 — originate (path C): `convert --to bif`.** Full synthesis from a
  non-BIF source: iScan XMP, EncodeInfo, overview + probability IFDs, ICC,
  per-IFD `level=…` ImageDescriptions, routing/plumbing.
- **Deferred / maybe never:** multi-AOI synthesis; Z-stacks; legacy-iScan dialect.

Rough size: comparable to the DICOM writer effort — a new writer package + a
metadata-synthesis module + CLI wiring + a conformance harness. The EncodeInfo
generator and serpentine ordering are the genuinely novel parts (and land in
Phase 2); Phase 1 is mostly offset-patching + byte-splice, close to the existing
SVS `label remove`.

## 6a. Phase 0 results (2026-06-17) — in-house reader testing & the tile-order truth

Phase 0 shipped (merged to main). Three real readers were then tested in-house
(openslide 4.0.0 + bio-formats `bftools` + opentile), which uncovered the central
correctness fact and overturned the writer's original assumption.

**THE DECISIVE FINDING: real Roche DP 200 stores tiles ROW-MAJOR, not serpentine.**
The whitepaper (Fig 2) describes a "serpentine" tile path, and opentile-go's BIF
reader hardcodes a serpentine remap accordingly — so the first writer stored
serpentine. That was **wrong**. Proof, three independent ways on the real
`Ventana-1.bif`:
- **Its own metadata says row-major.** The `<EncodeInfo>/<FrameInfo>/<Frame
  XY="col,row">` nodes declare the storage order (whitepaper p.14: "the order of
  the Frame-nodes … reflects the order in which the image tiles are stored in
  TILE_OFFSETS"). Real `Frame[k]` = `(k%cols, k/cols)` exactly — plain row-major
  (483 nodes = 23×21, in order).
- **Decoding the real tiles confirms it.** Re-compositing a real level's tiles
  row-major yields coherent tissue; serpentine yields a scramble.
- **bio-formats and openslide both read row-major** (they assume `TILE_OFFSETS[k]`
  is at image `(k%cols, k/cols)` top-left) and render the real file correctly.

So the "serpentine" is at most the physical scan/stage path, NOT the storage
order. The writer now **stores row-major** (commit `e7f263f`); `<Frame>` nodes
and tile numbering follow.

**Reader matrix on the corrected (row-major) output:**
- **bio-formats / QuPath: renders CORRECTLY** — coherent tissue matching the
  source, correct orientation/colors, full dims. This is the real-world
  acceptance target and it passes. (Required two other real-Roche-fidelity fixes
  to get there: tag 700 as TIFF type **BYTE+NUL** not UNDEFINED — commit
  `0d04175`, else "Content is not allowed in prolog"; and `<TileJointInfo>`
  directions **`LEFT`/`UP`** as real Roche emits — commit `1e952fe`, else a
  `maxYAdjust` integer underflow. Also pad `ImageWidth/Length` to whole tiles, as
  real Roche does, so bio-formats' integer `sizeX/tileWidth` grid math keeps the
  last row/col.)
- **openslide: rejects** (`"Bad direction attribute LEFT"`). Its Ventana driver
  only accepts `RIGHT`/`UP` — which is why it *also* rejects genuine Roche DP 200
  files. We correctly match real Roche (`LEFT`/`UP`); openslide is simply broken
  for DP 200 and is a metadata-only tool here. (An intermediate `RIGHT`/`UP` hack
  pleased openslide but broke bio-formats — a dead end, now reverted.)
- **opentile-go: reads metadata, SCRAMBLES pixels.** Its hardcoded serpentine
  remap mis-places the row-major tiles on any multi-tile level. **This is an
  opentile-go bug** (it only ever looked right on single-tile levels and on our
  earlier serpentine output, which matched the bug). The earlier "pixel-identical
  opentile round-trip" was our writer matching opentile's bug, not correctness.
  Read-side → **file an opentile-go issue** (boundary rule: we file, owner fixes
  upstream): its BIF reader should honor the `<Frame>` nodes, or default to
  row-major, not assume serpentine.

**Consequences for the plan:**
- **bio-formats/QuPath is the real-world placement oracle and it passes** on the
  row-major output. The convention question is fully resolved (row-major, proven
  from the file's own metadata) — no Roche viewer needed (Roche ships no free
  viewer anyway; uPath is enterprise-only).
- **opentile-go must be fixed** to read row-major/Frame-honoring before it can
  read real Roche DP 200 *or* our output correctly. Tracked as the next upstream
  issue.
- Lower-priority colour note: re-encoded/verbatim Aperio-SVS JPEG tiles (APP14/
  RGB) in a YCbCr-declared BIF can render with off colours in some readers — a
  Phase-1+ color-space concern for true origination, not a placement issue.

## 7. Risks & open questions

- **Tile-order question — RESOLVED (see §6a): real Roche DP 200 is ROW-MAJOR**,
  proven from `Ventana-1.bif`'s own `<Frame>` nodes + tile decode. The writer
  stores row-major; bio-formats/QuPath render it correctly. The remaining
  read-side item is an **opentile-go bug** (hardcoded serpentine remap scrambles
  real DP 200) — to be filed upstream. Historical framing retained below.
  is no single drop-in BIF validator (no `dciodvfy` equivalent), but §5's
  multi-oracle approach covers it: opentile round-trip + openslide + Roche viewer
  + QuPath + Appendix-A self-check. The remaining sharp question is **which BIF
  dialect satisfies the most consumers at once.** opentile-go's notes say
  openslide rejects the DP 200 fixture over `Direction="LEFT"`. There is a
  genuine tension:
  - *spec-compliant DP 200* (full EncodeInfo, Direction joints) → Roche viewer
    should accept; openslide *might* reject;
  - *legacy-ish / "regular BigTIFF"* (the DP 200 can also emit plain BigTIFF
    without stitch metadata, whitepaper p.3) → openslide-friendly but not the
    documented BIF.

  **Phase 0 must resolve this empirically:** emit spec-compliant zero-overlap DP
  200, run it through openslide and (owner) Roche viewer + QuPath, and see what
  each accepts. If they diverge, decide whether to (a) prioritize spec-compliance
  (Roche viewer is the authority) and accept openslide-incompatibility, or (b)
  emit a variant that threads all consumers. This is a real unknown but it is
  *testable now* with the oracles in hand — not the open-ended risk the first
  draft implied.
- **Probability + overview synthesis fidelity:** we'd be fabricating IFD 0/1 that
  no real scanner produced. Conformant per the spec, but their *content* is
  synthetic. Acceptable for v1; flag in output provenance.
- **Color fidelity:** re-encoded tiles would be sRGB-ish JPEG labeled YCbCr; the
  ICC story (device-dependent color) cannot be faithfully reproduced from a
  non-BIF source. We'd embed a generic/sRGB ICC and document the limitation
  (same posture as the DICOM writer's sRGB synth).
- **EncodeInfo realism:** our stitch graph is structurally valid but describes a
  trivial no-overlap grid; unknown whether any strict consumer expects non-zero
  overlaps or specific `Confidence`/`Pos-X/Y` semantics. Spec says OverlapX=0 is
  legal.
- **EncodeInfo regeneration correctness (paths B/C):** the synthesized stitch
  graph must stay self-consistent with the regenerated tile grid (NumRows/NumCols,
  Frame order = TILE_OFFSETS order, TileJointInfo adjacency). Bugs here read back
  scrambled — caught by the opentile round-trip hash, but it's the fiddliest code.

## 8. Label removal as a subset of modify-existing

Label removal is **one path-A transform**, not a separate track. In BIF the
printed-label PHI lives in the **overview (IFD 0)** — opentile synthesizes the
"label" associated image as the **top 1/3** of the overview (whitepaper's
25mm-of-75mm label band; `formats/bif/associated.go`). De-identifying a BIF means
**blanking the top label band of IFD 0** and clearing label-bearing iScan XMP
attributes (`Barcode1D`, `Barcode2D`), while copying everything else — pyramid
tiles, probability image, EncodeInfo, ICC — **verbatim**.

That is exactly the path-A byte-splice (Phase 1): copy all tiles + metadata
unchanged, rewrite only IFD 0's label-band pixels and the affected XMP, patch
offsets. It needs the `bifwriter` offset machinery but **none** of the
EncodeInfo/overview/probability/ICC *synthesis*. Architecturally it is the BIF
analog of wsitools' existing SVS `label remove`. So it falls out almost for free
once Phase 1's copy-and-patch core exists — no separate design needed.

## 9. Recommendation

The driver is **general BIF write + modify** (origination *and* in-place
transforms), with label removal as one case. Given that, and that we now have
real external oracles (openslide automated; Roche viewer + QuPath manual):

1. **Proceed, sequenced easiest-first** (§6): Phase 0 spike → Phase 1 (modify,
   L0-preserving, ships label removal) → Phase 2 (synthesis core + crop/downsample)
   → Phase 3 (`convert --to bif`). Each phase ships a real capability and de-risks
   the next.
2. **Phase 0 is the gating decision point.** It answers the one genuine unknown —
   **which dialect the real consumers accept** (spec-compliant zero-overlap DP 200
   through openslide / Roche viewer / QuPath). Cheap to build (one level, hand-
   written XML, one fixture round-trip) and it determines everything downstream.
3. **All output targets spec-compliant DP 200**; if Phase-0 oracle testing shows
   openslide can't read it, treat Roche viewer as the authority and keep openslide
   as a best-effort gate (don't compromise spec-compliance for it).

**Recommended next concrete step:** scope Phase 0 as a small implementation plan
(serpentine remap + minimal `bifwriter` + hand-authored iScan/EncodeInfo for one
re-containered level + opentile round-trip test), then run the owner-side oracle
checks on its output before planning Phase 1+. The only input needed from the
owner is access to the BIF fixtures (have them) and the manual viewer checks at
the end of Phase 0.
