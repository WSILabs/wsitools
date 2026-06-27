//go:build !nojp2k

// Package jp2k provides an OpenJPEG-backed JPEG 2000 encoder (raw J2K
// codestream). It is the re-encode counterpart to opentile-go's JP2K decoder,
// closing the "JP2K is decode/tile-copy only" gap (survey B1): JPEG 2000 can now
// be a `--codec` re-encode target (lossy 9/7 or, with reversible=true, lossless
// 5/3).
package jp2k

/*
#cgo pkg-config: libopenjp2
#include <stdlib.h>

extern int wsi_jp2k_encode(
    const unsigned char *rgb, int width, int height,
    int quality, int reversible,
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

// Factory creates JPEG 2000 encoders and satisfies codec.EncoderFactory.
type Factory struct{}

func (Factory) Name() string { return "jpeg2000" }

func (Factory) NewEncoder(_ codec.LevelGeometry, q codec.Quality) (codec.Encoder, error) {
	quality := 85
	if v, ok := q.Knobs["q"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			quality = n
		}
	}
	// reversible (alias: lossless) selects the 5/3 reversible wavelet → byte-exact
	// round-trip. Default is lossy 9/7.
	reversible := truthy(q.Knobs["reversible"]) || truthy(q.Knobs["lossless"])
	return &Encoder{quality: quality, reversible: reversible}, nil
}

func truthy(v string) bool { return v == "true" || v == "1" || v == "yes" }

// Encoder encodes JPEG 2000 tiles for one pyramid level.
type Encoder struct {
	quality    int
	reversible bool
}

func (*Encoder) LevelHeader() []byte        { return nil }
func (*Encoder) TIFFCompressionTag() uint16 { return tiff.CompressionJPEG2000 }
func (*Encoder) TIFFPhotometric() uint16    { return codec.PhotometricRGB }
func (*Encoder) Close() error               { return nil }

// IsLossless reports whether this encoder produces byte-exact (reversible 5/3) output.
func (e *Encoder) IsLossless() bool { return e.reversible }

// EncodeTile encodes an RGB888 tile as a raw J2K codestream via OpenJPEG.
func (e *Encoder) EncodeTile(rgb []byte, w, h int, dst []byte) ([]byte, error) {
	if len(rgb) < w*h*3 {
		return nil, fmt.Errorf("codec/jp2k: rgb buffer %d < %d*%d*3", len(rgb), w, h)
	}
	rev := 0
	if e.reversible {
		rev = 1
	}
	var outBuf *C.uchar
	var outSize C.size_t
	rc := C.wsi_jp2k_encode(
		(*C.uchar)(unsafe.Pointer(&rgb[0])),
		C.int(w), C.int(h),
		C.int(e.quality), C.int(rev),
		&outBuf, &outSize,
	)
	runtime.KeepAlive(rgb)
	if rc != 0 || outBuf == nil {
		return nil, fmt.Errorf("codec/jp2k: encode failed (rc=%d)", rc)
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
