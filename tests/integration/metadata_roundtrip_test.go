//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
)

// TestConvertDICOMPreservesMagnification guards wsitools#30: the DICOM writer
// never wrote ObjectiveLensPower (0048,0112), so magnification was lost (info
// reported 0). It must now round-trip the source magnification.
func TestConvertDICOMPreservesMagnification(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs") // 20x
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	out := filepath.Join(t.TempDir(), "dcm")
	if o, err := runCLI(bin, "convert", "--to", "dicom", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to dicom: %v\n%s", err, o)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sl.Close()
	if m := sl.Metadata().Magnification; m != 20 {
		t.Errorf("dicom magnification = %v, want 20 (ObjectiveLensPower round-trip)", m)
	}
}

// TestConvertTIFFPreservesDateTime guards wsitools#31: the generic-tiff provenance
// carried a date-only `date=` field, which opentile preferred over the full 306
// DateTime tag — truncating the acquisition time to midnight. The full timestamp
// must now survive.
func TestConvertTIFFPreservesDateTime(t *testing.T) {
	src := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	bin := buildOnce(t)
	ssl, err := opentile.OpenFile(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	want := ssl.Metadata().AcquisitionDateTime
	ssl.Close()
	if want.Hour() == 0 && want.Minute() == 0 && want.Second() == 0 {
		t.Skip("source datetime has no time-of-day to preserve")
	}
	out := filepath.Join(t.TempDir(), "out.tiff")
	if o, err := runCLI(bin, "convert", "--to", "tiff", "--codec", "jpeg", "-f", "-o", out, src); err != nil {
		t.Fatalf("convert --to tiff: %v\n%s", err, o)
	}
	sl, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sl.Close()
	if got := sl.Metadata().AcquisitionDateTime; !got.Equal(want) {
		t.Errorf("tiff datetime = %v, want %v (full acquisition time preserved, not midnight)", got, want)
	}
}
