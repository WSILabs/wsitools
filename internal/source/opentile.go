package source

import (
	"fmt"
	"image"
	"os"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	_ "github.com/wsilabs/opentile-go/decoder/all" // register all tile decoders (incl. LZW/none/Deflate) so DecodedTile works for any source compression
	_ "github.com/wsilabs/opentile-go/formats/all"
	dicom "github.com/wsilabs/opentile-go/formats/dicom"
	svsfmt "github.com/wsilabs/opentile-go/formats/svs"
)

// Open is the entry point. Opens the file via opentile-go and returns a
// Source backed by opentile-go's synthesized tile geometry. Formats with
// zero tile geometry on level 0 (genuinely unhandled future cases) are
// rejected with ErrUnsupportedFormat.
func Open(path string) (Source, error) {
	src, _, err := OpenWithSlide(path)
	return src, err
}

// OpenWithSlide opens the file and returns BOTH the Source wrapper and the raw
// opentile slide. Callers needing *opentile.Level (raster materialization in the
// downsample/crop DICOM emitters) use the slide; everything else uses the
// Source. Applies the same DICOM-dir ambiguity guard and zero-geometry check as
// the original Open.
// Close the returned Source (not the slide directly); Source.Close closes the underlying slide.
func OpenWithSlide(path string) (Source, *opentile.Slide, error) {
	// Safe-by-default: a directory holding >1 distinct WSM series is ambiguous;
	// refuse rather than silently opening the dominant one. A single .dcm is
	// never ambiguous, so only check dirs.
	if fi, statErr := os.Stat(path); statErr == nil && fi.IsDir() {
		if infos, lerr := dicom.ListWSMSeries(path); lerr == nil && len(infos) > 1 {
			return nil, nil, &AmbiguousSeriesError{Path: path, Series: infos}
		}
	}
	t, err := opentile.OpenFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("source: open %s: %w", path, err)
	}
	// Sanity: confirm opentile-go has synthesized tile geometry for this
	// format. Striped / single-frame formats (NDPI, OME-OneFrame) are
	// handled internally by opentile-go's readers, which synthesize
	// (Size, TileSize, Grid). If a future format reports zero TileSize
	// on level 0, reject early with a clear diagnostic rather than
	// surfacing the failure deep in the transcode pipeline.
	if levels := t.Levels(); len(levels) > 0 {
		lvl0 := levels[0]
		if lvl0.TileSize.W == 0 || lvl0.TileSize.H == 0 {
			t.Close()
			return nil, nil, fmt.Errorf("%w: %s reports zero tile geometry on level 0", ErrUnsupportedFormat, t.Format())
		}
	}
	return FromSlide(t, path), t, nil
}

// FromSlide wraps an already-open opentile slide as a Source, without reopening
// the file. Used by callers that already hold a *opentile.Slide (e.g. the crop
// emitter, whose front-end opened the slide). The returned Source's Close closes
// the underlying slide.
func FromSlide(t *opentile.Slide, path string) Source {
	// ReadSourceImageDescription returns ("", err) for non-TIFF sources (IFE) —
	// silence the error and treat "" as "no description".
	desc, _ := ReadSourceImageDescription(path)
	return &opentileSource{t: t, path: path, desc: desc}
}

type opentileSource struct {
	t    *opentile.Slide
	path string
	desc string
}

func (s *opentileSource) Format() string                 { return string(s.t.Format()) }
func (s *opentileSource) SourceImageDescription() string { return s.desc }
func (s *opentileSource) Close() error                   { return s.t.Close() }

func (s *opentileSource) Levels() []Level {
	out := make([]Level, 0, len(s.t.Levels()))
	for i, lvl := range s.t.Levels() {
		out = append(out, &opentileLevel{lvl: lvl, index: i})
	}
	return out
}

func (s *opentileSource) Associated() []AssociatedImage {
	src := s.t.AssociatedImages()
	out := make([]AssociatedImage, 0, len(src))
	for _, a := range src {
		out = append(out, &opentileAssociated{a: a})
	}
	return out
}

