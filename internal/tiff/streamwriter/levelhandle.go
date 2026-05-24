package streamwriter

import (
	"fmt"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
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

	// Order is the per-level tile emission strategy. If nil, the
	// writer's DefaultOrder (from Options) is used; if that's also
	// nil, RowMajor is used.
	Order tileorder.OrderStrategy
}

// LevelHandle accepts tile bytes for one pyramid level.
type LevelHandle struct {
	w     *Writer
	entry *imageEntry
	buf   *reorderBuffer
	order tileorder.OrderStrategy
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

	// Resolve effective tile-emission order.
	order := s.Order
	if order == nil {
		order = w.defaultOrder
	}
	if order == nil {
		order = tileorder.RowMajor
	}
	if !w.AcceptsOrder(order) {
		allowed := w.AcceptedOrderNames()
		if allowed == nil {
			allowed = []string{"<permissive>"}
		}
		return nil, fmt.Errorf("streamwriter: tile order %q not supported by format %q (allowed: %v)",
			order.Name(), w.formatName, allowed)
	}
	capacity := w.defaultReorderCapacity
	if capacity == 0 {
		// Default capacity must exceed pipeline.Run's in-flight depth
		// (in + out channels = 4*Workers + W workers in Process =
		// ~5W tiles) plus head-of-line slack. With a single Sink
		// goroutine that Submits arbitrary tiles before the head
		// arrives, undersizing this risks deadlock: the sink is
		// stuck in Submit on a non-head tile while the head tile
		// sits in the pipeline's `out` channel waiting for the sink
		// to pick it up. 1024 covers up to ~200 workers comfortably;
		// memory cost is bounded by (capacity * avg_tile_size) ≈
		// 60MB for typical JPEG WSI tiles.
		capacity = 1024
	}

	tilesX := (s.ImageWidth + s.TileWidth - 1) / s.TileWidth
	tilesY := (s.ImageHeight + s.TileHeight - 1) / s.TileHeight
	buf := newReorderBuffer(order, tilesX, tilesY, capacity)

	entry := &imageEntry{
		levelSpec:   &s,
		tileOffsets: make([]uint64, tilesX*tilesY),
		tileCounts:  make([]uint64, tilesX*tilesY),
		tilesX:      tilesX,
		tilesY:      tilesY,
	}
	w.imgs = append(w.imgs, entry)
	h := &LevelHandle{w: w, entry: entry, buf: buf, order: order}
	if w.handles == nil {
		w.handles = make(map[*imageEntry]*LevelHandle)
	}
	w.handles[entry] = h
	return h, nil
}

// WriteTile submits a compressed tile for the given grid position.
// The actual file append happens in writeTileAtIndex, called from the
// Sink's ordered-drain goroutine (or from the synchronous drain in
// buildLevelEntries for direct-use callers and tests).
// Multiple worker goroutines may call WriteTile concurrently.
func (h *LevelHandle) WriteTile(x, y uint32, compressed []byte) error {
	if x >= h.entry.tilesX || y >= h.entry.tilesY {
		return fmt.Errorf("streamwriter: tile (%d,%d) out of grid (%d,%d)",
			x, y, h.entry.tilesX, h.entry.tilesY)
	}
	return h.buf.Submit(x, y, compressed)
}

// writeTileAtIndex appends compressed to the file and records the
// resulting offset at strategy-emission index idx. Called only from
// the Sink-side ordered-drain loop or the synchronous drain fallback;
// never from worker goroutines.
func (h *LevelHandle) writeTileAtIndex(idx uint32, compressed []byte) error {
	off, err := h.w.appendBytes(compressed)
	if err != nil {
		return fmt.Errorf("streamwriter: write tile at index %d: %w", idx, err)
	}
	h.entry.tileOffsets[idx] = off
	h.entry.tileCounts[idx] = uint64(len(compressed))
	return nil
}

// CloseInput signals to the buffer that no more WriteTile calls will
// arrive for this level. The Sink-side drain continues until all
// pending tiles are emitted.
func (h *LevelHandle) CloseInput() {
	h.buf.CloseInput()
}

// Abort propagates a sticky error to the buffer. Pending WriteTile
// and Sink-side NextReady calls return this error.
func (h *LevelHandle) Abort(err error) {
	h.buf.Abort(err)
}

// NextReady returns the next-in-strategy-order tile from the buffer.
// Used by the Sink's ordered-drain loop. Returns ok=false when the
// level is drained.
func (h *LevelHandle) NextReady() (idx uint32, compressed []byte, ok bool, err error) {
	return h.buf.NextReady()
}

// WriteTileAtIndex is the public Sink-side write entry. After
// NextReady returns a tile, the Sink calls this to append the bytes
// to the file and record the offset.
func (h *LevelHandle) WriteTileAtIndex(idx uint32, compressed []byte) error {
	return h.writeTileAtIndex(idx, compressed)
}

// buildLevelEntries builds the IFD tag list for a tiled pyramid level.
func (w *Writer) buildLevelEntries(entry *imageEntry, isL0 bool) (*tiff.EntryBuilder, error) {
	// If any LevelHandle for this entry has a pending reorder buffer, drain it
	// synchronously now. This keeps direct-use callers (including all existing
	// tests) working without requiring a separate Sink drain goroutine.
	// Production pipelines that set up a Sink drain loop call CloseInput before
	// reaching this point; the drain here is idempotent in that case.
	if h, ok := w.handles[entry]; ok && h.buf != nil {
		h.CloseInput()
		for {
			idx, b, more, err := h.NextReady()
			if err != nil {
				return nil, err
			}
			if !more {
				break
			}
			if err := h.writeTileAtIndex(idx, b); err != nil {
				return nil, err
			}
		}
	}

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
		// JPEGTables is opaque bytes; TIFF spec requires type UNDEFINED (7).
		b.AddUndefined(tiff.TagJPEGTables, s.JPEGTables)
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
