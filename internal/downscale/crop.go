// crop.go holds the region-extract (offset, no scaling) primitives used by
// `wsitools crop`.
package downscale

import (
	"context"
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
)

// PasteSubRect copies a validW×validH RGB888 region starting at (srcX, srcY)
// within src (row stride srcStrideW*3) into dst (dimensions dstW×dstH) at
// (dx, dy). Callers must have clamped the region to fit inside both buffers.
func PasteSubRect(dst []byte, dstW, dstH, dx, dy int, src []byte, srcStrideW, srcX, srcY, validW, validH int) {
	if validW <= 0 || validH <= 0 {
		return
	}
	rowBytes := validW * 3
	srcStride := srcStrideW * 3
	dstStride := dstW * 3
	for y := 0; y < validH; y++ {
		srcOff := (srcY+y)*srcStride + srcX*3
		dstOff := (dy+y)*dstStride + dx*3
		copy(dst[dstOff:dstOff+rowBytes], src[srcOff:srcOff+rowBytes])
	}
}

// cropTilePlan computes, for source tile (tx,ty), how its decoded pixels map
// into the crop output raster anchored at (0,0). Returns the source-tile-local
// copy offset (srcLocalX/Y), the destination offset in the crop raster
// (dstX/Y), the copy extent (validW/validH), and ok=false if the tile does not
// overlap the crop rect. All inputs/outputs are in source-pixel units.
func cropTilePlan(tx, ty, srcTileW, srcTileH, srcW, srcH, cropX, cropY, cropW, cropH int) (srcLocalX, srcLocalY, dstX, dstY, validW, validH int, ok bool) {
	// Source tile bounds in image space, clamped to the image.
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
	// Crop bounds in image space.
	cx1 := cropX + cropW
	cy1 := cropY + cropH
	// Intersection.
	ix0 := max(sx0, cropX)
	iy0 := max(sy0, cropY)
	ix1 := min(sx1, cx1)
	iy1 := min(sy1, cy1)
	if ix0 >= ix1 || iy0 >= iy1 {
		return 0, 0, 0, 0, 0, 0, false
	}
	return ix0 - sx0, iy0 - sy0, ix0 - cropX, iy0 - cropY, ix1 - ix0, iy1 - iy0, true
}

// MaterializeCroppedL0 decodes the source-L0 region
// [cropX,cropX+cropW) × [cropY,cropY+cropH) and writes it, anchored at (0,0),
// into outL0 (an RGB888 raster of size cropW*cropH*3). It decodes only the
// source tiles overlapping the crop and pastes each tile's overlapping
// sub-rect — memory-bounded (only tiles touching the crop rect), offset (no scaling).
func MaterializeCroppedL0(ctx context.Context, srcL0 *opentile.Level, outL0 []byte, cropX, cropY, cropW, cropH int) error {
	srcTileW := srcL0.TileSize.W
	srcTileH := srcL0.TileSize.H
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H

	tx0 := cropX / srcTileW
	ty0 := cropY / srcTileH
	tx1 := (cropX + cropW - 1) / srcTileW
	ty1 := (cropY + cropH - 1) / srcTileH

	for ty := ty0; ty <= ty1; ty++ {
		for tx := tx0; tx <= tx1; tx++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			srcLocalX, srcLocalY, dstX, dstY, validW, validH, overlap := cropTilePlan(
				tx, ty, srcTileW, srcTileH, srcW, srcH, cropX, cropY, cropW, cropH)
			if !overlap {
				continue
			}
			// factor=1 → unscaled full-tile decode (codec-agnostic).
			decoded, decW, decH, err := DecodeReducedTile(srcL0, tx, ty, srcTileW, srcTileH, 1)
			if err != nil {
				return fmt.Errorf("decode source tile (%d,%d): %w", tx, ty, err)
			}
			// Defensive clamp: cropTilePlan derives validW/validH from image
			// bounds, but the decoded tile is the authoritative pixel source
			// — never read past it.
			if validW > decW {
				validW = decW
			}
			if validH > decH {
				validH = decH
			}
			PasteSubRect(outL0, cropW, cropH, dstX, dstY, decoded, decW, srcLocalX, srcLocalY, validW, validH)
		}
	}
	return nil
}
