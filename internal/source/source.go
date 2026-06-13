// Package source is a thin adapter between the wsitools CLI and opentile-go.
// It exposes a unified streaming-friendly tile API over opentile-go's
// synthesized tile geometry. Whatever opentile-go's various format-specific
// quirks are, the CLI consumes them through the Source interface uniformly.
package source

import (
	"errors"
	"fmt"
	"image"
	"strings"
	"time"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	dicom "github.com/wsilabs/opentile-go/formats/dicom"
)

// Source is what the transcode CLI consumes. Wraps an opentile-go Tiler.
type Source interface {
	// Format returns one of the opentile.Format* string values.
	Format() string

	// Levels returns the pyramid levels in order, L0 first.
	Levels() []Level

	// Associated returns the source's associated images (label, macro,
	// thumbnail, overview, probability, map) — the union of what
	// opentile-go's various format-specific readers expose.
	Associated() []AssociatedImage

	// Metadata returns cross-format scanner / acquisition facts.
	Metadata() Metadata

	// SourceImageDescription returns the L0 IFD's raw ImageDescription
	// string for TIFF-dialect sources, or "" for non-TIFF sources (IFE).
	// Errors are silenced — a missing or malformed tag yields "".
	SourceImageDescription() string

	Close() error
}

// Level is one pyramid level.
type Level interface {
	Index() int
	Size() image.Point     // image dimensions in pixels
	TileSize() image.Point // tile dimensions; preserved verbatim on output
	Grid() image.Point     // tilesX × tilesY
	Compression() Compression

	// TileMaxSize returns an upper bound on any tile's compressed-byte
	// length on this level — sized for sync.Pool buffers.
	TileMaxSize() int

	// TileInto writes the raw compressed tile bytes at (x, y) into dst
	// and returns the number of bytes written. dst must have len >=
	// TileMaxSize(); shorter buffers return io.ErrShortBuffer. The
	// returned slice (dst[:n]) is the canonical byte form for the
	// transcode/downsample decoder pipeline.
	TileInto(x, y int, dst []byte) (int, error)
}

// AssociatedImage is one of label / macro / thumbnail / overview /
// probability / map / associated.
type AssociatedImage interface {
	// Type returns the associated-image type (label/macro/thumbnail/...),
	// mirroring opentile-go's AssociatedImage.Type().
	Type() string
	Size() image.Point
	Compression() Compression
	Bytes() ([]byte, error) // self-contained encoded blob

	// Decode returns the faithfully-decoded pixels (delegates to opentile-go,
	// which owns all codec / LZW-predictor / TIFF-strip handling).
	Decode(opts decoder.DecodeOptions) (*decoder.Image, error)

	// Source returns the faithful on-disk source form (verbatim strips + TIFF
	// tags) for byte-identical re-emission into a new standalone TIFF; ok=false
	// for synthesized / tiled / non-TIFF associated images. Delegates to
	// opentile-go's AssociatedImage.Encoding() (GH opentile-go#22).
	Source() (opentile.AssociatedEncoding, bool)

	// IFDOffset returns the byte offset of the backing TIFF IFD for
	// TIFF-family slides; ok=false otherwise.
	IFDOffset() (int64, bool)
}

// Compression mirrors opentile-go's Compression enum.
type Compression int

const (
	CompressionUnknown Compression = iota
	CompressionJPEG
	CompressionJPEG2000
	CompressionLZW
	CompressionDeflate
	CompressionNone
	CompressionAVIF
	CompressionIrisProprietary
	CompressionWebP
	CompressionJPEGXL
	CompressionHTJ2K
)

func (c Compression) String() string {
	switch c {
	case CompressionJPEG:
		return "jpeg"
	case CompressionJPEG2000:
		return "jpeg2000"
	case CompressionLZW:
		return "lzw"
	case CompressionDeflate:
		return "deflate"
	case CompressionNone:
		return "none"
	case CompressionAVIF:
		return "avif"
	case CompressionIrisProprietary:
		return "iris-proprietary"
	case CompressionWebP:
		return "webp"
	case CompressionJPEGXL:
		return "jpegxl"
	case CompressionHTJ2K:
		return "htj2k"
	}
	return "unknown"
}

// Metadata is the cross-format scanner / acquisition info.
type Metadata struct {
	Make, Model, Software, SerialNumber string
	Magnification                       float64
	MPP                                 float64 // symmetric µm/px (0 if unknown OR asymmetric)
	MPPX                                float64 // µm/px, X axis; 0 if unknown
	MPPY                                float64 // µm/px, Y axis; 0 if unknown
	ICCProfile                          []byte  // embedded color profile; nil if none
	AcquisitionDateTime                 time.Time
	Raw                                 map[string]string
}

// AmbiguousSeriesError is returned by Open when a directory input resolves to
// more than one distinct DICOM WSM series. A single .dcm instance is never
// ambiguous (it anchors to its own SeriesUID).
type AmbiguousSeriesError struct {
	Path   string
	Series []dicom.SeriesInfo
}

func (e *AmbiguousSeriesError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s contains %d distinct WSM series:\n", e.Path, len(e.Series))
	for _, s := range e.Series {
		fmt.Fprintf(&b, "  • %s  (%s %s, %d levels, %gx)\n",
			s.SeriesUID, s.Manufacturer, s.Model, s.LevelCount, s.Magnification)
	}
	b.WriteString("Specify one by passing the path to a .dcm instance of the series you want.")
	return b.String()
}

var (
	// ErrUnsupportedFormat is returned by Open for source formats whose
	// opentile-go reader reports zero tile geometry on level 0. Call sites
	// wrap this sentinel with a format-specific diagnostic via fmt.Errorf.
	ErrUnsupportedFormat = errors.New("source: format unsupported")

	// ErrUnsupportedSourceCompression is returned when a tile uses a
	// compression we can't decode (e.g., Iris-proprietary, or AVIF source
	// before v0.2.1).
	ErrUnsupportedSourceCompression = errors.New("source: source compression not decodable at v0.2.0")
)
