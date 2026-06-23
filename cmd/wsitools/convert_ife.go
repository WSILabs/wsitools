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
// 256px JPEG/AVIF tiles via the streaming retile engine. With no transform
// flags the output L0 matches the source L0; --factor/--target-mag reduce it
// (outL0 = L0/factor, octave-floored pyramid). --rect (crop) is not yet
// supported. ICC, associated images (verbatim JPEG/AVIF or decoded→PNG), and
// free-text attributes are copied into the METADATA sub-blocks.
func runConvertIFE(cmd *cobra.Command, input string, start time.Time) error {
	// --rect (crop) not yet supported for IFE.
	if rectFlagsSet(cmd) {
		return fmt.Errorf("crop not yet supported for --to ife (use --factor for downsample)")
	}

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
	// pyramid tiles straight off the *opentile.Slide).
	src := source.FromSlide(slide, input)

	// Resolve downsample factor (1 = no scaling) and the output L0 dims.
	factor, ferr := resolveFactor(src, input, cvFactor, cvTargetMag)
	if ferr != nil {
		return ferr
	}
	srcSize := slide.Levels()[0].Size
	outW, outH, derr := reducedDims(srcSize.W, srcSize.H, factor)
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

	md := slide.Metadata()
	w, err := ife.Create(cvOutput, ife.Options{
		Encoding:      encByte,
		XExtent:       uint32(outL0.W),
		YExtent:       uint32(outL0.H),
		MPP:           md.MPP.X,
		Magnification: md.Magnification,
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
	if outL0 == srcSize {
		kernel = resample.Nearest // identity (no downscale)
	}
	srcRegion := opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: srcSize}
	runErr := retile.Run(cmd.Context(), retile.Spec{
		Slide: slide, SrcRegion: srcRegion, OutL0: outL0, Levels: levels,
		Kernel: kernel, Encoder: &codecTileEncoder{enc: enc}, Sink: ifeSink{w}, Workers: cvWorkers,
	})
	if runErr != nil {
		w.Abort()
		return fmt.Errorf("retile: %w", runErr)
	}

	// Metadata sub-blocks: ICC, associated images, free-text attributes.
	smd := src.Metadata()
	w.SetICCProfile(smd.ICCProfile) // nil-safe; Writer skips empty
	if !cvNoAssociated {
		addIFEAssociated(w, src)
	}
	w.SetAttributes(buildIFEAttributes(smd, src.Format()))

	if err := w.Finalize(); err != nil {
		return fmt.Errorf("finalize ife: %w", err)
	}

	if !flagQuiet {
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (ife, %d levels) in %s\n",
			cvOutput, len(levels), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// addIFEAssociated copies the source's associated images into the IFE writer's
// IMAGE_ARRAY. JPEG/AVIF blobs are copied verbatim; any other codec (LZW label,
// JPEG2000, etc.) is decoded and re-encoded to lossless PNG. A decode/encode
// failure logs a warning and skips that one image (mirroring the DICOM writer),
// rather than failing the whole conversion.
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
