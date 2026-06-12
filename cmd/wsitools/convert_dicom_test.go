package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/dicomwriter"
	"github.com/wsilabs/wsitools/internal/source"
)

// TestConvertDICOMReadBack round-trips a DICOM source through the
// dicomwriter (--to dicom, P0) and back through source.Open, proving the
// emitted single-instance .dcm reads as a one-level DICOM slide whose
// geometry matches the emitted source level and whose first raw tile is
// byte-identical to the source's (verbatim frame-copy through a file).
func TestConvertDICOMReadBack(t *testing.T) {
	dir := filepath.Join(testDir(t), "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}

	src, err := source.Open(dir)
	if err != nil {
		t.Fatalf("open source %s: %v", dir, err)
	}
	defer src.Close()

	levels := src.Levels()
	if len(levels) == 0 {
		t.Fatal("source has no levels")
	}
	// Emit the smallest level (last) to keep the test cheap.
	emit := len(levels) - 1
	srcLvl := levels[emit]

	out := filepath.Join(t.TempDir(), "out.dcm")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create %s: %v", out, err)
	}
	if err := dicomwriter.WriteVolumeInstance(f, src, emit, dicomwriter.Options{}); err != nil {
		f.Close()
		t.Fatalf("WriteVolumeInstance(level %d): %v", emit, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", out, err)
	}

	// Read the emitted single-instance .dcm back through source.Open.
	dst, err := source.Open(out)
	if err != nil {
		t.Fatalf("open emitted %s: %v", out, err)
	}
	defer dst.Close()

	if got := dst.Format(); got != "dicom" {
		t.Errorf("output Format() = %q, want \"dicom\"", got)
	}
	dstLevels := dst.Levels()
	if len(dstLevels) != 1 {
		t.Fatalf("output has %d levels, want 1 (single VOLUME instance)", len(dstLevels))
	}
	dstLvl := dstLevels[0]

	if dstLvl.Size() != srcLvl.Size() {
		t.Errorf("Size() mismatch: src=%v dst=%v", srcLvl.Size(), dstLvl.Size())
	}
	if dstLvl.TileSize() != srcLvl.TileSize() {
		t.Errorf("TileSize() mismatch: src=%v dst=%v", srcLvl.TileSize(), dstLvl.TileSize())
	}

	// Verbatim round-trip proof: first raw tile must be byte-identical.
	srcBuf := make([]byte, srcLvl.TileMaxSize())
	srcN, err := srcLvl.TileInto(0, 0, srcBuf)
	if err != nil {
		t.Fatalf("src TileInto(0,0): %v", err)
	}
	dstBuf := make([]byte, dstLvl.TileMaxSize())
	dstN, err := dstLvl.TileInto(0, 0, dstBuf)
	if err != nil {
		t.Fatalf("dst TileInto(0,0): %v", err)
	}
	if !bytes.Equal(srcBuf[:srcN], dstBuf[:dstN]) {
		t.Errorf("first raw tile not byte-identical: src=%d bytes, dst=%d bytes", srcN, dstN)
	}
}

// TestConvertDICOMCommand drives the cobra command path (convert --to dicom)
// so the handler wiring — overwrite guard, file create/cleanup, dispatch — is
// exercised, not just the library. The verbatim-byte proof lives in
// TestConvertDICOMReadBack; here we only assert a readable DICOM is produced.
func TestConvertDICOMCommand(t *testing.T) {
	dir := filepath.Join(testDir(t), "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}

	// Determine the smallest level to keep the test cheap.
	src, err := source.Open(dir)
	if err != nil {
		t.Fatalf("open source %s: %v", dir, err)
	}
	emit := len(src.Levels()) - 1
	src.Close()
	if emit < 0 {
		t.Fatal("source has no levels")
	}

	out := filepath.Join(t.TempDir(), "out.dcm")
	rootCmd.SetArgs([]string{"convert", "--to", "dicom", "--level", fmt.Sprint(emit), "-o", out, dir})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		cvOutput = ""
		cvForce = false
		cvDICOMLevel = 0
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("convert --to dicom: %v", err)
	}

	dst, err := source.Open(out)
	if err != nil {
		t.Fatalf("open emitted %s: %v", out, err)
	}
	defer dst.Close()
	if got := dst.Format(); got != "dicom" {
		t.Errorf("output Format() = %q, want \"dicom\"", got)
	}
}

func TestConvertDICOMPyramidCommand(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	svs := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skip("no CMU SVS fixture")
	}

	src, err := source.Open(svs)
	if err != nil {
		t.Fatal(err)
	}
	n := len(src.Levels())
	src.Close()

	out := filepath.Join(t.TempDir(), "pyramid")
	// The pyramid path requires --level NOT set; cobra persists Changed across
	// Execute() calls, so reset it defensively.
	convertCmd.Flags().Lookup("level").Changed = false
	cvOutput = ""
	cvForce = false
	rootCmd.SetArgs([]string{"convert", "--to", "dicom", "-o", out, svs})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		convertCmd.Flags().Lookup("level").Changed = false
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("convert --to dicom (pyramid): %v", err)
	}

	for level := 0; level < n; level++ {
		path := filepath.Join(out, "level-"+strconv.Itoa(level)+".dcm")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
		s, err := source.Open(path)
		if err != nil {
			t.Fatalf("source.Open(%s): %v", path, err)
		}
		if s.Format() != "dicom" {
			t.Errorf("%s Format = %q, want dicom", path, s.Format())
		}
		s.Close()
	}
}

