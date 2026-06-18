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

// encodeInfoXMP builds the minimal level-0 <EncodeInfo Ver="2"> for a single
// AOI with no tile overlap: SlideInfo/AoiInfo with the tile grid, a
// SlideStitchInfo/ImageInfo, FrameInfo with one <Frame> per tile in serpentine
// (TILE_OFFSETS) order, and AoiOrigin (0,0). TileJointInfo overlaps are all 0
// (abutting tiles). Reader requires Ver>=2.
func encodeInfoXMP(cols, rows, tileW, tileH int) []byte {
	// Frame nodes declare the storage order of TILE_OFFSETS (whitepaper p.14):
	// Frame[k] = image (col,row) of the k-th stored tile. Real DP 200 is
	// ROW-MAJOR (Frame[k] = (k%cols, k/cols)), which is what we emit and store.
	var frames []byte
	for idx := 0; idx < cols*rows; idx++ {
		col, row := idx%cols, idx/cols
		frames = append(frames, []byte(fmt.Sprintf(
			`<Frame XY="%d,%d" Z="0" Focus="0"/>`, col, row))...)
	}

	// TileJointInfo: one per adjacent image-tile pair, mirroring REAL Roche
	// DP 200 exactly (verified against Ventana-1.bif): horizontal joins use
	// Direction "LEFT" with Tile1=left-column / Tile2=right-column neighbor;
	// vertical joins use "UP" with Tile1=lower-row / Tile2=upper-row(=row-1)
	// neighbor. Tile indices are 1-based serpentine numbers; OverlapX/OverlapY=0
	// (abutting tiles — a value real files also use). This is the spec/real-Roche
	// convention that opentile and bio-formats (the serpentine readers that read
	// genuine Roche) expect. NOTE: openslide only accepts "RIGHT"/"UP" and will
	// reject these "LEFT" joins — but openslide reads DP 200 tiles row-major and
	// cannot render real serpentine Roche slides anyway, so it is a metadata-only
	// oracle here; we do NOT distort the stitch graph to satisfy it.
	tile := func(col, row int) int { return row*cols + col + 1 } // 1-based row-major
	var joints []byte
	for row := 0; row < rows; row++ {
		for col := 0; col+1 < cols; col++ { // horizontal LEFT joins
			joints = append(joints, []byte(fmt.Sprintf(
				`<TileJointInfo FlagJoined="1" Confidence="100" Direction="LEFT" `+
					`Tile1="%d" Tile2="%d" OverlapX="0" OverlapY="0"/>`,
				tile(col, row), tile(col+1, row)))...)
		}
	}
	for col := 0; col < cols; col++ {
		for row := 1; row < rows; row++ { // vertical UP joins (tile2 one row up)
			joints = append(joints, []byte(fmt.Sprintf(
				`<TileJointInfo FlagJoined="1" Confidence="100" Direction="UP" `+
					`Tile1="%d" Tile2="%d" OverlapX="0" OverlapY="0"/>`,
				tile(col, row), tile(col, row-1)))...)
		}
	}

	return []byte(fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>`+
			`<EncodeInfo Ver="2">`+
			`<SlideInfo Rack="0" Slot="0" BaseName="wsitools">`+
			`<AoiInfo XIMAGESIZE="%d" YIMAGESIZE="%d" NumRows="%d" NumCols="%d" Pos-X="0" Pos-Y="0"/>`+
			`</SlideInfo>`+
			`<SlideStitchInfo>`+
			`<ImageInfo AOIScanned="1" AOIIndex="0" NumRows="%d" NumCols="%d" Width="%d" Height="%d" Pos-X="0" Pos-Y="0">`+
			`%s`+
			`<FrameInfo AOIScanned="1" AOIIndex="0">%s</FrameInfo>`+
			`</ImageInfo>`+
			`</SlideStitchInfo>`+
			`<AoiOrigin><AOI0 OriginX="0" OriginY="0"/></AoiOrigin>`+
			`</EncodeInfo>`,
		tileW, tileH, rows, cols, rows, cols, tileW, tileH, joints, frames))
}
