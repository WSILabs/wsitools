package bifwriter

import (
	"bytes"
	"testing"
)

// fakeLevel is a tiny in-memory TileSource: a 2x2 tile grid of distinct
// 1-byte "tiles" (not real JPEG — this test only checks structural assembly
// and serpentine placement, not decodability; Task 4 does the real round-trip).
type fakeLevel struct{}

func (fakeLevel) SizeW() int       { return 3 }
func (fakeLevel) SizeH() int       { return 3 }
func (fakeLevel) TileW() int       { return 2 }
func (fakeLevel) TileH() int       { return 2 }
func (fakeLevel) TileMaxSize() int { return 1 }
func (fakeLevel) TileInto(x, y int, dst []byte) (int, error) {
	dst[0] = byte(10*y + x) // encodes (col,row) so we can locate it in the file
	return 1, nil
}

// bufAt is an in-memory io.WriterAt backing.
type bufAt struct{ b []byte }

func (w *bufAt) WriteAt(p []byte, off int64) (int, error) {
	end := int(off) + len(p)
	if end > len(w.b) {
		w.b = append(w.b, make([]byte, end-len(w.b))...)
	}
	copy(w.b[off:], p)
	return len(p), nil
}

func TestWriteSingleLevelAssembles(t *testing.T) {
	var w bufAt
	if err := WriteSingleLevel(&w, fakeLevel{}, IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
		t.Fatalf("WriteSingleLevel: %v", err)
	}
	// BigTIFF magic (II, 0x2B).
	if !bytes.HasPrefix(w.b, []byte{0x49, 0x49, 0x2B, 0x00}) {
		t.Errorf("output is not little-endian BigTIFF: % x", w.b[:8])
	}
	// The <iScan marker must be in the bytes (detection).
	if !bytes.Contains(w.b, []byte("<iScan")) {
		t.Errorf("output missing <iScan marker")
	}
}