func TestSVSToDICOMPixelRoundTrip(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	svsPath := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svsPath); err != nil {
		t.Skip("no CMU SVS fixture")
	}

	// Emit SVS level 0 → DICOM.
	src, err := source.Open(svsPath)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "svs.dcm")
	f, err := os.Create(out)
	if err != nil {
		src.Close()
		t.Fatal(err)
	}
	if err := dicomwriter.WriteVolumeInstance(f, src, 0, dicomwriter.Options{}); err != nil {
		f.Close()
		src.Close()
		t.Fatalf("WriteVolumeInstance: %v", err)
	}
	f.Close()
	src.Close()

	// Read back as DICOM (regression: it opens + reports as a DICOM slide).
	back, err := source.Open(out)
	if err != nil {
		t.Fatalf("source.Open(emitted dicom): %v", err)
	}
	if back.Format() != "dicom" {
		t.Errorf("emitted Format = %q, want dicom", back.Format())
	}
	back.Close()

	// Pixel round-trip: decode region (0,0,w,h) from both files and compare.
	const w, h = 240, 240 // CMU L0 tile size; a single-tile region exercises frame 0
	decodeRGB := func(path string) *decoder.Image {
		s, err := opentile.OpenFile(path)
		if err != nil {
			t.Fatalf("opentile.OpenFile(%s): %v", path, err)
		}
		defer s.Close()
		img, err := s.ImageReadRegion(0, 0, 0, 0, w, h, opentile.WithFormat(decoder.PixelFormatRGB))
		if err != nil {
			t.Fatalf("ImageReadRegion(%s): %v", path, err)
		}
		return img
	}
	srcImg := decodeRGB(svsPath)
	dcmImg := decodeRGB(out)

	if srcImg.Width != dcmImg.Width || srcImg.Height != dcmImg.Height {
		t.Fatalf("dim mismatch: src=%dx%d dcm=%dx%d", srcImg.Width, srcImg.Height, dcmImg.Width, dcmImg.Height)
	}
	// Verbatim copy + matching photometric ⇒ byte-identical decoded RGB.
	if !bytes.Equal(srcImg.Pix, dcmImg.Pix) {
		n := len(srcImg.Pix)
		if len(dcmImg.Pix) < n {
			n = len(dcmImg.Pix)
		}
		for i := 0; i < n; i++ {
			if srcImg.Pix[i] != dcmImg.Pix[i] {
				t.Fatalf("pixel mismatch at byte %d: src=%d dcm=%d (colorspace/photometric likely wrong)", i, srcImg.Pix[i], dcmImg.Pix[i])
			}
		}
		t.Fatalf("pixel buffers differ in length: src=%d dcm=%d", len(srcImg.Pix), len(dcmImg.Pix))
	}
}
