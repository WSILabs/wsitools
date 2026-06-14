// Package derivedsource presents a derived (reduced or cropped) pyramid as a
// source.Source so it can be handed to dicomwriter.WritePyramid, which reads
// compressed tiles verbatim via Level.TileInto. Two level kinds back the
// pyramid: rasterLevel (holds RGB, JPEG-encodes tiles on demand) and
// passthroughLevel (returns a source level's verbatim compressed frame at a
// tile offset, used for a lossless crop's L0).
package derivedsource

import (
	"fmt"
	"image"
	"io"
	"strconv"

	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/downscale"
	"github.com/wsilabs/wsitools/internal/source"
)

// rasterLevel is one pyramid level backed by an RGB888 raster; tiles are
// JPEG-baseline encoded on demand as complete (self-contained) frames — the
// form DICOM encapsulated PixelData requires.
type rasterLevel struct {
	raster   []byte
	w, h     int
	tileSize int
	quality  int
	index    int
}

// rasterLevel implements source.Level.
var _ source.Level = (*rasterLevel)(nil)

func (l *rasterLevel) Index() int                      { return l.index }
func (l *rasterLevel) Size() image.Point               { return image.Point{X: l.w, Y: l.h} }
func (l *rasterLevel) TileSize() image.Point           { return image.Point{X: l.tileSize, Y: l.tileSize} }
func (l *rasterLevel) Compression() source.Compression { return source.CompressionJPEG }
func (l *rasterLevel) TileMaxSize() int                { return l.tileSize*l.tileSize*3 + 2048 }

func (l *rasterLevel) Grid() image.Point {
	return image.Point{
		X: (l.w + l.tileSize - 1) / l.tileSize,
		Y: (l.h + l.tileSize - 1) / l.tileSize,
	}
}

func (l *rasterLevel) TileInto(x, y int, dst []byte) (int, error) {
	// One encoder per call: stateless, concurrency-safe, and the table compute
	// is negligible against the encode itself.
	enc, err := jpegcodec.New(codec.LevelGeometry{
		TileWidth:   l.tileSize,
		TileHeight:  l.tileSize,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(l.quality)}})
	if err != nil {
		return 0, fmt.Errorf("derivedsource: new jpeg encoder: %w", err)
	}
	tileRGB := downscale.ExtractTile(l.raster, l.w, l.h, x, y, l.tileSize)
	frame, err := enc.EncodeStandalone(tileRGB, l.tileSize, l.tileSize)
	if err != nil {
		return 0, fmt.Errorf("derivedsource: encode tile (%d,%d): %w", x, y, err)
	}
	if len(frame) > len(dst) {
		return 0, io.ErrShortBuffer
	}
	return copy(dst, frame), nil
}

// derived implements source.Source over a list of synthesized levels.
type derived struct {
	format string
	levels []source.Level
	md     source.Metadata
	assoc  []source.AssociatedImage
}

// derived implements source.Source.
var _ source.Source = (*derived)(nil)

func (d *derived) Format() string                       { return d.format }
func (d *derived) Levels() []source.Level               { return d.levels }
func (d *derived) Associated() []source.AssociatedImage { return d.assoc }
func (d *derived) Metadata() source.Metadata            { return d.md }
func (d *derived) SourceImageDescription() string       { return "" }
func (d *derived) Close() error                         { return nil }

// passthroughLevel returns a source level's verbatim compressed frame at a tile
// offset — the lossless-crop L0. Output tile (x,y) maps to source tile
// (x+offX, y+offY); Size/Grid report the snapped output geometry; TileSize and
// Compression are the source's.
type passthroughLevel struct {
	src        source.Level
	offX, offY int
	size       image.Point
	grid       image.Point
	index      int
}

// passthroughLevel implements source.Level.
var _ source.Level = (*passthroughLevel)(nil)

func (l *passthroughLevel) Index() int                      { return l.index }
func (l *passthroughLevel) Size() image.Point               { return l.size }
func (l *passthroughLevel) Grid() image.Point               { return l.grid }
func (l *passthroughLevel) TileSize() image.Point           { return l.src.TileSize() }
func (l *passthroughLevel) Compression() source.Compression { return l.src.Compression() }
func (l *passthroughLevel) TileMaxSize() int                { return l.src.TileMaxSize() }
func (l *passthroughLevel) TileInto(x, y int, dst []byte) (int, error) {
	return l.src.TileInto(x+l.offX, y+l.offY, dst)
}

// WithLosslessL0 builds a derived source whose L0 is a passthrough over srcL0
// (verbatim frames for the tile-aligned crop region) and whose nLevels-1 lower
// levels are box-halved raster levels decoded from the snapped region
// (lowerRaster, snapW×snapH). Used by crop --lossless into DICOM. Returns fewer
// than nLevels levels if a box-halved dimension reaches 0.
func WithLosslessL0(srcL0 source.Level, offX, offY, gridW, gridH, snapW, snapH int, lowerRaster []byte, nLevels, tileSize, quality int, format string, md source.Metadata, assoc []source.AssociatedImage) (source.Source, error) {
	if nLevels < 1 {
		return nil, fmt.Errorf("derivedsource: nLevels must be at least 1, got %d", nLevels)
	}
	levels := make([]source.Level, 0, nLevels)
	levels = append(levels, &passthroughLevel{
		src:   srcL0,
		offX:  offX,
		offY:  offY,
		size:  image.Point{X: snapW, Y: snapH},
		grid:  image.Point{X: gridW, Y: gridH},
		index: 0,
	})
	raster, lw, lh := lowerRaster, snapW, snapH
	for i := 1; i < nLevels; i++ {
		var err error
		raster, lw, lh, err = downscale.BoxHalve(raster, lw, lh, 2)
		if err != nil {
			return nil, fmt.Errorf("derivedsource: halve level %d→%d: %w", i-1, i, err)
		}
		if lw == 0 || lh == 0 {
			break
		}
		levels = append(levels, &rasterLevel{raster: raster, w: lw, h: lh, tileSize: tileSize, quality: quality, index: i})
	}
	return &derived{format: format, levels: levels, md: md, assoc: assoc}, nil
}

// FromReducedL0 builds an all-raster derived source: L0 is the supplied
// (reduced or cropped) raster, and nLevels-1 lower levels are produced by box-
// halving. tileSize/quality drive the JPEG encode; format/md/assoc are carried
// onto the source (md already factor-scaled by the caller). Used by downsample,
// convert --factor, and the re-encode crop.
// Returns fewer than nLevels levels if a box-halved dimension reaches 0 before
// nLevels iterations.
func FromReducedL0(l0 []byte, w, h, nLevels, tileSize, quality int, format string, md source.Metadata, assoc []source.AssociatedImage) (source.Source, error) {
	if nLevels < 1 {
		return nil, fmt.Errorf("derivedsource: nLevels must be at least 1, got %d", nLevels)
	}
	levels := make([]source.Level, 0, nLevels)
	raster, lw, lh := l0, w, h
	for i := 0; i < nLevels; i++ {
		levels = append(levels, &rasterLevel{raster: raster, w: lw, h: lh, tileSize: tileSize, quality: quality, index: i})
		if i == nLevels-1 {
			break
		}
		var err error
		raster, lw, lh, err = downscale.BoxHalve(raster, lw, lh, 2)
		if err != nil {
			return nil, fmt.Errorf("derivedsource: halve level %d→%d: %w", i, i+1, err)
		}
		if lw == 0 || lh == 0 {
			break
		}
	}
	return &derived{format: format, levels: levels, md: md, assoc: assoc}, nil
}
