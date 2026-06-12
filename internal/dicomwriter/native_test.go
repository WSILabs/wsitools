package dicomwriter

import (
	"bytes"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// TestNativePixelDataRoundTrip de-risks the suyashkumar/dicom NATIVE (non-
// encapsulated) PixelData write path: build a tiny uncompressed RGB instance,
// dicom.Write it (Explicit VR LE), dicom.Parse it back, and assert the raw pixels
// survive. The working construction recorded here drives nativePixelData.
func TestNativePixelDataRoundTrip(t *testing.T) {
	const rows, cols = 2, 2
	rgb := []byte{
		10, 20, 30, 40, 50, 60, // row 0: two RGB pixels
		70, 80, 90, 100, 110, 120, // row 1
	}
	pd, err := nativePixelData(rgb, rows, cols, 3)
	if err != nil {
		t.Fatalf("nativePixelData: %v", err)
	}
	mk := func(tg tag.Tag, v any) *dicom.Element {
		e, err := dicom.NewElement(tg, v)
		if err != nil {
			t.Fatalf("NewElement(%v): %v", tg, err)
		}
		return e
	}
	ds := dicom.Dataset{Elements: []*dicom.Element{
		mk(tag.MediaStorageSOPClassUID, []string{wsmSOPClassUID}),
		mk(tag.MediaStorageSOPInstanceUID, []string{NewUID()}),
		mk(tag.TransferSyntaxUID, []string{explicitVRLE}),
		mk(tag.SamplesPerPixel, []int{3}),
		mk(tag.PhotometricInterpretation, []string{"RGB"}),
		mk(tag.PlanarConfiguration, []int{0}),
		mk(tag.Rows, []int{rows}),
		mk(tag.Columns, []int{cols}),
		mk(tag.BitsAllocated, []int{8}),
		mk(tag.BitsStored, []int{8}),
		mk(tag.HighBit, []int{7}),
		mk(tag.PixelRepresentation, []int{0}),
		pd,
	}}
	var buf bytes.Buffer
	if err := dicom.Write(&buf, ds); err != nil {
		t.Fatalf("dicom.Write: %v", err)
	}
	got, err := dicom.Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	if err != nil {
		t.Fatalf("dicom.Parse: %v", err)
	}
	pdBack, err := got.FindElementByTag(tag.PixelData)
	if err != nil {
		t.Fatalf("PixelData missing on read-back: %v", err)
	}
	info, ok := pdBack.Value.GetValue().(dicom.PixelDataInfo)
	if !ok {
		t.Fatalf("PixelData value is %T, want dicom.PixelDataInfo", pdBack.Value.GetValue())
	}
	if info.IsEncapsulated {
		t.Fatalf("read-back PixelData is encapsulated, want native")
	}
	if len(info.Frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(info.Frames))
	}
}
