# Static, Self-Contained Binary Releases — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Attach statically-linked, download-and-run `wsitools` binaries (all six codecs) for five OS/arch targets to every `vX.Y.Z` GitHub Release, with macOS signed+notarized and a `SHA256SUMS` manifest.

**Architecture:** cgo forbids cross-compilation, so each target builds on a native runner. The six codec C libraries are sourced as **static** libs via **vcpkg** static triplets (uniform across platforms, emitting pkg-config files cgo already consumes). A shared **composite action** encapsulates the vcpkg-install → static-build → `doctor` smoke-test → static-linkage-assertion recipe; it is invoked by both a tag-triggered 5-target matrix in `release.yml` and a one-target (linux/musl) build-only **canary** in `release-canary.yml`. The one Go source change is making htj2k discover OpenJPH via pkg-config instead of hardcoded `/opt/homebrew` paths.

**Tech Stack:** Go 1.26 (cgo), vcpkg (C/C++ deps, static triplets), GitHub Actions (native-runner matrix, Alpine/musl container for Linux), Apple `codesign`/`notarytool`/`stapler`.

**Spec:** `docs/superpowers/specs/2026-06-26-static-binary-releases-design.md`

---

## File Structure

| Path | Create/Modify | Responsibility |
|---|---|---|
| `internal/codec/htj2k/htj2k.go` | Modify | Replace hardcoded `/opt/homebrew` cgo flags with `pkg-config: openjph` + GOOS-conditional C++ stdlib link |
| `vcpkg.json` | Create | Manifest pinning the 6 codec libs + a builtin baseline (reproducible versions) |
| `vcpkg-configuration.json` | Create | Pin the vcpkg registry baseline commit |
| `.github/actions/build-static/action.yml` | Create | Composite action: bootstrap vcpkg → install (triplet) → static `go build` → `doctor` smoke → linkage assertion → stage artifact |
| `.github/workflows/release-canary.yml` | Create | One-target (linux/amd64 musl) build-only canary on release-path PRs/pushes |
| `.github/workflows/release.yml` | Modify | Add `build` (5-target matrix, sign/notarize on macOS, upload) + `checksums` jobs; keep notes job; add `workflow_dispatch` |
| `docs/RELEASING.md` | Create | Maintainer runbook: required secrets, how to cut a release, dry-run, troubleshooting |
| `README.md` | Modify | "Install — prebuilt binaries" section + per-platform codec matrix |

---

## Task 1: htj2k pkg-config portability fix

The htj2k codec hardcodes Homebrew paths, so it only builds on an Apple-Silicon Mac with Homebrew. Every other build environment (Intel Mac `/usr/local`, Linux, Windows, vcpkg) needs pkg-config discovery. This is a prerequisite for *any* portable/CI static build. Verified locally: `openjph.pc` is already discoverable via Homebrew (v0.27.3), and `openjph.pc` provides `-lopenjph` but **not** the C++ stdlib, so an explicit GOOS-conditional `-lc++`/`-lstdc++` must remain (the package compiles `shim.cpp`, C++17).

**Files:**
- Modify: `internal/codec/htj2k/htj2k.go:6-8`
- Test (existing, must stay green): `internal/codec/htj2k/htj2k_test.go`

- [ ] **Step 1: Confirm the existing htj2k test passes BEFORE the change (baseline)**

Run:
```bash
go test ./internal/codec/htj2k/ 2>&1 | grep -v "duplicate librar"
```
Expected: `ok  github.com/wsilabs/wsitools/internal/codec/htj2k`

- [ ] **Step 2: Replace the cgo preamble flags**

In `internal/codec/htj2k/htj2k.go`, replace exactly these two lines:

```go
#cgo CXXFLAGS: -I/opt/homebrew/include -std=c++17
#cgo LDFLAGS: -L/opt/homebrew/lib -lopenjph -lstdc++
```

with:

```go
#cgo CXXFLAGS: -std=c++17
#cgo pkg-config: openjph
#cgo darwin LDFLAGS: -lc++
#cgo linux LDFLAGS: -lstdc++
#cgo windows LDFLAGS: -lstdc++
```

Rationale: `pkg-config: openjph` resolves the include dir + `-lopenjph` from `openjph.pc` (works on Homebrew today and on vcpkg in CI). The per-GOOS `LDFLAGS` supply the C++ standard library that `.pc` omits — `libc++` on macOS (clang), `libstdc++` on Linux/Windows (gcc/mingw). This exact block was build-verified on macOS arm64 (binary built, `wsitools doctor` listed `htj2k`).

- [ ] **Step 3: Build the package and the full binary**

Run:
```bash
go build ./internal/codec/htj2k/ && go build -o /tmp/wsitools-t1 ./cmd/wsitools 2>&1 | grep -v "duplicate librar"; echo "exit=$?"
```
Expected: no errors; `/tmp/wsitools-t1` produced.

- [ ] **Step 4: Verify htj2k is still registered and the test passes**

Run:
```bash
/tmp/wsitools-t1 doctor | grep htj2k && go test ./internal/codec/htj2k/ 2>&1 | grep -v "duplicate librar"
```
Expected: `  ✓ htj2k` and `ok  …/internal/codec/htj2k`.

