# Releasing wsitools

`.github/workflows/release.yml` runs on a `vX.Y.Z` tag push: it creates the
GitHub Release (notes from the matching `CHANGELOG.md` section), builds
statically-linked binaries for 5 targets, signs + notarizes the macOS ones,
and attaches them plus a `SHA256SUMS` manifest.

## Targets

| Asset | Runner | Notes |
|---|---|---|
| `wsitools-linux-amd64.tar.gz` | `ubuntu-latest` | glibc, static codecs (mostly-static) |
| `wsitools-linux-arm64.tar.gz` | `ubuntu-24.04-arm` | glibc, static codecs |
| `wsitools-darwin-arm64.tar.gz` | `macos-latest` | native arm64 |
| `wsitools-darwin-amd64.tar.gz` | `macos-latest` | **cross-compiled** to x86_64 (no Intel runner) |
| `wsitools-windows-amd64.zip` | `windows-latest` | mingw, fully static |

All six codecs (jpeg, jpeg2000, htj2k, jpegxl, avif, webp) are included on every
target. The codec C libraries are built statically by **vcpkg** (`vcpkg.json` +
`.github/vcpkg-triplets/`); the shared recipe lives in
`.github/actions/build-static`.

**AVIF needs an explicit AV1 encoder.** vcpkg's `libavif` port has no default
features, so a bare `"libavif"` dependency builds libavif with *no* AV1 codec —
`avifEncoderWrite` then returns `NO_CODEC_AVAILABLE` and every AVIF encode fails
(this was the Windows AVIF bug, wsitools#34). `vcpkg.json` therefore pins
`{"name":"libavif","default-features":false,"features":["aom"]}`. `aom` is the
only AV1 encoder the port exposes at our baseline (`e287d598…` → libavif 1.4.2;
no `svt-av1`/`rav1e` feature there — those would need a baseline bump). `aom`
provides both encode and decode, so it fixes AVIF encode *and* decode. It is a
heavy dependency (build time + binary size on every target); confirm a
`doctor` `✓ avif` on the built binaries after any libavif/baseline change.

## One-time setup — macOS signing secrets

Add these GitHub repo secrets for signed + notarized macOS binaries. Without
them the macOS legs still build but ship **unsigned** (a `::warning::` is logged;
users would need `xattr -d com.apple.quarantine wsitools`).

| Secret | How to produce it |
|---|---|
| `MACOS_CERT_P12_BASE64` | Export your *Developer ID Application* cert + key as `.p12`, then `base64 -i cert.p12 \| pbcopy` |
| `MACOS_CERT_PASSWORD` | The password you set on the `.p12` export |
| `MACOS_NOTARY_KEY_P8_BASE64` | App Store Connect API key: `base64 -i AuthKey_XXXX.p8 \| pbcopy` |
| `MACOS_NOTARY_KEY_ID` | The API key's Key ID (App Store Connect → Users and Access → Integrations → Keys) |
| `MACOS_NOTARY_ISSUER_ID` | The Issuer ID shown on the same page |

The API key needs the *Developer* role (sufficient for notarization).

## Cut a release

1. Add a `## [X.Y.Z] - YYYY-MM-DD` section to `CHANGELOG.md`.
2. Bump `Version` in `cmd/wsitools/version.go` (it is a compiled-in `const`, so
   the binary reports whatever is committed at tag time — no ldflags injection).
3. Tag and push:
   ```sh
   git tag -a vX.Y.Z -m "vX.Y.Z — short summary"
   git push origin vX.Y.Z
   ```
4. Watch the **Release** workflow: `release` (notes) → `build` (5-target matrix,
   sign/notarize) → `checksums`. The CI suite runs on the same tag in parallel.

## Dry-run before a real tag

The `release-canary` workflow already guards the static build on every
release-path PR (linux only). To exercise the **full 5-target matrix** before a
production tag, push a throwaway prerelease tag (the `-` marks it prerelease):

```sh
git tag -a v0.0.0-rc.test -m "dry-run"
git push origin v0.0.0-rc.test
# …inspect the attached assets, download + run on a clean machine…
gh release delete v0.0.0-rc.test --yes --cleanup-tag
```

(`workflow_dispatch` is also defined, but only works once this workflow is on the
default branch.)

## Troubleshooting

- **Linux build fails in vcpkg:** the legs use glibc on ubuntu (not musl/Alpine,
  which broke on vcpkg's glibc-cmake download and Alpine's samurai-not-ninja).
  Keep them on ubuntu runners.
- **Windows `pkg-config` "Can't find …Strawberry…pkg-config.bat":** cgo found the
  Strawberry stub. The matrix prepends the real MSYS2 `mingw64/bin` and sets
  `PKG_CONFIG=pkgconf`; ensure `setup-msys2` installed `mingw-w64-x86_64-pkgconf`.
- **darwin/amd64 is x86_64?** It is cross-built on the arm64 runner via
  `CGO_*FLAGS=-arch x86_64`; the vcpkg `x64-osx-static` triplet pins
  `VCPKG_OSX_ARCHITECTURES=x86_64`. The smoke test runs it under Rosetta.
- **Notarization rejected / stapler fails on a bare Mach-O:** staple the
  `.tar.gz` artifact instead of the raw binary.
- **vcpkg cold build is slow (~15–25 min/leg):** `actions/cache` persists the
  vcpkg binary cache keyed on the triplet + `vcpkg.json`; warm runs are fast.
