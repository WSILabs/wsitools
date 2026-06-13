package downscale

import "testing"

// fillPattern returns an RGB raster of w×h where pixel (x,y) = {x%256, y%256, (x+y)%256}.
func fillPattern(w, h int) []byte {
	buf := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := (y*w + x) * 3
			buf[o] = byte(x % 256)
			buf[o+1] = byte(y % 256)
			buf[o+2] = byte((x + y) % 256)
		}
	}
	return buf
}

func TestPasteSubRect_InteriorOffset(t *testing.T) {
	src := fillPattern(8, 8)
	dst := make([]byte, 4*4*3)
	// Copy the 4×4 region of src starting at (2,3) into dst at (0,0).
	PasteSubRect(dst, 4, 4, 0, 0, src, 8, 2, 3, 4, 4)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			sx, sy := x+2, y+3
			o := (y*4 + x) * 3
			if dst[o] != byte(sx%256) || dst[o+1] != byte(sy%256) || dst[o+2] != byte((sx+sy)%256) {
				t.Fatalf("dst(%d,%d)=%v,%v,%v want src(%d,%d)", x, y, dst[o], dst[o+1], dst[o+2], sx, sy)
			}
		}
	}
}

func TestPasteSubRect_DstOffset(t *testing.T) {
	src := fillPattern(4, 4)
	dst := make([]byte, 8*8*3)
	PasteSubRect(dst, 8, 8, 3, 2, src, 4, 0, 0, 4, 4)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			o := ((y+2)*8 + (x + 3)) * 3
			if dst[o] != byte(x%256) || dst[o+1] != byte(y%256) {
				t.Fatalf("dst placement wrong at (%d,%d)", x, y)
			}
		}
	}
}
