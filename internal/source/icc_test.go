package source_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

// TestMetadataICCProfile: an SVS fixture with an embedded ICC profile
// surfaces it via Metadata().ICCProfile.
func TestMetadataICCProfile(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	path := filepath.Join(dir, "svs", "CMU-1.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	src, err := source.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()
	icc := src.Metadata().ICCProfile
	if len(icc) == 0 {
		t.Fatal("expected non-empty ICCProfile for CMU-1.svs")
	}
	if len(icc) != 141992 {
		t.Errorf("ICCProfile len = %d, want 141992", len(icc))
	}
}
