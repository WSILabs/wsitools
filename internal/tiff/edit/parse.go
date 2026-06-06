package edit

import (
	"fmt"
	"io"
)

// File is a parsed TIFF / BigTIFF file structure (metadata only, no pixel data).
type File struct {
	Header *Header
	IFDs   []*IFD
	// Ranges is populated by a later task (Task 4/5).
	Ranges *RangeMap
}

// Parse walks the IFD chain from the header and returns a File describing
// the byte structure of the TIFF. size is the file length (used to bound
// reads; bytes.Reader.Size() satisfies this via int64(len(data))).
//
// Ranges is left nil in this task; Task 5 populates it.
func Parse(r io.ReaderAt, size int64) (*File, error) {
	sr := io.NewSectionReader(r, 0, size)
	h, err := ParseHeader(sr)
	if err != nil {
		return nil, err
	}

	f := &File{Header: h}

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
		f.IFDs = append(f.IFDs, ifd)
		offset = ifd.NextOffset
	}
	return f, nil
}
