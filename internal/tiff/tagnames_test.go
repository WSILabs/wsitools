package tiff

import "testing"

func TestTagName(t *testing.T) {
	cases := []struct {
		tag  uint16
		want string
	}{
		{254, "NewSubfileType"},
		{256, "ImageWidth"},
		{257, "ImageLength"},
		{259, "Compression"},
		{262, "PhotometricInterpretation"},
		{270, "ImageDescription"},
		{322, "TileWidth"},
		{323, "TileLength"},
		{330, "SubIFDs"},
		{347, "JPEGTables"},
		{65083, "WSISourceFormat"},
		{12345, ""},
	}
	for _, c := range cases {
		if got := TagName(c.tag); got != c.want {
			t.Errorf("TagName(%d) = %q, want %q", c.tag, got, c.want)
		}
	}
}

func TestTypeName(t *testing.T) {
	cases := []struct {
		typ  uint16
		want string
	}{
		{1, "BYTE"}, {2, "ASCII"}, {3, "SHORT"}, {4, "LONG"},
		{5, "RATIONAL"}, {6, "SBYTE"}, {7, "UNDEFINED"},
		{8, "SSHORT"}, {9, "SLONG"}, {10, "SRATIONAL"},
		{11, "FLOAT"}, {12, "DOUBLE"},
		{13, "IFD"}, {16, "LONG8"}, {17, "SLONG8"}, {18, "IFD8"},
		{0, "TYPE_0"}, {99, "TYPE_99"},
	}
	for _, c := range cases {
		if got := TypeName(c.typ); got != c.want {
			t.Errorf("TypeName(%d) = %q, want %q", c.typ, got, c.want)
		}
	}
}

func TestTypeSize(t *testing.T) {
	cases := []struct {
		typ  uint16
		want int
	}{
		{1, 1}, {2, 1}, {3, 2}, {4, 4}, {5, 8},
		{6, 1}, {7, 1}, {8, 2}, {9, 4}, {10, 8},
		{11, 4}, {12, 8}, {13, 4}, {16, 8}, {17, 8}, {18, 8},
		{0, 0}, {99, 0},
	}
	for _, c := range cases {
		if got := TypeSize(c.typ); got != c.want {
			t.Errorf("TypeSize(%d) = %d, want %d", c.typ, got, c.want)
		}
	}
}
