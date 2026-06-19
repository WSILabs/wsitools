package main

import (
	"bytes"
	"image"
	"testing"

	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
)

func TestSpoolSinkAndSourceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	levels := []retile.LevelSpec{
		{Index: 0, Width: 512, Height: 512, Cols: 2, Rows: 2, TileW: 256, TileH: 256},
		{Index: 1, Width: 256, Height: 256, Cols: 1, Rows: 1, TileW: 256, TileH: 256},
	}
	md := source.Metadata{MPP: 0.5, Magnification: 20}
	sink, err := newSpoolTileSink(dir, levels)
	if err != nil {
		t.Fatal(err)
	}
	frames := map[[3]int][]byte{
		{0, 1, 1}: []byte("L0-11"), {0, 0, 0}: []byte("L0-00"),
		{0, 1, 0}: []byte("L0-10"), {0, 0, 1}: []byte("L0-01"),
		{1, 0, 0}: []byte("L1-00"),
	}
	for k, v := range frames {
		if err := sink.WriteTile(k[0], k[1], k[2], v); err != nil {
			t.Fatalf("WriteTile %v: %v", k, err)
		}
	}
	src := newSpoolSource(sink, "dicom", source.CompressionJPEG, md, nil)
	defer src.Close()

	if src.Format() != "dicom" || src.Metadata().MPP != 0.5 {
		t.Errorf("source format/md wrong: %q %v", src.Format(), src.Metadata())
	}
	lv := src.Levels()
	if len(lv) != 2 || lv[0].Size() != (image.Point{X: 512, Y: 512}) || lv[0].Compression() != source.CompressionJPEG {
		t.Fatalf("levels wrong: %d %v %v", len(lv), lv[0].Size(), lv[0].Compression())
	}
	buf := make([]byte, lv[0].TileMaxSize())
	n, err := lv[0].TileInto(1, 1, buf)
	if err != nil {
		t.Fatalf("TileInto: %v", err)
	}
	if !bytes.Equal(buf[:n], []byte("L0-11")) {
		t.Errorf("L0(1,1) = %q, want L0-11", buf[:n])
	}
}
