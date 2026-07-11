package cogwsiwriter

import (
	"bufio"
	"io"
	"os"
)

// spoolEntry records one tile (or associated image) in the spool.
type spoolEntry struct {
	Length uint32 // bytes
	Off    int64  // absolute byte offset of this entry within the spool file
}

// spool is a scratch file accumulating compressed tile bytes during
// AddLevel/WriteTile or AddAssociated. Entries are appended in source
// order; Close streams them into the output at finalize time.
type spool struct {
	path    string
	f       *os.File
	bw      *bufio.Writer // buffers Appends so per-tile writes aren't a syscall each
	entries []spoolEntry
	size    int64 // running total of appended bytes = next entry's offset
	flushed bool  // bw drained to f (must precede any read-back)

	sr     *bufio.Reader // buffered sequential read-back (row-major finalize)
	seqIdx int           // next entry index for NextEntry
}

func openSpool(path string) (*spool, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &spool{path: path, f: f, bw: bufio.NewWriterSize(f, 1<<20)}, nil
}

// Append writes one entry; records its length and absolute spool offset. Writes
// go through a buffered writer, so a per-tile Append is not a per-tile syscall.
func (s *spool) Append(b []byte) error {
	if _, err := s.bw.Write(b); err != nil {
		return err
	}
	s.entries = append(s.entries, spoolEntry{Length: uint32(len(b)), Off: s.size})
	s.size += int64(len(b))
	return nil
}

// sync drains the append buffer to the file. Idempotent; must run before any
// read-back (Rewind/Read/ReadEntryAt) so the buffered tail is on disk.
func (s *spool) sync() error {
	if s.flushed || s.bw == nil {
		return nil
	}
	if err := s.bw.Flush(); err != nil {
		return err
	}
	s.flushed = true
	return nil
}

// Entries returns the accumulated entry records (in append order).
func (s *spool) Entries() []spoolEntry { return s.entries }

// Rewind seeks the spool to the beginning for sequential read-back.
// Callers must invoke this before Read.
func (s *spool) Rewind() error {
	if err := s.sync(); err != nil {
		return err
	}
	_, err := s.f.Seek(0, io.SeekStart)
	return err
}

// Read implements io.Reader on the underlying file (post-Rewind).
func (s *spool) Read(p []byte) (int, error) { return s.f.Read(p) }

// BeginSeqRead rewinds the spool and arms a buffered sequential reader, so
// entries can be read back in append order via NextEntry with ~fileSize/bufSize
// read syscalls instead of one ReadAt per entry. Used by the finalize when the
// tile emission order equals the spool append order (row-major). (wsitools#37)
func (s *spool) BeginSeqRead() error {
	if err := s.Rewind(); err != nil { // Rewind syncs the append buffer first
		return err
	}
	if s.sr == nil {
		s.sr = bufio.NewReaderSize(s.f, 1<<20)
	} else {
		s.sr.Reset(s.f)
	}
	s.seqIdx = 0
	return nil
}

// NextEntry reads the next entry's bytes in append order from the buffered
// sequential reader armed by BeginSeqRead. Byte-for-byte identical to
// ReadEntryAt(seqIdx); it just avoids the per-entry ReadAt syscall.
func (s *spool) NextEntry() ([]byte, error) {
	buf := make([]byte, s.entries[s.seqIdx].Length)
	if _, err := io.ReadFull(s.sr, buf); err != nil {
		return nil, err
	}
	s.seqIdx++
	return buf, nil
}

// ReadEntryAt reads the compressed bytes for entry idx (0-based, raster order)
// into a freshly allocated buffer and returns it. idx must be in [0, len(entries)).
// Uses ReadAt so it does not disturb the sequential read position used by Rewind+Read.
func (s *spool) ReadEntryAt(idx int) ([]byte, error) {
	if err := s.sync(); err != nil {
		return nil, err
	}
	e := s.entries[idx]
	buf := make([]byte, e.Length)
	_, err := s.f.ReadAt(buf, e.Off)
	return buf, err
}

// Close closes the file handle without removing the file. Use Remove to
// also delete from disk.
func (s *spool) Close() error {
	if s.f == nil {
		return nil
	}
	_ = s.sync() // drain any un-read-back tail before closing
	err := s.f.Close()
	s.f = nil
	return err
}

// Remove closes (if open) and unlinks the spool file.
func (s *spool) Remove() error {
	_ = s.Close()
	return os.Remove(s.path)
}
