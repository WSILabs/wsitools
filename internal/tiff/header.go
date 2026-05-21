package tiff

import (
	"encoding/binary"
	"io"
)

// HeaderSize returns the byte length of the TIFF header: 8 for classic,
// 16 for BigTIFF.
func HeaderSize(bigtiff bool) int {
	if bigtiff {
		return 16
	}
	return 8
}

// WriteHeader writes the TIFF header at offset 0 of w. firstIFDOffset
// is the absolute byte offset of IFD 0 in the output file. All bytes
// are little-endian.
func WriteHeader(w io.WriterAt, bigtiff bool, firstIFDOffset uint64) error {
	hdr := make([]byte, HeaderSize(bigtiff))
	hdr[0], hdr[1] = 'I', 'I'
	if bigtiff {
		binary.LittleEndian.PutUint16(hdr[2:4], 0x002B)
		binary.LittleEndian.PutUint16(hdr[4:6], 8)
		binary.LittleEndian.PutUint16(hdr[6:8], 0)
		binary.LittleEndian.PutUint64(hdr[8:16], firstIFDOffset)
	} else {
		binary.LittleEndian.PutUint16(hdr[2:4], 0x002A)
		binary.LittleEndian.PutUint32(hdr[4:8], uint32(firstIFDOffset))
	}
	_, err := w.WriteAt(hdr, 0)
	return err
}
