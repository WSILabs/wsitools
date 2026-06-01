package cogwsiwriter

import (
	"fmt"

	"github.com/wsilabs/wsitools/internal/tiff"
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
	Bytes         uint32 // total length
	Width, Height uint32
	Compression   uint16
	Kind          string // canonical WSIImageType value
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
	BigTIFF         bool
	HeaderSize      uint64 // 8 (classic) or 16 (BigTIFF)
	GhostOffset     uint64 // == HeaderSize
	GhostSize       uint64
	FirstIFDOffset  uint64 // immediately after ghost
	Levels          []levelLayoutPlan
	Associated      []associatedLayoutPlan
	HeadBlockEnd    uint64 // first byte of pyramid tile data area
	FileSize        uint64 // total file size including all tile + associated data
}

const (
	tileAlign = 16
)

// planLayout computes the full file layout. It does NOT write any bytes.
func planLayout(in layoutInput) (layoutPlan, error) {
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

	// Phase 3: tile data in REVERSE level order (smallest first).
	for i := len(in.Levels) - 1; i >= 0; i-- {
		lv := in.Levels[i]
		offsets := make([]uint64, len(lv.TileBytes))
		for j, n := range lv.TileBytes {
			cursor = alignUp(cursor, tileAlign)
			offsets[j] = cursor
			cursor += uint64(n)
		}
		plan.Levels[i].TileOffsets = offsets
		if len(offsets) > 0 {
			plan.Levels[i].TileDataOffset = offsets[0]
		}
	}

	// Phase 4: associated-image data after all pyramid data.
	for i, a := range in.Associated {
		cursor = alignUp(cursor, tileAlign)
		plan.Associated[i].DataOffset = cursor
		cursor += uint64(a.Bytes)
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
		total += uint64(a.Bytes)
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
		// WSIImageType ASCII: "pyramid\0" = 8 bytes (> 4-byte classic inline cap).
		// IsL0 levels' 2048-byte budget below already covers this.
		if !lv.IsL0 {
			external += 8
		}
	}
	if lv.IsL0 {
		// Exact upper-bound external size for the L0 metadata tags
		// (ImageDescription, scanner strings, MPP/mag doubles, resolution,
		// ICC). Replaces the old fixed 2 KiB guess.
		external += lv.L0MetaExternal
	}
	return ifd, external
}

func ifdSizeForAssociated(a associatedLayoutInput, big bool) (uint64, uint64) {
	tagCount := countTagsForAssociated(a)
	ifd := uint64(tiff.IFDRecordSize(tagCount, big))
	// Associated images use StripOffsets/StripByteCounts (1 entry each, typically inline).
	// Reserve 64 bytes external for safety (BitsPerSample array, etc.).
	return ifd, 64
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

func countTagsForAssociated(_ associatedLayoutInput) int {
	// NewSubfileType, ImageWidth, ImageLength, BitsPerSample, Compression,
	// PhotometricInterpretation, SamplesPerPixel, PlanarConfig, StripOffsets,
	// StripByteCounts, RowsPerStrip, WSIImageType. (12)
	return 12
}

func alignUp(v, align uint64) uint64 {
	if rem := v % align; rem != 0 {
		v += align - rem
	}
	return v
}
