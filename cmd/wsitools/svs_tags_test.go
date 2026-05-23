package main

import (
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
)

func TestBuildSVSL0ExtraTagsContainsImageDescription(t *testing.T) {
	desc := "Aperio Image Library v11.2.1\r\nMPP = 0.499"
	tags := buildSVSL0ExtraTags(desc)
	found := false
	for _, raw := range tags {
		if raw.Tag == tiff.TagImageDescription {
			if s, ok := raw.Value.(string); ok && strings.Contains(s, "Aperio Image Library") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected ImageDescription tag with Aperio prefix, got %v", tags)
	}
}

func TestSVSMacroSubfileTypeIs9(t *testing.T) {
	tags := buildSVSMacroExtraTags()
	for _, raw := range tags {
		if raw.Tag == tiff.TagNewSubfileType {
			if vs, ok := raw.Value.([]uint32); ok && len(vs) == 1 && vs[0] == 9 {
				return
			}
		}
	}
	t.Errorf("expected NewSubfileType=9 on macro tags, got %v", tags)
}

func TestSVSLabelSubfileTypeIs1(t *testing.T) {
	tags := buildSVSLabelExtraTags()
	for _, raw := range tags {
		if raw.Tag == tiff.TagNewSubfileType {
			if vs, ok := raw.Value.([]uint32); ok && len(vs) == 1 && vs[0] == 1 {
				return
			}
		}
	}
	t.Errorf("expected NewSubfileType=1 on label tags, got %v", tags)
}
