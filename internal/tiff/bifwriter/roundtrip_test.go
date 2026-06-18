package bifwriter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("WSI_TOOLS_TESTDIR")
	if d == "" {
		d = "../../../sample_files"
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("fixtures unavailable (%s): %v", d, err)
	}
	return d
}

// TestRoundTripPixelIdentical: write a BIF from a small SVS level, reopen it via
// opentile (the BIF reader), and assert every tile decodes to the same pixels as
// the source level. A wrong serpentine mapping scrambles tiles and fails here.
func TestRoundTripPixelIdentical(t *testing.T) {
	src, err := source.Open(filepath.Join(fixtureDir(t), "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Skipf("open source: %v", err)
	}
	defer src.Close()
	lvl := src.Levels()[0]

	out := filepath.Join(t.TempDir(), "spike.bif")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSingleLevel(f, FromLevel(lvl), IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
		f.Close()
		t.Fatalf("WriteSingleLevel: %v", err)
	}
	f.Close()

	// Reopen through wsitools' source layer (opentile under the hood).
	got, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen written BIF: %v", err)
	}
	defer got.Close()
	if got.Format() != "bif" {
		t.Fatalf("written file detected as %q, want bif", got.Format())
	}
	gl := got.Levels()[0]
	if gl.Size() != lvl.Size() {
		t.Fatalf("level size %v != source %v", gl.Size(), lvl.Size())
	}

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
					t.Fatalf("tile (%d,%d) pixel %d differs: src=%d bif=%d (serpentine mismatch?)",
						col, row, i, want.Pix[i], have.Pix[i])
				}
			}
		}
	}
}
