//go:build !nowebp

// Package webp provides a libwebp-backed WebP encoder.
package webp

/*
#cgo pkg-config: libwebp
#include <stdlib.h>
#include <webp/encode.h>

// wsi_webp_encode encodes RGB888 pixels as WebP.
//
// quality: 1..100; only used in lossy mode.
// lossless != 0: use WebPEncodeLosslessRGB instead of WebPEncodeRGB.
//
// On success, *outbuf points to a libwebp-allocated buffer; caller must
// WebPFree(*outbuf). *outsize is the byte count.
// Returns 0 on success, -1 on error.
static int wsi_webp_encode(
    const unsigned char *rgb, int width, int height,
    int quality, int lossless,
    unsigned char **outbuf, size_t *outsize)
{
    *outbuf = NULL;
    *outsize = 0;

    uint8_t *libwebp_out = NULL;
    size_t out_size = 0;
    int stride = width * 3;
    if (lossless) {
        out_size = WebPEncodeLosslessRGB(rgb, width, height, stride, &libwebp_out);
    } else {
        out_size = WebPEncodeRGB(rgb, width, height, stride, (float)quality, &libwebp_out);
    }
    if (out_size == 0 || libwebp_out == NULL) {
        if (libwebp_out) WebPFree(libwebp_out);
        return -1;
    }
    *outbuf = libwebp_out; // caller WebPFrees
    *outsize = out_size;
    return 0;
}
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

type Factory struct{}

func (Factory) Name() string { return "webp" }

func (Factory) NewEncoder(g codec.LevelGeometry, q codec.Quality) (codec.Encoder, error) {
	quality := 85
	if v, ok := q.Knobs["q"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			quality = n
		}
	}
	lossless := false
	if v, ok := q.Knobs["lossless"]; ok && v == "true" {
		lossless = true
	}
	return &Encoder{quality: quality, lossless: lossless}, nil
}

type Encoder struct {
	quality  int
	lossless bool
}

func (*Encoder) LevelHeader() []byte        { return nil }
func (*Encoder) TIFFCompressionTag() uint16 { return tiff.CompressionWebP }
func (*Encoder) Close() error               { return nil }

// IsLossless reports whether this encoder produces byte-exact output.
func (e *Encoder) IsLossless() bool { return e.lossless }

func (e *Encoder) EncodeTile(rgb []byte, w, h int, dst []byte) ([]byte, error) {
	if len(rgb) < w*h*3 {
		return nil, fmt.Errorf("codec/webp: rgb buffer %d < %d*%d*3", len(rgb), w, h)
	}
	var outBuf *C.uchar
	var outSize C.size_t
	losslessFlag := 0
	if e.lossless {
		losslessFlag = 1
	}
	rc := C.wsi_webp_encode(
		(*C.uchar)(unsafe.Pointer(&rgb[0])),
		C.int(w), C.int(h),
		C.int(e.quality), C.int(losslessFlag),
		&outBuf, &outSize,
	)
	runtime.KeepAlive(rgb)
	if rc != 0 || outBuf == nil {
		return nil, fmt.Errorf("codec/webp: encode failed (rc=%d)", rc)
	}
	out := C.GoBytes(unsafe.Pointer(outBuf), C.int(outSize))
	C.WebPFree(unsafe.Pointer(outBuf))
	if dst != nil && cap(dst) >= len(out) {
		dst = dst[:len(out)]
		copy(dst, out)
		return dst, nil
	}
	return out, nil
}
