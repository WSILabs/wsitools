//go:build !noavif

// Package avif provides a libavif-backed AVIF encoder, registered with
// internal/codec under the name "avif". One AVIF still per tile; output
// uses TIFF Compression=60001 (wsitools-private — no standardised TIFF
// tag for AVIF as of 2026-05).
package avif

/*
#cgo pkg-config: libavif
#include <stdlib.h>
#include <string.h>
#include <avif/avif.h>

// wsi_avif_encode encodes width*height 8-bit RGB pixels as one AVIF still.
// On success, *outbuf is malloc'd; caller frees.
// quality: 1..100 (higher = better quality / larger files).
// speed:   0..10 (0 = slowest/best, 10 = fastest/worst).
// Returns 0 on success, -1 on error.
static int wsi_avif_encode(
    const unsigned char *rgb, int width, int height,
    int quality, int speed,
    unsigned char **outbuf, size_t *outsize)
{
    *outbuf = NULL;
    *outsize = 0;

    avifResult r;
    avifImage *image = avifImageCreate(width, height, 8, AVIF_PIXEL_FORMAT_YUV444);
    if (!image) return -1;

    avifRGBImage rgb_img;
    avifRGBImageSetDefaults(&rgb_img, image);
    rgb_img.format = AVIF_RGB_FORMAT_RGB;
    rgb_img.depth = 8;
    rgb_img.pixels = (uint8_t *)rgb;
    rgb_img.rowBytes = (uint32_t)(width * 3);

    r = avifImageRGBToYUV(image, &rgb_img);
    if (r != AVIF_RESULT_OK) {
        avifImageDestroy(image);
        return -1;
    }

    avifEncoder *encoder = avifEncoderCreate();
    if (!encoder) {
        avifImageDestroy(image);
        return -1;
    }
    encoder->quality = quality;
    encoder->qualityAlpha = quality;
    encoder->speed = speed;

    avifRWData output = AVIF_DATA_EMPTY;
    r = avifEncoderWrite(encoder, image, &output);
    avifEncoderDestroy(encoder);
    avifImageDestroy(image);

    if (r != AVIF_RESULT_OK) {
        avifRWDataFree(&output);
        return -1;
    }

    *outbuf = (unsigned char *)malloc(output.size);
    if (!*outbuf) {
        avifRWDataFree(&output);
        return -1;
    }
    memcpy(*outbuf, output.data, output.size);
    *outsize = output.size;
    avifRWDataFree(&output);
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

func (Factory) Name() string { return "avif" }

func (Factory) NewEncoder(g codec.LevelGeometry, q codec.Quality) (codec.Encoder, error) {
	quality := 85
	if v, ok := q.Knobs["q"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			quality = n
		}
	}
	speed := 6
	if v, ok := q.Knobs["speed"]; ok {
		if s, err := strconv.Atoi(v); err == nil && s >= 0 && s <= 10 {
			speed = s
		}
	}
	return &Encoder{quality: quality, speed: speed}, nil
}

type Encoder struct {
	quality int
	speed   int
}

func (*Encoder) LevelHeader() []byte        { return nil }
func (*Encoder) TIFFCompressionTag() uint16 { return tiff.CompressionAVIF }
func (*Encoder) Close() error               { return nil }

func (e *Encoder) EncodeTile(rgb []byte, w, h int, dst []byte) ([]byte, error) {
	if len(rgb) < w*h*3 {
		return nil, fmt.Errorf("codec/avif: rgb buffer %d < %d*%d*3", len(rgb), w, h)
	}
	var outBuf *C.uchar
	var outSize C.size_t

	rc := C.wsi_avif_encode(
		(*C.uchar)(unsafe.Pointer(&rgb[0])),
		C.int(w), C.int(h),
		C.int(e.quality), C.int(e.speed),
		&outBuf, &outSize,
	)
	runtime.KeepAlive(rgb)

	if rc != 0 {
		return nil, fmt.Errorf("codec/avif: wsi_avif_encode returned %d", rc)
	}
	if outBuf == nil {
		return nil, fmt.Errorf("codec/avif: nil output buffer")
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
