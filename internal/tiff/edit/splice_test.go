package edit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.tiff")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSpliceRemoveMiddleIFD(t *testing.T) {
	data, _ := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4", "Aperio\nmacro 8x8"})
	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "out.tiff")

	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := Splice(SpliceParams{
		InPath: in, OutPath: out, File: f, Mode: SpliceRemove, TargetIdx: 1, Fsync: false,
	}); err != nil {
		t.Fatalf("Splice: %v", err)
	}

	outData, _ := os.ReadFile(out)
	of, err := Parse(bytes.NewReader(outData), int64(len(outData)))
	if err != nil {
		t.Fatalf("re-parse output: %v", err)
	}
	if len(of.IFDs) != 2 {
		t.Fatalf("output has %d IFDs, want 2", len(of.IFDs))
	}
	if bytes.Contains(outData, []byte("label 4x4")) {
		t.Errorf("removed label bytes still present in output (PHI not erased)")
	}
	d0, _ := of.IFDs[0].StringValue(TagImageDescription)
	d1, _ := of.IFDs[1].StringValue(TagImageDescription)
	if d0 != "Aperio\nimage" || d1 != "Aperio\nmacro 8x8" {
		t.Errorf("output descs = %q, %q", d0, d1)
	}
}

// TestSpliceRemoveWithFsync exercises the Fsync (default-on) commit path, which
// the other splice tests skip (Fsync:false). Guards wsitools#38: the output temp
// was opened O_WRONLY, and Windows FlushFileBuffers returns ERROR_ACCESS_DENIED
// on a write-only handle, so every default splice edit failed on Windows. This
// passes on all platforms once the temp is opened O_RDWR; on Windows CI it would
// fail without the fix.
func TestSpliceRemoveWithFsync(t *testing.T) {
	data, _ := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4", "Aperio\nmacro 8x8"})
	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "out.tiff")

	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := Splice(SpliceParams{
		InPath: in, OutPath: out, File: f, Mode: SpliceRemove, TargetIdx: 1, Fsync: true,
	}); err != nil {
		t.Fatalf("Splice with Fsync=true: %v", err)
	}
	outData, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	of, err := Parse(bytes.NewReader(outData), int64(len(outData)))
	if err != nil {
		t.Fatalf("re-parse output: %v", err)
	}
	if len(of.IFDs) != 2 {
		t.Fatalf("output has %d IFDs, want 2", len(of.IFDs))
	}
}

func TestSpliceRefusesInterleavedLayout(t *testing.T) {
	data, _ := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4", "Aperio\nmacro"})
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	cut := f.Ranges.MinOffsetOfOwnersAtOrAfter(1)
	_ = f.Ranges.Add(Range{Start: cut + 4, End: cut + 8, Owner: 0, What: "forged"})

	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "out.tiff")
	err = Splice(SpliceParams{InPath: in, OutPath: out, File: f, Mode: SpliceRemove, TargetIdx: 1})
	if !errors.Is(err, ErrUnexpectedLayout) {
		t.Fatalf("want ErrUnexpectedLayout, got %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Errorf("output created despite refusal")
	}
}

func TestSpliceRemoveBigTIFF(t *testing.T) {
	data, _ := buildBigTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4", "Aperio\nmacro 8x8"})
	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "outbig.tiff")
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := Splice(SpliceParams{InPath: in, OutPath: out, File: f, Mode: SpliceRemove, TargetIdx: 1}); err != nil {
		t.Fatalf("Splice bigtiff: %v", err)
	}
	outData, _ := os.ReadFile(out)
	of, err := Parse(bytes.NewReader(outData), int64(len(outData)))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(of.IFDs) != 2 {
		t.Fatalf("got %d IFDs, want 2", len(of.IFDs))
	}
	if bytes.Contains(outData, []byte("label 4x4")) {
		t.Errorf("label bytes still present")
	}
}

func makeReplacement(desc string) *ReplacementIFD {
	strip := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	descBytes := append([]byte(desc), 0)
	le := func(v uint16) []byte { return []byte{byte(v), byte(v >> 8)} }
	le32 := func(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }
	return &ReplacementIFD{
		Tags: []OutTag{
			{Tag: TagImageWidth, Type: TypeLong, Count: 1, Inline: true, Bytes: le32(2)},
			{Tag: TagImageLength, Type: TypeLong, Count: 1, Inline: true, Bytes: le32(2)},
			{Tag: TagImageDescription, Type: TypeASCII, Count: uint64(len(descBytes)), Inline: false, Bytes: descBytes},
			{Tag: TagStripOffsets, Type: TypeLong, Count: 1, Inline: false, Bytes: make([]byte, 4), ResolvesToOffset: true, OffsetRefs: []int{0}},
			{Tag: TagStripByteCounts, Type: TypeLong, Count: 1, Inline: true, Bytes: le32(uint32(len(strip)))},
			{Tag: TagCompression, Type: TypeShort, Count: 1, Inline: true, Bytes: le(1)},
		},
		StripData: [][]byte{strip},
	}
}

func TestSpliceReplace(t *testing.T) {
	data, _ := buildClassicTIFF(t, []string{"Aperio\nimage", "Aperio\nlabel 4x4"})
	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "out.tiff")
	f, _ := Parse(bytes.NewReader(data), int64(len(data)))
	if err := Splice(SpliceParams{InPath: in, OutPath: out, File: f,
		Mode: SpliceReplace, TargetIdx: 1, Replacement: makeReplacement("Aperio\nlabel NEW")}); err != nil {
		t.Fatal(err)
	}
	outData, _ := os.ReadFile(out)
	of, err := Parse(bytes.NewReader(outData), int64(len(outData)))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(of.IFDs) != 2 {
		t.Fatalf("got %d IFDs, want 2", len(of.IFDs))
	}
	if bytes.Contains(outData, []byte("label 4x4")) {
		t.Errorf("old label bytes still present")
	}
	d1, _ := of.IFDs[1].StringValue(TagImageDescription)
	if d1 != "Aperio\nlabel NEW" {
		t.Errorf("replaced desc = %q", d1)
	}
}

