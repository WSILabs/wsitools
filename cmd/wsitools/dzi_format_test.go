package main

import "testing"

func TestResolveDZIFormat(t *testing.T) {
	cases := []struct {
		name      string
		codec     string
		codecSet  bool
		dziFormat string
		want      string
		wantErr   bool
	}{
		{"default jpeg", "", false, "jpeg", "jpeg", false},
		{"codec png", "png", true, "jpeg", "png", false},
		{"codec jpeg", "jpeg", true, "jpeg", "jpeg", false},
		{"deprecated dzi-format png", "", false, "png", "png", false},
		{"codec wins over dzi-format", "png", true, "jpeg", "png", false},
		{"codec invalid for dzi", "avif", true, "jpeg", "", true},
		{"dzi-format invalid", "", false, "tiff", "", true},
		{"empty codec errors", "", true, "jpeg", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveDZIFormat(c.codec, c.codecSet, c.dziFormat)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
