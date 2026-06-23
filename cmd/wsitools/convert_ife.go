package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	resample "github.com/wsilabs/opentile-go/resample"

	"github.com/wsilabs/wsitools/internal/codec"
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
// supported. Associated images / ICC / attributes are a later task.
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

	// Resolve downsample factor (1 = no scaling) and the output L0 dims.
	factor, ferr := resolveFactor(source.FromSlide(slide, input), input, cvFactor, cvTargetMag)
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

	if err := w.Finalize(); err != nil {
		return fmt.Errorf("finalize ife: %w", err)
	}

	if !flagQuiet {
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (ife, %d levels) in %s\n",
			cvOutput, len(levels), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// Compile-time interface assertion.
var _ retile.TileSink = ifeSink{}
