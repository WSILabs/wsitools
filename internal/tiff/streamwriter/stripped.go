package streamwriter

import (
	"fmt"

	"github.com/cornish/wsitools/internal/tiff"
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
	StripBytes      []byte
	NewSubfileType  uint32
	WSIImageType    string
	ExtraTags       []tiff.RawTag
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
	off, err := w.appendBytes(s.StripBytes)
	if err != nil {
		return fmt.Errorf("streamwriter: write strip data: %w", err)
	}
	entry := &imageEntry{
		strippedSpec: &s,
		stripOffset:  off,
		stripCount:   uint64(len(s.StripBytes)),
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
	b.AddLong(tiff.TagRowsPerStrip, []uint32{s.RowsPerStrip})
	if err := b.AddTileOffsets(tiff.TagStripOffsets, []uint64{entry.stripOffset}); err != nil {
		return nil, err
	}
	if err := b.AddTileOffsets(tiff.TagStripByteCounts, []uint64{entry.stripCount}); err != nil {
		return nil, err
	}
	b.AddShort(tiff.TagPlanarConfiguration, []uint16{1})

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
