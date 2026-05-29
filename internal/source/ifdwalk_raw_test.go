package source

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildMinimalTIFF writes a tiny classic-TIFF with one IFD containing three
// entries: ImageWidth=100 (SHORT), ImageLength=50 (SHORT), Compression=7
// (JPEG, SHORT). Returns the file path.
func buildMinimalTIFF(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.tif")

	bo := binary.LittleEndian
	const ifdOff = 16
	hdr := make([]byte, 8)
	copy(hdr[:2], "II")
	bo.PutUint16(hdr[2:4], 42)
	bo.PutUint32(hdr[4:8], ifdOff)

	pad := make([]byte, 8)

	const nEntries = 3
	ifd := make([]byte, 2+nEntries*12+4)
	bo.PutUint16(ifd[0:2], nEntries)

	write := func(off int, tag, typ uint16, count uint32, val uint32) {
		bo.PutUint16(ifd[off:off+2], tag)
		bo.PutUint16(ifd[off+2:off+4], typ)
		bo.PutUint32(ifd[off+4:off+8], count)
		bo.PutUint32(ifd[off+8:off+12], val)
	}
	write(2+0*12, 256, 3, 1, 100)
	write(2+1*12, 257, 3, 1, 50)
	write(2+2*12, 259, 3, 1, 7)
	bo.PutUint32(ifd[2+nEntries*12:], 0)

	out := append(hdr, pad...)
	out = append(out, ifd...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWalkIFDsRawMinimal(t *testing.T) {
	path := buildMinimalTIFF(t)
	ifds, err := WalkIFDsRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ifds) != 1 {
		t.Fatalf("got %d IFDs, want 1", len(ifds))
	}
	rec := ifds[0]
	if rec.ByteOrder != binary.LittleEndian {
		t.Errorf("ByteOrder = %v, want LittleEndian", rec.ByteOrder)
	}
	if len(rec.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(rec.Entries))
	}
	gotTags := []uint16{rec.Entries[0].Tag, rec.Entries[1].Tag, rec.Entries[2].Tag}
	wantTags := []uint16{256, 257, 259}
	for i := range gotTags {
		if gotTags[i] != wantTags[i] {
			t.Errorf("entry[%d].Tag = %d, want %d", i, gotTags[i], wantTags[i])
		}
	}
	if rec.Width != 100 || rec.Height != 50 || rec.Compression != 7 {
		t.Errorf("typed fields wrong: w=%d h=%d c=%d", rec.Width, rec.Height, rec.Compression)
	}
	if got := len(rec.Entries[0].Raw); got != 2 {
		t.Errorf("entry[0].Raw len = %d, want 2", got)
	}
	if binary.LittleEndian.Uint16(rec.Entries[0].Raw) != 100 {
		t.Errorf("entry[0].Raw decoded = %d, want 100", binary.LittleEndian.Uint16(rec.Entries[0].Raw))
	}
}

func TestWalkIFDsSlimNoEntries(t *testing.T) {
	path := buildMinimalTIFF(t)
	ifds, err := WalkIFDs(path)
	if err != nil {
		t.Fatal(err)
	}
	if ifds[0].Entries != nil {
		t.Errorf("WalkIFDs populated Entries; should be nil in slim mode")
	}
	if ifds[0].ByteOrder == nil {
		t.Errorf("ByteOrder not set in slim mode")
	}
}
