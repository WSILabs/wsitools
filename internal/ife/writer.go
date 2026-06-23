package ife

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// Options configures a Writer at Create time.
type Options struct {
	Encoding      uint8   // encJPEG or encAVIF (pyramid-tile codec)
	XExtent       uint32  // native width in pixels (informational; reader derives dims from tiles)
	YExtent       uint32  // native height in pixels
	MPP           float64 // microns per pixel (0 = unknown)
	Magnification float64 // 0 = unknown
	CodecMajor    uint16
	CodecMinor    uint16
	CodecBuild    uint16
}

type tileRec struct {
	offset uint64
	size   uint32
}

type levelGrid struct {
	xTiles, yTiles uint32
	tiles          map[[2]int]tileRec // [col,row] -> rec; absent = sparse
}

// Writer emits one IFE v1.0 file. Levels are added native-first; the file stores
// them coarsest-first. Not safe for concurrent use; the engine drains tiles serially.
// Callers MUST call either Finalize or Abort to release the file and remove the temp file; dropping a Writer without calling either leaks both.
type Writer struct {
	f       *os.File
	path    string
	tmpPath string
	opts    Options
	pos     int64
	levels  []levelGrid
	icc     []byte
	assoc   []assocImage
	attrs   [][2]string
	closed  bool
}

type assocImage struct {
	title    string
	width    uint32
	height   uint32
	encoding uint8
	blob     []byte
}

const fileHeaderSize = 38

// Create opens path for writing (via path+".tmp", atomic-renamed on Finalize) and
// reserves the 38-byte FILE_HEADER. Tile blobs are appended after it.
func Create(path string, opts Options) (*Writer, error) {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, fmt.Errorf("ife: create %s: %w", tmp, err)
	}
	if _, err := f.Write(make([]byte, fileHeaderSize)); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, fmt.Errorf("ife: reserve header: %w", err)
	}
	return &Writer{f: f, path: path, tmpPath: tmp, opts: opts, pos: fileHeaderSize}, nil
}

// AddLevel registers a pyramid level's tile-grid dimensions. Call native-first.
func (w *Writer) AddLevel(xTiles, yTiles uint32) {
	w.levels = append(w.levels, levelGrid{xTiles: xTiles, yTiles: yTiles, tiles: map[[2]int]tileRec{}})
}

// WriteTile appends a compressed tile blob and records its offset/size. apiLevel
// is native-first (0 = native).
func (w *Writer) WriteTile(apiLevel, col, row int, blob []byte) error {
	if apiLevel < 0 || apiLevel >= len(w.levels) {
		return fmt.Errorf("ife: WriteTile level %d out of range", apiLevel)
	}
	if len(blob) > 0xFFFFFF {
		return fmt.Errorf("ife: tile %d,%d,%d is %d bytes (>16MB 24-bit cap)", apiLevel, col, row, len(blob))
	}
	if w.pos+int64(len(blob)) > int64(nullTile) {
		return fmt.Errorf("ife: file exceeds 40-bit offset cap")
	}
	if _, err := w.f.WriteAt(blob, w.pos); err != nil {
		return fmt.Errorf("ife: write tile: %w", err)
	}
	w.levels[apiLevel].tiles[[2]int{col, row}] = tileRec{offset: uint64(w.pos), size: uint32(len(blob))}
	w.pos += int64(len(blob))
	return nil
}

// SetICCProfile records the ICC blob to emit (nil/empty => no ICC_PROFILE block).
// NOTE: the ICC_PROFILE writer lands in a later slice; calling this before then panics at Finalize.
func (w *Writer) SetICCProfile(icc []byte) { w.icc = icc }

