//go:build !nojxl

// Package jpegxl provides a libjxl-backed JPEG-XL encoder, registered with
// internal/codec under the name "jpegxl". One frame per tile; output is a
// self-contained JPEG-XL codestream (TIFF Compression=50002).
package jpegxl

/*
#cgo pkg-config: libjxl_threads libjxl
#include <stdlib.h>
#include <string.h>
#include <jxl/encode.h>
#include <jxl/color_encoding.h>
#include <jxl/thread_parallel_runner.h>
#include <jxl/types.h>

// wsi_jxl_encode encodes width*height 8-bit RGB pixels as a JPEG-XL codestream.
//
// distance: 0.0 = lossless; 1.0 = visually lossless; higher = more compression.
// effort:   1..9; higher = slower + smaller. libjxl default is 7.
//
// On success, *outbuf is a malloc'd buffer of *outsize bytes; caller frees.
// Returns 0 on success, -1 on error.
static int wsi_jxl_encode(
    const unsigned char *rgb, int width, int height,
    float distance, int effort,
    unsigned char **outbuf, size_t *outsize)
{
    *outbuf = NULL;
    *outsize = 0;

    JxlEncoder *enc = JxlEncoderCreate(NULL);
    if (!enc) return -1;

    void *runner = JxlThreadParallelRunnerCreate(NULL,
        JxlThreadParallelRunnerDefaultNumWorkerThreads());
    if (!runner) {
        JxlEncoderDestroy(enc);
        return -1;
    }
    if (JxlEncoderSetParallelRunner(enc, JxlThreadParallelRunner, runner) != JXL_ENC_SUCCESS) {
        JxlThreadParallelRunnerDestroy(runner);
        JxlEncoderDestroy(enc);
        return -1;
    }

    JxlBasicInfo info;
    JxlEncoderInitBasicInfo(&info);
    info.xsize = width;
    info.ysize = height;
    info.bits_per_sample = 8;
    info.num_color_channels = 3;
    info.alpha_bits = 0;
    info.uses_original_profile = JXL_FALSE;

    if (JxlEncoderSetBasicInfo(enc, &info) != JXL_ENC_SUCCESS) goto fail;

    JxlColorEncoding color;
    JxlColorEncodingSetToSRGB(&color, JXL_FALSE);
    if (JxlEncoderSetColorEncoding(enc, &color) != JXL_ENC_SUCCESS) goto fail;

    JxlEncoderFrameSettings *frame_settings = JxlEncoderFrameSettingsCreate(enc, NULL);
    if (!frame_settings) goto fail;
    if (JxlEncoderSetFrameDistance(frame_settings, distance) != JXL_ENC_SUCCESS) goto fail;
    if (JxlEncoderFrameSettingsSetOption(frame_settings, JXL_ENC_FRAME_SETTING_EFFORT, effort) != JXL_ENC_SUCCESS) goto fail;
    if (distance == 0.0f) {
        if (JxlEncoderSetFrameLossless(frame_settings, JXL_TRUE) != JXL_ENC_SUCCESS) goto fail;
    }

    JxlPixelFormat pixel_format;
    pixel_format.num_channels = 3;
    pixel_format.data_type = JXL_TYPE_UINT8;
    pixel_format.endianness = JXL_NATIVE_ENDIAN;
    pixel_format.align = 0;

    if (JxlEncoderAddImageFrame(frame_settings, &pixel_format, rgb,
            (size_t)width * (size_t)height * 3) != JXL_ENC_SUCCESS) goto fail;

    JxlEncoderCloseInput(enc);

    // Iteratively pull bytes from JxlEncoderProcessOutput.
    size_t cap = 65536;
    *outbuf = (unsigned char *)malloc(cap);
    if (!*outbuf) goto fail;
    *outsize = 0;

    for (;;) {
        unsigned char *next_out = *outbuf + *outsize;
        size_t avail_out = cap - *outsize;
        JxlEncoderStatus s = JxlEncoderProcessOutput(enc, &next_out, &avail_out);
        size_t written = (cap - *outsize) - avail_out;
        *outsize += written;
        if (s == JXL_ENC_SUCCESS) break;
        if (s != JXL_ENC_NEED_MORE_OUTPUT) goto fail;
        size_t newcap = cap * 2;
        unsigned char *grown = (unsigned char *)realloc(*outbuf, newcap);
        if (!grown) goto fail;
        *outbuf = grown;
        cap = newcap;
    }

    JxlThreadParallelRunnerDestroy(runner);
    JxlEncoderDestroy(enc);
    return 0;

fail:
    if (*outbuf) { free(*outbuf); *outbuf = NULL; *outsize = 0; }
    JxlThreadParallelRunnerDestroy(runner);
    JxlEncoderDestroy(enc);
    return -1;
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

// Factory creates JPEG-XL encoders.
type Factory struct{}

func (Factory) Name() string { return "jpegxl" }

func (Factory) NewEncoder(g codec.LevelGeometry, q codec.Quality) (codec.Encoder, error) {
	// Quality knob mapping: --quality 1..100 → distance.
	// quality=100 → distance=0.0 (true lossless); quality<100 maps via a simple
	// linear-ish formula. distance=1.0 is libjxl's "visually lossless" target.
	distance := float32(1.0)
	if v, ok := q.Knobs["q"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			if n >= 100 {
				distance = 0.0
			} else {
				distance = float32(15.0 * (1.0 - float32(n)/100.0))
			}
		}
	}
	if v, ok := q.Knobs["distance"]; ok {
		if d, err := strconv.ParseFloat(v, 32); err == nil {
			distance = float32(d)
		}
	}
	effort := 7
	if v, ok := q.Knobs["effort"]; ok {
		if e, err := strconv.Atoi(v); err == nil && e >= 1 && e <= 9 {
			effort = e
		}
	}
	return &Encoder{distance: distance, effort: effort}, nil
}

// Encoder encodes JPEG-XL tiles.
type Encoder struct {
	distance float32
	effort   int
}

func (*Encoder) LevelHeader() []byte        { return nil }
func (*Encoder) TIFFCompressionTag() uint16 { return tiff.CompressionJPEGXL }
func (*Encoder) TIFFPhotometric() uint16    { return codec.PhotometricRGB }
func (*Encoder) Close() error               { return nil }

// IsLossless reports whether this encoder produces byte-exact output (distance 0).
func (e *Encoder) IsLossless() bool { return e.distance == 0 }

func (e *Encoder) EncodeTile(rgb []byte, w, h int, dst []byte) ([]byte, error) {
	if len(rgb) < w*h*3 {
		return nil, fmt.Errorf("codec/jpegxl: rgb buffer %d < %d*%d*3", len(rgb), w, h)
	}
	var outBuf *C.uchar
	var outSize C.size_t

	rc := C.wsi_jxl_encode(
		(*C.uchar)(unsafe.Pointer(&rgb[0])),
		C.int(w), C.int(h),
		C.float(e.distance), C.int(e.effort),
		&outBuf, &outSize,
	)
	runtime.KeepAlive(rgb)

	if rc != 0 {
		return nil, fmt.Errorf("codec/jpegxl: wsi_jxl_encode returned %d", rc)
	}
	if outBuf == nil {
		return nil, fmt.Errorf("codec/jpegxl: nil output buffer")
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
