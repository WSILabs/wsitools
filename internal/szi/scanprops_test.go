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
		SerialNumber:        "SN-12345",
		AcquisitionDateTime: time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC),
		Software:            "Aperio v12.1",
	}
	var buf bytes.Buffer
	if err := WriteScanProperties(&buf, md); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, want := range []string{
		"ScannerManufacturer",
		"Aperio",
		"ScannerModel",
		"ScanScope CS",
		"Magnification",
		"40",
		"ScannerSerial",
		"SN-12345",
		"AcquisitionDateTime",
		"2024-01-15T09:30:00Z",
		"ScannerSoftware",
		"Aperio v12.1",
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
	if strings.Contains(s, "ScannerModel") {
		t.Errorf("empty Model should be omitted; got:\n%s", s)
	}
	if strings.Contains(s, "Magnification") {
		t.Errorf("zero Magnification should be omitted; got:\n%s", s)
	}
	if strings.Contains(s, "AcquisitionDateTime") {
		t.Errorf("zero time should be omitted; got:\n%s", s)
	}
	if !strings.Contains(s, "Aperio") {
		t.Errorf("present field missing; got:\n%s", s)
	}
}
