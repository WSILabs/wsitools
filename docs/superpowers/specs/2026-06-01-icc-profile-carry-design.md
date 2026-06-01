# ICC profile carry-through — design

**Status:** APPROVED design (2026-06-01)
**Scope:** Emit the source ICC color profile (TIFF tag 34675) on the
output L0 of every TIFF write target (`cog-wsi`, `svs`, `tiff`,
`ome-tiff`) and `downsample`. Pulled from opentile-go's cross-format
`Slide.ICCProfile()`. First slice of the per-target metadata/conformance
work (sub-project #1).

---

## 1. Problem

`convert`/`downsample` drop the ICC profile on every path. A color
profile is genuine, irreplaceable source metadata (e.g. CMU-1.svs carries
a 141,992-byte ICC profile). Without it, color-managed viewers can't
reproduce the slide's colors correctly. ICC is the one field in the
`svs→svs` conformance audit that is *true source data* (the rest —
ImageDepth, YCbCrSubSampling, … — are derive/default conformance fields,
handled in sub-project #2).

ICC is **cross-cutting**: it belongs to every TIFF target's schema, so it
ships as its own slice ahead of the per-format conformance work.

### Out of scope
- The derive/default SVS-conformance tags (ImageDepth 32997,
  YCbCrSubSampling 530, Orientation 274, ReferenceBlackWhite 532,
  PageNumber 297) — sub-project #2.
- SZI/DZI ICC (no TIFF tag; would be JPEG-APP2 tile-embedding) — later.
- Per-associated-image ICC: opentile exposes one slide-level ICC
  (`Slide.ICCProfile()`); we emit it on the main L0 only.

---

## 2. Decisions (sealed)

| # | Decision | Choice |
|---|---|---|
| D1 | Source | opentile `Slide.ICCProfile() []byte` (cross-format, v0.31). NOT a raw tag lookup. |
| D2 | Placement | Output **L0 only**, tag 34675, TIFF type UNDEFINED. |
| D3 | Pull tier | Tier 1 (dedicated accessor), surfaced through wsitools `source.Metadata.ICCProfile`. |
| D4 | Generated-override | N/A — ICC is carry-only, wsitools never generates it; no collision. |
| D5 | Empty source | `len(ICCProfile)==0` → emit nothing (no fabricated tag). |
| D6 | downsample | Carries ICC unchanged — downsampling doesn't change color space. |

---

## 3. Components

### 3.1 `internal/source`
- `Metadata` gains `ICCProfile []byte` (after the MPP fields).
- `opentileSource.Metadata()` sets `m.ICCProfile = s.t.ICCProfile()`
  (returns nil/empty when the source has none; pass through verbatim).

### 3.2 `internal/tiff/streamwriter` (backs svs/tiff/ome-tiff + downsample)
- `Options` gains `ICCProfile []byte`; `Writer` stores `iccProfile`,
  assigned in `Create`.
- `addL0Metadata` appends, when `len(w.iccProfile) > 0`:
  `b.AddUndefined(tiff.TagICCProfile, w.iccProfile)`.
  (Add `TagICCProfile uint16 = 34675` to `internal/tiff/tags.go`; the
  name already exists in `tagnames.go`.) `EntryBuilder.Encode` sorts, so
  placement among the other L0 tags is automatic.

### 3.3 `internal/tiff/cogwsiwriter` (backs cog-wsi)
- `Metadata` gains `ICCProfile []byte`; `populateLevelIFD` emits
  `b.AddUndefined(tiff.TagICCProfile, opts.Metadata.ICCProfile)` on L0
  when non-empty.
- **Layout (`layout.go`) — the load-bearing change:** the L0 IFD's
  pre-computed `externalSize` must include the ICC blob. ICC is large
  (~142 KB) and far exceeds the inline cap, so it goes external.
  `ifdSizeForLevel` (L0 only) adds: one directory entry (12 classic / 20
  BigTIFF) **and** `uint64(len(ICCProfile))` external bytes when present.
  Reuse/extend the existing external-byte accounting; assert it in a test
  against the real emission. (The fixed `countTagsForLevel` bump alone is
  insufficient — the *bytes* must be budgeted.)

### 3.4 Callers — `convert` (4 targets) + `downsample`
Each already does `md := src.Metadata()`. Add to the writer options:
- streamwriter targets (`convert_tiff.go` both literals; `downsample.go`):
  `ICCProfile: md.ICCProfile`.
- cog-wsi (`convert_cogwsi.go` Metadata literal): `ICCProfile: md.ICCProfile`.

---

## 4. Data flow
```
src.ICCProfile() ─▶ source.Metadata.ICCProfile ─▶ md
   convert/downsample: writer Options/Metadata.ICCProfile = md.ICCProfile
        ▼ (len>0)
   streamwriter.addL0Metadata / cogwsiwriter.populateLevelIFD
        AddUndefined(34675, icc)         cog-wsi: + layout externalSize += len(icc)
        ▼
   L0 IFD: tag 34675 UNDEFINED, bytes verbatim
```

---

## 5. Testing
- **streamwriter:** Options with a synthetic ICC blob (e.g. 5000 bytes) →
  output L0 has tag 34675, UNDEFINED, byte-identical length; empty ICC →
  no 34675 (presence via tiffinfo, length via re-read).
- **cogwsiwriter:** layout test — a 142 KB ICC is budgeted (the
  external-size helper matches emitted bytes); end-to-end write produces a
  valid file (existing round-trip/decode tests pass; `dump-ifds --raw`
  reads tag 34675 with the right length).
- **Integration (fixture-gated, acceptance):** CMU-1.svs (ICC = 141,992 B)
  → `convert --to {svs,tiff,ome-tiff,cog-wsi}` and `downsample --factor 2`
  each produce output whose L0 tag 34675 is **byte-identical**
  (count 141,992) to source. JP2K-33003-1.svs (also has ICC) likewise.
  scan_620_.svs (no ICC) → output has **no** 34675.
- **No regression:** tile-copy bit-exactness, ImageDescription/resolution/
  WSI tags unchanged, tile order, full `-race` suite green.

---

## 6. Success criteria
- ICC profile present and byte-identical in the output of all four TIFF
  targets + downsample, for any source that has one.
- Sources without ICC emit no 34675 (no fabricated tag).
- cog-wsi output with a 142 KB ICC is well-formed (layout externals sized
  correctly); decode/round-trip tests pass.
- No regression in pixels, tile order, or other metadata.

---

## 7. References
- opentile-go v0.31 `Slide.ICCProfile()` (slide.go:96); `EntryBuilder.
  AddUndefined` (internal/tiff/entry.go:134).
- SVS-conformance tag audit (2026-06-01): ICC is the only true source-data
  gap; rest are derive/default (sub-project #2).
- Builds on the scale-metadata milestone (resolution + WSI MPP tags) and
  the cogwsiwriter layout-budget pattern fixed there.
