# Static, Self-Contained Binary Releases — Design

**Date:** 2026-06-26
**Status:** Approved (brainstorming) — ready for implementation plan
**Topic:** Attach statically-linked, download-and-run `wsitools` binaries for 5 OS/arch
targets to every `vX.Y.Z` GitHub Release.

---

## Problem

wsitools ships no binary artifacts. The existing `release.yml` is **notes-only**:
on a `v*` tag it turns the matching `CHANGELOG.md` section into a GitHub Release
and stops there. A comment in that workflow records why: *"the codec cgo deps make
cross-platform binaries a separate concern."*

That concern is real. wsitools is **not** a pure-Go binary. The codec layer links
six C libraries — five via `pkg-config`, one (htj2k/OpenJPH) via hardcoded
`/opt/homebrew` paths:

| Codec | Links | Build tag |
|---|---|---|
| jpeg | libjpeg-turbo | mandatory |
| jp2k | libopenjp2 | `!nojp2k` |
| jpegxl | libjxl + libjxl_threads | `!nojxl` |
| avif | libavif | `!noavif` |
| webp | libwebp | `!nowebp` |
| htj2k | libopenjph (+ `-lstdc++`, C++17) | `!nohtj2k` |

Two consequences fix the whole shape of this design:

1. **No cross-compilation.** cgo means you cannot `GOOS=… go build` a target from a
   foreign host without a full cross-toolchain *and* cross-built C libs. Every target
   must build on a **native runner**.
2. **A naive build isn't portable.** Linking the system `.dylib`/`.so`/`.dll`
   dynamically forces the downloader to install the six codec libs at matching ABI —
   a poor "download and run" story. The binaries must be **statically linked**.

## Goal

Every `vX.Y.Z` tag attaches, to the same GitHub Release the notes job creates,
download-and-run binaries for **5 targets**, each with **all six codecs** statically
linked, macOS **signed + notarized**, plus a `SHA256SUMS` manifest.

## Non-goals

- Package-manager distribution (Homebrew tap, apt/deb, winget, Scoop). Future work.
- Cross-compilation. Explicitly rejected above — native runners only.
- Changing the from-source build story (`make build`, `go install`) — unchanged.
- A Linux/arm64 fallback via QEMU — the repo is **public**, so free native
  `ubuntu-24.04-arm` runners are available; QEMU is not needed.

---

## Deliverables

Per-target asset on the GitHub Release:

| Asset | Runner | Arch | Codecs |
|---|---|---|---|
| `wsitools-linux-amd64.tar.gz` | `ubuntu-latest` + Alpine/musl container | x86-64 | all 6 |
| `wsitools-linux-arm64.tar.gz` | `ubuntu-24.04-arm` + Alpine/musl container | arm64 | all 6 |
| `wsitools-darwin-arm64.tar.gz` | `macos-latest` | arm64 | all 6 |
| `wsitools-darwin-amd64.tar.gz` | `macos-13` | x86-64 | all 6 |
| `wsitools-windows-amd64.zip` | `windows-latest` (mingw) | x86-64 | all 6 |
| `SHA256SUMS` | final aggregation job | — | — |

Each archive contains: the `wsitools` binary (`.exe` on Windows), `LICENSE`, and a
short `README.txt` (one-paragraph "what this is" + the codec matrix). The binary
already self-reports build metadata via `wsitools version`.

**Portability targets:**
- **Linux:** built under **musl/Alpine** and fully static (`-extldflags "-static"`),
  so one binary runs on *any* distro — no glibc-version coupling. A file-only CLI
  uses no NSS/`getaddrinfo`, so musl-static has no downside here.
- **macOS:** "static codec libs + dynamic system frameworks." Apple forbids a
  fully-static binary (no static libSystem/crt), but the system libs are guaranteed
  present on every Mac; only the *codec* libs need to be static.
- **Windows:** mingw-static (`-static`), including the C++ runtime for OpenJPH.

---

## Component 1 — Static dependencies via vcpkg

A single `vcpkg.json` manifest at the repo root pins the six libraries:

```json
{
  "dependencies": [
    "libjpeg-turbo", "openjpeg", "libjxl", "libavif", "libwebp", "openjph"
  ]
}
```

Each runner installs deps with a **static triplet** so vcpkg emits `.a` archives
plus pkg-config (`.pc`) files that cgo consumes unchanged:

| Platform | Triplet |
|---|---|
| Linux amd64 (musl) | `x64-linux` (in Alpine container; musl is the container's libc) |
| Linux arm64 (musl) | `arm64-linux` (in Alpine container) |
| macOS arm64 | `arm64-osx` |
| macOS amd64 | `x64-osx` |
| Windows amd64 | `x64-mingw-static` |

One recipe across all platforms instead of three bespoke dependency scripts.
vcpkg's GitHub Actions **binary cache** (`actions/cache` or the built-in
`X-GitHub-Actions` provider) amortizes the build so repeat runs are fast.

