//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// novelCodecs are the codecs that produce generic-TIFF output opentile-go can
// parse (compression-tag recognition) but does not decode.
var novelCodecs = []struct {
	name string
	want opentile.Compression
	// allowNonconformant marks codecs the conformance gate rejects for the TIFF
	// container (writable bytes opentile-go can't read back) — they need
	// --allow-nonconformant. Per containerCapabilities("tiff"), jpegxl is the
	// lone non-conformant novel codec; avif/webp/htj2k are conformant.
	allowNonconformant bool
}{
	{"jpegxl", opentile.CompressionJPEGXL, true},
	{"avif", opentile.CompressionAVIF, false},
	{"webp", opentile.CompressionWebP, false},
	{"htj2k", opentile.CompressionHTJ2K, false},
}

// TestConvertTIFF_NovelCodecs re-encodes CMU-1-Small-Region.svs to generic-TIFF
// with each novel codec via `convert --to tiff --codec X` and verifies opentile-go
// recognizes the output's format, per-level compression tag, tile size, and
// magnification round-trip. (Restores the CLI coverage dropped when the removed
// `transcode` command's integration tests were deleted.)
func TestConvertTIFF_NovelCodecs(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)

	// Source geometry/metadata for round-trip assertions.
	srcTlr, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("opentile.OpenFile(src): %v", err)
	}
	srcLevels := srcTlr.Levels()
	if len(srcLevels) == 0 {
		t.Fatalf("source has no levels")
	}
	wantTileSize := srcLevels[0].TileSize
	wantMag := srcTlr.Metadata().Magnification
	srcTlr.Close()

	for _, c := range novelCodecs {
		c := c
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.tiff")
			args := []string{"convert", "--to", "tiff", "--codec", c.name}
			if c.allowNonconformant {
				args = append(args, "--allow-nonconformant")
			}
			args = append(args, "-o", out, src)
			cmd := exec.Command(bin, args...)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("convert --to tiff --codec %s: %v\n%s", c.name, err, b)
			}
			validateNovelCodecTIFF(t, out, c.want, wantTileSize, wantMag)
		})
	}

	// The jpeg codec produces standard JPEG tiles opentile-go can re-open;
	// round-trip to verify generic-TIFF integrity.
	t.Run("jpeg", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "out.tiff")
		cmd := exec.Command(bin, "convert", "--to", "tiff", "--codec", "jpeg", "-o", out, src)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("convert --to tiff --codec jpeg: %v\n%s", err, b)
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

// TestConvertTIFF_BigTIFFStreaming converts the multi-GB BigTIFF fixture with
// webp (smallest novel-codec footprint) and asserts it completes without OOM —
// exercising the streaming tile path.
func TestConvertTIFF_BigTIFFStreaming(t *testing.T) {
	td := testdir(t)
	src := filepath.Join(td, "svs", "svs_40x_bigtiff.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("BigTIFF fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "out.tiff")
	cmd := exec.Command(bin, "convert", "--to", "tiff", "--codec", "webp", "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert BigTIFF --codec webp: %v\n%s", err, b)
	}
}

// validateNovelCodecTIFF re-opens a converted generic-TIFF and asserts
// compression-tag recognition + geometry + magnification round-trip.
func validateNovelCodecTIFF(t *testing.T, outPath string, wantCompression opentile.Compression, wantTileSize opentile.Size, wantMag float64) {
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
		t.Errorf("L0 Compression = %v, want %v", got, wantCompression)
	}
	if got := levels[0].TileSize; got != wantTileSize {
		t.Errorf("L0 TileSize = %v, want %v", got, wantTileSize)
	}
	if md := tlr.Metadata(); md.Magnification != wantMag {
		t.Errorf("Metadata.Magnification = %v, want %v", md.Magnification, wantMag)
	}
}
