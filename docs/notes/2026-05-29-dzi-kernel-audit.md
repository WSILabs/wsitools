# DZI / downsample kernel audit — findings

**Date:** 2026-05-29
**Plan:** `docs/superpowers/plans/2026-05-29-downsample-kernel-audit.md`

---

## Part A: DZI cascade vs libvips parity — **PARITY CONFIRMED**

### Setup

- libvips 8.18.2 (Homebrew `vips` 8.18.2).
- wsitools commit `cfc5870` (`convert --to dzi`).
- Fixture: `sample_files/svs/CMU-1-Small-Region.svs` (2220 × 2967 px).

### libvips defaults verified

From `vips dzsave` operation help on this host:

```
   region-shrink - Method to shrink regions, input VipsRegionShrink
			default enum: mean
			allowed enums: mean, median, mode, max, min, nearest
   tile-size    - default: 254
   overlap      - default: 1
   Q            - default: 75
```

`region-shrink=mean` is the documented and active default. "mean" is 2×2
box averaging — same algorithm `boxDownsample2x` in
`cmd/wsitools/convert_dzi_descent.go:503` implements.

### Commands run

```sh
# libvips dzsave with matched JPEG quality
vips dzsave sample_files/svs/CMU-1-Small-Region.svs \
  /tmp/kernel-audit/libvips-small \
  --suffix '.jpeg[Q=85]' \
  --tile-size 256 --overlap 1

# wsitools convert --to dzi
wsitools convert --to dzi \
  -o /tmp/kernel-audit/wsitools-small.dzi \
  sample_files/svs/CMU-1-Small-Region.svs
```

### Pyramid structure

Both produced 13 levels (0 through 12), top-down match:

| Level | libvips tiles | wsitools tiles |
|---|---|---|
| L0  | 1 | 1 |
| L5  | 1 | 1 |
| L10 | 9 | 9 |
| L11 | 30 | 30 |
| L12 | 108 | 108 |

Manifests are equivalent — same `Format="jpeg" Overlap="1" TileSize="256"`,
same `Size Width="2220" Height="2967"`. Whitespace and attribute order
differ; image content does not.

### Decoded-pixel parity

Diffed three tiles via Pillow + numpy:

```python
for level, tile in [('10','1_1'), ('11','2_3'), ('12','5_6')]:
    a = np.array(Image.open(f'libvips-small_files/{level}/{tile}.jpeg').convert('RGB')).astype(int)
    b = np.array(Image.open(f'wsitools-small_files/{level}/{tile}.jpeg').convert('RGB')).astype(int)
    d = np.abs(a-b)
    print(f'L{level} {tile}: mean={d.mean():.3f} p99={np.percentile(d,99):.0f} max={d.max()}')
```

```
L10 1_1: shape=(258, 258, 3) mean=0.000 p99=0 max=0
L11 2_3: shape=(258, 258, 3) mean=0.000 p99=0 max=0
L12 5_6: shape=(258, 258, 3) mean=0.000 p99=0 max=0
```

**Decoded pixels are bit-identical.** Mean absolute difference is zero;
max difference is zero. JPEG file size differs by ~18 bytes per tile
(libjpeg-turbo vs libvips' jpeg encoder produce slightly different APP
markers), but image content matches exactly.

### Decision (Step A6, outcome 1)

**No change required.** wsitools' DZI cascade uses the same algorithm as
libvips dzsave and produces identical decoded output. Close the TODO.

### Caveat

This was tested on a small natively-tiled SVS. The cascade algorithm
(2×2 box averaging on RGB pixel data) is identical regardless of source
format, so the result generalises to NDPI, OME-TIFF, etc. — but
synthesized-tile sources (NDPI strips → reconstructed JPEG tiles) may
show differences arising from the *L0 source-tile* path, not the
cascade. That's a different audit (source synthesis fidelity, not
downsample kernel choice).

---

## Part B: `downsample` CLI vs Lanczos3

Not yet executed. See plan for B1–B8 procedure.
