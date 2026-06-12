package streamwriter

import (
	"fmt"

	"github.com/wsilabs/wsitools/internal/tiff"
)

// StrippedSpec describes a single-strip image (label/macro/thumbnail/overview
// or any non-tiled associated image). The caller provides already-encoded
// strip bytes.
type StrippedSpec struct {
	Width, Height   uint32
	RowsPerStrip    uint32
	BitsPerSample   []uint16
	SamplesPerPixel uint16
	Photometric     uint16
	Compression     uint16

	// Strips holds the already-encoded strip bytes in document order; each
	// is written verbatim. Predictor (tag 317, only emitted when >1) and
	// JPEGTables (tag 347, only emitted when non-nil) carry the source IFD's
	// pixel-reconstruction state so the copy decodes faithfully. This is the
	// multi-strip form used to re-emit associated images (WSILabs/wsitools#1).
	Strips     [][]byte
	Predictor  uint16
	JPEGTables []byte

	// StripBytes is the legacy single-strip form. When Strips is empty it is
	// promoted to a one-element Strips. Retained for back-compat with existing
	// callers until they migrate.
	StripBytes []byte

	NewSubfileType uint32
	WSIImageType   string
	ExtraTags      []tiff.RawTag
}

// AddStripped appends a single-strip IFD to the writer. Strip bytes are
// written immediately; the IFD is deferred until Close.
func (w *Writer) AddStripped(s StrippedSpec) error {
	if w.closed {
		return fmt.Errorf("streamwriter: writer is closed")
	}
	if s.WSIImageType != "" {
		if err := tiff.ValidateWSIImageType(s.WSIImageType); err != nil {
			return err
		}
	}
	if s.SamplesPerPixel == 0 {
		s.SamplesPerPixel = 3
	}
	if s.RowsPerStrip == 0 {
		s.RowsPerStrip = s.Height
	}
	// Promote the legacy single-strip form to a one-element Strips slice so
	// callers that still set StripBytes keep working.
	if len(s.Strips) == 0 && len(s.StripBytes) > 0 {
		s.Strips = [][]byte{s.StripBytes}
	}
	offs := make([]uint64, len(s.Strips))
	cnts := make([]uint64, len(s.Strips))
	for i, strip := range s.Strips {
		off, err := w.appendBytes(strip)
		if err != nil {
			return fmt.Errorf("streamwriter: write strip %d: %w", i, err)
		}
		offs[i] = off
		cnts[i] = uint64(len(strip))
	}
	entry := &imageEntry{
		strippedSpec: &s,
		stripOffsets: offs,
		stripCounts:  cnts,
	}
	w.imgs = append(w.imgs, entry)
	return nil
}

// buildStrippedEntries builds the IFD tag list for a stripped image.
func (w *Writer) buildStrippedEntries(entry *imageEntry, isL0 bool) (*tiff.EntryBuilder, error) {
	s := entry.strippedSpec
	b := tiff.NewEntryBuilder(w.bigtiff)

	bps := s.BitsPerSample
	if len(bps) == 0 {
		bps = []uint16{8, 8, 8}
	}

	b.AddLong(tiff.TagImageWidth, []uint32{s.Width})
	b.AddLong(tiff.TagImageLength, []uint32{s.Height})
	b.AddShort(tiff.TagBitsPerSample, bps)
	b.AddShort(tiff.TagCompression, []uint16{s.Compression})
	b.AddShort(tiff.TagPhotometricInterpretation, []uint16{s.Photometric})
	b.AddShort(tiff.TagSamplesPerPixel, []uint16{s.SamplesPerPixel})
	if w.sampleFormat != 0 {
		b.AddShort(tiff.TagSampleFormat, []uint16{w.sampleFormat})
	}
	b.AddLong(tiff.TagRowsPerStrip, []uint32{s.RowsPerStrip})
	if err := b.AddTileOffsets(tiff.TagStripOffsets, entry.stripOffsets); err != nil {
		return nil, err
	}
	if err := b.AddTileOffsets(tiff.TagStripByteCounts, entry.stripCounts); err != nil {
		return nil, err
	}
	b.AddShort(tiff.TagPlanarConfiguration, []uint16{1})
	// Predictor (317) and JPEGTables (347) carry the source IFD's
	// pixel-reconstruction state for faithful multi-strip copies. Encode sorts
	// entries by tag, so emission order here is cosmetic.
	if s.Predictor > 1 {
		b.AddShort(tiff.TagPredictor, []uint16{s.Predictor})
	}
	if len(s.JPEGTables) > 0 {
		b.AddUndefined(tiff.TagJPEGTables, s.JPEGTables)
	}

	if s.NewSubfileType != 0 {
		b.AddLong(tiff.TagNewSubfileType, []uint32{s.NewSubfileType})
	}
	if s.WSIImageType != "" {
		b.AddASCII(tiff.TagWSIImageType, s.WSIImageType)
	}

	if isL0 {
		w.addL0Metadata(b)
	}

	for _, et := range s.ExtraTags {
		if err := b.AddRaw(et); err != nil {
			return nil, fmt.Errorf("extra tag %d: %w", et.Tag, err)
		}
	}

	return b, nil
}
