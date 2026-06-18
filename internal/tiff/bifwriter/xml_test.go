package bifwriter

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestIScanXMPWellFormedAndDetectable(t *testing.T) {
	blob := iScanXMP(IScanMeta{Magnification: 40, ScanRes: 0.25})
	// opentile detects BIF by this exact substring; it must be present.
	if !strings.Contains(string(blob), "<iScan") {
		t.Fatalf("iScan XMP missing the <iScan detection marker:\n%s", blob)
	}
	// Mandated constant the reader/spec requires (whitepaper Table 1b).
	if !strings.Contains(string(blob), `ScannerModel="VENTANA DP 200"`) {
		t.Errorf("missing ScannerModel=\"VENTANA DP 200\":\n%s", blob)
	}
	// Must be valid XML.
	var v any
	if err := xml.Unmarshal(blob, &v); err != nil {
		t.Errorf("iScan XMP is not well-formed XML: %v\n%s", err, blob)
	}
}
