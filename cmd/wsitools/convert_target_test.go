package main

import "testing"

func TestResolveConvertTarget(t *testing.T) {
	cases := []struct {
		to        string
		srcFormat string
		want      string
		wantErr   bool
	}{
		{"dicom", "svs", "dicom", false},
		{"", "svs", "svs", false},
		{"", "ome-tiff", "ome-tiff", false},
		{"", "bogus-format", "", true},
	}
	for _, c := range cases {
		got, err := resolveConvertTarget(c.to, c.srcFormat)
		if (err != nil) != c.wantErr {
			t.Fatalf("to=%q src=%q: err=%v wantErr=%v", c.to, c.srcFormat, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Fatalf("to=%q src=%q: got %q want %q", c.to, c.srcFormat, got, c.want)
		}
	}
}
