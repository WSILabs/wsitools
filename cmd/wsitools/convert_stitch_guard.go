package main

import (
	"fmt"

	"github.com/wsilabs/wsitools/internal/source"
)

// guardStitchedSource refuses converting a source with overlapping/stitched
// tiles to a per-tile target. opentile v0.46+ reports a stitched Level.Size that
// disagrees with the raw tile Grid (a Ventana BIF's tiles physically overlap, so
// Grid has more tile columns than the stitched Size needs). The per-tile
// tile-copy / re-encode convert paths can't consume that: they crash ("tile out
// of grid"), panic (ome-tiff), or silently place tiles at naive un-compacted
// positions (dicom/bif). dzi/szi go through the streaming descent (ScaledStrips),
// which composites the stitched image correctly, so they are exempt. The general
// fix is the streaming retile engine
// (docs/superpowers/specs/2026-06-18-retiling-engine-design.md).
//
// Detection: opentile-go's Level.Overlapping (#71 / v0.48.0) is the authoritative
// signal that a level's stored tiles overlap (Grid does not tile Size).
func guardStitchedSource(input, target string) error {
	if target == "" || target == "dzi" || target == "szi" {
		return nil
	}
	src, err := source.Open(input)
	if err != nil {
		return nil // let the actual convert path surface open errors
	}
	defer src.Close()
	for _, lvl := range src.Levels() {
		if lvl.Overlapping() {
			return fmt.Errorf("%s has overlapping/stitched tiles (e.g. a Ventana BIF): "+
				"re-tiling to %s is not yet supported — convert to dzi or szi (which "+
				"composite the stitched image correctly), or wait for the streaming "+
				"retile engine", input, target)
		}
	}
	return nil
}