func TestSpliceAppend(t *testing.T) {
	data, _ := buildClassicTIFF(t, []string{"Aperio\nimage"})
	in := writeTemp(t, data)
	out := filepath.Join(filepath.Dir(in), "out.tiff")
	f, _ := Parse(bytes.NewReader(data), int64(len(data)))
	if err := Splice(SpliceParams{InPath: in, OutPath: out, File: f,
		Mode: SpliceAppend, TargetIdx: len(f.IFDs), Replacement: makeReplacement("Aperio\nlabel ADDED")}); err != nil {
		t.Fatal(err)
	}
	outData, _ := os.ReadFile(out)
	of, err := Parse(bytes.NewReader(outData), int64(len(outData)))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(of.IFDs) != 2 {
		t.Fatalf("got %d IFDs, want 2", len(of.IFDs))
	}
	d1, _ := of.IFDs[1].StringValue(TagImageDescription)
	if d1 != "Aperio\nlabel ADDED" {
		t.Errorf("appended desc = %q", d1)
	}
}

// buildBigTIFF builds a minimal little-endian BigTIFF with one IFD per desc.
// Per-IFD layout (sequential, non-interleaved):
//
//	[4-byte strip data][4-byte unattributed gap][desc blob][IFD record]
//
// BigTIFF header: "II" + magic=43 + offsetSize=8 + reserved=0 + firstIFDOffset(8).
// BigTIFF IFD record: count(8) + entries×20 + nextOffset(8).
// 4 entries per IFD: ImageDescription, StripOffsets, StripByteCounts, ImageWidth.
func buildBigTIFF(t *testing.T, descs []string) ([]byte, []uint64) {
	t.Helper()
	var buf bytes.Buffer
	le := binary.LittleEndian
	w16 := func(v uint16) { b := make([]byte, 2); le.PutUint16(b, v); buf.Write(b) }
	w64 := func(v uint64) { b := make([]byte, 8); le.PutUint64(b, v); buf.Write(b) }

	// BigTIFF header: "II" + magic=43 + offsetSize(2) + reserved(2) + firstIFDOffset(8).
	buf.WriteString("II")
	w16(43) // BigTIFF magic
	w16(8)  // offset bytesize
	w16(0)  // reserved
	firstIFDLoc := buf.Len()
	w64(0) // placeholder for first IFD offset (header is now 16 bytes)

	// BigTIFF IFD record: 8(count) + 4*20(entries) + 8(next) = 96 bytes.
	// Per-IFD: strip(4) + gap(4) + desc + IFD(96).

	type nextPatch struct{ at int }
	ifdOffsets := make([]uint64, len(descs))
	nextPatches := make([]nextPatch, len(descs))

	// Pre-compute offsets.
	type biglayout struct {
		stripOff uint64
		descOff  uint64
		ifdOff   uint64
	}
	layouts := make([]biglayout, len(descs))
	pos := uint64(16) // after 16-byte BigTIFF header
	for i, d := range descs {
		layouts[i].stripOff = pos
		pos += 4 // strip bytes
		pos += 4 // unattributed gap
		layouts[i].descOff = pos
		descLen := uint64(len(d) + 1)
		pos += descLen
		if pos%2 != 0 {
			pos++
		}
		layouts[i].ifdOff = pos
		pos += 96 // 8 + 4*20 + 8
	}

	// Emit data.
	for i, d := range descs {
		// Strip data (4 bytes).
		buf.Write([]byte{byte(i), byte(i), byte(i), byte(i)})
		// Unattributed gap (4 bytes).
		buf.Write([]byte{0, 0, 0, 0})
		// Desc blob.
		buf.WriteString(d)
		buf.WriteByte(0)
		if buf.Len()%2 != 0 {
			buf.WriteByte(0)
		}
		// IFD record.
		ifdOffsets[i] = uint64(buf.Len())
		w64(4) // 4 entries

		// Entry: ImageDescription (tag=270, ASCII, count=len+1, out-of-line)
		w16(270); w16(2)
		w64(uint64(len(d) + 1)) // count
		w64(layouts[i].descOff) // out-of-line offset

		// Entry: StripOffsets (tag=273, LONG8=16, count=1, inline value=stripOff)
		// TypeLong8 count=1 → DataSize=8 = valueFieldSize=8 → Inline.
		// Parse still uses UintArray to read strip offsets for range attribution.
		w16(273); w16(16) // TypeLong8
		w64(1)            // count
		w64(layouts[i].stripOff) // inline value (strip offset)

		// Entry: StripByteCounts (tag=279, LONG8=16, count=1, inline=4)
		w16(279); w16(16)
		w64(1)
		w64(4) // inline value: 4 bytes per strip

		// Entry: ImageWidth (tag=256, SHORT=3, count=1, inline=1)
		{
			vb := make([]byte, 8)
			le.PutUint16(vb, 1) // value=1
			w16(256); w16(3); w64(1); buf.Write(vb)
		}

		// next-IFD pointer (8 bytes).
		nextPatches[i] = nextPatch{at: buf.Len()}
		w64(0)
	}

	out := buf.Bytes()
	le.PutUint64(out[firstIFDLoc:], ifdOffsets[0])
	for i := range descs {
		var next uint64
		if i+1 < len(descs) {
			next = ifdOffsets[i+1]
		}
		le.PutUint64(out[nextPatches[i].at:], next)
	}
	_ = layouts
	return out, ifdOffsets
}
