package main

import "testing"

func TestOMEAssocName(t *testing.T) {
	cases := map[string]string{
		"label":     "label",
		"macro":     "macro",
		"overview":  "macro", // Aperio's overview maps to OME's macro
		"thumbnail": "thumbnail",
		"map":       "", // no OME equivalent → dropped
		"":          "",
	}
	for kind, want := range cases {
		if got := omeAssocName(kind); got != want {
			t.Errorf("omeAssocName(%q) = %q, want %q", kind, got, want)
		}
	}
}
