# wsitools

Go-based utilities for whole-slide imaging (WSI) files. v0.1 ships a downsample CLI;
v0.2+ adds transcode + more source formats.

## Module path

`github.com/wsilabs/wsitools`

## Conventions

- Reader = `github.com/cornish/opentile-go` (consumed as a Go module dep, not forked).
- TIFF core = `internal/tiff` (byte-emission primitives: types, tag IDs, EntryBuilder, WriteHeader, JPEGTables, BigTIFF auto-promote, PatchUint32/64). Pure Go.
- Writers built on the core:
  - `internal/tiff/streamwriter` — streaming TIFF writer; backs `transcode` + `downsample`.
  - `internal/tiff/cogwsiwriter` — spool-and-finalize COG-WSI writer; backs `convert`.
  Both are pure Go; cgo only inside codec wrappers.
- Codecs = `internal/codec/<codec>/` subpackages, one per codec, registered via `init()`.
- WSI private TIFF tag namespace: 65080–65087 (see `internal/tiff/tags.go`).
- Decoders = `internal/decoder/` (smaller surface — only what source slides need).
- Pipeline = `internal/pipeline` (worker-pool decode/process/encode).
- CLI = `cmd/wsitools/` using cobra.

## Test discipline

- `make test` runs with `-race -count=1`.
- Integration tests gated by `WSI_TOOLS_TESTDIR` env var (default `./sample_files`).
- `sample_files/` is gitignored; soft-link to opentile-go's pool:

  ```sh
  ln -s "$HOME/GitHub/opentile-go/sample_files" sample_files
  ```

## No guessing

When unsure about TIFF byte layout, Aperio ImageDescription, or any WSI quirk: read
the opentile-go reader source first; it's canonical. The spec rule from opentile-go's
CLAUDE.md applies here too — don't reason from first principles about WSI formats,
read the reference implementation.

## Spec + plans

Design docs live at `docs/superpowers/specs/`; implementation plans at
`docs/superpowers/plans/`.
