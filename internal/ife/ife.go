// Package ife writes Iris File Extension (IFE) v1.0 whole-slide files with
// JPEG/AVIF tiles. It never writes the IRIS-proprietary codec. The container is
// all little-endian; every block opens with an 8-byte validation field (== the
// block's own file offset) and a 2-byte recovery magic. Verified against
// opentile-go's IFE reader (formats/ife) and the official Iris-Codec validator.
package ife

// On-disk constants (opentile-go formats/ife + Iris-Headers IrisCodecExtension.hpp).
const (
	magicBytes     uint32 = 0x49726973 // "Iris" as LE uint32
	nullOffset     uint64 = 0xFFFFFFFFFFFFFFFF
	nullTile       uint64 = 0xFFFFFFFFFF // 40-bit all-ones
	tileSidePixels        = 256

	extMajor uint16 = 1
	extMinor uint16 = 0

	// Pyramid-tile encoding (TILE_TABLE.encoding).
	encJPEG uint8 = 2
	encAVIF uint8 = 3
	// Associated-image encoding (IMAGE_ENTRY.encoding); note 1=PNG here.
	imgEncPNG  uint8 = 1
	imgEncJPEG uint8 = 2
	imgEncAVIF uint8 = 3

	formatR8G8B8 uint8 = 2

	attrFormatFreeText uint8 = 1

	// Recovery magics — the RECOVERY enum from Iris-Headers IrisCodecExtension.hpp.
	// EVERY block gets its correct tag: the official Iris-Codec validator checks
	// them even though opentile's reader ignores the tile-path ones.
	recoverHeader          uint16 = 0x5501 // FILE_HEADER
	recoverTileTable       uint16 = 0x5502 // TILE_TABLE
	recoverMetadata        uint16 = 0x5504
	recoverAttributes      uint16 = 0x5505
	recoverLayerExtents    uint16 = 0x5506 // LAYER_EXTENTS
	recoverTileOffsets     uint16 = 0x5507 // TILE_OFFSETS
	recoverAttributesSizes uint16 = 0x5508
	recoverAttributesBytes uint16 = 0x5509
	recoverImageArray      uint16 = 0x550A
	recoverImageBytes      uint16 = 0x550B
	recoverICCProfile      uint16 = 0x550C
)

// EncodingFor maps a wsitools codec name to the IFE TILE_TABLE encoding byte.
// ok=false for codecs IFE pyramid tiles can't carry.
func EncodingFor(codecName string) (uint8, bool) {
	switch codecName {
	case "jpeg", "":
		return encJPEG, true
	case "avif":
		return encAVIF, true
	default:
		return 0, false
	}
}

// putUint40 writes v as a 40-bit little-endian integer into b[0:5].
func putUint40(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
}

// putUint24 writes v as a 24-bit little-endian integer into b[0:3].
func putUint24(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
}
