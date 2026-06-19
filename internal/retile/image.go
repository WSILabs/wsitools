package retile

import "sync"

// RGBImage is a 3-byte-per-pixel RGB image. Pixel (x,y) lives at
// Pix[y*Stride + x*3]. Source pixels arrive from opentile-go as RGB and
// libjpeg-turbo accepts RGB, so the pipeline carries RGB throughout.
type RGBImage struct {
	Pix    []byte // length = H * Stride
	Stride int    // = W * 3
	W, H   int
}

// rgbPixPool pools the backing []byte for RGBImage tile destinations produced
// by assembleTile and consumed by the encoder. Ownership: assembleTile borrows;
// the encoder releases after EncodeTile returns.
var rgbPixPool = sync.Pool{
	New: func() any { b := make([]byte, 0, 258*258*3); return &b },
}

// newPooledRGB returns an *RGBImage whose Pix is borrowed from rgbPixPool.
// Callers must release via releaseRGB after the last read of Pix. The borrowed
// slice is not zeroed — callers must fully overwrite every byte.
func newPooledRGB(w, h int) *RGBImage {
	need := w * h * 3
	b := *(rgbPixPool.Get().(*[]byte))
	if cap(b) < need {
		b = make([]byte, need)
	} else {
		b = b[:need]
	}
	return &RGBImage{Pix: b, Stride: w * 3, W: w, H: h}
}

// releaseRGB returns img.Pix to the pool. Caller must not reference img after.
func releaseRGB(img *RGBImage) {
	b := img.Pix[:0]
	rgbPixPool.Put(&b)
}
