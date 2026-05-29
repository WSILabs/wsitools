package source

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// IFDRecord is one IFD's tags-of-interest, returned by WalkIFDs.
// Field defaults (zero for ints, "" for strings, nil for maps) indicate
// the tag was not present.
type IFDRecord struct {
	// Index is the 0-based position in walk order. Top-level IFDs are
	// numbered first in main-chain order; SubIFDs are appended in the
	// order they're discovered (after their parent).
	Index int

	// Offset is the IFD's byte offset in the file. Useful for ordering
	// by physical layout if the caller wants to.
	Offset int64

	// IsBigTIFF is true if the file uses BigTIFF format.
	IsBigTIFF bool

	// IsSubIFD is true for IFDs reached via tag 330 (SubIFDs) on a
	// parent IFD.
	IsSubIFD    bool
	ParentIndex int // valid only when IsSubIFD; the parent's Index

	// Standard TIFF tags we extract for dump-ifds.
	Width            uint64 // tag 256
	Height           uint64 // tag 257
	TileWidth        uint64 // tag 322 (0 if not tiled)
	TileHeight       uint64 // tag 323 (0 if not tiled)
	Compression      uint64 // tag 259
	NewSubfileType   uint64 // tag 254
	ImageDescription string // tag 270 (truncated to 200 chars)

	// wsitools private tags 65080–65084.
	WSIImageType    string  // 65080
	WSILevelIndex   *uint64 // 65081 (pointer so we distinguish "absent" from 0)
	WSILevelCount   *uint64 // 65082
	WSISourceFormat string  // 65083
	WSIToolsVersion string  // 65084

	// ByteOrder is the file's byte order. Set by both WalkIFDs and
	// WalkIFDsRaw so consumers can decode RawEntry.Raw bytes correctly.
	ByteOrder binary.ByteOrder

	// Entries is populated only by WalkIFDsRaw — one entry per directory
	// entry, in directory order. nil from WalkIFDs.
	Entries []RawEntry
}

// RawEntry is one TIFF directory entry, captured verbatim by WalkIFDsRaw.
type RawEntry struct {
	Tag         uint16
	Type        uint16 // 1..18; values outside are preserved as-is
	Count       uint64
	ValueOffset uint64 // raw offset/inline-value field, native-decoded
	Raw         []byte // bytes pointed to (or inline bytes); nil if unreadable
}

// HasWSITags reports whether any of the wsitools private tags are present.
func (r *IFDRecord) HasWSITags() bool {
	return r.WSIImageType != "" || r.WSILevelIndex != nil ||
		r.WSILevelCount != nil || r.WSISourceFormat != "" ||
		r.WSIToolsVersion != ""
}

// WalkIFDs opens path and returns one IFDRecord per IFD found, in walk
// order: top-level chain first, with each IFD's SubIFDs (tag 330) appended
// immediately after the parent.
func WalkIFDs(path string) ([]IFDRecord, error) {
	return walkIFDs(path, false)
}

// WalkIFDsRaw is like WalkIFDs but additionally captures every directory
// entry into IFDRecord.Entries. Use this for full tag dumps; use WalkIFDs
// for the slim format-aware walk that only extracts a fixed tag whitelist.
func WalkIFDsRaw(path string) ([]IFDRecord, error) {
	return walkIFDs(path, true)
}

