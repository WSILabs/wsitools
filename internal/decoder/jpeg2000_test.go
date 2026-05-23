package decoder

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

func TestJPEG2000Decoder(t *testing.T) {
	testdir := os.Getenv("WSI_TOOLS_TESTDIR")
	if testdir == "" {
		testdir = "../../sample_files"
	}
	src := filepath.Join(testdir, "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	tlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatal(err)
	}
	defer tlr.Close()
	l0 := tlr.Levels()[0]
	tile, err := l0.Tile(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tw, th := l0.TileSize().W, l0.TileSize().H

	d := NewJPEG2000()
	dst, err := d.DecodeTile(tile, nil, 1, 1)
	if err != nil {
		t.Fatalf("DecodeTile: %v", err)
	}
	if len(dst) != tw*th*3 {
		t.Errorf("decoded length: got %d, want %d", len(dst), tw*th*3)
	}
	// Sanity check: first 9 bytes should not all be zero (tile has tissue content)
	allZero := true
	for _, b := range dst[:9] {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Errorf("first 9 decoded bytes are all zero, expected tissue content")
	}
	t.Logf("tile size: %dx%d, decoded bytes: %d", tw, th, len(dst))
	t.Logf("first 9 RGB bytes: %v", dst[:9])
}
