package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	xtiff "golang.org/x/image/tiff"

	"github.com/wsilabs/opentile-go/decoder"

	"github.com/wsilabs/wsitools/internal/source"
)

var (
	extractKind   string
	extractOutput string
	extractFormat string
)

var extractCmd = &cobra.Command{
	Use:   "extract <file>",
	Short: "Save an associated image (label/macro/thumbnail/overview) as PNG or JPEG",
	Long: `Save an associated image embedded in a WSI as a standalone PNG or JPEG file.

Available associated-image kinds depend on the source format and the slide:
typically label, macro, thumbnail, overview. Run 'wsitools info <file>'
to list which kinds the slide carries.

For --format jpeg, when the source associated image is already JPEG-compressed,
the original bytes are passed through verbatim (no decode/re-encode loss).
For --format png, the image is decoded to RGB and re-encoded as PNG.`,
	Args: cobra.ExactArgs(1),
	RunE: runExtract,
}

func init() {
	extractCmd.Flags().StringVar(&extractKind, "kind", "", "associated-image kind (label|macro|thumbnail|overview)")
	extractCmd.Flags().StringVarP(&extractOutput, "output", "o", "", "output file path")
	extractCmd.Flags().StringVar(&extractFormat, "format", "png", "output format: png|jpeg")
	_ = extractCmd.MarkFlagRequired("kind")
	_ = extractCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(extractCmd)
}

func runExtract(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	path := args[0]

	if extractFormat != "png" && extractFormat != "jpeg" {
		return fmt.Errorf("--format must be png or jpeg, got %q", extractFormat)
	}

	src, err := source.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	var match source.AssociatedImage
	var available []string
	for _, a := range src.Associated() {
		available = append(available, a.Kind())
		if a.Kind() == extractKind {
			match = a
		}
	}
	if match == nil {
		return fmt.Errorf("no associated image with kind %q (available: %s)",
			extractKind, strings.Join(available, ", "))
	}

	bytesIn, err := match.Bytes()
	if err != nil {
		return fmt.Errorf("read associated %s: %w", extractKind, err)
	}
	srcComp := match.Compression()

	// JPEG byte-pass-through path: source is JPEG and user wants JPEG.
	if extractFormat == "jpeg" && srcComp == source.CompressionJPEG {
		if err := os.WriteFile(extractOutput, bytesIn, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", extractOutput, err)
		}
		fmt.Printf("wrote %s (%s)\n", extractOutput, formatBytes(int64(len(bytesIn))))
		return nil
	}

	// Decode → re-encode path.
	img, err := decodeAssociated(path, bytesIn, srcComp, match.Size().X, match.Size().Y)
	if err != nil {
		return err
	}

	out, err := os.Create(extractOutput)
	if err != nil {
		return fmt.Errorf("create %s: %w", extractOutput, err)
	}
	defer out.Close()
	switch extractFormat {
	case "png":
		if err := png.Encode(out, img); err != nil {
			return fmt.Errorf("encode png: %w", err)
		}
	case "jpeg":
		if err := jpeg.Encode(out, img, &jpeg.Options{Quality: 90}); err != nil {
			return fmt.Errorf("encode jpeg: %w", err)
		}
	}
	stat, _ := os.Stat(extractOutput)
	if stat != nil {
		fmt.Printf("wrote %s (%s)\n", extractOutput, formatBytes(stat.Size()))
	}
	return nil
}

