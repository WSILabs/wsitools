//go:build integration

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	_ "github.com/wsilabs/opentile-go/decoder/all"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// TestCropReadableWriterlessSource guards wsitools#32: crop / convert --rect must
// accept a readable-but-writerless SOURCE (BIF, IFE) as long as an explicit --to
// target is given — the source only needs to be *readable*. Previously the gate
// keyed off "does the source format have a matching writer" and rejected BIF/IFE
// outright. It also exercises the cropToSVS synthetic-Aperio path (a BIF source
// has no Aperio ImageDescription to mutate).
func TestCropReadableWriterlessSource(t *testing.T) {
	src := filepath.Join(testdir(t), "bif", "Ventana-1.bif")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)

	const rect = "0,0,4096,4096"
	// svs exercises the synthetic-Aperio-description path; tiff is the plain path.
	outs := map[string]string{}
	for _, target := range []string{"svs", "tiff"} {
		out := filepath.Join(t.TempDir(), "crop."+target)
		cmd := exec.Command(bin, "convert", "--to", target, "--rect", rect, "-f", "-o", out, src)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("convert --to %s --rect from BIF: %v\n%s", target, err, b)
		}
		outs[target] = out
	}

	// Both outputs must open, be the requested format + crop size, and decode to
	// IDENTICAL pixels (same region from the same source → same pixels regardless
	// of container).
	region := opentile.Region{Origin: opentile.Point{X: 512, Y: 512}, Size: opentile.Size{W: 512, H: 512}}
	var refPix []byte
	for _, target := range []string{"svs", "tiff"} {
		tlr, err := opentile.OpenFile(outs[target])
		if err != nil {
			t.Fatalf("open %s crop: %v", target, err)
		}
		l0 := tlr.Levels()[0]
		if l0.Size.W != 4096 || l0.Size.H != 4096 {
			t.Errorf("%s crop L0 = %dx%d, want 4096x4096", target, l0.Size.W, l0.Size.H)
		}
		lv, err := tlr.Pyramid(0).Level(0)
		if err != nil {
			t.Fatalf("%s level0: %v", target, err)
		}
		p, err := lv.ReadRegion(region, opentile.WithFormat(decoder.PixelFormatRGB))
		if err != nil {
			t.Fatalf("%s ReadRegion: %v", target, err)
		}
		if refPix == nil {
			refPix = append([]byte(nil), p.Pix...)
		} else if !bytes.Equal(refPix, p.Pix) {
			t.Errorf("%s crop pixels differ from the first target — crop region not consistent across containers", target)
		}
		tlr.Close()
	}
}
