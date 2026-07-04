// Package szi provides the Smart Zoom Image writer: a ZIP-around-DZI
// wrapper plus the SZI-specific scan-properties.xml document populated
// from the source slide's metadata.
package szi

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"

	"github.com/wsilabs/wsitools/internal/source"
)

// The SZI scan-properties.xml document is rooted at <image> (NOT <scan-properties>)
// with a <properties> list of <property><name>/<value> children, matching the real
// Sakura/PathoZoom format (see sample_files/szi/CMU-1.szi) and opentile-go's reader.
// Property NAMES must match the reader's mapping (VendorName / ScannerName /
// ObjectiveMagnification / MicronsPerPixel{X,Y} / ScannerSerialNo / TimeStart /
// SoftwareName) or the values won't round-trip.
type propXML struct {
	Name  string `xml:"name"`
	Value string `xml:"value"`
}

type imageXML struct {
	XMLName    xml.Name  `xml:"image"`
	Xmlns      string    `xml:"xmlns,attr"`
	Date       string    `xml:"date,attr,omitempty"`
	Version    string    `xml:"version,attr"`
	Properties []propXML `xml:"properties>property"`
}

// WriteScanProperties emits an SZI scan-properties.xml document from the given
// source Metadata and the L0 image dimensions (imgW×imgH pixels). Empty/zero
// fields are omitted. Per the SZI spec (v1.2 §3.4) the mandatory fields are
// ImageWidth, ImageHeight, MicronsPerPixel, MicronsPerPixelX, MicronsPerPixelY —
// they let viewers add a measurement tool — so those are always emitted when
// known.
func WriteScanProperties(w io.Writer, md source.Metadata, imgW, imgH int) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	doc := imageXML{Xmlns: "http://www.pathozoom.com/szi", Version: "1.0"}
	if !md.AcquisitionDateTime.IsZero() {
		doc.Date = md.AcquisitionDateTime.UTC().Format("2006-01-02")
	}
	add := func(name, val string) {
		if val != "" {
			doc.Properties = append(doc.Properties, propXML{Name: name, Value: val})
		}
	}
	num := func(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
	// --- Mandatory measurement fields (SZI spec §3.4) ---
	if imgW > 0 {
		add("ImageWidth", strconv.Itoa(imgW))
	}
	if imgH > 0 {
		add("ImageHeight", strconv.Itoa(imgH))
	}
	// MicronsPerPixel is the single (symmetric) value; the spec says to take an
	// average when X and Y differ. md.MPP is the symmetric value (0 if asymmetric).
	mpp := md.MPP
	if mpp == 0 && md.MPPX != 0 && md.MPPY != 0 {
		mpp = (md.MPPX + md.MPPY) / 2
	}
	if mpp != 0 {
		add("MicronsPerPixel", num(mpp))
	}
	if md.MPPX != 0 {
		add("MicronsPerPixelX", num(md.MPPX))
	}
	if md.MPPY != 0 {
		add("MicronsPerPixelY", num(md.MPPY))
	}
	// --- Optional WSI fields ---
	add("VendorName", md.Make)
	add("ScannerName", md.Model)
	if md.Magnification != 0 {
		add("ObjectiveMagnification", num(md.Magnification))
	}
	add("ScannerSerialNo", md.SerialNumber)
	if !md.AcquisitionDateTime.IsZero() {
		add("TimeStart", md.AcquisitionDateTime.UTC().Format("2006-01-02T15:04:05"))
	}
	add("SoftwareName", md.Software)

	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("szi: encode scan-properties: %w", err)
	}
	return enc.Flush()
}
