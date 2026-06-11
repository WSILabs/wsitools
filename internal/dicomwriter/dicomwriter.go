package dicomwriter

import (
	"fmt"
	"io"

	"github.com/suyashkumar/dicom"

	"github.com/wsilabs/wsitools/internal/source"
)

// Options is reserved for future write-side knobs (P0: empty).
type Options struct{}

// WriteVolumeInstance emits ONE conformant DICOM WSM VOLUME instance for src
// level `level` to w, copying the source's compressed JPEG frames verbatim.
// P0: the source MUST be a DICOM slide (JPEG-baseline frames re-encapsulated
// as-is).
func WriteVolumeInstance(w io.Writer, src source.Source, level int, _ Options) error {
	if src.Format() != "dicom" {
		return fmt.Errorf("--to dicom requires a DICOM source (P0)")
	}
	if level < 0 || level >= len(src.Levels()) {
		return fmt.Errorf("level %d out of range (0..%d)", level, len(src.Levels())-1)
	}
	uids := UIDSet{
		SOP:              NewUID(),
		Study:            NewUID(),
		Series:           NewUID(),
		FrameOfReference: NewUID(),
		DimensionOrg:     NewUID(),
	}
	// Encapsulate first: the compressed byte total feeds the lossy compression
	// ratio that the dataset advertises (LossyImageCompressionRatio is Type 1C,
	// required when LossyImageCompression is "01").
	pd, compressedBytes, err := encapsulatePixelData(src, level)
	if err != nil {
		return err
	}
	lvl := src.Levels()[level]
	tileSize := lvl.TileSize()
	grid := lvl.Grid()
	// Uncompressed size of the stored frames = frames × tile pixels × samples.
	uncompressed := int64(grid.X) * int64(grid.Y) * int64(tileSize.X) * int64(tileSize.Y) * 3
	lossyRatio := 1.0
	if compressedBytes > 0 {
		lossyRatio = float64(uncompressed) / float64(compressedBytes)
	}
	ds, err := assembleWSMDataset(src, level, uids, lossyRatio)
	if err != nil {
		return err
	}
	ds.Elements = append(ds.Elements, pd) // PixelData last
	return dicom.Write(w, ds)
}
