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

Determine what it would take for wsitools to **write** a conformant BIF
(Biolmagene Image File / Roche Ventana) and scope the smallest version worth
building. Today wsitools can *read* BIF (via opentile-go) but `convert --to`
targets are only `cog-wsi|svs|tiff|ome-tiff|dzi|szi|dicom` — there is no BIF
emitter anywhere in `internal/`.

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

### In scope (the recommended v1 — "Tier B, narrowed")

A spec-compliant DP 200 BIF writer for the **common single-image case**:

- **Single AOI** (one rectangular scanned region = the whole pyramid; no
  multi-AOI convex-hull merging).
- **Brightfield, 8-bit RGB, single focal plane** (no Z-stack; `Z-layers=1`).
- **No tile overlap** (`OverlapX=0, OverlapY=0` — abutting tiles; spec-legal, see
  §4.5). Source pyramids from other WSIs have non-overlapping tiles, so this is
  the natural and correct choice.
- **Two source modes:**
  - *Transcode* — any opentile-readable WSI → BIF (re-encode pyramid tiles to
    JPEG/YCbCr).
  - *BIF→BIF transform* — verbatim tile copy (the motivating use case: produce a
    label-stripped BIF that stays a BIF; see §8).

### Explicit non-goals (v1)

Multi-AOI scans; volumetric Z-stacks; tile overlap synthesis; the legacy-iScan
dialect; fluorescence/non-brightfield; bit depths other than 8; producing files
that satisfy readers we cannot test against (see §7, the oracle problem).

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
1. **`internal/tiff/bifwriter`** — a new **spool-and-finalize** writer modeled on
   `cogwsiwriter` (NOT a streamwriter extension). Rationale: BIF tile file-offsets
   cannot be assigned until emission order (serpentine) is fixed, and the offset
   array may be sparse (empty tiles). Responsibilities: spool tiles → plan
   serpentine-ordered offsets → emit IFD 0/1/2/3+ with the role tags → patch the
   flat IFD chain + header. Reuses `internal/tiff` primitives throughout.
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

1. **Round-trip pixel identity:** write BIF → open with opentile-go BIF reader →
   `hash --mode pixel` equals the source. This is the primary functional gate
   (catches serpentine errors — wrong order fails the hash).
2. **Conformance self-check against Appendix A:** assert the exact tag set per IFD
   role, the mandated constant values (`ScannerModel="VENTANA DP 200"`,
   `EncodeInfo Ver≥2`, `FlagJoined=1`, `Confidence=100`, `OverlapY=0`, odd
   `Z-layers`), and the `level=N mag=M quality=Q` grammar. This validates against
   the *spec*, not just the reader.
3. **Structural byte-parity (optional):** opentile-go has a tifffile oracle
   (`tests/oracle/tifffile_test.go`) — a tifffile-based structural read of our
   output would be an independent cross-check of the TIFF/BigTIFF layout.

## 6. Effort & phasing

- **Phase 0 — de-risk the central gap (spike):** implement the serpentine remap +
  a minimal `bifwriter` that re-containers one source level into a tiled BIF IFD 2
  with a hand-built `<iScan>`+`<EncodeInfo>`, and prove the round-trip pixel hash
  against opentile on one fixture. This validates the riskiest assumption
  (serpentine + EncodeInfo placement) before committing to the full writer.
- **Phase 1 — full single-AOI writer:** all IFDs (0/1/2/3+), full EncodeInfo
  synthesis, ICC, overview+probability generation, `--to bif` routing,
  conformance self-check.
- **Phase 2 (deferred / maybe never):** BIF→BIF verbatim transform for label
  removal (§8); multi-AOI; Z-stacks; legacy-iScan dialect.

Rough size: comparable to the DICOM writer effort — a new writer package + a
metadata-synthesis module + CLI wiring + a conformance harness. The EncodeInfo
generator and serpentine ordering are the genuinely novel parts; everything else
follows existing patterns.

## 7. Risks & open questions

- **The oracle problem (biggest risk).** There is no external BIF validator
  (no `dciodvfy` equivalent). Worse, **openslide cannot be a Tier-B oracle** — it
  *rejects* spec-compliant DP 200 BIFs (chokes on `Direction="LEFT"`, per
  `opentile-go/docs/formats/bif.md`). So our only reader oracle is opentile-go
  itself, which is partly circular (it proves round-trip, not third-party
  acceptance), plus tifffile for structural parity. **We cannot prove a real
  Ventana/Roche system would accept our output without access to one.** The
  conformance self-check (§5.2) mitigates but does not eliminate this.
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
- **Use-case fit:** if the real need is the label-removal use case (§8), a BIF→BIF
  *verbatim* transform (Phase 2) is both smaller and higher-fidelity than the full
  transcode writer — it copies real scanner tiles/metadata and only excises the
  label, sidestepping overview/probability/ICC synthesis. That may be the right
  first deliverable instead of Phase 1.

## 8. The motivating use case (label removal)

This investigation began from "remove the label but keep a BIF container." In BIF
the printed-label PHI lives in the **overview (IFD 0)** — opentile synthesizes the
"label" associated image as the **top 1/3** of the overview (whitepaper's
25mm-of-75mm label band; `formats/bif/associated.go`). So de-identifying a BIF
means **blanking the top label band of IFD 0** (and clearing label-bearing XMP
attributes like `Barcode1D/2D`), while copying everything else verbatim.

That is a **BIF→BIF verbatim splice** (Phase 2 here), architecturally closer to
wsitools' existing SVS `label remove` byte-splice than to a full transcode writer:
copy all pyramid/probability tiles unchanged, rewrite only IFD 0's pixel band and
the affected XMP, patch offsets. It needs the `bifwriter` offset machinery but
**not** the overview/probability/ICC synthesis or re-encoding. If label removal is
the actual goal, recommend scoping a `bif label remove` splice directly rather
than the general `--to bif` writer.

## 9. Recommendation

1. **A general `convert --to bif` is feasible but expensive and only
   self-verifiable** (the oracle problem). Build it only if there's a concrete
   consumer that needs wsitools to *originate* BIFs and that we can test against.
2. **If the real driver is label de-identification, do the BIF→BIF verbatim
   splice instead** (§8) — smaller, higher fidelity (real scanner data preserved),
   and it directly answers the original question. It still needs a `bifwriter`
   offset-patching core, so a **Phase 0 spike** (serpentine + offset patching +
   round-trip on one fixture) de-risks *both* paths and is the recommended next
   concrete step regardless of which we pursue.

**Decision needed:** which consumer must accept the output — our own pipeline
(opentile: achievable now), or a third-party/Roche system (needs an oracle we
don't have)? And: is the goal originating BIFs (`--to bif`) or de-identifying
existing ones (`bif label remove`)? The answer selects Phase 1 vs Phase 2 as the
first build.
