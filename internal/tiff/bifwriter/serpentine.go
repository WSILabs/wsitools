// Package bifwriter writes Ventana/Roche BIF (Biolmagene Image File) pyramids.
// Phase 0 (spike): verbatim-tile single-level + spec-shaped two-IFD output,
// verified by opentile-go round-trip. Tile ordering mirrors opentile-go's
// formats/bif/serpentine.go bit-for-bit (the read-side counterpart).
package bifwriter

// imageToSerpentine maps image-space (col,row) in a (cols,rows) tile grid to the
// index into BIF's TileOffsets array. Stage rows count up from the bottom; even
// stage rows go left-to-right, odd rows right-to-left; index 0 = bottom-left.
// Out-of-grid coordinates return -1.
func imageToSerpentine(col, row, cols, rows int) int {
	if col < 0 || row < 0 || col >= cols || row >= rows {
		return -1
	}
	stageRow := rows - 1 - row
	stageCol := col
	if stageRow%2 == 1 {
		stageCol = cols - 1 - col
	}
	return stageRow*cols + stageCol
}

// serpentineToImage is the inverse of imageToSerpentine. Out-of-range idx → (-1,-1).
func serpentineToImage(idx, cols, rows int) (col, row int) {
	if idx < 0 || idx >= cols*rows {
		return -1, -1
	}
	stageRow := idx / cols
	stageCol := idx % cols
	if stageRow%2 == 1 {
		stageCol = cols - 1 - stageCol
	}
	return stageCol, rows - 1 - stageRow
}
