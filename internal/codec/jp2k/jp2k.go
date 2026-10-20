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
    int quality, int reversible, int subX, int subY,
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
	// Optional chroma subsampling (444 / 422 / 440 / 420): when set to a
	// subsampled ratio the encoder emits YCbCr + SYCC (MCT off) so a re-encode can
	// match a subsampled source's chroma; default (unset / 444) is RGB + MCT.
	subX, subY, err := parseSubsampling(q.Knobs["subsampling"])
	if err != nil {
		return nil, err
	}
	return &Encoder{quality: quality, reversible: reversible, subX: subX, subY: subY}, nil
}

// parseSubsampling maps a chroma-subsampling knob to (lumaH, lumaV) sampling
// factors (the luma:chroma ratio): 444→1,1; 422→2,1; 440→1,2; 420→2,2. Empty →
// 4:4:4 (no subsampling).
func parseSubsampling(v string) (subX, subY int, err error) {
	switch v {
	case "", "444":
		return 1, 1, nil
	case "422":
		return 2, 1, nil
	case "440":
		return 1, 2, nil
	case "420":
		return 2, 2, nil
	default:
		return 0, 0, fmt.Errorf("codec/jp2k: unknown subsampling %q (want 444|422|440|420)", v)
	}
}

func truthy(v string) bool { return v == "true" || v == "1" || v == "yes" }

// Encoder encodes JPEG 2000 tiles for one pyramid level.
type Encoder struct {
	quality    int
	reversible bool
	subX, subY int // chroma subsampling factors; 1,1 = 4:4:4 (RGB+MCT)
}

func (*Encoder) LevelHeader() []byte { return nil }

// subsampled reports whether this encoder subsamples chroma (→ YCbCr/SYCC output).
func (e *Encoder) subsampled() bool { return e.subX > 1 || e.subY > 1 }

// TIFFCompressionTag returns the Aperio JPEG 2000 code matching the output
// colorspace: 33005 (RGB) for the default RGB/sRGB+MCT codestream, or 33003
// (YCbCr) when chroma is subsampled (the encoder emits YCbCr/SYCC, which
// Aperio-family readers must YCbCr→RGB-convert). Mis-tagging 33003 for RGB output
// — or 33005 for YCbCr — makes those readers apply the wrong conversion.
// (wsitools#44/#28; requires opentile-go ≥ v0.61.0, which reads 33005.)
func (e *Encoder) TIFFCompressionTag() uint16 {
	if e.subsampled() {
		return tiff.CompressionJPEG2000 // 33003 (YCbCr convention)
	}
	return tiff.CompressionJPEG2000RGB // 33005 (RGB)
}

// TIFFPhotometric is RGB(2) in both cases — Aperio tags JP2K tiles (33003 and
// 33005 alike) PhotometricInterpretation=RGB(2); the YCbCr-ness of a 33003 tile
// is carried by the compression code, not the photometric tag.
func (*Encoder) TIFFPhotometric() uint16 { return codec.PhotometricRGB }
func (*Encoder) Close() error            { return nil }

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
		C.int(e.subX), C.int(e.subY),
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
