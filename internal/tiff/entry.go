package tiff

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// Internal byte-size constants for TIFF directory layout.
const (
	classicEntrySize = 12 // uint16 tag, uint16 type, uint32 count, uint32 value
	bigTIFFEntrySize = 20 // uint16 tag, uint16 type, uint64 count, uint64 value
)

// EntrySize returns the byte length of one IFD entry (directory record),
// 12 for classic TIFF and 20 for BigTIFF.
func EntrySize(bigtiff bool) int {
	if bigtiff {
		return bigTIFFEntrySize
	}
	return classicEntrySize
}

// IFDRecordSize returns the byte length of an IFD directory record with
// tagCount entries. Classic: 2 (count) + N*12 + 4 (next-IFD). BigTIFF:
// 8 (count) + N*20 + 8 (next-IFD).
func IFDRecordSize(tagCount int, bigtiff bool) int {
	if bigtiff {
		return 8 + tagCount*bigTIFFEntrySize + 8
	}
	return 2 + tagCount*classicEntrySize + 4
}

type ifdEntry struct {
	tag         uint16
	tiffType    uint16
	count       uint64
	inlineValue [8]byte
	externalRaw []byte
}

// EntryBuilder accumulates TIFF directory entries; Encode emits the
// directory record + concatenated external bytes for entries that
// don't fit inline. Little-endian only.
type EntryBuilder struct {
	bigtiff bool
	entries []ifdEntry
}

// NewEntryBuilder returns a new builder. bigtiff selects classic vs
// BigTIFF entry layout.
func NewEntryBuilder(bigtiff bool) *EntryBuilder {
	return &EntryBuilder{bigtiff: bigtiff}
}

func (b *EntryBuilder) inlineCap() int {
	if b.bigtiff {
		return 8
	}
	return 4
}

func (b *EntryBuilder) addRaw(tag uint16, tiffType uint16, count uint64, payload []byte) {
	e := ifdEntry{tag: tag, tiffType: tiffType, count: count}
	if len(payload) <= b.inlineCap() {
		copy(e.inlineValue[:], payload)
	} else {
		e.externalRaw = payload
	}
	b.entries = append(b.entries, e)
}

// AddShort appends a SHORT (uint16) array entry.
func (b *EntryBuilder) AddShort(tag uint16, vals []uint16) {
	payload := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint16(payload[i*2:], v)
	}
	b.addRaw(tag, TypeSHORT, uint64(len(vals)), payload)
}

// AddLong appends a LONG (uint32) array entry.
func (b *EntryBuilder) AddLong(tag uint16, vals []uint32) {
	payload := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(payload[i*4:], v)
	}
	b.addRaw(tag, TypeLONG, uint64(len(vals)), payload)
}

// AddLong8 appends a BigTIFF LONG8 (uint64) array entry. Only valid in BigTIFF.
func (b *EntryBuilder) AddLong8(tag uint16, vals []uint64) {
	payload := make([]byte, 8*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint64(payload[i*8:], v)
	}
	b.addRaw(tag, TypeLONG8, uint64(len(vals)), payload)
}

// AddTileOffsets appends offsets as LONG (classic) or LONG8 (BigTIFF).
// Returns an error if any offset exceeds the classic TIFF 4 GiB limit
// when in classic mode.
func (b *EntryBuilder) AddTileOffsets(tag uint16, offsets []uint64) error {
	if b.bigtiff {
		b.AddLong8(tag, offsets)
		return nil
	}
	asLong := make([]uint32, len(offsets))
	for i, o := range offsets {
		if o > 0xFFFFFFFF {
			return fmt.Errorf("tiff: tile offset %d (tag %d, index %d) overflows classic TIFF; BigTIFF promotion missed", o, tag, i)
		}
		asLong[i] = uint32(o)
	}
	b.AddLong(tag, asLong)
	return nil
}

