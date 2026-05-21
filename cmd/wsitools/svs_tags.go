package main

import "github.com/cornish/wsitools/internal/tiff"

// buildSVSL0ExtraTags returns the Aperio-specific tag set for the L0
// pyramid IFD of an SVS-shaped transcode output. desc is the source
// Aperio ImageDescription string (preserved verbatim from the source SVS).
func buildSVSL0ExtraTags(desc string) []tiff.RawTag {
	return []tiff.RawTag{
		{Tag: tiff.TagImageDescription, Type: tiff.TypeASCII, Value: desc},
	}
}

// buildSVSMacroExtraTags returns the Aperio-specific tag set for the
// macro associated image IFD. Aperio marks macro IFDs with
// NewSubfileType=9 (bits 0+3): bit 0 is the standard "reduced
// resolution" flag; bit 3 is Aperio's private marker for "this is the
// macro image."
func buildSVSMacroExtraTags() []tiff.RawTag {
	return []tiff.RawTag{
		{Tag: tiff.TagNewSubfileType, Type: tiff.TypeLONG, Value: []uint32{9}},
	}
}

// buildSVSLabelExtraTags returns the Aperio-specific tag set for the
// label associated image IFD. Standard NewSubfileType=1.
func buildSVSLabelExtraTags() []tiff.RawTag {
	return []tiff.RawTag{
		{Tag: tiff.TagNewSubfileType, Type: tiff.TypeLONG, Value: []uint32{1}},
	}
}
