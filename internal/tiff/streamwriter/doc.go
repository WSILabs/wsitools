// Package streamwriter writes WSI TIFF files using a streaming
// orchestration model: tile bytes submitted via WriteTile are held in a
// per-level reorder buffer and written to the output file in strategy
// order as the Sink drains; all IFDs are serialized in a single pass at
// Close, with the header's first-IFD pointer patched in place at the end.
//
// Backs the wsitools convert (--to svs|tiff|ome-tiff) and downsample
// commands.
//
// Design spec: docs/superpowers/specs/2026-05-21-tiff-core-extraction-design.md.
package streamwriter
