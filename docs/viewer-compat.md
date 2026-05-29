# wsitools — viewer compatibility checklist

Manual checklist of (output codec/container, viewer) pairs that have been
verified to load. Not in CI; run by hand and update this file when you
confirm a pair works.

Status legend: `✓` confirmed working, `✗` confirmed broken (add note),
`—` not yet tested, `n/a` not applicable.

## Container compatibility (`convert --to`)

Tile-copy fast path (no codec specified; source codec preserved):

| Target  | QuPath | openslide | OpenSeadragon | Notes |
|---------|--------|-----------|---------------|-------|
| svs     | —      | —         | n/a           | |
| tiff    | —      | —         | n/a           | |
| ome-tiff| —      | —         | n/a           | |
| cog-wsi | —      | —         | n/a           | Needs opentile-go reader; openslide may not load. |
| dzi     | n/a    | n/a       | —             | DZI is a folder/manifest, not a single-file slide. |
| szi     | n/a    | n/a       | —             | DZI inside store-method ZIP. |

## Re-encode codec compatibility (`convert --to {svs,tiff,ome-tiff} --codec X`)

Non-jpeg codecs write TIFF compression values (50001/50002/60001/60003)
that openslide does not decode. Viewers either need an opentile-go-backed
read path or their own libjxl / libavif / libwebp / OpenJPH integration.

| Codec   | QuPath | openslide | Custom Viewer | OpenSeadragon (via DZI) |
|---------|--------|-----------|---------------|--------------------------|
| jpeg    | —      | —         | —             | —                        |
| jpegxl  | —      | —         | —             | —                        |
| avif    | —      | —         | —             | —                        |
| webp    | —      | —         | —             | —                        |
| htj2k   | —      | —         | —             | —                        |

## DZI / SZI output (`convert --to dzi|szi`)

DZI is JPEG-encoded by default, so all OpenSeadragon-compatible viewers
should load it. SZI is the same content inside a store-method ZIP with
optional `scan-properties.xml`.

| Output  | OpenSeadragon | DeepZoomView | Pathomation | Notes |
|---------|---------------|--------------|-------------|-------|
| dzi     | —             | —            | n/a         | |
| szi     | n/a           | n/a          | —           | Pathomation viewers consume SZI directly. |

## Deferred test work

- Visual-fidelity round-trip tests via mini decoders (read raw tile bytes
  from opentile-go, decode via the matching codec library, pixel-compare
  against the source) — would let CI catch silent codec regressions
  without depending on third-party viewers.
- Cross-version pixel parity check between v(N) and v(N-1) output for the
  same input — would catch silent regressions in the decode-resample-
  encode chain across release bumps.
- jpegli (Homebrew jpeg-xl bottle ships libjxl without libjpegli; defer
  until upstream re-enables or we stand up a build-from-source path).
- HEIF, JPEG-LS, JPEG-XR, Basis Universal — queued for follow-on releases.
