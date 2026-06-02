package streamwriter

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// imageEntry is the internal per-IFD state held by the Writer.
// Either tiled (levelSpec != nil) or stripped (strippedSpec != nil).
type imageEntry struct {
	// tiled
	levelSpec   *LevelSpec
	tileOffsets []uint64
	tileCounts  []uint64
	tilesX      uint32
	tilesY      uint32

	// stripped
	strippedSpec *StrippedSpec
	stripOffset  uint64
	stripCount   uint64

	// IFD chain bookkeeping (populated during Close).
	pyramidLevelIndex int
}

// Writer writes a streaming WSI TIFF file. Construct via Create.
type Writer struct {
	path    string
	tmpPath string
	f       *os.File
	bigtiff bool
	off     int64 // current write offset (end of file)

	imgs    []*imageEntry
	handles map[*imageEntry]*LevelHandle // populated in AddLevel; used for synchronous drain
	closed  bool

	// L0 metadata from Options.
	imageDescription string
	make_            string
	model            string
	software         string
	dateTime         time.Time
	hasDateTime      bool
	sourceFormat     string
	toolsVersion     string
	mppX             float64
	mppY             float64
	magnification    float64
	iccProfile       []byte
	imageDepth       uint32
	ycbcrSubSampling []uint16

	// computed during Close.
	pyramidLevelCount int

	// tile-order config populated from Options in Create.
	formatName             string
	acceptedOrders         map[string]bool // nil → permissive
	defaultOrder           tileorder.OrderStrategy
	defaultReorderCapacity uint32
}

// Create opens a new streaming TIFF for writing at path. The file is
// created at path+".tmp" and atomically renamed on successful Close.
func Create(path string, opts Options) (*Writer, error) {
	bigtiff := tiff.Resolve(opts.BigTIFF, 0)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, fmt.Errorf("streamwriter: create tmp: %w", err)
	}
	w := &Writer{
		path:             path,
		tmpPath:          tmp,
		f:                f,
		bigtiff:          bigtiff,
		imageDescription: opts.ImageDescription,
		make_:            opts.Make,
		model:            opts.Model,
		software:         opts.Software,
		dateTime:         opts.DateTime,
		hasDateTime:      !opts.DateTime.IsZero(),
		sourceFormat:     opts.SourceFormat,
		toolsVersion:     opts.ToolsVersion,
		mppX:             opts.MPPX,
		mppY:             opts.MPPY,
		magnification:    opts.Magnification,
		iccProfile:       opts.ICCProfile,
		imageDepth:       opts.ImageDepth,
		ycbcrSubSampling: opts.YCbCrSubSampling,
	}
	// Write a placeholder header (firstIFD = 0). It gets patched in Close.
	if err := tiff.WriteHeader(f, bigtiff, 0); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, fmt.Errorf("streamwriter: write header: %w", err)
	}
	w.off = int64(tiff.HeaderSize(bigtiff))
	// Seek to end so subsequent appends land after the header.
	if _, err := f.Seek(w.off, io.SeekStart); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, fmt.Errorf("streamwriter: seek after header: %w", err)
	}
	// Populate tile-order config from Options.
	w.formatName = opts.FormatName
	if w.formatName == "" {
		w.formatName = "tiff"
	}
	if len(opts.AcceptedOrders) > 0 {
		w.acceptedOrders = make(map[string]bool, len(opts.AcceptedOrders))
		for _, n := range opts.AcceptedOrders {
			w.acceptedOrders[n] = true
		}
	}
	w.defaultOrder = opts.DefaultOrder
	if w.defaultOrder == nil {
		w.defaultOrder = tileorder.RowMajor
	}
	w.defaultReorderCapacity = opts.DefaultReorderCapacity
	return w, nil
}

// appendBytes appends p to the output file and returns the start offset.
func (w *Writer) appendBytes(p []byte) (uint64, error) {
	off := uint64(w.off)
	n, err := w.f.Write(p)
	if err != nil {
		return 0, err
	}
	w.off += int64(n)
	return off, nil
}

