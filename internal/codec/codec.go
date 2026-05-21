// Package codec defines the Encoder + EncoderFactory interfaces and a registry
// that codec subpackages register themselves into via init(). Concrete codec
// implementations live in internal/codec/<codec>/ subpackages.
package codec

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrUnknownCodec     = errors.New("codec: unknown codec name")
	ErrCodecUnavailable = errors.New("codec: not built into this binary")
)

type PixelFormat int

const (
	PixelFormatRGB8 PixelFormat = iota
	PixelFormatRGBA8
	PixelFormatYCbCr420
)

type ColorSpace struct {
	Name string
	ICC  []byte
}

type LevelGeometry struct {
	TileWidth, TileHeight int
	PixelFormat           PixelFormat
	ColorSpace            ColorSpace
}

type Quality struct {
	Knobs map[string]string
}

type Encoder interface {
	LevelHeader() []byte
	EncodeTile(rgb []byte, w, h int, dst []byte) ([]byte, error)
	TIFFCompressionTag() uint16
	Close() error
}

type EncoderFactory interface {
	Name() string
	NewEncoder(LevelGeometry, Quality) (Encoder, error)
}

var (
	regMu sync.RWMutex
	reg   = map[string]EncoderFactory{}
)

func Register(f EncoderFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	reg[f.Name()] = f
}

func Lookup(name string) (EncoderFactory, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	f, ok := reg[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCodec, name)
	}
	return f, nil
}

func List() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(reg))
	for k := range reg {
		out = append(out, k)
	}
	return out
}

func resetRegistryForTesting() {
	regMu.Lock()
	defer regMu.Unlock()
	reg = map[string]EncoderFactory{}
}
