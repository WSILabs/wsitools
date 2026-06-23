package ife

import "testing"

func TestPutUint40(t *testing.T) {
	var b [5]byte
	putUint40(b[:], 0x123456789A)
	want := [5]byte{0x9A, 0x78, 0x56, 0x34, 0x12}
	if b != want {
		t.Errorf("putUint40 = % x, want % x", b, want)
	}
}

func TestPutUint24(t *testing.T) {
	var b [3]byte
	putUint24(b[:], 0xABCDEF)
	want := [3]byte{0xEF, 0xCD, 0xAB}
	if b != want {
		t.Errorf("putUint24 = % x, want % x", b, want)
	}
}

func TestConsts(t *testing.T) {
	if magicBytes != 0x49726973 {
		t.Errorf("magic = %#x", magicBytes)
	}
	if nullTile != 0xFFFFFFFFFF {
		t.Errorf("nullTile = %#x", nullTile)
	}
	if tileSidePixels != 256 {
		t.Errorf("tileSide = %d", tileSidePixels)
	}
	if recoverHeader != 0x5501 || recoverTileTable != 0x5502 ||
		recoverLayerExtents != 0x5506 || recoverTileOffsets != 0x5507 {
		t.Errorf("tile-path recovery magics wrong")
	}
}