// Close finalises the TIFF: emits all IFDs with correct chaining, patches
// the first-IFD offset in the header, fsyncs, closes, and renames the
// tmp file to the final path. Idempotent. On error the tmp file is removed.
func (w *Writer) Close() (err error) {
	if w.closed {
		return nil
	}
	w.closed = true

	defer func() {
		if err != nil && w.f != nil {
			_ = w.f.Close()
			os.Remove(w.tmpPath)
			w.f = nil
		}
	}()

	// Compute pyramid level count and per-entry pyramid index.
	pyramidCount := 0
	for _, e := range w.imgs {
		if e.levelSpec != nil && e.levelSpec.WSIImageType == tiff.WSIImageTypePyramid {
			pyramidCount++
		}
	}
	w.pyramidLevelCount = pyramidCount
	pIdx := 0
	for _, e := range w.imgs {
		if e.levelSpec != nil && e.levelSpec.WSIImageType == tiff.WSIImageTypePyramid {
			e.pyramidLevelIndex = pIdx
			pIdx++
		} else {
			e.pyramidLevelIndex = -1
		}
	}

	// Emit each IFD in order; record the offset of each IFD's next-IFD
	// pointer field so we can patch the chain.
	ifdStarts := make([]uint64, len(w.imgs))
	nextIFDPatchAt := make([]int64, len(w.imgs))

	for i, entry := range w.imgs {
		b, err2 := w.buildEntryBuilder(entry, i == 0)
		if err2 != nil {
			return fmt.Errorf("streamwriter: build IFD %d: %w", i, err2)
		}
		ifdStart := uint64(w.off)
		ifd, ext, err2 := b.Encode(ifdStart)
		if err2 != nil {
			return fmt.Errorf("streamwriter: encode IFD %d: %w", i, err2)
		}
		// Write the directory bytes then external bytes (Encode assigns
		// external offsets at ifdStart + len(ifd)).
		if _, err2 = w.appendBytes(ifd); err2 != nil {
			return fmt.Errorf("streamwriter: write IFD %d: %w", i, err2)
		}
		if len(ext) > 0 {
			if _, err2 = w.appendBytes(ext); err2 != nil {
				return fmt.Errorf("streamwriter: write IFD %d external: %w", i, err2)
			}
		}
		ifdStarts[i] = ifdStart
		// next-IFD pointer is at the end of the directory record bytes.
		// IFDRecordSize = entryCount + N*entrySize + nextIFD; next-IFD
		// field is the last 4 (classic) or 8 (BigTIFF) bytes of `ifd`.
		nextPtrSize := int64(4)
		if w.bigtiff {
			nextPtrSize = 8
		}
		nextIFDPatchAt[i] = int64(ifdStart) + int64(len(ifd)) - nextPtrSize
	}

	// Chain: patch each IFD's next-IFD field to point at the following IFD.
	for i := 0; i < len(w.imgs)-1; i++ {
		if err = w.patchOffset(nextIFDPatchAt[i], ifdStarts[i+1]); err != nil {
			return fmt.Errorf("streamwriter: patch next-IFD for %d: %w", i, err)
		}
	}

	// Patch the header's first-IFD pointer.
	var firstIFD uint64
	if len(ifdStarts) > 0 {
		firstIFD = ifdStarts[0]
	}
	firstAt := int64(4)
	if w.bigtiff {
		firstAt = 8
	}
	if err = w.patchOffset(firstAt, firstIFD); err != nil {
		return fmt.Errorf("streamwriter: patch first-IFD: %w", err)
	}

	if err = w.f.Sync(); err != nil {
		return fmt.Errorf("streamwriter: fsync: %w", err)
	}
	if err = w.f.Close(); err != nil {
		w.f = nil
		return fmt.Errorf("streamwriter: close tmp: %w", err)
	}
	w.f = nil
	if err = os.Rename(w.tmpPath, w.path); err != nil {
		return fmt.Errorf("streamwriter: rename: %w", err)
	}
	return nil
}

// Abort closes the file handle (if open) and removes the tmp file.
// Idempotent — calling Abort after a successful Close leaves the
// finished output alone.
func (w *Writer) Abort() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	_ = os.Remove(w.tmpPath)
	return nil
}

