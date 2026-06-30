package bifwriter

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

// TestWritePyramidRowMajorAndOpens: WritePyramid emits a BIF that opens as bif
// and stores each level's tiles ROW-MAJOR. Uses the source's real level(s) plus
// a tiny synthetic overview.
func TestWritePyramidRowMajorAndOpens(t *testing.T) {
	src, err := source.Open(filepath.Join(fixtureDir(t), "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Skipf("open source: %v", err)
	}
	defer src.Close()
	lvl := src.Levels()[0]

	ov := Overview{W: 4, H: 4, RGB: make([]byte, 4*4*3)}
	levels := []PyramidLevel{{Src: FromLevel(lvl), Mag: 20}}

	out := filepath.Join(t.TempDir(), "pyr.bif")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePyramid(f, levels, ov, IScanMeta{Magnification: 20, ScanRes: 0.499}, nil); err != nil {
		f.Close()
		t.Fatalf("WritePyramid: %v", err)
	}
	f.Close()

	got, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer got.Close()
	if got.Format() != "bif" {
		t.Fatalf("detected %q, want bif", got.Format())
	}

	// Reader-independent row-major placement check on the level-0 pyramid IFD.
	bl := parseBIFLevel(t, out)
	cols := ceilDivT(lvl.Size().X, lvl.TileSize().X)
	rows := ceilDivT(lvl.Size().Y, lvl.TileSize().Y)
	if len(bl.tileBytes) != cols*rows {
		t.Fatalf("tile count %d != %d", len(bl.tileBytes), cols*rows)
	}
	buf := make([]byte, lvl.TileMaxSize())
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			n, err := lvl.TileInto(col, row, buf)
			if err != nil {
				t.Fatalf("source TileInto(%d,%d): %v", col, row, err)
			}
			if !bytes.Equal(bl.tileBytes[row*cols+col], buf[:n]) {
				t.Fatalf("level-0 tile (%d,%d) not at row-major index %d", col, row, row*cols+col)
			}
		}
	}
}

func TestWritePyramidRejectsBadOverview(t *testing.T) {
	levels := []PyramidLevel{{Src: fakeLevel{}, Mag: 40}}
	var w bufAt
	err := WritePyramid(&w, levels, Overview{W: 4, H: 4, RGB: []byte{1, 2, 3}}, IScanMeta{}, nil)
	if err == nil {
		t.Fatal("expected error for mismatched overview RGB length")
	}
}

func TestWritePyramidRejectsNoLevels(t *testing.T) {
	var w bufAt
	if err := WritePyramid(&w, nil, Overview{}, IScanMeta{}, nil); err == nil {
		t.Fatal("expected error for zero levels")
	}
}
