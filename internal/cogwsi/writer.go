package cogwsi

import (
	"fmt"
	"os"
	"time"
)

// Options configures a new Writer.
type Options struct {
	BigTIFF      BigTIFFMode
	ToolsVersion string
	Metadata     Metadata
}

// Metadata is the cross-format scanner / acquisition info passed through to L0.
type Metadata struct {
	MPPX, MPPY          float64
	Magnification       float64
	Make, Model         string
	Software            string
	AcquisitionDateTime time.Time
	SourceFormat        string
	SourceImageDesc     string // optional provenance for ImageDescription
}

// LevelSpec describes one pyramid level. Compression and PhotometricInterpretation
// MUST equal the source's; tile geometry MUST equal the source's. JPEGTables
// MUST be supplied when the source IFD used abbreviated-JPEG mode.
type LevelSpec struct {
	ImageWidth, ImageHeight uint32
	TileWidth, TileHeight   uint32
	Compression             uint16
	Photometric             uint16
	BitsPerSample           []uint16
	SamplesPerPixel         uint16
	JPEGTables              []byte
	IsL0                    bool
}

// LevelHandle is the per-level tile sink.
type LevelHandle struct {
	w      *Writer
	idx    int
	spec   LevelSpec
	gridX  uint32
	gridY  uint32
	nextTX uint32
	nextTY uint32
	spool  *spool
}

// AssociatedSpec describes one associated image.
type AssociatedSpec struct {
	Kind            string // canonical WSIImageType value
	Width, Height   uint32
	Compression     uint16
	Photometric     uint16
	BitsPerSample   []uint16
	SamplesPerPixel uint16
	Bytes           []byte // verbatim compressed payload
	Tiled           bool   // (informational; associated IFDs always use strips in v0.1)
}

// Writer is the public handle for a COG-WSI file under construction.
type Writer struct {
	path     string
	spoolDir string
	out      *os.File
	opts     Options
	levels   []*LevelHandle
	assoc    []assocSpooled
	closed   bool
}

type assocSpooled struct {
	spec AssociatedSpec
	off  uint64 // offset within the shared associated spool (post-Close)
}

// Create starts a new COG-WSI writer at path. The output file is created
// empty; the head block is written by Close. A sibling spool directory
// path+".spool" is created for scratch storage.
func Create(path string, opts Options) (*Writer, error) {
	spoolDir := path + ".spool"
	if err := os.MkdirAll(spoolDir, 0o755); err != nil {
		return nil, fmt.Errorf("create spool dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		os.RemoveAll(spoolDir)
		return nil, fmt.Errorf("create output: %w", err)
	}
	return &Writer{
		path:     path,
		spoolDir: spoolDir,
		out:      f,
		opts:     opts,
	}, nil
}

// AddLevel registers a new pyramid level and returns its tile sink. Levels
// MUST be added in source order, full-resolution first.
func (w *Writer) AddLevel(spec LevelSpec) (*LevelHandle, error) {
	if w.closed {
		return nil, fmt.Errorf("writer closed")
	}
	idx := len(w.levels)
	sp, err := openSpool(fmt.Sprintf("%s/L%d", w.spoolDir, idx))
	if err != nil {
		return nil, fmt.Errorf("open spool L%d: %w", idx, err)
	}
	gridX := (spec.ImageWidth + spec.TileWidth - 1) / spec.TileWidth
	gridY := (spec.ImageHeight + spec.TileHeight - 1) / spec.TileHeight
	h := &LevelHandle{
		w: w, idx: idx, spec: spec,
		gridX: gridX, gridY: gridY,
		spool: sp,
	}
	w.levels = append(w.levels, h)
	return h, nil
}

// WriteTile appends one compressed tile to the level spool. Tiles MUST be
// written in row-major order (ty major, tx minor) starting from (0, 0).
func (h *LevelHandle) WriteTile(tx, ty uint32, compressed []byte) error {
	if tx != h.nextTX || ty != h.nextTY {
		return fmt.Errorf("level %d: tile out of order: got (%d,%d) want (%d,%d)",
			h.idx, tx, ty, h.nextTX, h.nextTY)
	}
	if err := h.spool.Append(compressed); err != nil {
		return err
	}
	h.nextTX++
	if h.nextTX >= h.gridX {
		h.nextTX = 0
		h.nextTY++
	}
	return nil
}

// AddAssociated stages one associated image (label/macro/thumbnail/overview).
// Bytes are kept in memory; the writer copies them to a single associated
// spool during Close. (Associated images are typically <10 MiB each.)
func (w *Writer) AddAssociated(spec AssociatedSpec) error {
	if w.closed {
		return fmt.Errorf("writer closed")
	}
	w.assoc = append(w.assoc, assocSpooled{spec: spec})
	return nil
}

// Abort removes the output file and spool directory. Safe to call any time;
// idempotent. Use as a deferred cleanup in callers that want to discard the
// in-progress write.
func (w *Writer) Abort() error {
	if w.out != nil {
		_ = w.out.Close()
		w.out = nil
	}
	for _, lv := range w.levels {
		if lv.spool != nil {
			_ = lv.spool.Remove()
			lv.spool = nil
		}
	}
	_ = os.RemoveAll(w.spoolDir)
	_ = os.Remove(w.path)
	w.closed = true
	return nil
}

// Close is implemented in Task 8.
func (w *Writer) Close() error {
	return fmt.Errorf("Writer.Close: not yet implemented")
}
