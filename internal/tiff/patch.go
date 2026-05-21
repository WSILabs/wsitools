package tiff

import (
	"encoding/binary"
	"io"
)

// PatchUint32 writes a little-endian uint32 at the given offset. Used
// by streaming-style writers to fill in IFD offsets they emitted as
// placeholders.
func PatchUint32(w io.WriterAt, at int64, v uint32) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	_, err := w.WriteAt(buf[:], at)
	return err
}

// PatchUint64 writes a little-endian uint64 at the given offset.
// BigTIFF equivalent of PatchUint32.
func PatchUint64(w io.WriterAt, at int64, v uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	_, err := w.WriteAt(buf[:], at)
	return err
}
