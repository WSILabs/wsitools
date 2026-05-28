//go:build nocgo

package aperioapp14

import (
	"errors"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/tiff"
)

var errNoCGO = errors.New("aperioapp14: requires cgo + libjpeg-turbo (rebuild with cgo enabled / without -tags nocgo)")

type Encoder struct{}

func New(_ codec.LevelGeometry, _ codec.Quality) (*Encoder, error) {
	return nil, errNoCGO
}

func (*Encoder) EncodeTile(_ []byte, _, _ int, _ []byte) ([]byte, error) {
	return nil, errNoCGO
}

func (*Encoder) LevelHeader() []byte        { return nil }
func (*Encoder) TIFFCompressionTag() uint16 { return tiff.CompressionJPEG }
func (*Encoder) Close() error               { return nil }
