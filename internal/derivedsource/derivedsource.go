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
