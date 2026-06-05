package downscale

import "testing"

func TestBoxHalveDims(t *testing.T) {
	src := make([]byte, 256*256*3)
	pix, w, h, err := BoxHalve(src, 256, 256, 4)
	if err != nil {
		t.Fatal(err)
	}
	if w != 64 || h != 64 || len(pix) != 64*64*3 {
		t.Fatalf("got %dx%d len=%d, want 64x64 len=%d", w, h, len(pix), 64*64*3)
	}
}