- [ ] **Step 5: Verify the `nohtj2k` build tag still compiles (no regression to the opt-out path)**

Run:
```bash
go build -tags nohtj2k -o /tmp/wsitools-nohtj2k ./cmd/wsitools 2>&1 | grep -v "duplicate librar"; echo "exit=$?"
/tmp/wsitools-nohtj2k doctor | grep -c htj2k || echo "htj2k correctly absent"
```
Expected: builds; htj2k absent from `doctor`.

- [ ] **Step 6: Commit**

```bash
git add internal/codec/htj2k/htj2k.go
git commit -m "fix(htj2k): discover OpenJPH via pkg-config, not hardcoded /opt/homebrew

Hardcoded -I/opt/homebrew paths only built on an Apple-Silicon Mac with
Homebrew. Switch to pkg-config: openjph (works on Homebrew + vcpkg) and
supply the C++ stdlib that openjph.pc omits via GOOS-conditional LDFLAGS
(-lc++ on darwin/clang, -lstdc++ on linux+windows/gcc). Prereq for portable
static CI builds; also fixes Intel-Mac and clean-env builds.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: vcpkg manifest + local static-build proof

Create the vcpkg manifest that pins the six codec libraries, and prove — locally, on the dev Mac — that vcpkg can build them **statically** and that `wsitools` links against vcpkg's static `.pc` files. This de-risks the single hardest technical assumption before any CI is written. (If the engineer's machine lacks vcpkg, install it once: `git clone https://github.com/microsoft/vcpkg ~/vcpkg && ~/vcpkg/bootstrap-vcpkg.sh`.)

**Files:**
- Create: `vcpkg.json`
- Create: `vcpkg-configuration.json`

- [ ] **Step 1: Create `vcpkg.json`**

```json
{
  "$schema": "https://raw.githubusercontent.com/microsoft/vcpkg-tool/main/docs/vcpkg.schema.json",
  "name": "wsitools",
  "version-string": "0.0.0",
  "description": "Codec C libraries for wsitools static binary builds",
  "dependencies": [
    "libjpeg-turbo",
    "openjpeg",
    "libjxl",
    "libavif",
    "libwebp",
    "openjph"
  ],
  "builtin-baseline": "BASELINE_PLACEHOLDER"
}
```

- [ ] **Step 2: Pin the baseline to the current vcpkg registry commit**

Run (replaces the placeholder with a real commit SHA — `builtin-baseline` MUST be a 40-char vcpkg git SHA, not a tag):
```bash
BASELINE=$(git -C ~/vcpkg rev-parse HEAD)
sed -i '' "s/BASELINE_PLACEHOLDER/$BASELINE/" vcpkg.json   # macOS sed; on Linux drop the ''
echo "baseline=$BASELINE"
```
Expected: `vcpkg.json` now has a 40-hex-char `builtin-baseline`.

- [ ] **Step 3: Create `vcpkg-configuration.json` (pins the registry for reproducibility)**

```json
{
  "default-registry": {
    "kind": "git",
    "repository": "https://github.com/microsoft/vcpkg",
    "baseline": "BASELINE_PLACEHOLDER"
  }
}
```
Then run the same substitution:
```bash
sed -i '' "s/BASELINE_PLACEHOLDER/$BASELINE/" vcpkg-configuration.json   # macOS sed
```

- [ ] **Step 4: Install the six libs with a static triplet (local proof)**

Run (macOS arm64 → `arm64-osx` is dynamic by default; force static via the `-static` community triplet pattern using overlay, or use `--triplet arm64-osx` with `VCPKG_LIBRARY_LINKAGE=static`). The portable invocation:
```bash
VCPKG_FEATURE_FLAGS=manifests \
  ~/vcpkg/vcpkg install --triplet arm64-osx \
  --x-install-root="$PWD/vcpkg_installed" \
  --overlay-triplets=.github/vcpkg-triplets 2>&1 | tail -20
```
This step also requires the static triplet file from Step 5; do Step 5 first, then run this. Expected: vcpkg builds all six packages; `vcpkg_installed/arm64-osx-static*/lib` contains `.a` files (`libopenjph.a`, `libjpeg.a`/`libturbojpeg.a`, `libopenjp2.a`, `libjxl.a`, `libavif.a`, `libwebp.a`) and `…/lib/pkgconfig/*.pc`.

- [ ] **Step 5: Create the static overlay triplets**

vcpkg's default `*-osx`/`*-linux` triplets are dynamic on some platforms; pin static linkage explicitly so every platform behaves identically.

Create `.github/vcpkg-triplets/arm64-osx-static.cmake`:
```cmake
set(VCPKG_TARGET_ARCHITECTURE arm64)
set(VCPKG_CRT_LINKAGE dynamic)
set(VCPKG_LIBRARY_LINKAGE static)
set(VCPKG_CMAKE_SYSTEM_NAME Darwin)
set(VCPKG_OSX_ARCHITECTURES arm64)
```

Create `.github/vcpkg-triplets/x64-osx-static.cmake` (same, `x86_64`/`VCPKG_OSX_ARCHITECTURES x86_64`, `VCPKG_TARGET_ARCHITECTURE x64`).

