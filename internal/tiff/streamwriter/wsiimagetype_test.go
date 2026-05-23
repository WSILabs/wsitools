package streamwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

func TestWSIImageType_PyramidAndAssociated(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "wsi-tags.tiff")

	w, _ := streamwriter.Create(path, streamwriter.Options{BigTIFF: tiff.BigTIFFOn})
	// Two pyramid levels; level count should land in TagWSILevelCount=2.
	for _, dims := range [][2]uint32{{16, 16}, {8, 8}} {
		l, _ := w.AddLevel(streamwriter.LevelSpec{
			ImageWidth: dims[0], ImageHeight: dims[1],
			TileWidth: 8, TileHeight: 8,
			Compression: tiff.CompressionNone, Photometric: 2,
			WSIImageType: tiff.WSIImageTypePyramid,
		})
		tx := (dims[0] + 7) / 8
		ty := (dims[1] + 7) / 8
		for y := uint32(0); y < ty; y++ {
			for x := uint32(0); x < tx; x++ {
				l.WriteTile(x, y, make([]byte, 8*8*3))
			}
		}
	}
	w.AddStripped(streamwriter.StrippedSpec{
		WSIImageType:   tiff.WSIImageTypeLabel,
		StripBytes:     make([]byte, 4*4*3),
		Width:          4, Height: 4,
		Compression:    tiff.CompressionNone,
		Photometric:    2,
		NewSubfileType: 1,
	})
	w.Close()

	out, _ := exec.Command("tiffinfo", path).CombinedOutput()
	got := string(out)
	if !strings.Contains(got, "65080") && !strings.Contains(got, "FE18") && !strings.Contains(got, "0xfe18") {
		t.Errorf("WSIImageType (tag 65080) not in tiffinfo output:\n%s", got)
	}
	if !strings.Contains(got, "65082") && !strings.Contains(got, "FE1A") && !strings.Contains(got, "0xfe1a") {
		t.Errorf("WSILevelCount (tag 65082) not in tiffinfo output:\n%s", got)
	}
}
