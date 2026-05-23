# wsi-tools v0.5 — rename to wsitools (cosmetic milestone) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the project's identity from `wsi-tools` to `wsitools` everywhere it's externally exposed (module path, repo URL, binary name, CLI invocation, README/docs prose). Output files are bit-identical to v0.4 — the `wsi-tools/<version>` TIFF ImageDescription provenance string is **deliberately preserved** in v0.5.0 and will swap in v0.5.1 once the user ships an opentile-go v0.14.1 patch that accepts both prefixes.

**Architecture:** Pure-substitution rename. No new packages. No public API changes beyond the module-path-prefix change. Sequencing is unusual: the GitHub repo rename and the local working-directory rename happen *after* the v0.5.0 tag is pushed, so the GitHub redirect is set up cleanly and existing release URLs keep resolving.

**Tech Stack:** Go 1.22+ • cgo • opentile-go v0.14.0 (unchanged in this milestone) • cobra.

---

## File structure (what touches what)

| Path | Action | Responsibility |
|---|---|---|
| `go.mod` | modify | `module github.com/cornish/wsi-tools` → `module github.com/wsilabs/wsitools` |
| All `*.go` files | modify | sed-replace import path only: `"github.com/cornish/wsi-tools/"` → `"github.com/wsilabs/wsitools/"`. Leave non-import literal strings untouched. |
| `cmd/wsi-tools/` | rename | `git mv cmd/wsi-tools cmd/wsitools` (preserves history) |
| `cmd/wsitools/main.go` | modify | `cobra.Command{Use: "wsi-tools", ...}` → `Use: "wsitools"` |
| `cmd/wsitools/version.go` | modify | `fmt.Printf("wsi-tools %s\n", Version)` → `fmt.Printf("wsitools %s\n", Version)` |
| `cmd/wsitools/transcode.go` | **DO NOT MODIFY** the `buildProvenanceDesc` literal `"wsi-tools/%s transcode source=..."` — that's the v0.5.1 emission swap, deferred. |
| `Makefile` | modify | `bin/wsi-tools` → `bin/wsitools` (every occurrence) |
| `.github/workflows/ci.yml` | modify | `bin/wsi-tools` references |
| `README.md` | modify | title, CI badge URL, install command, every Usage example, prose |
| `CLAUDE.md` | modify | project name + module path |
| `docs/roadmap.md` | modify | "wsi-tools" prose mentions in current text only |
| `CHANGELOG.md` | modify | add `[0.5.0]` section above `[0.4.0]`. **Do not edit** any `[0.x.y]` entries from v0.4 or earlier — those are historical. |

**Explicitly NOT modified (historical artifacts):**
- `docs/superpowers/specs/2026-05-06-*` through `2026-05-09-*` (v0.1–v0.4 specs)
- `docs/superpowers/plans/2026-05-06-*` through `2026-05-09-*` (v0.1–v0.4 plans)
- CHANGELOG sections `[0.1.0]`–`[0.4.1]`
- The literal `"wsi-tools/%s ..."` format string in `cmd/wsitools/transcode.go::buildProvenanceDesc`
- Tag names `WSIImageType`, `WSILevelIndex`, `WSILevelCount`, `WSISourceFormat`, `WSIToolsVersion` (display labels for TIFF tags 65080–65084; preserve consistency with v0.2–v0.4 outputs in tiffinfo dumps)
- Go identifiers like `wsiwriter.WithToolsVersion`, `source.IFDRecord.WSIToolsVersion`, `source.CompressionWebP`, etc. (internal API; no churn)

---

## Conventions for the executor

- Working directory: `/Users/cornish/GitHub/wsi-tools/`
- Branch: `feat/v0.5-rename-wsitools` (already created from main; spec already committed at `9c02fd5`).
- One commit per task. Use the commit message verbatim from each task's last step.
- `make vet && make test` must pass at the end of every task.
- The integration sweep is run only at the final task — per-task verification is unit + build only.
- **Critical:** the sed pass for Go imports must use the precise pattern `github.com/cornish/wsi-tools/` (with the trailing slash), NOT the bare `wsi-tools` — otherwise the `wsi-tools/<version>` provenance string in `buildProvenanceDesc` will be incorrectly rewritten. This is deliberately deferred.
- macOS sed: use `sed -i ''` (empty backup arg). Linux sed differs; this plan assumes macOS per project context.

