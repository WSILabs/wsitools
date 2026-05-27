package main

import (
	"fmt"
	"strings"
)

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
// attribute records wsitools provenance.
func SyntheticOMEDescription(l0W, l0H uint32, mppX, mppY float64, name, srcSoftware string) string {
	if name == "" {
		name = "Image"
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<OME xmlns="http://www.openmicroscopy.org/Schemas/OME/2016-06"`)
	b.WriteString(` Creator="wsitools/` + Version)
	if srcSoftware != "" {
		b.WriteString(` (from ` + xmlEscape(srcSoftware) + `)`)
	}
	b.WriteString(`">` + "\n")
	b.WriteString(`  <Image ID="Image:0" Name="` + xmlEscape(name) + `">` + "\n")
	b.WriteString(`    <Pixels ID="Pixels:0:0" DimensionOrder="XYCZT" Type="uint8"`)
	b.WriteString(fmt.Sprintf(` SizeX="%d" SizeY="%d" SizeZ="1" SizeC="3" SizeT="1"`, l0W, l0H))
	if mppX != 0 {
		b.WriteString(fmt.Sprintf(` PhysicalSizeX="%g" PhysicalSizeXUnit="µm"`, mppX))
	}
	if mppY != 0 {
		b.WriteString(fmt.Sprintf(` PhysicalSizeY="%g" PhysicalSizeYUnit="µm"`, mppY))
	}
	b.WriteString(`>` + "\n")
	b.WriteString(`      <Channel ID="Channel:0:0" Name="Red" SamplesPerPixel="1"/>` + "\n")
	b.WriteString(`      <Channel ID="Channel:0:1" Name="Green" SamplesPerPixel="1"/>` + "\n")
	b.WriteString(`      <Channel ID="Channel:0:2" Name="Blue" SamplesPerPixel="1"/>` + "\n")
	b.WriteString(`      <TiffData FirstC="0" FirstZ="0" FirstT="0" IFD="0" PlaneCount="1"/>` + "\n")
	b.WriteString(`    </Pixels>` + "\n")
	b.WriteString(`  </Image>` + "\n")
	b.WriteString(`</OME>`)
	return b.String()
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
