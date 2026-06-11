package dicomwriter

import (
	"bytes"
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
