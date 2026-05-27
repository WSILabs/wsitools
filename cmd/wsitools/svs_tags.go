package main

import "github.com/wsilabs/wsitools/internal/tiff"

// buildL0ImageDescriptionTag returns an L0-only ExtraTags slice that
// emits the supplied ImageDescription on the L0 IFD. Used by SVS
// (Aperio header) and OME-TIFF (OME-XML document) output paths.
func buildL0ImageDescriptionTag(desc string) []tiff.RawTag {
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
