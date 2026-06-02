package main

import (
	"fmt"
	"strings"
)

// OMEAssoc describes one associated image (label/macro/thumbnail) to enumerate
// in the OME-XML. Name MUST be one of "label"/"macro"/"thumbnail" (the reader
// classifies any other Name as a main pyramid). W/H are pixel dimensions.
type OMEAssoc struct {
	Name string
	W, H uint32
}

// omePreamble is the OME-TIFF spec's recommended ImageDescription comment.
const omePreamble = `<!-- Warning: this comment is an OME-XML metadata block, which contains crucial dimensional parameters and other important metadata. Please edit cautiously (if at all), and back up the original data before doing so. -->`

// SyntheticOMEDescription builds a minimal OME-XML ImageDescription
// for OME-TIFF output written by wsitools from a non-OME source.
// opentile-go's OME reader (a port of tifffile's is_ome predicate)
// detects OME files by the literal suffix "OME>" on the L0
// ImageDescription. After the match, the parser requires at least
// one <Image> with a <Pixels> element carrying Size{X,Y,Z,C,T} and
// at least one <Channel>; an Image whose Name is "label"/"macro"/
// "thumbnail" is treated as associated, so the main pyramid image
// must have a different Name.
//
// Output is a single-image RGB document: SizeC=3, SizeZ=1, SizeT=1,
// Type=uint8. mppX/mppY (micrometres per pixel) are written when
// non-zero so reader Magnification math succeeds. The Creator
// attribute records wsitools provenance. assoc lists additional
// associated images to enumerate as <Image> elements (IFD positions
// 1, 2, … matching the order writeAssociatedImages writes them).
func SyntheticOMEDescription(l0W, l0H uint32, mppX, mppY float64, name, srcSoftware string, assoc []OMEAssoc) string {
	if name == "" {
		name = "Image"
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(omePreamble + "\n")
	b.WriteString(`<OME xmlns="http://www.openmicroscopy.org/Schemas/OME/2016-06"`)
	b.WriteString(` Creator="wsitools/` + Version)
	if srcSoftware != "" {
		b.WriteString(` (from ` + xmlEscape(srcSoftware) + `)`)
	}
	b.WriteString(`">` + "\n")
	writeOMEImage(&b, 0, name, l0W, l0H, mppX, mppY)
	for i, a := range assoc {
		writeOMEImage(&b, 1+i, a.Name, a.W, a.H, 0, 0)
	}
	b.WriteString(`</OME>`)
	return b.String()
}

// writeOMEImage writes one <Image>/<Pixels> block mapping to top-level IFD ifd.
// mppX/mppY are emitted as PhysicalSize only when non-zero.
func writeOMEImage(b *strings.Builder, ifd int, name string, w, h uint32, mppX, mppY float64) {
	fmt.Fprintf(b, `  <Image ID="Image:%d" Name="%s">`+"\n", ifd, xmlEscape(name))
	fmt.Fprintf(b, `    <Pixels ID="Pixels:%d:0" DimensionOrder="XYCZT" Type="uint8"`, ifd)
	fmt.Fprintf(b, ` SizeX="%d" SizeY="%d" SizeZ="1" SizeC="3" SizeT="1"`, w, h)
	if mppX != 0 {
		fmt.Fprintf(b, ` PhysicalSizeX="%g" PhysicalSizeXUnit="µm"`, mppX)
	}
	if mppY != 0 {
		fmt.Fprintf(b, ` PhysicalSizeY="%g" PhysicalSizeYUnit="µm"`, mppY)
	}
	b.WriteString(`>` + "\n")
	fmt.Fprintf(b, `      <Channel ID="Channel:%d:0" Name="Red" SamplesPerPixel="1"/>`+"\n", ifd)
	fmt.Fprintf(b, `      <Channel ID="Channel:%d:1" Name="Green" SamplesPerPixel="1"/>`+"\n", ifd)
	fmt.Fprintf(b, `      <Channel ID="Channel:%d:2" Name="Blue" SamplesPerPixel="1"/>`+"\n", ifd)
	fmt.Fprintf(b, `      <TiffData FirstC="0" FirstZ="0" FirstT="0" IFD="%d" PlaneCount="1"/>`+"\n", ifd)
	b.WriteString(`    </Pixels>` + "\n")
	b.WriteString(`  </Image>` + "\n")
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		`&`, `&amp;`,
		`<`, `&lt;`,
		`>`, `&gt;`,
		`"`, `&quot;`,
		`'`, `&apos;`,
	)
	return r.Replace(s)
}
