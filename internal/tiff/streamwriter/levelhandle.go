package streamwriter

import (
	"fmt"

	"github.com/cornish/wsitools/internal/tiff"
)

// LevelSpec describes one pyramid level. Compressed tiles are streamed
// in via LevelHandle.WriteTile after AddLevel.
type LevelSpec struct {
	ImageWidth, ImageHeight uint32
	TileWidth, TileHeight   uint32
	BitsPerSample           []uint16
	SamplesPerPixel         uint16
	Photometric             uint16
	Compression             uint16
	JPEGTables              []byte
	NewSubfileType          uint32
	WSIImageType            string
	ExtraTags               []tiff.RawTag
}

// LevelHandle accepts tile bytes for one pyramid level.
type LevelHandle struct {
	w     *Writer
	entry *imageEntry
}

// AddLevel registers a tiled IFD with the writer. Tile bytes are written
// by subsequent LevelHandle.WriteTile calls; the IFD itself is emitted
// during Close.
func (w *Writer) AddLevel(s LevelSpec) (*LevelHandle, error) {
	if w.closed {
		return nil, fmt.Errorf("streamwriter: writer is closed")
	}
	if s.TileWidth == 0 || s.TileHeight == 0 {
		return nil, fmt.Errorf("streamwriter: tile dimensions must be non-zero")
	}
	if s.WSIImageType != "" {
		if err := tiff.ValidateWSIImageType(s.WSIImageType); err != nil {
			return nil, err
		}
	}
	if s.SamplesPerPixel == 0 {
		s.SamplesPerPixel = 3
	}
	tilesX := (s.ImageWidth + s.TileWidth - 1) / s.TileWidth
	tilesY := (s.ImageHeight + s.TileHeight - 1) / s.TileHeight
	entry := &imageEntry{
		levelSpec:   &s,
		tileOffsets: make([]uint64, tilesX*tilesY),
		tileCounts:  make([]uint64, tilesX*tilesY),
		tilesX:      tilesX,
		tilesY:      tilesY,
	}
	w.imgs = append(w.imgs, entry)
	return &LevelHandle{w: w, entry: entry}, nil
}

// WriteTile appends `compressed` tile bytes to the file and records the
// (offset, length) for IFD emission. Tiles may be written in any order.
func (h *LevelHandle) WriteTile(x, y uint32, compressed []byte) error {
	if x >= h.entry.tilesX || y >= h.entry.tilesY {
		return fmt.Errorf("streamwriter: tile (%d,%d) out of grid (%d,%d)",
			x, y, h.entry.tilesX, h.entry.tilesY)
	}
	off, err := h.w.appendBytes(compressed)
	if err != nil {
		return fmt.Errorf("streamwriter: write tile: %w", err)
	}
	idx := y*h.entry.tilesX + x
	h.entry.tileOffsets[idx] = off
	h.entry.tileCounts[idx] = uint64(len(compressed))
	return nil
}

// buildLevelEntries builds the IFD tag list for a tiled pyramid level.
func (w *Writer) buildLevelEntries(entry *imageEntry, isL0 bool) (*tiff.EntryBuilder, error) {
	s := entry.levelSpec
	b := tiff.NewEntryBuilder(w.bigtiff)

	b.AddLong(tiff.TagImageWidth, []uint32{s.ImageWidth})
	b.AddLong(tiff.TagImageLength, []uint32{s.ImageHeight})

	bps := s.BitsPerSample
	if len(bps) == 0 {
		bps = []uint16{8, 8, 8}
	}
	b.AddShort(tiff.TagBitsPerSample, bps)
	b.AddShort(tiff.TagCompression, []uint16{s.Compression})
	b.AddShort(tiff.TagPhotometricInterpretation, []uint16{s.Photometric})
	b.AddShort(tiff.TagSamplesPerPixel, []uint16{s.SamplesPerPixel})
	b.AddLong(tiff.TagTileWidth, []uint32{s.TileWidth})
	b.AddLong(tiff.TagTileLength, []uint32{s.TileHeight})
	if err := b.AddTileOffsets(tiff.TagTileOffsets, entry.tileOffsets); err != nil {
		return nil, err
	}
	if err := b.AddTileOffsets(tiff.TagTileByteCounts, entry.tileCounts); err != nil {
		return nil, err
	}
	b.AddShort(tiff.TagPlanarConfiguration, []uint16{1}) // chunky

	if s.NewSubfileType != 0 {
		b.AddLong(tiff.TagNewSubfileType, []uint32{s.NewSubfileType})
	}

	if s.WSIImageType != "" {
		b.AddASCII(tiff.TagWSIImageType, s.WSIImageType)
		if s.WSIImageType == tiff.WSIImageTypePyramid {
			b.AddLong(tiff.TagWSILevelIndex, []uint32{uint32(entry.pyramidLevelIndex)})
			b.AddLong(tiff.TagWSILevelCount, []uint32{uint32(w.pyramidLevelCount)})
		}
	}

	if len(s.JPEGTables) > 0 {
		// JPEGTables is opaque bytes; matches cogwsiwriter convention.
		b.AddBytes(tiff.TagJPEGTables, s.JPEGTables)
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
