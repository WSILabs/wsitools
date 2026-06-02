package jpeg

import "testing"

// sofJPEG builds a minimal SOI + SOF0(3 components) + EOI bytestream.
// yHV is component-0's packed (Hi<<4 | Vi) sampling byte. Cb and Cr are
// fixed at 1x1 (0x11). The SOF segment length is 0x0011 (17 bytes).
func sofJPEG(yHV byte) []byte {
	return []byte{
		0xFF, 0xD8, // SOI
		0xFF, 0xC0, 0x00, 0x11, // SOF0, length=17
		0x08,       // precision
		0x00, 0x01, // height
		0x00, 0x01, // width
		0x03,            // num components
		0x01, yHV, 0x00, // comp 1 (Y)
		0x02, 0x11, 0x01, // comp 2 (Cb) 1x1
		0x03, 0x11, 0x01, // comp 3 (Cr) 1x1
		0xFF, 0xD9, // EOI
	}
}

func TestLumaSampling(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		h, v uint16
		ok   bool
	}{
		{"4:2:0", sofJPEG(0x22), 2, 2, true},
		{"4:2:2", sofJPEG(0x21), 2, 1, true},
		{"4:4:4", sofJPEG(0x11), 1, 1, true},
		{"not-jpeg", []byte{0x00, 0x01, 0x02, 0x03}, 0, 0, false},
		{"no-sof", []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, v, ok := LumaSampling(c.in)
			if h != c.h || v != c.v || ok != c.ok {
				t.Fatalf("LumaSampling = (%d,%d,%v), want (%d,%d,%v)", h, v, ok, c.h, c.v, c.ok)
			}
		})
	}
}
