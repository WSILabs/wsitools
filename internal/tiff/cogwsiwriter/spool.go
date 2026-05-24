package cogwsiwriter

import (
	"io"
	"os"
)

// spoolEntry records one tile (or associated image) in the spool.
type spoolEntry struct {
	Length uint32 // bytes
}

// spool is a scratch file accumulating compressed tile bytes during
// AddLevel/WriteTile or AddAssociated. Entries are appended in source
// order; Close streams them into the output at finalize time.
type spool struct {
	path    string
	f       *os.File
	entries []spoolEntry
}

func openSpool(path string) (*spool, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &spool{path: path, f: f}, nil
}

// Append writes one entry; records its length.
func (s *spool) Append(b []byte) error {
	if _, err := s.f.Write(b); err != nil {
		return err
	}
	s.entries = append(s.entries, spoolEntry{Length: uint32(len(b))})
	return nil
}

// Entries returns the accumulated entry records (in append order).
func (s *spool) Entries() []spoolEntry { return s.entries }

// Rewind seeks the spool to the beginning for sequential read-back.
// Callers must invoke this before Read.
func (s *spool) Rewind() error {
	_, err := s.f.Seek(0, io.SeekStart)
	return err
}

// Read implements io.Reader on the underlying file (post-Rewind).
func (s *spool) Read(p []byte) (int, error) { return s.f.Read(p) }

// ReadEntryAt reads the compressed bytes for entry idx (0-based, raster order)
// into a freshly allocated buffer and returns it. idx must be in [0, len(entries)).
// Uses ReadAt so it does not disturb the sequential read position used by Rewind+Read.
func (s *spool) ReadEntryAt(idx int) ([]byte, error) {
	// Compute byte offset by summing lengths of preceding entries.
	var off int64
	for i := 0; i < idx; i++ {
		off += int64(s.entries[i].Length)
	}
	buf := make([]byte, s.entries[idx].Length)
	_, err := s.f.ReadAt(buf, off)
	return buf, err
}

// Close closes the file handle without removing the file. Use Remove to
// also delete from disk.
func (s *spool) Close() error {
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

// Remove closes (if open) and unlinks the spool file.
func (s *spool) Remove() error {
	_ = s.Close()
	return os.Remove(s.path)
}
