package cogwsi_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cornish/wsitools/internal/cogwsi"
)

func TestPackageCompiles(t *testing.T) {
	var _ *cogwsi.Writer
}

func TestWriterCreateAndSpoolLevel(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")

	w, err := cogwsi.Create(out, cogwsi.Options{
		ToolsVersion: "test",
		BigTIFF:      cogwsi.BigTIFFAuto,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer w.Abort()

	h, err := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth:      8,
		ImageHeight:     8,
		TileWidth:       8,
		TileHeight:      8,
		Compression:     1, // none
		Photometric:     2, // RGB
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		IsL0:            true,
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}
	if err := h.WriteTile(0, 0, []byte("xxxxxxxx")); err != nil {
		t.Fatalf("WriteTile: %v", err)
	}

	if _, err := os.Stat(out + ".spool/L0"); err != nil {
		t.Errorf("expected spool file: %v", err)
	}

	h2, err := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 16, ImageHeight: 8,
		TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h2.WriteTile(1, 0, []byte("xxxxxxxx")); err == nil {
		t.Errorf("expected error for out-of-order tile (1,0) before (0,0)")
	}
}

func TestWriterAbortRemovesEverything(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := cogwsi.Create(out, cogwsi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Abort()
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output file should be gone")
	}
	if _, err := os.Stat(out + ".spool"); !os.IsNotExist(err) {
		t.Errorf("spool dir should be gone")
	}
}

func TestWriterCloseProducesValidTIFF(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")

	w, err := cogwsi.Create(out, cogwsi.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}

	makeTile := func(b byte) []byte {
		t := make([]byte, 192) // 8*8*3
		for i := range t {
			t[i] = b
		}
		return t
	}
	h0, _ := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 16, ImageHeight: 16, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8}, IsL0: true,
	})
	for ty := uint32(0); ty < 2; ty++ {
		for tx := uint32(0); tx < 2; tx++ {
			if err := h0.WriteTile(tx, ty, makeTile(byte(ty*2+tx+1))); err != nil {
				t.Fatal(err)
			}
		}
	}
	h1, _ := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8},
	})
	if err := h1.WriteTile(0, 0, makeTile(99)); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 {
		t.Fatalf("output too short: %d bytes", len(data))
	}
	if data[0] != 'I' || data[1] != 'I' {
		t.Errorf("byte order: got %c%c want II", data[0], data[1])
	}
	if data[2] != 42 || data[3] != 0 {
		t.Errorf("TIFF version: got %d,%d want 42,0", data[2], data[3])
	}

	if _, err := os.Stat(out + ".spool"); !os.IsNotExist(err) {
		t.Errorf("spool dir should be removed after Close")
	}

	if !bytes.HasPrefix(data[8:], []byte("GDAL_STRUCTURAL_METADATA_SIZE=")) {
		t.Errorf("ghost area missing at offset 8")
	}

	ifd0 := binary.LittleEndian.Uint32(data[4:8])
	if ifd0 == 0 {
		t.Fatalf("IFD0 offset is zero")
	}
}

func TestWriterCloseReverseOrderTileData(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, _ := cogwsi.Create(out, cogwsi.Options{ToolsVersion: "test"})

	tile := bytes.Repeat([]byte{0xAA}, 192)
	h0, _ := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 16, ImageHeight: 16, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8}, IsL0: true,
	})
	for ty := uint32(0); ty < 2; ty++ {
		for tx := uint32(0); tx < 2; tx++ {
			_ = h0.WriteTile(tx, ty, tile)
		}
	}
	h1, _ := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8},
	})
	_ = h1.WriteTile(0, 0, tile)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	l0Off, l1Off, err := readTileOffsets(out)
	if err != nil {
		t.Fatal(err)
	}
	if l1Off >= l0Off {
		t.Errorf("L1 tile offset (%d) must be < L0 tile offset (%d) — reverse order", l1Off, l0Off)
	}
}

func TestWriterAbortAfterCloseLeavesOutputIntact(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := cogwsi.Create(out, cogwsi.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := w.AddLevel(cogwsi.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8}, IsL0: true,
	})
	if err := h.WriteTile(0, 0, []byte("xxxxxxxx")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	stat, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output missing after Close: %v", err)
	}
	preSize := stat.Size()
	// Calling Abort after a successful Close must be a no-op.
	_ = w.Abort()
	stat2, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output deleted by Abort-after-Close: %v", err)
	}
	if stat2.Size() != preSize {
		t.Fatalf("output size changed after Abort-after-Close: pre=%d post=%d", preSize, stat2.Size())
	}
}

func readTileOffsets(path string) (l0, l1 uint64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	ifd0 := uint64(binary.LittleEndian.Uint32(data[4:8]))
	l0, ifd1, err := firstTileOffset(data, ifd0)
	if err != nil {
		return 0, 0, err
	}
	if ifd1 == 0 {
		return 0, 0, fmt.Errorf("IFD1 missing")
	}
	l1, _, err = firstTileOffset(data, ifd1)
	return l0, l1, err
}

func firstTileOffset(data []byte, ifdOff uint64) (uint64, uint64, error) {
	n := uint64(binary.LittleEndian.Uint16(data[ifdOff : ifdOff+2]))
	entries := data[ifdOff+2 : ifdOff+2+n*12]
	var tileOffsetsOff uint64
	var tileOffsetsCount uint64
	for i := uint64(0); i < n; i++ {
		e := entries[i*12 : (i+1)*12]
		tag := binary.LittleEndian.Uint16(e[0:2])
		count := uint64(binary.LittleEndian.Uint32(e[4:8]))
		val := uint64(binary.LittleEndian.Uint32(e[8:12]))
		if tag == 324 {
			tileOffsetsOff = val
			tileOffsetsCount = count
		}
	}
	next := uint64(binary.LittleEndian.Uint32(data[ifdOff+2+n*12 : ifdOff+2+n*12+4]))
	if tileOffsetsCount == 0 {
		return 0, next, fmt.Errorf("no TileOffsets in IFD")
	}
	if tileOffsetsCount == 1 {
		return tileOffsetsOff, next, nil
	}
	return uint64(binary.LittleEndian.Uint32(data[tileOffsetsOff : tileOffsetsOff+4])), next, nil
}
