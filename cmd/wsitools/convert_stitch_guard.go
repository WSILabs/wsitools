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
// Detection: a normal level's last tile is partial, so Grid == ceil(Size/tile);
// an overlapping level has extra full tile columns/rows, so Grid > ceil(Size/tile).
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
		sz, ts, g := lvl.Size(), lvl.TileSize(), lvl.Grid()
		if ts.X <= 0 || ts.Y <= 0 {
			continue
		}
		ceilX := (sz.X + ts.X - 1) / ts.X
		ceilY := (sz.Y + ts.Y - 1) / ts.Y
		if g.X > ceilX || g.Y > ceilY {
			return fmt.Errorf("%s has overlapping/stitched tiles (e.g. a Ventana BIF): "+
				"re-tiling to %s is not yet supported — convert to dzi or szi (which "+
				"composite the stitched image correctly), or wait for the streaming "+
				"retile engine", input, target)
		}
	}
	return nil
}
