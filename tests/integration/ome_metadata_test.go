//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// TestConvertOMETIFFPreservesMagnification guards wsitools#27: the ome-tiff
// tile-copy and re-encode paths used the mag-less OME-XML synthesis, so
// magnification was dropped (info reported 0) even though the --factor/crop
// paths carried it. Both paths must now emit the <Instrument>/<Objective>
// block so opentile reads the source magnification back.
func TestConvertOMETIFFPreservesMagnification(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs") // 20x source
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"tile-copy", []string{"convert", "--to", "ome-tiff"}},
		{"reencode", []string{"convert", "--to", "ome-tiff", "--codec", "jpeg"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.ome.tiff")
			args := append(append([]string{}, tc.args...), "-f", "-o", out, src)
			if o, err := runCLI(bin, args...); err != nil {
				t.Fatalf("convert: %v\n%s", err, o)
			}
			sl, err := opentile.OpenFile(out)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer sl.Close()
			if m := sl.Metadata().Magnification; m != 20 {
				t.Errorf("ome-tiff (%s) magnification = %v, want 20 (source preserved)", tc.name, m)
			}
		})
	}
}