---

## Task 1: Module path + Go imports + cmd directory rename

**Files:**
- Modify: `go.mod`
- Modify: every `*.go` file with a `github.com/cornish/wsi-tools/` import (~all packages: `cmd/wsi-tools/*.go`, `internal/source/*.go`, `internal/cliout/*.go`, `internal/codec/all/*.go`, `internal/wsiwriter/*.go`, all integration tests)
- Rename: `cmd/wsi-tools/` → `cmd/wsitools/`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`

This is the largest mechanical task: the Go module identity changes, every internal-package import follows, the cmd directory is renamed (preserving git history), and the build pipeline updates to the new binary path.

- [ ] **Step 1: Confirm pre-state**

```bash
git rev-parse HEAD
git status --short
go.mod | head -1 || head -1 go.mod
```

Expected: HEAD is at the spec commit (`9c02fd5` or similar), working tree clean, `go.mod` line 1 reads `module github.com/cornish/wsi-tools`.

- [ ] **Step 2: Update go.mod module line**

```bash
sed -i '' 's|^module github.com/cornish/wsi-tools$|module github.com/wsilabs/wsitools|' go.mod
head -1 go.mod
```

Expected: `module github.com/wsilabs/wsitools`.

- [ ] **Step 3: Sed-replace import paths in all .go files**

```bash
find . -type f -name '*.go' -not -path './sample_files/*' \
  -exec sed -i '' 's|github.com/cornish/wsi-tools/|github.com/wsilabs/wsitools/|g' {} +
