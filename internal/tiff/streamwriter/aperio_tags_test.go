package streamwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// TestImageDepthAndSubsamplingEmitted: a streamwriter given ImageDepth /
// YCbCrSubSampling emits tags 32997 / 530 on L0; the zero values emit
// nothing.
func TestImageDepthAndSubsamplingEmitted(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	write := func(depth uint32, sub []uint16) string {
		path := filepath.Join(t.TempDir(), "o.tiff")
		w, err := streamwriter.Create(path, streamwriter.Options{
			BigTIFF: tiff.BigTIFFOn, ImageDepth: depth, YCbCrSubSampling: sub,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		l, _ := w.AddLevel(streamwriter.LevelSpec{
			ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
			Compression: tiff.CompressionNone, Photometric: 2,
			SamplesPerPixel: 3, BitsPerSample: []uint16{8, 8, 8},
			WSIImageType: tiff.WSIImageTypePyramid,
		})
		l.WriteTile(0, 0, make([]byte, 8*8*3))
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		out, _ := exec.Command("tiffinfo", path).CombinedOutput()
		return strings.ToLower(string(out))
	}
	with := write(1, []uint16{2, 2})
	if !strings.Contains(with, "image depth") {
		t.Errorf("ImageDepth not reported by tiffinfo:\n%s", with)
	}
	if !strings.Contains(with, "ycbcr subsampling") {
		t.Errorf("YCbCrSubSampling not reported by tiffinfo:\n%s", with)
	}
	none := write(0, nil)
	if strings.Contains(none, "ycbcr subsampling") {
		t.Errorf("unexpected YCbCrSubSampling with zero values:\n%s", none)
	}
}
