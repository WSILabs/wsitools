package source

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDecodedTile_LZWSource guards F1: source.Level.DecodedTile must decode a
// tiled-LZW source (Aperio ImageScope export) via opentile-go's level-decode,
// which carries the TIFF tile-dims + predictor context that standalone codec
// decode lacks. Before this, only JPEG/JP2K decoded in the transcode/hash paths.
func TestDecodedTile_LZWSource(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "svs", "590_crop_lzw_imagescope.tif")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no 590_crop_lzw fixture")
	}
	src, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()
	l0 := src.Levels()[0]
	if l0.Compression() != CompressionLZW {
		t.Skipf("fixture L0 is %v, expected lzw", l0.Compression())
	}
	img, err := l0.DecodedTile(0, 0)
	if err != nil {
		t.Fatalf("DecodedTile on tiled-LZW source: %v", err)
	}
	ts := l0.TileSize()
	if img.Width != ts.X || img.Height != ts.Y {
		t.Errorf("decoded tile = %dx%d, want %dx%d", img.Width, img.Height, ts.X, ts.Y)
	}
	if len(img.Pix) == 0 {
		t.Error("decoded tile has no pixels")
	}
}
