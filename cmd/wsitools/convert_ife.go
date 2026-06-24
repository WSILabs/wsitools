package main

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	_ "github.com/wsilabs/opentile-go/formats/all"
	resample "github.com/wsilabs/opentile-go/resample"

	"github.com/wsilabs/wsitools/internal/codec"
	pngcodec "github.com/wsilabs/wsitools/internal/codec/png"
	"github.com/wsilabs/wsitools/internal/ife"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
)

// ifeSink adapts ife.Writer to retile.TileSink. The engine emits (level,col,row)
// finest-first; ife.Writer.WriteTile uses the same native-first convention
// (apiLevel 0 = native), so the engine's level index maps straight through.
type ifeSink struct{ w *ife.Writer }

func (s ifeSink) WriteTile(level, col, row int, encoded []byte) error {
	// Copy: the engine may reuse encoded's backing array after WriteTile returns,
	// and ife.Writer.WriteTile writes synchronously but does not retain the slice —
	// however the sink drainer is single-threaded so a copy is the safe contract.
	blob := make([]byte, len(encoded))
	copy(blob, encoded)
	return s.w.WriteTile(level, col, row, blob)
}

// runConvertIFE writes an Iris File Extension (IFE) v1.0 file from any
// opentile-readable source. The whole pyramid is decoded and re-encoded to
// 256px JPEG/AVIF tiles via the streaming retile engine, composing crop and
// downsample in one pass: --rect X,Y,W,H selects the source region, and
// --factor/--target-mag reduces it (outL0 = region/factor, octave-floored
// pyramid); with neither, the output L0 matches the source L0. MPP/mag scale
// with --factor (×factor / ÷factor); a pure crop preserves them. ICC, associated
// images (verbatim JPEG/AVIF or decoded→PNG), and free-text attributes are copied
// into the METADATA sub-blocks.
func runConvertIFE(cmd *cobra.Command, input string, start time.Time) error {
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", cvOutput)
		}
	}

	// Resolve codec: empty → jpeg. validateCodec already ran in runConvert for a
	// non-empty cvCodec; gate the IFE-carriable set explicitly here too.
	codecName := cvCodec
	if codecName == "" {
		codecName = "jpeg"
	}
	if _, ok := ife.EncodingFor(codecName); !ok {
		return fmt.Errorf("convert --to ife: --codec %q not supported; IFE tiles are jpeg or avif", codecName)
	}

	slide, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer slide.Close()

	// internal/source view over the same opentile slide — supplies associated
	// images, ICC, and Raw metadata for the METADATA sub-blocks (engine reads
	// pyramid tiles straight off the *opentile.Slide), and the verbatim
	// tile-copy fast path reads raw compressed tiles off it.
	src := source.FromSlide(slide, input)

	// Verbatim fast path: a 256px-tiled JPEG/AVIF source with no transform copies
	// tile-for-tile into the IFE (byte-identical, no decode/re-encode).
	if ifeVerbatimEligible(src, cvCodec, cvFactor, cvTargetMag, rectFlagsSet(cmd)) {
		return runConvertIFEVerbatim(cmd, src, slide, start)
	}

	// Resolve downsample factor (1 = no scaling).
	factor, ferr := resolveFactor(src, input, cvFactor, cvTargetMag)
	if ferr != nil {
		return ferr
	}
	srcSize := slide.Levels()[0].Size

	// Source region: the full L0, or the --rect crop (in L0 coords).
	rx, ry, rw, rh := 0, 0, srcSize.W, srcSize.H
	if rectFlagsSet(cmd) {
		var rerr error
		rx, ry, rw, rh, rerr = resolveRectValues(cmd, cvRect, cvRectX, cvRectY, cvRectW, cvRectH)
		if rerr != nil {
			return rerr
		}
		if err := validateCropBounds(rx, ry, rw, rh, srcSize.W, srcSize.H); err != nil {
			return err
		}
	}
	srcRegion := opentile.Region{Origin: opentile.Point{X: rx, Y: ry}, Size: opentile.Size{W: rw, H: rh}}

	// Output L0 = the (cropped) region reduced by factor.
	outW, outH, derr := reducedDims(rw, rh, factor)
	if derr != nil {
		return derr
	}
	outL0 := opentile.Size{W: outW, H: outH}

	levels := octaveLevelSpecsFor(outL0, outputTileSize)

	// Build the tile encoder for the resolved codec.
	fac, knobs, resolvedName, rerr := resolveTransformCodec(codecName, cvQuality)
	if rerr != nil {
		return rerr
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{
		TileWidth: outputTileSize, TileHeight: outputTileSize, PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: knobs})
	if err != nil {
		return fmt.Errorf("new encoder: %w", err)
	}
	defer enc.Close()

	encByte, ok := ife.EncodingFor(resolvedName)
	if !ok {
		return fmt.Errorf("convert --to ife: resolved codec %q not carriable by IFE", resolvedName)
	}

	// Crop preserves resolution; --factor scales it (MPP ×factor, mag ÷factor).
	md := slide.Metadata()
	outMPP := md.MPP.X * float64(factor)
	outMag := md.Magnification / float64(factor) // factor ≥ 1; 0 mag stays 0
	w, err := ife.Create(cvOutput, ife.Options{
		Encoding:      encByte,
		XExtent:       uint32(outL0.W),
		YExtent:       uint32(outL0.H),
		MPP:           outMPP,
		Magnification: outMag,
	})
	if err != nil {
		return fmt.Errorf("create ife: %w", err)
	}

	// Register levels native-first (engine LevelSpec is finest-first; Index 0 =
	// native), matching ife.Writer's native-first AddLevel convention.
	for _, ls := range levels {
		w.AddLevel(uint32(ls.Cols), uint32(ls.Rows))
	}

	kernel := resample.Box
	if outL0 == srcRegion.Size {
		kernel = resample.Nearest // identity (crop only, no downscale)
	}
	runErr := retile.Run(cmd.Context(), retile.Spec{
		Slide: slide, SrcRegion: srcRegion, OutL0: outL0, Levels: levels,
		Kernel: kernel, Encoder: &codecTileEncoder{enc: enc}, Sink: ifeSink{w}, Workers: cvWorkers,
	})
	if runErr != nil {
		w.Abort()
		return fmt.Errorf("retile: %w", runErr)
	}

	// Metadata sub-blocks: ICC, associated images, free-text attributes.
	assembleIFEMetadata(w, src)

	if err := w.Finalize(); err != nil {
		return fmt.Errorf("finalize ife: %w", err)
	}

	if !flagQuiet {
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (ife, %d levels) in %s\n",
			cvOutput, len(levels), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// ifeVerbatimEligible reports whether src's pyramid can be copied tile-for-tile
// into IFE without re-encoding: no transform, no codec override, and every level
// is 256px-tiled, non-overlapping, JPEG or AVIF.
func ifeVerbatimEligible(src source.Source, codecOverride string, factor, targetMag int, rectSet bool) bool {
	if factor != 1 || targetMag != 0 || rectSet || codecOverride != "" {
		return false
	}
	levels := src.Levels()
	if len(levels) == 0 {
		return false
	}
	// IFE's TILE_TABLE.encoding is per-FILE, so the whole pyramid must share one
	// codec; the encoding byte is derived from L0. Reject a (hypothetical)
	// mixed-codec pyramid, which would write JPEG/AVIF tiles under a single header.
	l0Codec := levels[0].Compression()
	for _, lvl := range levels {
		ts := lvl.TileSize()
		if ts.X != 256 || ts.Y != 256 || lvl.Overlapping() {
			return false
		}
		if lvl.Compression() != l0Codec {
			return false
		}
		if lvl.Compression() != source.CompressionJPEG && lvl.Compression() != source.CompressionAVIF {
			return false
		}
	}
	return true
}

// runConvertIFEVerbatim writes the IFE by copying the source's compressed pyramid
// tiles byte-for-byte (the lossless fast path). The Encoding byte comes from the
// source L0 codec; L0 extents are the source L0 size (no downsample/padding).
// Metadata assembly (ICC/associated/attributes) is shared with the engine path.
func runConvertIFEVerbatim(cmd *cobra.Command, src source.Source, slide *opentile.Slide, start time.Time) error {
	srcLevels := src.Levels()
	// Encoding byte from the source L0 codec → wsitools codec name → IFE encoding.
	var srcCodecName string
	switch srcLevels[0].Compression() {
	case source.CompressionAVIF:
		srcCodecName = "avif"
	default:
		srcCodecName = "jpeg"
	}
	encByte, ok := ife.EncodingFor(srcCodecName)
	if !ok {
		return fmt.Errorf("convert --to ife: source codec %q not carriable by IFE", srcCodecName)
	}

	l0 := srcLevels[0].Size()
	md := slide.Metadata()
	w, err := ife.Create(cvOutput, ife.Options{
		Encoding:      encByte,
		XExtent:       uint32(l0.X),
		YExtent:       uint32(l0.Y),
		MPP:           md.MPP.X,
		Magnification: md.Magnification,
	})
	if err != nil {
		return fmt.Errorf("create ife: %w", err)
	}

	if err := writeIFEVerbatim(w, src); err != nil {
		w.Abort()
		return err
	}

	// Metadata sub-blocks: ICC, associated images, free-text attributes.
	assembleIFEMetadata(w, src)

	if err := w.Finalize(); err != nil {
		return fmt.Errorf("finalize ife: %w", err)
	}

	if !flagQuiet {
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (ife, %d levels, verbatim tile-copy) in %s\n",
			cvOutput, len(srcLevels), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// writeIFEVerbatim copies every source level's compressed tiles verbatim into the
// IFE writer. Levels are registered native-first (matching ife.Writer's and the
// engine's convention).
func writeIFEVerbatim(w *ife.Writer, src source.Source) error {
	for _, lvl := range src.Levels() {
		grid := lvl.Grid()
		w.AddLevel(uint32(grid.X), uint32(grid.Y)) // native-first; matches engine ordering
		buf := make([]byte, lvl.TileMaxSize())
		for row := 0; row < grid.Y; row++ {
			for col := 0; col < grid.X; col++ {
				n, err := lvl.TileInto(col, row, buf)
				if err != nil {
					return fmt.Errorf("ife verbatim: level %d tile %d,%d: %w", lvl.Index(), col, row, err)
				}
				blob := make([]byte, n)
				copy(blob, buf[:n]) // buf is reused across tiles
				if err := w.WriteTile(lvl.Index(), col, row, blob); err != nil {
					return fmt.Errorf("ife verbatim: level %d tile %d,%d: %w", lvl.Index(), col, row, err)
				}
			}
		}
	}
	return nil
}

// assembleIFEMetadata writes the shared METADATA sub-blocks (ICC, associated
// images, free-text attributes) into w from src. Used by both the engine and
// verbatim pyramid paths.
func assembleIFEMetadata(w *ife.Writer, src source.Source) {
	smd := src.Metadata()
	w.SetICCProfile(smd.ICCProfile) // nil-safe; Writer skips empty
	if !cvNoAssociated {
		addIFEAssociated(w, src)
	}
	w.SetAttributes(buildIFEAttributes(smd, src.Format()))
}

// addIFEAssociated copies the source's associated images into the IFE writer's
// IMAGE_ARRAY. JPEG/AVIF blobs are copied verbatim; any other codec (LZW label,
// JPEG2000, etc.) is decoded and re-encoded to lossless PNG (encoding=1) so the
// label stays lossless (crisp barcodes). A decode/encode failure logs a warning
// and skips that one image (mirroring the DICOM writer), rather than failing the
// whole conversion. PNG associated images round-trip through opentile-go ≥ v0.49.0
// (#74); JPEG/AVIF associated images round-trip on any version.
func addIFEAssociated(w *ife.Writer, src source.Source) {
	for _, a := range src.Associated() {
		size := a.Size()
		var (
			blob []byte
			enc  uint8
		)
		switch a.Compression() {
		case source.CompressionJPEG:
			b, err := a.Bytes()
			if err != nil {
				slog.Warn("skipping associated image (bytes failed)", "type", a.Type(), "err", err)
				continue
			}
			blob, enc = b, ife.ImgEncJPEG
		case source.CompressionAVIF:
			b, err := a.Bytes()
			if err != nil {
				slog.Warn("skipping associated image (bytes failed)", "type", a.Type(), "err", err)
				continue
			}
			blob, enc = b, ife.ImgEncAVIF
		default:
			// Decode (opentile owns codec/predictor) → re-encode lossless PNG.
			di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
			if err != nil {
				slog.Warn("skipping associated image (decode failed)", "type", a.Type(), "err", err)
				continue
			}
			pngEnc, err := pngcodec.Factory{}.NewEncoder(
				codec.LevelGeometry{TileWidth: di.Width, TileHeight: di.Height, PixelFormat: codec.PixelFormatRGB8},
				codec.Quality{})
			if err != nil {
				slog.Warn("skipping associated image (png encoder)", "type", a.Type(), "err", err)
				continue
			}
			b, err := pngEnc.EncodeTile(tightIFERGB(di), di.Width, di.Height, nil)
			pngEnc.Close()
			if err != nil {
				slog.Warn("skipping associated image (png encode failed)", "type", a.Type(), "err", err)
				continue
			}
			blob, enc = b, ife.ImgEncPNG
			// Decoded dims are authoritative.
			size.X, size.Y = di.Width, di.Height
		}
		// Title MUST be the lowercase type so the reader round-trips it back to the
		// AssociatedLabel/Macro/... taxonomy.
		w.AddAssociated(strings.ToLower(a.Type()), uint32(size.X), uint32(size.Y), enc, blob)
	}
}

// tightIFERGB packs a decoder.Image to a tight Height*Width*3 RGB buffer,
// stripping any SIMD row-stride padding.
func tightIFERGB(di *decoder.Image) []byte {
	rowBytes := di.Width * 3
	if di.Stride == rowBytes {
		return di.Pix[:di.Height*rowBytes]
	}
	out := make([]byte, di.Height*rowBytes)
	for y := 0; y < di.Height; y++ {
		copy(out[y*rowBytes:(y+1)*rowBytes], di.Pix[y*di.Stride:y*di.Stride+rowBytes])
	}
	return out
}

// buildIFEAttributes assembles the free-text attribute key/value pairs (sorted by
// key for deterministic output) from the source Raw map plus synthesized
// provenance keys (wsitools version, source format, scanner make/model/etc.).
func buildIFEAttributes(md source.Metadata, srcFormat string) [][2]string {
	kvs := map[string]string{}
	for k, v := range md.Raw {
		kvs[k] = v
	}
	kvs["wsitools-version"] = Version
	kvs["source-format"] = srcFormat
	if md.Make != "" {
		kvs["scanner-make"] = md.Make
	}
	if md.Model != "" {
		kvs["scanner-model"] = md.Model
	}
	if md.Software != "" {
		kvs["scanner-software"] = md.Software
	}
	if md.SerialNumber != "" {
		kvs["scanner-serial-number"] = md.SerialNumber
	}
	keys := make([]string, 0, len(kvs))
	for k := range kvs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, [2]string{k, kvs[k]})
	}
	return out
}

// Compile-time interface assertion.
var _ retile.TileSink = ifeSink{}
