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

func TestEncodingFor(t *testing.T) {
	cases := []struct {
		name   string
		want   uint8
		wantOK bool
	}{
		{"jpeg", encJPEG, true},
		{"", encJPEG, true},
		{"avif", encAVIF, true},
		{"htj2k", 0, false},
	}
	for _, c := range cases {
		got, ok := EncodingFor(c.name)
		if got != c.want || ok != c.wantOK {
			t.Errorf("EncodingFor(%q) = (%d, %v), want (%d, %v)", c.name, got, ok, c.want, c.wantOK)
		}
	}
	if encJPEG != 2 || encAVIF != 3 {
		t.Errorf("encoding consts changed: jpeg=%d avif=%d", encJPEG, encAVIF)
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
