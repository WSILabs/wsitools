package bifwriter

import "fmt"

// IScanMeta carries the minimal scanner metadata Phase 0 emits in the <iScan>
// block. Magnification and ScanRes drive the reader's MPP/magnification; the
// rest are spec-mandated constants/placeholders.
type IScanMeta struct {
	Magnification int     // 20 or 40
	ScanRes       float64 // microns/pixel at level 0 (0.465 @20x, 0.25 @40x)
}

// iScanXMP builds the IFD-0 <iScan> XMP payload (tag 700). Wrapped in
// <Metadata> per the DP 200 (spec-compliant) layout. ScannerModel is the
// mandated literal "VENTANA DP 200"; UnitNumber is a synthetic >=2,000,000
// placeholder; Z-layers=1 (single focal plane).
func iScanXMP(m IScanMeta) []byte {
	return []byte(fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>`+
			`<Metadata><iScan Mode="brightfield" Magnification="%d" ScanRes="%g" `+
			`UnitNumber="2000515" ScannerModel="VENTANA DP 200" Z-layers="1" `+
			`Z-spacing="0" UserName="wsitools" BuildVersion="0.0.0.0" `+
			`BuildDate="1/1/2020 0:0:0 AM" ScanWhitePoint="255" Anonymization="1"/>`+
			`</Metadata>`,
		m.Magnification, m.ScanRes))
}