// decodeAssociated decodes associated-image bytes into an image.Image.
// For LZW, it reads strips directly from the source TIFF file (path) to
// correctly handle horizontal differencing (Predictor=2), which
// AssociatedImage.Bytes() re-encodes without preserving the strip structure
// needed by golang.org/x/image/tiff.
func decodeAssociated(path string, b []byte, comp source.Compression, w, h int) (image.Image, error) {
	switch comp {
	case source.CompressionJPEG:
		fac, ok := decoder.Get("jpeg")
		if !ok {
			return nil, fmt.Errorf("no jpeg decoder registered")
		}
		dec := fac.New()
		defer dec.Close()
		img, err := dec.Decode(b, decoder.DecodeOptions{Scale: 1, Format: decoder.PixelFormatRGB})
		if err != nil {
			return nil, fmt.Errorf("decode jpeg: %w", err)
		}
		return rgbToImage(img.Pix, w, h), nil
	case source.CompressionJPEG2000:
		fac, ok := decoder.Get("jpeg2000")
		if !ok {
			return nil, fmt.Errorf("no jpeg2000 decoder registered")
		}
		dec := fac.New()
		defer dec.Close()
		img, err := dec.Decode(b, decoder.DecodeOptions{Scale: 1, Format: decoder.PixelFormatRGB})
		if err != nil {
			return nil, fmt.Errorf("decode jpeg2000: %w", err)
		}
		return rgbToImage(img.Pix, w, h), nil
	case source.CompressionLZW:
		// AssociatedImage.Bytes() for LZW labels returns a single re-encoded LZW
		// stream, but TIFF LZW labels may use horizontal differencing (Predictor=2),
		// which is applied per-strip before encoding. The re-encoded stream doesn't
		// carry predictor metadata, and the x/image/tiff/lzw decoder only decodes a
		// fraction of the stream for large images due to a round-trip incompatibility
		// with opentile-go's internal tifflzw writer.
		//
		// Solution: read the strips directly from the source TIFF file, find the IFD
		// with matching dimensions and LZW compression, and wrap in a minimal
		// multi-strip TIFF that xtiff.Decode can handle (including Predictor=2).
		img, err := readLZWFromTIFF(path, w, h)
		if err != nil {
			return nil, fmt.Errorf("decode lzw (from source TIFF): %w", err)
		}
		return img, nil
	case source.CompressionDeflate, source.CompressionNone:
		// DICOM uncompressed associated images return raw interleaved 8-bit RGB
		// (w*h*3 bytes). SVS-family "none"/"deflate" associated images return a
		// TIFF-wrapped blob. Discriminate by exact length: a w*h*3 match is raw
		// RGB; otherwise fall back to the TIFF-wrapper path.
		if len(b) == w*h*3 {
			return rgbToImage(b, w, h), nil
		}
		return xtiff.Decode(bytes.NewReader(b))
	}
	return nil, fmt.Errorf("source compression %s is not decodable for extract", comp)
}

// readLZWFromTIFF opens the source TIFF file, walks IFDs to find one with
// matching width × height and LZW compression (TIFF tag 259 = 5), reads its
// strips, and builds a minimal multi-strip TIFF in memory for xtiff.Decode.
// This correctly handles the Predictor=2 (horizontal differencing) that SVS
// label IFDs carry — a detail lost in AssociatedImage.Bytes().
func readLZWFromTIFF(path string, targetW, targetH int) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	// Read TIFF header.
	hdr := make([]byte, 8)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	var bo binary.ByteOrder
	switch string(hdr[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, fmt.Errorf("not a TIFF file")
	}
	magic := bo.Uint16(hdr[2:4])
	if magic != 42 {
		return nil, fmt.Errorf("unsupported TIFF magic %d (BigTIFF not implemented for extract LZW path)", magic)
	}
	ifdOff := int64(bo.Uint32(hdr[4:8]))

	// Walk the IFD chain, looking for an IFD with LZW and the target dimensions.
	for ifdOff != 0 {
		ifd, nextOff, err := readExtractIFD(f, bo, ifdOff)
		if err != nil {
			return nil, err
		}
		if ifd.compression == 5 && // LZW
			int(ifd.imageW) == targetW &&
			int(ifd.imageH) == targetH &&
			len(ifd.stripOffsets) > 0 {
			if len(ifd.stripOffsets) != len(ifd.stripByteCounts) {
				return nil, fmt.Errorf("malformed IFD: %d StripOffsets vs %d StripByteCounts",
					len(ifd.stripOffsets), len(ifd.stripByteCounts))
			}
			// Found the matching IFD. Read strips and build a TIFF.
			strips := make([][]byte, len(ifd.stripOffsets))
			for i, off := range ifd.stripOffsets {
				buf := make([]byte, ifd.stripByteCounts[i])
				if _, err := f.ReadAt(buf, int64(off)); err != nil {
					return nil, fmt.Errorf("read strip %d: %w", i, err)
				}
				strips[i] = buf
			}
			tiffBytes := buildMultiStripTIFF(strips, targetW, targetH,
				int(ifd.samplesPerPixel), 5, ifd.predictor, int(ifd.rowsPerStrip))
			return xtiff.Decode(bytes.NewReader(tiffBytes))
		}
		ifdOff = nextOff
	}
	return nil, fmt.Errorf("no LZW IFD with dimensions %dx%d found in %s", targetW, targetH, path)
}

