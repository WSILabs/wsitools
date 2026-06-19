package retile

import "testing"

// makeRGB returns an *RGBImage of size w×h where pixel (x,y) =
// (id, byte(x), byte(y)) — encodes id+col+row for traceable assertions.
func makeRGB(w, h int, id byte) *RGBImage {
	img := &RGBImage{Pix: make([]byte, w*h*3), Stride: w * 3, W: w, H: h}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*img.Stride + x*3
			img.Pix[i+0] = id
			img.Pix[i+1] = byte(x)
			img.Pix[i+2] = byte(y)
		}
	}
	return img
}

func TestBoxDownsample2xHalvesDimensions(t *testing.T) {
	dst := boxDownsample2x(makeRGB(8, 8, 0xAA))
	if dst.W != 4 || dst.H != 4 {
		t.Errorf("dst dims: %dx%d, want 4x4", dst.W, dst.H)
	}
}

func TestBoxDownsample2xAverages2x2(t *testing.T) {
	src := &RGBImage{Pix: make([]byte, 4*4*3), Stride: 4 * 3, W: 4, H: 4}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			i := y*src.Stride + x*3
			src.Pix[i+0] = byte(y * 64)
			src.Pix[i+1] = byte(x * 64)
			src.Pix[i+2] = 100
		}
	}
	dst := boxDownsample2x(src)
	if dst.W != 2 || dst.H != 2 {
		t.Fatalf("dst dims: %dx%d, want 2x2", dst.W, dst.H)
	}
	i := 0
	if rr, gg, bb := dst.Pix[i+0], dst.Pix[i+1], dst.Pix[i+2]; rr != 32 || gg != 32 || bb != 100 {
		t.Errorf("dst(0,0) = R=%d G=%d B=%d; want R=32 G=32 B=100", rr, gg, bb)
	}
}

func TestBoxDownsample2xOddDimsRoundUp(t *testing.T) {
	dst := boxDownsample2x(makeRGB(9, 7, 0x11))
	if dst.W != 5 || dst.H != 4 {
		t.Errorf("dst dims: %dx%d, want 5x4 (ceil(9/2)×ceil(7/2))", dst.W, dst.H)
	}
}
