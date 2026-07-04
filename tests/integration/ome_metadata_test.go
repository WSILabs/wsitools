//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
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

// TestConvertOMETIFFIdentityFields guards the wsitools#27 remainder: the ome-tiff
// OME-XML now carries the source acquisition date (which round-trips through
// opentile) and the scanner identity in a standard <Microscope> element (read by
// Bio-Formats/QuPath; opentile's reader doesn't surface make/model yet).
func TestConvertOMETIFFIdentityFields(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	ssl, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	wantDT := ssl.Metadata().AcquisitionDateTime
	ssl.Close()

	out := filepath.Join(t.TempDir(), "out.ome.tiff")
	if o, err := runCLI(bin, "convert", "--to", "ome-tiff", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert: %v\n%s", err, o)
	}
	// AcquisitionDate round-trips through opentile (full time, not midnight).
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sl.Close()
	if !wantDT.IsZero() {
		if got := sl.Metadata().AcquisitionDateTime; !got.Equal(wantDT) {
			t.Errorf("ome-tiff datetime = %v, want %v", got, wantDT)
		}
	}
	// A standard <Microscope Manufacturer=...> element is emitted (source is Aperio).
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(b), `<Microscope Manufacturer="Aperio"`) {
		t.Errorf("OME-XML missing <Microscope Manufacturer=\"Aperio\"> (scanner identity)")
	}
}
