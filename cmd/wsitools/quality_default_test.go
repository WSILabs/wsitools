package main

import "testing"

func TestParseQualityKnobs_DefaultAndReversible(t *testing.T) {
	k, err := parseQualityKnobs("")
	if err != nil || k["q"] != "85" {
		t.Fatalf("default: q=%q err=%v (want q=85)", k["q"], err)
	}
	k, err = parseQualityKnobs("reversible=true")
	if err != nil || k["reversible"] != "true" || k["q"] != "85" {
		t.Fatalf("reversible: %v err=%v", k, err)
	}
}
