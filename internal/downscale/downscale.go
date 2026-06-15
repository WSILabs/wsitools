// Package downscale reduces a WSI source by an integer power-of-2 factor:
// codec-domain scaled decode where the codec supports it, else full-decode +
// box-halve.
package downscale

import (
	"context"
	"errors"
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	otdecoder "github.com/wsilabs/opentile-go/decoder"
	otresample "github.com/wsilabs/opentile-go/resample"
)

// MaterializeReducedL0 decodes every source-L0 tile reduced by 1/factor and
// pastes the result into outL0 at the correct image-space position. Each tile
// is reduced codec-agnostically (see DecodeReducedTile): codec-domain scaled
// decode where the source codec supports it (JPEG IDCT fast-scale, JP2K/HTJ2K
// wavelet resolution decode), else full-decode + chained 2x2 box-average.
func MaterializeReducedL0(ctx context.Context, srcL0 *opentile.Level, outL0 []byte, outW, outH, factor int) error {
	srcGrid := srcL0.Grid
	srcTileW := srcL0.TileSize.W
	srcTileH := srcL0.TileSize.H
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H

	for ty := 0; ty < srcGrid.H; ty++ {
		for tx := 0; tx < srcGrid.W; tx++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			// Compute the image-space destination rect for this source tile.
			// The source tile covers [sx0, sx1) × [sy0, sy1) in source-pixel
			// space, clamped at the image bounds. The corresponding output
			// region is [sx0/factor, sx1/factor) × [sy0/factor, sy1/factor).
			sx0 := tx * srcTileW
			sy0 := ty * srcTileH
			sx1 := sx0 + srcTileW
			sy1 := sy0 + srcTileH
			if sx1 > srcW {
				sx1 = srcW
			}
			if sy1 > srcH {
				sy1 = srcH
			}
			validSrcW := sx1 - sx0
			validSrcH := sy1 - sy0

			// Codec-domain scaled decode where the source codec supports it
			// (JPEG IDCT, JP2K/HTJ2K wavelet resolution), else full-decode +
			// box-halve. Self-contained per tile, so seam-free.
			decoded, decW, decH, err := DecodeReducedTile(srcL0, tx, ty, srcTileW, srcTileH, factor)
			if err != nil {
				return fmt.Errorf("decode tile (%d,%d): %w", tx, ty, err)
			}

			// The valid region inside the decoded tile (in decoded-pixel
			// units): only the pixels corresponding to actual image content,
			// not padding past the slide edge.
			validDecW := (validSrcW + factor - 1) / factor
			validDecH := (validSrcH + factor - 1) / factor
			if validDecW > decW {
				validDecW = decW
			}
			if validDecH > decH {
				validDecH = decH
			}

			// Destination position in the output L0 raster.
			dx := sx0 / factor
			dy := sy0 / factor
			// Clamp to output bounds (defensive: rounding could nudge past
			// outW/outH at the slide edge).
			if dx+validDecW > outW {
				validDecW = outW - dx
			}
			if dy+validDecH > outH {
				validDecH = outH - dy
			}
			PasteIntoRaster(outL0, outW, outH, dx, dy, decoded, decW, validDecW, validDecH)
		}
	}
	return nil
}

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
// step (the v0.1 caller ensures this by choosing factor ∈ {2,4,8,16} and
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