Create `.github/vcpkg-triplets/x64-linux-static.cmake` and `.github/vcpkg-triplets/arm64-linux-static.cmake`:
```cmake
set(VCPKG_TARGET_ARCHITECTURE x64)        # arm64 in the arm64 file
set(VCPKG_CRT_LINKAGE dynamic)
set(VCPKG_LIBRARY_LINKAGE static)
set(VCPKG_CMAKE_SYSTEM_NAME Linux)
```

Windows uses the built-in `x64-mingw-static` triplet (no overlay needed).

Re-run Step 4 with `--triplet arm64-osx-static --overlay-triplets=.github/vcpkg-triplets`.

- [ ] **Step 6: Build wsitools against the vcpkg static libs**

Run:
```bash
export PKG_CONFIG_PATH="$PWD/vcpkg_installed/arm64-osx-static/lib/pkgconfig"
pkg-config --exists openjph && echo "vcpkg openjph.pc visible"
CGO_ENABLED=1 go build -trimpath -o /tmp/wsitools-vcpkg ./cmd/wsitools 2>&1 | grep -v "duplicate librar"; echo "exit=$?"
/tmp/wsitools-vcpkg doctor
```
Expected: builds; `doctor` lists all six codecs (`avif htj2k jpeg jpeg2000 jpegxl webp`). This proves vcpkg static deps + cgo link end-to-end on at least one platform.

- [ ] **Step 7: Gitignore the local build artifacts**

Append to `.gitignore`:
```
/vcpkg_installed/
/vcpkg/
```

- [ ] **Step 8: Commit**

```bash
git add vcpkg.json vcpkg-configuration.json .github/vcpkg-triplets/ .gitignore
git commit -m "build(release): vcpkg manifest + static overlay triplets

Pin the 6 codec C libs (libjpeg-turbo, openjpeg, libjxl, libavif, libwebp,
openjph) via a vcpkg manifest with a builtin baseline, plus static overlay
triplets (LIBRARY_LINKAGE=static) for osx/linux. Locally verified: vcpkg
builds all six static and wsitools links + doctor lists all codecs.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: shared composite action `build-static`

Encapsulate the build recipe once so the canary and the release matrix can't drift. The composite assumes **Go is already on PATH** (container images ship it; mac/win jobs run `actions/setup-go` first) and focuses on vcpkg + build + smoke + linkage assertion + staging the artifact.

**Files:**
- Create: `.github/actions/build-static/action.yml`

- [ ] **Step 1: Write the composite action**

```yaml
name: Build static wsitools
description: vcpkg static deps + static go build + doctor smoke + linkage assertion
inputs:
  goos:        { description: "linux|darwin|windows", required: true }
  goarch:      { description: "amd64|arm64", required: true }
  vcpkg-triplet: { description: "e.g. x64-linux-static, arm64-osx-static, x64-mingw-static", required: true }
  static-libc: { description: "true to add -extldflags -static (linux/windows)", required: true }
  bin-name:    { description: "wsitools or wsitools.exe", required: true }
outputs:
  artifact-path: { description: "staged binary path", value: ${{ steps.stage.outputs.path }} }
