package edit

import (
	"fmt"
	"io"
)

// File is a parsed TIFF / BigTIFF file structure (metadata only, no pixel data).
type File struct {
	Header *Header
	IFDs   []*IFD
	// Ranges attributes every known byte range to its owning chain-order IFD
	// index. Populated by Parse; used by Splice for dominance checks.
	Ranges *RangeMap
}

// Parse walks the IFD chain from the header and returns a File describing
// the byte structure of the TIFF. size is the file length (used to bound
// reads; bytes.Reader.Size() satisfies this via int64(len(data))).
//
// Parse rejects files containing SubIFDs (tag 330): their pixel data is not
// attributable by a linear chain walk, which would make the dominance check
// unsound. Such files should be converted via the OME-TIFF / COG-WSI paths.
func Parse(r io.ReaderAt, size int64) (*File, error) {
	sr := io.NewSectionReader(r, 0, size)
	h, err := ParseHeader(sr)
	if err != nil {
		return nil, err
	}

	f := &File{Header: h, Ranges: &RangeMap{}}

	seen := map[uint64]bool{}
	offset := h.FirstIFDOffset
	for offset != 0 {
		if seen[offset] {
			return nil, fmt.Errorf("%w: IFD chain loop at offset %d", ErrUnexpectedLayout, offset)
		}
		seen[offset] = true
		ifd, err := ReadIFD(r, h, offset)
		if err != nil {
			return nil, fmt.Errorf("read IFD at %d: %w", offset, err)
		}

		// SubIFD safety: linear walk cannot attribute SubIFD-tree pixel data,
		// so the dominance check would be unsound. Reject at parse time.
		for _, e := range ifd.Entries {
			if e.Tag == TagSubIFDs {
				return nil, fmt.Errorf("%w: SubIFD pyramids not supported (Slice 2)", ErrUnexpectedLayout)
			}
		}

		owner := len(f.IFDs)
		f.IFDs = append(f.IFDs, ifd)

		// Attribute the IFD record itself.
		if err := f.Ranges.Add(Range{
			Start: ifd.Offset,
			End:   ifd.Offset + ifd.RecordLength,
			Owner: owner,
			What:  "ifd",
		}); err != nil {
			return nil, fmt.Errorf("ifd %d range: %w", owner, err)
		}

		// Attribute out-of-line tag blobs (skip inline and zero-size entries).
		for _, e := range ifd.Entries {
			if e.Inline || e.DataSize == 0 {
				continue
			}
			if err := f.Ranges.Add(Range{
				Start: e.DataOffset,
				End:   e.DataOffset + e.DataSize,
				Owner: owner,
				What:  fmt.Sprintf("tag%d", e.Tag),
			}); err != nil {
				return nil, fmt.Errorf("ifd %d tag %d range: %w", owner, e.Tag, err)
			}
		}

		// Attribute strip/tile pixel data.
		offs, offsOK := ifd.UintArray(TagStripOffsets, h.ByteOrder)
		if !offsOK {
			offs, offsOK = ifd.UintArray(TagTileOffsets, h.ByteOrder)
		}
		counts, countsOK := ifd.UintArray(TagStripByteCounts, h.ByteOrder)
		if !countsOK {
			counts, countsOK = ifd.UintArray(TagTileByteCounts, h.ByteOrder)
		}
		if offsOK && countsOK {
			if len(offs) != len(counts) {
				return nil, fmt.Errorf("%w: ifd %d has %d strip offsets but %d byte counts",
					ErrUnexpectedLayout, owner, len(offs), len(counts))
			}
			for i, off := range offs {
				if counts[i] == 0 {
					continue
				}
				if err := f.Ranges.Add(Range{
					Start: off,
					End:   off + counts[i],
					Owner: owner,
					What:  fmt.Sprintf("strip[%d]", i),
				}); err != nil {
					return nil, fmt.Errorf("ifd %d strip %d: %w", owner, i, err)
				}
			}
		}

		offset = ifd.NextOffset
	}
	return f, nil
}