func walkIFDs(path string, captureAll bool) ([]IFDRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("ifdwalk: open %s: %w", path, err)
	}
	defer f.Close()

	hdr := make([]byte, 16)
	if _, err := io.ReadFull(f, hdr[:8]); err != nil {
		return nil, fmt.Errorf("ifdwalk: read header: %w", err)
	}
	var bo binary.ByteOrder
	switch string(hdr[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, fmt.Errorf("ifdwalk: not a TIFF (bad byte order %q)", hdr[:2])
	}
	magic := bo.Uint16(hdr[2:4])
	var bigTIFF bool
	var firstIFDOff int64
	switch magic {
	case 42:
		bigTIFF = false
		firstIFDOff = int64(bo.Uint32(hdr[4:8]))
	case 43:
		bigTIFF = true
		// BigTIFF: bytes 4-5 = offset size (8), 6-7 = constant 0,
		//          8-15  = offset of first IFD.
		if _, err := io.ReadFull(f, hdr[8:16]); err != nil {
			return nil, fmt.Errorf("ifdwalk: read BigTIFF header: %w", err)
		}
		if bo.Uint16(hdr[4:6]) != 8 || bo.Uint16(hdr[6:8]) != 0 {
			return nil, fmt.Errorf("ifdwalk: malformed BigTIFF header")
		}
		firstIFDOff = int64(bo.Uint64(hdr[8:16]))
	default:
		return nil, fmt.Errorf("ifdwalk: bad TIFF magic %d", magic)
	}

	var out []IFDRecord
	// Walk the main chain, recording parents as we go so we can later
	// append their SubIFDs.
	type subPending struct {
		parentIndex int
		offsets     []int64
	}
	var pending []subPending

	off := firstIFDOff
	for off != 0 {
		rec, nextOff, subs, err := readIFDFull(f, bo, bigTIFF, off, captureAll)
		if err != nil {
			return nil, err
		}
		rec.Index = len(out)
		rec.IsBigTIFF = bigTIFF
		rec.ByteOrder = bo
		out = append(out, rec)
		if len(subs) > 0 {
			pending = append(pending, subPending{
				parentIndex: rec.Index,
				offsets:     subs,
			})
		}
		off = nextOff
	}

	// Walk pending SubIFD chains, in the order their parents appeared.
	for _, p := range pending {
		for _, subOff := range p.offsets {
			// Each entry in the SubIFDs tag is itself the head of an
			// IFD chain (rare to have a chain, but follow it).
			cur := subOff
			for cur != 0 {
				rec, nextOff, _, err := readIFDFull(f, bo, bigTIFF, cur, captureAll)
				if err != nil {
					return nil, err
				}
				rec.Index = len(out)
				rec.IsBigTIFF = bigTIFF
				rec.ByteOrder = bo
				rec.IsSubIFD = true
				rec.ParentIndex = p.parentIndex
				out = append(out, rec)
				cur = nextOff
			}
		}
	}

	return out, nil
}

