package cogwsi

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// TIFF data types we use.
const (
	tiffByte     = 1
	tiffASCII    = 2
	tiffShort    = 3
	tiffLong     = 4
	tiffRational = 5
	tiffDouble   = 12
	tiffLong8    = 16
)

type ifdEntry struct {
	tag         uint16
	tiffType    uint16
	count       uint64
	inlineValue [8]byte // up to 8 bytes; classic uses the low 4
	externalRaw []byte  // if non-nil, value is external and this is the payload
}

// ifdBuilder accumulates TIFF directory entries; Encode emits the
// directory record + concatenated external bytes for entries that don't
// fit inline.
type ifdBuilder struct {
	bigtiff bool
	entries []ifdEntry
}

func newIFDBuilder(bigtiff bool) *ifdBuilder {
	return &ifdBuilder{bigtiff: bigtiff}
}

func (b *ifdBuilder) inlineCap() int {
	if b.bigtiff {
		return 8
	}
	return 4
}

func (b *ifdBuilder) addRaw(tag uint16, tiffType uint16, count uint64, payload []byte) {
	e := ifdEntry{tag: tag, tiffType: tiffType, count: count}
	if len(payload) <= b.inlineCap() {
		copy(e.inlineValue[:], payload)
	} else {
		e.externalRaw = payload
	}
	b.entries = append(b.entries, e)
}

// AddShort appends a SHORT (uint16) array entry.
func (b *ifdBuilder) AddShort(tag uint16, vals []uint16) {
	payload := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint16(payload[i*2:], v)
	}
	b.addRaw(tag, tiffShort, uint64(len(vals)), payload)
}

// AddLong appends a LONG (uint32) array entry.
func (b *ifdBuilder) AddLong(tag uint16, vals []uint32) {
	payload := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(payload[i*4:], v)
	}
	b.addRaw(tag, tiffLong, uint64(len(vals)), payload)
}

// AddLong8 appends a BigTIFF LONG8 (uint64) array entry. Only valid in BigTIFF.
func (b *ifdBuilder) AddLong8(tag uint16, vals []uint64) {
	payload := make([]byte, 8*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint64(payload[i*8:], v)
	}
	b.addRaw(tag, tiffLong8, uint64(len(vals)), payload)
}

// AddTileOffsets / AddTileByteCounts pick LONG or LONG8 depending on bigtiff.
// Returns an error if any offset exceeds the classic TIFF 4 GiB limit.
func (b *ifdBuilder) AddTileOffsets(tag uint16, offsets []uint64) error {
	if b.bigtiff {
		b.AddLong8(tag, offsets)
		return nil
	}
	asLong := make([]uint32, len(offsets))
	for i, o := range offsets {
		if o > 0xFFFFFFFF {
			return fmt.Errorf("cogwsi: tile offset %d (tag %d, index %d) overflows classic TIFF; BigTIFF promotion missed", o, tag, i)
		}
		asLong[i] = uint32(o)
	}
	b.AddLong(tag, asLong)
	return nil
}

// AddASCII appends an ASCII entry with the trailing NUL count + 1.
func (b *ifdBuilder) AddASCII(tag uint16, s string) {
	payload := append([]byte(s), 0)
	b.addRaw(tag, tiffASCII, uint64(len(payload)), payload)
}

// AddBytes appends raw bytes (BYTE type).
func (b *ifdBuilder) AddBytes(tag uint16, payload []byte) {
	b.addRaw(tag, tiffByte, uint64(len(payload)), payload)
}

// AddDouble appends a DOUBLE (float64) array entry.
func (b *ifdBuilder) AddDouble(tag uint16, vals []float64) {
	payload := make([]byte, 8*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint64(payload[i*8:], math.Float64bits(v))
	}
	b.addRaw(tag, tiffDouble, uint64(len(vals)), payload)
}

// Encode writes the IFD record at ifdOffset and returns:
//   - ifd: the directory bytes
//   - ext: concatenated external bytes, placed at ifdOffset + len(ifd)
//
// External entries' value fields are filled in with their final absolute
// offsets.
func (b *ifdBuilder) Encode(ifdOffset uint64) (ifd, ext []byte, err error) {
	if !b.bigtiff && ifdOffset > 0xFFFFFFFF {
		return nil, nil, fmt.Errorf("classic TIFF ifd offset overflow: %d", ifdOffset)
	}

	// Sort entries by tag (TIFF requires ascending tag order).
	sorted := append([]ifdEntry(nil), b.entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].tag < sorted[j].tag })

	// Compute directory size.
	var dirSize uint64
	if b.bigtiff {
		dirSize = 8 + uint64(len(sorted))*bigTIFFTagEntrySize + 8
	} else {
		dirSize = 2 + uint64(len(sorted))*classicTagEntrySize + 4
	}

	ifd = make([]byte, dirSize)
	// Walk external entries; assign offsets immediately after the IFD record.
	cursor := ifdOffset + dirSize
	var extBuf []byte
	for i := range sorted {
		if sorted[i].externalRaw == nil {
			continue
		}
		// Write offset into inlineValue, then append payload to extBuf.
		setOffset(sorted[i].inlineValue[:], cursor, b.bigtiff)
		extBuf = append(extBuf, sorted[i].externalRaw...)
		cursor += uint64(len(sorted[i].externalRaw))
	}

	// Write entry_count.
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
			off += bigTIFFTagEntrySize
		} else {
			binary.LittleEndian.PutUint32(ifd[off+4:off+8], uint32(e.count))
			copy(ifd[off+8:off+12], e.inlineValue[:4])
			off += classicTagEntrySize
		}
	}
	// next-IFD field stays zero; the writer patches it during Close.
	return ifd, extBuf, nil
}

func setOffset(slot []byte, val uint64, bigtiff bool) {
	if bigtiff {
		binary.LittleEndian.PutUint64(slot[:8], val)
	} else {
		binary.LittleEndian.PutUint32(slot[:4], uint32(val))
	}
}
