// Package streamwriter writes WSI TIFF files using a streaming
// orchestration model: tile bytes are written inline to the output
// file as WriteTile is called, IFDs are emitted with placeholder
// offsets, then patched in place once each level is complete.
//
// Backs the wsitools transcode + downsample commands.
//
// Design spec: docs/superpowers/specs/2026-05-21-tiff-core-extraction-design.md.
package streamwriter
