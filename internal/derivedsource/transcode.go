package derivedsource

import (
	"fmt"
	"image"
	"io"
	"strconv"
	"sync"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/source"
)

// transcodeLevel presents a source level of any decodable codec as a
// JPEG-baseline level: each tile is decoded via the source's level-decode
// (LZW / uncompressed / Deflate / AVIF / WebP / … all handled) and re-encoded
// as a self-contained JPEG frame on demand. Backs `convert --to dicom
// --codec jpeg`, the explicit opt-in for re-encoding a source whose tiles are
// not a DICOM transfer syntax.
type transcodeLevel struct {
	src          source.Level
	quality      int
	index        int
	tileW, tileH int

	mu     sync.Mutex
	enc    *jpegcodec.Encoder
	encErr error
	inited bool
}

// transcodeLevel implements source.Level.
var _ source.Level = (*transcodeLevel)(nil)

func (l *transcodeLevel) Index() int                      { return l.index }
func (l *transcodeLevel) Size() image.Point               { return l.src.Size() }
func (l *transcodeLevel) TileSize() image.Point           { return l.src.TileSize() }
func (l *transcodeLevel) Grid() image.Point               { return l.src.Grid() }
func (l *transcodeLevel) Overlapping() bool               { return l.src.Overlapping() }
func (l *transcodeLevel) Compression() source.Compression { return source.CompressionJPEG }
func (l *transcodeLevel) TileMaxSize() int                { return l.tileW*l.tileH*3 + 2048 }

func (l *transcodeLevel) DecodedTile(x, y int) (*decoder.Image, error) {
	return l.src.DecodedTile(x, y)
}

func (l *transcodeLevel) TileInto(x, y int, dst []byte) (int, error) {
	img, err := l.src.DecodedTile(x, y)
	if err != nil {
		return 0, fmt.Errorf("derivedsource: decode source tile (%d,%d): %w", x, y, err)
	}
	rgb := tightTileRGB(img)
	// WritePyramid pulls tiles serially, but guard the reused cgo encoder so a
	// future concurrent puller can't race it.
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.inited {
		l.enc, l.encErr = jpegcodec.New(codec.LevelGeometry{
			TileWidth:   l.tileW,
			TileHeight:  l.tileH,
			PixelFormat: codec.PixelFormatRGB8,
		}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(l.quality)}})
		l.inited = true
	}
	if l.encErr != nil {
		return 0, fmt.Errorf("derivedsource: new jpeg encoder: %w", l.encErr)
	}
	frame, err := l.enc.EncodeStandalone(rgb, l.tileW, l.tileH)
	if err != nil {
		return 0, fmt.Errorf("derivedsource: encode tile (%d,%d): %w", x, y, err)
	}
	if len(frame) > len(dst) {
		return 0, io.ErrShortBuffer
	}
	return copy(dst, frame), nil
}

// tightTileRGB returns the decoded tile's pixels as a tightly packed RGB buffer
// (stride = Width*3), the form the JPEG encoder expects.
func tightTileRGB(img *decoder.Image) []byte {
	rowBytes := img.Width * 3
	if img.Stride == rowBytes {
		return img.Pix[:img.Height*rowBytes]
	}
	out := make([]byte, img.Height*rowBytes)
	for y := 0; y < img.Height; y++ {
		copy(out[y*rowBytes:(y+1)*rowBytes], img.Pix[y*img.Stride:y*img.Stride+rowBytes])
	}
	return out
}

// TranscodeToJPEG wraps src as a derived source whose every pyramid level is
// re-encoded to JPEG-baseline on demand (see transcodeLevel). Associated images
// and metadata pass through unchanged. Backs `convert --to dicom --codec jpeg`.
//
// workers is accepted for signature parity with the other derivedsource
// constructors and a future parallel pre-encode; tiles are currently transcoded
// serially, on demand, as WritePyramid pulls them (bounded memory: one decoded
// tile at a time).
func TranscodeToJPEG(src source.Source, quality, workers int) source.Source {
	_ = workers
	levels := make([]source.Level, 0, len(src.Levels()))
	for i, lvl := range src.Levels() {
		ts := lvl.TileSize()
		levels = append(levels, &transcodeLevel{
			src:     lvl,
			quality: quality,
			index:   i,
			tileW:   ts.X,
			tileH:   ts.Y,
		})
	}
	return &derived{
		format: src.Format(),
		levels: levels,
		md:     src.Metadata(),
		assoc:  src.Associated(),
	}
}
