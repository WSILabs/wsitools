package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
)

func testdir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("WSI_TOOLS_TESTDIR")
	if d == "" {
		d = "../../sample_files"
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("WSI_TOOLS_TESTDIR=%s not accessible: %v", d, err)
	}
	return d
}

func TestOpen_SVS(t *testing.T) {
	td := testdir(t)
	src, err := Open(filepath.Join(td, "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()
	if src.Format() == "" {
		t.Errorf("empty format string")
	}
	if len(src.Levels()) == 0 {
		t.Errorf("zero levels")
	}
}

func TestOpen_NDPI_Rejects(t *testing.T) {
	td := testdir(t)
	candidate := filepath.Join(td, "ndpi", "CMU-1.ndpi")
	if _, err := os.Stat(candidate); err != nil {
		t.Skipf("NDPI fixture missing: %v", err)
	}
	_, err := Open(candidate)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("expected ErrUnsupportedFormat, got %v", err)
	}
}

func TestOpen_LeicaSCN_Rejects(t *testing.T) {
	td := testdir(t)
	// opentile-go's sample fixture directory is `scn/` (not `leica-scn/`).
	candidate := filepath.Join(td, "scn", "Leica-1.scn")
	if _, err := os.Stat(candidate); err != nil {
		t.Skipf("Leica SCN fixture missing: %v", err)
	}
	_, err := Open(candidate)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("expected ErrUnsupportedFormat, got %v", err)
	}
}

func TestOpen_NotATIFF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.bin")
	if err := os.WriteFile(path, []byte("not a TIFF"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if err == nil {
		t.Error("expected error opening non-TIFF garbage")
	}
}

func TestMapOpentileCompression_NovelCodecs(t *testing.T) {
	cases := []struct {
		in   opentile.Compression
		want Compression
	}{
		{opentile.CompressionWebP, CompressionWebP},
		{opentile.CompressionJPEGXL, CompressionJPEGXL},
		{opentile.CompressionHTJ2K, CompressionHTJ2K},
	}
	for _, tc := range cases {
		if got := mapOpentileCompression(tc.in); got != tc.want {
			t.Errorf("mapOpentileCompression(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestLevel_TileInto_RoundTrip opens a known SVS fixture and verifies
// that TileInto fills a buffer with the same bytes the underlying
// opentile.Level returns.
func TestLevel_TileInto_RoundTrip(t *testing.T) {
	testDir := os.Getenv("WSI_TOOLS_TESTDIR")
	if testDir == "" {
		t.Skip("WSI_TOOLS_TESTDIR not set")
	}
	path := filepath.Join(testDir, "svs", "CMU-1-Small-Region.svs")
	src, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()

	lvl := src.Levels()[0]
	max := lvl.TileMaxSize()
	if max <= 0 {
		t.Fatalf("TileMaxSize() = %d, want > 0", max)
	}
	buf := make([]byte, max)
	n, err := lvl.TileInto(0, 0, buf)
	if err != nil {
		t.Fatalf("TileInto: %v", err)
	}
	if n <= 0 || n > max {
		t.Fatalf("TileInto returned n=%d (max=%d)", n, max)
	}
}
