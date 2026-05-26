// Package szi provides the Smart Zoom Image writer: a ZIP-around-DZI
// wrapper plus the SZI-specific scan-properties.xml document populated
// from the source slide's metadata.
package szi

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/wsilabs/wsitools/internal/source"
)

type propXML struct {
	XMLName xml.Name `xml:"property"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:",chardata"`
}

type scanPropsXML struct {
	XMLName    xml.Name  `xml:"scan-properties"`
	Properties []propXML `xml:"property"`
}

// WriteScanProperties emits an SZI scan-properties.xml document from
// the given source Metadata. Empty/zero fields are omitted.
func WriteScanProperties(w io.Writer, md source.Metadata) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	doc := scanPropsXML{}
	add := func(name, val string) {
		if val != "" {
			doc.Properties = append(doc.Properties, propXML{Name: name, Value: val})
		}
	}
	add("ScannerManufacturer", md.Make)
	add("ScannerModel", md.Model)
	if md.Magnification != 0 {
		add("Magnification", strconv.FormatFloat(md.Magnification, 'g', -1, 64))
	}
	add("ScannerSerial", md.SerialNumber)
	if !md.AcquisitionDateTime.IsZero() {
		add("AcquisitionDateTime", md.AcquisitionDateTime.UTC().Format(time.RFC3339))
	}
	add("ScannerSoftware", md.Software)

	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("szi: encode scan-properties: %w", err)
	}
	return enc.Flush()
}
