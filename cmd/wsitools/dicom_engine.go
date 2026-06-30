package main

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	resample "github.com/wsilabs/opentile-go/resample"
	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
	dicomwriter "github.com/wsilabs/wsitools/internal/dicomwriter"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
)

// runDICOMEngine streams srcRegion → outL0 through the engine into a spool, then
// hands a spoolSource to emitDICOM. md/assoc/format describe the OUTPUT (md
// scale-adjusted by the caller). codecName selects the frame codec (default jpeg).
func runDICOMEngine(ctx context.Context, slide *opentile.Slide, srcRegion opentile.Region, outL0 opentile.Size, codecName string, quality, workers int, format string, md source.Metadata, assoc []source.AssociatedImage, opts dicomwriter.Options, output string, force bool) error {
	levels := octaveLevelSpecsFor(outL0, resolveTileSize(slide.Levels()[0].TileSize.W, cvTileSize))

	enc, comp, err := newDicomFrameEncoder(codecName, quality)
	if err != nil {
		return err
	}
	// Pad partial edge frames up to the full frame size (DICOM TILED_FULL frames
	// are uniform Rows×Columns); all levels share one square tile size.
	enc.tileW, enc.tileH = levels[0].TileW, levels[0].TileH
	defer enc.Close()

	spoolDir, err := os.MkdirTemp("", "wsitools-dcm-spool-*")
	if err != nil {
		return err
	}
	sink, err := newSpoolTileSink(spoolDir, levels)
	if err != nil {
		_ = os.RemoveAll(spoolDir)
		return err
	}

	kernel := resample.Box
	if outL0 == srcRegion.Size {
		kernel = resample.Nearest // identity (crop)
	}
	bar := newTileProgress("encoding", sumLevelTiles(levels))
	runErr := retile.Run(ctx, retile.Spec{
		Slide: slide, SrcRegion: srcRegion, OutL0: outL0, Levels: levels,
		Kernel: kernel, Encoder: enc, Sink: sink, Workers: workers,
		OnTileWritten: bar.Increment,
	})
	bar.Wait()
	if runErr != nil {
		sink.remove()
		_ = os.RemoveAll(spoolDir)
		return runErr
	}

	// The spools stay OPEN; spoolSource reads frames via ReadAt. src.Close() →
	// sink.remove() closes+removes them after emitDICOM finishes pulling.
	src := newSpoolSource(sink, format, comp, md, assoc)
	defer func() { _ = src.Close(); _ = os.RemoveAll(spoolDir) }()
	return emitDICOM(src, opts, output, force)
}

// spoolTileSink implements retile.TileSink, spooling each level's frames to a
// per-level tileSpool indexed by row-major tile position.
type spoolTileSink struct {
	levels []retile.LevelSpec
	spools []*tileSpool
}

func newSpoolTileSink(dir string, levels []retile.LevelSpec) (*spoolTileSink, error) {
	spools := make([]*tileSpool, len(levels))
	for i, ls := range levels {
		sp, err := newTileSpool(filepath.Join(dir, fmt.Sprintf("L%d", i)), ls.Cols*ls.Rows)
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

// dicomFrameEncoder implements retile.TileEncoder, producing SELF-CONTAINED
// frames (DICOM has no TIFF tag-347 shared-tables mechanism). JPEG uses
// EncodeStandalone; J2K-family codecs (jpeg2000/htj2k) already return a complete
// codestream from EncodeTile.
type dicomFrameEncoder struct {
	jpeg         *jpegcodec.Encoder // non-nil for jpeg
	codec        codec.Encoder      // non-nil for j2k-family
	tileW, tileH int                // full frame size; partial edge tiles are padded up to it
}

// newDicomFrameEncoder builds the frame encoder + reports the source.Compression
// the spoolSource should advertise (so dicomwriter picks the transfer syntax).
func newDicomFrameEncoder(codecName string, quality int) (*dicomFrameEncoder, source.Compression, error) {
	switch codecName {
	case "", "jpeg":
		je, err := jpegcodec.New(codec.LevelGeometry{}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}})
		if err != nil {
			return nil, 0, fmt.Errorf("jpeg.New: %w", err)
		}
		return &dicomFrameEncoder{jpeg: je}, source.CompressionJPEG, nil
	case "jpeg2000":
		return newJ2KFrameEncoder("jpeg2000", quality)
	case "htj2k":
		return newJ2KFrameEncoder("htj2k", quality)
	default:
		return nil, 0, fmt.Errorf("--codec %q not supported for DICOM (jpeg, jpeg2000, htj2k)", codecName)
	}
}

func newJ2KFrameEncoder(codecName string, quality int) (*dicomFrameEncoder, source.Compression, error) {
	fac, err := codec.Lookup(codecName)
	if err != nil {
		return nil, 0, err
	}
	ce, err := fac.NewEncoder(codec.LevelGeometry{PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}})
	if err != nil {
		return nil, 0, err
	}
	comp := source.CompressionJPEG2000
	if codecName == "htj2k" {
		comp = source.CompressionHTJ2K
	}
	return &dicomFrameEncoder{codec: ce}, comp, nil
}

func (e *dicomFrameEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	// DICOM TILED_FULL requires every frame to be exactly Rows×Columns; the engine
	// hands partial right/bottom edge frames (and whole levels smaller than one
	// frame) at truncated content size. Pad up to the full frame (edge-replicated)
	// so strict readers (OpenSlide's DICOM reader, pydicom consumers) don't hit a
	// frame/dimension mismatch — mirrors codecTileEncoder for the TIFF family.
	if e.tileW > 0 && e.tileH > 0 && (w < e.tileW || h < e.tileH) {
		rgb = padRGBTileReplicate(rgb, w, h, e.tileW, e.tileH)
		w, h = e.tileW, e.tileH
	}
	if e.jpeg != nil {
		return e.jpeg.EncodeStandalone(rgb, w, h)
	}
	return e.codec.EncodeTile(rgb, w, h, nil) // J2K-family: already a complete codestream
}

func (e *dicomFrameEncoder) Close() error {
	if e.jpeg != nil {
		return e.jpeg.Close()
	}
	if e.codec != nil {
		return e.codec.Close()
	}
	return nil
}

// Compile-time interface assertions: a missing or mismatched method fails the build.
var _ source.Source = (*spoolSource)(nil)
var _ source.Level = (*spoolLevel)(nil)
var _ retile.TileEncoder = (*dicomFrameEncoder)(nil)
