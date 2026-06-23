package ife

import (
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

func TestWriterBarePyramid(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.iris")

	w, err := Create(out, Options{Encoding: encJPEG, XExtent: 512, YExtent: 256, MPP: 0.25, Magnification: 20})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Two layers, added native-first. native 2x1 tiles (512x256); /2 = 1x1 (256x256).
	w.AddLevel(2, 1)
	w.AddLevel(1, 1)
	tile := solidTile(t)
	if err := w.WriteTile(0, 0, 0, tile); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteTile(0, 1, 0, tile); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteTile(1, 0, 0, tile); err != nil {
		t.Fatal(err)
	}
	if err := w.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile: %v", err)
	}
	defer sl.Close()
	if got := string(sl.Format()); got != "ife" {
		t.Errorf("format = %q, want ife", got)
	}
	levels := sl.Levels()
	if len(levels) != 2 {
		t.Fatalf("levels = %d, want 2", len(levels))
	}
	// PADDING QUIRK: dims = x_tiles*256 x y_tiles*256.
	if levels[0].Size.W != 512 || levels[0].Size.H != 256 {
		t.Errorf("L0 = %dx%d, want 512x256", levels[0].Size.W, levels[0].Size.H)
	}
	if levels[1].Size.W != 256 || levels[1].Size.H != 256 {
		t.Errorf("L1 = %dx%d, want 256x256", levels[1].Size.W, levels[1].Size.H)
	}
	md := sl.Metadata()
	if md.MPP.X != 0.25 {
		t.Errorf("MPP.X = %v, want 0.25", md.MPP.X)
	}
	if md.Magnification != 20 {
		t.Errorf("Magnification = %v, want 20", md.Magnification)
	}
}
