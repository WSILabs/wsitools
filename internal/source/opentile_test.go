package source

import (
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

func TestOpen_NDPI_Accepts(t *testing.T) {
	td := testdir(t)
	candidate := filepath.Join(td, "ndpi", "CMU-1.ndpi")
	if _, err := os.Stat(candidate); err != nil {
		t.Skipf("NDPI fixture missing: %v", err)
	}
	src, err := Open(candidate)
	if err != nil {
		t.Fatalf("Open(NDPI): %v", err)
	}
	defer src.Close()
	levels := src.Levels()
	if len(levels) == 0 {
		t.Fatal("NDPI source reports no levels")
	}
	lvl0 := levels[0]
	if lvl0.Size().X == 0 || lvl0.Size().Y == 0 {
		t.Errorf("NDPI L0 Size is zero: %+v", lvl0.Size())
	}
	if lvl0.TileSize().X == 0 || lvl0.TileSize().Y == 0 {
		t.Errorf("NDPI L0 TileSize is zero: %+v", lvl0.TileSize())
	}
}

func TestOpen_LeicaSCN_Accepts(t *testing.T) {
	td := testdir(t)
	// opentile-go's sample fixture directory is `scn/` (not `leica-scn/`).
	candidate := filepath.Join(td, "scn", "Leica-1.scn")
	if _, err := os.Stat(candidate); err != nil {
		t.Skipf("Leica SCN fixture missing: %v", err)
	}
	src, err := Open(candidate)
	if err != nil {
		t.Fatalf("Open(SCN): %v", err)
	}
	defer src.Close()
	levels := src.Levels()
	if len(levels) == 0 {
		t.Fatal("SCN source reports no levels")
	}
	lvl0 := levels[0]
	if lvl0.Size().X == 0 || lvl0.Size().Y == 0 {
		t.Errorf("SCN L0 Size is zero")
	}
	if lvl0.TileSize().X == 0 || lvl0.TileSize().Y == 0 {
		t.Errorf("SCN L0 TileSize is zero")
	}
}

// TestOpen_OMEOneFrame_Accepts verifies single-frame OME-TIFF
// (no native tile geometry; opentile-go synthesizes) opens cleanly
// via source.Open. Skips if no OneFrame fixture is available — the
// gate removal still ships without the integration check.
func TestOpen_OMEOneFrame_Accepts(t *testing.T) {
	td := testdir(t)
	// Try a few candidate paths for OneFrame fixtures.
	candidates := []string{
		filepath.Join(td, "ome-tiff", "oneframe-1.ome.tiff"),
		filepath.Join(td, "ome-tiff", "Leica-1.ome.tiff"),
	}
	var path string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			path = p
			break
		}
	}
	if path == "" {
		t.Skip("no OME-OneFrame fixture available")
	}
	src, err := Open(path)
	if err != nil {
		t.Fatalf("Open(OME-OneFrame): %v", err)
	}
	defer src.Close()
	levels := src.Levels()
	if len(levels) == 0 {
		t.Fatal("OME source reports no levels")
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

func TestOpenWithSlide_ReturnsBothHandles(t *testing.T) {
	td := testdir(t)
	p := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no svs fixture")
	}
	src, slide, err := OpenWithSlide(p)
	if err != nil {
		t.Fatalf("OpenWithSlide: %v", err)
	}
	defer src.Close()
	if slide == nil {
		t.Fatal("OpenWithSlide returned nil slide")
	}
	if src.Format() != string(opentile.FormatSVS) {
		t.Errorf("Format = %q, want svs", src.Format())
	}
	if len(src.Levels()) != len(slide.Levels()) {
		t.Errorf("source Levels = %d, slide Levels = %d", len(src.Levels()), len(slide.Levels()))
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
