package bifwriter

import (
	"fmt"
	"io"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
)

// TileSource is the subset of source.Level the writer needs (verbatim
// compressed tiles + geometry). source.Level satisfies it.
type TileSource interface {
	SizeW() int
	SizeH() int
	TileW() int
	TileH() int
	TileMaxSize() int
	TileInto(x, y int, dst []byte) (int, error)
}

// levelAdapter adapts a source.Level to TileSource.
type levelAdapter struct{ l source.Level }

func (a levelAdapter) SizeW() int       { return a.l.Size().X }
func (a levelAdapter) SizeH() int       { return a.l.Size().Y }
func (a levelAdapter) TileW() int       { return a.l.TileSize().X }
func (a levelAdapter) TileH() int       { return a.l.TileSize().Y }
func (a levelAdapter) TileMaxSize() int { return a.l.TileMaxSize() }
func (a levelAdapter) TileInto(x, y int, dst []byte) (int, error) {
	return a.l.TileInto(x, y, dst)
}

// FromLevel wraps a source.Level as a TileSource.
func FromLevel(l source.Level) TileSource { return levelAdapter{l} }

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// WriteSingleLevel writes a minimal one-IFD BIF: a tiled JPEG pyramid level
// (ImageDescription "level=0 ...") whose tiles are copied verbatim from src and
// stored in BIF serpentine order, carrying the <iScan> marker XMP. This is the
// spike's de-risk artifact — opentile must read it back pixel-identical.
func WriteSingleLevel(w io.WriterAt, src TileSource, meta IScanMeta) error {
	cols := ceilDiv(src.SizeW(), src.TileW())
	rows := ceilDiv(src.SizeH(), src.TileH())
	n := cols * rows

	// 1. Read every tile's compressed bytes, keyed by serpentine index.
	tileBytes := make([][]byte, n)
	buf := make([]byte, src.TileMaxSize())
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			nb, err := src.TileInto(col, row, buf)
			if err != nil {
				return fmt.Errorf("bifwriter: read tile (%d,%d): %w", col, row, err)
			}
			idx := imageToSerpentine(col, row, cols, rows)
			b := make([]byte, nb)
			copy(b, buf[:nb])
			tileBytes[idx] = b
		}
	}

	xmp := iScanXMP(meta)

	// 2. Build the IFD twice: first with placeholder offsets to learn the
	//    record+external size, then again with the real tile offsets. The
	//    external SIZE is identical between passes (same array lengths / string
	//    lengths), so tileDataStart computed from pass 1 is correct for pass 2.
	const ifdOffset = uint64(16) // BigTIFF header is 16 bytes; IFD 0 follows.
	placeholder := make([]uint64, n)
	zeroCounts := make([]uint64, n)
	ifd0, ext0, err := buildLevelIFD(src, cols, rows, placeholder, zeroCounts, xmp)
	if err != nil {
		return err
	}
	tileDataStart := ifdOffset + uint64(len(ifd0)) + uint64(len(ext0))

	offsets := make([]uint64, n)
	counts := make([]uint64, n)
	cursor := tileDataStart
	for i := 0; i < n; i++ {
		offsets[i] = cursor
		counts[i] = uint64(len(tileBytes[i]))
		cursor += uint64(len(tileBytes[i]))
	}

	ifd, ext, err := buildLevelIFD(src, cols, rows, offsets, counts, xmp)
	if err != nil {
		return err
	}
	if len(ifd) != len(ifd0) || len(ext) != len(ext0) {
		return fmt.Errorf("bifwriter: IFD size unstable between passes (%d/%d vs %d/%d)",
			len(ifd0), len(ext0), len(ifd), len(ext))
	}
	// Single IFD: next-IFD pointer stays zero (Encode already left it zero).

	// 3. Write header, IFD, external data, then tile bodies.
	if err := tiff.WriteHeader(w, true, ifdOffset); err != nil {
		return err
	}
	if _, err := w.WriteAt(ifd, int64(ifdOffset)); err != nil {
		return err
	}
	if _, err := w.WriteAt(ext, int64(ifdOffset)+int64(len(ifd))); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if _, err := w.WriteAt(tileBytes[i], int64(offsets[i])); err != nil {
			return fmt.Errorf("bifwriter: write tile %d: %w", i, err)
		}
	}
	return nil
}

// buildLevelIFD assembles the pyramid-level IFD (tiled JPEG/YCbCr) with the
// supplied serpentine-ordered tile offsets/counts and the iScan XMP.
func buildLevelIFD(src TileSource, cols, rows int, offsets, counts []uint64, xmp []byte) (ifd, ext []byte, err error) {
	b := tiff.NewEntryBuilder(true)
	b.AddLong(tiff.TagImageWidth, []uint32{uint32(src.SizeW())})
	b.AddLong(tiff.TagImageLength, []uint32{uint32(src.SizeH())})
	b.AddShort(tiff.TagBitsPerSample, []uint16{8, 8, 8})
	b.AddShort(tiff.TagCompression, []uint16{tiff.CompressionJPEG})
	b.AddShort(tiff.TagPhotometricInterpretation, []uint16{6}) // YCbCr
	b.AddASCII(tiff.TagImageDescription,
		fmt.Sprintf("level=0 mag=%g quality=90", magFor(src)))
	b.AddShort(tiff.TagSamplesPerPixel, []uint16{3})
	b.AddShort(tiff.TagPlanarConfiguration, []uint16{1})
	b.AddShort(tiff.TagTileWidth, []uint16{uint16(src.TileW())})
	b.AddShort(tiff.TagTileLength, []uint16{uint16(src.TileH())})
	if err := b.AddTileOffsets(tiff.TagTileOffsets, offsets); err != nil {
		return nil, nil, err
	}
	if err := b.AddTileOffsets(tiff.TagTileByteCounts, counts); err != nil {
		return nil, nil, err
	}
	b.AddShort(tiff.TagYCbCrSubSampling, []uint16{2, 2})
	b.AddUndefined(uint16(700), xmp) // XMP
	return b.Encode(16)
}

// magFor is a placeholder magnification for the single emitted level. Phase 0
// does not thread real magnification; opentile derives MPP from <iScan>/ScanRes,
// not from this token, so any positive value round-trips.
func magFor(src TileSource) float64 { return 40 }
