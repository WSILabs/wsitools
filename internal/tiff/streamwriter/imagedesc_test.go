package streamwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

func TestImageDescription(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "with-desc.tiff")
	desc := "Aperio Image Library v12.0.15\r\n8x8 [...] |MPP = 1.0 |AppMag = 20"
	w, _ := streamwriter.Create(path, streamwriter.Options{
		BigTIFF:          tiff.BigTIFFOn,
		ImageDescription: desc,
	})
	level, _ := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: tiff.CompressionNone, Photometric: 2,
	})
	level.WriteTile(0, 0, make([]byte, 8*8*3))
	w.Close()

	out, _ := exec.Command("tiffinfo", path).CombinedOutput()
	got := string(out)
	if !strings.Contains(got, "AppMag = 20") {
		t.Errorf("ImageDescription not in output:\n%s", got)
	}
}
