package downscale

// ExtractTile copies a tileSize×tileSize RGB888 tile at tile coordinate (tx, ty)
// out of an RGB888 raster of rasterW×rasterH pixels. Pixels past the raster's
// right/bottom edge are left zero (the standard edge-pad). The returned slice is
// always tileSize*tileSize*3 bytes.
func ExtractTile(raster []byte, rasterW, rasterH, tx, ty, tileSize int) []byte {
	tile := make([]byte, tileSize*tileSize*3)
	x0 := tx * tileSize
	y0 := ty * tileSize
	if x0 >= rasterW || y0 >= rasterH {
		return tile // empty edge — full zero pad
	}
	copyW := tileSize
	if x0+copyW > rasterW {
		copyW = rasterW - x0
	}
	copyH := tileSize
	if y0+copyH > rasterH {
		copyH = rasterH - y0
	}
	srcStride := rasterW * 3
	dstStride := tileSize * 3
	for y := 0; y < copyH; y++ {
		srcOff := (y0+y)*srcStride + x0*3
		dstOff := y * dstStride
		copy(tile[dstOff:dstOff+copyW*3], raster[srcOff:srcOff+copyW*3])
	}
	return tile
}