func (s *opentileSource) Metadata() Metadata {
	md := s.t.Metadata()
	m := Metadata{
		Make:                md.ScannerManufacturer,
		Model:               md.ScannerModel,
		SerialNumber:        md.ScannerSerial,
		Magnification:       md.Magnification,
		AcquisitionDateTime: md.AcquisitionDateTime,
		Raw:                 map[string]string{},
	}
	if len(md.ScannerSoftware) > 0 {
		m.Software = md.ScannerSoftware[0]
	}
	// Cross-format scale: opentile-go normalizes every format's native
	// pixel size into MicronsPerPixelX/Y. Prefer that; fall back to the
	// SVS-specific struct only when the cross-format value is absent.
	m.MPPX = md.MPP.X
	m.MPPY = md.MPP.Y
	m.MPP = md.MPP.Symmetric() // single value if X==Y, else 0
	if smd, ok := svsfmt.MetadataOf(s.t); ok {
		if m.MPPX == 0 && smd.MPP != 0 {
			m.MPPX, m.MPPY, m.MPP = smd.MPP, smd.MPP, smd.MPP
		}
		if smd.Filename != "" {
			m.Raw["filename"] = smd.Filename
		}
	}
	m.ICCProfile = s.t.ICCProfile()
	return m
}

type opentileLevel struct {
	lvl   *opentile.Level
	index int
}

func (l *opentileLevel) Index() int { return l.index }
func (l *opentileLevel) Size() image.Point {
	return image.Point{X: l.lvl.Size.W, Y: l.lvl.Size.H}
}
func (l *opentileLevel) TileSize() image.Point {
	return image.Point{X: l.lvl.TileSize.W, Y: l.lvl.TileSize.H}
}
func (l *opentileLevel) Grid() image.Point {
	return image.Point{X: l.lvl.Grid.W, Y: l.lvl.Grid.H}
}
func (l *opentileLevel) TileMaxSize() int { return l.lvl.TileMaxSize() }

func (l *opentileLevel) TileInto(x, y int, dst []byte) (int, error) {
	return l.lvl.TileInto(x, y, dst)
}

func (l *opentileLevel) DecodedTile(x, y int) (*decoder.Image, error) {
	return l.lvl.DecodedTile(x, y, opentile.WithFormat(decoder.PixelFormatRGB))
}

func (l *opentileLevel) Compression() Compression {
	return mapOpentileCompression(l.lvl.Compression)
}

type opentileAssociated struct {
	a opentile.AssociatedImage
}

func (a *opentileAssociated) Type() string {
	return string(a.a.Type())
}
func (a *opentileAssociated) Size() image.Point {
	sz := a.a.Size()
	return image.Point{X: sz.W, Y: sz.H}
}
func (a *opentileAssociated) Bytes() ([]byte, error) { return a.a.Bytes() }
func (a *opentileAssociated) Decode(opts decoder.DecodeOptions) (*decoder.Image, error) {
	return a.a.Decode(opts)
}
func (a *opentileAssociated) Compression() Compression {
	return mapOpentileCompression(a.a.Compression())
}
func (a *opentileAssociated) IFDOffset() (int64, bool) {
	return a.a.IFDOffset()
}
func (a *opentileAssociated) Source() (opentile.AssociatedEncoding, bool) {
	return a.a.Encoding()
}

func mapOpentileCompression(c opentile.Compression) Compression {
	switch c {
	case opentile.CompressionJPEG:
		return CompressionJPEG
	case opentile.CompressionJP2K:
		return CompressionJPEG2000
	case opentile.CompressionLZW:
		return CompressionLZW
	case opentile.CompressionDeflate:
		return CompressionDeflate
	case opentile.CompressionNone:
		return CompressionNone
	case opentile.CompressionAVIF:
		return CompressionAVIF
	case opentile.CompressionIRIS:
		return CompressionIrisProprietary
	case opentile.CompressionWebP:
		return CompressionWebP
	case opentile.CompressionJPEGXL:
		return CompressionJPEGXL
	case opentile.CompressionHTJ2K:
		return CompressionHTJ2K
	}
	return CompressionUnknown
}
