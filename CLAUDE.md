# wsitools

Go-based utilities for whole-slide imaging (WSI) files used in digital
pathology. CLI bundles read-side inspection (`info`, `dump-ifds`, `extract`,
`hash`, `region`, `validate`), write-side conversion (`convert --to {cog-wsi, dzi,
szi, svs, tiff, ome-tiff, dicom, bif}`, `downsample`, `crop`), associated-image
editing (`label|macro|thumbnail|overview remove|replace`), and diagnostics
(`doctor`, `version`).

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
  - `internal/tiff/bifwriter` — Ventana/Roche **BIF** (DP 200) writer; backs
    `convert --to bif` (driver in `cmd/wsitools/convert_bif.go`). Verbatim
    JPEG tile-copy, full pyramid + generated overview + synthesized `<iScan>`/
    `<EncodeInfo>` XMP. **Tiles stored ROW-MAJOR** (real DP 200, per the file's
    own `<Frame>` nodes — NOT serpentine, despite the whitepaper prose);
    `TileJointInfo` stitch IDs use the serpentine physical numbering. Validated
    against bio-formats/QuPath and a pixel-identical round-trip through opentile
    (the BIF read bug we found is fixed in opentile-go v0.45.3 / #57/#58/#59).
  All are pure Go; cgo only inside codec wrappers.
- DICOM-WSM writer = `internal/dicomwriter` (`WritePyramid`/`WriteVolumeInstance`,
  built on `suyashkumar/dicom`); backs `convert --to dicom`. It reads compressed
  frames verbatim from a `source.Source` and emits TILED_FULL WSM instances.
- DICOM as a transform TARGET (`convert --to dicom --factor`, `downsample
  <dicom>`, `crop <dicom>` ± `--lossless`) = `internal/derivedsource`: a
  synthesized `source.Source` over a reduced/cropped pyramid (rasterLevel
  re-encodes tiles via a worker pool; passthroughLevel copies verbatim L0 frames
  for lossless crop) fed to `WritePyramid`. Re-encoded levels are JPEG-baseline
  (the derivedsource path does not yet wire up the JPEG 2000 / HTJ2K encoders —
  those exist in `internal/codec` and are reachable from the TIFF-family
  `convert --codec`). `crop`/`downsample`/`convert --factor` for the TIFF
  family live in `cmd/wsitools` (crop.go/crop_formats.go, downsample.go,
  convert_factor.go).
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
- `make test`/`make cover` run a `check-fixtures` preflight: when
  `WSI_TOOLS_TESTDIR` is **set**, it fails loud unless the dir exists and holds
  the sentinel `svs/CMU-1-Small-Region.svs` — so a stale/wrong path (e.g. left
  over from a repo move) can't make every fixture-gated test silently skip and
  masquerade as a green run. **Unset** is a no-op (unit-only / fresh-checkout
  case; the Go helpers then fall back to the `./sample_files` symlink). If you
  hit the error, fix the path or `unset WSI_TOOLS_TESTDIR`.
- CI downloads fixtures from `wsilabs/wsi-fixtures` **v7** (`svs.tar` — incl. the
  `590_crop` ImageScope export crops, `ndpi.tar`, `cog-wsi.tar`, `dicom.tar` —
  incl. the 3DHISTECH JP2K/HTJ2K + scan_621 Grundium DICOM-WSM fixtures,
  `ome-tiff.tar`), verifies them against `.github/fixtures.sha256`, and runs both
  the unit suite and the `-tags integration` suite on every push and PR (see
  `.github/workflows/ci.yml`).
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
