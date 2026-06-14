package dicomwriter

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/wsilabs/wsitools/internal/source"
)

// TestWriteVolumeInstanceRoundTrip writes a WSM VOLUME instance for the
// smallest level of the Grundium fixture, parses it back, and proves the
// verbatim-copy property: the first read-back frame equals the source's first
// raw tile read independently. Gated on the DICOM fixture (skips when absent).
func TestWriteVolumeInstanceRoundTrip(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no dicom fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Skipf("source.Open: %v", err)
	}
	defer src.Close()

	level := len(src.Levels()) - 1 // smallest
	lvl := src.Levels()[level]
	grid := lvl.Grid()
	wantFrames := grid.X * grid.Y

	// Independently read the source's first raw tile (0,0) for the
	// verbatim-copy proof.
	srcBuf := make([]byte, lvl.TileMaxSize())
	n, err := lvl.TileInto(0, 0, srcBuf)
	if err != nil {
		t.Fatalf("source TileInto(0,0): %v", err)
	}
	wantFirstTile := append([]byte(nil), srcBuf[:n]...)

	var out bytes.Buffer
	if err := WriteVolumeInstance(&out, src, level, Options{}); err != nil {
		t.Fatalf("WriteVolumeInstance: %v", err)
	}

	got, err := dicom.Parse(bytes.NewReader(out.Bytes()), int64(out.Len()), nil)
	if err != nil {
		t.Fatalf("dicom.Parse: %v", err)
	}

	// SOPClassUID == WSM.
	sopClass, err := got.FindElementByTag(tag.SOPClassUID)
	if err != nil {
		t.Fatalf("SOPClassUID missing: %v", err)
	}
	if vs, _ := sopClass.Value.GetValue().([]string); len(vs) == 0 || vs[0] != wsmSOPClassUID {
		t.Errorf("SOPClassUID = %v, want WSM %q", sopClass.Value.GetValue(), wsmSOPClassUID)
	}

	// NumberOfFrames (IS string) == grid.X*grid.Y.
	nf, err := got.FindElementByTag(tag.NumberOfFrames)
	if err != nil {
		t.Fatalf("NumberOfFrames missing: %v", err)
	}
	if vs, _ := nf.Value.GetValue().([]string); len(vs) == 0 || vs[0] != strconv.Itoa(wantFrames) {
		t.Errorf("NumberOfFrames = %v, want %d", nf.Value.GetValue(), wantFrames)
	}

	// PixelData: encapsulated, frame count matches, first frame == source tile.
	pd, err := got.FindElementByTag(tag.PixelData)
	if err != nil {
		t.Fatalf("PixelData missing: %v", err)
	}
	info, ok := pd.Value.GetValue().(dicom.PixelDataInfo)
	if !ok {
		t.Fatalf("PixelData value is %T, want dicom.PixelDataInfo", pd.Value.GetValue())
	}
	if !info.IsEncapsulated {
		t.Fatalf("read-back PixelData is not encapsulated")
	}
	if len(info.Frames) != wantFrames {
		t.Fatalf("read-back frame count = %d, want %d", len(info.Frames), wantFrames)
	}
	ef, err := info.Frames[0].GetEncapsulatedFrame()
	if err != nil {
		t.Fatalf("GetEncapsulatedFrame(0): %v", err)
	}
	if !bytes.Equal(ef.Data, wantFirstTile) {
		t.Fatalf("first frame bytes (%d) != source tile (%d) — verbatim-copy violated",
			len(ef.Data), len(wantFirstTile))
	}
}

type nopWriteCloser struct{ *bytes.Buffer }

func (nopWriteCloser) Close() error { return nil }

