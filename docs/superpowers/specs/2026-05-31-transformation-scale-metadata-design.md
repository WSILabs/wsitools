# Scale metadata across transformations — design

**Status:** APPROVED design (2026-05-31)
**Scope:** Make `convert` and `downsample` outputs self-describing for
physical scale (MPP, magnification, TIFF resolution) for **all** source
formats, with correctly-scaled values on downsample.

---

## 1. Problem

Audit of transformation metadata (2026-05-31) found:

1. **wsitools surfaces MPP only for SVS sources.** `internal/source.
   Metadata()` populates `MPP` solely via `svsfmt.MetadataOf` (the SVS
   format struct). For NDPI / Philips / OME-TIFF / BIF / generic-TIFF /
   COG-WSI it stays `0` — even though opentile-go normalizes every
   format's native scale into the cross-format `Metadata.MicronsPerPixelX
   /Y`. Confirmed: `info CMU-1.ndpi` shows magnification but **no MPP**.
   Consequence: `convert` from any non-SVS source emits **no** scale
   metadata.
2. **No writer emits TIFF resolution tags** (XResolution/YResolution/
   ResolutionUnit, 282/283/296). The streamwriter (svs/tiff/ome-tiff/
   downsample) emits none; the cogwsiwriter emits the WSI private MPP/mag
   tags but no resolution. Non-WSI-aware consumers (openslide, QuPath,
   generic TIFF tools) therefore can't read physical scale from any
   wsitools output.
3. **The streamwriter never emits the WSI private MPP/mag tags
   (65085–87).** Only the cogwsiwriter does. So `downsample` output (and
   `convert --to svs|tiff|ome-tiff`) lack the self-describing MPP/mag
   tags — for downsample, specifically the *scaled* values.

These are fidelity/completeness gaps, not wrong-value bugs — what is
emitted today is correct (downsample's Aperio ImageDescription is
correctly scaled). The fix adds the missing carriers and the cross-format
plumbing.

### Out of scope
- Preserving the original source ImageDescription in COG-WSI output (the
  new resolution tags make MPP openslide-readable; keep the provenance
  string).
- Rewriting the inert secondary Aperio banner inside the downsampled
  ImageDescription (fragile free-form parse; values readers use are the
  pipe-delimited `AppMag`/`MPP`, which are correct).
- Per-axis MPP for `downsample` source reading (downsample is SVS-only
  and Aperio is symmetric; it scales a single MPP for both axes).

---

## 2. Decisions (sealed)

| # | Decision | Choice |
|---|---|---|
| D1 | Resolution values | **Derived from MPP** (`XRes = pixels/cm = 10000/MPP`, ResolutionUnit = cm). Single source of truth; auto-scales on downsample; works for all formats. |
| D2 | MPP source in wsitools | Read opentile-go's cross-format `MicronsPerPixelX/Y` (SVS path kept as fallback). Surface **per-axis** `MPPX`/`MPPY` in `internal/source.Metadata`. |
| D3 | Per-axis | Writers consume `MPPX`/`MPPY` separately so asymmetric sources get `XRes ≠ YRes`. |
| D4 | COG-WSI ImageDescription | Unchanged (provenance string); resolution tags close the openslide gap. |
| D5 | Secondary Aperio banner | Left as-is (inert). |
| D6 | MPP == 0 (unknown) | Emit no resolution / MPP / mag tag (never a fake value). |

---

## 3. Components

### 3.1 `internal/source` — cross-format, per-axis MPP

`source.Metadata` (source.go:~112) gains `MPPX, MPPY float64`. `MPP`
(symmetric convenience) is retained.

`opentileSource.Metadata()` (opentile.go:67):
```go
md := s.t.Metadata()           // opentile.Metadata
m.MPPX = md.MicronsPerPixelX
m.MPPY = md.MicronsPerPixelY
m.MPP  = md.MicronsPerPixel     // opentile's symmetric value (0 if asymmetric)
// Fallback: SVS-specific struct when cross-format is absent.
if m.MPPX == 0 {
    if smd, ok := svsfmt.MetadataOf(s.t); ok && smd.MPP != 0 {
        m.MPPX, m.MPPY, m.MPP = smd.MPP, smd.MPP, smd.MPP
    }
}
```
`info` displays MPP per-axis when `MPPX != MPPY`, else the single value.

### 3.2 `internal/tiff` — resolution helper + constants

Add to `tags.go`:
```go
TagXResolution    uint16 = 282
TagYResolution    uint16 = 283
TagResolutionUnit uint16 = 296
ResolutionUnitCentimeter uint16 = 3
```
(Names 282/283/296 already exist in `tagnames.go`.)

New `resolution.go`:
```go
// MPPToResolution converts microns-per-pixel to a TIFF XResolution/
// YResolution RATIONAL in pixels-per-centimeter (ResolutionUnit = cm).
// Returns (0,0) for mpp <= 0. Denominator is chosen so the numerator
// fits uint32 across the realistic MPP range; clamps for extreme values.
func MPPToResolution(mpp float64) (num, denom uint32)
```
Formula: `pixels/cm = 10000 / mpp`. Implementation: `denom = 10`,
`num = round(10000/mpp * 10)`; if `num > math.MaxUint32`, fall back to
`denom = 1`, `num = round(10000/mpp)`. Unit-tested for formula
correctness, round-trip (`10000/(num/denom) ≈ mpp`), and the overflow
clamp.

### 3.3 `streamwriter` — emit resolution + WSI MPP/mag

`Options` (options.go) gains `MPPX, MPPY, Magnification float64`.

`addL0Metadata` (writer.go:269), appended after existing tags:
```go
if w.mppX > 0 {
    n, d := tiff.MPPToResolution(w.mppX)
    b.AddRational(tiff.TagXResolution, []uint32{n}, []uint32{d})
    b.AddDouble(tiff.TagWSIMPPX, []float64{w.mppX})
}
if w.mppY > 0 {
    n, d := tiff.MPPToResolution(w.mppY)
    b.AddRational(tiff.TagYResolution, []uint32{n}, []uint32{d})
    b.AddDouble(tiff.TagWSIMPPY, []float64{w.mppY})
}
if w.mppX > 0 || w.mppY > 0 {
    b.AddShort(tiff.TagResolutionUnit, []uint16{tiff.ResolutionUnitCentimeter})
}
if w.magnification > 0 {
    b.AddDouble(tiff.TagWSIMagnification, []float64{w.magnification})
}
```
(Writer stores `mppX/mppY/magnification` from Options, mirroring the
existing `imageDescription` etc. fields.) L0 only.

### 3.4 `cogwsiwriter` — add resolution

Where it already emits `TagWSIMPPX/Y/Magnification` (writer.go:476–483),
add, gated identically on `opts.Metadata.MPPX/MPPY > 0`:
```go
n, d := tiff.MPPToResolution(opts.Metadata.MPPX)
b.AddRational(tiff.TagXResolution, []uint32{n}, []uint32{d})
// ... YResolution from MPPY ...
b.AddShort(tiff.TagResolutionUnit, []uint16{tiff.ResolutionUnitCentimeter})
```

### 3.5 Callers

- **`downsample.go`** (Options at ~193): `MPPX: desc.MPP, MPPY: desc.MPP,
  Magnification: desc.AppMag` — these are the **post-`MutateForDownsample`
  scaled** values (Aperio is symmetric). → scaled resolution + WSI tags.
- **`convert_tiff.go`** (the streamwriter Options for svs/tiff/ome-tiff):
  set `MPPX: md.MPPX, MPPY: md.MPPY, Magnification: md.Magnification`.
- **`convert_cogwsi.go`** (Metadata at 60–63): `MPPX: md.MPPX, MPPY:
  md.MPPY` (was `md.MPP` for both).

---

## 4. Data flow

```
source slide ──opentile.Metadata.MicronsPerPixelX/Y──▶ source.Metadata{MPPX,MPPY}
                                                            │
   downsample: desc.MPP×factor / desc.AppMag÷factor ───────┤ (SVS, symmetric)
   convert:    md.MPPX / md.MPPY / md.Magnification ────────┤ (all formats)
                                                            ▼
                         writer Options (MPPX,MPPY,Mag)
                                  │
                MPPToResolution(MPP) ──▶ XRes/YRes (px/cm) + ResolutionUnit=cm
                MPP/Mag ─────────────▶ 65085/65086/65087 (DOUBLE)
