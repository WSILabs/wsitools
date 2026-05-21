package tiff

import "testing"

func TestTIFFTypeConstants(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"BYTE", TypeBYTE, 1},
		{"ASCII", TypeASCII, 2},
		{"SHORT", TypeSHORT, 3},
		{"LONG", TypeLONG, 4},
		{"RATIONAL", TypeRATIONAL, 5},
		{"DOUBLE", TypeDOUBLE, 12},
		{"LONG8", TypeLONG8, 16},
		{"IFD8", TypeIFD8, 18},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}

func TestTypeByteSize(t *testing.T) {
	cases := []struct {
		t    uint16
		want int
	}{
		{TypeBYTE, 1},
		{TypeASCII, 1},
		{TypeSHORT, 2},
		{TypeLONG, 4},
		{TypeRATIONAL, 8},
		{TypeDOUBLE, 8},
		{TypeLONG8, 8},
		{TypeIFD8, 8},
	}
	for _, c := range cases {
		if got := TypeByteSize(c.t); got != c.want {
			t.Errorf("TypeByteSize(%d): got %d want %d", c.t, got, c.want)
		}
	}
}
