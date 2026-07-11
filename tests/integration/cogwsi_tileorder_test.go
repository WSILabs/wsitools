//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	_ "github.com/wsilabs/opentile-go/decoder/all"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// TestCOGWSITileOrderRoundTrip guards wsitools#41: convert --to cog-wsi with a
// reordering tile strategy (hilbert/morton) used to produce corrupt, undecodable
// output — the finalize sized tile slots in raster order but placed tiles in
// emission order, so a reordered tile overran a slot sized for a different tile.
// Every order must now produce a decodable file, and (since reorder only changes
// on-disk tile placement, not the image) all orders must decode to identical
// pixels. The pre-#41 tileorder unit tests only covered the index math, not an
// end-to-end conversion — this closes that gap.
func TestCOGWSITileOrderRoundTrip(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)

	// A representative region read from each output; compared across orders.
	region := opentile.Region{Origin: opentile.Point{X: 256, Y: 256}, Size: opentile.Size{W: 512, H: 512}}
	var refPix []byte
	var refFile string
	for _, order := range []string{"row-major", "hilbert", "morton"} {
		out := filepath.Join(t.TempDir(), "out."+order+".cog.tiff")
		cmd := exec.Command(bin, "convert", "--to", "cog-wsi", "--tile-order", order, "-f", "-o", out, src)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("convert --tile-order %s: %v\n%s", order, err, b)
		}
		tlr, err := opentile.OpenFile(out)
		if err != nil {
			t.Fatalf("open %s output: %v", order, err)
		}
		lv, err := tlr.Pyramid(0).Level(0)
		if err != nil {
			t.Fatalf("%s level0: %v", order, err)
		}
		p, err := lv.ReadRegion(region, opentile.WithFormat(decoder.PixelFormatRGB))
		if err != nil {
			// This is exactly how #41 manifested: "two SOI markers" on decode.
			t.Fatalf("%s: decode failed (corrupt tiles?): %v", order, err)
		}
		if refPix == nil {
			refPix = append([]byte(nil), p.Pix...)
			refFile = order
		} else if string(refPix) != string(p.Pix) {
			t.Errorf("%s pixels differ from %s — reorder must not change the image", order, refFile)
		}
		tlr.Close()
	}
}
