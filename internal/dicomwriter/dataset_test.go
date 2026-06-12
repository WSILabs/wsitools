package dicomwriter

import (
	"fmt"
	"math"
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
	ds, err := assembleWSMDataset(src, uids, instanceSpec{
		Size:                 size,
		TileSize:             tileSize,
		NumFrames:            grid.X * grid.Y,
		ImageType:            []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		SpecimenLabelInImage: "NO",
		InstanceNumber:       1,
		// Inert placeholders — this test asserts no spatial tags; real per-level
		// spacing/extent is verified in TestPerLevelSpatialMetadata.
		PixelSpacingX: 0.000251, PixelSpacingY: 0.000251,
		ImagedVolumeW: 16.0, ImagedVolumeH: 16.0,
		ImageDescriptor: ImageDescriptor{
			TransferSyntax: jpegBaselineTS, Photometric: "YBR_FULL_422", SamplesPerPixel: 3,
			ICCProfile: src.Metadata().ICCProfile, // Grundium fixture carries an ICC profile
			Lossy:      true, LossyMethod: "ISO_10918_1", LossyRatio: 10.0,
		},
	})
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

// imagedVolumeWidth reads the top-level ImagedVolumeWidth (FL) from ds.
func imagedVolumeWidth(t *testing.T, ds dicom.Dataset) float64 {
	t.Helper()
	e, err := ds.FindElementByTag(tag.ImagedVolumeWidth)
	if err != nil {
		t.Fatalf("ImagedVolumeWidth: %v", err)
	}
	vs, ok := e.Value.GetValue().([]float64)
	if !ok || len(vs) == 0 {
		t.Fatalf("ImagedVolumeWidth value is %T", e.Value.GetValue())
	}
	return vs[0]
}

// pixelSpacingYX reads PixelSpacing (DS, row\col = Y\X) from the nested
// SharedFunctionalGroupsSequence → PixelMeasuresSequence.
func pixelSpacingYX(t *testing.T, ds dicom.Dataset) (psY, psX float64) {
	t.Helper()
	sfg, err := ds.FindElementByTag(tag.SharedFunctionalGroupsSequence)
	if err != nil {
		t.Fatalf("SharedFunctionalGroupsSequence: %v", err)
	}
	items := sfg.Value.GetValue().([]*dicom.SequenceItemValue)
	for _, el := range items[0].GetValue().([]*dicom.Element) {
		if el.Tag != tag.PixelMeasuresSequence {
			continue
		}
		pm := el.Value.GetValue().([]*dicom.SequenceItemValue)
		for _, pe := range pm[0].GetValue().([]*dicom.Element) {
			if pe.Tag == tag.PixelSpacing {
				vs := pe.Value.GetValue().([]string)
				fmt.Sscanf(vs[0], "%g", &psY)
				fmt.Sscanf(vs[1], "%g", &psX)
				return psY, psX
			}
		}
	}
	t.Fatal("PixelSpacing not found")
	return 0, 0
}

func TestPerLevelSpatialMetadata(t *testing.T) {
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
	if len(src.Levels()) < 2 {
		t.Skip("need >= 2 levels")
	}
	last := len(src.Levels()) - 1

	uids := UIDSet{SOP: NewUID(), Study: NewUID(), Series: NewUID(), FrameOfReference: NewUID(), DimensionOrg: NewUID()}

	specFor := func(level int) instanceSpec {
		lvl := src.Levels()[level]
		md := src.Metadata()
		mppX, mppY := md.MPPX, md.MPPY
		if mppX == 0 {
			mppX = md.MPP
		}
		if mppY == 0 {
			mppY = md.MPP
		}
		psX, psY, w, h := levelSpatial(src.Levels()[0].Size(), lvl.Size(), mppX, mppY)
		g := lvl.Grid()
		return instanceSpec{
			Size: lvl.Size(), TileSize: lvl.TileSize(), NumFrames: g.X * g.Y,
			ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"}, SpecimenLabelInImage: "NO",
			InstanceNumber: level + 1, PixelSpacingX: psX, PixelSpacingY: psY, ImagedVolumeW: w, ImagedVolumeH: h,
			ImageDescriptor: ImageDescriptor{TransferSyntax: jpegBaselineTS, Photometric: "YBR_FULL_422", SamplesPerPixel: 3, ICCProfile: src.Metadata().ICCProfile, Lossy: true, LossyMethod: "ISO_10918_1", LossyRatio: 10.0},
		}
	}

	ds0, err := assembleWSMDataset(src, uids, specFor(0))
	if err != nil {
		t.Fatal(err)
	}
	dsN, err := assembleWSMDataset(src, uids, specFor(last))
	if err != nil {
		t.Fatal(err)
	}

	// Physical extent must be CONSTANT across levels.
	iv0 := imagedVolumeWidth(t, ds0)
	ivN := imagedVolumeWidth(t, dsN)
	if iv0 <= 0 {
		t.Fatalf("ImagedVolumeWidth at L0 = %g (want > 0; fixture should carry MPP)", iv0)
	}
	if math.Abs(iv0-ivN) > iv0*1e-6 {
		t.Errorf("ImagedVolumeWidth not constant across levels: L0=%g L%d=%g", iv0, last, ivN)
	}

	// PixelSpacing at a reduced level must scale by its downsample factor.
	_, ps0 := pixelSpacingYX(t, ds0)
	_, psN := pixelSpacingYX(t, dsN)
	downsample := float64(src.Levels()[0].Size().X) / float64(src.Levels()[last].Size().X)
	want := ps0 * downsample
	if math.Abs(psN-want) > want*0.01 {
		t.Errorf("L%d PixelSpacing(X)=%g, want ~%g (L0 %g × downsample %g)", last, psN, want, ps0, downsample)
	}
}

func TestAssembleWSMDatasetLosslessOmitsLossyTags(t *testing.T) {
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

	uids := UIDSet{SOP: NewUID(), Study: NewUID(), Series: NewUID(), FrameOfReference: NewUID(), DimensionOrg: NewUID()}
	lvl := src.Levels()[0]
	ds, err := assembleWSMDataset(src, uids, instanceSpec{
		Size:                 lvl.Size(),
		TileSize:             lvl.TileSize(),
		NumFrames:            lvl.Grid().X * lvl.Grid().Y,
		ImageType:            []string{"ORIGINAL", "PRIMARY", "VOLUME", "NONE"},
		SpecimenLabelInImage: "NO",
		InstanceNumber:       1,
		ImageDescriptor: ImageDescriptor{
			TransferSyntax:  jp2kLosslessTS,
			Photometric:     "RGB",
			SamplesPerPixel: 3,
			ICCProfile:      src.Metadata().ICCProfile,
			Lossy:           false, // lossless → ratio + method must be omitted
			LossyMethod:     "",
			LossyRatio:      1.0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if e, err := ds.FindElementByTag(tag.LossyImageCompression); err != nil {
		t.Errorf("LossyImageCompression missing: %v", err)
	} else if v := e.Value.GetValue().([]string); len(v) == 0 || v[0] != "00" {
		t.Errorf("LossyImageCompression = %v, want 00", e.Value.GetValue())
	}
	if _, err := ds.FindElementByTag(tag.LossyImageCompressionRatio); err == nil {
		t.Error("LossyImageCompressionRatio present on a lossless instance (must be omitted)")
	}
	if _, err := ds.FindElementByTag(tag.LossyImageCompressionMethod); err == nil {
		t.Error("LossyImageCompressionMethod present on a lossless instance (must be omitted)")
	}
	if e, err := ds.FindElementByTag(tag.TransferSyntaxUID); err != nil {
		t.Errorf("TransferSyntaxUID missing: %v", err)
	} else if v := e.Value.GetValue().([]string); v[0] != jp2kLosslessTS {
		t.Errorf("TransferSyntaxUID = %v, want %s", e.Value.GetValue(), jp2kLosslessTS)
	}
}

func TestAssembleWSMDatasetMonoOmitsPlanarConfiguration(t *testing.T) {
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

	uids := UIDSet{SOP: NewUID(), Study: NewUID(), Series: NewUID(), FrameOfReference: NewUID(), DimensionOrg: NewUID()}
	lvl := src.Levels()[0]
	ds, err := assembleWSMDataset(src, uids, instanceSpec{
		Size:                 lvl.Size(),
		TileSize:             lvl.TileSize(),
		NumFrames:            lvl.Grid().X * lvl.Grid().Y,
		ImageType:            []string{"ORIGINAL", "PRIMARY", "VOLUME", "NONE"},
		SpecimenLabelInImage: "NO",
		InstanceNumber:       1,
		ImageDescriptor: ImageDescriptor{
			TransferSyntax:  jpegBaselineTS,
			Photometric:     "MONOCHROME2",
			SamplesPerPixel: 1, // mono → PlanarConfiguration (Type 1C) must be omitted
			ICCProfile:      src.Metadata().ICCProfile,
			Lossy:           true,
			LossyMethod:     "ISO_10918_1",
			LossyRatio:      10.0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if e, err := ds.FindElementByTag(tag.SamplesPerPixel); err != nil {
		t.Errorf("SamplesPerPixel missing: %v", err)
	} else if v := e.Value.GetValue().([]int); len(v) == 0 || v[0] != 1 {
		t.Errorf("SamplesPerPixel = %v, want 1", e.Value.GetValue())
	}
	if _, err := ds.FindElementByTag(tag.PlanarConfiguration); err == nil {
		t.Error("PlanarConfiguration present on a SamplesPerPixel=1 instance (Type 1C: must be omitted)")
	}
}

func TestFormatDS(t *testing.T) {
	cases := []float64{
		0.0009992571101966163, // the JP2K-fixture value dciodvfy rejected (21 chars at %g)
		0.0002498, 0.000251, 0.004016, 12.5, 0, 1.0 / 3.0, 1e-7, 123456789.0,
	}
	for _, v := range cases {
		s := formatDS(v)
		if len(s) > 16 {
			t.Errorf("formatDS(%v) = %q (%d chars), want ≤16", v, s, len(s))
		}
		// Must remain a parseable decimal that's close to the input.
		got, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Errorf("formatDS(%v) = %q is not a valid float: %v", v, s, err)
		}
		if v != 0 && (got/v < 0.999 || got/v > 1.001) {
			t.Errorf("formatDS(%v) = %q parses to %v (>0.1%% off)", v, s, got)
		}
	}
}