```

---

## 5. Testing

- **`internal/tiff`** `MPPToResolution`: table test (0.25 → ~40000 px/cm;
  round-trip within tolerance; mpp≤0 → 0,0; overflow clamp for tiny mpp).
- **`streamwriter`** writer test: Options with `MPPX=0.5, MPPY=0.5,
  Magnification=20` → L0 IFD contains 282/283 (correct rational), 296=cm,
  65085=0.5, 65086=0.5, 65087=20; asymmetric `MPPX≠MPPY` → `XRes≠YRes`;
  `MPPX=MPPY=0` → none of these tags present.
- **`cogwsiwriter`** test: Metadata with MPP → output L0 has 282/283/296.
- **`internal/source`** (fixture-gated): `Metadata()` on an NDPI fixture
  returns `MPPX/MPPY > 0` (cross-format path); on SVS returns the Aperio
  MPP (fallback path). Mirror with an `info`-level integration assertion
  that `info CMU-1.ndpi` now prints an MPP line.
- **`cmd` integration** (fixture-gated): `downsample --factor 2` on an
  SVS fixture → output WSIMagnification == source AppMag / 2 and
  WSIMPPx == source MPP × 2; `convert --to cog-wsi` on NDPI → output
  carries 65085/86/87 + resolution.

---

## 6. Success criteria

- `convert`/`downsample` outputs carry XResolution/YResolution/
  ResolutionUnit (derived from MPP) + WSIMPPx/y/Magnification, for **all**
  source formats that report scale (verified NDPI + SVS).
- `downsample` values are correctly scaled (mag ÷ factor, MPP × factor).
- openslide/generic TIFF tools can read MPP from any wsitools output
  (via the resolution tags).
- MPP=0 sources emit no scale tags (no fabricated values).
- No regression: existing tile bytes, ImageDescription, and tile-order
  behavior unchanged; `make test -race` green.

---

## 7. References
- Audit session 2026-05-31; opentile-go cross-format `Metadata.
  MicronsPerPixelX/Y` (formats/*/metadata.go).
- `docs/tiff-tags.md` (WSI private tags 65085–87); `internal/tiff/
  tagnames.go` (282/283/296 names already present).
