package source

import (
	"fmt"
	"image"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	svsfmt "github.com/wsilabs/opentile-go/formats/svs"
)

// Open is the entry point. Opens the file via opentile-go, then routes
// through the sanity gate. Sanity-gate-rejected formats return
// ErrUnsupportedFormat with a descriptive suffix.
func Open(path string) (Source, error) {
	t, err := opentile.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("source: open %s: %w", path, err)
	}
	switch t.Format() {
	case opentile.FormatNDPI:
		t.Close()
		return nil, fmt.Errorf("%w: NDPI (striped MCU streams have no source tile geometry)", ErrUnsupportedFormat)
	case opentile.FormatLeicaSCN:
		// SCN supports multi-image (multiple disjoint tissue rectangles) and
		// multi-channel (fluorescence) — neither fits v0.2.0's single-pyramid
		// streaming model.
		t.Close()
		return nil, fmt.Errorf("%w: Leica SCN (multi-image / multi-channel deferred to v0.2.x)", ErrUnsupportedFormat)
	case opentile.FormatOMETIFF:
		// OneFrame OMEs report TileSize == zero on all levels.
		if oneFrameOME(t) {
			t.Close()
			return nil, fmt.Errorf("%w: OME-OneFrame", ErrUnsupportedFormat)
		}
	}
	// ReadSourceImageDescription returns ("", err) for non-TIFF sources
	// (e.g. IFE) — silence the error and treat "" as "no description".
	desc, _ := ReadSourceImageDescription(path)
	return &opentileSource{t: t, path: path, desc: desc}, nil
}

func oneFrameOME(t opentile.Tiler) bool {
	for _, lvl := range t.Levels() {
		if lvl.TileSize() == (opentile.Size{}) {
			return true
		}
	}
	return false
}

type opentileSource struct {
	t    opentile.Tiler
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
	src := s.t.Associated()
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
	if smd, ok := svsfmt.MetadataOf(s.t); ok {
		m.MPP = smd.MPP
		if smd.Filename != "" {
			m.Raw["filename"] = smd.Filename
		}
	}
	return m
}

type opentileLevel struct {
	lvl   opentile.Level
	index int
}

func (l *opentileLevel) Index() int { return l.index }
func (l *opentileLevel) Size() image.Point {
	sz := l.lvl.Size()
	return image.Point{X: sz.W, Y: sz.H}
}
func (l *opentileLevel) TileSize() image.Point {
	sz := l.lvl.TileSize()
	return image.Point{X: sz.W, Y: sz.H}
}
func (l *opentileLevel) Grid() image.Point {
	g := l.lvl.Grid()
	return image.Point{X: g.W, Y: g.H}
}
func (l *opentileLevel) TileMaxSize() int { return l.lvl.TileMaxSize() }

func (l *opentileLevel) TileInto(x, y int, dst []byte) (int, error) {
	return l.lvl.TileInto(x, y, dst)
}

func (l *opentileLevel) Compression() Compression {
	return mapOpentileCompression(l.lvl.Compression())
}

type opentileAssociated struct {
	a opentile.AssociatedImage
}

func (a *opentileAssociated) Kind() string {
	return a.a.Type()
}
func (a *opentileAssociated) Size() image.Point {
	sz := a.a.Size()
	return image.Point{X: sz.W, Y: sz.H}
}
func (a *opentileAssociated) Bytes() ([]byte, error) { return a.a.Bytes() }
func (a *opentileAssociated) Compression() Compression {
	return mapOpentileCompression(a.a.Compression())
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

