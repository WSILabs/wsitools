package tileorder

// RowMajor emits tiles in row-major (raster scan) order: (0,0), (1,0),
// ..., (W-1,0), (0,1), (1,1), ..., (W-1,H-1). Universal default; the
// only ordering all writers accept.
var RowMajor OrderStrategy = rowMajor{}

type rowMajor struct{}

func (rowMajor) Name() string { return "row-major" }

func (rowMajor) Index(x, y, tilesX, _ uint32) uint32 {
	return y*tilesX + x
}

func (rowMajor) IndexToXY(idx, tilesX, _ uint32) (x, y uint32) {
	return idx % tilesX, idx / tilesX
}

func init() { register(RowMajor) }
