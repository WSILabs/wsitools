package bifwriter

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/wsilabs/wsitools/internal/tiff"
)

// PyramidLevel is one source pyramid level plus its magnification (for the
// "level=N mag=M" ImageDescription token). Src tiles are copied verbatim.
type PyramidLevel struct {
	Src TileSource
	Mag float64
}

// Overview is the IFD-0 whole-slide overview image (the "Label_Image"): packed
// W×H RGB888, stored uncompressed as a single strip. len(RGB) must be W*H*3.
type Overview struct {
	W, H int
	RGB  []byte
}

// WritePyramid writes a full DP 200-shaped BIF: IFD 0 = overview, IFD 1..N =
// pyramid levels 0..N-1 (tiled JPEG, ROW-MAJOR storage, level=k mag=M
// ImageDescription). The level-0 pyramid IFD carries the <EncodeInfo> stitch
// XMP; the overview carries the <iScan> XMP; higher levels carry no XMP (per the
// Roche tag-by-IFD layout). Tiles are copied verbatim from each level's
// TileSource (BIF is a JPEG container; callers must ensure JPEG tiles).
//
// Layout: header | IFD0 | ext0 | IFD1 | ext1 | … | IFDn | extn | overview |
// L0 tiles | L1 tiles | … . Tile data is read once and streamed to its computed
// offset (no whole-pyramid buffering).
func WritePyramid(out io.WriterAt, levels []PyramidLevel, ov Overview, meta IScanMeta) error {
	if len(levels) == 0 {
		return fmt.Errorf("bifwriter: no pyramid levels")
	}
	if len(ov.RGB) != ov.W*ov.H*3 {
		return fmt.Errorf("bifwriter: overview RGB len %d != %d×%d×3", len(ov.RGB), ov.W, ov.H)
	}

	// Per-level grid geometry.
	type geom struct{ cols, rows, n int }
	g := make([]geom, len(levels))
	for i, l := range levels {
		c := ceilDiv(l.Src.SizeW(), l.Src.TileW())
		r := ceilDiv(l.Src.SizeH(), l.Src.TileH())
		g[i] = geom{c, r, c * r}
	}

	iscan := iScanXMP(meta)
	encinfo := encodeInfoXMP(g[0].cols, g[0].rows, levels[0].Src.TileW(), levels[0].Src.TileH())

	// XMP per pyramid IFD: level 0 → EncodeInfo, higher levels → none.
	pyramidXMP := func(level int) []byte {
		if level == 0 {
			return encinfo
		}
		return nil
	}

	// Pass A — size every IFD with placeholder (zero) offset/count arrays of the
	// correct length, to learn record+external byte sizes and thus each IFD's
	// offset and the data region start. Sizes are value-independent, so this
	// matches the real-offset pass exactly.
	const hdr = uint64(16)
	ifdOff := make([]uint64, 1+len(levels))
	var ifdLen, extLen []int
	cursor := hdr

	ovIFDa, ovExtA, err := buildOverviewIFD(ov.W, ov.H, 0, iscan)
	if err != nil {
		return err
	}
	ifdOff[0] = cursor
	ifdLen = append(ifdLen, len(ovIFDa))
	extLen = append(extLen, len(ovExtA))
	cursor += uint64(len(ovIFDa) + len(ovExtA))

	for i, l := range levels {
		zero := make([]uint64, g[i].n)
		ifd, ext, err := buildPyramidIFD(cursor, l.Src, g[i].cols, g[i].rows, i, l.Mag, zero, zero, pyramidXMP(i))
		if err != nil {
			return err
		}
		ifdOff[i+1] = cursor
		ifdLen = append(ifdLen, len(ifd))
		extLen = append(extLen, len(ext))
		cursor += uint64(len(ifd) + len(ext))
	}

	ovOff := cursor
	tilesStart := ovOff + uint64(len(ov.RGB))

	// Stream tile data to disk (single read per tile), recording offsets/counts.
	offsets := make([][]uint64, len(levels))
	counts := make([][]uint64, len(levels))
	cur := tilesStart
	for i, l := range levels {
		offsets[i] = make([]uint64, g[i].n)
		counts[i] = make([]uint64, g[i].n)
		buf := make([]byte, l.Src.TileMaxSize())
		for row := 0; row < g[i].rows; row++ {
			for col := 0; col < g[i].cols; col++ {
				nb, err := l.Src.TileInto(col, row, buf)
				if err != nil {
					return fmt.Errorf("bifwriter: read level %d tile (%d,%d): %w", i, col, row, err)
				}
				idx := row*g[i].cols + col // ROW-MAJOR storage
				offsets[i][idx] = cur
				counts[i][idx] = uint64(nb)
				if _, err := out.WriteAt(buf[:nb], int64(cur)); err != nil {
					return fmt.Errorf("bifwriter: write level %d tile (%d,%d): %w", i, col, row, err)
				}
				cur += uint64(nb)
			}
		}
	}

	// Pass B — rebuild IFDs with real offsets, patch the chain, write the head.
	ovIFD, ovExt, err := buildOverviewIFD(ov.W, ov.H, ovOff, iscan)
	if err != nil {
		return err
	}
	if len(ovIFD) != ifdLen[0] || len(ovExt) != extLen[0] {
		return fmt.Errorf("bifwriter: overview IFD size unstable between passes")
	}
	binary.LittleEndian.PutUint64(ovIFD[len(ovIFD)-8:], ifdOff[1]) // → IFD 1

	if err := tiff.WriteHeader(out, true, hdr); err != nil {
		return err
	}
	if _, err := out.WriteAt(ovIFD, int64(ifdOff[0])); err != nil {
		return err
	}
	if _, err := out.WriteAt(ovExt, int64(ifdOff[0])+int64(len(ovIFD))); err != nil {
		return err
	}
	for i, l := range levels {
		ifd, ext, err := buildPyramidIFD(ifdOff[i+1], l.Src, g[i].cols, g[i].rows, i, l.Mag, offsets[i], counts[i], pyramidXMP(i))
		if err != nil {
			return err
		}
		if len(ifd) != ifdLen[i+1] || len(ext) != extLen[i+1] {
			return fmt.Errorf("bifwriter: level %d IFD size unstable between passes", i)
		}
		next := uint64(0)
		if i+1 < len(levels) {
			next = ifdOff[i+2] // → next pyramid level
		}
		binary.LittleEndian.PutUint64(ifd[len(ifd)-8:], next)
		if _, err := out.WriteAt(ifd, int64(ifdOff[i+1])); err != nil {
			return err
		}
		if _, err := out.WriteAt(ext, int64(ifdOff[i+1])+int64(len(ifd))); err != nil {
			return err
		}
	}
	if _, err := out.WriteAt(ov.RGB, int64(ovOff)); err != nil {
		return err
	}
	return nil
}

