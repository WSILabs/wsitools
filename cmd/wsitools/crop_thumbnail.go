package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"

	opentile "github.com/wsilabs/opentile-go"
	otdecoder "github.com/wsilabs/opentile-go/decoder"
	otresample "github.com/wsilabs/opentile-go/resample"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// croppedThumbnail is a synthetic source.AssociatedImage holding a JPEG
// thumbnail rendered from a crop's L0 raster, so a cropped DICOM pyramid carries
// a thumbnail of the CROP region rather than the verbatim whole-slide thumbnail.
// Its codec is JPEG, so the DICOM writer emits it via the verbatim-encapsulated
// path (Bytes()) without ever calling Decode.
type croppedThumbnail struct {
	jpegBytes []byte
	w, h      int
}

var _ source.AssociatedImage = (*croppedThumbnail)(nil)

func (c *croppedThumbnail) Type() string                    { return "thumbnail" }
func (c *croppedThumbnail) Size() image.Point               { return image.Point{X: c.w, Y: c.h} }
func (c *croppedThumbnail) Compression() source.Compression { return source.CompressionJPEG }
func (c *croppedThumbnail) Bytes() ([]byte, error)          { return c.jpegBytes, nil }
func (c *croppedThumbnail) Decode(otdecoder.DecodeOptions) (*otdecoder.Image, error) {
	return nil, fmt.Errorf("croppedThumbnail: Decode unsupported (emitted as JPEG via Bytes)")
}
func (c *croppedThumbnail) Source() (opentile.AssociatedEncoding, bool) {
	return opentile.AssociatedEncoding{}, false
}
func (c *croppedThumbnail) IFDOffset() (int64, bool) { return 0, false }

// regenCropThumbnailAssoc returns assoc with any thumbnail entry replaced by a
// croppedThumbnail rendered from the crop L0 raster (l0/l0W/l0H); all other
// associated images pass through unchanged. Used by cropToDICOM so a cropped
// DICOM pyramid's thumbnail reflects the crop region, not the whole slide —
// mirroring the TIFF-family crop's regenCropThumbnail. If the source has no
// thumbnail, the list is returned unchanged.
func regenCropThumbnailAssoc(assoc []source.AssociatedImage, l0 []byte, l0W, l0H, quality int) ([]source.AssociatedImage, error) {
	out := make([]source.AssociatedImage, 0, len(assoc))
	for _, a := range assoc {
		if a.Type() == string(opentile.AssociatedThumbnail) {
			jpegBytes, tw, th, err := renderCropThumbnail(l0, l0W, l0H, quality)
			if err != nil {
				return nil, err
			}
			out = append(out, &croppedThumbnail{jpegBytes: jpegBytes, w: tw, h: th})
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

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

// renderCropThumbnail box-downscales the cropped L0 to a thumbnail (longest side
// thumbLongSide, aspect preserved) and returns the encoded baseline-JPEG bytes
// and its dimensions. Writer-agnostic.
func renderCropThumbnail(l0 []byte, l0W, l0H, quality int) (jpegBytes []byte, tw, th int, err error) {
	tw, th = thumbDims(l0W, l0H, thumbLongSide)
	src := &otdecoder.Image{Width: l0W, Height: l0H, Stride: l0W * 3, Format: otdecoder.PixelFormatRGB, Pix: l0}
	dst := otdecoder.NewImageFormat(tw, th, otdecoder.PixelFormatRGB)
	if err = otresample.ImageInto(src, dst, otresample.Box); err != nil {
		return nil, 0, 0, fmt.Errorf("thumbnail downscale: %w", err)
	}
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
	if err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, fmt.Errorf("thumbnail encode: %w", err)
	}
	return buf.Bytes(), tw, th, nil
}

// regenCropThumbnail renders a thumbnail from the cropped L0 raster (RGB888,
// l0W×l0H) at the crop's aspect ratio and writes it as a single-strip baseline
// JPEG associated IFD (type "thumbnail", NewSubfileType=0). The complete JFIF
// decodes via opentile-go's associated Bytes()→ViaCodec path.
func regenCropThumbnail(w *streamwriter.Writer, l0 []byte, l0W, l0H, quality int) error {
	jpegBytes, tw, th, err := renderCropThumbnail(l0, l0W, l0H, quality)
	if err != nil {
		return err
	}
	return w.AddStripped(streamwriter.StrippedSpec{
		Width:           uint32(tw),
		Height:          uint32(th),
		RowsPerStrip:    uint32(th),
		BitsPerSample:   []uint16{8, 8, 8},
		SamplesPerPixel: 3,
		Photometric:     6, // YCbCr (stdlib JFIF); cosmetic for opentile decode
		Compression:     tiff.CompressionJPEG,
		StripBytes:      jpegBytes,
		NewSubfileType:  0,
		WSIImageType:    tiff.WSIImageTypeThumbnail,
	})
}

// regenCropThumbnailCOGWSI emits a regenerated thumbnail into a cogwsiwriter.
func regenCropThumbnailCOGWSI(w *cogwsiwriter.Writer, l0 []byte, l0W, l0H, quality int) error {
	jpegBytes, tw, th, err := renderCropThumbnail(l0, l0W, l0H, quality)
	if err != nil {
		return err
	}
	return w.AddAssociated(cogwsiwriter.AssociatedSpec{
		Type:            tiff.WSIImageTypeThumbnail,
		Width:           uint32(tw),
		Height:          uint32(th),
		Compression:     tiff.CompressionJPEG,
		Photometric:     6,
		BitsPerSample:   []uint16{8, 8, 8},
		SamplesPerPixel: 3,
		Bytes:           jpegBytes,
		RowsPerStrip:    uint32(th),
	})
}
