package cogwsiwriter

import (
	"fmt"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// BigTIFFMode controls classic vs BigTIFF selection.
type BigTIFFMode int

const (
	BigTIFFAuto BigTIFFMode = iota
	BigTIFFOn
	BigTIFFOff
)

// tileGeom is the per-level pixel/tile geometry.
type tileGeom struct {
	TileW, TileH, ImgW, ImgH uint32
}

// levelLayoutInput is what the writer hands the planner after all tiles for a
// level have been spooled.
type levelLayoutInput struct {
	TileBytes    []uint32 // per-tile compressed length, in source (row-major) order
	TileCount    uint32   // == len(TileBytes); kept for clarity
	GridX        uint32   // tiles per row; GridY = TileCount/GridX (for emission-order placement)
	TileGeometry tileGeom
	Compression  uint16
	JPEGTables   []byte // optional, abbreviated-JPEG mode
	IsL0         bool   // true for pyramid index 0 — gets the L0 metadata tags
	// L0MetaExternal is the exact upper-bound external byte size of the L0
	// metadata tags (ImageDescription, scanner strings, MPP/mag doubles,
	// resolution, ICC). Replaces the old fixed 2 KiB guess. 0 for non-L0.
	L0MetaExternal uint64
}

// associatedLayoutInput is one associated image (label/macro/thumbnail/overview).
type associatedLayoutInput struct {
	StripBytes    []uint32 // per-strip compressed lengths, in document order
	JPEGTables    uint32   // len of JPEGTables (0 if none)
	Predictor     uint16   // tag 317 (emitted only when > 1)
	Width, Height uint32
	Compression   uint16
	Type          string // canonical WSIImageType value
}

// totalBytes returns the sum of all strip lengths (the associated data area).
func (a associatedLayoutInput) totalBytes() uint64 {
	var n uint64
	for _, b := range a.StripBytes {
		n += uint64(b)
	}
	return n
}

type layoutInput struct {
	Levels      []levelLayoutInput
	Associated  []associatedLayoutInput
	BigTIFFMode BigTIFFMode
	// MetaBytes is an upper-bound estimate of ImageDescription + extra metadata
	// bytes that live in the head block. The writer fills this when it knows
	// what metadata it will emit.
	MetaBytes uint32
}

// levelLayoutPlan is the planner's per-level output.
type levelLayoutPlan struct {
	IFDOffset      uint64   // absolute file offset of this IFD record
	Reserved       uint64   // bytes reserved for this IFD record + its external data
	TileOffsets    []uint64 // absolute file offsets per tile, in source row-major order
	TileDataOffset uint64   // offset of the first tile (== TileOffsets[0])
}

// associatedLayoutPlan is the planner's per-associated-image output.
type associatedLayoutPlan struct {
	IFDOffset  uint64
	Reserved   uint64 // bytes reserved for this IFD record + its external data
	DataOffset uint64
}

// layoutPlan is the complete head-block + tile-data layout for the file.
type layoutPlan struct {
	BigTIFF        bool
	HeaderSize     uint64 // 8 (classic) or 16 (BigTIFF)
	GhostOffset    uint64 // == HeaderSize
	GhostSize      uint64
	FirstIFDOffset uint64 // immediately after ghost
	Levels         []levelLayoutPlan
	Associated     []associatedLayoutPlan
	HeadBlockEnd   uint64 // first byte of pyramid tile data area
	FileSize       uint64 // total file size including all tile + associated data
}

const (
	tileAlign = 16
)

// planLayout computes the full file layout. It does NOT write any bytes. order
// is the tile emission order (row-major/hilbert/morton) — tile data is placed in
// that order (so a reorder strategy's spatial locality is real), while the
// returned TileOffsets stay raster-indexed for the IFD.
func planLayout(order tileorder.OrderStrategy, in layoutInput) (layoutPlan, error) {
	useBig, err := decideBigTIFF(in)
	if err != nil {
		return layoutPlan{}, err
	}
	plan := layoutPlan{
		BigTIFF:    useBig,
		HeaderSize: uint64(tiff.HeaderSize(useBig)),
	}
	plan.GhostOffset = plan.HeaderSize

	ghostBytes, err := defaultGhost().Marshal()
	if err != nil {
		return layoutPlan{}, fmt.Errorf("ghost: %w", err)
	}
	plan.GhostSize = uint64(len(ghostBytes))
	plan.FirstIFDOffset = plan.GhostOffset + plan.GhostSize

	cursor := plan.FirstIFDOffset

	// Phase 1: pyramid IFD records + their external tag arrays packed in order.
	plan.Levels = make([]levelLayoutPlan, len(in.Levels))
	for i, lv := range in.Levels {
		ifdSize, externalSize := ifdSizeForLevel(lv, useBig)
		plan.Levels[i].IFDOffset = cursor
		plan.Levels[i].Reserved = ifdSize + externalSize
		cursor += ifdSize + externalSize
	}

	// Phase 2: associated-image IFD records + their externals.
	plan.Associated = make([]associatedLayoutPlan, len(in.Associated))
	for i, a := range in.Associated {
		ifdSize, externalSize := ifdSizeForAssociated(a, useBig)
		plan.Associated[i].IFDOffset = cursor
		plan.Associated[i].Reserved = ifdSize + externalSize
		cursor += ifdSize + externalSize
	}

	// Align to 16 bytes before tile data starts.
	cursor = alignUp(cursor, tileAlign)
	plan.HeadBlockEnd = cursor

	// Phase 3: tile data in REVERSE level order (smallest first). Within a level,
	// tiles are placed in EMISSION order — the k-th emitted tile (raster index
	// rasterIdx = order-mapped from k) gets the next slot, sized for THAT tile.
	// This is what makes a reorder strategy's on-disk spatial locality real and,
	// critically, keeps each slot sized for the tile written into it (#41 — the
	// old raster-order sizing overran slots for reordered tiles). The offsets
	// array stays raster-indexed (offsets[rasterIdx]) so it drops straight into
	// the IFD. Row-major is unchanged (rasterIdx == k).
	for i := len(in.Levels) - 1; i >= 0; i-- {
		lv := in.Levels[i]
		offsets := make([]uint64, len(lv.TileBytes))
		// GridX==0 (minimal/synthetic inputs): fall back to a single row so the
		// grid math can't divide by zero; that's raster order, identical to the
		// pre-#41 packing.
		gridX := lv.GridX
		if gridX == 0 {
			gridX = lv.TileCount
		}
		var tilesY uint32
		if gridX > 0 {
			tilesY = lv.TileCount / gridX
		}
		firstSet := false
		for k := uint32(0); k < lv.TileCount; k++ {
			x, y := order.IndexToXY(k, gridX, tilesY)
			rasterIdx := y*gridX + x
			cursor = alignUp(cursor, tileAlign)
			if !firstSet {
				plan.Levels[i].TileDataOffset = cursor // start of this level's tile region
				firstSet = true
			}
			offsets[rasterIdx] = cursor
			cursor += uint64(lv.TileBytes[rasterIdx])
		}
		plan.Levels[i].TileOffsets = offsets
	}

	// Phase 4: associated-image data after all pyramid data.
	for i, a := range in.Associated {
		cursor = alignUp(cursor, tileAlign)
		plan.Associated[i].DataOffset = cursor
		cursor += a.totalBytes()
	}

	plan.FileSize = cursor
	return plan, nil
}

func decideBigTIFF(in layoutInput) (bool, error) {
	switch in.BigTIFFMode {
	case BigTIFFOn:
		return true, nil
	case BigTIFFOff:
		return false, nil
	}
	var total uint64
	for _, lv := range in.Levels {
		for _, n := range lv.TileBytes {
			total += uint64(n)
		}
	}
	for _, a := range in.Associated {
		total += a.totalBytes()
	}
	total += uint64(in.MetaBytes) + 64*1024 // metadata + safety margin
	// Promote when predicted size > 2 GiB (leaves 2 GiB cushion under the 4 GiB classic ceiling).
	return total > (2 << 30), nil
}

// ifdSizeForLevel returns (ifd_record_size, external_arrays_size) for a pyramid IFD.
func ifdSizeForLevel(lv levelLayoutInput, big bool) (uint64, uint64) {
	tagCount := countTagsForLevel(lv)
	ifd := uint64(tiff.IFDRecordSize(tagCount, big))

	// External arrays for tags that don't fit inline:
	//   TileOffsets:     N entries × (4 or 8) bytes
	//   TileByteCounts:  N entries × (4 or 8) bytes
	//   JPEGTables:      raw bytes (if present)
	//   BitsPerSample:   SamplesPerPixel=3 → 3×2=6 bytes; > 4 byte classic inline cap → external
	//   WSIImageType:    "pyramid\0" = 8 bytes; > 4 byte classic inline cap → external
	//
	// BigTIFF has an 8-byte inline cap, so BitsPerSample (6 bytes) and
	// WSIImageType (8 bytes) both fit inline. Classic TIFF needs them external.
	var external uint64
	// offsetFieldSize is the per-element byte width for TileOffsets/TileByteCounts:
	// 4 bytes (uint32) for classic TIFF, 8 bytes (uint64) for BigTIFF.
	offsetFieldSize := uint64(4)
	if big {
		offsetFieldSize = 8
	}
	external += uint64(len(lv.TileBytes)) * offsetFieldSize // TileOffsets
	external += uint64(len(lv.TileBytes)) * offsetFieldSize // TileByteCounts
	if lv.JPEGTables != nil {
		external += uint64(len(lv.JPEGTables))
	}
	if !big {
		// BitsPerSample: 3 SHORTs = 6 bytes (> 4-byte classic inline cap).
		external += 6
		// WSIImageType ASCII: "pyramid\0" = 8 bytes (> 4-byte classic inline
		// cap). Counted for every pyramid level including L0 (the L0
		// metadata sum below covers only the L0-specific tags).
		external += 8
	}
	if lv.IsL0 {
		// Exact upper-bound external size for the L0 metadata tags
		// (ImageDescription, scanner strings, MPP/mag doubles, resolution,
		// ICC). Replaces the old fixed 2 KiB guess.
		external += lv.L0MetaExternal
	}
	return ifd, external
}

// ifdSizeForAssociated returns (ifd_record_size, external_arrays_size) for an
// associated-image IFD. Mirrors ifdSizeForLevel: the multi-strip arrays,
// JPEGTables, and classic-TIFF external BitsPerSample/WSIImageType all live in
// the external area. Predictor (317) is a single inline SHORT — no external.
func ifdSizeForAssociated(a associatedLayoutInput, big bool) (uint64, uint64) {
	tagCount := countTagsForAssociated(a)
	ifd := uint64(tiff.IFDRecordSize(tagCount, big))
	var external uint64
	offsetFieldSize := uint64(4)
	if big {
		offsetFieldSize = 8
	}
	n := uint64(len(a.StripBytes))
	external += n * offsetFieldSize // StripOffsets
	external += n * offsetFieldSize // StripByteCounts
	external += uint64(a.JPEGTables)
	if !big {
		external += 6 // BitsPerSample: 3 SHORTs = 6 bytes (> 4-byte classic inline cap)
		// WSIImageType ASCII: len(Type)+1 (NUL), padded to even; external when
		// > 4-byte classic inline cap.
		wt := uint64(len(a.Type) + 1)
		if wt%2 != 0 {
			wt++
		}
		external += wt
	}
	return ifd, external
}

// countTagsForLevel returns the count of TIFF directory entries we will emit
// on a pyramid IFD. Must be kept in sync with populateLevelIFD in writer.go.
func countTagsForLevel(lv levelLayoutInput) int {
	// Always present: NewSubfileType, ImageWidth, ImageLength, BitsPerSample,
	// Compression, PhotometricInterpretation, SamplesPerPixel, PlanarConfig,
	// TileWidth, TileLength, TileOffsets, TileByteCounts, WSIImageType,
	// WSILevelIndex, WSILevelCount. (15)
	n := 15
	if lv.JPEGTables != nil {
		n++ // JPEGTables
	}
	if lv.IsL0 {
		// ImageDescription, Make, Model, Software, DateTime, SourceFormat,
		// ToolsVersion, WSIMPPX, WSIMPPY, WSIMagnification,
		// XResolution, YResolution, ResolutionUnit, ICCProfile. (14;
		// emitted only when set — but for size budgeting we assume all
		// may appear.)
		n += 14
	}
	return n
}

func countTagsForAssociated(a associatedLayoutInput) int {
	// NewSubfileType, ImageWidth, ImageLength, BitsPerSample, Compression,
	// PhotometricInterpretation, SamplesPerPixel, PlanarConfig, StripOffsets,
	// StripByteCounts, RowsPerStrip, WSIImageType. (12)
	n := 12
	if a.Predictor > 1 {
		n++ // Predictor (317)
	}
	if a.JPEGTables > 0 {
		n++ // JPEGTables (347)
	}
	return n
}

func alignUp(v, align uint64) uint64 {
	if rem := v % align; rem != 0 {
		v += align - rem
	}
	return v
}
