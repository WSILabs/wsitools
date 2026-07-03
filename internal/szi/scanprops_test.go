package szi

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/wsilabs/wsitools/internal/source"
)

func TestScanPropsAllFields(t *testing.T) {
	md := source.Metadata{
		Make:                "Aperio",
		Model:               "ScanScope CS",
		Magnification:       40,
		MPPX:                0.25,
		MPPY:                0.25,
		SerialNumber:        "SN-12345",
		AcquisitionDateTime: time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC),
		Software:            "Aperio v12.1",
	}
	var buf bytes.Buffer
	if err := WriteScanProperties(&buf, md); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	// Root MUST be <image> (the real Sakura/PathoZoom format + what opentile-go's
	// reader expects), NOT the old <scan-properties> — that mismatch made every
	// szi output unreadable (wsitools#26).
	if !strings.Contains(s, "<image ") || strings.Contains(s, "<scan-properties") {
		t.Errorf("root element must be <image>, not <scan-properties>; got:\n%s", s)
	}
	// Property NAMES must match the reader's mapping so values round-trip.
	for _, want := range []string{
		"http://www.pathozoom.com/szi",
		"VendorName", "Aperio",
		"ScannerName", "ScanScope CS",
		"ObjectiveMagnification", "40",
		"MicronsPerPixelX", "0.25",
		"MicronsPerPixelY",
		"ScannerSerialNo", "SN-12345",
		"TimeStart", "2024-01-15T09:30:00",
		"SoftwareName", "Aperio v12.1",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestScanPropsOmitsEmpty(t *testing.T) {
	md := source.Metadata{Make: "Aperio"} // others empty/zero
	var buf bytes.Buffer
	if err := WriteScanProperties(&buf, md); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if strings.Contains(s, "ScannerName") {
		t.Errorf("empty Model should be omitted; got:\n%s", s)
	}
	if strings.Contains(s, "ObjectiveMagnification") {
		t.Errorf("zero Magnification should be omitted; got:\n%s", s)
	}
	if strings.Contains(s, "TimeStart") {
		t.Errorf("zero time should be omitted; got:\n%s", s)
	}
	if !strings.Contains(s, "Aperio") {
		t.Errorf("present field missing; got:\n%s", s)
	}
}
