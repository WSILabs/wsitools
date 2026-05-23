// Package jpeg provides a libjpeg-turbo-backed JPEG encoder that produces
// Aperio-compatible abbreviated tiles (TIFF tag 347 JPEGTables + per-tile
// abbreviated JPEG streams with Adobe APP14 RGB colorspace marker).
package jpeg

/*
#cgo pkg-config: libjpeg
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <jpeglib.h>

// adobeAPP14Payload: 12-byte APP14 payload (no FF EE or length prefix —
// jpeg_write_marker writes those itself).
static const JOCTET wsi_app14_payload[] = {
    0x41, 0x64, 0x6F, 0x62, 0x65, // "Adobe"
    0x00, 0x64,                   // DCTEncodeVersion = 100
    0x80, 0x00,                   // APP14Flags0 = 0x8000
    0x00, 0x00,                   // APP14Flags1 = 0
    0x00,                         // ColorTransform = 0 (RGB)
};

// wsi_encode encodes width*height 8-bit RGB pixels into a JPEG stream.
//
// abbreviated=0: full self-contained JPEG (SOI + DQT + DHT + SOF + SOS + scan + EOI).
// abbreviated=1: abbreviated JPEG (SOI + APP14 + SOS + scan + EOI); tables omitted.
//
// On success, *outbuf points to a malloc'd buffer of *outsize bytes containing
// the JPEG stream. The caller must free(*outbuf).
// Returns 0 on success, -1 on error.
//
// KNOWN LIMITATION: jpeg_std_error's default error_exit calls exit(1) on any
// libjpeg error; install a custom handler for production use (follow-up task).
static int wsi_encode(
    const unsigned char *rgb, int width, int height,
    int quality, int abbreviated,
    unsigned char **outbuf, unsigned long *outsize)
{
    struct jpeg_compress_struct cinfo;
    struct jpeg_error_mgr jerr;

    cinfo.err = jpeg_std_error(&jerr);
    jpeg_create_compress(&cinfo);

    *outbuf = NULL;
    *outsize = 0;
    jpeg_mem_dest(&cinfo, outbuf, outsize);

    cinfo.image_width = (JDIMENSION)width;
    cinfo.image_height = (JDIMENSION)height;
    cinfo.input_components = 3;
    cinfo.in_color_space = JCS_RGB;
    jpeg_set_defaults(&cinfo);
    // Override the default JCS_YCbCr storage with JCS_RGB so pixels are
    // encoded raw RGB without the YCbCr conversion. This matches what the
    // Adobe APP14 marker (ColorTransform=0) declares to decoders, and is
    // what real Aperio SVS files do. libjpeg's jpeg_set_colorspace updates
    // component sampling factors, quant table targets, and Huffman tables
    // for the new colorspace — must be called AFTER jpeg_set_defaults and
    // BEFORE jpeg_set_quality.
    //
    // Decoders that honor APP14 (libjpeg-turbo, openslide, QuPath, libvips)
    // skip the inverse YCbCr transform and produce the original RGB. The Go
    // stdlib JPEG decoder does NOT honor APP14 and will produce hue-rotated
    // output — which is why this codec's round-trip tests decode via
    // libjpeg-turbo (tjDecompress2) rather than image/jpeg.
    jpeg_set_colorspace(&cinfo, JCS_RGB);
    jpeg_set_quality(&cinfo, quality, TRUE);

    if (abbreviated) {
        jpeg_suppress_tables(&cinfo, TRUE);
        jpeg_start_compress(&cinfo, FALSE); // write_all_tables = FALSE
        // Write Adobe APP14 RGB marker (APP0+14 = 0xEE).
        jpeg_write_marker(&cinfo, JPEG_APP0 + 14,
            wsi_app14_payload, sizeof(wsi_app14_payload));
    } else {
        jpeg_start_compress(&cinfo, TRUE); // write_all_tables = TRUE
    }

    // Feed scanlines one row at a time.
    while (cinfo.next_scanline < cinfo.image_height) {
        const unsigned char *row = rgb + cinfo.next_scanline * width * 3;
        JSAMPROW rowptr = (JSAMPROW)row;
        jpeg_write_scanlines(&cinfo, &rowptr, 1);
    }

    jpeg_finish_compress(&cinfo);
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

// Factory creates JPEG encoders and satisfies codec.EncoderFactory.
type Factory struct{}

func (Factory) Name() string { return "jpeg" }

func (Factory) NewEncoder(g codec.LevelGeometry, q codec.Quality) (codec.Encoder, error) {
	quality := 85
	if v, ok := q.Knobs["q"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			quality = n
		}
	}
	enc := &Encoder{quality: quality, geom: g}
	if err := enc.computeTables(); err != nil {
		return nil, err
	}
	return enc, nil
}

// Encoder encodes JPEG tiles for one pyramid level.
// tables holds the shared JPEGTables (SOI + DQT + DHT + EOI) computed once
// from a probe tile; each subsequent EncodeTile call produces an abbreviated
// stream (SOI + APP14 + SOS + scan data + EOI).
type Encoder struct {
	quality int
	geom    codec.LevelGeometry
	tables  []byte
}

func (e *Encoder) LevelHeader() []byte        { return e.tables }
func (e *Encoder) TIFFCompressionTag() uint16 { return tiff.CompressionJPEG }
func (e *Encoder) Close() error               { return nil }

// computeTables encodes a blank probe tile as a self-contained JPEG, then
// extracts the DQT/DHT tables to form the shared JPEGTables for this level.
func (e *Encoder) computeTables() error {
	probe := make([]byte, e.geom.TileWidth*e.geom.TileHeight*3)
	full, err := e.encodeRaw(probe, e.geom.TileWidth, e.geom.TileHeight, false)
	if err != nil {
		return fmt.Errorf("codec/jpeg: probe encode: %w", err)
	}
	tables, err := tiff.ExtractJPEGTables(full)
	if err != nil {
		return fmt.Errorf("codec/jpeg: extract tables: %w", err)
	}
	e.tables = tables
	return nil
}

// EncodeTile encodes rgb as an abbreviated JPEG tile (no DQT/DHT tables).
// The returned bytes are a valid JPEG when combined with LevelHeader via
// TIFF tag 347 (JPEGTables) or by splicing the tables directly before SOS.
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

// encodeRaw calls the C wsi_encode helper which drives the libjpeg compress loop.
//
// abbreviated=false: full self-contained JPEG for probe (used to extract tables).
// abbreviated=true:  abbreviated JPEG tile with Adobe APP14 RGB marker.
//
// All libjpeg state (jpeg_compress_struct, jpeg_error_mgr) lives in C memory
// inside wsi_encode; we only cross the cgo boundary with a plain data pointer
// (rgb slice backing array) and output-buffer double-pointer (both C-allocated),
// avoiding Go-pointer-to-Go-pointer violations.
//
// KNOWN LIMITATION: jpeg_std_error's default error_exit calls exit(1) on any
// libjpeg error. For production use, install a custom C error handler that
// longjmp()s out instead — flagged as a follow-up concern.
func (e *Encoder) encodeRaw(rgb []byte, w, h int, abbreviated bool) ([]byte, error) {
	var outBuf *C.uchar
	var outSize C.ulong

	abbr := C.int(0)
	if abbreviated {
		abbr = 1
	}

	ret := C.wsi_encode(
		(*C.uchar)(unsafe.Pointer(&rgb[0])),
		C.int(w), C.int(h),
		C.int(e.quality), abbr,
		&outBuf, &outSize,
	)

	// Keep rgb alive until after the cgo call returns.
	runtime.KeepAlive(rgb)

	if ret != 0 {
		return nil, fmt.Errorf("codec/jpeg: wsi_encode returned %d", ret)
	}
	if outBuf == nil {
		return nil, fmt.Errorf("codec/jpeg: wsi_encode produced nil output")
	}

	// Copy C-malloc'd buffer into Go memory, then free the C buffer.
	result := C.GoBytes(unsafe.Pointer(outBuf), C.int(outSize))
	C.free(unsafe.Pointer(outBuf))

	return result, nil
}
