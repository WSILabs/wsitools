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

func TestCropTilePlan_MidTileOrigin(t *testing.T) {
	// Source: 512×512, 256×256 tiles (2×2 grid). Crop [100,80 300x300]
	// (origin mid-tile (0,0); spans all four tiles).
	const tw, th, sw, sh = 256, 256, 512, 512
	const cx, cy, cw, ch = 100, 80, 300, 300

	// Tile (0,0): covers src [0,256)×[0,256). Overlap with crop [100,400)×[80,380)
	// → src [100,256)×[80,256) = 156×176. dst = (0,0). srcLocal = (100,80).
	slx, sly, dx, dy, vw, vh, ok := cropTilePlan(0, 0, tw, th, sw, sh, cx, cy, cw, ch)
	if !ok || slx != 100 || sly != 80 || dx != 0 || dy != 0 || vw != 156 || vh != 176 {
		t.Fatalf("tile(0,0): got slx=%d sly=%d dx=%d dy=%d vw=%d vh=%d ok=%v", slx, sly, dx, dy, vw, vh, ok)
	}

	// Tile (1,1): covers src [256,512)×[256,512). Overlap with crop [100,400)×[80,380)
	// → src [256,400)×[256,380) = 144×124. dst = (256-100, 256-80) = (156,176).
	// srcLocal = (256-256, 256-256) = (0,0).
	slx, sly, dx, dy, vw, vh, ok = cropTilePlan(1, 1, tw, th, sw, sh, cx, cy, cw, ch)
	if !ok || slx != 0 || sly != 0 || dx != 156 || dy != 176 || vw != 144 || vh != 124 {
		t.Fatalf("tile(1,1): got slx=%d sly=%d dx=%d dy=%d vw=%d vh=%d ok=%v", slx, sly, dx, dy, vw, vh, ok)
	}
}

func TestCropTilePlan_NoOverlap(t *testing.T) {
	// Crop entirely inside tile (0,0); tile (1,1) does not overlap.
	if _, _, _, _, _, _, ok := cropTilePlan(1, 1, 256, 256, 512, 512, 0, 0, 100, 100); ok {
		t.Error("tile(1,1) should not overlap crop [0,0 100x100]")
	}
}
