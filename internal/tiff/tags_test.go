package tiff

import "testing"

func TestStandardTagIDs(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"NewSubfileType", TagNewSubfileType, 254},
		{"ImageWidth", TagImageWidth, 256},
		{"ImageLength", TagImageLength, 257},
		{"BitsPerSample", TagBitsPerSample, 258},
		{"Compression", TagCompression, 259},
		{"PhotometricInterpretation", TagPhotometricInterpretation, 262},
		{"ImageDescription", TagImageDescription, 270},
		{"Make", TagMake, 271},
		{"Model", TagModel, 272},
		{"StripOffsets", TagStripOffsets, 273},
		{"SamplesPerPixel", TagSamplesPerPixel, 277},
		{"RowsPerStrip", TagRowsPerStrip, 278},
		{"StripByteCounts", TagStripByteCounts, 279},
		{"PlanarConfiguration", TagPlanarConfiguration, 284},
		{"Software", TagSoftware, 305},
		{"DateTime", TagDateTime, 306},
		{"TileWidth", TagTileWidth, 322},
		{"TileLength", TagTileLength, 323},
		{"TileOffsets", TagTileOffsets, 324},
		{"TileByteCounts", TagTileByteCounts, 325},
		{"JPEGTables", TagJPEGTables, 347},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}

func TestWSIPrivateTagIDs(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"WSIImageType", TagWSIImageType, 65080},
		{"WSILevelIndex", TagWSILevelIndex, 65081},
		{"WSILevelCount", TagWSILevelCount, 65082},
		{"WSISourceFormat", TagWSISourceFormat, 65083},
		{"WSIToolsVersion", TagWSIToolsVersion, 65084},
		{"WSIMPPX", TagWSIMPPX, 65085},
		{"WSIMPPY", TagWSIMPPY, 65086},
		{"WSIMagnification", TagWSIMagnification, 65087},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
		if c.got < 32768 {
			t.Errorf("%s tag id %d outside TIFF private range (>= 32768)", c.name, c.got)
		}
	}
}

func TestCompressionConstants(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"None", CompressionNone, 1},
		{"LZW", CompressionLZW, 5},
		{"JPEG", CompressionJPEG, 7},
		{"Deflate", CompressionDeflate, 8},
		{"JPEG2000", CompressionJPEG2000, 33003},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}

func TestExtensionCompressionConstants(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"WebP", CompressionWebP, 50001},
		{"JPEGXL", CompressionJPEGXL, 50002},
		{"AVIF", CompressionAVIF, 60001},
		{"HTJ2K", CompressionHTJ2K, 60003},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}
