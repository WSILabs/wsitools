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
	stripOffsets []uint64
	stripCounts  []uint64

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
	sampleFormat     uint16
	subResPyramid    bool

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
		sampleFormat:     opts.SampleFormat,
		subResPyramid:    opts.SubResolutionPyramid,
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

// emitIFD builds, encodes, and writes one IFD at the current offset and
// returns its start offset plus the file offset of its next-IFD pointer field
// (for chain patching). When subIFDs is non-empty, a SubIFDs (330) tag listing
// those child offsets is added before encoding (LONG8 on BigTIFF, LONG on
// classic).
func (w *Writer) emitIFD(entry *imageEntry, isL0 bool, subIFDs []uint64) (ifdStart uint64, nextPatchAt int64, err error) {
	b, err := w.buildEntryBuilder(entry, isL0)
	if err != nil {
		return 0, 0, err
	}
	if len(subIFDs) > 0 {
		if w.bigtiff {
			b.AddLong8(tiff.TagSubIFDs, subIFDs)
		} else {
			// Classic TIFF: Encode already rejects IFD offsets >4 GiB, so
			// every sub-resolution offset fits in a uint32.
			v := make([]uint32, len(subIFDs))
			for i, o := range subIFDs {
				v[i] = uint32(o)
			}
			b.AddLong(tiff.TagSubIFDs, v)
		}
	}
	ifdStart = uint64(w.off)
	ifd, ext, err := b.Encode(ifdStart)
	if err != nil {
		return 0, 0, err
	}
	if _, err = w.appendBytes(ifd); err != nil {
		return 0, 0, err
	}
	if len(ext) > 0 {
		if _, err = w.appendBytes(ext); err != nil {
			return 0, 0, err
		}
	}
	nextPtrSize := int64(4)
	if w.bigtiff {
		nextPtrSize = 8
	}
	nextPatchAt = int64(ifdStart) + int64(len(ifd)) - nextPtrSize
	return ifdStart, nextPatchAt, nil
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

	var firstIFD uint64
	var perr error
	if w.subResPyramid && pyramidCount > 0 {
		firstIFD, perr = w.closeSubIFDLayout()
	} else {
		firstIFD, perr = w.closeFlatLayout()
	}
	if perr != nil {
		return perr
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

// closeFlatLayout emits every image as a top-level IFD in w.imgs order and
// chains them linearly. Returns the first IFD offset. This is the layout for
// every format except OME (SubResolutionPyramid off).
func (w *Writer) closeFlatLayout() (uint64, error) {
	ifdStarts := make([]uint64, len(w.imgs))
	nextPatchAt := make([]int64, len(w.imgs))
	for i, entry := range w.imgs {
		start, patchAt, err := w.emitIFD(entry, i == 0, nil)
		if err != nil {
			return 0, fmt.Errorf("streamwriter: emit IFD %d: %w", i, err)
		}
		ifdStarts[i] = start
		nextPatchAt[i] = patchAt
	}
	for i := 0; i < len(w.imgs)-1; i++ {
		if err := w.patchOffset(nextPatchAt[i], ifdStarts[i+1]); err != nil {
			return 0, fmt.Errorf("streamwriter: patch next-IFD for %d: %w", i, err)
		}
	}
	if len(ifdStarts) == 0 {
		return 0, nil
	}
	return ifdStarts[0], nil
}

// closeSubIFDLayout emits sub-resolution pyramid levels as SubIFDs of L0
// (OME-TIFF convention): children first (capturing offsets), then L0 with a
// SubIFDs (330) tag, then associated images. The top-level next-IFD chain is
// L0 → associated…; sub-resolution IFDs are reachable only via 330 and keep
// nextIFD = 0. Returns L0's offset (the first IFD).
func (w *Writer) closeSubIFDLayout() (uint64, error) {
	var l0 *imageEntry
	var subRes, assoc []*imageEntry
	for _, e := range w.imgs {
		switch {
		case e.pyramidLevelIndex == 0:
			l0 = e
		case e.pyramidLevelIndex > 0:
			subRes = append(subRes, e) // w.imgs order = L1..Ln = largest→smallest
		default:
			assoc = append(assoc, e)
		}
	}
	if l0 == nil {
		return w.closeFlatLayout()
	}
	subOffsets := make([]uint64, 0, len(subRes))
	for _, e := range subRes {
		start, _, err := w.emitIFD(e, false, nil) // not chained; nextIFD stays 0
		if err != nil {
			return 0, fmt.Errorf("streamwriter: emit sub-resolution IFD: %w", err)
		}
		subOffsets = append(subOffsets, start)
	}
	l0Start, l0Patch, err := w.emitIFD(l0, true, subOffsets)
	if err != nil {
		return 0, fmt.Errorf("streamwriter: emit L0 IFD: %w", err)
	}
	topStarts := []uint64{l0Start}
	topPatch := []int64{l0Patch}
	for _, e := range assoc {
		start, patch, err := w.emitIFD(e, false, nil)
		if err != nil {
			return 0, fmt.Errorf("streamwriter: emit associated IFD: %w", err)
		}
		topStarts = append(topStarts, start)
		topPatch = append(topPatch, patch)
	}
	for i := 0; i < len(topStarts)-1; i++ {
		if err := w.patchOffset(topPatch[i], topStarts[i+1]); err != nil {
			return 0, fmt.Errorf("streamwriter: patch top-level next-IFD %d: %w", i, err)
		}
	}
	return l0Start, nil
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
	// NOTE: YCbCrSubSampling(530) is emitted PER-LEVEL in buildLevelEntries (a
	// genuine Aperio SVS tags every pyramid level, and libtiff/OpenSlide warn on
	// any JPEG-YCbCr level whose 530 tag doesn't match the tile's SOF), not here.
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
