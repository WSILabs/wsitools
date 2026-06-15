package dicomwriter

import (
	"fmt"

	"github.com/WSILabs/dicom"
	"github.com/WSILabs/dicom/pkg/frame"
	"github.com/WSILabs/dicom/pkg/tag"

	"github.com/wsilabs/wsitools/internal/source"
)

// encapsulatePixelData reads every tile of src level `level` in TILED_FULL
// frame order (row-major, column fastest: frameIndex = ty*gridX + tx) and
// re-encapsulates the verbatim compressed frame bytes into a single
// encapsulated PixelData element.
//
// For a DICOM source, TileInto returns the raw encapsulated JPEG frame bytes
// exactly as stored, so the copy is byte-for-byte — except an odd-length frame
// is padded with a trailing 0x00 to satisfy DICOM's even-length fragment
// requirement (PS3.5 §7.5/§A.4). Pixel content is unchanged, since JPEG decoders
// stop at the EOI marker.
//
// The returned Element is HAND-BUILT (VR "OB", undefined length) rather than
// constructed via dicom.NewElement(tag.PixelData, ...): NewElement forces
// VR "OW" with ValueLength=0, which sends dicom.Write down the native branch
// and SIGSEGVs on a nil NativeData. See smoke_test.go.
//
// The second return value is the total compressed byte count across all frames,
// used by the caller to compute LossyImageCompressionRatio.
func encapsulatePixelData(src source.Source, level int) (*dicom.Element, int64, error) {
	if level < 0 || level >= len(src.Levels()) {
		return nil, 0, fmt.Errorf("level %d out of range (0..%d)", level, len(src.Levels())-1)
	}
	lvl := src.Levels()[level]
	grid := lvl.Grid()

	buf := make([]byte, lvl.TileMaxSize())
	frames := make([]*frame.Frame, 0, grid.X*grid.Y)
	var totalBytes int64
	for ty := 0; ty < grid.Y; ty++ {
		for tx := 0; tx < grid.X; tx++ {
			n, err := lvl.TileInto(tx, ty, buf)
			if err != nil {
				return nil, 0, fmt.Errorf("read tile (%d,%d): %w", tx, ty, err)
			}
			// Copy out of the reused buffer before the next iteration overwrites it.
			data := append([]byte(nil), buf[:n]...)
			totalBytes += int64(n)
			// DICOM encapsulated fragment Items must have even length
			// (PS3.5 §7.5/§A.4). Pad an odd-length JPEG frame with a
			// trailing 0x00; decoders ignore bytes after the EOI marker,
			// so pixel data is preserved verbatim.
			if len(data)%2 != 0 {
				data = append(data, 0x00)
			}
			frames = append(frames, &frame.Frame{
				Encapsulated:     true,
				EncapsulatedData: frame.EncapsulatedFrame{Data: data},
			})
		}
	}

	// A zeroed Basic Offset Table (one entry per frame) is accepted.
	offsets := make([]uint32, len(frames))

	pdValue, err := dicom.NewValue(dicom.PixelDataInfo{
		IsEncapsulated: true,
		Offsets:        offsets,
		Frames:         frames,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("build PixelData value: %w", err)
	}
	elem := &dicom.Element{
		Tag:                    tag.PixelData,
		ValueRepresentation:    tag.VRPixelData,
		RawValueRepresentation: "OB",                  // encapsulated => OB, not OW
		ValueLength:            tag.VLUndefinedLength, // 0xffffffff => encapsulated branch
		Value:                  pdValue,
	}
	return elem, totalBytes, nil
}
