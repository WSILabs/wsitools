package cogwsi

import (
	"encoding/binary"
	"testing"
)

func TestIFDBuilderClassicSimple(t *testing.T) {
	b := newIFDBuilder(false /*bigtiff*/)
	b.AddShort(256 /*ImageWidth*/, []uint16{512})
	b.AddShort(257 /*ImageLength*/, []uint16{384})
	ifd, ext, err := b.Encode(100 /*ifdOffset*/, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 0 {
		t.Errorf("expected no external bytes, got %d", len(ext))
	}
	// Classic IFD: uint16 entry_count + 2 entries * 12 + uint32 next_ifd_offset = 2 + 24 + 4 = 30.
	if len(ifd) != 30 {
		t.Errorf("ifd size: got %d want 30", len(ifd))
	}
	if binary.LittleEndian.Uint16(ifd[:2]) != 2 {
		t.Errorf("entry count: got %d want 2", binary.LittleEndian.Uint16(ifd[:2]))
	}
	// Last 4 bytes are next-IFD offset, defaulting to 0.
	if binary.LittleEndian.Uint32(ifd[26:30]) != 0 {
		t.Errorf("next IFD offset: got %d want 0", binary.LittleEndian.Uint32(ifd[26:30]))
	}
}

func TestIFDBuilderBigTIFFLongArray(t *testing.T) {
	b := newIFDBuilder(true /*bigtiff*/)
	offsets := []uint64{1000, 2000, 3000}
	b.AddLong8(324 /*TileOffsets*/, offsets)
	ifd, ext, err := b.Encode(100, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 24 {
		t.Errorf("external bytes: got %d want 24 (3*8)", len(ext))
	}
	// BigTIFF IFD: uint64 entry_count + 1 entry * 20 + uint64 next_ifd_offset = 8 + 20 + 8 = 36.
	if len(ifd) != 36 {
		t.Errorf("ifd size: got %d want 36", len(ifd))
	}
	// The entry's value field (last 8 bytes of the 20-byte entry) holds the absolute
	// offset to the external array. The external array sits immediately after the IFD,
	// at ifdOffset + ifdSize = 100 + 36 = 136.
	entryStart := 8 // after uint64 entry_count
	valueAt := entryStart + 12
	if got := binary.LittleEndian.Uint64(ifd[valueAt : valueAt+8]); got != 136 {
		t.Errorf("external offset: got %d want 136", got)
	}
}

func TestIFDBuilderASCIIInline(t *testing.T) {
	// Short string fits inline (≤4 bytes classic, ≤8 BigTIFF).
	b := newIFDBuilder(false)
	b.AddASCII(305 /*Software*/, "go")
	ifd, ext, err := b.Encode(100, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 0 {
		t.Errorf("short ASCII should be inline, got %d external bytes", len(ext))
	}
	// Verify count includes the trailing NUL.
	const entryStart = 2
	count := binary.LittleEndian.Uint32(ifd[entryStart+4 : entryStart+8])
	if count != 3 {
		t.Errorf("ASCII count: got %d want 3 (go\\0)", count)
	}
}

func TestIFDBuilderASCIIExternal(t *testing.T) {
	b := newIFDBuilder(false)
	long := "this string is more than four bytes long"
	b.AddASCII(270 /*ImageDescription*/, long)
	ifd, ext, err := b.Encode(100, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != len(long)+1 { // includes trailing NUL
		t.Errorf("external bytes: got %d want %d", len(ext), len(long)+1)
	}
	// Verify the inline value field holds the external offset (= ifdOffset + ifdSize).
	const entryStart = 2
	valueAt := entryStart + 8
	got := binary.LittleEndian.Uint32(ifd[valueAt : valueAt+4])
	want := uint32(100 + len(ifd))
	if got != want {
		t.Errorf("external offset: got %d want %d", got, want)
	}
}
