// Package png provides a stdlib image/png-backed PNG tile encoder. PNG tiles are
// lossless RGB888 and self-contained (no shared tables), used for Deep Zoom
// (DZI/SZI) output. PNG is ENCODE-ONLY and is NOT a TIFF tile codec — opentile
// does not read PNG-compressed TIFF tiles — so TIFFCompressionTag returns 0 and
// callers must restrict it to DZI/SZI (enforced at the CLI).
package png

import (
	"bytes"
	"image"
	"image/color"
	stdpng "image/png"

	"github.com/wsilabs/wsitools/internal/codec"
)

func init() { codec.Register(Factory{}) }

// Factory builds PNG encoders. Registered under the name "png".
type Factory struct{}

func (Factory) Name() string { return "png" }

func (Factory) NewEncoder(_ codec.LevelGeometry, _ codec.Quality) (codec.Encoder, error) {
	return &Encoder{}, nil
}

// Encoder encodes RGB888 tiles as standalone PNG. Stateless and safe to reuse.
type Encoder struct{}

// LevelHeader returns nil: PNG tiles carry no shared header/tables.
func (e *Encoder) LevelHeader() []byte { return nil }

// EncodeTile encodes w×h RGB888 pixels as a complete PNG. The dst hint is
// ignored (image/png allocates its own buffer).
func (e *Encoder) EncodeTile(rgb []byte, w, h int, _ []byte) ([]byte, error) {
	var b bytes.Buffer
	if err := stdpng.Encode(&b, &rgbImage{pix: rgb, stride: w * 3, w: w, h: h}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// TIFFCompressionTag returns 0: PNG is not a TIFF tile codec (DZI/SZI only).
func (e *Encoder) TIFFCompressionTag() uint16 { return 0 }

func (e *Encoder) Close() error { return nil }

// rgbImage wraps a raw RGB888 byte buffer as image.Image. It reports NRGBA with
// alpha hard-coded to 255 (opaque) — identical to convert_dzi.go's prior inline
// wrapper, so PNG output bytes are unchanged.
type rgbImage struct {
	pix    []byte
	stride int
	w, h   int
}

func (r *rgbImage) ColorModel() color.Model { return color.NRGBAModel }
func (r *rgbImage) Bounds() image.Rectangle { return image.Rect(0, 0, r.w, r.h) }
func (r *rgbImage) At(x, y int) color.Color {
	i := y*r.stride + x*3
	return color.NRGBA{R: r.pix[i+0], G: r.pix[i+1], B: r.pix[i+2], A: 0xFF}
}
