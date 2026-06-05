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
func MaterializeReducedL0(ctx context.Context, src *opentile.Slide, srcL0 opentile.Level, outL0 []byte, outW, outH, factor int) error {
	srcCompression := srcL0.Compression
	srcGrid := srcL0.Grid
	srcTileW := srcL0.TileSize.W
	srcTileH := srcL0.TileSize.H
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H

	// libjpeg-turbo's scale formula: outDim = ceil(inDim * 1 / factor).
	// For interior tiles this is srcTileW/factor, srcTileH/factor exactly
	// (we choose factors that divide common tile sizes 240/256 cleanly:
	// 240/2=120, 240/4=60, 240/8=30, 240/16=15; same shape for 256).
	fac, ok := otdecoder.GetByCompressionTag(opentile.CompressionToTIFFTag(srcCompression))
	if !ok {
		return fmt.Errorf("no decoder registered for source compression %s", srcCompression)
	}

	tileBuf := make([]byte, src.TileMaxSize(srcL0.Index))

	for ty := 0; ty < srcGrid.H; ty++ {
		for tx := 0; tx < srcGrid.W; tx++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			n, err := src.RawTileInto(srcL0.Index, tx, ty, tileBuf)
			if err != nil {
				return fmt.Errorf("read source tile (%d,%d): %w", tx, ty, err)
			}
			compressed := tileBuf[:n]
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
			decoded, decW, decH, err := DecodeReducedTile(fac, compressed, srcTileW, srcTileH, factor)
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
	if validW <= 0 || validH <= 0 {
		return
	}
	rowBytes := validW * 3
	srcStride := srcStrideW * 3
	dstStride := dstW * 3
	for y := 0; y < validH; y++ {
		srcOff := y * srcStride
		dstOff := (dy+y)*dstStride + dx*3
		copy(dst[dstOff:dstOff+rowBytes], src[srcOff:srcOff+rowBytes])
	}
}

// DecodeReducedTile decodes one source tile's compressed bytes reduced by
// `factor`, preferring codec-domain scaled decode (DecodeOptions.Scale) and
// falling back to a full decode + box-halving only when the codec cannot
// scale-decode (ErrUnsupportedScale). Returns packed RGB and its actual dims.
func DecodeReducedTile(fac otdecoder.Factory, compressed []byte, srcTileW, srcTileH, factor int) (pix []byte, w, h int, err error) {
	dec := fac.New()
	defer dec.Close()
	img, derr := dec.Decode(compressed, otdecoder.DecodeOptions{Scale: factor, Format: otdecoder.PixelFormatRGB})
	if derr == nil {
		return img.Pix, img.Width, img.Height, nil
	}
	if !errors.Is(derr, otdecoder.ErrUnsupportedScale) {
		return nil, 0, 0, derr
	}
	// Codec can't scale-decode at this factor: full decode + box-halve.
	full, ferr := dec.Decode(compressed, otdecoder.DecodeOptions{Scale: 1, Format: otdecoder.PixelFormatRGB})
	if ferr != nil {
		return nil, 0, 0, ferr
	}
	return BoxHalve(full.Pix, srcTileW, srcTileH, factor)
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
