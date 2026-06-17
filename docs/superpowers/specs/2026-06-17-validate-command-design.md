# `wsitools validate` — Design

**Status:** approved (2026-06-17)
**Scope:** v1 — standalone read-side `validate` command. Write-side self-check
deferred to a follow-on discussion.

## Goal

Expose opentile-go v0.45.1's structural `Validate` API as a `wsitools validate
<file>` command: report a slide's structural conformance (per opentile-go's
reader) as a human or JSON report, with a CI-gateable exit code.

## Background — the opentile-go API consumed

opentile-go v0.45.1 ships a read-only structural validator
(`github.com/wsilabs/opentile-go`, `validate.go`):

- `ValidateFile(path string, opts ...ValidateOption) (*Report, error)` — stats
  `path` first; a genuinely missing/unreadable path is the **only** thing
  returned as a Go `error`. A file that exists but fails to open/parse becomes a
  `CheckUnopenable` **Error finding** inside a `*Report` (not an error return).
  `OpenFile` handles both the single-file and DICOM series-directory cases.
- `Validate(r io.ReaderAt, size int64, ...) (*Report, error)` — streamed variant
  (not used by v1).
- `(*Slide).Validate(...) *Report` — validate an already-open slide (not used by
  v1).
- `Report{ Format, Findings []Finding }` with:
  - `OK() bool` — true iff there are **no Error-severity findings** (Warning/Info
    do not affect `OK`).
  - `Worst() Severity` — highest severity present, or `Info` for an empty report.
- `Finding{ Severity, Code CheckCode, Message string, Pyramid, Level int, Count int }`.
  `Pyramid`/`Level` are `-1` when the locus is the whole file / not applicable.
  `Count` rolls up repeated occurrences of the same `(Code, locus)`. Findings are
  returned pre-sorted: Error first, then by code, then by level.
- `Severity` ∈ `Info` < `Warning` < `Error`, with `String()` → `"info" |
  "warning" | "error"`.
- `CheckCode` is an open-ended string catalog: `unopenable`,
  `offsets-out-of-bounds`, `tile-grid-mismatch`, `inconsistent-pyramid`,
  `missing-metadata`, plus reserved-but-unemitted `non-conformant-format` /
  `orphan-ifd`.
- `ValidateOption` is an additive seam for future Tier-2 (decode-based) checks;
  **v1 of opentile ships zero options.**

The checks opentile runs today: Tier 0 = open; Tier 1 = format-agnostic level
geometry (degenerate sizes, `grid != ceil(size/tile)`, non-monotone pyramid,
missing MPP) plus a per-reader `Validator` hook.

## Command surface

```
wsitools validate <file> [--json] [--strict]
```

- **Exactly one path** (`cobra.ExactArgs(1)`). A DICOM series directory counts as
  one path — `ValidateFile`/`OpenFile` resolve it. Matches `info`/`hash`/`doctor`.
- `--json` — machine-readable output, registered via the existing
  `cliout.RegisterJSONFlag(cmd)` (same mechanism as `info --json`).
- `--strict` — promote Warning findings to failures for exit-code purposes
  (default: only Error findings fail).

## Data flow

The command calls **`opentile.ValidateFile(path)` directly** — it does **not**
go through `internal/source.Open`. This is deliberate: `internal/source.Open`
would return a hard error on a structurally-broken file, whereas the whole point
of `validate` is to report on such files. `ValidateFile` converts an
open/parse failure into a `CheckUnopenable` Error finding and only returns a Go
`error` for an operational failure (missing/unreadable path).

Consequently there is **no new code in `internal/source`**. `validate` is a thin
`cmd/wsitools/validate.go` wrapper: one `ValidateFile` call + report formatting +
exit-code mapping.

## Output

### Human (default)

A header line, then one line per finding (in opentile's sort order — Error
first), then nothing else:

```
CMU-1.svs · svs · INVALID (2 findings)
  [error]   tile-grid-mismatch     P0/L3 ×200  level 3 grid 4x4 != ceil(size/tile) 5x4
  [warning] missing-metadata       P0/L0       level 0 has no MPP (microns-per-pixel) metadata
