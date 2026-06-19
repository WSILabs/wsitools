// Package all exists solely to import every codec subpackage so they register
// themselves with the codec registry on import. Application binaries
// (cmd/wsitools) blank-import this package once.
//
// Optional codecs (avif, webp, jpegxl, htj2k) each live behind their own
// `!no<name>` build tag — see the per-codec files in this package. Disabling
// a codec via `-tags no<name>` drops both the codec package itself AND its
// blank-import here, producing a slim binary without dangling imports.
package all

import (
	_ "github.com/wsilabs/wsitools/internal/codec/jpeg"
	_ "github.com/wsilabs/wsitools/internal/codec/png"
)