// patchOffset writes a wordSize-wide value at the given file offset.
func (w *Writer) patchOffset(at int64, v uint64) error {
	if w.bigtiff {
		return tiff.PatchUint64(w.f, at, v)
	}
	return tiff.PatchUint32(w.f, at, uint32(v))
}

// buildEntryBuilder returns a populated EntryBuilder for `entry`.
// isL0 indicates whether this is the first IFD in the chain (receives
// L0-only metadata tags from Options).
func (w *Writer) buildEntryBuilder(entry *imageEntry, isL0 bool) (*tiff.EntryBuilder, error) {
	if entry.levelSpec != nil {
		return w.buildLevelEntries(entry, isL0)
	}
	return w.buildStrippedEntries(entry, isL0)
}

// addL0Metadata appends ImageDescription, Make, Model, Software,
// DateTime, SourceFormat, ToolsVersion to b when set.
func (w *Writer) addL0Metadata(b *tiff.EntryBuilder) {
	if w.imageDescription != "" {
		b.AddASCII(tiff.TagImageDescription, w.imageDescription)
	}
	if w.make_ != "" {
		b.AddASCII(tiff.TagMake, w.make_)
	}
	if w.model != "" {
		b.AddASCII(tiff.TagModel, w.model)
	}
	if w.software != "" {
		b.AddASCII(tiff.TagSoftware, w.software)
	}
	if w.hasDateTime {
		b.AddASCII(tiff.TagDateTime, formatTIFFDateTime(w.dateTime))
	}
	if w.sourceFormat != "" {
		b.AddASCII(tiff.TagWSISourceFormat, w.sourceFormat)
	}
	if w.toolsVersion != "" {
		b.AddASCII(tiff.TagWSIToolsVersion, w.toolsVersion)
	}
	if w.mppX > 0 {
		n, d := tiff.MPPToResolution(w.mppX)
		b.AddRational(tiff.TagXResolution, []uint32{n}, []uint32{d})
		b.AddDouble(tiff.TagWSIMPPX, []float64{w.mppX})
	}
	if w.mppY > 0 {
		n, d := tiff.MPPToResolution(w.mppY)
		b.AddRational(tiff.TagYResolution, []uint32{n}, []uint32{d})
		b.AddDouble(tiff.TagWSIMPPY, []float64{w.mppY})
	}
	if w.mppX > 0 || w.mppY > 0 {
		b.AddShort(tiff.TagResolutionUnit, []uint16{tiff.ResolutionUnitCentimeter})
	}
	if w.magnification > 0 {
		b.AddDouble(tiff.TagWSIMagnification, []float64{w.magnification})
	}
	if len(w.iccProfile) > 0 {
		b.AddUndefined(tiff.TagICCProfile, w.iccProfile)
	}
	if w.imageDepth > 0 {
		b.AddLong(tiff.TagImageDepth, []uint32{w.imageDepth})
	}
	if len(w.ycbcrSubSampling) == 2 {
		b.AddShort(tiff.TagYCbCrSubSampling, w.ycbcrSubSampling)
	}
}

// formatTIFFDateTime formats t as TIFF 6.0's "YYYY:MM:DD HH:MM:SS".
func formatTIFFDateTime(t time.Time) string {
	return t.Format("2006:01:02 15:04:05")
}

// AcceptsOrder returns true iff this writer's format permits the given
// tile-order strategy. A writer with no acceptedOrders restriction
// (the "tiff" default) accepts all strategies. SVS-configured writers
// accept only "row-major".
func (w *Writer) AcceptsOrder(s tileorder.OrderStrategy) bool {
	if w.acceptedOrders == nil {
		return true
	}
	return w.acceptedOrders[s.Name()]
}

// AcceptedOrderNames returns the canonical names this writer accepts,
// or nil for permissive writers.
func (w *Writer) AcceptedOrderNames() []string {
	if w.acceptedOrders == nil {
		return nil
	}
	out := make([]string, 0, len(w.acceptedOrders))
	for n := range w.acceptedOrders {
		out = append(out, n)
	}
	return out
}