// AddASCII appends an ASCII entry. count includes the trailing NUL.
func (b *EntryBuilder) AddASCII(tag uint16, s string) {
	payload := append([]byte(s), 0)
	b.addRaw(tag, TypeASCII, uint64(len(payload)), payload)
}

// AddBytes appends raw bytes (BYTE type).
func (b *EntryBuilder) AddBytes(tag uint16, payload []byte) {
	b.addRaw(tag, TypeBYTE, uint64(len(payload)), payload)
}

// AddDouble appends a DOUBLE (float64) array entry.
func (b *EntryBuilder) AddDouble(tag uint16, vals []float64) {
	payload := make([]byte, 8*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint64(payload[i*8:], math.Float64bits(v))
	}
	b.addRaw(tag, TypeDOUBLE, uint64(len(vals)), payload)
}

// AddRational appends a RATIONAL (uint32/uint32) array entry. Pairs of
// (numerator, denominator) per value.
func (b *EntryBuilder) AddRational(tag uint16, nums, denoms []uint32) {
	if len(nums) != len(denoms) {
		panic("tiff: AddRational: nums/denoms length mismatch")
	}
	payload := make([]byte, 8*len(nums))
	for i := range nums {
		binary.LittleEndian.PutUint32(payload[i*8:], nums[i])
		binary.LittleEndian.PutUint32(payload[i*8+4:], denoms[i])
	}
	b.addRaw(tag, TypeRATIONAL, uint64(len(nums)), payload)
}

// Encode serializes the IFD record at ifdOffset and returns:
//   - ifd: the directory bytes
//   - ext: external bytes that go at ifdOffset + len(ifd)
//
// External entries' inline-value slots are filled with their final
// absolute offsets.
func (b *EntryBuilder) Encode(ifdOffset uint64) (ifd, ext []byte, err error) {
	if !b.bigtiff && ifdOffset > 0xFFFFFFFF {
		return nil, nil, fmt.Errorf("tiff: classic TIFF IFD offset overflow: %d", ifdOffset)
	}

	// Sort by tag (TIFF requires ascending tag order).
	sorted := append([]ifdEntry(nil), b.entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].tag < sorted[j].tag })

	dirSize := uint64(IFDRecordSize(len(sorted), b.bigtiff))
	ifd = make([]byte, dirSize)

	// Assign external offsets, accumulate external buffer.
	cursor := ifdOffset + dirSize
	var extBuf []byte
	for i := range sorted {
		if sorted[i].externalRaw == nil {
			continue
		}
		setOffset(sorted[i].inlineValue[:], cursor, b.bigtiff)
		extBuf = append(extBuf, sorted[i].externalRaw...)
		cursor += uint64(len(sorted[i].externalRaw))
	}

	// Write entry count.
	if b.bigtiff {
		binary.LittleEndian.PutUint64(ifd[0:8], uint64(len(sorted)))
	} else {
		binary.LittleEndian.PutUint16(ifd[0:2], uint16(len(sorted)))
	}

	// Write entries.
	off := uint64(8)
	if !b.bigtiff {
		off = 2
	}
	for _, e := range sorted {
		binary.LittleEndian.PutUint16(ifd[off:off+2], e.tag)
		binary.LittleEndian.PutUint16(ifd[off+2:off+4], e.tiffType)
		if b.bigtiff {
			binary.LittleEndian.PutUint64(ifd[off+4:off+12], e.count)
			copy(ifd[off+12:off+20], e.inlineValue[:8])
			off += bigTIFFEntrySize
		} else {
			binary.LittleEndian.PutUint32(ifd[off+4:off+8], uint32(e.count))
			copy(ifd[off+8:off+12], e.inlineValue[:4])
			off += classicEntrySize
		}
	}
	// next-IFD field stays zero; the writer patches it during finalize.
	return ifd, extBuf, nil
}

func setOffset(slot []byte, val uint64, bigtiff bool) {
	if bigtiff {
		binary.LittleEndian.PutUint64(slot[:8], val)
	} else {
		binary.LittleEndian.PutUint32(slot[:4], uint32(val))
	}
}
