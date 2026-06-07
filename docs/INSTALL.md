# Installing wsitools (with all codecs)

wsitools is built from source with Go. Its image codecs are C libraries linked
via cgo, so you install those libraries first, then build. This guide covers a
**full-codec** install (JPEG, JPEG 2000, JPEG XL, AVIF, WebP, HTJ2K) on macOS,
Linux, and Windows.

> **Why from source?** The codec libraries are linked at build time against your
> machine's libraries. A prebuilt binary would only run where those exact
> libraries live, so building locally is the reliable way to get a fast, native
> binary with every codec. The build is a one-liner once the libraries are
> present.

## Requirements (all platforms)

- **Go 1.26+** (see [go.dev/dl](https://go.dev/dl/)). cgo must be enabled
  (`CGO_ENABLED=1`, the default).
- A C/C++ toolchain (clang or gcc) and **pkg-config**.
- Codec libraries (provide these `pkg-config` modules):

  | Codec | pkg-config module(s) | Required? | Opt-out build tag |
  |---|---|---|---|
  | JPEG (libjpeg-turbo) | `libturbojpeg`, `libjpeg` | **yes** | — |
  | JPEG 2000 (OpenJPEG) | `libopenjp2` | **yes** | — |
  | JPEG XL | `libjxl`, `libjxl_threads` | optional | `nojxl` |
  | AVIF | `libavif` | optional | `noavif` |
  | WebP | `libwebp` | optional | `nowebp` |
  | HTJ2K (OpenJPH) | `openjph` | optional | `nohtj2k` |

The two required codecs cover Aperio SVS, the TIFF family, and DICOM
JPEG/JPEG2000 — i.e. most real-world WSI. The optional codecs are **opt-out**:
by default the build expects all of them, so install everything below for a
full build, or skip a library and pass its `-tags no<codec>` flag (see
[Skipping a codec](#skipping-a-codec)).

---

## macOS (Homebrew)

```sh
brew install go pkg-config jpeg-turbo openjpeg libtiff jpeg-xl libavif webp openjph
go install github.com/wsilabs/wsitools/cmd/wsitools@latest
```

`go install` places the binary in `$(go env GOBIN)` (or `$(go env GOPATH)/bin`);
add that to your `PATH`.

> **If the build can't find `libturbojpeg`:** `jpeg-turbo` is keg-only on
> Homebrew, so its `pkg-config` files aren't on the default path. Export it and
> rebuild:
> ```sh
> export PKG_CONFIG_PATH="$(brew --prefix jpeg-turbo)/lib/pkgconfig:$PKG_CONFIG_PATH"
> ```

This is the exact dependency set wsitools' CI builds and tests against, so the
full-codec build is known-good on macOS.

---

## Linux

Package names vary by distribution and version; install the toolchain,
`pkg-config`, and the codec `-dev`/`-devel` packages. **OpenJPH (HTJ2K) is not
packaged on most distros** — build it from source (below) or skip it with
`-tags nohtj2k`.

**Debian / Ubuntu:**

```sh
sudo apt update
sudo apt install -y build-essential pkg-config \
  libjpeg-dev libturbojpeg0-dev libopenjp2-7-dev libtiff-dev \
  libjxl-dev libavif-dev libwebp-dev
# Install Go 1.26+ from https://go.dev/dl/ (distro packages are often older).
```

**Fedora / RHEL:**

```sh
sudo dnf install -y gcc gcc-c++ pkgconf-pkg-config \
  libjpeg-turbo-devel turbojpeg-devel openjpeg2-devel libtiff-devel \
  libjxl-devel libavif-devel libwebp-devel
```

Then verify the required modules resolve and build:

```sh
pkg-config --exists libturbojpeg libopenjp2 && echo "core codecs OK"
go install github.com/wsilabs/wsitools/cmd/wsitools@latest
```

If `libjxl-dev`/`libjxl-devel` is unavailable on an older release, either
upgrade the distro, build libjxl from source, or skip it with `-tags nojxl`.

### OpenJPH (HTJ2K) from source

```sh
git clone https://github.com/aous72/OpenJPH
cd OpenJPH && mkdir build && cd build
cmake .. -DCMAKE_BUILD_TYPE=Release
make -j"$(nproc)"
sudo make install && sudo ldconfig
pkg-config --exists openjph && echo "openjph OK"
```

Once `openjph` resolves, a plain `go install …@latest` (no `nohtj2k`) includes
HTJ2K. Without it, build with `-tags nohtj2k`.

---

## Windows (MSYS2 / MINGW64)

Install [MSYS2](https://www.msys2.org/), open the **MINGW64** shell, then:

```sh
pacman -S --needed \
  mingw-w64-x86_64-toolchain mingw-w64-x86_64-go mingw-w64-x86_64-pkgconf \
  mingw-w64-x86_64-libjpeg-turbo mingw-w64-x86_64-openjpeg2 \
  mingw-w64-x86_64-libjxl mingw-w64-x86_64-libavif mingw-w64-x86_64-libwebp

export PATH=/mingw64/bin:$PATH
export PKG_CONFIG_PATH=/mingw64/lib/pkgconfig
export CGO_ENABLED=1

# OpenJPH (HTJ2K) is not packaged for MSYS2, so disable it:
go install -tags nohtj2k github.com/wsilabs/wsitools/cmd/wsitools@latest
```

This gives JPEG, JPEG 2000, JPEG XL, AVIF, and WebP. For HTJ2K on Windows you'd
build OpenJPH from source with the MINGW64 toolchain and drop the `nohtj2k` tag —
advanced and rarely needed.

---

## Building from a clone (instead of `go install`)

```sh
git clone https://github.com/wsilabs/wsitools
cd wsitools
make build          # → ./wsitools  (or: go build -o wsitools ./cmd/wsitools)
```

Pass codec opt-outs through `go build` directly, e.g.
`go build -tags nohtj2k -o wsitools ./cmd/wsitools`.

## Skipping a codec

The optional codecs are opt-out. To build without one, omit its library and add
its tag (tags combine with commas):

```sh
# JPEG + JPEG2000 only (no jxl/avif/webp/htj2k) — needs just jpeg-turbo + openjpeg:
go build -tags nojxl,noavif,nowebp,nohtj2k -o wsitools ./cmd/wsitools
```

A binary built without a codec cannot use it at runtime even if the library is
later installed — codec support is fixed at build time.

## Verify

```sh
wsitools version
wsitools doctor          # reports environment + memory limits
wsitools info <slide>    # exercises the reader/codecs on a real file
```