func TestWritePyramid(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "dicom", "scan_621_grundium_dicom")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no dicom fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	n := len(src.Levels())
	if n < 2 {
		t.Skip("need >= 2 levels")
	}

	bufs := map[string]*bytes.Buffer{}
	factory := func(name string) (io.WriteCloser, error) {
		b := &bytes.Buffer{}
		bufs[name] = b
		return nopWriteCloser{b}, nil
	}
	if err := WritePyramid(src, Options{}, factory); err != nil {
		t.Fatalf("WritePyramid: %v", err)
	}

	firstStr := func(ds dicom.Dataset, tg tag.Tag) string {
		e, err := ds.FindElementByTag(tg)
		if err != nil {
			t.Fatalf("missing %v: %v", tg, err)
		}
		return e.Value.GetValue().([]string)[0]
	}
	firstInt := func(ds dicom.Dataset, tg tag.Tag) int {
		e, err := ds.FindElementByTag(tg)
		if err != nil {
			t.Fatalf("missing %v: %v", tg, err)
		}
		return e.Value.GetValue().([]int)[0]
	}

	var series, frameOfRef, study string
	sops := map[string]bool{}
	for level := 0; level < n; level++ {
		b := bufs[fmt.Sprintf("level-%d", level)]
		if b == nil {
			t.Fatalf("level %d was never written", level)
		}
		ds, err := dicom.Parse(bytes.NewReader(b.Bytes()), int64(b.Len()), nil)
		if err != nil {
			t.Fatalf("parse level %d: %v", level, err)
		}
		s := firstStr(ds, tag.SeriesInstanceUID)
		fr := firstStr(ds, tag.FrameOfReferenceUID)
		st := firstStr(ds, tag.StudyInstanceUID)
		sop := firstStr(ds, tag.SOPInstanceUID)
		inst := firstStr(ds, tag.InstanceNumber)
		cols := firstInt(ds, tag.TotalPixelMatrixColumns)

		if level == 0 {
			series, frameOfRef, study = s, fr, st
		} else {
			if s != series {
				t.Errorf("level %d SeriesInstanceUID %q != L0 %q", level, s, series)
			}
			if fr != frameOfRef {
				t.Errorf("level %d FrameOfReferenceUID %q != L0 %q", level, fr, frameOfRef)
			}
			if st != study {
				t.Errorf("level %d StudyInstanceUID %q != L0 %q", level, st, study)
			}
		}
		if sops[sop] {
			t.Errorf("duplicate SOPInstanceUID %q at level %d", sop, level)
		}
		sops[sop] = true
		if inst != strconv.Itoa(level+1) {
			t.Errorf("level %d InstanceNumber = %q, want %d", level, inst, level+1)
		}
		if want := src.Levels()[level].Size().X; cols != want {
			t.Errorf("level %d TotalPixelMatrixColumns = %d, want %d", level, cols, want)
		}
	}
	if len(sops) != n {
		t.Errorf("got %d distinct SOPInstanceUIDs, want %d", len(sops), n)
	}
}

func openDICOMFixture(t *testing.T, name string) source.Source {
	t.Helper()
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "dicom", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no DICOM fixture at %s", p)
	}
	src, err := source.Open(p)
	if err != nil {
		t.Skipf("source.Open(%s): %v", name, err)
	}
	return src
}

// TestWriteVolumeInstance_JP2KSourceTransferSyntax guards the fix for the P0 bug:
// frames are copied VERBATIM, so a JPEG 2000 DICOM source's TransferSyntaxUID must
// be JPEG 2000 (.90/.91) — the old code hardcoded JPEG-baseline (.50) for every
// DICOM source, mislabeling J2K frames and producing invalid DICOM.
func TestWriteVolumeInstance_JP2KSourceTransferSyntax(t *testing.T) {
	src := openDICOMFixture(t, "3DHISTECH-JP2K")
	defer src.Close()
	level := len(src.Levels()) - 1 // smallest

	var out bytes.Buffer
	if err := WriteVolumeInstance(&out, src, level, Options{}); err != nil {
		t.Fatalf("WriteVolumeInstance: %v", err)
	}
	ds, err := dicom.Parse(bytes.NewReader(out.Bytes()), int64(out.Len()), nil)
	if err != nil {
		t.Fatalf("dicom.Parse: %v", err)
	}
	// 3DHISTECH-JP2K is reversible (lossless) JPEG 2000 → TS .90.
	if ts := firstStrA(t, ds, tag.TransferSyntaxUID); ts != jp2kLosslessTS {
		t.Errorf("TransferSyntaxUID = %q, want %q (JPEG 2000 lossless); J2K frames mislabeled as JPEG-baseline make invalid DICOM", ts, jp2kLosslessTS)
	}
}

// TestWriteVolumeInstance_JPEGSourceTransferSyntax guards the unchanged common
// case: a JPEG-baseline DICOM source keeps TS .50.
func TestWriteVolumeInstance_JPEGSourceTransferSyntax(t *testing.T) {
	src := openDICOMFixture(t, "scan_621_grundium_dicom")
	defer src.Close()
	level := len(src.Levels()) - 1

	var out bytes.Buffer
	if err := WriteVolumeInstance(&out, src, level, Options{}); err != nil {
		t.Fatalf("WriteVolumeInstance: %v", err)
	}
	ds, err := dicom.Parse(bytes.NewReader(out.Bytes()), int64(out.Len()), nil)
	if err != nil {
		t.Fatalf("dicom.Parse: %v", err)
	}
	if ts := firstStrA(t, ds, tag.TransferSyntaxUID); ts != jpegBaselineTS {
		t.Errorf("TransferSyntaxUID = %q, want %q (JPEG-baseline)", ts, jpegBaselineTS)
	}
}

// TestWriteVolumeInstance_HTJ2KSourceRejected confirms an HTJ2K DICOM source fails
// LOUD (frame-copy unsupported) rather than silently mislabeling HTJ2K frames as
// JPEG-baseline.
func TestWriteVolumeInstance_HTJ2KSourceRejected(t *testing.T) {
	src := openDICOMFixture(t, "3DHISTECH-HTJ2K")
	defer src.Close()
	level := len(src.Levels()) - 1

	var out bytes.Buffer
	if err := WriteVolumeInstance(&out, src, level, Options{}); err == nil {
		t.Fatalf("expected error for HTJ2K DICOM source (frame-copy unsupported), got success")
	}
}