The build exports `PKG_CONFIG_PATH` to vcpkg's static `.pc` directory and builds with
`CGO_ENABLED=1`. cgo's existing `#cgo pkg-config:` directives resolve against the
vcpkg `.pc` files with no per-codec edits — **except htj2k** (Component 2).

## Component 2 — htj2k cgo path fix (the one source change)

`internal/codec/htj2k/htj2k.go` currently hardcodes Homebrew paths:

```go
#cgo CXXFLAGS: -I/opt/homebrew/include -std=c++17
#cgo LDFLAGS: -L/opt/homebrew/lib -lopenjph -lstdc++
```

Replace the path-specific flags with pkg-config discovery, keeping the C++17 dialect:

```go
#cgo CXXFLAGS: -std=c++17
#cgo pkg-config: openjph
```

vcpkg emits `openjph.pc`. If that `.pc` does not pull in the C++ standard library on
a given linker, retain an explicit `#cgo LDFLAGS: -lstdc++` (Linux/Windows) /
`-lc++` (macOS) guarded as needed — to be confirmed empirically during
implementation; the plan must verify the link on each platform, not assume.

Side benefit: this fixes htj2k on Intel Macs (`/usr/local`) and any non-Homebrew
build environment. The change must first be proven non-regressing against the
current local/CI build (Homebrew still provides `openjph.pc`? verify; if not, the
local dev story needs a `PKG_CONFIG_PATH` note in CONTRIBUTING/Makefile).

## Component 3 — Build + per-target smoke test

Each runner builds:

```
CGO_ENABLED=1 PKG_CONFIG_PATH=<vcpkg-static-pc> \
  go build -trimpath -ldflags "-s -w <-extldflags '-static' on linux/windows>" \
  -o wsitools<.exe> ./cmd/wsitools
```

All six codec tags **on** — no `nohtj2k` anywhere.

After build, a **smoke test proves the artifact is complete** on that target. Note
the strongest guarantee is free: cgo resolves every `#cgo pkg-config:` symbol at
**link time**, so a codec whose static lib is missing or broken makes `go build`
itself fail — a successful build already proves all six linked. The smoke test adds
registration + runtime confirmation on top:

1. `wsitools version` runs clean (binary launches on the target — catches a
   bad-arch or quarantine problem on the runner itself).
2. `wsitools doctor` output **lists all six codecs** (`jpeg`, `jpeg2000`, `htj2k`,
   `jpegxl`, `avif`, `webp`). `doctor` enumerates `codec.List()`, i.e. the
   build-tag-registered codecs — so a dropped tag (e.g. an accidental `nohtj2k`)
   surfaces here even though the build succeeded. The job greps for all six names
   and fails if any is absent.

This smoke test is **fixture-free** — it does not depend on the `wsi-fixtures`
download, keeping the release workflow self-contained. A deeper actual-encode
round-trip per codec already lives in the main CI suite (run on the same tag push
in parallel); the release job does not duplicate it.

## Component 4 — macOS signing + notarization

The two macOS jobs, after a successful build + smoke test:

1. `codesign --force --options runtime --timestamp --sign "$DEV_ID_APP" wsitools`
2. zip, then `xcrun notarytool submit out.zip --wait` with App Store Connect creds
3. `xcrun stapler staple` the result, re-archive as the release `.tar.gz`

**Required GitHub repo secrets** (provisioned by the maintainer):

| Secret | Purpose |
|---|---|
| `MACOS_CERT_P12_BASE64` | Developer ID Application cert, base64-encoded `.p12` |
| `MACOS_CERT_PASSWORD` | password for that `.p12` |
| `MACOS_NOTARY_KEY_P8_BASE64` | App Store Connect API key (`.p8`), base64 |
| `MACOS_NOTARY_KEY_ID` | the API key's Key ID |
| `MACOS_NOTARY_ISSUER_ID` | the API key's Issuer ID |

(App Store Connect API key is preferred over Apple-ID + app-specific-password; the
plan may use either, but the spec standardizes on the API key.)

The signing/notarization steps **no-op gracefully when the secrets are absent** (e.g.
on fork PRs or `workflow_dispatch` dry-runs from a contributor) so the macOS *build*
still succeeds and produces an unsigned artifact — only the notarize step is skipped,
with a logged warning.

## Component 5 — Workflow integration

Two workflow files plus a shared build recipe. The expensive 5-target matrix lives in
`release.yml` (tag-triggered); a one-target build-only **canary** lives in
`release-canary.yml` (release-path PRs); both invoke the **same** vcpkg + static-build
+ smoke-test steps via a composite action / reusable workflow so they can't drift.

Extend the existing `.github/workflows/release.yml`:

- **`release` job** (existing, unchanged): create/update the GitHub Release from the
  `CHANGELOG.md` section + annotated-tag title; mark `-rc*` as prerelease.
- **`build` job** (new): `strategy.matrix` over the 5 targets →
  checkout → vcpkg deps (cached) → build → smoke-test → (macOS: sign + notarize) →
  archive → `gh release upload "$TAG" <asset>`. Depends on `release` so the Release
  exists first.
