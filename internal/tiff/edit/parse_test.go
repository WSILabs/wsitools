package edit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// buildClassicTIFF returns a minimal little-endian classic TIFF with one IFD
// per entry in descs, each carrying ImageDescription (out-of-line) and a
// 4-byte strip, chained in order. Returns the bytes and per-IFD record offsets.
//
// Per-IFD layout (sequential, non-interleaved):
//
//	[4-byte strip data][4-byte unattributed gap][desc blob][IFD record]
//
// The 4-byte gap at (ifd_i_start + 4) lets tests inject a forged owner-0 range
// that begins past IFD 1's attributed start without overlapping any existing
// attributed range, triggering the Splice dominance check.
func buildClassicTIFF(t *testing.T, descs []string) ([]byte, []uint64) {
	t.Helper()
	var buf bytes.Buffer
	le := binary.LittleEndian
	w16 := func(v uint16) { b := make([]byte, 2); le.PutUint16(b, v); buf.Write(b) }
	w32 := func(v uint32) { b := make([]byte, 4); le.PutUint32(b, v); buf.Write(b) }

	// Header: "II" + 42 + firstIFDOffset placeholder.
	buf.WriteString("II")
	w16(42)
	firstIFDLoc := int(buf.Len())
	w32(0)

	// IFD record: 2(count) + 4*12(entries) + 4(next) = 54 bytes.
	// Entries: ImageDescription, StripOffsets, StripByteCounts, ImageWidth.
	const numEntries = 4

	type nextPatch struct{ at int }
	ifdOffsets := make([]uint64, len(descs))
	nextPatches := make([]nextPatch, len(descs))
	// We need two-pass: first compute all offsets, then emit.
	// Use a pre-pass to compute per-IFD offsets.
	type ifdLayout struct {
		stripOff uint32
		gapOff   uint32
		descOff  uint32
		ifdOff   uint32
	}
	layouts := make([]ifdLayout, len(descs))
	pos := uint32(8) // right after header
	for i, d := range descs {
		layouts[i].stripOff = pos
		pos += 4 // strip
		layouts[i].gapOff = pos
		pos += 4 // gap (unattributed)
		layouts[i].descOff = pos
		descLen := uint32(len(d) + 1) // include NUL
		pos += descLen
		if pos%2 != 0 {
			pos++ // word-align
		}
		layouts[i].ifdOff = pos
		pos += 2 + numEntries*12 + 4 // IFD record
	}

	// Emit all data.
	for i, d := range descs {
		// Strip data (4 bytes).
		buf.Write([]byte{byte(i), byte(i), byte(i), byte(i)})
		// Gap (4 unattributed bytes).
		buf.Write([]byte{0, 0, 0, 0})
		// Desc blob.
		buf.WriteString(d)
		buf.WriteByte(0)
		if buf.Len()%2 != 0 {
			buf.WriteByte(0)
		}
		// IFD record.
		ifdOffsets[i] = uint64(buf.Len())
		w16(numEntries)
		// ImageDescription: tag=270, ASCII, count=len(d)+1, out-of-line offset
		w16(270); w16(2); w32(uint32(len(d) + 1)); w32(layouts[i].descOff)
		// StripOffsets: tag=273, LONG, count=1, value=stripOff (inline, 4 bytes)
		w16(273); w16(4); w32(1); w32(layouts[i].stripOff)
		// StripByteCounts: tag=279, LONG, count=1, value=4 (inline)
		w16(279); w16(4); w32(1); w32(4)
		// ImageWidth: tag=256, LONG, count=1, value=1 (inline)
		w16(256); w16(4); w32(1); w32(1)
		// next-IFD pointer placeholder
		nextPatches[i] = nextPatch{at: buf.Len()}
		w32(0)
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
	_ = layouts
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
