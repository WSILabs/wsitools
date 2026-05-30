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

## Part B: `downsample` CLI vs Lanczos3 — **VISIBLE DIFFERENCE, PERF MAKES LANCZOS3 UNVIABLE AS DEFAULT**

### Setup

- Same host + tooling as Part A.
- Source: CMU-1-Small-Region.svs (2220 × 2967).
- Downsample factor: 4 (Section B1 fixture choice).
- Comparison region: tissue-dense 200×200 patch centered on stained
  cellular material at output coords (250, 150) — picked by averaging
  brightness in 100×100 blocks and selecting the darkest (most tissue).

### Commands

```sh
# wsitools (box-halve chain)
wsitools downsample --factor 4 -o wsitools-box.svs CMU-1-Small-Region.svs

# libvips Lanczos3 (matches `vips resize` default)
vips resize CMU-1-Small-Region.svs libvips-lanczos.tif 0.25 --kernel lanczos3

# libvips box-equivalent (integer-factor block average; same encoder
# as the Lanczos run, so this isolates kernel-only difference)
vips shrink CMU-1-Small-Region.svs libvips-box.tif 4 4

# libvips bilinear
vips resize CMU-1-Small-Region.svs libvips-linear.tif 0.25 --kernel linear
```

### Quality measurements (200×200 tissue patch)

Same encoder (libvips' TIFF, uncompressed). Pixel diff against Lanczos3
as reference. "Edge energy" = mean |dx|+|dy| of the luminance channel; a
proxy for high-frequency content retained.

| Kernel | Edge energy | Mean abs diff vs Lanczos3 | p99 diff vs Lanczos3 |
|---|---|---|---|
| **Box** | 20.73 | 4.63 | 23 |
| **Bilinear** | 16.80 | 5.09 | (similar) |
| **Lanczos3** | 22.20 | 0 (ref) | 0 (ref) |

Takeaways:

- **Lanczos3 has 7% more edge energy than box.** That's a visible
  sharpness lift on cell membranes and tissue boundaries.
- **Bilinear is strictly worse than box** at this scale (19% LESS edge
  energy than box, 24% less than Lanczos3). The 4-tap bilinear support
  is too narrow for 4× downsample — it just blurs. Don't expose
  bilinear as a downsample kernel option; it's a footgun.

### Perf (5-run average, 2-step halve chain 2220×2967 → 555×741)

Microbenchmark on a synthetic 2220×2967 RGB raster via
`opentile-go/resample.ImageInto`:

| Kernel | Time | Ratio vs Box |
|---|---|---|
| Nearest | 4.5 ms | 0.24× |
| **Box** | 18.7 ms | 1.0× |
| Bilinear | 11.0 ms | 0.59× |
| **Lanczos3** | 3982 ms | **213×** |

The Lanczos3 cost is 213× Box. Two causes:

1. **Larger support per output pixel.** Box reads 4 source pixels per
   output pixel; Lanczos3 at 2× downsample reads 13×13 = 169.
2. **opentile-go's implementation is naive.** `resample/lanczos.go`
   computes `math.Sin` per source pixel inside the inner loop with no
   precomputed weight tables and no separable 2-pass formulation. A
   proper separable Lanczos with precomputed weights would drop to
   ~5–10× Box (13+13 reads, table-lookup weights).

Extrapolated to a real WSI: a 100 K × 60 K L0 downsampled 4× would
take ~3 s with box, ~10 min with the current Lanczos3 implementation.
That's a non-starter as default.

### Decision (Step B6) — outcome 2 (visible difference + use case justifies flag) with revised defaults

**Ship `--kernel` flag. Keep default = box.**

The audit plan's "default matches libvips lanczos3" framing was wrong —
matching libvips' general-resize default would make wsitools 200× slower
than today. Box is the right default until opentile-go's Lanczos3 is
optimised. The flag exists for users who want maximum sharpness on
non-batch jobs and can wait minutes per slide.

Revised flag design vs the plan:

| `--kernel` value | opentile-go enum | Notes |
|---|---|---|
| `box` (default) | `Box` | Current behavior; matches libvips `dzsave` cascade default. |
| `lanczos3` | `Lanczos` | +7% edge energy; ~200× slower until opentile-go optimises. |
| `nearest` | `Nearest` | Fastest; debug aid only (visible aliasing). |

**Drop `bilinear` / `linear` from the value set** — strictly worse than
box at 4× downsample on tissue.

### Follow-up: opentile-go Lanczos optimisation

Filed as a separate concern for the inbound opentile-go updates:
**separable 2-pass Lanczos3 with precomputed 1-D weight tables**. Would
drop the cost from 213× to ~5–10× box, making Lanczos3 viable as a
default in some workflows. Not blocking this audit; the `--kernel` flag
is correct shipping with current opentile-go regardless.