// Finalize writes the trailing blocks and backpatches the header, then atomically
// renames tmp -> path. After Finalize (or Abort) the Writer must not be reused.
func (w *Writer) Finalize() (err error) {
	if w.closed {
		return fmt.Errorf("ife: Finalize after close")
	}
	defer func() {
		w.closed = true
		cerr := w.f.Close()
		if err == nil && cerr != nil {
			err = cerr
		}
		if err != nil {
			os.Remove(w.tmpPath)
			return
		}
		if err = os.Rename(w.tmpPath, w.path); err != nil {
			os.Remove(w.tmpPath)
		}
	}()

	n := len(w.levels)
	fileOrder := make([]int, n) // fileOrder[fileIdx] = apiLevel (coarsest-first)
	for i := range fileOrder {
		fileOrder[i] = n - 1 - i
	}

	put := binary.LittleEndian

	// METADATA sub-blocks first (so METADATA can point at them).
	iccOff := nullOffset
	if len(w.icc) > 0 {
		iccOff = uint64(w.pos)
		if err = w.writeICC(); err != nil {
			return err
		}
	}
	imagesOff := nullOffset
	if len(w.assoc) > 0 {
		if imagesOff, err = w.writeImageArray(); err != nil {
			return err
		}
	}
	attrsOff := nullOffset
	if len(w.attrs) > 0 {
		if attrsOff, err = w.writeAttributes(); err != nil {
			return err
		}
	}
	metadataOff := uint64(w.pos)
	meta := make([]byte, 56)
	put.PutUint64(meta[0:8], metadataOff)
	put.PutUint16(meta[8:10], recoverMetadata)
	put.PutUint16(meta[10:12], w.opts.CodecMajor)
	put.PutUint16(meta[12:14], w.opts.CodecMinor)
	put.PutUint16(meta[14:16], w.opts.CodecBuild)
	put.PutUint64(meta[16:24], attrsOff)
	put.PutUint64(meta[24:32], imagesOff)
	put.PutUint64(meta[32:40], iccOff)
	put.PutUint64(meta[40:48], nullOffset) // annotations
	put.PutUint32(meta[48:52], math.Float32bits(float32(w.opts.MPP)))
	put.PutUint32(meta[52:56], math.Float32bits(float32(w.opts.Magnification)))
	if err = w.appendBlock(meta); err != nil {
		return err
	}

	// LAYER_EXTENTS (coarsest-first).
	layerExtOff := uint64(w.pos)
	le := make([]byte, blockHeaderValidation+12*n)
	put.PutUint64(le[0:8], layerExtOff)
	put.PutUint16(le[8:10], recoverLayerExtents)
	put.PutUint16(le[10:12], 12)
	put.PutUint32(le[12:16], uint32(n))
	for fi, api := range fileOrder {
		base := blockHeaderValidation + 12*fi
		g := w.levels[api]
		put.PutUint32(le[base:base+4], g.xTiles)
		put.PutUint32(le[base+4:base+8], g.yTiles)
		scale := float32(1.0) / float32(uint64(1)<<uint(api)) // native(api 0)=1.0
		put.PutUint32(le[base+8:base+12], math.Float32bits(scale))
	}
	if err = w.appendBlock(le); err != nil {
		return err
	}

	// TILE_OFFSETS (coarsest-first, row-major).
	var totalTiles int
	for _, g := range w.levels {
		totalTiles += int(g.xTiles) * int(g.yTiles)
	}
	tileOffOff := uint64(w.pos)
	to := make([]byte, blockHeaderValidation+8*totalTiles)
	put.PutUint64(to[0:8], tileOffOff)
	put.PutUint16(to[8:10], recoverTileOffsets)
	put.PutUint16(to[10:12], 8)
	put.PutUint32(to[12:16], uint32(totalTiles))
	idx := 0
	for _, api := range fileOrder {
		g := w.levels[api]
		for row := 0; row < int(g.yTiles); row++ {
			for col := 0; col < int(g.xTiles); col++ {
				base := blockHeaderValidation + 8*idx
				rec, ok := g.tiles[[2]int{col, row}]
				if ok {
					putUint40(to[base:base+5], rec.offset)
					putUint24(to[base+5:base+8], rec.size)
				} else {
					putUint40(to[base:base+5], nullTile)
					putUint24(to[base+5:base+8], 0)
				}
				idx++
			}
		}
	}
	if err = w.appendBlock(to); err != nil {
		return err
	}

	// TILE_TABLE.
	ttOff := uint64(w.pos)
	tt := make([]byte, 44)
	put.PutUint64(tt[0:8], ttOff)
	put.PutUint16(tt[8:10], recoverTileTable)
	tt[10] = w.opts.Encoding
	tt[11] = formatR8G8B8
	put.PutUint64(tt[12:20], nullOffset)
	put.PutUint64(tt[20:28], tileOffOff)
	put.PutUint64(tt[28:36], layerExtOff)
	put.PutUint32(tt[36:40], w.opts.XExtent)
	put.PutUint32(tt[40:44], w.opts.YExtent)
	if err = w.appendBlock(tt); err != nil {
		return err
	}

	// FILE_HEADER backpatch.
	fileSize := uint64(w.pos)
	hdr := make([]byte, fileHeaderSize)
	put.PutUint32(hdr[0:4], magicBytes)
	put.PutUint16(hdr[4:6], recoverHeader)
	put.PutUint64(hdr[6:14], fileSize)
	put.PutUint16(hdr[14:16], extMajor)
	put.PutUint16(hdr[16:18], extMinor)
	put.PutUint32(hdr[18:22], 0)
	put.PutUint64(hdr[22:30], ttOff)
	put.PutUint64(hdr[30:38], metadataOff)
	if _, err = w.f.WriteAt(hdr, 0); err != nil {
		return fmt.Errorf("ife: backpatch header: %w", err)
	}
	return nil
}

// appendBlock writes b at the current append position and advances pos.
func (w *Writer) appendBlock(b []byte) error {
	if _, err := w.f.WriteAt(b, w.pos); err != nil {
		return fmt.Errorf("ife: append block: %w", err)
	}
	w.pos += int64(len(b))
	return nil
}

// Abort closes and removes the temp file without producing output.
func (w *Writer) Abort() {
	if w.closed {
		return
	}
	w.closed = true
	w.f.Close()
	os.Remove(w.tmpPath)
}

// blockHeaderValidation is the 16-byte "validation + recovery + entry_size +
// entry_count" prefix on LAYER_EXTENTS / TILE_OFFSETS.
const blockHeaderValidation = 16

// The following are implemented in a later task (Slice 3); bare-pyramid never
// populates icc/assoc/attrs, so these stubs keep this task compiling. Replace in
// the metadata-sub-blocks task.
func (w *Writer) writeICC() error { panic("ife: writeICC implemented in slice 3") }
func (w *Writer) writeImageArray() (uint64, error) {
	panic("ife: writeImageArray implemented in slice 3")
}
func (w *Writer) writeAttributes() (uint64, error) {
	panic("ife: writeAttributes implemented in slice 3")
}
