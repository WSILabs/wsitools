package streamwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// TestScaleTagsEmitted verifies the writer emits XResolution (282),
// YResolution (283), ResolutionUnit (296), and the WSI MPP/mag tags
// (65085/65086/65087) when MPP/magnification are set. Presence-checked
// via tiffinfo (value math is covered by tiff.MPPToResolution tests and
// the cmd integration tests).
func TestScaleTagsEmitted(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "scale.tiff")
	w, _ := streamwriter.Create(path, streamwriter.Options{
		BigTIFF:       tiff.BigTIFFOn,
		MPPX:          0.5,
		MPPY:          0.5,
		Magnification: 20,
	})
	l, _ := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: tiff.CompressionNone, Photometric: 2,
		WSIImageType: tiff.WSIImageTypePyramid,
	})
	l.WriteTile(0, 0, make([]byte, 8*8*3))
	w.Close()

	out, _ := exec.Command("tiffinfo", path).CombinedOutput()
	got := strings.ToLower(string(out))
	for _, want := range []string{"resolution", "65085", "65086", "65087"} {
		if !strings.Contains(got, want) {
			t.Errorf("tiffinfo output missing %q:\n%s", want, out)
		}
	}
}
