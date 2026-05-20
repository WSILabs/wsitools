package cogwsi

import "testing"

func TestLayoutClassicTIFFTwoLevels(t *testing.T) {
	in := []levelLayoutInput{
		{TileBytes: []uint32{100, 100, 100, 100}, TileCount: 4, TileGeometry: tileGeom{TileW: 256, TileH: 256, ImgW: 512, ImgH: 512}, JPEGTables: nil},
		{TileBytes: []uint32{50, 50}, TileCount: 2, TileGeometry: tileGeom{TileW: 256, TileH: 256, ImgW: 256, ImgH: 512}, JPEGTables: nil},
	}
	plan, err := planLayout(layoutInput{
		Levels:     in,
		Associated: nil,
		BigTIFFMode: BigTIFFAuto,
	})
	if err != nil {
		t.Fatalf("planLayout: %v", err)
	}
	if plan.BigTIFF {
		t.Errorf("expected classic TIFF for tiny input, got BigTIFF")
	}
	// Smallest level (index 1) tile data must come before largest level (index 0).
	if plan.Levels[1].TileDataOffset >= plan.Levels[0].TileDataOffset {
		t.Errorf("reverse order: L1 tile data offset (%d) must be < L0 (%d)",
			plan.Levels[1].TileDataOffset, plan.Levels[0].TileDataOffset)
	}
	// All IFDs must be in the head block (before the first tile data byte).
	firstTile := plan.Levels[1].TileDataOffset
	for i, lv := range plan.Levels {
		if lv.IFDOffset >= firstTile {
			t.Errorf("level %d IFD offset %d not in head block (firstTile=%d)", i, lv.IFDOffset, firstTile)
		}
	}
	// Tile offsets aligned to 16.
	for i, lv := range plan.Levels {
		for j, off := range lv.TileOffsets {
			if off%16 != 0 {
				t.Errorf("level %d tile %d offset %d not 16-aligned", i, j, off)
			}
		}
	}
}

func TestLayoutPromotesToBigTIFF(t *testing.T) {
	// 3 GiB of fake tile bytes → must promote.
	one := uint32(1 << 20) // 1 MiB
	var tiles []uint32
	for i := 0; i < 3072; i++ {
		tiles = append(tiles, one)
	}
	in := []levelLayoutInput{{
		TileBytes:    tiles,
		TileCount:    uint32(len(tiles)),
		TileGeometry: tileGeom{TileW: 256, TileH: 256, ImgW: 65536, ImgH: 49152},
	}}
	plan, err := planLayout(layoutInput{Levels: in, BigTIFFMode: BigTIFFAuto})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.BigTIFF {
		t.Errorf("3 GiB input should promote to BigTIFF")
	}
}

func TestLayoutHonorsBigTIFFOverride(t *testing.T) {
	in := []levelLayoutInput{{TileBytes: []uint32{10}, TileCount: 1, TileGeometry: tileGeom{TileW: 8, TileH: 8, ImgW: 8, ImgH: 8}}}
	on, _ := planLayout(layoutInput{Levels: in, BigTIFFMode: BigTIFFOn})
	if !on.BigTIFF {
		t.Errorf("BigTIFFOn override ignored")
	}
	off, _ := planLayout(layoutInput{Levels: in, BigTIFFMode: BigTIFFOff})
	if off.BigTIFF {
		t.Errorf("BigTIFFOff override ignored")
	}
	// BigTIFF IFD record size > classic IFD record size for the same tag count.
	classicSize := ifdRecordSize(15, false)
	bigSize := ifdRecordSize(15, true)
	if bigSize <= classicSize {
		t.Errorf("BigTIFF IFD record size %d should exceed classic %d for same tag count", bigSize, classicSize)
	}
	// BigTIFF header should be 16 bytes vs 8 for classic.
	if on.HeaderSize != 16 {
		t.Errorf("BigTIFFOn HeaderSize: got %d want 16", on.HeaderSize)
	}
	if off.HeaderSize != 8 {
		t.Errorf("BigTIFFOff HeaderSize: got %d want 8", off.HeaderSize)
	}
}

func TestLayoutAssociatedImagesPlacedAfterPyramid(t *testing.T) {
	in := layoutInput{
		Levels: []levelLayoutInput{
			{TileBytes: []uint32{100, 100}, TileCount: 2, TileGeometry: tileGeom{TileW: 256, TileH: 256, ImgW: 512, ImgH: 256}},
		},
		Associated: []associatedLayoutInput{
			{Bytes: 5000, Width: 1024, Height: 512, Kind: "label"},
			{Bytes: 2000, Width: 512, Height: 256, Kind: "macro"},
		},
		BigTIFFMode: BigTIFFAuto,
	}
	plan, err := planLayout(in)
	if err != nil {
		t.Fatalf("planLayout: %v", err)
	}
	if len(plan.Associated) != 2 {
		t.Fatalf("plan.Associated length: got %d want 2", len(plan.Associated))
	}
	// Find the maximum pyramid tile offset.
	var maxPyramidOff uint64
	for _, lv := range plan.Levels {
		for _, off := range lv.TileOffsets {
			if off > maxPyramidOff {
				maxPyramidOff = off
			}
		}
	}
	for i, a := range plan.Associated {
		if a.DataOffset <= maxPyramidOff {
			t.Errorf("associated %d data offset %d must be > max pyramid tile offset %d", i, a.DataOffset, maxPyramidOff)
		}
		if a.IFDOffset >= plan.HeadBlockEnd {
			t.Errorf("associated %d IFD offset %d must be < HeadBlockEnd %d", i, a.IFDOffset, plan.HeadBlockEnd)
		}
		if a.DataOffset%16 != 0 {
			t.Errorf("associated %d data offset %d not 16-aligned", i, a.DataOffset)
		}
	}
}
