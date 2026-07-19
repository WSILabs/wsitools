# Memory & performance

## Footprint

Conversion memory scales with slide **width**, not a fixed ceiling.
`convert --to dzi|szi` streams top-to-bottom but holds full-width strip buffers
across every pyramid level, plus the reader's per-tile decode caches, so peak
resident memory grows with the widest level. Typical peaks (defaults, 16 GB
host):

| Slide | L0 dimensions | Peak RSS |
|---|---|---|
| CMU-1.ndpi | 51200 × 38144 | ~4 GB |
| OS-2.ndpi  | 126976 × 73728 | ~7 GB |

Throughput is competitive with — and often faster than — libvips `dzsave` on
these slides. Most of the memory headroom above the live working set is Go's
garbage collector holding freed memory under a generous soft limit (see below):
lowering `--max-memory` trades a little speed for a substantially smaller peak.

Natively-tiled sources (SVS, TIFF, COG-WSI) have a much smaller footprint than
NDPI, which stores each level as one large JPEG and needs a wider decode window.

## The soft memory limit

To keep a runaway conversion from exhausting the machine, wsitools sets a **soft
memory limit at 75% of physical RAM by default** (via Go's `GOMEMLIMIT`). Under
pressure the garbage collector works harder — trading some speed — instead of
letting the process OOM the host.

```sh
# Cap the soft limit at 4 GiB (slower, lower peak)
wsitools --max-memory 4GiB convert --to dzi -o out.dzi in.ndpi

# Disable the cap entirely
wsitools --max-memory off convert --to dzi -o out.dzi in.ndpi

# The GOMEMLIMIT env var is respected and takes precedence over the default
GOMEMLIMIT=8GiB wsitools convert --to dzi -o out.dzi in.ndpi
```

Precedence: `--max-memory` > `GOMEMLIMIT` > 75% default. `wsitools doctor`
reports the active limit and its source.

The reader's own decode-cache budget is separately tunable via the
`OPENTILE_READ_MEMORY_BUDGET` environment variable (bytes; default 1 GiB). On a
memory-constrained host, combining a low `--max-memory` with a small read budget
gives the smallest peak, at a modest cost in speed.
