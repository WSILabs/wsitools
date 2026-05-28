//go:build nocgo

package jpeg

import (
	"errors"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/tiff"
)

var errNoCGO = errors.New("codec/jpeg: requires cgo + libjpeg-turbo (rebuild with cgo enabled / without -tags nocgo)")

func init() {
	codec.Register(Factory{})
}

type Factory struct{}

func (Factory) Name() string { return "jpeg" }
func (Factory) NewEncoder(_ codec.LevelGeometry, _ codec.Quality) (codec.Encoder, error) {
	return nil, errNoCGO
}

type Encoder struct{}

func New(_ codec.LevelGeometry, _ codec.Quality) (*Encoder, error) { return nil, errNoCGO }

func (*Encoder) EncodeTile(_ []byte, _, _ int, _ []byte) ([]byte, error) { return nil, errNoCGO }
func (*Encoder) EncodeStandalone(_ []byte, _, _ int) ([]byte, error)     { return nil, errNoCGO }
func (*Encoder) LevelHeader() []byte                                      { return nil }
func (*Encoder) TIFFCompressionTag() uint16                               { return tiff.CompressionJPEG }
func (*Encoder) Close() error                                             { return nil }
