package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkIFDs_SVS(t *testing.T) {
	testDir := os.Getenv("WSI_TOOLS_TESTDIR")
	if testDir == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	path := filepath.Join(testDir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	ifds, err := WalkIFDs(path)
	if err != nil {
		t.Fatalf("WalkIFDs: %v", err)
	}
	if len(ifds) < 4 {
		t.Errorf("expected >= 4 IFDs in CMU-1-Small-Region.svs, got %d", len(ifds))
	}
	first := ifds[0]
	if first.Width == 0 || first.Height == 0 {
		t.Errorf("IFD 0 dimensions zero: %+v", first)
	}
	// CMU-1-Small-Region.svs L0 is JPEG-compressed (TIFF compression tag 7).
	if first.Compression != 7 {
		t.Errorf("IFD 0 Compression = %d, want 7 (JPEG)", first.Compression)
	}
	// L0 should be tiled (TileWidth/TileHeight non-zero).
	if first.TileWidth == 0 || first.TileHeight == 0 {
		t.Errorf("IFD 0 should be tiled: %+v", first)
	}
	// BitsPerSample(258)/SamplesPerPixel(277) must be extracted — the transform
	// entry guard (assertRGB8Source) relies on these to reject non-8-bit / non-RGB
	// sources instead of silently mis-tagging them. A genuine SVS is 8-bit RGB.
	if first.BitsPerSample != 8 {
		t.Errorf("IFD 0 BitsPerSample = %d, want 8", first.BitsPerSample)
	}
	if first.SamplesPerPixel != 3 {
		t.Errorf("IFD 0 SamplesPerPixel = %d, want 3", first.SamplesPerPixel)
	}
}

func TestWalkIFDs_BigTIFF(t *testing.T) {
	testDir := os.Getenv("WSI_TOOLS_TESTDIR")
	if testDir == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	// Use a BigTIFF fixture from the pool; pick one that exists.
	path := filepath.Join(testDir, "philips-tiff", "Philips-1.tiff")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	ifds, err := WalkIFDs(path)
	if err != nil {
		t.Fatalf("WalkIFDs: %v", err)
	}
	if len(ifds) == 0 {
		t.Fatalf("no IFDs walked")
	}
	for i, ifd := range ifds {
		if ifd.Width == 0 || ifd.Height == 0 {
			t.Errorf("IFD %d has zero dimensions: %+v", i, ifd)
		}
	}
}

func TestWalkIFDs_GenericTIFF(t *testing.T) {
	testDir := os.Getenv("WSI_TOOLS_TESTDIR")
	if testDir == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	path := filepath.Join(testDir, "generic-tiff", "CMU-1.tiff")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	ifds, err := WalkIFDs(path)
	if err != nil {
		t.Fatalf("WalkIFDs: %v", err)
	}
	if len(ifds) == 0 {
		t.Fatalf("no IFDs walked")
	}
}
