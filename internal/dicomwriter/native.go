package dicomwriter

import (
	"fmt"

	"github.com/WSILabs/dicom"
	"github.com/WSILabs/dicom/pkg/frame"
	"github.com/WSILabs/dicom/pkg/tag"
)

// explicitVRLE is the Explicit VR Little Endian transfer syntax — used for
// uncompressed (native) pixel data.
const explicitVRLE = "1.2.840.10008.1.2.1"

// nativePixelData builds a NATIVE (non-encapsulated) PixelData element from
// interleaved 8-bit samples (rgb, length = rows*cols*samples). Used for an
// associated image whose source codec can't be tile-copied into DICOM (e.g. an
// LZW label) — decoded and stored uncompressed, losslessly.
//
// WORKING CONSTRUCTION (verified by TestNativePixelDataRoundTrip): unlike the
// encapsulated path (see encapsulate.go), dicom.NewElement(tag.PixelData, ...)
// works as-is for the native case. NewElement picks RawVR by bit depth ("OB"
// for these 8-bit samples) and leaves ValueLength at its zero value (0). In
// write.go's writeElement the element's
// ValueLength (0) is passed through to writePixelData, where `vl == 0` is NOT
// VLUndefinedLength, so it routes to the NATIVE branch. That branch reads
// NativeData.Rows()/Cols()/SamplesPerPixel()/BitsPerSample() and, for
// BitsPerSample()==8, asserts RawDataSlice().([]uint8) — which a
// *frame.NativeFrame[uint8] satisfies. (The encapsulated SIGSEGV happened
// because encapsulated frames have nil NativeData yet still routed to the
// native branch; here NativeData is non-nil, so the native branch is valid.)
//
// Task 3 can therefore rely on this exact form: NewElement(tag.PixelData,
// PixelDataInfo{IsEncapsulated:false, Frames:[{NativeData: *NativeFrame[uint8]}]}).
func nativePixelData(rgb []byte, rows, cols, samples int) (*dicom.Element, error) {
	if len(rgb) != rows*cols*samples {
		return nil, fmt.Errorf("nativePixelData: have %d bytes, want %d (%dx%d×%d)", len(rgb), rows*cols*samples, cols, rows, samples)
	}
	nf := &frame.NativeFrame[uint8]{
		RawData:                 rgb,
		InternalSamplesPerPixel: samples,
		InternalRows:            rows,
		InternalCols:            cols,
		InternalBitsPerSample:   8,
	}
	// dicom.NewElement selects the PixelData VR by native bit depth: 8-bit
	// samples get VR "OB" (Other Byte), as DICOM requires — with "OW" a
	// conformant reader (e.g. opentile) would read the 8-bit buffer as 16-bit
	// words and collapse every RGB triple to grayscale. (This used to need a
	// manual `el.RawValueRepresentation = "OB"` override here; the fork now does
	// it at the source — WSILabs/dicom pixelDataVR, v1.1.0-wsilabs.2.)
	el, err := dicom.NewElement(tag.PixelData, dicom.PixelDataInfo{
		IsEncapsulated: false,
		Frames:         []*frame.Frame{{Encapsulated: false, NativeData: nf}},
	})
	if err != nil {
		return nil, err
	}
	return el, nil
}
