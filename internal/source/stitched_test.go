package source

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/opentile-go/decoder"
)

// tightRGB returns a packed Width*Height*3 RGB buffer from im (stride padding
// removed) so two tiles compare on content, not layout.
func tightRGB(im *decoder.Image) []byte {
	rb := im.Width * 3
	out := make([]byte, rb*im.Height)
	for y := 0; y < im.Height; y++ {
		copy(out[y*rb:(y+1)*rb], im.Pix[y*im.Stride:y*im.Stride+rb])
	}
	return out
}

// TestStitchedDeOverlapsBIF verifies the de-overlapped display surface
// (opentile-go ≥ v0.50.0): for a stitched Ventana BIF, StitchedGrid is the
// canonical ceil(Size/TileSize) partition, and — the point of the surface —
// StitchedTile composites away the per-tile overlap so its pixels differ from the
// raw DecodedTile for at least one tile. (The tile COUNT can match the raw Grid;
// the overlap lives in pixel content, which is why `hash --mode pixel` must read
// the stitched surface to digest the real slide.) Fixture-gated (BIF is not in
// the CI pool; runs locally).
func TestStitchedDeOverlapsBIF(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "bif", "*.bif"))
	if len(matches) == 0 {
		t.Skip("no BIF fixture in the pool")
	}
	src, err := Open(matches[0])
	if err != nil {
		t.Fatalf("open %s: %v", matches[0], err)
	}
	defer src.Close()

	l0 := src.Levels()[0]
	if !l0.Overlapping() {
		t.Skipf("%s L0 is not overlapping", matches[0])
	}
	ol, ok := l0.(*opentileLevel)
	if !ok {
		t.Fatalf("expected *opentileLevel, got %T", l0)
	}

	sz, ts := l0.Size(), l0.TileSize()
	want := image.Point{X: (sz.X + ts.X - 1) / ts.X, Y: (sz.Y + ts.Y - 1) / ts.Y}
	if sg := ol.StitchedGrid(); sg != want {
		t.Errorf("StitchedGrid = %v, want ceil(Size/TileSize) = %v", sg, want)
	}

	// De-overlap must actually change pixels: scan the partition and require at
	// least one tile whose stitched pixels differ from the raw decoded tile.
	g := ol.StitchedGrid()
	differs := false
	for ty := 0; ty < g.Y && !differs; ty++ {
		for tx := 0; tx < g.X && !differs; tx++ {
			d, err := ol.DecodedTile(tx, ty)
			if err != nil {
				t.Fatalf("DecodedTile(%d,%d): %v", tx, ty, err)
			}
			s, err := ol.StitchedTile(tx, ty)
			if err != nil {
				t.Fatalf("StitchedTile(%d,%d): %v", tx, ty, err)
			}
			if d.Width != s.Width || d.Height != s.Height || !bytes.Equal(tightRGB(d), tightRGB(s)) {
				differs = true
			}
		}
	}
	if !differs {
		t.Error("StitchedTile produced identical pixels to DecodedTile on every tile — no de-overlap observed")
	}
}