// extractIFDMeta holds the strip-related tags needed by readLZWFromTIFF.
type extractIFDMeta struct {
	imageW, imageH  uint32
	compression     uint16
	samplesPerPixel uint16
	rowsPerStrip    uint32
	predictor       uint16
	stripOffsets    []uint32
	stripByteCounts []uint32
}

// readExtractIFD reads one classic-TIFF IFD at off and returns its metadata
// and the offset to the next IFD.
func readExtractIFD(f *os.File, bo binary.ByteOrder, off int64) (extractIFDMeta, int64, error) {
	var m extractIFDMeta
	m.predictor = 1

	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return m, 0, fmt.Errorf("seek IFD@%d: %w", off, err)
	}
	var nRaw [2]byte
	if _, err := io.ReadFull(f, nRaw[:]); err != nil {
		return m, 0, fmt.Errorf("read IFD entry count: %w", err)
	}
	n := int(bo.Uint16(nRaw[:]))

	entries := make([]byte, n*12)
	if _, err := io.ReadFull(f, entries); err != nil {
		return m, 0, fmt.Errorf("read IFD entries: %w", err)
	}

	// Read next-IFD offset.
	var nextRaw [4]byte
	if _, err := io.ReadFull(f, nextRaw[:]); err != nil {
		return m, 0, fmt.Errorf("read next-IFD offset: %w", err)
	}
	nextOff := int64(bo.Uint32(nextRaw[:]))

	// Decode entries.
	for i := 0; i < n; i++ {
		e := entries[i*12 : (i+1)*12]
		tag := bo.Uint16(e[0:2])
		typ := bo.Uint16(e[2:4])
		count := bo.Uint32(e[4:8])
		vf := e[8:12] // value-or-offset field

		// Helper: read one scalar value.
		scalar := func() uint32 {
			switch typ {
			case 3: // SHORT
				return uint32(bo.Uint16(vf[:2]))
			case 4: // LONG
				return bo.Uint32(vf[:])
			}
			return 0
		}
		// Helper: read an array of LONGs from the file. Surfaces ReadFull
		// errors so a truncated file fails loudly instead of silently
		// returning a partial array (which would corrupt the LZW decode).
		readLongArray := func() ([]uint32, error) {
			off2 := int64(bo.Uint32(vf[:]))
			cur, _ := f.Seek(0, io.SeekCurrent)
			defer f.Seek(cur, io.SeekStart) //nolint:errcheck
			if _, err := f.Seek(off2, io.SeekStart); err != nil {
				return nil, err
			}
			out := make([]uint32, count)
			for j := range out {
				var b [4]byte
				if _, err := io.ReadFull(f, b[:]); err != nil {
					return nil, fmt.Errorf("read long-array element %d: %w", j, err)
				}
				out[j] = bo.Uint32(b[:])
			}
			return out, nil
		}

		switch tag {
		case 256: // ImageWidth
			m.imageW = scalar()
		case 257: // ImageLength
			m.imageH = scalar()
		case 259: // Compression
			m.compression = uint16(scalar())
		case 277: // SamplesPerPixel
			m.samplesPerPixel = uint16(scalar())
		case 278: // RowsPerStrip
			m.rowsPerStrip = scalar()
		case 279: // StripByteCounts
			if count == 1 {
				m.stripByteCounts = []uint32{scalar()}
			} else {
				arr, err := readLongArray()
				if err != nil {
					return m, 0, fmt.Errorf("StripByteCounts (tag 279): %w", err)
				}
				m.stripByteCounts = arr
			}
		case 273: // StripOffsets
			if count == 1 {
				m.stripOffsets = []uint32{scalar()}
			} else {
				arr, err := readLongArray()
				if err != nil {
					return m, 0, fmt.Errorf("StripOffsets (tag 273): %w", err)
				}
				m.stripOffsets = arr
			}
		case 317: // Predictor
			m.predictor = uint16(scalar())
		}
	}

	// Default SamplesPerPixel to 3 if absent (Aperio SVS labels are always RGB).
	if m.samplesPerPixel == 0 {
		m.samplesPerPixel = 3
	}

	return m, nextOff, nil
}

