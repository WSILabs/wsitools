// Package cogwsi writes WSI files conforming to the COG-WSI v0.1 format
// specification (docs/superpowers/specs/2026-05-20-cog-wsi-format.md).
//
// COG-WSI is a strict extension of Cloud Optimized GeoTIFF: pyramid IFDs
// and their tile-index arrays are packed at the file head; tile data is
// laid out in reverse pyramid order (smallest overview first, full-res
// last); associated images (label/macro/thumbnail/overview) are placed at
// the file tail. The writer copies compressed tile bytes verbatim from
// source — no decode, no re-encode.
package cogwsi
