# wsi-tools v0.5 — rename `wsi-tools` → `wsitools` (cosmetic milestone)

**Status:** Sealed for implementation
**Date:** 2026-05-10
**Predecessors:** [v0.4 batch-1 utilities](2026-05-08-wsi-tools-batch1-utilities-design.md)

## Goal

Drop the hyphen from the project's identity wherever it's exposed: module
path, repo URL, binary name, CLI `Use` string, version-output label, and
prose in README / `docs/roadmap.md` / `CLAUDE.md`. Project, repo, module,
and binary all align on the single name `wsitools`.

This milestone is **purely cosmetic**. No behavioural changes to output
files; no functional changes to any subcommand. The TIFF-level provenance
emission still says `wsi-tools/<version>` in v0.5.0; that switch is queued
as a follow-up (v0.5.1) coordinated with an opentile-go v0.14.1 patch the
user will ship separately.

## Why split the cosmetic rename from the emission swap

The transcode pipeline writes `wsi-tools/<version>` strings into TIFF
ImageDescription. opentile-go v0.14's parser specifically matches the
literal prefix `wsi-tools/`. If wsi-tools v0.5.0 changes that prefix to
`wsitools/` before opentile-go v0.14.1 ships a parser that accepts both,
the integration test that asserts metadata round-trip via opentile-go
fails.

Splitting the work cleanly avoids the coupling:

- **v0.5.0** (this spec): pure rename. Output files are bit-identical to
  v0.4. Integration tests remain green against opentile-go v0.14.0. Ships
  as soon as the rename pass is verified.
- **v0.5.1** (follow-up, separate spec): one-line emission swap from
  `wsi-tools/<version>` to `wsitools/<version>`, conditional on opentile-go
  v0.14.1 having landed and been picked up via `go get`. Out of scope here.

## Non-goals (v0.5.0)

- **Output-format changes.** Files written by `wsitools transcode v0.5.0`
  are bit-identical to files written by `wsi-tools transcode v0.4.0`
  (modulo the unchanged `wsi-tools/<version>` provenance string).
- **Historical doc rewrites.** Specs and plans under
  `docs/superpowers/specs/` and `docs/superpowers/plans/` (v0.1–v0.4)
  retain their original "wsi-tools" prose. CHANGELOG entries v0.1.0–v0.4.1
  retain their original prose. Only the new `[0.5.0]` section is added.
- **Tag name changes.** `WSIToolsVersion` (TIFF tag 65084) keeps its
  human-readable label. The wire-level identity is the tag NUMBER (65084)
  + ASCII content; the tag-name string is purely a debug-display label
  used by tiffinfo and similar.
- **Backwards-compat install shim.** No `wsi-tools` deprecation binary
  that prints a "renamed to wsitools" notice. The install path break is
  one-time and documented in the v0.5.0 release notes.

## Scope

### Mechanical rename — wsi-tools repo

