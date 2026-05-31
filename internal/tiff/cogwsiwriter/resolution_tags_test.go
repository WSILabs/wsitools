package cogwsiwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

// TestCOGWSIEmitsResolution verifies that a cogwsiwriter closed with
// Metadata{MPPX, MPPY, Magnification} set emits XResolution (282),
// YResolution (283), ResolutionUnit (296), and the WSI MPP tags
// (65085 / 65086) in the L0 IFD.
func TestCOGWSIEmitsResolution(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "res.tiff")

	w, err := cogwsiwriter.Create(out, cogwsiwriter.Options{
		ToolsVersion: "test",
		Metadata: cogwsiwriter.Metadata{
			MPPX:          0.5,
			MPPY:          0.5,
			Magnification: 20,
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer w.Abort()

	h, err := w.AddLevel(cogwsiwriter.LevelSpec{
		ImageWidth:      8,
		ImageHeight:     8,
		TileWidth:       8,
		TileHeight:      8,
		Compression:     1, // none
		Photometric:     2, // RGB
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		IsL0:            true,
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}
	if err := h.WriteTile(0, 0, make([]byte, 8*8*3)); err != nil {
		t.Fatalf("WriteTile: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tiOut, _ := exec.Command("tiffinfo", out).CombinedOutput()
	got := strings.ToLower(string(tiOut))

	// XResolution/YResolution/ResolutionUnit should appear as "resolution" in tiffinfo.
	if !strings.Contains(got, "resolution") {
		t.Errorf("tiffinfo output missing resolution tags:\n%s", tiOut)
	}
	// WSI private MPP tags.
	for _, tag := range []string{"65085", "65086"} {
		if !strings.Contains(got, tag) {
			t.Errorf("tiffinfo output missing WSI tag %s:\n%s", tag, tiOut)
		}
	}
	// 0.5 µm/px → 20000 px/cm — verify the numeric value is present.
	if !strings.Contains(got, "20000") {
		t.Errorf("expected XResolution/YResolution 20000 px/cm for MPPX=MPPY=0.5:\n%s", tiOut)
	}
}
