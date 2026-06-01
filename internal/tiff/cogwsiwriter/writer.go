package cogwsiwriter

import (
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// Options configures a new Writer.
type Options struct {
	BigTIFF      BigTIFFMode
	ToolsVersion string
	Metadata     Metadata
	// DefaultOrder is the per-level tile emission order applied during
	// finalize. nil → tileorder.RowMajor (standard COG layout).
	// Accepted strategies: row-major, hilbert, morton.
	DefaultOrder tileorder.OrderStrategy
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
	ICCProfile          []byte // embedded color profile; emitted on L0 as tag 34675
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
	order    tileorder.OrderStrategy
}

type assocSpooled struct {
	spec AssociatedSpec
	off  uint64 // offset within the shared associated spool (post-Close)
}

// Create starts a new COG-WSI writer at path. The output file is created
// empty; the head block is written by Close. A sibling spool directory
// path+".spool" is created for scratch storage.
func Create(path string, opts Options) (*Writer, error) {
	ord := opts.DefaultOrder
	if ord == nil {
		ord = tileorder.RowMajor
	}
	switch ord.Name() {
	case "row-major", "hilbert", "morton":
		// allowed
	default:
		return nil, fmt.Errorf("cogwsiwriter: tile order %q not supported (allowed: row-major, hilbert, morton)", ord.Name())
	}

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
		order:    ord,
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
	if err := validateAssocKind(spec.Kind); err != nil {
		return err
	}
	w.assoc = append(w.assoc, assocSpooled{spec: spec})
	return nil
}

// Abort removes the output file and spool directory. Safe to call any time;
// idempotent. Use as a deferred cleanup in callers that want to discard the
// in-progress write.
func (w *Writer) Abort() error {
	if w.closed {
		return nil // successful Close (or prior Abort) — leave the finished output alone
	}
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

// Close finalizes the file: serializes the ghost area, all IFDs, and
// external tag arrays at the file head with patched-up tile offsets;
// streams spool files into the output in reverse level order (smallest
// level first); appends associated-image data; removes spool files.
//
// On error, removes spool files and the partial output.
func (w *Writer) Close() error {
	if w.closed {
		return fmt.Errorf("writer already closed")
	}
	defer func() { w.closed = true }()

	// Build layoutInput.
	in := layoutInput{BigTIFFMode: w.opts.BigTIFF}
	for i, lv := range w.levels {
		entries := lv.spool.Entries()
		bytesLen := make([]uint32, len(entries))
		for j, e := range entries {
			bytesLen[j] = e.Length
		}
		l0MetaExternal := uint64(0)
		if i == 0 {
			l0MetaExternal = l0MetadataExternalBytes(w.opts)
		}
		in.Levels = append(in.Levels, levelLayoutInput{
			TileBytes:      bytesLen,
			TileCount:      uint32(len(entries)),
			TileGeometry:   tileGeom{TileW: lv.spec.TileWidth, TileH: lv.spec.TileHeight, ImgW: lv.spec.ImageWidth, ImgH: lv.spec.ImageHeight},
			Compression:    lv.spec.Compression,
			JPEGTables:     lv.spec.JPEGTables,
			IsL0:           i == 0,
			L0MetaExternal: l0MetaExternal,
		})
	}
	for _, a := range w.assoc {
		in.Associated = append(in.Associated, associatedLayoutInput{
			Bytes:       uint32(len(a.spec.Bytes)),
			Width:       a.spec.Width,
			Height:      a.spec.Height,
			Compression: a.spec.Compression,
			Kind:        a.spec.Kind,
		})
	}

	plan, err := planLayout(in)
	if err != nil {
		w.abortInternal()
		return err
	}

	totalLevels := len(w.levels)

	// Remap plan TileOffsets from raster-emission order to strategy order.
	// planLayout assigns TileOffsets[emitIdx] in sequential (raster) order.
	// When the strategy reorders emission, tile (x,y) is emitted at
	// emitIdx = order.Index(x,y,...), so its file offset is
	// plan.Levels[i].TileOffsets[emitIdx]. The IFD must record this at
	// raster slot y*tilesX+x. Build per-level remapped offset slices.
	remappedOffsets := make([][]uint64, len(w.levels))
	for i, lv := range w.levels {
		tilesX := lv.gridX
		tilesY := lv.gridY
		total := tilesX * tilesY
		remapped := make([]uint64, total)
		for emitIdx := uint32(0); emitIdx < total; emitIdx++ {
			x, y := w.order.IndexToXY(emitIdx, tilesX, tilesY)
			remapped[y*tilesX+x] = plan.Levels[i].TileOffsets[emitIdx]
		}
		remappedOffsets[i] = remapped
	}

	// Build IFD bytes for each level and associated image.
	type ifdBlob struct {
		offset uint64
		ifd    []byte
		ext    []byte
	}
	var blobs []ifdBlob

	for i, lv := range w.levels {
		b := tiff.NewEntryBuilder(plan.BigTIFF)
		if err := populateLevelIFD(b, lv.spec, remappedOffsets[i], lv.spool.Entries(), w.opts, i, totalLevels); err != nil {
			w.abortInternal()
			return fmt.Errorf("populate IFD L%d: %w", i, err)
		}
		ifd, ext, err := b.Encode(plan.Levels[i].IFDOffset)
		if err != nil {
			w.abortInternal()
			return fmt.Errorf("encode IFD L%d: %w", i, err)
		}
		if got := uint64(len(ifd) + len(ext)); got > plan.Levels[i].Reserved {
			w.abortInternal()
			return fmt.Errorf("cogwsi: level %d IFD+external %d bytes exceeds reserved %d (layout sizing bug)", i, got, plan.Levels[i].Reserved)
		}
		blobs = append(blobs, ifdBlob{offset: plan.Levels[i].IFDOffset, ifd: ifd, ext: ext})
	}
	for i, a := range w.assoc {
		b := tiff.NewEntryBuilder(plan.BigTIFF)
		if err := populateAssocIFD(b, plan.BigTIFF, a.spec, plan.Associated[i].DataOffset); err != nil {
			w.abortInternal()
			return fmt.Errorf("populate assoc IFD %d: %w", i, err)
		}
		ifd, ext, err := b.Encode(plan.Associated[i].IFDOffset)
		if err != nil {
			w.abortInternal()
			return fmt.Errorf("encode IFD assoc%d: %w", i, err)
		}
		if got := uint64(len(ifd) + len(ext)); got > plan.Associated[i].Reserved {
			w.abortInternal()
			return fmt.Errorf("cogwsi: assoc %d IFD+external %d bytes exceeds reserved %d (layout sizing bug)", i, got, plan.Associated[i].Reserved)
		}
		blobs = append(blobs, ifdBlob{offset: plan.Associated[i].IFDOffset, ifd: ifd, ext: ext})
	}

	// Patch next_ifd_offset chain.
	for i := 0; i < len(blobs)-1; i++ {
		patchNextIFD(blobs[i].ifd, blobs[i+1].offset, plan.BigTIFF)
	}

	// Write head block.
	if err := writeHeader(w.out, plan); err != nil {
		w.abortInternal()
		return err
	}
	ghostBytes, _ := defaultGhost().Marshal()
	if _, err := w.out.WriteAt(ghostBytes, int64(plan.GhostOffset)); err != nil {
		w.abortInternal()
		return fmt.Errorf("write ghost: %w", err)
	}
	for _, b := range blobs {
		if _, err := w.out.WriteAt(b.ifd, int64(b.offset)); err != nil {
			w.abortInternal()
			return fmt.Errorf("write IFD: %w", err)
		}
		if len(b.ext) > 0 {
			if _, err := w.out.WriteAt(b.ext, int64(b.offset)+int64(len(b.ifd))); err != nil {
				w.abortInternal()
				return fmt.Errorf("write IFD external: %w", err)
			}
		}
	}

	// Stream level spools in reverse order (smallest first), in strategy order.
	for i := len(w.levels) - 1; i >= 0; i-- {
		lv := w.levels[i]
		tilesX := lv.gridX
		tilesY := lv.gridY
		total := tilesX * tilesY
		for emitIdx := uint32(0); emitIdx < total; emitIdx++ {
			x, y := w.order.IndexToXY(emitIdx, tilesX, tilesY)
			rasterIdx := y*tilesX + x
			// TileOffsets[emitIdx] is the pre-planned file offset for the
			// emitIdx-th tile in raster emission order. Since we emit tile
			// (x,y) at emission position emitIdx, it occupies that slot.
			// The IFD records this offset at TileOffsets[rasterIdx] (set above).
			off := int64(plan.Levels[i].TileOffsets[emitIdx])
			buf, err := lv.spool.ReadEntryAt(int(rasterIdx))
			if err != nil {
				w.abortInternal()
				return fmt.Errorf("read L%d tile (%d,%d): %w", i, x, y, err)
			}
			if _, err := w.out.WriteAt(buf, off); err != nil {
				w.abortInternal()
				return fmt.Errorf("write L%d tile (%d,%d): %w", i, x, y, err)
			}
		}
	}

	// Write associated images.
	for i, a := range w.assoc {
		if _, err := w.out.WriteAt(a.spec.Bytes, int64(plan.Associated[i].DataOffset)); err != nil {
			w.abortInternal()
			return fmt.Errorf("write assoc %d: %w", i, err)
		}
	}

	// Sync, close, cleanup.
	if err := w.out.Sync(); err != nil {
		w.abortInternal()
		return fmt.Errorf("fsync: %w", err)
	}
	if err := w.out.Close(); err != nil {
		w.abortInternal()
		return fmt.Errorf("close output: %w", err)
	}
	w.out = nil
	for _, lv := range w.levels {
		_ = lv.spool.Remove()
		lv.spool = nil
	}
	_ = os.Remove(w.spoolDir)
	return nil
}

// abortInternal is like Abort but does not set closed (the deferred set
// in Close handles that). Removes output + spool, best-effort.
func (w *Writer) abortInternal() {
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
}

func writeHeader(f *os.File, plan layoutPlan) error {
	hdr := make([]byte, plan.HeaderSize)
	hdr[0], hdr[1] = 'I', 'I'
	if plan.BigTIFF {
		binary.LittleEndian.PutUint16(hdr[2:4], 0x002B)
		binary.LittleEndian.PutUint16(hdr[4:6], 8) // offset size
		binary.LittleEndian.PutUint16(hdr[6:8], 0) // constant zero
		binary.LittleEndian.PutUint64(hdr[8:16], plan.FirstIFDOffset)
	} else {
		binary.LittleEndian.PutUint16(hdr[2:4], 0x002A)
		binary.LittleEndian.PutUint32(hdr[4:8], uint32(plan.FirstIFDOffset))
	}
	_, err := f.WriteAt(hdr, 0)
	return err
}

func patchNextIFD(ifd []byte, next uint64, big bool) {
	if big {
		binary.LittleEndian.PutUint64(ifd[len(ifd)-8:], next)
	} else {
		binary.LittleEndian.PutUint32(ifd[len(ifd)-4:], uint32(next))
	}
}

// populateLevelIFD fills an ifdBuilder with the tags for a pyramid level.
// levelIdx is the 0-based index of this level; totalLevels is the total
// pyramid depth. These are used for WSILevelIndex and WSILevelCount tags.
func populateLevelIFD(b *tiff.EntryBuilder, spec LevelSpec, tileOffsets []uint64, entries []spoolEntry, opts Options, levelIdx, totalLevels int) error {
	subfile := uint32(1) // reduced-resolution
	if levelIdx == 0 {
		subfile = 0
	}
	b.AddLong(254 /*NewSubfileType*/, []uint32{subfile})
	b.AddLong(256 /*ImageWidth*/, []uint32{spec.ImageWidth})
	b.AddLong(257 /*ImageLength*/, []uint32{spec.ImageHeight})
	b.AddShort(258 /*BitsPerSample*/, spec.BitsPerSample)
	b.AddShort(259 /*Compression*/, []uint16{spec.Compression})
	b.AddShort(262 /*PhotometricInterpretation*/, []uint16{spec.Photometric})
	b.AddShort(277 /*SamplesPerPixel*/, []uint16{spec.SamplesPerPixel})
	b.AddShort(284 /*PlanarConfiguration*/, []uint16{1})
	b.AddLong(322 /*TileWidth*/, []uint32{spec.TileWidth})
	b.AddLong(323 /*TileLength*/, []uint32{spec.TileHeight})
	if err := b.AddTileOffsets(324 /*TileOffsets*/, tileOffsets); err != nil {
		return err
	}
	byteCounts := make([]uint32, len(entries))
	for i, e := range entries {
		byteCounts[i] = e.Length
	}
	b.AddLong(325 /*TileByteCounts*/, byteCounts)
	if spec.JPEGTables != nil {
		b.AddBytes(347 /*JPEGTables*/, spec.JPEGTables)
	}
	b.AddASCII(tiff.TagWSIImageType, tiff.WSIImageTypePyramid)
	b.AddLong(tiff.TagWSILevelIndex, []uint32{uint32(levelIdx)})
	b.AddLong(tiff.TagWSILevelCount, []uint32{uint32(totalLevels)})
	if levelIdx == 0 {
		if opts.Metadata.SourceImageDesc != "" {
			b.AddASCII(270 /*ImageDescription*/, opts.Metadata.SourceImageDesc)
		}
		if opts.Metadata.Make != "" {
			b.AddASCII(271 /*Make*/, opts.Metadata.Make)
		}
		if opts.Metadata.Model != "" {
			b.AddASCII(272 /*Model*/, opts.Metadata.Model)
		}
		if opts.Metadata.Software != "" {
			b.AddASCII(305 /*Software*/, opts.Metadata.Software)
		}
		if !opts.Metadata.AcquisitionDateTime.IsZero() {
			b.AddASCII(306 /*DateTime*/, opts.Metadata.AcquisitionDateTime.Format("2006:01:02 15:04:05"))
		}
		if opts.Metadata.SourceFormat != "" {
			b.AddASCII(tiff.TagWSISourceFormat, opts.Metadata.SourceFormat)
		}
		if opts.ToolsVersion != "" {
			b.AddASCII(tiff.TagWSIToolsVersion, opts.ToolsVersion)
		}
		if opts.Metadata.MPPX > 0 {
			b.AddDouble(tiff.TagWSIMPPX, []float64{opts.Metadata.MPPX})
			n, d := tiff.MPPToResolution(opts.Metadata.MPPX)
			b.AddRational(tiff.TagXResolution, []uint32{n}, []uint32{d})
		}
		if opts.Metadata.MPPY > 0 {
			b.AddDouble(tiff.TagWSIMPPY, []float64{opts.Metadata.MPPY})
			n, d := tiff.MPPToResolution(opts.Metadata.MPPY)
			b.AddRational(tiff.TagYResolution, []uint32{n}, []uint32{d})
		}
		if opts.Metadata.MPPX > 0 || opts.Metadata.MPPY > 0 {
			b.AddShort(tiff.TagResolutionUnit, []uint16{tiff.ResolutionUnitCentimeter})
		}
		if opts.Metadata.Magnification > 0 {
			b.AddDouble(tiff.TagWSIMagnification, []float64{opts.Metadata.Magnification})
		}
		if len(opts.Metadata.ICCProfile) > 0 {
			b.AddUndefined(tiff.TagICCProfile, opts.Metadata.ICCProfile)
		}
	}
	return nil
}

// l0MetadataExternalBytes is a safe upper bound on the external bytes the
// L0 metadata tags consume. It sums each value's full byte length as if
// external (inline values only make this an over-estimate, never under),
// so the layout never under-reserves. Mirror populateLevelIFD's L0 block:
// when you add/remove an L0 metadata tag, update this too. The Close-time
// bounds-check is the backstop if they ever drift.
func l0MetadataExternalBytes(opts Options) uint64 {
	asciiLen := func(s string) uint64 {
		if s == "" {
			return 0
		}
		return uint64(len(s)) + 1 // NUL terminator
	}
	var n uint64
	n += asciiLen(opts.Metadata.SourceImageDesc)
	n += asciiLen(opts.Metadata.Make)
	n += asciiLen(opts.Metadata.Model)
	n += asciiLen(opts.Metadata.Software)
	if !opts.Metadata.AcquisitionDateTime.IsZero() {
		n += 20 // "YYYY:MM:DD HH:MM:SS\0"
	}
	n += asciiLen(opts.Metadata.SourceFormat)
	n += asciiLen(opts.ToolsVersion)
	n += 3 * 8 // WSIMPPX, WSIMPPY, WSIMagnification (DOUBLE, 8 bytes each)
	n += 2 * 8 // XResolution, YResolution (RATIONAL, 8 bytes each)
	n += uint64(len(opts.Metadata.ICCProfile))
	return n
}

// populateAssocIFD fills a tiff.EntryBuilder for an associated image. Associated
// images use strip-based encoding (1 strip covering the full image).
// In classic-TIFF mode it returns an error if the data offset or byte count
// would overflow a uint32; in BigTIFF mode it uses LONG8 for both fields.
func populateAssocIFD(b *tiff.EntryBuilder, bigtiff bool, spec AssociatedSpec, dataOffset uint64) error {
	if !bigtiff {
		if dataOffset > 0xFFFFFFFF {
			return fmt.Errorf("cogwsi: associated image data offset %d overflows classic TIFF", dataOffset)
		}
		if uint64(len(spec.Bytes)) > 0xFFFFFFFF {
			return fmt.Errorf("cogwsi: associated image byte count %d overflows classic TIFF", len(spec.Bytes))
		}
	}
	b.AddLong(254 /*NewSubfileType*/, []uint32{1})
	b.AddLong(256, []uint32{spec.Width})
	b.AddLong(257, []uint32{spec.Height})
	if len(spec.BitsPerSample) == 0 {
		spec.BitsPerSample = []uint16{8, 8, 8}
	}
	b.AddShort(258, spec.BitsPerSample)
	b.AddShort(259, []uint16{spec.Compression})
	b.AddShort(262, []uint16{spec.Photometric})
	if spec.SamplesPerPixel == 0 {
		spec.SamplesPerPixel = 3
	}
	b.AddShort(277, []uint16{spec.SamplesPerPixel})
	b.AddShort(284, []uint16{1})
	if bigtiff {
		b.AddLong8(273 /*StripOffsets*/, []uint64{dataOffset})
		b.AddLong8(279 /*StripByteCounts*/, []uint64{uint64(len(spec.Bytes))})
	} else {
		b.AddLong(273 /*StripOffsets*/, []uint32{uint32(dataOffset)})
		b.AddLong(279 /*StripByteCounts*/, []uint32{uint32(len(spec.Bytes))})
	}
	b.AddLong(278 /*RowsPerStrip*/, []uint32{spec.Height})
	b.AddASCII(tiff.TagWSIImageType, spec.Kind)
	return nil
}
