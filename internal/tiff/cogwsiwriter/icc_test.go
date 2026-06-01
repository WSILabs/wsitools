package cogwsiwriter_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

// writeL0 builds a 1-tile L0-only cog-wsi with the given Metadata and
// returns the output path. Fails the test on any writer error — notably
// Close, which is where a layout under-reservation would surface.
func writeL0(t *testing.T, md cogwsiwriter.Metadata) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "o.cog.tiff")
	w, err := cogwsiwriter.Create(out, cogwsiwriter.Options{
		ToolsVersion: "test",
		Metadata:     md,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer w.Abort()
	h, err := w.AddLevel(cogwsiwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: 1, Photometric: 2, SamplesPerPixel: 3,
		BitsPerSample: []uint16{8, 8, 8}, IsL0: true,
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
	return out
}

// TestCOGWSIEmitsICC: a 142 KB ICC profile is budgeted and emitted (tag
// 34675) without corrupting the pre-computed layout.
func TestCOGWSIEmitsICC(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	out := writeL0(t, cogwsiwriter.Metadata{ICCProfile: make([]byte, 141992)})
	tiOut, err := exec.Command("tiffinfo", out).CombinedOutput()
	if err != nil {
		t.Fatalf("tiffinfo failed (malformed file?): %v\n%s", err, tiOut)
	}
	got := strings.ToLower(string(tiOut))
	if !strings.Contains(got, "34675") && !strings.Contains(got, "icc profile") {
		t.Errorf("ICC tag 34675 not in cog-wsi output:\n%s", tiOut)
	}
}

// TestCOGWSILongImageDescription: an ImageDescription larger than the old
// fixed 2 KiB metadata reserve must not corrupt the layout. Under the old
// `external += 2048` guess this silently overran the next IFD.
func TestCOGWSILongImageDescription(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	out := writeL0(t, cogwsiwriter.Metadata{
		SourceImageDesc: strings.Repeat("x", 5000), // > 2 KiB; no ICC
	})
	// A well-formed file: tiffinfo parses it without error.
	if tiOut, err := exec.Command("tiffinfo", out).CombinedOutput(); err != nil {
		t.Fatalf("tiffinfo failed on long-ImageDescription output (corrupt layout?): %v\n%s", err, tiOut)
	}
}
