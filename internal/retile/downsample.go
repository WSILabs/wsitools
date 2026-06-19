package retile

// boxDownsample2x reduces an RGBImage by 2× in both dimensions via 2×2 box
// averaging. Odd-dimension sources treat the last row/column as duplicated, so
// the output is ceil(srcH/2) × ceil(srcW/2).
func boxDownsample2x(src *RGBImage) *RGBImage {
	srcW, srcH := src.W, src.H
	if srcW == 0 || srcH == 0 {
		return &RGBImage{Pix: []byte{}, Stride: 0, W: 0, H: 0}
	}
	w := (srcW + 1) / 2
	h := (srcH + 1) / 2
	dst := newPooledRGB(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx0, sy0 := x*2, y*2
			sx1 := sx0 + 1
			if sx1 >= srcW {
				sx1 = srcW - 1
			}
			sy1 := sy0 + 1
			if sy1 >= srcH {
				sy1 = srcH - 1
			}
			i0 := sy0*src.Stride + sx0*3
			i1 := sy0*src.Stride + sx1*3
			i2 := sy1*src.Stride + sx0*3
			i3 := sy1*src.Stride + sx1*3
			r := (uint32(src.Pix[i0+0]) + uint32(src.Pix[i1+0]) + uint32(src.Pix[i2+0]) + uint32(src.Pix[i3+0])) / 4
			g := (uint32(src.Pix[i0+1]) + uint32(src.Pix[i1+1]) + uint32(src.Pix[i2+1]) + uint32(src.Pix[i3+1])) / 4
			b := (uint32(src.Pix[i0+2]) + uint32(src.Pix[i1+2]) + uint32(src.Pix[i2+2]) + uint32(src.Pix[i3+2])) / 4
			di := y*dst.Stride + x*3
			dst.Pix[di+0] = byte(r)
			dst.Pix[di+1] = byte(g)
			dst.Pix[di+2] = byte(b)
		}
	}
	return dst
}
