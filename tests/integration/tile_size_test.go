//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// cmuFixture returns the CMU-1-Small-Region.svs path, skipping if absent.
func cmuFixture(t *testing.T) string {
	t.Helper()
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	return src
}

// TestTileSizeOverride verifies `--tile-size N` re-tiles the output to N.
func TestTileSizeOverride(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.tiff")

	if o, err := runCLI(bin, "convert", "--to", "tiff", "--codec", "jpeg", "--tile-size", "512", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --tile-size 512: %v\n%s", err, o)
	}

	tlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile(out): %v", err)
	}
	defer tlr.Close()
	if got := tlr.Levels()[0].TileSize.W; got != 512 {
		t.Errorf("L0 TileSize.W = %d, want 512", got)
	}
}

// TestTileSizeDefaultMatchesSource is a regression guard against the old
// hardcoded 256: with no --tile-size (even under --factor), the output tiling
// must match the source's, not default to 256.
func TestTileSizeDefaultMatchesSource(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.tiff")

	if o, err := runCLI(bin, "convert", "--to", "tiff", "--codec", "jpeg", "--factor", "2", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --factor 2 (no --tile-size): %v\n%s", err, o)
	}

	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("opentile.OpenFile(src): %v", err)
	}
	defer srcTlr.Close()
	outTlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile(out): %v", err)
	}
	defer outTlr.Close()

	srcW := srcTlr.Levels()[0].TileSize.W
	if got := outTlr.Levels()[0].TileSize.W; got != srcW {
		t.Errorf("default L0 TileSize.W = %d, want source's %d (regression: must not be hardcoded 256)", got, srcW)
	}
}

// TestTileSizeSameAsSourceMatches requests the source tile size explicitly and
// confirms the output tiling equals the source's.
func TestTileSizeSameAsSourceMatches(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)

	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("opentile.OpenFile(src): %v", err)
	}
	srcW := srcTlr.Levels()[0].TileSize.W
	srcTlr.Close()

	out := filepath.Join(t.TempDir(), "out.svs")
	if o, err := runCLI(bin, "convert", "--to", "svs", "--tile-size", strconv.Itoa(srcW), "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to svs --tile-size %d: %v\n%s", srcW, err, o)
	}

	outTlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile(out): %v", err)
	}
	defer outTlr.Close()
	if got := outTlr.Levels()[0].TileSize.W; got != srcW {
		t.Errorf("L0 TileSize.W = %d, want source's %d", got, srcW)
	}
}

// TestTileSizeBIFErrors confirms `--to bif` rejects --tile-size with a guard
// message mentioning tile-size.
func TestTileSizeBIFErrors(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.bif")

	o, err := runCLI(bin, "convert", "--to", "bif", "--tile-size", "512", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected error for --to bif --tile-size, got success\n%s", o)
	}
	if !strings.Contains(o, "tile-size") {
		t.Errorf("error output does not mention tile-size:\n%s", o)
	}
}

// TestTileSizeIFEErrors: IFE is a fixed-256px-tile format, so --tile-size != 256
// is rejected (like --to bif).
func TestTileSizeIFEErrors(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.iris")

	o, err := runCLI(bin, "convert", "--to", "ife", "--tile-size", "512", "-f", "-o", out, src)
	if err == nil {
		t.Fatalf("expected error for --to ife --tile-size 512, got success\n%s", o)
	}
	if !strings.Contains(o, "tile-size") {
		t.Errorf("error output does not mention tile-size:\n%s", o)
	}
}

// TestTileSizeDICOMRowsColumns verifies --tile-size surfaces as the DICOM frame
// Rows/Columns (read back by opentile as the level tile size).
func TestTileSizeDICOMRowsColumns(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "outdir")

	if o, err := runCLI(bin, "convert", "--to", "dicom", "--tile-size", "512", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to dicom --tile-size 512: %v\n%s", err, o)
	}

	tlr, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("opentile.OpenFile(outdir): %v", err)
	}
	defer tlr.Close()
	if got := tlr.Levels()[0].TileSize.W; got != 512 {
		t.Errorf("DICOM L0 TileSize.W = %d, want 512", got)
	}
}

// TestTileSizeDZIManifest verifies --tile-size is written into the .dzi manifest.
func TestTileSizeDZIManifest(t *testing.T) {
	src := cmuFixture(t)
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.dzi")

	if o, err := runCLI(bin, "convert", "--to", "dzi", "--tile-size", "512", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to dzi --tile-size 512: %v\n%s", err, o)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read .dzi manifest: %v", err)
	}
	if !strings.Contains(string(b), `TileSize="512"`) {
		t.Errorf("manifest missing TileSize=\"512\":\n%s", b)
	}
}