| Where | Change |
|---|---|
| `go.mod` | `module github.com/cornish/wsi-tools` → `module github.com/wsilabs/wsitools` |
| All Go imports | `"github.com/cornish/wsi-tools/..."` → `"github.com/wsilabs/wsitools/..."` (one sed pass; ~161 grep hits across `*.go`, `*.md`, `Makefile`, `*.yml`, `go.mod`) |
| `cmd/wsi-tools/` | rename to `cmd/wsitools/` (binary name follows directory name) |
| `cmd/wsitools/main.go` | `cobra.Command{Use: "wsi-tools", ...}` → `Use: "wsitools"` |
| `cmd/wsitools/version.go` | `fmt.Printf("wsi-tools %s\n", Version)` → `fmt.Printf("wsitools %s\n", Version)` (the `Version` constant itself doesn't change) |
| `Makefile` | `bin/wsi-tools` → `bin/wsitools` |
| `.github/workflows/ci.yml` | any `bin/wsi-tools` references |
| `README.md` | title (`# wsi-tools` → `# wsitools`), CI badge URL, install command, every Usage example, every prose mention |
| `docs/roadmap.md` | every `wsi-tools` mention in current prose |
| `CLAUDE.md` | project name + module path |
| `[0.5.0]` CHANGELOG entry | new — calls out the breaking-rename + install path change |

### What does NOT change

- `WSIToolsVersion` TIFF tag name (display label; preserves consistency
  with v0.2–v0.4 outputs in tiffinfo dumps).
- `wsiwriter.WithToolsVersion` option name (internal API, no external
  callers).
- `source.IFDRecord.WSIToolsVersion` field name (internal struct field).
- `source.WSIImageType` private TIFF tag name and field, and other
  `WSI*` Go-side identifiers (internal; no outward effect).
- `cmd/wsitools/transcode.go::buildProvenanceDesc`: KEEPS emitting
  `"wsi-tools/%s transcode source=..."` for v0.5.0. The string emitted
  into TIFF tag 270 is bit-identical to v0.4. Switching to
  `wsitools/<version>` is queued for v0.5.1.

### Coordinated, but out of this spec

A small follow-up PR to `cornish/opentile-go`:

- `formats/generictiff/wsitools.go::wsiToolsPrefix` accepts both
  `"wsi-tools/"` and `"wsitools/"` prefixes.
- New v0.14.1 patch-level tag.

The user will ship this themselves (instructions provided at the end of
v0.5.0 implementation). Once opentile-go v0.14.1 is on the proxy and
bumped here, v0.5.1 ships the wsi-tools emission swap.

## Architecture (for v0.5.0)

No architectural changes. Every package keeps its boundaries, its public
surface, and its tests. The rename is a pure substitution exercise:

- Source tree organisation unchanged.
- Public APIs unchanged (just module-path-prefix changes for importers).
- Internal package structure unchanged.
- Integration test surface unchanged.

## Sequencing

1. **Branch** `feat/v0.5-rename-wsitools` from current main (`e74e83a`).
2. **Mechanical pass (narrow target — do not blanket-replace):**
   - `go.mod`: change `module github.com/cornish/wsi-tools` line. Same
     for `go.sum` if any reference appears (typically none).
   - `*.go` files: sed-replace ONLY the import path
     `"github.com/cornish/wsi-tools/"` → `"github.com/wsilabs/wsitools/"`.
     This is precise enough to leave the
     `cmd/wsitools/transcode.go::buildProvenanceDesc` literal
     `"wsi-tools/%s transcode source=..."` untouched (it stays).
   - `git mv cmd/wsi-tools cmd/wsitools` (preserves git history).
   - Hand-edit `cmd/wsitools/main.go` `cobra.Command{Use: "wsi-tools"}`
     → `Use: "wsitools"`.
   - Hand-edit `cmd/wsitools/version.go`
     `fmt.Printf("wsi-tools %s\n", ...)` → `fmt.Printf("wsitools %s\n", ...)`.
   - Update `Makefile`: `bin/wsi-tools` → `bin/wsitools`.
   - Update `.github/workflows/ci.yml`: `bin/wsi-tools` references.
   - **Explicitly skip:** files under `docs/superpowers/specs/` and
     `docs/superpowers/plans/`; CHANGELOG entries `[0.1.0]`–`[0.4.1]`;
     the in-source `wsi-tools/<version>` literal in `buildProvenanceDesc`.
3. **Update prose docs:** README, docs/roadmap.md, CLAUDE.md.
4. **Verify:** `make vet`, `make build`, `make test`, integration sweep
   (60m timeout, no `-race` per project memory).
5. **Smoke-test each subcommand** against `CMU-1-Small-Region.svs`.
6. **Add `[0.5.0]` CHANGELOG entry** explicitly framed as breaking-rename
   at the install/binary layer; non-breaking at the file-format layer.
7. **Bump `Version` constant** to `"0.5.0"`, commit.
8. **Local merge** `feat/v0.5-rename-wsitools` → `main`, fast-forward.
   **Tag `v0.5.0`. STOP HERE for user confirmation before push.**
9. **Push origin/main + tag, create GH release.**
10. **Post-release bump** `Version` → `"0.6.0-dev"`, commit, push.
11. **Verify v0.5.0 tag CI passes** (project policy: never declare shipped
    until tag CI is green).
12. **Rename the GitHub repo** `cornish/wsi-tools` → `wsilabs/wsitools`
    AFTER v0.5.0 tag CI is green. GitHub auto-redirects the old URL.
    Update local `origin` remote.
13. **Move local working directory** `/Users/cornish/GitHub/wsi-tools/`
    → `/Users/cornish/GitHub/wsitools/` only after step 12 confirms
    everything is healthy. Update the `sample_files` symlink target if
    needed (it points at `~/GitHub/opentile-go/sample_files`, so it
    survives the move; just verify).

The unusual ordering (rename repo AFTER tag is pushed) is deliberate:
GitHub's redirect kicks in for old URLs once the rename happens, so any
existing release URL `https://github.com/cornish/wsi-tools/releases/tag/v0.4.0`
keeps resolving via redirect after the rename. Renaming first would
create an awkward window where the v0.5.0 tag is being pushed to a
no-longer-existing repo URL.

## Final user-facing instructions (delivered at end of implementation)

After v0.5.0 ships and before v0.5.1, the user does the following at
their own pace:

```
A. Update opentile-go to accept both prefixes:
   1. cd ~/GitHub/opentile-go
   2. Edit formats/generictiff/wsitools.go: change the wsiToolsPrefix
      check so it matches BOTH "wsi-tools/" AND "wsitools/".
      (Two-line change: const a "wsi-tools/", const b "wsitools/",
      then HasPrefix(desc, a) || HasPrefix(desc, b).)
   3. Update tests in wsitools_test.go to assert both prefixes parse.
   4. Bump CHANGELOG.md with [0.14.1] — additive: accept wsitools/
      prefix for forward compat with wsitools v0.5.1+.
   5. Tag v0.14.1, push.

B. After opentile-go v0.14.1 is published, in wsitools:
   1. Cut feat/v0.5.1-emission-swap.
   2. go get github.com/cornish/opentile-go@v0.14.1
   3. cmd/wsitools/transcode.go::buildProvenanceDesc:
      change "wsi-tools/" → "wsitools/" in the format string.
   4. Bump CHANGELOG.md with [0.5.1] — emission swap completed.
   5. Bump Version to 0.5.1, tag, push, release.
```

These steps are the user's responsibility and explicitly out of scope for
the v0.5.0 milestone.

## Risks

- **Stale Go module proxy cache for the old path.** After the GitHub
  repo is renamed, `go install github.com/cornish/wsi-tools/cmd/wsitools@latest`
  may briefly serve cached state. Mitigation: install path was always
  going to break (cmd dir renamed); the v0.5.0 release notes give the
  new path explicitly.

- **Local-IDE / open-shell-session disruption.** Renaming the local
  working directory invalidates open editors, shells, and session-cwd
  paths. Mitigation: do the local rename after step 12, with shells and
  IDEs closed for the project.

- **Broken sample_files symlink.** The `sample_files` symlink in the
  project points at `$HOME/GitHub/opentile-go/sample_files`, which is
  unchanged. Verify the symlink still resolves after the local rename.

- **CI redirect flakiness for the renamed repo.** GitHub redirects
  HTTPS URLs reliably; the GitHub Actions workflow URL in the README
  badge will need updating to the new repo path or it'll break the
  badge. Update README badge URL as part of step 3.

## Testing

- All existing tests must pass after the rename — unit + integration.
- Integration sweep (60m, no -race per project memory).
- Manual smoke: each subcommand (`info`, `dump-ifds`, `extract`, `hash`,
  `transcode`, `downsample`, `doctor`, `version`) against
  `CMU-1-Small-Region.svs`.
- `wsitools version` must print `wsitools 0.5.0` after the version bump.
- v0.5.0 tag CI must pass on both macOS and Windows before the
  GitHub-repo rename happens.

## Versioning

Target **v0.5.0**. Breaking-rename at the install path / binary name
layer; non-breaking at the file-format layer. Communicated explicitly in
the CHANGELOG.

CHANGELOG framing for `[0.5.0]` (draft):

```markdown
## [0.5.0] — 2026-05-10

Project rename: `wsi-tools` → `wsitools`. Drops the hyphen everywhere
the project's identity is exposed (module path, repo URL, binary name,
CLI invocation). Output files are bit-identical to v0.4 — the
ImageDescription provenance string still emits `wsi-tools/<version>`
and will swap in v0.5.1 once opentile-go v0.14.1 is in place.

### Breaking (install path + binary name)

- Module path: `github.com/cornish/wsi-tools` → `github.com/wsilabs/wsitools`.
- Repo URL: `cornish/wsi-tools` → `wsilabs/wsitools` (GitHub auto-redirects old URLs).
- Binary name: `wsi-tools` → `wsitools`.
- Install: `go install github.com/wsilabs/wsitools/cmd/wsitools@latest`.

### Unchanged

- Every command and flag works identically.
- Output file format is unchanged. Slides written by v0.5.0 are
  byte-equivalent to slides written by v0.4 at the same options.
- The `WSI*` private TIFF tag namespace (65080–65084) keeps its
  current names and values.
- Existing v0.1.0–v0.4.1 binaries continue to work; the rename only
  affects new installs from `@latest`.

### Queued for v0.5.1

- ImageDescription provenance prefix swap from `wsi-tools/<version>`
  to `wsitools/<version>`, coordinated with an opentile-go v0.14.1
  patch that accepts both prefixes.
```