// readIFDFull reads one IFD at offset off, populates an IFDRecord, and
// returns (record, nextIFDOffset, subIFDOffsets, err). When captureAll is
// true, every directory entry is also appended to rec.Entries.
func readIFDFull(f *os.File, bo binary.ByteOrder, bigTIFF bool, off int64, captureAll bool) (IFDRecord, int64, []int64, error) {
	var rec IFDRecord
	rec.Offset = off

	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return rec, 0, nil, fmt.Errorf("ifdwalk: seek IFD@%d: %w", off, err)
	}

	// Entry count: 2 bytes (Classic) or 8 bytes (BigTIFF).
	var nEntries uint64
	if bigTIFF {
		var b [8]byte
		if _, err := io.ReadFull(f, b[:]); err != nil {
			return rec, 0, nil, fmt.Errorf("ifdwalk: read entry count: %w", err)
		}
		nEntries = bo.Uint64(b[:])
	} else {
		var b [2]byte
		if _, err := io.ReadFull(f, b[:]); err != nil {
			return rec, 0, nil, fmt.Errorf("ifdwalk: read entry count: %w", err)
		}
		nEntries = uint64(bo.Uint16(b[:]))
	}

	// Sanity bound: ClassicTIFF allows at most 65535 entries per IFD;
	// BigTIFF in theory allows 2^64. A real WSI IFD has < 200 tags.
	// Cap at 65536 to avoid OOM on hostile or corrupted input.
	if nEntries > 65536 {
		return rec, 0, nil, fmt.Errorf("ifdwalk: IFD@%d has implausible entry count %d", off, nEntries)
	}

	entrySize := 12
	if bigTIFF {
		entrySize = 20
	}
	entriesBuf := make([]byte, int(nEntries)*entrySize)
	if _, err := io.ReadFull(f, entriesBuf); err != nil {
		return rec, 0, nil, fmt.Errorf("ifdwalk: read entries: %w", err)
	}

	var subIFDs []int64
	for i := uint64(0); i < nEntries; i++ {
		entry := entriesBuf[int(i)*entrySize : int(i+1)*entrySize]
		tag := bo.Uint16(entry[0:2])
		typ := bo.Uint16(entry[2:4])
		var count uint64
		var valueField []byte
		if bigTIFF {
			count = bo.Uint64(entry[4:12])
			valueField = entry[12:20]
		} else {
			count = uint64(bo.Uint32(entry[4:8]))
			valueField = entry[8:12]
		}

		readValue := func() ([]byte, error) {
			return readTagValue(f, bo, bigTIFF, typ, count, valueField)
		}

		switch tag {
		case 254: // NewSubfileType
			if v, err := readValue(); err == nil {
				rec.NewSubfileType = readUint(bo, typ, v)
			}
		case 256: // ImageWidth
			if v, err := readValue(); err == nil {
				rec.Width = readUint(bo, typ, v)
			}
		case 257: // ImageLength
			if v, err := readValue(); err == nil {
				rec.Height = readUint(bo, typ, v)
			}
		case 259: // Compression
			if v, err := readValue(); err == nil {
				rec.Compression = readUint(bo, typ, v)
			}
		case 270: // ImageDescription
			if v, err := readValue(); err == nil {
				s := string(v)
				if len(s) > 200 {
					s = s[:200]
				}
				// Strip trailing NUL.
				for len(s) > 0 && s[len(s)-1] == 0 {
					s = s[:len(s)-1]
				}
				rec.ImageDescription = s
			}
		case 322: // TileWidth
			if v, err := readValue(); err == nil {
				rec.TileWidth = readUint(bo, typ, v)
			}
		case 323: // TileLength
			if v, err := readValue(); err == nil {
				rec.TileHeight = readUint(bo, typ, v)
			}
		case 330: // SubIFDs
			if v, err := readValue(); err == nil {
				// One offset per count.
				step := 4
				if bigTIFF || typ == 16 {
					step = 8
				}
				for j := 0; j+step <= len(v); j += step {
					var off64 int64
					if step == 8 {
						off64 = int64(bo.Uint64(v[j : j+8]))
					} else {
						off64 = int64(bo.Uint32(v[j : j+4]))
					}
					if off64 > 0 {
						subIFDs = append(subIFDs, off64)
					}
				}
			}
		case 65080:
			if v, err := readValue(); err == nil {
				s := string(v)
				for len(s) > 0 && s[len(s)-1] == 0 {
					s = s[:len(s)-1]
				}
				rec.WSIImageType = s
			}
		case 65081:
			if v, err := readValue(); err == nil {
				u := readUint(bo, typ, v)
				rec.WSILevelIndex = &u
			}
		case 65082:
			if v, err := readValue(); err == nil {
				u := readUint(bo, typ, v)
				rec.WSILevelCount = &u
			}
		case 65083:
			if v, err := readValue(); err == nil {
				s := string(v)
				for len(s) > 0 && s[len(s)-1] == 0 {
					s = s[:len(s)-1]
				}
				rec.WSISourceFormat = s
			}
		case 65084:
			if v, err := readValue(); err == nil {
				s := string(v)
				for len(s) > 0 && s[len(s)-1] == 0 {
					s = s[:len(s)-1]
				}
				rec.WSIToolsVersion = s
			}
		}

		if captureAll {
			raw, _ := readTagValue(f, bo, bigTIFF, typ, count, valueField)
			var voff uint64
			if bigTIFF {
				voff = bo.Uint64(valueField[:8])
			} else {
				voff = uint64(bo.Uint32(valueField[:4]))
			}
			rec.Entries = append(rec.Entries, RawEntry{
				Tag:         tag,
				Type:        typ,
				Count:       count,
				ValueOffset: voff,
				Raw:         raw,
			})
		}
	}

	// Read next-IFD offset (4 bytes Classic, 8 bytes BigTIFF).
	var nextOff int64
	if bigTIFF {
		var b [8]byte
		if _, err := io.ReadFull(f, b[:]); err != nil {
			return rec, 0, nil, fmt.Errorf("ifdwalk: read next-IFD offset: %w", err)
		}
		nextOff = int64(bo.Uint64(b[:]))
	} else {
		var b [4]byte
		if _, err := io.ReadFull(f, b[:]); err != nil {
			return rec, 0, nil, fmt.Errorf("ifdwalk: read next-IFD offset: %w", err)
		}
		nextOff = int64(bo.Uint32(b[:]))
	}

	return rec, nextOff, subIFDs, nil
}

