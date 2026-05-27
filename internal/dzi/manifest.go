package dzi

import (
	"encoding/xml"
	"fmt"
	"io"
)

// Manifest is the in-memory representation of a DZI .dzi manifest.
// The writer emits the canonical Microsoft DeepZoom 2008 namespace.
type Manifest struct {
	Format   string // "jpeg" or "png"
	Overlap  int
	TileSize int
	Width    int
	Height   int
}

type manifestXML struct {
	XMLName  xml.Name `xml:"Image"`
	XMLNS    string   `xml:"xmlns,attr"`
	Format   string   `xml:"Format,attr"`
	Overlap  int      `xml:"Overlap,attr"`
	TileSize int      `xml:"TileSize,attr"`
	Size     sizeXML  `xml:"Size"`
}

type sizeXML struct {
	Width  int `xml:"Width,attr"`
	Height int `xml:"Height,attr"`
}

// Write emits the manifest as a UTF-8 XML document. Named Write (not
// WriteTo) to avoid clashing with io.WriterTo's (int64, error) shape.
func (m Manifest) Write(w io.Writer) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	doc := manifestXML{
		XMLNS:    "http://schemas.microsoft.com/deepzoom/2008",
		Format:   m.Format,
		Overlap:  m.Overlap,
		TileSize: m.TileSize,
		Size:     sizeXML{Width: m.Width, Height: m.Height},
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("dzi: encode manifest: %w", err)
	}
	return enc.Flush()
}