- **`checksums` job** (new): `needs: build`; download all assets, compute
  `SHA256SUMS`, upload it.

**Triggering (release):** keep `release.yml` on `push: tags: ['v*']`, and add
`workflow_dispatch` with an input (e.g. a target ref/tag) so the **full matrix** can
be **dry-run** — uploading to a *draft* or *prerelease* — before it is trusted on a
real tag. Releasing (attach assets + notarize + publish) is **always tag-only**.

**Triggering (canary):** a separate, lightweight workflow —
`.github/workflows/release-canary.yml` — guards the *static/vcpkg build path* (the
new fragile surface: musl quirks, mingw/openjph, triplet drift) **before** a tag is
ever cut. It runs on `pull_request` and `push` **filtered to release-relevant
paths**:

```yaml
on:
  pull_request:
    paths: [.github/workflows/release*.yml, vcpkg.json,
            internal/codec/**, go.mod]
  push:
    branches: [main]
    paths: [.github/workflows/release*.yml, vcpkg.json,
            internal/codec/**, go.mod]
```

It builds **one representative target only — `linux/amd64` (musl/Alpine)** — through
the *same* vcpkg + static-build + smoke-test steps as the release matrix, but **does
not notarize and does not upload anything**. One runner's time, only when a
release-relevant file changes. Rationale: the regular `ci.yml` already covers the
*dynamic* build + full test suite on every push/PR; this canary covers only what
`ci.yml` doesn't — that the *static* release build still links and produces a
complete binary. The linux/musl target is the most representative single canary
because it is the strictest (fully static, the libjxl/libavif-on-musl risk lives
here). Reusing shared steps (a composite action or reusable workflow) keeps the
canary and the release matrix from drifting apart.

**Release notes** gain a short, documented **per-platform codec matrix** (all six on
every target, per the decisions here) and a macOS Gatekeeper note (moot once
notarized, retained for any unsigned dry-run artifacts).

## Verification / testing

- **Per-target smoke test** (Component 3) is the primary in-CI gate: a successful
  static build (link-time proof all six libs resolved) + `doctor` listing all six
  codecs on the real artifact. Deeper per-codec encode round-trips run in the
  parallel main-CI suite, not here.
- **Static-linkage assertion:** on Linux, `ldd wsitools` reports "not a dynamic
  executable" (musl-static); on macOS, `otool -L` lists only `/usr/lib/*` and
  `/System/*` system libs (no vcpkg/Homebrew paths); on Windows, `ldd`/Dependencies
  shows no mingw codec DLLs. Each is asserted in the job.
- **macOS notarization** is self-verifying: `notarytool --wait` returns the accepted
  status, and `stapler validate` confirms the ticket.
- **Dry-run before first real tag:** exercise the whole matrix via
  `workflow_dispatch` into a prerelease, download each artifact on a clean machine,
  and confirm it runs, before cutting a production tag.

## Risks

1. **vcpkg cold-build time** (15–30 min/runner on a cache miss; openjph + libjxl are
   the slow ones). Mitigation: vcpkg binary caching keyed on the manifest + triplet.
2. **musl + libjxl/libavif** occasionally assume glibc. Mitigation: Alpine container
   is the controlled env; documented fallback is glibc-static on `ubuntu-latest` for
   any lib that refuses musl (the binary is then "mostly static" but still has no
   codec-lib runtime deps).
3. **OpenJPH static on Windows/mingw via vcpkg** is the least-trodden path. Mitigation:
   if `x64-mingw-static` can't build openjph, fall back to building OpenJPH from source
   in that one job (it's CMake/C++17). htj2k stays on per the decision; this is a
   build-mechanics fallback, not a scope change.
4. **Apple notary flakiness / queue latency.** `notarytool --wait` handles transient
   waits; a failed submission fails the macOS job without poisoning the other targets
   (matrix `fail-fast: false`).

## Decisions locked in brainstorming

- **Targets:** the 5 mainstream OS/arch combos (linux amd64+arm64, darwin arm64+amd64,
  windows amd64).
- **macOS:** sign **and** notarize (maintainer provisions an Apple Developer ID).
- **htj2k:** kept on **all five** targets (build OpenJPH static everywhere incl.
  Windows; fix the hardcoded path).
- **Orchestration:** hand-rolled GitHub Actions matrix extending `release.yml` — not
  goreleaser (its single-host cross-compile model fights cgo + per-platform static).
- **Static deps:** **vcpkg** static triplets, uniform across platforms.
- **Linux libc:** **musl/Alpine** for maximum "runs anywhere" portability.
- **Triggers:** full matrix builds + releases **tag-only** (`v*`) + `workflow_dispatch`
  dry-run; a one-target (`linux/amd64` musl) **build-only canary** runs on PRs/pushes
  touching release-relevant paths, to catch static-build rot before a tag is cut.
