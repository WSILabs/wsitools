package dicomwriter

import (
	"fmt"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/frame"
	"github.com/suyashkumar/dicom/pkg/tag"
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
// works as-is for the native case. NewElement forces RawVR "OW" and leaves
// ValueLength at its zero value (0). In write.go's writeElement the element's
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
	el, err := dicom.NewElement(tag.PixelData, dicom.PixelDataInfo{
		IsEncapsulated: false,
		Frames:         []*frame.Frame{{Encapsulated: false, NativeData: nf}},
	})
	if err != nil {
		return nil, err
	}
	// dicom.NewElement hardcodes VR "OW" (16-bit Other Word) for PixelData
	// regardless of bit depth (element.go: `if t == tag.PixelData { rawVR = "OW" }`).
	// Our samples are 8-bit, so DICOM requires VR "OB" (Other Byte): with OW a
	// conformant reader (e.g. opentile) interprets the 8-bit buffer as 16-bit
	// words and collapses every RGB triple to grayscale — a silently lossy
	// transcode. Override to OB; PixelData's allowed VRs are {OB, OW}, so the
	// write-side VR check accepts it.
	el.RawValueRepresentation = "OB"
	return el, nil
}