runs:
  using: composite
  steps:
    - name: Bootstrap vcpkg (pinned)
      shell: bash
      run: |
        set -euo pipefail
        git clone https://github.com/microsoft/vcpkg "$RUNNER_TEMP/vcpkg"
        BASELINE=$(python3 -c "import json;print(json.load(open('vcpkg.json'))['builtin-baseline'])")
        git -C "$RUNNER_TEMP/vcpkg" checkout "$BASELINE"
        "$RUNNER_TEMP/vcpkg/bootstrap-vcpkg.sh" -disableMetrics
        echo "VCPKG_ROOT=$RUNNER_TEMP/vcpkg" >> "$GITHUB_ENV"

    - name: Enable vcpkg GitHub Actions binary cache
      uses: actions/github-script@v7
      with:
        script: |
          core.exportVariable('ACTIONS_CACHE_URL', process.env.ACTIONS_CACHE_URL || '');
          core.exportVariable('ACTIONS_RUNTIME_TOKEN', process.env.ACTIONS_RUNTIME_TOKEN || '');

    - name: Install codec libs (static)
      shell: bash
      env:
        VCPKG_BINARY_SOURCES: "clear;x-gha,readwrite"
      run: |
        set -euo pipefail
        "$VCPKG_ROOT/vcpkg" install \
          --triplet "${{ inputs.vcpkg-triplet }}" \
          --overlay-triplets="$PWD/.github/vcpkg-triplets" \
          --x-install-root="$PWD/vcpkg_installed"
        echo "PKG_CONFIG_PATH=$PWD/vcpkg_installed/${{ inputs.vcpkg-triplet }}/lib/pkgconfig" >> "$GITHUB_ENV"

    - name: Static build
      shell: bash
      env:
        CGO_ENABLED: "1"
        GOOS: ${{ inputs.goos }}
        GOARCH: ${{ inputs.goarch }}
      run: |
        set -euo pipefail
        LDFLAGS="-s -w"
        if [ "${{ inputs.static-libc }}" = "true" ]; then
          LDFLAGS="$LDFLAGS -extldflags \"-static\""
        fi
        go build -trimpath -ldflags "$LDFLAGS" -o "${{ inputs.bin-name }}" ./cmd/wsitools

    - name: Smoke test — version + all six codecs
      shell: bash
      run: |
        set -euo pipefail
        ./${{ inputs.bin-name }} version
        OUT=$(./${{ inputs.bin-name }} doctor)
        echo "$OUT"
        for c in jpeg jpeg2000 htj2k jpegxl avif webp; do
          echo "$OUT" | grep -qE "✓ $c\b" || { echo "MISSING CODEC: $c"; exit 1; }
        done

    - name: Assert static linkage
      shell: bash
      run: |
        set -euo pipefail
        case "${{ inputs.goos }}" in
          linux)
            # musl-static => "not a dynamic executable"
            if ldd "${{ inputs.bin-name }}" 2>&1 | grep -qiE "not a dynamic executable|statically linked"; then
              echo "linux: static OK"
            else
              echo "linux: NOT static:"; ldd "${{ inputs.bin-name }}" || true; exit 1
            fi ;;
          darwin)
            # no Homebrew/vcpkg dylibs; only /usr/lib + /System
            if otool -L "${{ inputs.bin-name }}" | tail -n +2 | grep -vqE "/usr/lib/|/System/"; then
              echo "darwin: NON-system dylib linked:"; otool -L "${{ inputs.bin-name }}"; exit 1
            fi
            echo "darwin: only system dylibs OK" ;;
          windows)
            # mingw codec DLLs must not be referenced
            if command -v ldd >/dev/null && ldd "${{ inputs.bin-name }}" | grep -qiE "openjph|jxl|avif|webp|turbojpeg|openjp2"; then
              echo "windows: codec DLL referenced:"; ldd "${{ inputs.bin-name }}"; exit 1
            fi
            echo "windows: no codec DLLs OK" ;;
        esac

    - name: Stage artifact (binary + LICENSE + README)
      id: stage
      shell: bash
      run: |
        set -euo pipefail
        DIR="dist/wsitools-${{ inputs.goos }}-${{ inputs.goarch }}"
        mkdir -p "$DIR"
        cp "${{ inputs.bin-name }}" "$DIR/"
        cp LICENSE "$DIR/"
        cat > "$DIR/README.txt" <<'EOF'
        wsitools — whole-slide-imaging CLI (https://github.com/WSILabs/wsitools)
        Statically-linked build. All codecs (jpeg, jpeg2000, htj2k, jpegxl, avif, webp) included.
        Run `wsitools --help` to begin.
        EOF
        echo "path=$DIR" >> "$GITHUB_OUTPUT"
```

- [ ] **Step 2: Lint the YAML**

Run:
```bash
python3 -c "import yaml,sys; yaml.safe_load(open('.github/actions/build-static/action.yml')); print('action.yml valid')"
```
Expected: `action.yml valid`.

- [ ] **Step 3: Commit**

```bash
git add .github/actions/build-static/action.yml
git commit -m "ci(release): shared build-static composite action

vcpkg bootstrap (pinned baseline) + GHA binary cache + static codec install
+ static go build + doctor smoke (asserts all 6 codecs) + per-OS static-
linkage assertion + artifact staging. Single recipe for canary + release
matrix so they cannot drift.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: release-canary.yml — linux/musl build-only (first real-CI milestone)

The cheapest end-to-end validation: run the composite on linux/amd64 musl inside an Alpine container, build-only (no upload, no notarize), on release-path changes. Green canary = vcpkg + static + smoke works in real CI on the strictest target. **musl caveat baked in:** an auto-downloaded Go toolchain is glibc and won't run on musl, so the Alpine job uses the musl-native Go from the `golang:1.26-alpine` image and sets `GOTOOLCHAIN=local`.

**Files:**
- Create: `.github/workflows/release-canary.yml`

- [ ] **Step 1: Write the canary workflow**

```yaml
name: Release canary
# Guards the STATIC/vcpkg build path before a tag is cut. ci.yml already covers
# the dynamic build + tests; this covers only what ci.yml doesn't.
on:
  pull_request:
    paths:
      - .github/workflows/release*.yml
      - .github/actions/build-static/**
      - .github/vcpkg-triplets/**
      - vcpkg.json
      - vcpkg-configuration.json
      - internal/codec/**
      - go.mod
  push:
    branches: [main]
    paths:
      - .github/workflows/release*.yml
      - .github/actions/build-static/**
      - .github/vcpkg-triplets/**
      - vcpkg.json
      - vcpkg-configuration.json
      - internal/codec/**
      - go.mod

jobs:
  canary-linux-musl:
    runs-on: ubuntu-latest
    container: golang:1.26-alpine
    env:
      GOTOOLCHAIN: local   # musl image's Go; never fetch a glibc toolchain
    steps:
      - name: Install build prerequisites (Alpine is bare)
        run: apk add --no-cache git bash cmake ninja pkgconf build-base linux-headers perl python3 zip curl
      - uses: actions/checkout@v4
      - uses: ./.github/actions/build-static
        with:
          goos: linux
          goarch: amd64
          vcpkg-triplet: x64-linux-static
          static-libc: "true"
          bin-name: wsitools
```

- [ ] **Step 2: Lint the YAML**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release-canary.yml')); print('canary valid')"
```
Expected: `canary valid`.

- [ ] **Step 3: Commit and push the branch to trigger the canary**

```bash
git add .github/workflows/release-canary.yml
git commit -m "ci(release): linux/musl build-only canary on release-path changes

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
git push -u origin feat/binary-releases
```

- [ ] **Step 4: Watch the canary run to green**

Run:
```bash
sleep 20 && gh run list --branch feat/binary-releases --workflow release-canary.yml --limit 1
RID=$(gh run list --branch feat/binary-releases --workflow release-canary.yml --limit 1 --json databaseId -q '.[0].databaseId')
gh run watch "$RID" --exit-status
```
Expected: success. **This is the load-bearing milestone** — if vcpkg-on-musl chokes on a lib (libjxl/libavif are the risks), fix-forward here per the spec's documented fallback (glibc-static on `ubuntu-latest` for the offending lib) before proceeding. Iterate: edit → commit → push → re-watch. Do not move to Task 5 until the canary is green.

---

## Task 5: release.yml — 5-target build matrix + upload (notarization stubbed)

Extend the existing notes-only `release.yml` with a matrix `build` job that reuses the composite across all five targets and uploads each archive to the Release. macOS signing is added in Task 6; here the macOS legs build + upload **unsigned** so the matrix is provable without secrets.

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Add `workflow_dispatch` + the `build` matrix job**

Append `workflow_dispatch` to the `on:` block, and gate the existing `release`
notes job to **tag pushes only** (on dispatch, `GITHUB_REF_NAME` is the branch, so
the notes job must not run — the dispatch dry-run uploads to a pre-created
prerelease instead):
```yaml
on:
  push:
    tags: ['v*']
  workflow_dispatch:
    inputs:
      ref:
        description: "tag or ref to dry-run the matrix against (uploads to a prerelease)"
        required: false
```
On the existing `release` job add:
```yaml
  release:
    if: github.event_name == 'push'   # notes job: real tags only
    # ...existing steps unchanged...
```

Add the matrix job. `needs: release` orders it after the notes job on a tag push,
but `if: always() && ...` lets it still run on dispatch (where `release` is skipped):
```yaml
  build:
    name: build ${{ matrix.goos }}/${{ matrix.goarch }}
    needs: release
    if: always() && (needs.release.result == 'success' || github.event_name == 'workflow_dispatch')
    permissions: { contents: write }
    strategy:
      fail-fast: false
      matrix:
        include:
          - { runner: ubuntu-latest,    container: "golang:1.26-alpine", goos: linux,   goarch: amd64, triplet: x64-linux-static,   static: "true",  bin: wsitools,     archive: tar }
          - { runner: ubuntu-24.04-arm, container: "golang:1.26-alpine", goos: linux,   goarch: arm64, triplet: arm64-linux-static, static: "true",  bin: wsitools,     archive: tar }
          - { runner: macos-latest,     container: "",                   goos: darwin,  goarch: arm64, triplet: arm64-osx-static,   static: "false", bin: wsitools,     archive: tar }
          - { runner: macos-13,         container: "",                   goos: darwin,  goarch: amd64, triplet: x64-osx-static,     static: "false", bin: wsitools,     archive: tar }
          - { runner: windows-latest,   container: "",                   goos: windows, goarch: amd64, triplet: x64-mingw-static,   static: "true",  bin: wsitools.exe, archive: zip }
    runs-on: ${{ matrix.runner }}
    container: ${{ matrix.container }}
    env:
      GOTOOLCHAIN: ${{ matrix.container != '' && 'local' || 'auto' }}
    steps:
      - name: Alpine prerequisites
        if: matrix.container != ''
        run: apk add --no-cache git bash cmake ninja pkgconf build-base linux-headers perl python3 zip curl

      - uses: actions/checkout@v4
        with:
          ref: ${{ github.event.inputs.ref || github.ref }}

      - name: Set up Go (non-container runners)
        if: matrix.container == ''
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Set up MSYS2 (Windows mingw toolchain)
        if: matrix.goos == 'windows'
        uses: msys2/setup-msys2@v2
        with:
          msystem: MINGW64
          install: mingw-w64-x86_64-toolchain mingw-w64-x86_64-cmake mingw-w64-x86_64-ninja

      - name: Build (static)
        id: build
        uses: ./.github/actions/build-static
        with:
          goos: ${{ matrix.goos }}
          goarch: ${{ matrix.goarch }}
          vcpkg-triplet: ${{ matrix.triplet }}
          static-libc: ${{ matrix.static }}
          bin-name: ${{ matrix.bin }}

      - name: Archive
        id: archive
        shell: bash
        run: |
          set -euo pipefail
          DIR="${{ steps.build.outputs.artifact-path }}"
          BASE="wsitools-${{ matrix.goos }}-${{ matrix.goarch }}"
          if [ "${{ matrix.archive }}" = "zip" ]; then
            (cd dist && zip -r "../$BASE.zip" "$(basename "$DIR")")
            echo "asset=$BASE.zip" >> "$GITHUB_OUTPUT"
          else
            tar -czf "$BASE.tar.gz" -C dist "$(basename "$DIR")"
            echo "asset=$BASE.tar.gz" >> "$GITHUB_OUTPUT"
          fi

      - name: Upload to release
        env:
          GH_TOKEN: ${{ github.token }}
        shell: bash
        run: |
          set -euo pipefail
          TAG="${{ github.event.inputs.ref || github.ref_name }}"
          gh release upload "$TAG" "${{ steps.archive.outputs.asset }}" --clobber --repo "$GITHUB_REPOSITORY"
```

Note: on Windows the mingw toolchain must be on PATH for vcpkg's `x64-mingw-static`; `msys2/setup-msys2` adds it. If vcpkg can't locate the mingw compiler, set `CC`/`CXX` to the MSYS2 gcc in a follow-up step (documented fallback in the spec).

- [ ] **Step 2: Lint the YAML**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml')); print('release valid')"
```
Expected: `release valid`.

- [ ] **Step 3: Commit and push**

```bash
git add .github/workflows/release.yml
git commit -m "ci(release): 5-target static build matrix + upload (unsigned mac)

Adds a build job that reuses build-static across linux amd64+arm64 (musl),
darwin arm64+amd64, windows amd64 (mingw); archives + uploads each to the
release. workflow_dispatch dry-runs the matrix. macOS signing added next.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
git push
```

- [ ] **Step 4: Dry-run the full matrix via workflow_dispatch into a prerelease**

Create a throwaway prerelease tag's worth of run (the dispatch uploads to the tag in `ref`; use a `-rc` prerelease so production users never see it):
```bash
gh release create v0.0.0-canary --prerelease --notes "matrix dry-run; do not use" --target feat/binary-releases || true
gh workflow run release.yml --ref feat/binary-releases -f ref=v0.0.0-canary
sleep 25
RID=$(gh run list --workflow release.yml --limit 1 --json databaseId -q '.[0].databaseId')
gh run watch "$RID" --exit-status || true
gh release view v0.0.0-canary --json assets -q '.assets[].name'
```
Expected: five archives (`wsitools-{linux-amd64,linux-arm64,darwin-arm64,darwin-amd64,windows-amd64}.{tar.gz,zip}`) attached. Fix-forward any failing leg (windows/openjph is the likeliest per the spec). Keep `v0.0.0-canary` for Task 6/7 verification; delete it at the end of Task 8.

---

## Task 6: macOS sign + notarize (conditional on secrets)

Add Developer-ID signing + notarization to the two macOS legs, gated so the matrix still builds (unsigned) when secrets are absent (forks/contributors). Requires the maintainer to provision the secrets named below.

**Files:**
- Modify: `.github/workflows/release.yml` (macOS-only steps in the `build` job)

- [ ] **Step 1: Add a conditional sign+notarize step before Archive (macOS only)**

Insert into the `build` job, after `Build (static)` and before `Archive`:
```yaml
      - name: Sign + notarize (macOS, if secrets present)
        if: matrix.goos == 'darwin' && env.MACOS_CERT_P12_BASE64 != ''
        env:
          MACOS_CERT_P12_BASE64:      ${{ secrets.MACOS_CERT_P12_BASE64 }}
          MACOS_CERT_PASSWORD:        ${{ secrets.MACOS_CERT_PASSWORD }}
          MACOS_NOTARY_KEY_P8_BASE64: ${{ secrets.MACOS_NOTARY_KEY_P8_BASE64 }}
          MACOS_NOTARY_KEY_ID:        ${{ secrets.MACOS_NOTARY_KEY_ID }}
          MACOS_NOTARY_ISSUER_ID:     ${{ secrets.MACOS_NOTARY_ISSUER_ID }}
        run: |
          set -euo pipefail
          BIN="${{ steps.build.outputs.artifact-path }}/wsitools"
          # import cert into a temporary keychain
          KEYCHAIN="$RUNNER_TEMP/sign.keychain-db"
          security create-keychain -p "" "$KEYCHAIN"
          security set-keychain-settings "$KEYCHAIN"
          security unlock-keychain -p "" "$KEYCHAIN"
          echo "$MACOS_CERT_P12_BASE64" | base64 -d > "$RUNNER_TEMP/cert.p12"
          security import "$RUNNER_TEMP/cert.p12" -k "$KEYCHAIN" -P "$MACOS_CERT_PASSWORD" -T /usr/bin/codesign
          security set-key-partition-list -S apple-tool:,apple: -s -k "" "$KEYCHAIN" >/dev/null
          security list-keychains -d user -s "$KEYCHAIN" $(security list-keychains -d user | tr -d '"')
          IDENTITY=$(security find-identity -v -p codesigning "$KEYCHAIN" | awk '/Developer ID Application/{print $2; exit}')
          codesign --force --options runtime --timestamp --sign "$IDENTITY" "$BIN"
          codesign --verify --strict --verbose=2 "$BIN"
          # notarize
          echo "$MACOS_NOTARY_KEY_P8_BASE64" | base64 -d > "$RUNNER_TEMP/key.p8"
          ditto -c -k "${{ steps.build.outputs.artifact-path }}" "$RUNNER_TEMP/notarize.zip"
          xcrun notarytool submit "$RUNNER_TEMP/notarize.zip" \
            --key "$RUNNER_TEMP/key.p8" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID" \
            --wait
          xcrun stapler staple "$BIN"
          xcrun stapler validate "$BIN"

      - name: Note unsigned macOS build
        if: matrix.goos == 'darwin' && env.MACOS_CERT_P12_BASE64 == ''
        env:
          MACOS_CERT_P12_BASE64: ${{ secrets.MACOS_CERT_P12_BASE64 }}
        run: echo "::warning::macOS signing secrets absent — uploading UNSIGNED binary (Gatekeeper will quarantine)."
```

Note: stapling a bare CLI executable (not an app bundle) staples the ticket into the binary's extended attributes; `stapler validate` confirms. If stapling a raw Mach-O is rejected by a future `stapler`, the documented fallback is to staple the `.tar.gz`-wrapped artifact instead — captured in `docs/RELEASING.md` (Task 7).

- [ ] **Step 2: Lint the YAML**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml')); print('release valid')"
```
Expected: `release valid`.

- [ ] **Step 3: Commit and push**

```bash
git add .github/workflows/release.yml
git commit -m "ci(release): macOS Developer-ID sign + notarize (secret-gated)

Imports the Developer ID cert into a temp keychain, codesigns with hardened
runtime + timestamp, notarizes via notarytool (App Store Connect API key),
staples. Skips with a warning when secrets are absent so fork/contributor
builds still produce an unsigned artifact.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
git push
```

- [ ] **Step 4: Verify (secret-dependent)**

If the maintainer has added the secrets, re-run the dispatch dry-run (Task 5 Step 4) and confirm the macOS legs log notarization acceptance and `stapler validate` passes. If secrets are not yet provisioned, confirm instead that the macOS legs log the `::warning::` and still upload an unsigned artifact (matrix stays green). Record which case held.

---

## Task 7: checksums job + release-notes codec matrix + docs

Add the `SHA256SUMS` aggregation job, document the per-platform codec matrix in the release body, and write the maintainer runbook + README install section.

**Files:**
- Modify: `.github/workflows/release.yml` (add `checksums` job; extend notes)
- Create: `docs/RELEASING.md`
- Modify: `README.md`

- [ ] **Step 1: Add the `checksums` job**

Append to `release.yml`:
```yaml
  checksums:
    name: SHA256SUMS
    needs: build
    runs-on: ubuntu-latest
    permissions: { contents: write }
    steps:
      - env: { GH_TOKEN: "${{ github.token }}" }
        shell: bash
        run: |
          set -euo pipefail
          TAG="${{ github.event.inputs.ref || github.ref_name }}"
          mkdir dl && cd dl
          gh release download "$TAG" --repo "$GITHUB_REPOSITORY" --pattern 'wsitools-*'
          sha256sum wsitools-* > SHA256SUMS
          cat SHA256SUMS
          gh release upload "$TAG" SHA256SUMS --clobber --repo "$GITHUB_REPOSITORY"
```

- [ ] **Step 2: Append the codec matrix + Gatekeeper note to the release body**

Add a step to the existing `release` job (after "Create or update release") that appends a static footer to the notes file, OR document it once in `docs/RELEASING.md` and add a fixed line to `release_notes.md` generation. Concretely, in the `release` job's notes step, after writing `release_notes.md`, append:
```bash
cat >> release_notes.md <<'EOF'

---
### Prebuilt binaries
All targets include every codec (jpeg, jpeg2000, htj2k, jpegxl, avif, webp).

| Target | Asset |
|---|---|
| Linux x86-64 | `wsitools-linux-amd64.tar.gz` |
| Linux arm64  | `wsitools-linux-arm64.tar.gz` |
| macOS Apple Silicon | `wsitools-darwin-arm64.tar.gz` |
| macOS Intel  | `wsitools-darwin-amd64.tar.gz` |
| Windows x86-64 | `wsitools-windows-amd64.zip` |

Verify with `sha256sum -c SHA256SUMS`. macOS binaries are signed + notarized;
if you see a Gatekeeper prompt on an unsigned dry-run build, run
`xattr -d com.apple.quarantine wsitools`.
EOF
```

- [ ] **Step 3: Write `docs/RELEASING.md`**

```markdown
# Releasing wsitools

## Prerequisites (one-time)
Add these GitHub repo secrets for signed+notarized macOS binaries:
- `MACOS_CERT_P12_BASE64` — `base64 -i DeveloperIDApp.p12`
- `MACOS_CERT_PASSWORD` — the .p12 export password
- `MACOS_NOTARY_KEY_P8_BASE64` — `base64 -i AuthKey_XXXX.p8` (App Store Connect API key)
- `MACOS_NOTARY_KEY_ID` — the key's Key ID
- `MACOS_NOTARY_ISSUER_ID` — the key's Issuer ID

Without them the macOS legs still build but upload UNSIGNED binaries.

## Cut a release
1. Update `CHANGELOG.md` with a `## [X.Y.Z] - DATE` section.
2. Bump `Version` in `cmd/wsitools/version.go`.
3. Tag: `git tag -a vX.Y.Z -m "vX.Y.Z — summary" && git push origin vX.Y.Z`.
4. `release.yml` builds the 5-target matrix, signs/notarizes macOS, uploads
   archives + `SHA256SUMS`.

## Dry-run before a real tag
`gh workflow run release.yml --ref <branch> -f ref=vX.Y.Z-rc1` (make the rc a
prerelease first). Inspect the attached assets, download on a clean machine, run.

## Troubleshooting
- **musl + libjxl/libavif build fails:** fall back to glibc-static on
  `ubuntu-latest` for that lib (drop the Alpine container on the linux legs).
- **Windows openjph (mingw) fails in vcpkg:** build OpenJPH from source in that
  leg, or set `CC/CXX` to the MSYS2 gcc before vcpkg install.
- **stapler rejects a bare Mach-O:** staple the `.tar.gz` artifact instead.
```

- [ ] **Step 4: Add a README install section**

Add under a top-level "Install" heading in `README.md`:
```markdown
## Install

### Prebuilt binaries (recommended)
Download the archive for your platform from the [latest release](https://github.com/WSILabs/wsitools/releases/latest), extract, and run `wsitools`. All binaries are statically linked and include every codec (jpeg, jpeg2000, htj2k, jpegxl, avif, webp). Verify integrity with `sha256sum -c SHA256SUMS`.

| Platform | Asset |
|---|---|
| Linux x86-64 / arm64 | `wsitools-linux-{amd64,arm64}.tar.gz` |
| macOS Apple Silicon / Intel | `wsitools-darwin-{arm64,amd64}.tar.gz` |
| Windows x86-64 | `wsitools-windows-amd64.zip` |

### From source
Requires Go 1.26+ and the codec C libraries (`brew install jpeg-turbo openjpeg jpeg-xl libavif webp openjph`); then `make build` or `go install ./cmd/wsitools`.
```

- [ ] **Step 5: Lint + commit**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml')); print('release valid')"
git add .github/workflows/release.yml docs/RELEASING.md README.md
git commit -m "ci(release): SHA256SUMS job + codec-matrix notes + RELEASING/README docs

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
git push
```

- [ ] **Step 6: Re-run the dispatch dry-run and verify checksums + notes**

```bash
gh workflow run release.yml --ref feat/binary-releases -f ref=v0.0.0-canary
sleep 25
RID=$(gh run list --workflow release.yml --limit 1 --json databaseId -q '.[0].databaseId')
gh run watch "$RID" --exit-status
gh release download v0.0.0-canary --pattern 'SHA256SUMS' -O - | head
```
Expected: matrix + checksums jobs green; `SHA256SUMS` lists all five archives.

---

## Task 8: Final verification + cleanup

**Files:** none (verification + teardown)

- [ ] **Step 1: Download one artifact per OS family on a clean path and run it**

```bash
TMP=$(mktemp -d); cd "$TMP"
gh release download v0.0.0-canary -R WSILabs/wsitools --pattern 'wsitools-darwin-arm64.tar.gz'
tar -xzf wsitools-darwin-arm64.tar.gz
./wsitools-darwin-arm64/wsitools doctor   # all six codecs, runs with no external libs installed
```
Expected: runs and lists all six codecs on a machine without the codec libs installed (proves self-containment). (Linux/Windows artifacts are verified in-CI by the linkage assertion; spot-check locally if hardware available.)

- [ ] **Step 2: Confirm the canary fired on this branch's CI and is green**

```bash
gh run list --branch feat/binary-releases --workflow release-canary.yml --limit 1
```
Expected: latest canary run = success.

- [ ] **Step 3: Delete the throwaway dry-run release + tag**

```bash
gh release delete v0.0.0-canary -R WSILabs/wsitools --yes --cleanup-tag
```
Expected: release + tag removed.

- [ ] **Step 4: Final review**

Dispatch a code-review subagent over the whole branch diff (`git diff main...feat/binary-releases`): check the composite action, both workflows, the htj2k change, vcpkg manifest/triplets, and docs for correctness, secret-handling hygiene (no secret echoed to logs), and spec conformance. Address findings, then proceed to finishing-a-development-branch.

---

## Self-Review notes (author)

- **Spec coverage:** htj2k fix (Component 2 → Task 1); vcpkg static deps (Component 1 → Task 2); shared build recipe + smoke + linkage (Component 3 → Task 3); canary trigger (Component 5 → Task 4); 5-target matrix + upload (Deliverables + Component 5 → Task 5); macOS sign+notarize (Component 4 → Task 6); checksums + codec-matrix notes + secrets runbook (Deliverables + Components 4/5 → Task 7); end-to-end self-containment verification (Verification → Task 8). All spec sections map to a task.
- **Version is a `const`** (`cmd/wsitools/version.go`), not ldflags-injected — the released binary reports the compiled-in value; no `-X` injection attempted (RELEASING.md step bumps it before tagging).
- **musl toolchain trap** (glibc auto-toolchain won't run on musl) handled via `golang:1.26-alpine` image + `GOTOOLCHAIN=local`.
- **Iterate-in-CI reality:** Tasks 4–7 each push and watch a real run; the canary (Task 4) and windows/openjph (Task 5) are the expected fix-forward points, with fallbacks documented in `docs/RELEASING.md`.
