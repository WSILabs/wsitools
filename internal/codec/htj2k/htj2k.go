//go:build !nohtj2k

// Package htj2k provides an OpenJPH-backed High-Throughput JPEG 2000 encoder.
package htj2k

/*
#cgo CXXFLAGS: -std=c++17
#cgo pkg-config: openjph
#cgo darwin LDFLAGS: -lc++
#cgo linux LDFLAGS: -lstdc++
#cgo windows LDFLAGS: -lstdc++
#include <stdlib.h>

extern int wsi_htj2k_encode(
    const unsigned char *rgb, int width, int height,
    int quality,
    unsigned char **outbuf, size_t *outsize);
*/
import "C"
import (
	"fmt"
	"runtime"
	"strconv"
	"unsafe"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/tiff"
)

func init() {
	codec.Register(Factory{})
}

// Factory creates HTJ2K encoders and satisfies codec.EncoderFactory.
type Factory struct{}

func (Factory) Name() string { return "htj2k" }

func (Factory) NewEncoder(g codec.LevelGeometry, q codec.Quality) (codec.Encoder, error) {
	quality := 85
	if v, ok := q.Knobs["q"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			quality = n
		}
	}
	return &Encoder{quality: quality}, nil
}

// Encoder encodes HTJ2K tiles for one pyramid level.
type Encoder struct {
	quality int
}

func (*Encoder) LevelHeader() []byte        { return nil }
func (*Encoder) TIFFCompressionTag() uint16 { return tiff.CompressionHTJ2K }
func (*Encoder) TIFFPhotometric() uint16    { return codec.PhotometricRGB }
func (*Encoder) Close() error               { return nil }

// EncodeTile encodes an RGB888 tile as a raw J2K codestream using OpenJPH.
func (e *Encoder) EncodeTile(rgb []byte, w, h int, dst []byte) ([]byte, error) {
	if len(rgb) < w*h*3 {
		return nil, fmt.Errorf("codec/htj2k: rgb buffer %d < %d*%d*3", len(rgb), w, h)
	}
	var outBuf *C.uchar
	var outSize C.size_t
	rc := C.wsi_htj2k_encode(
		(*C.uchar)(unsafe.Pointer(&rgb[0])),
		C.int(w), C.int(h),
		C.int(e.quality),
		&outBuf, &outSize,
	)
	runtime.KeepAlive(rgb)
	if rc != 0 || outBuf == nil {
		return nil, fmt.Errorf("codec/htj2k: encode failed (rc=%d)", rc)
	}
	out := C.GoBytes(unsafe.Pointer(outBuf), C.int(outSize))
	C.free(unsafe.Pointer(outBuf))
	if dst != nil && cap(dst) >= len(out) {
		dst = dst[:len(out)]
		copy(dst, out)
		return dst, nil
	}
	return out, nil
}
