// Package downscale reduces a WSI source by an integer power-of-2 factor:
// codec-domain scaled decode where the codec supports it, else full-decode +
// box-halve.
package downscale

import (
	"errors"

	opentile "github.com/wsilabs/opentile-go"
	otdecoder "github.com/wsilabs/opentile-go/decoder"
	otresample "github.com/wsilabs/opentile-go/resample"
)

// PasteIntoRaster copies the top-left validW×validH region of the decoded RGB
// tile (which has stride decW*3) into the dst raster at position (dx, dy).
// Caller must have clamped validW/validH to fit inside dst.
func PasteIntoRaster(dst []byte, dstW, dstH, dx, dy int, src []byte, srcStrideW, validW, validH int) {
	PasteSubRect(dst, dstW, dstH, dx, dy, src, srcStrideW, 0, 0, validW, validH)
}

// DecodeReducedTile decodes the source tile at (tx, ty) reduced by `factor`,
// preferring codec-domain scaled decode (DecodeOptions.Scale) and falling back
// to a full decode + box-halving only when the codec cannot scale-decode
// (ErrUnsupportedScale). Returns tightly packed RGB (stride = w*3) and its
// actual dims.
//
// Decode routes through opentile-go's level-decode (srcL0.DecodedTile) rather
// than a standalone codec-of-bytes decode, so it handles EVERY source
// compression — LZW / uncompressed / Deflate (which need explicit tile-dims +
// predictor context) as well as the self-describing JPEG / JP2K / HTJ2K. Pass
// factor=1 for an unscaled full-tile decode (the crop path).
func DecodeReducedTile(srcL0 *opentile.Level, tx, ty, srcTileW, srcTileH, factor int) (pix []byte, w, h int, err error) {
	img, derr := srcL0.DecodedTile(tx, ty, opentile.WithScale(factor), opentile.WithFormat(otdecoder.PixelFormatRGB))
	if derr == nil {
		return tightTilePix(img), img.Width, img.Height, nil
	}
	if !errors.Is(derr, otdecoder.ErrUnsupportedScale) {
		return nil, 0, 0, derr
	}
	// Codec can't scale-decode at this factor: full decode + box-halve.
	full, ferr := srcL0.DecodedTile(tx, ty, opentile.WithFormat(otdecoder.PixelFormatRGB))
	if ferr != nil {
		return nil, 0, 0, ferr
	}
	return BoxHalve(tightTilePix(full), srcTileW, srcTileH, factor)
}

// tightTilePix returns the decoded tile's pixels as a tightly packed RGB buffer
// (stride = Width*3). When the decoder already produced a tight buffer the
// backing slice is returned directly; otherwise rows are compacted into a fresh
// buffer so downstream paste/box-halve can assume a w*3 stride.
func tightTilePix(img *otdecoder.Image) []byte {
	rowBytes := img.Width * 3
	if img.Stride == rowBytes {
		return img.Pix[:img.Height*rowBytes]
	}
	out := make([]byte, img.Height*rowBytes)
	for y := 0; y < img.Height; y++ {
		copy(out[y*rowBytes:(y+1)*rowBytes], img.Pix[y*img.Stride:y*img.Stride+rowBytes])
	}
	return out
}

// BoxHalve chains Box-halving steps log2(factor) times to produce a
// downsampled RGB buffer. Returns the downsampled bytes and its dimensions.
// Requires factor to be a power of two and srcW/srcH to be even at every
// step (the caller ensures this by choosing factor ∈ {2,4,8,16} and
// using source tile sizes that are multiples of 16).
func BoxHalve(rgb []byte, srcW, srcH, factor int) ([]byte, int, int, error) {
	cur := rgb
	curW, curH := srcW, srcH
	for f := factor; f > 1; f /= 2 {
		srcImg := &otdecoder.Image{
			Width:  curW,
			Height: curH,
			Stride: curW * 3,
			Format: otdecoder.PixelFormatRGB,
			Pix:    cur,
		}
		dstImg := otdecoder.NewImageFormat(curW/2, curH/2, otdecoder.PixelFormatRGB)
		if err := otresample.ImageInto(srcImg, dstImg, otresample.Box); err != nil {
			return nil, 0, 0, err
		}
		cur = dstImg.Pix
		curW /= 2
		curH /= 2
	}
	return cur, curW, curH, nil
}
