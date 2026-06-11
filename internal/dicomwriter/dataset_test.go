package dicomwriter

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/wsilabs/wsitools/internal/source"
)

// TestAssembleWSMDataset assembles the WSM IOD for the smallest level of the
// Grundium fixture and asserts the key DERIVED + structural attributes. Gated
// on the DICOM fixture (skips when absent), like the other integration tests.
func TestAssembleWSMDataset(t *testing.T) {
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

	level := len(src.Levels()) - 1 // smallest
	lvl := src.Levels()[level]
	grid := lvl.Grid()
	size := lvl.Size()
	tileSize := lvl.TileSize()

	uids := UIDSet{
		SOP:              NewUID(),
		Study:            NewUID(),
		Series:           NewUID(),
		FrameOfReference: NewUID(),
		DimensionOrg:     NewUID(),
	}
	ds, err := assembleWSMDataset(src, level, uids, 10.0)
	if err != nil {
		t.Fatal(err)
	}

	// strVal returns the first string value of an element.
	strVal := func(tg tag.Tag) string {
		e, err := ds.FindElementByTag(tg)
		if err != nil {
			t.Fatalf("missing %v: %v", tg, err)
		}
		vs, ok := e.Value.GetValue().([]string)
		if !ok || len(vs) == 0 {
			t.Fatalf("%v value is %T (want non-empty []string)", tg, e.Value.GetValue())
		}
		return vs[0]
	}
	intVal := func(tg tag.Tag) int {
		e, err := ds.FindElementByTag(tg)
		if err != nil {
			t.Fatalf("missing %v: %v", tg, err)
		}
		vs, ok := e.Value.GetValue().([]int)
		if !ok || len(vs) == 0 {
			t.Fatalf("%v value is %T (want non-empty []int)", tg, e.Value.GetValue())
		}
		return vs[0]
	}

	if got := strVal(tag.SOPClassUID); got != wsmSOPClassUID {
		t.Errorf("SOPClassUID = %q, want WSM %q", got, wsmSOPClassUID)
	}
	if got := strVal(tag.DimensionOrganizationType); got != "TILED_FULL" {
		t.Errorf("DimensionOrganizationType = %q, want TILED_FULL", got)
	}
	if got := strVal(tag.Modality); got != "SM" {
		t.Errorf("Modality = %q, want SM", got)
	}
	wantFrames := grid.X * grid.Y
	if got := strVal(tag.NumberOfFrames); got != strconv.Itoa(wantFrames) {
		t.Errorf("NumberOfFrames = %q, want %d", got, wantFrames)
	}
	if got := intVal(tag.TotalPixelMatrixColumns); got != size.X {
		t.Errorf("TotalPixelMatrixColumns = %d, want %d", got, size.X)
	}
	if got := intVal(tag.TotalPixelMatrixRows); got != size.Y {
		t.Errorf("TotalPixelMatrixRows = %d, want %d", got, size.Y)
	}
	if got := intVal(tag.Rows); got != tileSize.Y {
		t.Errorf("Rows = %d, want tile height %d", got, tileSize.Y)
	}
	if got := intVal(tag.Columns); got != tileSize.X {
		t.Errorf("Columns = %d, want tile width %d", got, tileSize.X)
	}
	if got := intVal(tag.SamplesPerPixel); got != 3 {
		t.Errorf("SamplesPerPixel = %d, want 3", got)
	}

	// SOPInstanceUID must equal the UID we passed in (and the file-meta one).
	if got := strVal(tag.SOPInstanceUID); got != uids.SOP {
		t.Errorf("SOPInstanceUID = %q, want %q", got, uids.SOP)
	}
	if got := strVal(tag.MediaStorageSOPInstanceUID); got != uids.SOP {
		t.Errorf("MediaStorageSOPInstanceUID = %q, want %q", got, uids.SOP)
	}

	// Sequence presence: OpticalPathSequence + SharedFunctionalGroupsSequence.
	if _, err := ds.FindElementByTag(tag.OpticalPathSequence); err != nil {
		t.Errorf("OpticalPathSequence missing: %v", err)
	}
	if _, err := ds.FindElementByTag(tag.SharedFunctionalGroupsSequence); err != nil {
		t.Errorf("SharedFunctionalGroupsSequence missing: %v", err)
	}

	// Conformance attributes added for dciodvfy (regression net for the values
	// the validator checks but no other unit test would catch).
	if got := strVal(tag.LossyImageCompressionRatio); got != "10" {
		t.Errorf("LossyImageCompressionRatio = %q, want %q (from ratio 10.0)", got, "10")
	}
	if got := strVal(tag.DeviceSerialNumber); got == "" {
		t.Error("DeviceSerialNumber empty (Type 1 must be non-empty)")
	}
	// AcquisitionDateTime + ContentDate are Type 1 (must be present, non-empty).
	if got := strVal(tag.AcquisitionDateTime); got == "" {
		t.Error("AcquisitionDateTime empty (Type 1)")
	}
	if got := strVal(tag.ContentDate); got == "" {
		t.Error("ContentDate empty (Type 1)")
	}

	// ICCProfile is carried into the OpticalPathSequence item when the source
	// has one; the Grundium fixture does, so assert it survives into the item.
	if len(src.Metadata().ICCProfile) > 0 {
		opSeq, err := ds.FindElementByTag(tag.OpticalPathSequence)
		if err != nil {
			t.Fatalf("OpticalPathSequence missing: %v", err)
		}
		items, ok := opSeq.Value.GetValue().([]*dicom.SequenceItemValue)
		if !ok || len(items) == 0 {
			t.Fatalf("OpticalPathSequence value is %T (want non-empty items)", opSeq.Value.GetValue())
		}
		var foundICC bool
		for _, el := range items[0].GetValue().([]*dicom.Element) {
			if el.Tag == tag.ICCProfile {
				foundICC = true
				break
			}
		}
		if !foundICC {
			t.Error("ICCProfile not carried into OpticalPathSequence item")
		}
	}
}
