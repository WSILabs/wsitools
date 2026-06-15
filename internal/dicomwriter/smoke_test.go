package dicomwriter

import (
	"bytes"
	"testing"

	"github.com/WSILabs/dicom"
	"github.com/WSILabs/dicom/pkg/frame"
	"github.com/WSILabs/dicom/pkg/tag"
)

// TestEncapsulatedWriteRoundTrip de-risks the suyashkumar/dicom encapsulated
// multi-frame write API: build a minimal WSM-shaped dataset with one
// encapsulated JPEG frame, dicom.Write it, dicom.Parse it back, and assert the
// PixelData survives byte-for-byte. The working construction patterns recorded
// here drive Tasks 2-3.
//
// VERIFIED API FACTS (suyashkumar/dicom v1.1.0):
//
//   - JPEG Baseline transfer syntax: there is NO exported named const in
//     pkg/uid (only ImplicitVR/ExplicitVR vars + a string-keyed map). Use the
//     literal "1.2.840.10008.1.2.4.50". dicom.Write reads this element to pick
//     the body byte-order/implicitness; for any 1.2.840.10008.1.2.4.* JPEG
//     transfer syntax the body is explicit-VR little-endian.
//
//   - dicom.NewElement(tag, data) infers VR from the tag dictionary; `data`
//     type must match the VR kind: UI/IS -> []string, US (Rows/Columns) ->
//     []int, PixelData -> dicom.PixelDataInfo.
//
//   - NumberOfFrames is VR "IS" -> pass []string{"1"} (NOT []int).
//
//   - Encapsulated PixelData: dicom.NewElement(tag.PixelData, ...) does NOT
//     work for the encapsulated case — it forces VR "OW" and leaves
//     ValueLength=0, so the writer takes the NATIVE branch and dereferences a
//     nil NativeData (SIGSEGV at write.go:640). The CANONICAL construction
//     (matching the library's own write_test.go "encapsulated PixelData" case)
//     is to hand-build the Element with VR "OB" and an UNDEFINED length:
//
//     &dicom.Element{
//     Tag: tag.PixelData,
//     ValueRepresentation:    tag.VRPixelData,
//     RawValueRepresentation: "OB",          // encapsulated => OB, not OW
//     ValueLength:            tag.VLUndefinedLength, // 0xffffffff -> encapsulated branch
//     Value: <dicom.NewValue(dicom.PixelDataInfo{...})>, // exported, returns (Value,error)
//     IsEncapsulated: true,
//     Offsets:        []uint32{...},
//     Frames:         []*frame.Frame{ {Encapsulated:true, EncapsulatedData: frame.EncapsulatedFrame{Data: jpegBytes}}, ... },
//     }),
//     }
//
//     The library then writes the Basic Offset Table + per-frame item
//     fragments + a SequenceDelimitationItem.
//
//   - File-meta (group 0002): dicom.Write only writes the group-0002 elements
//     that are PRESENT in ds.Elements (MediaStorageSOPClassUID,
//     MediaStorageSOPInstanceUID, TransferSyntaxUID, FileMetaInformationVersion,
//     plus any other group-0002 elements). It computes
//     FileMetaInformationGroupLength itself. ImplementationClassUID is NOT
//     required by Write; the preamble + "DICM" magic are emitted automatically.
//
//   - Read-back: element.Value.GetValue().(dicom.PixelDataInfo).Frames[0] is a
//     *frame.Frame; its bytes are GetEncapsulatedFrame().Data.
func TestEncapsulatedWriteRoundTrip(t *testing.T) {
	const jpegBaseline = "1.2.840.10008.1.2.4.50"
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xD9} // minimal SOI/EOI

	mk := func(tg tag.Tag, v any) *dicom.Element {
		e, err := dicom.NewElement(tg, v)
		if err != nil {
			t.Fatalf("NewElement(%v): %v", tg, err)
		}
		return e
	}

	pdValue, err := dicom.NewValue(dicom.PixelDataInfo{
		IsEncapsulated: true,
		Offsets:        []uint32{0},
		Frames: []*frame.Frame{
			{Encapsulated: true, EncapsulatedData: frame.EncapsulatedFrame{Data: jpeg}},
		},
	})
	if err != nil {
		t.Fatalf("NewValue(PixelDataInfo): %v", err)
	}
	pdElem := &dicom.Element{
		Tag:                    tag.PixelData,
		ValueRepresentation:    tag.VRPixelData,
		RawValueRepresentation: "OB", // encapsulated => OB (NewElement would force OW)
		ValueLength:            tag.VLUndefinedLength,
		Value:                  pdValue,
	}

	ds := dicom.Dataset{Elements: []*dicom.Element{
		mk(tag.MediaStorageSOPClassUID, []string{"1.2.840.10008.5.1.4.1.1.77.1.6"}),
		mk(tag.MediaStorageSOPInstanceUID, []string{NewUID()}),
		mk(tag.TransferSyntaxUID, []string{jpegBaseline}),
		mk(tag.Rows, []int{2}),
		mk(tag.Columns, []int{2}),
		mk(tag.NumberOfFrames, []string{"1"}),
		pdElem,
	}}

	var buf bytes.Buffer
	if err := dicom.Write(&buf, ds); err != nil {
		t.Fatalf("dicom.Write: %v", err)
	}

	got, err := dicom.Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	if err != nil {
		t.Fatalf("dicom.Parse back: %v", err)
	}

	pd, err := got.FindElementByTag(tag.PixelData)
	if err != nil {
		t.Fatalf("PixelData missing on read-back: %v", err)
	}

	info, ok := pd.Value.GetValue().(dicom.PixelDataInfo)
	if !ok {
		t.Fatalf("PixelData value is %T, want dicom.PixelDataInfo", pd.Value.GetValue())
	}
	if !info.IsEncapsulated {
		t.Fatalf("read-back PixelData is not encapsulated")
	}
	if len(info.Frames) != 1 {
		t.Fatalf("read-back frame count = %d, want 1", len(info.Frames))
	}
	ef, err := info.Frames[0].GetEncapsulatedFrame()
	if err != nil {
		t.Fatalf("GetEncapsulatedFrame: %v", err)
	}
	if !bytes.Equal(ef.Data, jpeg) {
		t.Fatalf("read-back frame bytes = % x, want % x", ef.Data, jpeg)
	}
}
