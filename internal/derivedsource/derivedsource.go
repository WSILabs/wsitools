// Package derivedsource presents a derived (cropped) pyramid as a
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
	"sync"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/downscale"
	"github.com/wsilabs/wsitools/internal/source"
)

// jpegEncodeKnobs builds the JPEG codec quality knobs for a re-encoded level:
// the quality integer plus an optional chroma-subsampling knob so a re-encode
// honors the source's subsampling (444/422/440/420) instead of forcing the
// encoder default (4:2:0). An empty subsampling leaves the encoder default.
func jpegEncodeKnobs(quality int, subsampling string) map[string]string {
	k := map[string]string{"q": strconv.Itoa(quality)}
	if subsampling != "" {
		k["subsampling"] = subsampling
	}
	return k
}

// rasterLevel is one pyramid level backed by an RGB888 raster; tiles are
// JPEG-baseline encoded as complete (self-contained) frames — the form DICOM
// encapsulated PixelData requires. WritePyramid pulls tiles one at a time, so
// the level pre-encodes every tile once (lazily, on first TileInto) using a
// worker pool sized by `workers`, then serves frames from the cache. This
// parallelizes the CPU-bound JPEG encode across cores while preserving the
// pull-based Level API.
type rasterLevel struct {
	raster      []byte
	w, h        int
	tileSize    int
	quality     int
	subsampling string // chroma subsampling knob ("444"/"422"/"440"/"420"); "" = encoder default (4:2:0)
	workers     int
	index       int

	once   sync.Once
	frames [][]byte // [ty*tilesX + tx] → encoded frame; populated by encodeAll
	encErr error    // first error from the parallel pre-encode, if any
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

// Overlapping: a derived raster pyramid is freshly re-tiled, never overlapping.
func (l *rasterLevel) Overlapping() bool { return false }

// encodeAll JPEG-encodes every tile of this level into l.frames using a worker
// pool. Each tile index is written by exactly one worker (distinct slice
// elements), so no per-element locking is needed; l.encErr is mutex-guarded.
func (l *rasterLevel) encodeAll() {
	g := l.Grid()
	l.frames = make([][]byte, g.X*g.Y)
	workers := l.workers
	if workers < 1 {
		workers = 1
	}

	type job struct{ tx, ty, idx int }
	jobs := make(chan job)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			enc, err := jpegcodec.New(codec.LevelGeometry{
				TileWidth:   l.tileSize,
				TileHeight:  l.tileSize,
				PixelFormat: codec.PixelFormatRGB8,
			}, codec.Quality{Knobs: jpegEncodeKnobs(l.quality, l.subsampling)})
			if err != nil {
				mu.Lock()
				if l.encErr == nil {
					l.encErr = fmt.Errorf("derivedsource: new jpeg encoder: %w", err)
				}
				mu.Unlock()
			}
			for j := range jobs {
				if enc == nil {
					continue // drain remaining jobs so the producer never blocks
				}
				mu.Lock()
				failed := l.encErr != nil
				mu.Unlock()
				if failed {
					continue
				}
				tileRGB := downscale.ExtractTile(l.raster, l.w, l.h, j.tx, j.ty, l.tileSize)
				frame, err := enc.EncodeStandalone(tileRGB, l.tileSize, l.tileSize)
				if err != nil {
					mu.Lock()
					if l.encErr == nil {
						l.encErr = fmt.Errorf("derivedsource: encode tile (%d,%d): %w", j.tx, j.ty, err)
					}
					mu.Unlock()
					continue
				}
				l.frames[j.idx] = frame
			}
		}()
	}
	for ty := 0; ty < g.Y; ty++ {
		for tx := 0; tx < g.X; tx++ {
			jobs <- job{tx: tx, ty: ty, idx: ty*g.X + tx}
		}
	}
	close(jobs)
	wg.Wait()
	// The encoded frames supersede the raster; release it so a level's raster
	// and its frames don't both pin memory for the rest of the write.
	l.raster = nil
}

// DecodedTile returns this level's tile as RGB directly from the raster (no
// codec round-trip) — the level is already decoded pixels.
func (l *rasterLevel) DecodedTile(x, y int) (*decoder.Image, error) {
	rgb := downscale.ExtractTile(l.raster, l.w, l.h, x, y, l.tileSize)
	return &decoder.Image{Width: l.tileSize, Height: l.tileSize, Stride: l.tileSize * 3, Format: decoder.PixelFormatRGB, Pix: rgb}, nil
}

func (l *rasterLevel) TileInto(x, y int, dst []byte) (int, error) {
	l.once.Do(l.encodeAll)
	if l.encErr != nil {
		return 0, l.encErr
	}
	idx := y*l.Grid().X + x
	if idx < 0 || idx >= len(l.frames) {
		return 0, fmt.Errorf("derivedsource: tile (%d,%d) out of range", x, y)
	}
	frame := l.frames[idx]
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
func (l *passthroughLevel) Overlapping() bool               { return l.src.Overlapping() }
func (l *passthroughLevel) TileSize() image.Point           { return l.src.TileSize() }
func (l *passthroughLevel) Compression() source.Compression { return l.src.Compression() }
func (l *passthroughLevel) TileMaxSize() int                { return l.src.TileMaxSize() }
func (l *passthroughLevel) TileInto(x, y int, dst []byte) (int, error) {
	return l.src.TileInto(x+l.offX, y+l.offY, dst)
}
func (l *passthroughLevel) DecodedTile(x, y int) (*decoder.Image, error) {
	return l.src.DecodedTile(x+l.offX, y+l.offY)
}

// WithLosslessL0 builds a derived source whose L0 is a passthrough over srcL0
// (verbatim frames for the tile-aligned crop region) and whose nLevels-1 lower
// levels are box-halved raster levels decoded from the snapped region
// (lowerRaster, snapW×snapH). Used by crop --lossless into DICOM. Returns fewer
// than nLevels levels if a box-halved dimension reaches 0.
func WithLosslessL0(srcL0 source.Level, offX, offY, gridW, gridH, snapW, snapH int, lowerRaster []byte, nLevels, tileSize, quality int, subsampling string, workers int, format string, md source.Metadata, assoc []source.AssociatedImage) (source.Source, error) {
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
		levels = append(levels, &rasterLevel{raster: raster, w: lw, h: lh, tileSize: tileSize, quality: quality, subsampling: subsampling, workers: workers, index: i})
	}
	return &derived{format: format, levels: levels, md: md, assoc: assoc}, nil
}

