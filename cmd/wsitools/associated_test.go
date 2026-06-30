package main

import (
	"strings"
	"testing"
)

func TestAssociatedCommandsRegistered(t *testing.T) {
	want := map[string]bool{"label": false, "macro": false, "thumbnail": false, "overview": false}
	for _, c := range rootCmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
			subs := map[string]bool{}
			for _, s := range c.Commands() {
				subs[s.Name()] = true
			}
			if !subs["remove"] || !subs["replace"] {
				t.Errorf("%s missing remove/replace subcommands", c.Name())
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("command %q not registered", name)
		}
	}
}

func TestResolveAssocOutputRejectsSameAsInput(t *testing.T) {
	if _, err := resolveAssocOutput("/x/slide.svs", "/x/slide.svs", false, false); err == nil || !strings.Contains(err.Error(), "same") {
		t.Fatalf("want same-path error, got %v", err)
	}
}

func TestResolveAssocOutputDerivesName(t *testing.T) {
	got, err := resolveAssocOutput("/x/slide.svs", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "slide_edited.svs") {
		t.Errorf("derived = %q, want .../slide_edited.svs", got)
	}
}

func TestResolveAssocOutputInPlaceReturnsInput(t *testing.T) {
	got, err := resolveAssocOutput("/x/slide.svs", "", true, false)
	if err != nil || got != "/x/slide.svs" {
		t.Fatalf("in-place got=%q err=%v, want input path", got, err)
	}
}

func TestResolveAssocOutputRejectsBothOutAndInPlace(t *testing.T) {
	if _, err := resolveAssocOutput("/x/slide.svs", "/y/out.svs", true, false); err == nil {
		t.Fatal("want error for -o together with --in-place")
	}
}
