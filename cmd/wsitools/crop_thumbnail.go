package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"io"

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

// replaceThumbnailAssoc returns assoc with any thumbnail entry replaced by a
// croppedThumbnail built from already-encoded JPEG bytes (tw×th); all other
// associated images pass through unchanged. It mirrors regenCropThumbnailAssoc
// but sources the bytes from streamCropThumbnail (the engine crop path) instead
// of rendering from an in-memory L0 raster. If the source has no thumbnail, the
// list is returned unchanged.
func replaceThumbnailAssoc(assoc []source.AssociatedImage, jpegBytes []byte, tw, th int) []source.AssociatedImage {
	out := make([]source.AssociatedImage, 0, len(assoc))
	for _, a := range assoc {
		if a.Type() == string(opentile.AssociatedThumbnail) {
			out = append(out, &croppedThumbnail{jpegBytes: jpegBytes, w: tw, h: th})
			continue
		}
		out = append(out, a)
	}
	return out
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
	// Pack dst into a tight tw×th RGB buffer (dst.Stride may exceed tw*3).
	rgb := make([]byte, tw*th*3)
	for y := 0; y < th; y++ {
		copy(rgb[y*tw*3:(y+1)*tw*3], dst.Pix[y*dst.Stride:y*dst.Stride+tw*3])
	}
	jpegBytes, err = encodeThumbnailJPEG(rgb, tw, th, quality)
	if err != nil {
		return nil, 0, 0, err
	}
	return jpegBytes, tw, th, nil
}

// encodeThumbnailJPEG copies a tw×th RGB raster (stride tw*3) into an RGBA image
// and JPEG-encodes it at the given quality.
func encodeThumbnailJPEG(rgb []byte, tw, th, quality int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		for x := 0; x < tw; x++ {
			o := y*tw*3 + x*3
			p := y*img.Stride + x*4
			img.Pix[p] = rgb[o]
			img.Pix[p+1] = rgb[o+1]
			img.Pix[p+2] = rgb[o+2]
			img.Pix[p+3] = 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("thumbnail encode: %w", err)
	}
	return buf.Bytes(), nil
}

// streamCropThumbnail regenerates the crop thumbnail WITHOUT a full raster: it
// reads the crop rect downscaled directly to thumbnail dims via ScaledStrips
// (Box), then JPEG-encodes. ew,eh are the crop L0 dims (for sizing). Used by the
// streaming (engine) crop path, which holds no decoded raster.
func streamCropThumbnail(slide *opentile.Slide, rect opentile.Region, ew, eh, quality int) (jpegBytes []byte, tw, th int, err error) {
	tw, th = thumbDims(ew, eh, thumbLongSide)
	it := slide.Pyramid(0).ScaledStrips(rect, opentile.Size{W: tw, H: th}, th, opentile.WithStripKernel(otresample.Box))
	defer it.Close()
	rgb := make([]byte, tw*th*3)
	filled := 0
	for filled < th {
		img, nerr := it.Next()
		if nerr == io.EOF {
			break
		}
		if nerr != nil {
			return nil, 0, 0, fmt.Errorf("thumbnail region read: %w", nerr)
		}
		bpp := 3
		if img.Format == otdecoder.PixelFormatRGBA {
			bpp = 4
		}
		rows := img.Height
		for y := 0; y < rows && filled < th; y++ {
			srow := img.Pix[y*img.Stride:]
			drow := rgb[filled*tw*3:]
			for x := 0; x < tw; x++ {
				drow[x*3+0] = srow[x*bpp+0]
				drow[x*3+1] = srow[x*bpp+1]
				drow[x*3+2] = srow[x*bpp+2]
			}
			filled++
		}
	}
	if filled != th {
		return nil, 0, 0, fmt.Errorf("thumbnail short read: got %d of %d rows", filled, th)
	}
	jpegBytes, err = encodeThumbnailJPEG(rgb, tw, th, quality)
	if err != nil {
		return nil, 0, 0, err
	}
	return jpegBytes, tw, th, nil
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
	return addCropThumbnailStripped(w, jpegBytes, tw, th)
}

// addCropThumbnailStripped writes a pre-encoded baseline-JPEG crop thumbnail as a
// single-strip associated IFD (type "thumbnail", NewSubfileType=0). Shared by the
// raster-based regenCropThumbnail and the streaming engine crop path (which feeds
// streamCropThumbnail's bytes here directly).
func addCropThumbnailStripped(w *streamwriter.Writer, jpegBytes []byte, tw, th int) error {
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
