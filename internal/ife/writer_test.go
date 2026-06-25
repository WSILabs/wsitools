package ife

import (
	"bytes"
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
	// opentile-go ≥ v0.53.0 reads Level.Size as the true content extent: L0 from
	// TILE_TABLE.x_extent/y_extent (= Options.XExtent/YExtent = 512x256), and each
	// reduced level scaled from it (L1 = native ÷ 2 = 256x128) — not the
	// 256-padded tile-grid extent.
	if levels[0].Size.W != 512 || levels[0].Size.H != 256 {
		t.Errorf("L0 = %dx%d, want 512x256", levels[0].Size.W, levels[0].Size.H)
	}
	if levels[1].Size.W != 256 || levels[1].Size.H != 128 {
		t.Errorf("L1 = %dx%d, want 256x128 (native ÷2 content extent)", levels[1].Size.W, levels[1].Size.H)
	}
	md := sl.Metadata()
	if md.MPP.X != 0.25 {
		t.Errorf("MPP.X = %v, want 0.25", md.MPP.X)
	}
	if md.Magnification != 20 {
		t.Errorf("Magnification = %v, want 20", md.Magnification)
	}
}

// writeSinglePyramid writes a minimal 1-tile (256x256) IFE to out, calling cfg to
// set ICC/associated/attributes before Finalize.
func writeSinglePyramid(t *testing.T, out string, cfg func(*Writer)) {
	t.Helper()
	w, err := Create(out, Options{Encoding: encJPEG, XExtent: 256, YExtent: 256})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w.AddLevel(1, 1)
	if err := w.WriteTile(0, 0, 0, solidTile(t)); err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		cfg(w)
	}
	if err := w.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func TestWriterICCRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "icc.iris")
	icc := []byte("FAKE-ICC-PROFILE-BYTES-\x00\x01\x02\x03")
	writeSinglePyramid(t, out, func(w *Writer) { w.SetICCProfile(icc) })

	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile: %v", err)
	}
	defer sl.Close()
	if got := sl.ICCProfile(); !bytes.Equal(got, icc) {
		t.Errorf("ICCProfile = %q, want %q", got, icc)
	}
}

func TestWriterAssociatedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "assoc.iris")
	tile := solidTile(t)
	writeSinglePyramid(t, out, func(w *Writer) {
		w.AddAssociated("label", 256, 256, imgEncJPEG, tile)
	})

	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile: %v", err)
	}
	defer sl.Close()
	imgs := sl.AssociatedImages()
	if len(imgs) != 1 {
		t.Fatalf("associated images = %d, want 1", len(imgs))
	}
	if got := imgs[0].Type(); got != opentile.AssociatedLabel {
		t.Errorf("associated[0].Type = %q, want %q", got, opentile.AssociatedLabel)
	}
	if got := imgs[0].Size(); got.W != 256 || got.H != 256 {
		t.Errorf("associated[0].Size = %dx%d, want 256x256", got.W, got.H)
	}
}