```

Verify the only remaining `github.com/cornish/wsi-tools` references are intentional (none in *.go after this pass):

```bash
grep -rn "github.com/cornish/wsi-tools" --include='*.go' . 2>&1 | head
```

Expected: zero hits (all .go imports now use `wsitools`).

Sanity-check that the `wsi-tools/<version>` provenance literal in `transcode.go` is NOT touched:

```bash
grep -n 'wsi-tools/' cmd/wsi-tools/transcode.go
```

Expected: one hit on the line `fmt.Fprintf(&b, "wsi-tools/%s transcode source=...` — exactly as before.

- [ ] **Step 4: Rename the cmd directory (preserves git history)**

```bash
git mv cmd/wsi-tools cmd/wsitools
ls cmd/
```

Expected: `cmd/wsitools/` exists; `cmd/wsi-tools/` is gone.

- [ ] **Step 5: Update Makefile**

Inspect first:

```bash
grep -n 'wsi-tools' Makefile
```

Then sed:

```bash
sed -i '' 's|bin/wsi-tools|bin/wsitools|g; s|cmd/wsi-tools|cmd/wsitools|g' Makefile
grep -n 'wsi' Makefile
```

Expected: all references now read `bin/wsitools` and `cmd/wsitools` (no leftover `wsi-tools`).

- [ ] **Step 6: Update CI workflow**

```bash
grep -n 'wsi-tools' .github/workflows/ci.yml
sed -i '' 's|bin/wsi-tools|bin/wsitools|g; s|cmd/wsi-tools|cmd/wsitools|g' .github/workflows/ci.yml
grep -n 'wsi' .github/workflows/ci.yml
```

Expected: no `wsi-tools` left in the workflow file (only `wsitools`).

- [ ] **Step 7: Build + run vet + run unit tests**

```bash
make vet
make build
make test
```

Expected: all pass clean. The binary is now built at `bin/wsitools`. The linker warning `ld: warning: ignoring duplicate libraries: '-lc++', '-lturbojpeg'` is benign and pre-existing.

- [ ] **Step 8: Smoke-check the binary location and version**

```bash
ls -la bin/wsitools
./bin/wsitools version
./bin/wsitools doctor
```

Expected:
- `bin/wsitools` exists (no `bin/wsi-tools`).
- `wsitools version` still prints `wsi-tools 0.5.0-dev` — that's correct for this task; the printf string is updated in Task 2.
- `doctor` lists the registered codecs.

- [ ] **Step 9: Commit**

```bash
git add go.mod .github/workflows/ci.yml Makefile cmd/wsitools/ internal/ tests/
git commit -m "$(cat <<'EOF'
refactor(rename): module github.com/cornish/wsi-tools -> wsitools

Renames the Go module path, every internal-package import, the cmd
directory (cmd/wsi-tools -> cmd/wsitools, git mv preserves history),
the Makefile binary target (bin/wsi-tools -> bin/wsitools), and the CI
workflow's binary references.

CLI Use string and version printf are updated in the next commit.
The wsi-tools/<version> TIFF ImageDescription provenance literal in
buildProvenanceDesc is deliberately preserved (queued for v0.5.1
once opentile-go v0.14.1 lands a parser that accepts both prefixes).
EOF
)"
git log --oneline -2
```

---

## Task 2: CLI identity strings (Use, version printf)

**Files:**
- Modify: `cmd/wsitools/main.go`
- Modify: `cmd/wsitools/version.go`

The user-facing strings printed by the CLI itself.

- [ ] **Step 1: Read main.go to locate cobra Use**

```bash
grep -n 'Use:' cmd/wsitools/main.go
```

Expected: one hit, something like `Use: "wsi-tools",`.

- [ ] **Step 2: Update cobra Use**

```bash
sed -i '' 's|Use:   "wsi-tools",|Use:   "wsitools",|' cmd/wsitools/main.go
grep -n 'Use:' cmd/wsitools/main.go
```

Expected: `Use:   "wsitools",`. (Whitespace before `"wsi-tools"` is three spaces in the existing source; sed preserves it because we matched the exact pattern.)

If the whitespace differs in the actual file, use:

```bash
sed -i '' 's|Use: *"wsi-tools",|Use: "wsitools",|' cmd/wsitools/main.go
```

- [ ] **Step 3: Read version.go to locate the printf**

```bash
cat cmd/wsitools/version.go
```

Expected: a printf that reads `fmt.Printf("wsi-tools %s\n", Version)`.

- [ ] **Step 4: Update version printf**

```bash
sed -i '' 's|"wsi-tools %s\\n"|"wsitools %s\\n"|' cmd/wsitools/version.go
grep -n 'wsi' cmd/wsitools/version.go
```

Expected: line now reads `fmt.Printf("wsitools %s\n", Version)`.

- [ ] **Step 5: Build and verify CLI surface**

```bash
make build
./bin/wsitools version
./bin/wsitools --help | head -10
```

Expected:
- `wsitools 0.5.0-dev` (not `wsi-tools 0.5.0-dev`).
- Help text top line reads `wsitools — a Swiss-army knife...` (cobra Use is now `wsitools`).

- [ ] **Step 6: Run unit suite**

```bash
make test
```

Expected: all pass (no test pins on the `wsi-tools` literal label string).

- [ ] **Step 7: Commit**

```bash
git add cmd/wsitools/main.go cmd/wsitools/version.go
git commit -m "$(cat <<'EOF'
refactor(rename): wsi-tools -> wsitools in CLI identity strings

cobra.Command.Use is now "wsitools" (was "wsi-tools"). The version
subcommand prints "wsitools <version>" (was "wsi-tools <version>").

The Version constant value is unchanged — only its display label.
EOF
)"
git log --oneline -2
```

---

## Task 3: Prose docs (README, CLAUDE.md, docs/roadmap.md)

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `docs/roadmap.md`

User-facing prose. Must update title, CI badge URL, install command, every Usage example, and every "wsi-tools" mention in the active prose. Historical references in `docs/superpowers/specs/`, `docs/superpowers/plans/`, and CHANGELOG entries v0.1.0–v0.4.1 are NOT touched.

- [ ] **Step 1: Survey current state**

```bash
grep -cn "wsi-tools" README.md CLAUDE.md docs/roadmap.md
```

Note the counts — they'll drop to zero after this task (or only stay >0 for the title-line if `# wsi-tools` shows up, which it does in README and is a target).

- [ ] **Step 2: Update README**

The README needs four kinds of edit:

1. Title: `# wsi-tools` → `# wsitools`
2. CI badge URL: `cornish/wsi-tools/actions/workflows/ci.yml` → `wsilabs/wsitools/actions/workflows/ci.yml`. Also pkg.go.dev badge: `pkg.go.dev/badge/github.com/cornish/wsi-tools.svg` → `pkg.go.dev/badge/github.com/wsilabs/wsitools.svg`.
3. Install command: `go install github.com/cornish/wsi-tools/cmd/wsi-tools@latest` → `go install github.com/wsilabs/wsitools/cmd/wsitools@latest`.
4. Every prose mention of `wsi-tools` (subcommand examples, "v0.4 — what's here", etc.).

A blanket sed handles all four cleanly because every `wsi-tools` in this file is a target:

```bash
sed -i '' 's|wsi-tools|wsitools|g' README.md
grep -cn "wsi-tools" README.md
```

Expected: zero hits remain. Inspect to confirm:

```bash
head -25 README.md
```

Expected first 25 lines: title now `# wsitools`, badges link to `wsilabs/wsitools`, "v0.4 — what's here" intact.

- [ ] **Step 3: Update CLAUDE.md**

Inspect first:

```bash
cat CLAUDE.md
```

Look for the project-name line and the module-path line. Both should change. Then sed:

```bash
sed -i '' 's|wsi-tools|wsitools|g' CLAUDE.md
grep -cn "wsi-tools" CLAUDE.md
```

Expected: zero hits remain.

- [ ] **Step 4: Update docs/roadmap.md**

Inspect:

```bash
grep -n "wsi-tools" docs/roadmap.md
```

Then sed:

```bash
sed -i '' 's|wsi-tools|wsitools|g' docs/roadmap.md
grep -cn "wsi-tools" docs/roadmap.md
```

Expected: zero hits remain.

- [ ] **Step 5: Verify historical docs are untouched**

```bash
grep -l "wsi-tools" docs/superpowers/specs/ docs/superpowers/plans/ 2>/dev/null | head
```

Expected: many hits (every historical spec/plan is preserved as-is). This is intentional.

```bash
awk '/^## \[0\.4\.1\]/{flag=1} /^## \[0\.4\.0\]/{flag=2} flag==1 && /wsi-tools/{print "0.4.1: " $0}' CHANGELOG.md | head -3
```

Expected: at least one hit in the v0.4.1 section. Historical entries stay.

- [ ] **Step 6: Build + test (no code changed but Makefile already moved)**

```bash
make vet
make build
make test
./bin/wsitools --help | head -3
```

Expected: all pass; help still prints `wsitools` Use string.

- [ ] **Step 7: Commit**

```bash
git add README.md CLAUDE.md docs/roadmap.md
git commit -m "$(cat <<'EOF'
docs: rename wsi-tools -> wsitools in active prose

Updates README (title, CI badge, install command, every Usage example),
CLAUDE.md (project name + module path), and docs/roadmap.md.

Historical specs and plans under docs/superpowers/specs/ and
docs/superpowers/plans/, and CHANGELOG entries [0.1.0]-[0.4.1], are
deliberately preserved as time-capsule artifacts.
EOF
)"
git log --oneline -2
```

---

## Task 4: CHANGELOG `[0.5.0]` entry

**Files:**
- Modify: `CHANGELOG.md`

Add the `[0.5.0]` section above `[0.4.0]`. Do not edit anything below the `[0.4.0]` heading.

- [ ] **Step 1: Find the insertion point**

```bash
grep -n '^## ' CHANGELOG.md | head -8
```

Expected: `[Unreleased]` first, then `[0.4.0]`. The new `[0.5.0]` section goes between them.

- [ ] **Step 2: Insert the new section**

Open `CHANGELOG.md` and insert this block immediately after `## [Unreleased]` and one blank line, and before `## [0.4.0] — 2026-05-09`:

```markdown
## [0.5.0] — 2026-05-10

Project rename: `wsi-tools` → `wsitools`. Drops the hyphen everywhere
the project's identity is exposed (module path, repo URL, binary name,
CLI invocation, README/docs prose). Output files are bit-identical to
v0.4 — the ImageDescription provenance string still emits
`wsi-tools/<version>` and will swap in v0.5.1 once opentile-go v0.14.1
ships a parser that accepts both prefixes.

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
- Historical specs/plans under `docs/superpowers/` and CHANGELOG
  entries v0.1.0–v0.4.1 retain their original "wsi-tools" prose as
  time-capsule artifacts.
- Existing v0.1.0–v0.4.1 binaries continue to work; the rename only
  affects new installs from `@latest`.

### Queued for v0.5.1

- ImageDescription provenance prefix swap from `wsi-tools/<version>`
  to `wsitools/<version>`, coordinated with an opentile-go v0.14.1
  patch that accepts both prefixes.
```

- [ ] **Step 3: Verify section ordering**

```bash
grep -n '^## ' CHANGELOG.md | head -8
```

Expected ordering: `[Unreleased]`, `[0.5.0]`, `[0.4.0]`, `[0.3.1]`, `[0.3.0]`, `[0.2.0]`, `[0.1.0]`.

- [ ] **Step 4: Verify v0.4 entries are untouched**

```bash
awk '/^## \[0\.4\.0\]/,/^## \[0\.3\.1\]/' CHANGELOG.md | head -5
```

Expected: the v0.4.0 section opens with the original "Inspection-utilities milestone..." prose unchanged.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: CHANGELOG.md for v0.5.0"
git log --oneline -2
```

---

## Task 5: Final smoke + Version bump + tag v0.5.0 (LOCAL ONLY)

**Files:**
- Modify: `cmd/wsitools/version.go` (bump `Version` constant from `"0.5.0-dev"` to `"0.5.0"`)

This task includes the full integration sweep + Version bump + local merge to main + annotated tag. **STOP at the end of this task — the next task pushes to origin and creates a public GitHub Release. Confirm with the controller / user first.**

- [ ] **Step 1: Final regression check**

```bash
make vet
make build
make test
WSI_TOOLS_TESTDIR=$HOME/GitHub/opentile-go/sample_files \
  go test ./tests/integration/ -tags integration -count=1 -timeout 60m
./bin/wsitools doctor
./bin/wsitools version
./bin/wsitools info sample_files/svs/CMU-1-Small-Region.svs | head -10
./bin/wsitools dump-ifds sample_files/svs/CMU-1-Small-Region.svs | head -5
./bin/wsitools hash sample_files/svs/CMU-1-Small-Region.svs
./bin/wsitools extract --kind label -o /tmp/v05-label.png sample_files/svs/CMU-1-Small-Region.svs
ls -la /tmp/v05-label.png && rm /tmp/v05-label.png
./bin/wsitools transcode --codec webp -o /tmp/v05-final.tiff sample_files/svs/CMU-1-Small-Region.svs
ls -la /tmp/v05-final.tiff && rm /tmp/v05-final.tiff
```

Expected: every step passes. `wsitools version` should print `wsitools 0.5.0-dev`. The integration sweep takes ~26–29 minutes; use the 60m timeout per project memory.

- [ ] **Step 2: Bump Version constant**

```bash
sed -i '' 's|const Version = "0.5.0-dev"|const Version = "0.5.0"|' cmd/wsitools/version.go
go build -o bin/wsitools ./cmd/wsitools
./bin/wsitools version
```

Expected: `wsitools 0.5.0`.

- [ ] **Step 3: Commit version bump**

```bash
git add cmd/wsitools/version.go
git commit -m "release: bump Version to 0.5.0"
git log --oneline -3
```

- [ ] **Step 4: Verify ff-only merge feasibility**

```bash
git fetch origin
git log --oneline origin/main -3
git log --oneline main -3
git rev-list --count main..feat/v0.5-rename-wsitools
```

Expected: `origin/main` and local `main` at the same commit (the v0.4 post-release bump `34d4b2d` or whatever followed). Feat branch is ~6 commits ahead. ff-only will work.

- [ ] **Step 5: Local merge to main + tag**

```bash
git checkout main
git merge --ff-only feat/v0.5-rename-wsitools
git log --oneline -5
git tag -a v0.5.0 -m "wsitools v0.5.0 — project rename (wsi-tools -> wsitools); cosmetic milestone"
git tag -l v0.5.0 -n5
```

Expected: main fast-forwarded; tag v0.5.0 exists at the version-bump commit.

- [ ] **Step 6: STOP — confirm with user before push**

The next task pushes to origin, creates a public GitHub Release, runs the post-release Version bump, AND renames the GitHub repo. All four are externally visible. Report the local state to the controller and wait for explicit confirmation:

```
State now:
- main fast-forwarded to <SHA>
- annotated tag v0.5.0 at <SHA>
- N commits ahead of origin/main, nothing pushed yet

Next, irreversible:
  git push origin main
  git push origin v0.5.0
  gh release create v0.5.0 --title "v0.5.0 — project rename"
  # post-release: bump Version to 0.6.0-dev + commit + push
  # verify v0.5.0 tag CI on macOS + Windows
  # rename GitHub repo cornish/wsi-tools -> wsilabs/wsitools
  # update local origin remote
  # rename local working dir
```

---

## Task 6: Push, release, post-release bump, GitHub repo rename, local dir rename

**Files:**
- Modify: `cmd/wsitools/version.go` (bump `Version` to `"0.6.0-dev"`)

**Triggered only after the user confirms.** Each step is a checkpoint — if any one fails, stop and report.

- [ ] **Step 1: Push origin/main + tag**

```bash
git push origin main
git push origin v0.5.0
```

Expected: both push successfully. Sample output:
```
To https://github.com/cornish/wsi-tools.git
   <oldSHA>..<newSHA>  main -> main
   * [new tag]         v0.5.0 -> v0.5.0
```

- [ ] **Step 2: Extract release notes from CHANGELOG**

```bash
awk '/^## \[0\.5\.0\]/{flag=1; next} /^## \[/{flag=0} flag' CHANGELOG.md > /tmp/v0.5.0-release-notes.md
wc -l /tmp/v0.5.0-release-notes.md
head -3 /tmp/v0.5.0-release-notes.md
```

Expected: ~30+ lines starting with the rename intro paragraph.

- [ ] **Step 3: Create GitHub release**

```bash
gh release create v0.5.0 --title "v0.5.0 — project rename (wsi-tools -> wsitools)" --notes-file /tmp/v0.5.0-release-notes.md
```

Expected: prints the release URL, `https://github.com/cornish/wsi-tools/releases/tag/v0.5.0` (still on the old repo URL — the rename happens after CI passes).

- [ ] **Step 4: Post-release bump to 0.6.0-dev**

```bash
sed -i '' 's|const Version = "0.5.0"|const Version = "0.6.0-dev"|' cmd/wsitools/version.go
git add cmd/wsitools/version.go
git commit -m "post-release: bump Version to 0.6.0-dev"
git push origin main
./bin/wsitools version 2>/dev/null || go build -o bin/wsitools ./cmd/wsitools && ./bin/wsitools version
```

Expected: build succeeds; printout shows `wsitools 0.6.0-dev`.

- [ ] **Step 5: Verify v0.5.0 tag CI passes on both platforms**

```bash
gh run list --branch v0.5.0 --limit 1
```

Get the run ID, then poll for completion. Once complete:

```bash
gh run view <RUN_ID> --json status,conclusion,jobs \
  | jq -r '"status: \(.status)", "conclusion: \(.conclusion // "-")", (.jobs[] | "  \(.conclusion // .status)\t\(.name)")'
```

Expected: `status: completed`, `conclusion: success`, both `build + test (macOS)` and `build (Windows)` show success.

If either fails, **STOP and triage before doing the GitHub repo rename**. The repo rename is the point of no easy return — it should only happen after green CI.

- [ ] **Step 6: Rename the GitHub repo**

```bash
gh repo rename wsitools --repo cornish/wsi-tools
```

Expected: GitHub renames the repo. Existing URLs to `cornish/wsi-tools` auto-redirect to `wsilabs/wsitools`. Releases, tags, issues, PRs all carry over.

Verify:

```bash
gh repo view wsilabs/wsitools | head -3
```

Expected: shows the renamed repo.

- [ ] **Step 7: Update the local origin remote**

```bash
git remote -v
git remote set-url origin https://github.com/wsilabs/wsitools.git
git remote -v
git fetch origin
```

Expected: origin URL now points at the new repo. Fetch confirms connectivity.

- [ ] **Step 8: Optionally rename the local working directory**

This step changes the cwd path everywhere it appears (open shells, IDEs, scripts). **Only do this when no shells, IDEs, Claude sessions, or tooling are open against `/Users/cornish/GitHub/wsi-tools/`.**

If ready:

```bash
cd ~
mv ~/GitHub/wsi-tools ~/GitHub/wsitools
ls -la ~/GitHub/wsitools/sample_files
cd ~/GitHub/wsitools
make build
./bin/wsitools version
```

Expected: directory renamed; `sample_files` symlink still resolves (it points at `~/GitHub/opentile-go/sample_files`, unaffected by this move); build + version still work.

If the executor isn't sure all sessions are closed: skip step 8. Renaming the local directory is purely a local convenience and can be done any time.

- [ ] **Step 9: Print follow-up instructions for the user (opentile-go v0.14.1 + wsitools v0.5.1)**

Print this exactly to the controller / user:

```
v0.5.0 SHIPPED.

To complete the coordinated emission swap, do the following at your pace:

A. Update opentile-go to accept both ImageDescription prefixes:

   1. cd ~/GitHub/opentile-go
   2. Edit formats/generictiff/wsitools.go.
      Change the wsiToolsPrefix constant + check so it matches BOTH
      "wsi-tools/" AND "wsitools/" prefixes:

      // Before:
      const wsiToolsPrefix = "wsi-tools/"
      ...
      if !strings.HasPrefix(desc, wsiToolsPrefix) { return ..., false }

      // After:
      const (
          wsiToolsPrefix    = "wsi-tools/" // legacy: wsi-tools v0.2-v0.4
          wsitoolsPrefix    = "wsitools/"  // forward: wsitools v0.5.1+
      )
      ...
      if !strings.HasPrefix(desc, wsiToolsPrefix) &&
         !strings.HasPrefix(desc, wsitoolsPrefix) {
          return ..., false
      }

   3. Update tests in wsitools_test.go to assert both prefixes parse.
   4. Bump CHANGELOG.md with [0.14.1] — additive: accept wsitools/
      prefix for forward compat with wsitools v0.5.1+.
   5. Tag v0.14.1, push.

B. After opentile-go v0.14.1 is published, in wsitools:

   1. cd ~/GitHub/wsitools
   2. git checkout -b feat/v0.5.1-emission-swap
   3. go get github.com/cornish/opentile-go@v0.14.1
   4. Edit cmd/wsitools/transcode.go::buildProvenanceDesc:
      change "wsi-tools/%s" to "wsitools/%s" in the format string.
   5. Run integration tests; they should still pass (opentile-go v0.14.1
      now accepts the new prefix).
   6. Bump CHANGELOG.md with [0.5.1] — emission swap completed.
   7. Bump Version to 0.5.1, commit, merge, tag, push, release.
```

---

## Self-review checklist (executor: do this after Task 6)

1. **All 6 tasks committed?** `git log --oneline v0.4.1..v0.5.0` — expect ~5 commits (one per task plus the version bump).
2. **All tests pass on the v0.5.0 tag?** Both macOS and Windows green per CI.
3. **`./bin/wsitools version`** prints `wsitools 0.5.0` (tagged build) or `0.6.0-dev` (post-release main).
4. **GitHub repo URL works**: `https://github.com/wsilabs/wsitools` resolves; `https://github.com/cornish/wsi-tools` redirects to it.
5. **Old release URLs still work**: `https://github.com/cornish/wsi-tools/releases/tag/v0.4.0` redirects to the new path.
6. **No `wsi-tools` references** in active prose: README, CLAUDE.md, docs/roadmap.md should all be clean.
7. **Historical artifacts untouched**: `docs/superpowers/specs/`, `docs/superpowers/plans/`, CHANGELOG entries `[0.1.0]`–`[0.4.1]` still say `wsi-tools` (intentional).
8. **`buildProvenanceDesc` literal still says `wsi-tools/%s`**: verify with `grep "wsi-tools" cmd/wsitools/transcode.go`. Exactly one hit on the format string. This is the v0.5.1 follow-up target.
9. **`source.IFDRecord.WSIToolsVersion`, `wsiwriter.WithToolsVersion`, etc., are still named with `WSI*` / `Tools*`**: internal Go API, unchanged.
10. **`sample_files` symlink resolves** if the local dir was renamed.
