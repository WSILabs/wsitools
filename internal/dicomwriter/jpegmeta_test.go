package dicomwriter

import "testing"

// jpeg builds a minimal marker stream: SOI, the given segments, then SOS+EOI.
// Each segment is {marker, payload}; length bytes are computed.
func jpegStream(segs ...[]byte) []byte {
	out := []byte{0xFF, 0xD8} // SOI
	for _, s := range segs {
		out = append(out, s...)
	}
	out = append(out, 0xFF, 0xDA, 0x00, 0x02) // SOS (len 2, no body)
	out = append(out, 0xFF, 0xD9)             // EOI
	return out
}

// sof0 builds an SOF0 segment (FF C0) with the given precision, 1 component
// block per (h,v) sampling pair.
func sof0(prec byte, comps [][2]byte) []byte {
	body := []byte{prec, 0x00, 0x10, 0x00, 0x10, byte(len(comps))} // prec, h=16,w=16, ncomp
	for i, c := range comps {
		body = append(body, byte(i+1), c[0]<<4|c[1], 0x00) // id, sampling, qtable
	}
	seg := []byte{0xFF, 0xC0, 0x00, byte(2 + len(body))}
	return append(seg, body...)
}

// app14 builds an APP14 Adobe segment with the given transform byte.
func app14(transform byte) []byte {
	body := []byte("Adobe")
	body = append(body, 0x00, 0x64, 0x80, 0x00, 0x00, 0x00, transform) // ver,flags0,flags1,transform
	seg := []byte{0xFF, 0xEE, 0x00, byte(2 + len(body))}
	return append(seg, body...)
}

func TestInspectAPP14RGB(t *testing.T) {
	// Aperio order: SOF before APP14, transform=0 → RGB, 3 comps 1x1 (not subsampled).
	j := jpegStream(sof0(8, [][2]byte{{1, 1}, {1, 1}, {1, 1}}), app14(0))
	info, err := Inspect(j)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Color != ColorRGB {
		t.Errorf("Color = %v, want ColorRGB", info.Color)
	}
	if info.Components != 3 || info.Precision != 8 || info.Subsampled {
		t.Errorf("got comps=%d prec=%d sub=%v, want 3/8/false", info.Components, info.Precision, info.Subsampled)
	}
	if p, _ := Photometric(info); p != "RGB" {
		t.Errorf("Photometric = %q, want RGB", p)
	}
}

func TestInspectYCbCrSubsampled(t *testing.T) {
	// No APP14, luma 2x2 + chroma 1x1 → subsampled YCbCr → YBR_FULL_422.
	j := jpegStream(sof0(8, [][2]byte{{2, 2}, {1, 1}, {1, 1}}))
	info, err := Inspect(j)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Color != ColorYCbCr || !info.Subsampled {
		t.Errorf("got Color=%v sub=%v, want YCbCr/true", info.Color, info.Subsampled)
	}
	if p, _ := Photometric(info); p != "YBR_FULL_422" {
		t.Errorf("Photometric = %q, want YBR_FULL_422", p)
	}
}

func TestInspectYCbCr444(t *testing.T) {
	// No APP14, all 1x1 → JFIF default YCbCr, not subsampled → YBR_FULL.
	j := jpegStream(sof0(8, [][2]byte{{1, 1}, {1, 1}, {1, 1}}))
	info, _ := Inspect(j)
	if p, _ := Photometric(info); p != "YBR_FULL" {
		t.Errorf("Photometric = %q, want YBR_FULL", p)
	}
}

func TestInspectMonochrome(t *testing.T) {
	j := jpegStream(sof0(8, [][2]byte{{1, 1}}))
	info, _ := Inspect(j)
	if p, _ := Photometric(info); p != "MONOCHROME2" {
		t.Errorf("Photometric = %q, want MONOCHROME2", p)
	}
}

func TestInspectErrors(t *testing.T) {
	if _, err := Inspect([]byte{0x00, 0x01}); err == nil {
		t.Error("want error for non-JPEG (no SOI)")
	}
	if _, err := Inspect([]byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x02}); err == nil {
		t.Error("want error when no SOF before SOS")
	}
	if _, err := Photometric(JPEGInfo{Precision: 12, Components: 3}); err == nil {
		t.Error("want error for precision != 8")
	}
}
