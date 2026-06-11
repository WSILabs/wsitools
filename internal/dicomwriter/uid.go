// Package dicomwriter builds and emits DICOM Whole Slide Microscopy (WSM)
// instances from a wsitools source. Phase 0 (spike): a single VOLUME instance
// for one pyramid level, copying the source's JPEG frames verbatim.
package dicomwriter

import (
	"crypto/rand"
	"math/big"
)

// NewUID returns a DICOM UID under the UUID-derived root 2.25.<128-bit-int>
// (PS3.5 B.2). The 2.25 root requires no registered org root and the result is
// always <= 64 chars (2^128-1 is 39 decimal digits; "2.25." + 39 = 44). The
// 128 random bits make collisions practically impossible.
func NewUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	n := new(big.Int).SetBytes(b[:])
	return "2.25." + n.String()
}
