package tiff

import (
	"fmt"
	"strings"
)

// TagName returns the well-known name for a TIFF tag, or "" if unknown.
func TagName(tag uint16) string {
	return tagNames[tag]
}

// TypeName returns the TIFF type name, or "TYPE_<n>" for unknown types.
func TypeName(typ uint16) string {
	if name, ok := typeNames[typ]; ok {
		return name
	}
	return fmt.Sprintf("TYPE_%d", typ)
}

// TypeSize returns the bytes-per-element for a TIFF type, or 0 if unknown.
func TypeSize(typ uint16) int {
	return typeSizes[typ]
}

// InterpretEnum returns a friendly name for well-known enum tags.
// Returns "" when no interpreter exists for the given tag/value.
func InterpretEnum(tag uint16, val uint64) string {
	switch tag {
	case 254: // NewSubfileType (bitfield)
		var parts []string
		if val&1 != 0 {
			parts = append(parts, "reduced-resolution")
		}
		if val&2 != 0 {
			parts = append(parts, "page-of-multi")
		}
		if val&4 != 0 {
			parts = append(parts, "transparency-mask")
		}
		return strings.Join(parts, "|")
	case 255: // SubfileType
		return map[uint64]string{
			1: "full-resolution",
			2: "reduced-resolution",
			3: "page-of-multi",
		}[val]
	case 259: // Compression
		return map[uint64]string{
			1:     "None",
			2:     "CCITT-1D",
			3:     "CCITTFax3",
			4:     "CCITTFax4",
			5:     "LZW",
			6:     "OJPEG",
			7:     "JPEG",
			8:     "Deflate",
			32773: "PackBits",
			33003: "JPEG2000",
			50001: "WebP",
			50002: "JPEG-XL",
			60001: "AVIF",
			60003: "HTJ2K",
		}[val]
	case 262: // PhotometricInterpretation
		return map[uint64]string{
			0: "WhiteIsZero",
			1: "BlackIsZero",
			2: "RGB",
			3: "Palette",
			4: "Mask",
			5: "Separated",
			6: "YCbCr",
			8: "CIELab",
		}[val]
	case 266: // FillOrder
		return map[uint64]string{1: "msb2lsb", 2: "lsb2msb"}[val]
	case 274: // Orientation
		return map[uint64]string{
			1: "top-left", 2: "top-right", 3: "bottom-right", 4: "bottom-left",
			5: "left-top", 6: "right-top", 7: "right-bottom", 8: "left-bottom",
		}[val]
	case 284: // PlanarConfiguration
		return map[uint64]string{1: "chunky", 2: "planar"}[val]
	case 296: // ResolutionUnit
		return map[uint64]string{1: "none", 2: "inch", 3: "cm"}[val]
	case 317: // Predictor
		return map[uint64]string{
			1: "none", 2: "horizontal", 3: "floating-point",
		}[val]
	case 338: // ExtraSamples
		return map[uint64]string{
			0: "unspecified", 1: "associated-alpha", 2: "unassociated-alpha",
		}[val]
	case 339: // SampleFormat
		return map[uint64]string{
			1: "uint", 2: "int", 3: "float", 4: "undefined",
			5: "complex-int", 6: "complex-float",
		}[val]
	}
	return ""
}

var typeNames = map[uint16]string{
	1: "BYTE", 2: "ASCII", 3: "SHORT", 4: "LONG", 5: "RATIONAL",
	6: "SBYTE", 7: "UNDEFINED", 8: "SSHORT", 9: "SLONG", 10: "SRATIONAL",
	11: "FLOAT", 12: "DOUBLE", 13: "IFD",
	16: "LONG8", 17: "SLONG8", 18: "IFD8",
}

var typeSizes = map[uint16]int{
	1: 1, 2: 1, 3: 2, 4: 4, 5: 8,
	6: 1, 7: 1, 8: 2, 9: 4, 10: 8,
	11: 4, 12: 8, 13: 4,
	16: 8, 17: 8, 18: 8,
}

// tagNames is the well-known TIFF tag dictionary.
var tagNames = map[uint16]string{
	254: "NewSubfileType",
	255: "SubfileType",
	256: "ImageWidth",
	257: "ImageLength",
	258: "BitsPerSample",
	259: "Compression",
	262: "PhotometricInterpretation",
	263: "Threshholding",
	264: "CellWidth",
	265: "CellLength",
	266: "FillOrder",
	269: "DocumentName",
	270: "ImageDescription",
	271: "Make",
	272: "Model",
	273: "StripOffsets",
	274: "Orientation",
	277: "SamplesPerPixel",
	278: "RowsPerStrip",
	279: "StripByteCounts",
	280: "MinSampleValue",
	281: "MaxSampleValue",
	282: "XResolution",
	283: "YResolution",
	284: "PlanarConfiguration",
	285: "PageName",
	286: "XPosition",
	287: "YPosition",
	288: "FreeOffsets",
	289: "FreeByteCounts",
	290: "GrayResponseUnit",
	291: "GrayResponseCurve",
	292: "T4Options",
	293: "T6Options",
	296: "ResolutionUnit",
	297: "PageNumber",
	301: "TransferFunction",
	305: "Software",
	306: "DateTime",
	315: "Artist",
	316: "HostComputer",
	317: "Predictor",
	318: "WhitePoint",
	319: "PrimaryChromaticities",
	320: "ColorMap",
	321: "HalftoneHints",

	322: "TileWidth",
	323: "TileLength",
	324: "TileOffsets",
	325: "TileByteCounts",
	326: "BadFaxLines",
	327: "CleanFaxData",
	328: "ConsecutiveBadFaxLines",
	330: "SubIFDs",
	332: "InkSet",
	333: "InkNames",
	334: "NumberOfInks",
	336: "DotRange",
	337: "TargetPrinter",
	338: "ExtraSamples",
	339: "SampleFormat",
	340: "SMinSampleValue",
	341: "SMaxSampleValue",
	342: "TransferRange",
	343: "ClipPath",
	344: "XClipPathUnits",
	345: "YClipPathUnits",
	346: "Indexed",
	347: "JPEGTables",
	351: "OPIProxy",
	400: "GlobalParametersIFD",
	401: "ProfileType",
	402: "FaxProfile",
	403: "CodingMethods",
	404: "VersionYear",
	405: "ModeNumber",
	433: "Decode",
	434: "DefaultImageColor",
	512: "JPEGProc",
	513: "JPEGInterchangeFormat",
	514: "JPEGInterchangeFormatLength",
	515: "JPEGRestartInterval",
	517: "JPEGLosslessPredictors",
	518: "JPEGPointTransforms",
	519: "JPEGQTables",
	520: "JPEGDCTables",
	521: "JPEGACTables",
	529: "YCbCrCoefficients",
	530: "YCbCrSubSampling",
	531: "YCbCrPositioning",
	532: "ReferenceBlackWhite",
	700: "XMP",

	33550: "ModelPixelScale",
	33922: "ModelTiepoint",
	34264: "ModelTransformation",
	34735: "GeoKeyDirectory",
	34736: "GeoDoubleParams",
	34737: "GeoAsciiParams",

	34665: "ExifIFD",
	34675: "ICCProfile",
	33445: "MDFileTag",
	33446: "MDScalePixel",
	33447: "MDColorTable",

	50001: "WebPCompression",
	50002: "JPEGXLCompression",

	65080: "WSIImageType",
	65081: "WSILevelIndex",
	65082: "WSILevelCount",
	65083: "WSISourceFormat",
	65084: "WSIToolsVersion",
	65085: "WSIMPPx",
	65086: "WSIMPPy",
	65087: "WSIMagnification",
}