// buildMultiStripTIFF builds a minimal little-endian TIFF in memory from the
// given strips. Tags are ordered ascending (required by the TIFF spec). The
// Predictor tag is omitted when predictor == 1 (no prediction). The caller is
// responsible for ensuring samples * 8 = bits per sample.
func buildMultiStripTIFF(strips [][]byte, w, h, samples int, compression uint16, predictor uint16, rowsPerStrip int) []byte {
	// Layout:
	// [0..7]        : header (II, 42, ifdOffset as 4 bytes)
	// [8..8+bpsSize): BitsPerSample array (samples × 2 bytes each)
	// [8+bpsSize..) : strip data, concatenated
	// [after data..) : StripOffsets array (LONG × nStrips)
	// [after offsets): StripByteCounts array (LONG × nStrips)
	// [after counts) : IFD

	bpsSize := uint32(samples * 2) // one SHORT per sample
	bpsOffset := uint32(8)

	// Compute strip offsets within the output buffer.
	nStrips := len(strips)
	stripOffsets := make([]uint32, nStrips)
	stripByteCounts := make([]uint32, nStrips)
	cur := bpsOffset + bpsSize
	for i, s := range strips {
		stripOffsets[i] = cur
		stripByteCounts[i] = uint32(len(s))
		cur += uint32(len(s))
	}

	// Arrays come after all strip data.
	arrBase := cur
	if arrBase%2 != 0 {
		arrBase++
	}
	stripOffsetsArrOff := arrBase
	stripCountsArrOff := arrBase + uint32(nStrips)*4

	// IFD comes after both arrays.
	ifdOffset := stripCountsArrOff + uint32(nStrips)*4
	if ifdOffset%2 != 0 {
		ifdOffset++
	}

	var buf bytes.Buffer
	le16 := func(v uint16) { buf.Write([]byte{byte(v), byte(v >> 8)}) }
	le32 := func(v uint32) { buf.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}) }

	// Header.
	buf.Write([]byte{'I', 'I'})
	le16(42)
	le32(ifdOffset)

	// BitsPerSample array.
	for i := 0; i < samples; i++ {
		le16(8)
	}

	// Strip data.
	for _, s := range strips {
		buf.Write(s)
	}
	// Alignment padding before arrays.
	for uint32(buf.Len()) < arrBase {
		buf.WriteByte(0)
	}

	// StripOffsets array (LONG[nStrips]).
	for _, off := range stripOffsets {
		le32(off)
	}
	// StripByteCounts array (LONG[nStrips]).
	for _, bc := range stripByteCounts {
		le32(bc)
	}
	// Alignment padding before IFD.
	for uint32(buf.Len()) < ifdOffset {
		buf.WriteByte(0)
	}

	// IFD — entries must be in ascending tag order.
	numEntries := uint16(9)
	if predictor > 1 {
		numEntries = 10
	}
	le16(numEntries)

	shortEntry := func(tag, value uint16) {
		le16(tag); le16(3); le32(1); le16(value); le16(0)
	}
	longEntry := func(tag uint16, value uint32) {
		le16(tag); le16(4); le32(1); le32(value)
	}
	arrayEntry := func(tag uint16, typ uint16, count uint32, arrOff uint32) {
		le16(tag); le16(typ); le32(count); le32(arrOff)
	}

	shortEntry(256, uint16(w))                               // ImageWidth
	shortEntry(257, uint16(h))                               // ImageLength
	arrayEntry(258, 3, uint32(samples), bpsOffset)           // BitsPerSample (SHORT[samples])
	shortEntry(259, compression)                             // Compression
	shortEntry(262, 2)                                       // PhotometricInterpretation (RGB)
	arrayEntry(273, 4, uint32(nStrips), stripOffsetsArrOff) // StripOffsets
	shortEntry(277, uint16(samples))                         // SamplesPerPixel
	longEntry(278, uint32(rowsPerStrip))                     // RowsPerStrip
	arrayEntry(279, 4, uint32(nStrips), stripCountsArrOff)  // StripByteCounts
	if predictor > 1 {
		shortEntry(317, predictor) // Predictor
	}
	le32(0) // NextIFD = 0

	return buf.Bytes()
}

func rgbToImage(rgb []byte, w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			si := (y*w + x) * 3
			di := y*img.Stride + x*4
			img.Pix[di+0] = rgb[si+0]
			img.Pix[di+1] = rgb[si+1]
			img.Pix[di+2] = rgb[si+2]
			img.Pix[di+3] = 0xFF
		}
	}
	return img
}
