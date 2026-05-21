package tiff

// Standard TIFF tag IDs we use (subset of TIFF 6.0 §2 plus BigTIFF
// additions). Centralized so writers don't redeclare them.
const (
	TagNewSubfileType            uint16 = 254
	TagImageWidth                uint16 = 256
	TagImageLength               uint16 = 257
	TagBitsPerSample             uint16 = 258
	TagCompression               uint16 = 259
	TagPhotometricInterpretation uint16 = 262
	TagImageDescription          uint16 = 270
	TagMake                      uint16 = 271
	TagModel                     uint16 = 272
	TagStripOffsets               uint16 = 273
	TagSamplesPerPixel           uint16 = 277
	TagRowsPerStrip              uint16 = 278
	TagStripByteCounts           uint16 = 279
	TagPlanarConfiguration       uint16 = 284
	TagSoftware                  uint16 = 305
	TagDateTime                  uint16 = 306
	TagTileWidth                 uint16 = 322
	TagTileLength                uint16 = 323
	TagTileOffsets               uint16 = 324
	TagTileByteCounts            uint16 = 325
	TagJPEGTables                uint16 = 347
)

// TIFF Compression tag values we support. The Compression tag (259)
// itself is declared above; these are the value-space constants.
const (
	CompressionNone     uint16 = 1
	CompressionLZW      uint16 = 5
	CompressionJPEG     uint16 = 7
	CompressionDeflate  uint16 = 8
	CompressionJPEG2000 uint16 = 33003 // Aperio / OpenJPEG codestream
)

// WSI private tag IDs (range 65080–65087) reserved by wsitools.
// See docs/superpowers/specs/2026-05-20-cog-wsi-format.md §5.2.
const (
	TagWSIImageType     uint16 = 65080
	TagWSILevelIndex    uint16 = 65081
	TagWSILevelCount    uint16 = 65082
	TagWSISourceFormat  uint16 = 65083
	TagWSIToolsVersion  uint16 = 65084
	TagWSIMPPX          uint16 = 65085
	TagWSIMPPY          uint16 = 65086
	TagWSIMagnification uint16 = 65087
)
