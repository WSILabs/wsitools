//go:build !nocgo

// Package jpeg provides a vanilla libjpeg-turbo-backed JPEG encoder.
// Output is YCbCr+4:2:0 chroma-subsampled JPEG with embedded
// DQT/DHT tables when EncodeStandalone is used, or abbreviated
// tiles (no tables; expected to be combined with LevelHeader via
// TIFF tag 347) when EncodeTile is used.
//
// No Adobe APP14 marker; no raw-RGB storage. The Aperio APP14
// variant lives in internal/codec/aperioapp14.
package jpeg

/*
#cgo pkg-config: libturbojpeg
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <jpeglib.h>

static int wsi_encode_vanilla(
    const unsigned char *rgb, int w, int h, int quality, int abbreviated,
    unsigned char **outBuf, unsigned long *outSize) {

    struct jpeg_compress_struct cinfo;
    struct jpeg_error_mgr jerr;
    cinfo.err = jpeg_std_error(&jerr);
    jpeg_create_compress(&cinfo);

    *outBuf = NULL;
    *outSize = 0;
    jpeg_mem_dest(&cinfo, outBuf, outSize);

    cinfo.image_width = w;
    cinfo.image_height = h;
    cinfo.input_components = 3;
    cinfo.in_color_space = JCS_RGB;
    jpeg_set_defaults(&cinfo);                // → YCbCr output, 4:2:0
    jpeg_set_quality(&cinfo, quality, TRUE);

    if (abbreviated) {
        jpeg_suppress_tables(&cinfo, TRUE);
    }
    jpeg_start_compress(&cinfo, !abbreviated);

    JSAMPROW row_pointer[1];
    int row_stride = w * 3;
    while (cinfo.next_scanline < cinfo.image_height) {
        row_pointer[0] = (JSAMPROW)(rgb + cinfo.next_scanline * row_stride);
        jpeg_write_scanlines(&cinfo, row_pointer, 1);
    }

    jpeg_finish_compress(&cinfo);
    jpeg_destroy_compress(&cinfo);
    return 0;
}

static int wsi_compute_tables(int quality, unsigned char **outBuf, unsigned long *outSize) {
    struct jpeg_compress_struct cinfo;
    struct jpeg_error_mgr jerr;
    cinfo.err = jpeg_std_error(&jerr);
    jpeg_create_compress(&cinfo);

    *outBuf = NULL;
    *outSize = 0;
    jpeg_mem_dest(&cinfo, outBuf, outSize);

    cinfo.image_width = 16;
    cinfo.image_height = 16;
    cinfo.input_components = 3;
    cinfo.in_color_space = JCS_RGB;
    jpeg_set_defaults(&cinfo);
    jpeg_set_quality(&cinfo, quality, TRUE);

    jpeg_write_tables(&cinfo);
    jpeg_destroy_compress(&cinfo);
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

func (Factory) Name() string { return "jpeg" }

func (Factory) NewEncoder(g codec.LevelGeometry, q codec.Quality) (codec.Encoder, error) {
	return New(g, q)
}

type Encoder struct {
	geometry codec.LevelGeometry
	quality  int
	tables   []byte
}

func New(g codec.LevelGeometry, q codec.Quality) (*Encoder, error) {
	e := &Encoder{geometry: g, quality: 85}
	if v, ok := q.Knobs["q"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			e.quality = n
		}
	}
	if err := e.computeTables(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Encoder) LevelHeader() []byte        { return e.tables }
func (e *Encoder) TIFFCompressionTag() uint16 { return tiff.CompressionJPEG }
func (e *Encoder) Close() error               { return nil }

func (e *Encoder) computeTables() error {
	var buf *C.uchar
	var size C.ulong
	if ret := C.wsi_compute_tables(C.int(e.quality), &buf, &size); ret != 0 {
		return fmt.Errorf("codec/jpeg: compute_tables failed (ret=%d)", int(ret))
	}
	defer C.free(unsafe.Pointer(buf))
	e.tables = C.GoBytes(unsafe.Pointer(buf), C.int(size))
	return nil
}

// EncodeTile encodes rgb as an abbreviated JPEG tile (no DQT/DHT).
// Combine with LevelHeader via TIFF tag 347 for decodable output.
func (e *Encoder) EncodeTile(rgb []byte, w, h int, dst []byte) ([]byte, error) {
	out, err := e.encodeRaw(rgb, w, h, true)
	if err != nil {
		return nil, fmt.Errorf("codec/jpeg: EncodeTile: %w", err)
	}
	if dst != nil && cap(dst) >= len(out) {
		dst = dst[:len(out)]
		copy(dst, out)
		return dst, nil
	}
	return out, nil
}

// EncodeStandalone encodes rgb as a complete self-contained JPEG
// (SOI + DQT + DHT + SOS + scan + EOI). Used for DZI/SZI per-tile.
func (e *Encoder) EncodeStandalone(rgb []byte, w, h int) ([]byte, error) {
	out, err := e.encodeRaw(rgb, w, h, false)
	if err != nil {
		return nil, fmt.Errorf("codec/jpeg: EncodeStandalone: %w", err)
	}
	return out, nil
}

func (e *Encoder) encodeRaw(rgb []byte, w, h int, abbreviated bool) ([]byte, error) {
	if len(rgb) < w*h*3 {
		return nil, fmt.Errorf("codec/jpeg: rgb buffer too small (have %d, need %d)", len(rgb), w*h*3)
	}
	var outBuf *C.uchar
	var outSize C.ulong

	abbr := C.int(0)
	if abbreviated {
		abbr = 1
	}
	ret := C.wsi_encode_vanilla(
		(*C.uchar)(unsafe.Pointer(&rgb[0])),
		C.int(w), C.int(h),
		C.int(e.quality), abbr,
		&outBuf, &outSize,
	)
	runtime.KeepAlive(rgb)
	if ret != 0 {
		return nil, fmt.Errorf("codec/jpeg: encode failed (ret=%d)", int(ret))
	}
	defer C.free(unsafe.Pointer(outBuf))
	return C.GoBytes(unsafe.Pointer(outBuf), C.int(outSize)), nil
}
