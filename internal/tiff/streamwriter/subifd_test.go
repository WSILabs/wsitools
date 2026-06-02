package streamwriter_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// readClassicIFD reads the entry count, a tag→inline-value map, the raw 330
// offset list, and the nextIFD pointer of the classic-TIFF IFD at `at`.
// Only valid for classic (non-Big) TIFF.
func readClassicIFD(t *testing.T, b []byte, at uint32) (tags map[uint16]uint32, subIFDs []uint32, nextIFD uint32) {
	t.Helper()
	n := binary.LittleEndian.Uint16(b[at:])
	tags = map[uint16]uint32{}
	p := at + 2
	for i := 0; i < int(n); i++ {
		e := b[p : p+12]
		tag := binary.LittleEndian.Uint16(e[0:])
		cnt := binary.LittleEndian.Uint32(e[4:])
		val := binary.LittleEndian.Uint32(e[8:])
		tags[tag] = val
		if tag == 330 { // SubIFDs: LONG array
			if cnt == 1 {
				subIFDs = []uint32{val}
			} else {
				off := val
				for k := uint32(0); k < cnt; k++ {
					subIFDs = append(subIFDs, binary.LittleEndian.Uint32(b[off+k*4:]))
				}
			}
		}
		p += 12
	}
	nextIFD = binary.LittleEndian.Uint32(b[p:])
	return tags, subIFDs, nextIFD
}

// TestSubIFDPyramidLayout: a 3-level pyramid + 1 associated image written with
// SubResolutionPyramid=true puts L1/L2 in L0's SubIFDs (330), keeps only
// L0→associated in the top-level chain, and tags every IFD SampleFormat=1.
func TestSubIFDPyramidLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "o.tiff")
	w, err := streamwriter.Create(path, streamwriter.Options{
		BigTIFF:              tiff.BigTIFFOff,
		SubResolutionPyramid: true,
		SampleFormat:         1,
		FormatName:           "ome-tiff",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dims := []uint32{16, 8, 4}
	for _, d := range dims {
		l, err := w.AddLevel(streamwriter.LevelSpec{
			ImageWidth: d, ImageHeight: d, TileWidth: d, TileHeight: d,
			Compression: tiff.CompressionNone, Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			WSIImageType: tiff.WSIImageTypePyramid,
		})
		if err != nil {
			t.Fatalf("AddLevel %d: %v", d, err)
		}
		l.WriteTile(0, 0, make([]byte, int(d*d*3)))
	}
	if err := w.AddStripped(streamwriter.StrippedSpec{
		Width: 8, Height: 8, RowsPerStrip: 8, BitsPerSample: []uint16{8, 8, 8},
		SamplesPerPixel: 3, Photometric: 2, Compression: tiff.CompressionNone,
		StripBytes: make([]byte, 8*8*3), WSIImageType: "label",
	}); err != nil {
		t.Fatalf("AddStripped: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	firstIFD := binary.LittleEndian.Uint32(b[4:]) // classic TIFF header
	l0tags, subIFDs, l0next := readClassicIFD(t, b, firstIFD)

	if l0tags[256] != 16 {
		t.Errorf("first IFD ImageWidth = %d, want 16 (L0)", l0tags[256])
	}
	if l0tags[339] != 1 {
		t.Errorf("L0 SampleFormat = %d, want 1", l0tags[339])
	}
	if len(subIFDs) != 2 {
		t.Fatalf("L0 SubIFDs count = %d, want 2", len(subIFDs))
	}
	s1, _, _ := readClassicIFD(t, b, subIFDs[0])
	s2, _, _ := readClassicIFD(t, b, subIFDs[1])
	if s1[256] != 8 || s2[256] != 4 {
		t.Errorf("SubIFD widths = %d,%d, want 8,4 (largest→smallest)", s1[256], s2[256])
	}
	if s1[339] != 1 || s2[339] != 1 {
		t.Errorf("SubIFD SampleFormat = %d,%d, want 1,1", s1[339], s2[339])
	}
	if l0next == 0 {
		t.Fatalf("L0 nextIFD = 0, want the associated IFD")
	}
	assoc, _, assocNext := readClassicIFD(t, b, l0next)
	if assoc[256] != 8 {
		t.Errorf("associated ImageWidth = %d, want 8", assoc[256])
	}
	if assoc[339] != 1 {
		t.Errorf("associated SampleFormat = %d, want 1", assoc[339])
	}
	if assocNext != 0 {
		t.Errorf("associated nextIFD = %d, want 0 (end of chain)", assocNext)
	}
}
