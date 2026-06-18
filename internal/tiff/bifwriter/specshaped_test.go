package bifwriter

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

// TestSpecShapedRowMajorAndOpens: the two-IFD spec-shaped output (overview +
// EncodeInfo) opens as bif AND stores its pyramid tiles ROW-MAJOR. The
// spec-shaped path builds the pyramid IFD + EncodeInfo Frame order
// independently of WriteSingleLevel, so verify placement there too — via the
// reader-independent TILE_OFFSETS check (opentile's serpentine remap is buggy).
// If BIF_SPIKE_OUT is set, the artifact is written there for manual viewer
// testing (bio-formats / QuPath / openslide).
func TestSpecShapedRowMajorAndOpens(t *testing.T) {
	src, err := source.Open(filepath.Join(fixtureDir(t), "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Skipf("open source: %v", err)
	}
	defer src.Close()
	lvl := src.Levels()[0]

	out := os.Getenv("BIF_SPIKE_OUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "specshaped.bif")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSpecShaped(f, FromLevel(lvl), IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
		f.Close()
		t.Fatalf("WriteSpecShaped: %v", err)
	}
	f.Close()
	t.Logf("wrote spec-shaped BIF to %s", out)

	got, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen spec-shaped BIF: %v", err)
	}
	defer got.Close()
	if got.Format() != "bif" {
		t.Fatalf("detected %q, want bif", got.Format())
	}

	// Reader-independent row-major placement check on the pyramid IFD.
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
				t.Fatalf("spec-shaped tile (%d,%d) not at row-major index %d", col, row, row*cols+col)
			}
		}
	}
}