```

A clean report:

```
CMU-1-Small-Region.svs · svs · valid
```

Locus rendering:
- both `Pyramid` and `Level` ≥ 0 → `P{p}/L{l}`
- `Pyramid` ≥ 0, `Level` == -1 → `P{p}`
- both -1 → omitted (whole-file finding)
- `Count` > 1 → append `×{count}`

The header verb reflects the **failure-threshold decision** (so it never
contradicts the exit code):
- zero findings → `valid`
- crossed the failure threshold (Error present, or Warning present under
  `--strict`) → `INVALID (N findings)`
- has findings but passed the gate (e.g. Warnings without `--strict`) → `OK (N
  findings)`

### JSON (`--json`)

Marshal a result struct mirroring the `info --json` style (via `cliout.JSON`):

```json
{
  "path": "CMU-1.svs",
  "format": "svs",
  "ok": false,
  "worst": "error",
  "findings": [
    {
      "severity": "error",
      "code": "tile-grid-mismatch",
      "message": "level 3 grid 4x4 != ceil(size/tile) 5x4",
      "pyramid": 0,
      "level": 3,
      "count": 200
    }
  ]
}
```

- `ok` = `Report.OK()`. `worst` = `Report.Worst().String()`.
- `pyramid`/`level` are emitted as `null` (via `*int`) when the source value is
  `-1`, so "whole-file" findings read cleanly.
- `findings` is always an array (never null); `[]` for a clean file.

## Exit codes

Three-way, derived directly from the API contract:

| Code | Meaning | Source |
|------|---------|--------|
| **0** | valid — no Error findings (and, under `--strict`, no Warning findings) | `Report.OK()`, plus `Worst() < Warning` when `--strict` |
| **2** | invalid — findings crossed the failure threshold | failing `Report` |
| **1** | operational error — could not attempt validation (path missing/unreadable) | `ValidateFile`'s non-nil `error` return |

Rationale for splitting **2** (file is bad) from **1** (couldn't run): a CI
corpus sweep must be able to distinguish a genuinely non-conformant slide from a
typo'd path or a permissions problem. The report is still printed to stdout in
the exit-2 case — the findings **are** the output; no Go-style `error:` line is
printed for exit 2.

Failure threshold:
- default: fail iff `!report.OK()` (any Error finding).
- `--strict`: fail iff `report.Worst() >= Warning` (any Error or Warning).
- Info findings never cause failure.

### main.go integration

`validate`'s `RunE` prints the report, then returns a sentinel
`errValidationFailed` when the report crosses the failure threshold. `main.go`
recognizes this sentinel next to the existing `context.Canceled` branch:

```go
if err := rootCmd.ExecuteContext(ctx); err != nil {
    if errors.Is(err, context.Canceled) {
        fmt.Fprintln(os.Stderr, "interrupted")
        os.Exit(130)
    }
    if errors.Is(err, errValidationFailed) {
        os.Exit(2) // report already printed by the command
    }
    fmt.Fprintln(os.Stderr, "error:", err)
    os.Exit(1)
}
```

The command sets `cmd.SilenceUsage = true` (matches other commands) so the
sentinel does not trigger a usage dump. The operational-error path (missing path)
returns the `ValidateFile` error normally → the existing branch prints `error:
...` and exits 1.

## Components / files

- **Create `cmd/wsitools/validate.go`:** the `validateCmd` cobra command, its
  `RunE`, the `validateResult`/`validateFinding` JSON structs, the human-line
  formatter, and the `errValidationFailed` sentinel + threshold helper.
- **Modify `cmd/wsitools/main.go`:** add the `errors.Is(err,
  errValidationFailed)` → `os.Exit(2)` branch.
- **Create `cmd/wsitools/validate_test.go`:** unit + integration tests.

No `internal/` changes.

## Testing

**Unit (no fixtures):**
- Table-driven `formatFinding` test: each locus combination (`P/L`, `P` only,
  whole-file), `Count` 1 vs >1, each severity → expected human line.
- `validateResult` JSON mapping: a crafted `Report` (mixed severities, a `-1`
  locus) → expected JSON (`pyramid`/`level` null when -1; `ok`/`worst` correct;
  `findings: []` for a clean report).
- Threshold/exit logic: `(report, strict) → shouldFail` truth table — clean
  report, Info-only, Warning-only (strict vs not), Error present.

**Integration (gated by `WSI_TOOLS_TESTDIR`, using the `stripedBinary`/`runBin`
harness):**
- Known-good SVS (e.g. `CMU-1-Small-Region.svs`) → exit 0, output contains
  `valid`.
- Missing path → exit 1, stderr contains `error:`.
- A truncated/garbage file (write a few junk bytes to a temp file) → exit 2,
  output contains `unopenable`.
- `--json` on the good SVS → parses as JSON with `"ok": true`.

(`--strict` flips a Warning-only file from 0→2: covered by the unit threshold
table; an integration fixture for it is only added if a Warning-only slide is
readily available, otherwise the unit table is the authority.)

## Out of scope for v1

- **Write-side self-check** (convert/downsample/crop validating their own
  output before reporting success) — to be discussed separately; `validate` is
  the reusable primitive it would call.
- Batch / multi-path validation.
- Tier-2 decode-based checks (opentile ships zero `ValidateOption`s today).
- Wiring `validate` into CI as a corpus gate (a later D-series item).

## Documentation

- README: add `validate` to the command bundle list (read-side inspection
  group) with a one-line description and the exit-code table.
- CLAUDE.md: mention `validate` in the CLI command summary line.
- CHANGELOG `[Unreleased]`: add the `validate` command + note it consumes
  opentile-go's `Validate` API.
