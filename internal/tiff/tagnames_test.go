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
		{65085, "WSIMPPx"},
		{65086, "WSIMPPy"},
		{65087, "WSIMagnification"},
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

func TestInterpretEnum(t *testing.T) {
	cases := []struct {
		tag  uint16
		val  uint64
		want string
	}{
		{259, 1, "None"},
		{259, 5, "LZW"},
		{259, 7, "JPEG"},
		{259, 8, "Deflate"},
		{259, 33003, "JPEG2000"},
		{259, 50001, "WebP"},
		{259, 50002, "JPEG-XL"},
		{259, 99999, ""},
		{262, 0, "WhiteIsZero"},
		{262, 1, "BlackIsZero"},
		{262, 2, "RGB"},
		{262, 6, "YCbCr"},
		{262, 8, "CIELab"},
		{284, 1, "chunky"},
		{284, 2, "planar"},
		{296, 1, "none"}, {296, 2, "inch"}, {296, 3, "cm"},
		{274, 1, "top-left"},
		{274, 8, "left-bottom"},
		{317, 1, "none"}, {317, 2, "horizontal"}, {317, 3, "floating-point"},
		{266, 1, "msb2lsb"}, {266, 2, "lsb2msb"},
		{339, 1, "uint"}, {339, 3, "float"},
		{338, 0, "unspecified"}, {338, 1, "associated-alpha"}, {338, 2, "unassociated-alpha"},
		{255, 1, "full-resolution"}, {255, 2, "reduced-resolution"}, {255, 3, "page-of-multi"},
		{254, 0, ""},
		{254, 1, "reduced-resolution"},
		{254, 2, "page-of-multi"},
		{254, 4, "transparency-mask"},
		{254, 5, "reduced-resolution|transparency-mask"},
		{254, 7, "reduced-resolution|page-of-multi|transparency-mask"},
		{256, 1024, ""},
	}
	for _, c := range cases {
		if got := InterpretEnum(c.tag, c.val); got != c.want {
			t.Errorf("InterpretEnum(%d, %d) = %q, want %q", c.tag, c.val, got, c.want)
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

func TestTagNameImageDepth(t *testing.T) {
	if got := TagName(32997); got != "ImageDepth" {
		t.Fatalf("TagName(32997) = %q, want %q", got, "ImageDepth")
	}
	if got := TagName(530); got != "YCbCrSubSampling" {
		t.Fatalf("TagName(530) = %q, want %q", got, "YCbCrSubSampling")
	}
}

func TestAperioTagConstants(t *testing.T) {
	if TagImageDepth != 32997 {
		t.Errorf("TagImageDepth = %d, want 32997", TagImageDepth)
	}
	if TagYCbCrSubSampling != 530 {
		t.Errorf("TagYCbCrSubSampling = %d, want 530", TagYCbCrSubSampling)
	}
}