// readTagValue returns the raw bytes for a TIFF tag's value, handling
// inline-vs-offset based on size. typ is the TIFF type; count is the
// number of elements; valueField is the 4-byte (Classic) or 8-byte
// (BigTIFF) inline value-or-offset slot.
func readTagValue(f *os.File, bo binary.ByteOrder, bigTIFF bool, typ uint16, count uint64, valueField []byte) ([]byte, error) {
	elemSize := tiffTypeSize(typ)
	if elemSize == 0 {
		return nil, fmt.Errorf("ifdwalk: unknown TIFF type %d", typ)
	}
	totalSize := count * uint64(elemSize)
	// Sanity bound: 256 MiB is far larger than any legitimate tag value
	// (ImageDescription is at most a few KB; tile-offset arrays scale
	// with grid size but stay well under this for realistic slides).
	const maxTagValueSize = 256 * 1024 * 1024
	if totalSize > maxTagValueSize {
		return nil, fmt.Errorf("ifdwalk: tag value size %d exceeds %d-byte limit", totalSize, maxTagValueSize)
	}

	inlineCap := uint64(4)
	if bigTIFF {
		inlineCap = 8
	}
	if totalSize <= inlineCap {
		return valueField[:totalSize], nil
	}
	// Out-of-line: valueField holds an offset.
	var off int64
	if bigTIFF {
		off = int64(bo.Uint64(valueField))
	} else {
		off = int64(bo.Uint32(valueField))
	}
	cur, _ := f.Seek(0, io.SeekCurrent)
	defer f.Seek(cur, io.SeekStart) //nolint:errcheck
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, fmt.Errorf("ifdwalk: seek tag value@%d: %w", off, err)
	}
	out := make([]byte, totalSize)
	if _, err := io.ReadFull(f, out); err != nil {
		return nil, fmt.Errorf("ifdwalk: read tag value: %w", err)
	}
	return out, nil
}

func tiffTypeSize(typ uint16) int {
	// TIFF types: 1=BYTE, 2=ASCII, 3=SHORT, 4=LONG, 5=RATIONAL, 6=SBYTE,
	// 7=UNDEFINED, 8=SSHORT, 9=SLONG, 10=SRATIONAL, 11=FLOAT, 12=DOUBLE,
	// 13=IFD, 16=LONG8, 17=SLONG8, 18=IFD8.
	switch typ {
	case 1, 2, 6, 7:
		return 1
	case 3, 8:
		return 2
	case 4, 9, 11, 13:
		return 4
	case 5, 10, 12, 16, 17, 18:
		return 8
	}
	return 0
}

// readUint extracts a single uint64 from a tag value buffer for a uint-ish
// TIFF type (BYTE/SHORT/LONG/LONG8). Returns 0 if the buffer is empty or
// the type isn't a uint variant.
func readUint(bo binary.ByteOrder, typ uint16, v []byte) uint64 {
	switch typ {
	case 1: // BYTE
		if len(v) >= 1 {
			return uint64(v[0])
		}
	case 3: // SHORT
		if len(v) >= 2 {
			return uint64(bo.Uint16(v[:2]))
		}
	case 4, 13: // LONG, IFD
		if len(v) >= 4 {
			return uint64(bo.Uint32(v[:4]))
		}
	case 16, 18: // LONG8, IFD8
		if len(v) >= 8 {
			return bo.Uint64(v[:8])
		}
	}
	return 0
}
