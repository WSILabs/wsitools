package streamwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// TestICCEmitted: a streamwriter given an ICC profile emits tag 34675 on
// L0; absent ICC emits nothing.
func TestICCEmitted(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	write := func(icc []byte) string {
		path := filepath.Join(t.TempDir(), "o.tiff")
		w, err := streamwriter.Create(path, streamwriter.Options{
			BigTIFF: tiff.BigTIFFOn, ICCProfile: icc,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		l, _ := w.AddLevel(streamwriter.LevelSpec{
			ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
			Compression: tiff.CompressionNone, Photometric: 2,
			WSIImageType: tiff.WSIImageTypePyramid,
		})
		l.WriteTile(0, 0, make([]byte, 8*8*3))
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		out, _ := exec.Command("tiffinfo", path).CombinedOutput()
		return strings.ToLower(string(out))
	}
	withICC := write(make([]byte, 5000))
	if !strings.Contains(withICC, "34675") && !strings.Contains(withICC, "iccprofile") && !strings.Contains(withICC, "icc profile") {
		t.Errorf("ICC tag 34675 not reported by tiffinfo:\n%s", withICC)
	}
	noICC := write(nil)
	if strings.Contains(noICC, "34675") || strings.Contains(noICC, "icc profile") {
		t.Errorf("unexpected ICC tag with nil profile:\n%s", noICC)
	}
}
