//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// v02Codecs lists the 4 novel codecs added in v0.2 that produce generic-TIFF
// outputs. opentile-go v0.14 recognises all four compression tags, so we can
// validate via a proper opentile-go round-trip instead of shelling out to
// tiffinfo.
var v02Codecs = []struct {
	name            string
	wantCompression opentile.Compression
}{
	{"jpegxl", opentile.CompressionJPEGXL},
	{"avif", opentile.CompressionAVIF},
	{"webp", opentile.CompressionWebP},
	{"htj2k", opentile.CompressionHTJ2K},
}

func TestTranscode_PerCodec_CMU1Small(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)

	// Read source properties once so per-codec assertions can compare
	// against actual source values rather than hardcoded constants.
	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("opentile.OpenFile(src): %v", err)
	}
	srcLevels := srcTlr.Levels()
	if len(srcLevels) == 0 {
		t.Fatalf("source has no levels: %s", src)
	}
	srcTileSize := srcLevels[0].TileSize
	srcMag := srcTlr.Metadata().Magnification
	srcTlr.Close()

	for _, c := range v02Codecs {
		c := c
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.tiff")
			cmd := exec.Command(bin, "transcode", "--codec", c.name, "-o", out, src)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("transcode --codec %s: %v\n%s", c.name, err, b)
			}
			validateNovelCodecOutput(t, out, c.wantCompression, srcTileSize, srcMag)
		})
	}

	// Bonus: the v0.1 jpeg codec produces standard JPEG tiles that opentile-go
	// CAN re-open; round-trip via opentile-go to verify SVS-shape integrity.
	t.Run("jpeg", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "out.svs")
		cmd := exec.Command(bin, "transcode", "--codec", "jpeg", "-o", out, src)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("transcode --codec jpeg: %v\n%s", err, b)
		}
		tlr, err := opentile.OpenFile(out)
		if err != nil {
			t.Fatalf("opentile.OpenFile(out): %v", err)
		}
		defer tlr.Close()
		if len(tlr.Levels()) == 0 {
			t.Errorf("output has no levels")
		}
	})
}

func TestTranscode_PerSourceFormat_ToJPEGXL(t *testing.T) {
	td := testdir(t)
	bin := buildOnce(t)

	cases := []struct {
		name   string
		path   string
		wantOK bool
	}{
		{"svs", filepath.Join(td, "svs", "CMU-1-Small-Region.svs"), true},
		{"philips", filepath.Join(td, "philips-tiff", "Philips-1.tiff"), true},
		{"ome-tiled", filepath.Join(td, "ome-tiff", "Leica-1.ome.tiff"), true},
		{"bif", filepath.Join(td, "bif", "Ventana-1.bif"), true},
		{"ife", filepath.Join(td, "ife", "cervix_2x_jpeg.iris"), true},
		{"generic-tiff", filepath.Join(td, "generic-tiff", "CMU-1.tiff"), true},
		{"ndpi-rejection", filepath.Join(td, "ndpi", "CMU-1.ndpi"), false},
		{"leica-scn-rejection", filepath.Join(td, "scn", "Leica-1.scn"), false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, err := os.Stat(c.path); err != nil {
				t.Skipf("fixture missing: %v", err)
			}
			out := filepath.Join(t.TempDir(), "out.tiff")
			cmd := exec.Command(bin, "transcode", "--codec", "jpegxl", "-o", out, c.path)
			b, err := cmd.CombinedOutput()
			if c.wantOK && err != nil {
				t.Fatalf("expected OK, got: %v\n%s", err, b)
			}
			if !c.wantOK && err == nil {
				t.Fatalf("expected failure for %s, transcode succeeded", c.name)
			}
			if !c.wantOK {
				if !strings.Contains(string(b), "format unsupported") &&
					!strings.Contains(string(b), "ErrUnsupportedFormat") {
					t.Errorf("expected sanity-gate failure mention; got:\n%s", b)
				}
			}
		})
	}
}

func TestTranscode_BigTIFFFixture(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "svs_40x_bigtiff.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("BigTIFF fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.tiff")
	// Use webp for BigTIFF: smallest codec footprint, still tile-by-tile streaming.
	cmd := exec.Command(bin, "transcode", "--codec", "webp", "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("transcode of 4.8 GB BigTIFF failed: %v\n%s", err, b)
	}
	// If we got here without OOM, streaming works.
}

// validateNovelCodecOutput re-opens a transcoded output via opentile-go
// (v0.14+) and asserts compression-tag recognition + ImageDescription
// metadata round-trip. The 4 novel codecs (WebP, JPEGXL, AVIF, HTJ2K)
// produce generic-TIFF outputs that v0.14 can parse but does not
// decode — assertions stay at the metadata layer, not the tile-pixel
// layer.
func validateNovelCodecOutput(t *testing.T, outPath string, wantCompression opentile.Compression, wantTileSize opentile.Size, wantMag float64) {
	t.Helper()
	tlr, err := opentile.OpenFile(outPath)
	if err != nil {
		t.Fatalf("opentile.OpenFile(%s): %v", outPath, err)
	}
	defer tlr.Close()

	if got := tlr.Format(); got != opentile.FormatGenericTIFF {
		t.Errorf("Format() = %v, want %v", got, opentile.FormatGenericTIFF)
	}
	levels := tlr.Levels()
	if len(levels) == 0 {
		t.Fatalf("no levels in %s", outPath)
	}
	if got := levels[0].Compression; got != wantCompression {
		t.Errorf("L0 Compression() = %v, want %v", got, wantCompression)
	}
	if got := levels[0].TileSize; got != wantTileSize {
		t.Errorf("L0 TileSize() = %v, want %v", got, wantTileSize)
	}

	md := tlr.Metadata()
	if md.Magnification != wantMag {
		t.Errorf("Metadata.Magnification = %v, want %v", md.Magnification, wantMag)
	}
	if md.AcquisitionDateTime.IsZero() {
		t.Errorf("Metadata.AcquisitionDateTime is zero — wsitools ImageDescription parser failed?")
	}
}
