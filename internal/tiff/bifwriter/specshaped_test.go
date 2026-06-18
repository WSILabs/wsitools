package bifwriter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func TestSpecShapedOpensInOpentile(t *testing.T) {
	src, err := source.Open(filepath.Join(fixtureDir(t), "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Skipf("open source: %v", err)
	}
	defer src.Close()

	// If BIF_SPIKE_OUT is set, write there for manual viewer testing; else tmp.
	out := os.Getenv("BIF_SPIKE_OUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "specshaped.bif")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSpecShaped(f, FromLevel(src.Levels()[0]), IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
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

	// Pixel-identity over the pyramid level: the spec-shaped path builds the
	// pyramid IFD and the EncodeInfo Frame order independently, so verify the
	// serpentine placement survives there too (not just that the file opens).
	lvl := src.Levels()[0]
	gl := got.Levels()[0]
	cols := ceilDiv(lvl.Size().X, lvl.TileSize().X)
	rows := ceilDiv(lvl.Size().Y, lvl.TileSize().Y)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			want, err := lvl.DecodedTile(col, row)
			if err != nil {
				t.Fatalf("source DecodedTile(%d,%d): %v", col, row, err)
			}
			have, err := gl.DecodedTile(col, row)
			if err != nil {
				t.Fatalf("bif DecodedTile(%d,%d): %v", col, row, err)
			}
			if len(want.Pix) != len(have.Pix) {
				t.Fatalf("tile (%d,%d) pix len %d != %d", col, row, len(have.Pix), len(want.Pix))
			}
			for i := range want.Pix {
				if want.Pix[i] != have.Pix[i] {
					t.Fatalf("spec-shaped tile (%d,%d) pixel %d differs: src=%d bif=%d",
						col, row, i, want.Pix[i], have.Pix[i])
				}
			}
		}
	}
}
