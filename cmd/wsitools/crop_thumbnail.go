package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"

	otdecoder "github.com/wsilabs/opentile-go/decoder"
	otresample "github.com/wsilabs/opentile-go/resample"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// thumbLongSide is the target longest-edge length for a regenerated thumbnail.
const thumbLongSide = 1024

// thumbDims returns thumbnail dimensions preserving aspect, with the longest
// side scaled to longSide. It never upscales (a source smaller than longSide is
// returned unchanged).
func thumbDims(srcW, srcH, longSide int) (int, int) {
	if srcW <= longSide && srcH <= longSide {
		return srcW, srcH
	}
	if srcW >= srcH {
		h := int(float64(longSide)*float64(srcH)/float64(srcW) + 0.5)
		if h < 1 {
			h = 1
		}
		return longSide, h
	}
	w := int(float64(longSide)*float64(srcW)/float64(srcH) + 0.5)
	if w < 1 {
		w = 1
	}
	return w, longSide
}

// regenCropThumbnail renders a thumbnail from the cropped L0 raster (RGB888,
// l0W×l0H) at the crop's aspect ratio and writes it as a single-strip baseline
// JPEG associated IFD (type "thumbnail", NewSubfileType=0). The complete JFIF
// decodes via opentile-go's associated Bytes()→ViaCodec path.
func regenCropThumbnail(w *streamwriter.Writer, l0 []byte, l0W, l0H, quality int) error {
	tw, th := thumbDims(l0W, l0H, thumbLongSide)

	src := &otdecoder.Image{
		Width:  l0W,
		Height: l0H,
		Stride: l0W * 3,
		Format: otdecoder.PixelFormatRGB,
		Pix:    l0,
	}
	dst := otdecoder.NewImageFormat(tw, th, otdecoder.PixelFormatRGB)
	if err := otresample.ImageInto(src, dst, otresample.Box); err != nil {
		return fmt.Errorf("thumbnail downscale: %w", err)
	}

	// Pack into image.RGBA for the stdlib JPEG encoder (complete JFIF baseline).
	img := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		for x := 0; x < tw; x++ {
			o := y*dst.Stride + x*3
			p := y*img.Stride + x*4
			img.Pix[p] = dst.Pix[o]
			img.Pix[p+1] = dst.Pix[o+1]
			img.Pix[p+2] = dst.Pix[o+2]
			img.Pix[p+3] = 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return fmt.Errorf("thumbnail encode: %w", err)
	}

	return w.AddStripped(streamwriter.StrippedSpec{
		Width:           uint32(tw),
		Height:          uint32(th),
		RowsPerStrip:    uint32(th),
		BitsPerSample:   []uint16{8, 8, 8},
		SamplesPerPixel: 3,
		Photometric:     6, // YCbCr (stdlib JFIF); cosmetic for opentile decode
		Compression:     tiff.CompressionJPEG,
		StripBytes:      buf.Bytes(),
		NewSubfileType:  0,
		WSIImageType:    tiff.WSIImageTypeThumbnail,
	})
}
