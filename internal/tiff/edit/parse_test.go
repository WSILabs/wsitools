package edit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// buildClassicTIFF returns a minimal little-endian classic TIFF with one IFD
// per entry in descs, each carrying ImageWidth (256) + ImageDescription
// (out-of-line), chained in order. Returns the bytes and per-IFD record offsets.
func buildClassicTIFF(t *testing.T, descs []string) ([]byte, []uint64) {
	t.Helper()
	var buf bytes.Buffer
	le := binary.LittleEndian
	w16 := func(v uint16) { b := make([]byte, 2); le.PutUint16(b, v); buf.Write(b) }
	w32 := func(v uint32) { b := make([]byte, 4); le.PutUint32(b, v); buf.Write(b) }
	buf.WriteString("II")
	w16(42)
	firstIFDLoc := uint32(buf.Len())
	w32(0)

	type patch struct{ at uint32 }
	var nextPatches []patch
	ifdOffsets := make([]uint64, len(descs))

	descOffsets := make([]uint32, len(descs))
	for i, d := range descs {
		descOffsets[i] = uint32(buf.Len())
		buf.WriteString(d)
		buf.WriteByte(0)
		if buf.Len()%2 != 0 {
			buf.WriteByte(0)
		}
	}

	for i, d := range descs {
		if buf.Len()%2 != 0 {
			buf.WriteByte(0)
		}
		ifdOffsets[i] = uint64(buf.Len())
		w16(2) // entry count
		w16(256)
		w16(4)
		w32(1)
		w32(256)
		w16(270)
		w16(2)
		w32(uint32(len(d) + 1))
		w32(descOffsets[i])
		nextLoc := uint32(buf.Len())
		w32(0)
		nextPatches = append(nextPatches, patch{at: nextLoc})
		_ = i
	}

	out := buf.Bytes()
	le.PutUint32(out[firstIFDLoc:], uint32(ifdOffsets[0]))
	for i := range descs {
		var next uint32
		if i+1 < len(descs) {
			next = uint32(ifdOffsets[i+1])
		}
		le.PutUint32(out[nextPatches[i].at:], next)
	}
	return out, ifdOffsets
}

func TestParsePopulatesRanges(t *testing.T) {
	data, offs := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4"})
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Ranges == nil {
		t.Fatal("Ranges not populated")
	}
	if got := f.Ranges.MinOffsetOfOwner(1); got > offs[1] {
		t.Errorf("MinOffsetOfOwner(1) = %d, want <= %d (its record)", got, offs[1])
	}
	if _, ok := f.Ranges.AnyRangeOfOwnerAtOrAfter(0, offs[1]); ok {
		t.Errorf("IFD0 owns bytes >= IFD1 record; expected clean layout")
	}
}

func TestParseRejectsSubIFDs(t *testing.T) {
	// Hand-build a 1-IFD classic TIFF whose single entry is SubIFDs (330).
	var b bytes.Buffer
	le := binary.LittleEndian
	w16 := func(v uint16) { x := make([]byte, 2); le.PutUint16(x, v); b.Write(x) }
	w32 := func(v uint32) { x := make([]byte, 4); le.PutUint32(x, v); b.Write(x) }
	b.WriteString("II")
	w16(42)
	w32(8) // first IFD at offset 8
	w16(1) // one entry
	w16(330)
	w16(4) // LONG
	w32(1)
	w32(0) // value
	w32(0) // next IFD
	data := b.Bytes()
	_, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("expected ErrUnexpectedLayout for SubIFDs, got nil")
	}
	if !errors.Is(err, ErrUnexpectedLayout) {
		t.Errorf("expected errors.Is(err, ErrUnexpectedLayout), got: %v", err)
	}
}

func TestParseChainAndOffsets(t *testing.T) {
	data, offs := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4", "Aperio\nmacro 8x8"})
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.IFDs) != 3 {
		t.Fatalf("got %d IFDs, want 3", len(f.IFDs))
	}
	for i := range f.IFDs {
		if f.IFDs[i].Offset != offs[i] {
			t.Errorf("IFD %d Offset = %d, want %d", i, f.IFDs[i].Offset, offs[i])
		}
	}
	desc, ok := f.IFDs[1].StringValue(TagImageDescription)
	if !ok || desc != "Aperio\nlabel 4x4" {
		t.Errorf("IFD1 desc = %q ok=%v", desc, ok)
	}
}
