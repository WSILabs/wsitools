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

// TestCropFactorCOGWSISkipsUnsupportedAssociated guards wsitools#36: converting a
// source that carries an associated type cog-wsi doesn't accept (a Ventana BIF's
// "probability" image) into cog-wsi via a TRANSFORM path (--factor / crop) must
// skip the unsupported image with a warning — matching the plain convert path —
// not abort the whole conversion. The transform paths previously returned the
// writer's ErrInvalidAssocType verbatim; they now route through the shared
// skip-with-warning helper.
func TestCropFactorCOGWSISkipsUnsupportedAssociated(t *testing.T) {
	src := filepath.Join(testdir(t), "bif", "Ventana-1.bif")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %s", src)
	}
	bin := buildOnce(t)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"crop", []string{"convert", "--to", "cog-wsi", "--rect", "0,0,8192,8192"}},
		{"factor", []string{"convert", "--to", "cog-wsi", "--rect", "0,0,8192,8192", "--factor", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.cog.tiff")
			args := append(append([]string{}, tc.args...), "-f", "-o", out, src)
			if b, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
				t.Fatalf("convert %s: %v\n%s", tc.name, err, b)
			}
			tlr, err := opentile.OpenFile(out)
			if err != nil {
				t.Fatalf("open output: %v", err)
			}
			defer tlr.Close()
			// The unsupported "probability" image must be absent; the cog-wsi-valid
			// ones (overview/label) survive — proving we skipped, not dropped-all.
			var kept int
			for _, a := range tlr.AssociatedImages() {
				if string(a.Type()) == "probability" {
					t.Errorf("%s: cog-wsi output unexpectedly contains a 'probability' associated image", tc.name)
				}
				kept++
			}
			if kept == 0 {
				t.Errorf("%s: no associated images survived — expected overview/label kept", tc.name)
			}
		})
	}
}
