package main

import "testing"

func TestResolveDZIFormat(t *testing.T) {
	cases := []struct {
		name         string
		codec        string
		codecSet     bool
		dziFormat    string
		dziFormatSet bool
		want         string
		wantErr      bool
	}{
		{"default jpeg", "", false, "jpeg", false, "jpeg", false},
		{"codec png", "png", true, "jpeg", false, "png", false},
		{"codec jpeg", "jpeg", true, "jpeg", false, "jpeg", false},
		{"deprecated dzi-format png", "", false, "png", true, "png", false},
		{"codec wins over dzi-format", "png", true, "jpeg", true, "png", false},
		{"codec invalid for dzi", "avif", true, "jpeg", false, "", true},
		{"dzi-format invalid", "", false, "tiff", true, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveDZIFormat(c.codec, c.codecSet, c.dziFormat, c.dziFormatSet)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
