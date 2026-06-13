// crop.go holds the region-extract (offset, no scaling) primitives used by
// `wsitools crop`.
package downscale

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
