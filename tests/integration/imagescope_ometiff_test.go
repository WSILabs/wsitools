//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// svsFixture returns a path under svs/ in the test pool, or skips.
func svsFixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(testdir(t), "svs", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no fixture %s", name)
	}
	return p
}

// TestImageScope_TiledLZWDecodes guards opentile-go v0.41.1 #28: tiled
// LZW/uncompressed/Deflate levels (ImageScope SVS/TIFF exports) now decode.
// Reading an L0 region of a tiled-LZW ImageScope export used to error
// "Dst is required"; region decodes the tile to PNG.
func TestImageScope_TiledLZWDecodes(t *testing.T) {
	bin := buildOnce(t)
	src := svsFixture(t, "590_crop_lzw_imagescope.tif")
	out := filepath.Join(t.TempDir(), "lzw_region.png")
	if o, err := runCLI(bin, "region", "--x", "0", "--y", "0", "--w", "256", "--h", "256", "--level", "0", "-o", out, src); err != nil {
		t.Fatalf("region on tiled-LZW ImageScope export: %v\n%s", err, o)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Errorf("region produced no PNG (err=%v)", err)
	}
}

// TestImageScope_NonJPEGAssociatedDecodes guards opentile-go v0.41.1 #29:
// SVS associated images in a non-JPEG codec (ImageScope re-exports thumbnail/
// label/overview to match the pyramid codec) now decode. An uncompressed
// thumbnail used to error "Could not determine subsampling — corrupt input"
// in the stripped-JPEG reassembler; extract now dispatches by Compression tag.
func TestImageScope_NonJPEGAssociatedDecodes(t *testing.T) {
	bin := buildOnce(t)
	src := svsFixture(t, "590_crop_none_imagescope.tif")
	out := filepath.Join(t.TempDir(), "thumb.png")
	if o, err := runCLI(bin, "extract", "--type", "thumbnail", "-o", out, src); err != nil {
		t.Fatalf("extract non-JPEG (uncompressed) thumbnail: %v\n%s", err, o)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Errorf("extract produced no PNG (err=%v)", err)
	}
}

// TestOMETIFF_CMU_Downsample exercises the OME-TIFF format-preserving downsample
// path in CI using the small CC0 CMU-1-Small-Region.ome.tiff fixture (added to
// wsi-fixtures v7). Previously OME-TIFF paths only had the large local-only
// Leica fixtures, so they skipped in CI (survey D3).
func TestOMETIFF_CMU_Downsample(t *testing.T) {
	bin := buildOnce(t)
	src := filepath.Join(testdir(t), "ome-tiff", "CMU-1-Small-Region.ome.tiff")
	if _, err := os.Stat(src); err != nil {
		t.Skip("no ome-tiff fixture")
	}
	out := filepath.Join(t.TempDir(), "down.ome.tiff")
	if o, err := runCLI(bin, "downsample", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("downsample --factor 2 <ome-tiff>: %v\n%s", err, o)
	}
	info, _ := runCLI(bin, "info", out)
	if !contains(info, "Format:  ome-tiff") {
		t.Errorf("expected ome-tiff output (format-preserving):\n%s", info)
	}
}

// contains is a tiny substring helper (some test files in this package define
// their own; this one is local to avoid collisions if they are build-tag gated).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
