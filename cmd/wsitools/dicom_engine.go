package main

import (
	"fmt"
	"image"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
)

// spoolTileSink implements retile.TileSink, spooling each level's frames to a
// per-level tileSpool indexed by row-major tile position.
type spoolTileSink struct {
	levels []retile.LevelSpec
	spools []*tileSpool
}

func newSpoolTileSink(dir string, levels []retile.LevelSpec) (*spoolTileSink, error) {
	spools := make([]*tileSpool, len(levels))
	for i, ls := range levels {
		sp, err := newTileSpool(fmt.Sprintf("%s/L%d", dir, i), ls.Cols*ls.Rows)
		if err != nil {
			return nil, err
		}
		spools[i] = sp
	}
	return &spoolTileSink{levels: levels, spools: spools}, nil
}

func (s *spoolTileSink) WriteTile(level, col, row int, encoded []byte) error {
	if level < 0 || level >= len(s.spools) {
		return fmt.Errorf("spoolTileSink: level %d out of range", level)
	}
	ls := s.levels[level]
	frame := make([]byte, len(encoded)) // copy: engine may reuse encoded's backing array
	copy(frame, encoded)
	return s.spools[level].put(row*ls.Cols+col, frame)
}

func (s *spoolTileSink) remove() {
	for _, sp := range s.spools {
		if sp != nil {
			_ = sp.remove()
		}
	}
}

// spoolSource is a source.Source over a finished spoolTileSink, pulled by
// dicomwriter.WritePyramid. Frames are served verbatim; metadata/associated/
// compression are supplied by the driver.
type spoolSource struct {
	sink   *spoolTileSink
	format string
	comp   source.Compression
	md     source.Metadata
	assoc  []source.AssociatedImage
}

func newSpoolSource(sink *spoolTileSink, format string, comp source.Compression, md source.Metadata, assoc []source.AssociatedImage) *spoolSource {
	return &spoolSource{sink: sink, format: format, comp: comp, md: md, assoc: assoc}
}

func (s *spoolSource) Format() string                       { return s.format }
func (s *spoolSource) Metadata() source.Metadata            { return s.md }
func (s *spoolSource) Associated() []source.AssociatedImage { return s.assoc }
func (s *spoolSource) SourceImageDescription() string       { return "" }
func (s *spoolSource) Close() error                         { s.sink.remove(); return nil }
func (s *spoolSource) Levels() []source.Level {
	out := make([]source.Level, len(s.sink.levels))
	for i := range s.sink.levels {
		out[i] = &spoolLevel{src: s, i: i}
	}
	return out
}

type spoolLevel struct {
	src *spoolSource
	i   int
}

func (l *spoolLevel) spec() retile.LevelSpec          { return l.src.sink.levels[l.i] }
func (l *spoolLevel) Index() int                      { return l.i }
func (l *spoolLevel) Size() image.Point               { return image.Point{X: l.spec().Width, Y: l.spec().Height} }
func (l *spoolLevel) TileSize() image.Point           { return image.Point{X: l.spec().TileW, Y: l.spec().TileH} }
func (l *spoolLevel) Grid() image.Point               { return image.Point{X: l.spec().Cols, Y: l.spec().Rows} }
func (l *spoolLevel) Overlapping() bool               { return false }
func (l *spoolLevel) Compression() source.Compression { return l.src.comp }
func (l *spoolLevel) TileMaxSize() int                { return l.spec().TileW*l.spec().TileH*3 + 1024 }

func (l *spoolLevel) TileInto(x, y int, dst []byte) (int, error) {
	ls := l.spec()
	frame, err := l.src.sink.spools[l.i].get(y*ls.Cols + x)
	if err != nil {
		return 0, err
	}
	if len(dst) < len(frame) {
		return 0, fmt.Errorf("spoolLevel: dst too small (%d < %d)", len(dst), len(frame))
	}
	return copy(dst, frame), nil
}

func (l *spoolLevel) DecodedTile(x, y int) (*decoder.Image, error) {
	// dicomwriter pulls verbatim frames via TileInto and does not decode.
	return nil, fmt.Errorf("spoolLevel.DecodedTile: not supported (verbatim-frame source)")
}

// Compile-time interface assertions: a missing or mismatched method fails the build.
var _ source.Source = (*spoolSource)(nil)
var _ source.Level = (*spoolLevel)(nil)
