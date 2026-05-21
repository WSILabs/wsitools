package tiff

import (
	"encoding/binary"
	"testing"
)

func TestEntryBuilderClassicSimple(t *testing.T) {
	b := NewEntryBuilder(false /*bigtiff*/)
	b.AddShort(TagImageWidth, []uint16{512})
	b.AddShort(TagImageLength, []uint16{384})
	ifd, ext, err := b.Encode(100 /*ifdOffset*/)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 0 {
		t.Errorf("expected no external bytes, got %d", len(ext))
	}
	// Classic IFD: uint16 entry_count + 2 entries * 12 + uint32 next_ifd = 30.
	if len(ifd) != 30 {
		t.Errorf("ifd size: got %d want 30", len(ifd))
	}
	if binary.LittleEndian.Uint16(ifd[:2]) != 2 {
		t.Errorf("entry count: got %d want 2", binary.LittleEndian.Uint16(ifd[:2]))
	}
}

func TestEntryBuilderBigTIFFLongArray(t *testing.T) {
	b := NewEntryBuilder(true /*bigtiff*/)
	offsets := []uint64{1000, 2000, 3000}
	b.AddLong8(TagTileOffsets, offsets)
	ifd, ext, err := b.Encode(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ext) != 24 {
		t.Errorf("external bytes: got %d want 24", len(ext))
	}
	if len(ifd) != 36 {
		t.Errorf("ifd size: got %d want 36", len(ifd))
	}
}

func TestEntryBuilderASCIIInline(t *testing.T) {
	b := NewEntryBuilder(false)
	b.AddASCII(TagSoftware, "go")
	ifd, ext, _ := b.Encode(100)
	if len(ext) != 0 {
		t.Errorf("short ASCII should be inline, got %d external bytes", len(ext))
	}
	const entryStart = 2
	count := binary.LittleEndian.Uint32(ifd[entryStart+4 : entryStart+8])
	if count != 3 {
		t.Errorf("ASCII count: got %d want 3 (go\\0)", count)
	}
}

func TestEntryBuilderASCIIExternal(t *testing.T) {
	b := NewEntryBuilder(false)
	long := "this string is more than four bytes long"
	b.AddASCII(TagImageDescription, long)
	_, ext, _ := b.Encode(100)
	if len(ext) != len(long)+1 {
		t.Errorf("external bytes: got %d want %d", len(ext), len(long)+1)
	}
}

func TestAddTileOffsetsClassicOverflow(t *testing.T) {
	b := NewEntryBuilder(false)
	err := b.AddTileOffsets(TagTileOffsets, []uint64{0xFFFFFFFFFF})
	if err == nil {
		t.Errorf("expected overflow error in classic mode for offset > 4 GiB")
	}
}

func TestEntrySize(t *testing.T) {
	if got := EntrySize(false); got != 12 {
		t.Errorf("EntrySize(classic): got %d want 12", got)
	}
	if got := EntrySize(true); got != 20 {
		t.Errorf("EntrySize(BigTIFF): got %d want 20", got)
	}
}

func TestIFDRecordSize(t *testing.T) {
	if got := IFDRecordSize(5, false); got != 6+5*12 {
		t.Errorf("IFDRecordSize(5, classic): got %d want %d", got, 6+5*12)
	}
	if got := IFDRecordSize(5, true); got != 16+5*20 {
		t.Errorf("IFDRecordSize(5, BigTIFF): got %d want %d", got, 16+5*20)
	}
}

func TestAddRawShort(t *testing.T) {
	b := NewEntryBuilder(false)
	if err := b.AddRaw(RawTag{Tag: TagImageWidth, Type: TypeSHORT, Value: []uint16{512}}); err != nil {
		t.Fatal(err)
	}
	ifd, _, _ := b.Encode(0)
	const entryStart = 2
	if binary.LittleEndian.Uint16(ifd[entryStart:entryStart+2]) != TagImageWidth {
		t.Errorf("AddRaw didn't add the expected tag")
	}
}

func TestAddRawASCII(t *testing.T) {
	b := NewEntryBuilder(false)
	if err := b.AddRaw(RawTag{Tag: TagImageDescription, Type: TypeASCII, Value: "hello"}); err != nil {
		t.Fatal(err)
	}
	ifd, _, _ := b.Encode(0)
	const entryStart = 2
	if binary.LittleEndian.Uint32(ifd[entryStart+4:entryStart+8]) != uint32(len("hello")+1) {
		t.Errorf("AddRaw ASCII count mismatch")
	}
}

func TestAddRawRejectsUnknownType(t *testing.T) {
	b := NewEntryBuilder(false)
	err := b.AddRaw(RawTag{Tag: 256, Type: 99 /*nonexistent*/, Value: []uint16{1}})
	if err == nil {
		t.Errorf("expected error for unknown TIFF type 99")
	}
}

func TestAddRawTypeMismatch(t *testing.T) {
	b := NewEntryBuilder(false)
	// SHORT type but []uint32 value — should error.
	err := b.AddRaw(RawTag{Tag: 256, Type: TypeSHORT, Value: []uint32{1}})
	if err == nil {
		t.Errorf("expected error for type/value mismatch")
	}
}

func TestAddUndefined(t *testing.T) {
	b := NewEntryBuilder(false)
	b.AddUndefined(347 /*JPEGTables*/, []byte{0xFF, 0xD8, 0xFF, 0xD9})
	ifd, _, _ := b.Encode(0)
	const entryStart = 2
	gotType := binary.LittleEndian.Uint16(ifd[entryStart+2 : entryStart+4])
	if gotType != TypeUNDEFINED {
		t.Errorf("AddUndefined: got type %d want %d (UNDEFINED)", gotType, TypeUNDEFINED)
	}
	gotCount := binary.LittleEndian.Uint32(ifd[entryStart+4 : entryStart+8])
	if gotCount != 4 {
		t.Errorf("AddUndefined count: got %d want 4", gotCount)
	}
}

func TestAddRawUndefined(t *testing.T) {
	b := NewEntryBuilder(false)
	err := b.AddRaw(RawTag{Tag: 347, Type: TypeUNDEFINED, Value: []byte{0xFF, 0xD8, 0xFF, 0xD9}})
	if err != nil {
		t.Fatalf("AddRaw UNDEFINED: %v", err)
	}
}