// buildPyramidIFD builds one pyramid-level IFD (tiled JPEG/YCbCr, ROW-MAJOR
// tiles) with ImageDescription "level=<level> mag=<mag> quality=90". xmp is
// emitted as tag 700 (BYTE+NUL) when non-nil; pass nil for levels above 0, which
// carry no XMP.
func buildPyramidIFD(off uint64, src TileSource, cols, rows, level int, mag float64, offsets, counts []uint64, xmp []byte) (ifd, ext []byte, err error) {
	b := tiff.NewEntryBuilder(true)
	b.AddLong(tiff.TagImageWidth, []uint32{uint32(cols * src.TileW())})
	b.AddLong(tiff.TagImageLength, []uint32{uint32(rows * src.TileH())})
	b.AddShort(tiff.TagBitsPerSample, []uint16{8, 8, 8})
	b.AddShort(tiff.TagCompression, []uint16{tiff.CompressionJPEG})
	b.AddShort(tiff.TagPhotometricInterpretation, []uint16{6}) // YCbCr
	b.AddASCII(tiff.TagImageDescription, fmt.Sprintf("level=%d mag=%g quality=90", level, mag))
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
	if xmp != nil {
		b.AddBytes(uint16(700), append(append([]byte(nil), xmp...), 0))
	}
	return b.Encode(off)
}
