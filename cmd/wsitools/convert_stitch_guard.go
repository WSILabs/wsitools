package main

import (
	"fmt"

	"github.com/wsilabs/wsitools/internal/source"
)

// guardTargetHandlesOverlap reports whether target can consume an overlapping/
// stitched source. dzi/szi composite via their own streaming descent; cog-wsi/
// svs/tiff/ome-tiff route through the retile engine (SP2 M2). dicom (derivedsource
// path) and bif (no engine sink yet) cannot, so they stay guarded.
func guardTargetHandlesOverlap(target string) bool {
	switch target {
	case "", "dzi", "szi", "cog-wsi", "svs", "tiff", "ome-tiff", "ife":
		return true
	default:
		return false
	}
}

func overlapGuardError(input, target string) error {
	return fmt.Errorf("%s has overlapping/stitched tiles (e.g. a Ventana BIF): "+
		"re-tiling to %s is not yet supported — convert to a TIFF-family target "+
		"(cog-wsi/svs/tiff/ome-tiff) or dzi/szi, which composite the stitched image", input, target)
}

// guardStitchedSource refuses converting a stitched source only to a target that
// cannot consume it (dicom/bif). Overlap-capable targets return nil and handle
// the source via their own descent (dzi/szi) or the retile engine (the four
// TIFF-family targets, SP2 M2).
func guardStitchedSource(input, target string) error {
	if guardTargetHandlesOverlap(target) {
		return nil
	}
	src, err := source.Open(input)
	if err != nil {
		return nil // let the actual convert path surface open errors
	}
	defer src.Close()
	for _, lvl := range src.Levels() {
		if lvl.Overlapping() {
			return overlapGuardError(input, target)
		}
	}
	return nil
}
