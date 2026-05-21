// Package tiff provides TIFF byte-emission primitives shared by the
// streamwriter and cogwsiwriter packages. It contains no I/O
// orchestration — only header serialization, IFD entry encoding,
// type/tag constants, JPEGTables construction, BigTIFF auto-promote
// math, and in-place patch helpers.
//
// Design spec: docs/superpowers/specs/2026-05-21-tiff-core-extraction-design.md.
//
// All functions assume little-endian byte order. WSI files in practice
// are universally LE, and the v0.6.0 cogwsi/ifd.go (the ancestor of
// this package's EntryBuilder) removed its byte-order parameter for
// the same reason.
package tiff
