# wsitools

Go-based utilities for whole-slide imaging (WSI) files used in digital
pathology. CLI bundles read-side inspection (`info`, `dump-ifds`, `extract`,
`hash`, `region`), write-side conversion (`convert --to {cog-wsi, dzi,
szi, svs, tiff, ome-tiff}`, `downsample`), and diagnostics (`doctor`,
`version`).

## Module path

`github.com/wsilabs/wsitools`

## Conventions

- Reader = `github.com/wsilabs/opentile-go` (consumed as a Go module dep, not forked).
- TIFF core = `internal/tiff` (byte-emission primitives: types, tag IDs,
  EntryBuilder, WriteHeader, JPEGTables, BigTIFF auto-promote,
  PatchUint32/64; plus `tagnames.go` with TagName/TypeName/TypeSize/
  InterpretEnum dictionaries used by `dump-ifds --raw`). Pure Go.
- Writers built on the core:
  - `internal/tiff/streamwriter` — streaming TIFF writer; backs
    `downsample` and `convert --to svs|tiff|ome-tiff`.
  - `internal/tiff/cogwsiwriter` — spool-and-finalize COG-WSI writer;
    backs `convert --to cog-wsi`.
  Both are pure Go; cgo only inside codec wrappers.
- Tile-ordering strategies (row-major / hilbert / morton) =
  `internal/tiff/tileorder`, used by the COG-WSI writer's finalize pass.
- DZI/SZI writers = `internal/dzi`, `internal/szi`. `convert --to dzi|szi`
  uses a single-pass pyramid-descent generator (see `cmd/wsitools/convert_dzi_descent.go`)
  with parallel libjpeg-turbo encoders; ~150× faster than the v0.16 naive path.
- Codecs = `internal/codec/<codec>/` subpackages, one per codec, registered
  via `init()`. Vanilla YCbCr JPEG is the default. (wsitools does not
  reproduce Aperio's APP14+raw-RGB JPEG framing on re-encode; the old
  `internal/codec/aperioapp14` keep-around encoder was removed unused.)
- Source layer = `internal/source` (slide open + IFD walk). `WalkIFDs` is
  the slim format-aware walk; `WalkIFDsRaw` captures every directory entry
  for `dump-ifds --raw`.
- WSI private TIFF tag namespace: 65080–65087 (see `internal/tiff/tags.go`).
- Pipeline = `internal/pipeline` (worker-pool decode/process/encode), used
  by `convert --to svs|tiff|ome-tiff` and `downsample`.
- Memory cap = `internal/memlimit`: sets `GOMEMLIMIT` to 75% of physical
  RAM by default (so conversions degrade under GC instead of OOM-ing the
  host); wired to the global `--max-memory` flag. CLI output helpers =
  `internal/cliout`.
- CLI = `cmd/wsitools/` using cobra.

## Test discipline

- `make test` runs with `-race -count=1`.
- Integration tests gated by `WSI_TOOLS_TESTDIR` env var (default
  `./sample_files`).
- CI downloads CMU-1-Small-Region.svs + CMU-1.ndpi from
  `wsilabs/wsi-fixtures` v1 and runs the integration suite on every push
  and PR (see `.github/workflows/ci.yml`).
- For local work, soft-link to opentile-go's fixture pool:

  ```sh
  ln -s "$HOME/GitHub/opentile-go/sample_files" sample_files
  ```

- **Verifying an opentile-go version bump:** running wsitools' suite only
  confirms *non-regression* of existing paths — it does NOT exercise new
  opentile-go behavior wsitools doesn't yet call. To verify the *update-specific*
  changes, run opentile-go's own suite in its repo, and set its fixture gate or
  the integration tests silently skip:
  `OPENTILE_TESTDIR=$(pwd)/sample_files go test ./decoder/... ./formats/...`
  (run `-v` and confirm the relevant tests PASS, not SKIP).
- Heavy `-race` suites (esp. `cmd/wsitools`) can exceed Go's default 600s test
  timeout under concurrent load → a false-FAIL that looks like a crash. Run heavy
  suites uncontended and/or with `-timeout 30m`.

## No guessing

When unsure about TIFF byte layout, Aperio ImageDescription, or any WSI
quirk: read the opentile-go reader source first; it's canonical. The spec
rule from opentile-go's CLAUDE.md applies here too — don't reason from
first principles about WSI formats, read the reference implementation.

## Spec + plans

Design docs live at `docs/superpowers/specs/`; implementation plans at
`docs/superpowers/plans/`.
