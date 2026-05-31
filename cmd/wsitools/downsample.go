// Package main: the downsample subcommand wires opentile-go (read), our
// JPEG/JP2K decoders, the resample primitives, the JPEG codec, and the
// streamwriter (write) into a single command that produces a power-of-2-
// downsampled SVS.
//
// v0.1 architecture (intentionally simple, tightening in v0.2):
//
//   - The output L0 is fully materialised in memory as a packed RGB888 buffer.
//     Source L0 tiles are decoded at libjpeg-turbo's fast-scale 1/factor (or
//     full-decoded + Area2x2 for JP2K sources) and pasted into the buffer.
//   - Output L1+ is computed by repeated 2x2 area-average over the previous
//     level's in-memory raster.
//   - For each output level, the raster is re-tiled into 256x256 chunks and
//     encoded via the JPEG codec, written to the output via streamwriter.
//   - Associated images (label, macro, thumbnail/overview) are passed through
//     verbatim via opentile-go's AssociatedImage.Bytes().
//
// Memory: a 40x slide L0 (50K x 30K) needs ~4.5 GB; a 100K x 60K source needs
// ~18 GB. v0.1 accepts this; v0.2 streams the L0 raster in row strips.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	otdecoder "github.com/wsilabs/opentile-go/decoder"
	otresample "github.com/wsilabs/opentile-go/resample"
	codec "github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/pipeline"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

const (
	// outputTileSize is the standard Aperio SVS tile size and what
	// opentile-go's SVS reader expects on re-open.
	outputTileSize = 256
	// bigTIFFThreshold is the predicted output size at which we auto-promote
	// to BigTIFF (8-byte offsets). Classic TIFF tops out at 4 GiB but we
	// promote earlier with safety margin against late-IFD growth.
	bigTIFFThreshold = int64(2) * 1024 * 1024 * 1024
)

var (
	dsOutput     string
	dsFactor     int
	dsTargetMag  int
	dsQuality    int
	dsJobs       int
	dsForce      bool
	dsTileOrder  string
)

var downsampleCmd = &cobra.Command{
	Use:   "downsample [flags] <input>",
	Short: "Downsample a WSI by a power-of-2 factor",
	Long: `Downsample a WSI by an integer power-of-2 factor (default 2 = 40x → 20x).
Regenerates the entire pyramid from the new L0; passes through associated
images (label, macro, thumbnail, overview) verbatim.

v0.1 supports SVS sources only.

Examples:

  # 40x → 20x SVS (defaults)
  wsitools downsample -o slide-20x.svs slide-40x.svs

  # 40x → 10x at higher quality, 8 workers
  wsitools downsample --factor 4 --quality 95 --jobs 8 -o out.svs in.svs`,
	Args:          cobra.ExactArgs(1),
	RunE:          runDownsample,
	SilenceUsage:  true,
	SilenceErrors: false,
}

func init() {
	downsampleCmd.Flags().StringVarP(&dsOutput, "output", "o", "", "output file path (required)")
	downsampleCmd.Flags().IntVar(&dsFactor, "factor", 2, "downsample factor (must be a power of 2 in {2,4,8,16})")
	downsampleCmd.Flags().IntVar(&dsTargetMag, "target-mag", 0, "alternative to --factor: derive factor from source AppMag")
	downsampleCmd.Flags().IntVar(&dsQuality, "quality", 90, "JPEG quality 1..100")
	downsampleCmd.Flags().IntVar(&dsJobs, "jobs", runtime.NumCPU(), "worker goroutines")
	downsampleCmd.Flags().BoolVarP(&dsForce, "force", "f", false, "overwrite output if it exists")
	downsampleCmd.Flags().StringVar(&dsTileOrder, "tile-order", "row-major",
		"Tile emission order within each level (row-major|hilbert|morton). "+
			"Format-restricted: SVS accepts row-major only; COG-WSI / TIFF / OME-TIFF "+
			"accept all three.")
	_ = downsampleCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(downsampleCmd)
}

func runDownsample(cmd *cobra.Command, args []string) error {
	input := args[0]

	// Validation.
	if dsQuality < 1 || dsQuality > 100 {
		return fmt.Errorf("--quality must be in [1, 100], got %d", dsQuality)
	}
	if dsJobs < 1 {
		dsJobs = 1
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if !dsForce {
		if _, err := os.Stat(dsOutput); err == nil {
			return fmt.Errorf("output exists (use --force to overwrite): %s", dsOutput)
		}
	}
	absIn, _ := filepath.Abs(input)
	absOut, _ := filepath.Abs(dsOutput)
	if absIn == absOut {
		return fmt.Errorf("input and output paths are the same")
	}

	// Open source.
	src, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()
	if src.Format() != opentile.FormatSVS {
		return fmt.Errorf("v0.1 downsample supports SVS sources only; got %s", src.Format())
	}

	// Parse source ImageDescription (read raw from TIFF tag 270 of IFD 0;
	// opentile-go's Tiler does not expose the raw description verbatim).
	rawDesc, err := source.ReadSourceImageDescription(input)
	if err != nil {
		return fmt.Errorf("read source ImageDescription: %w", err)
	}
	desc, err := ParseImageDescription(rawDesc)
	if err != nil {
		return fmt.Errorf("parse source ImageDescription: %w", err)
	}

	// Resolve --target-mag if specified (overrides --factor).
	if dsTargetMag > 0 {
		if desc.AppMag <= 0 {
			return fmt.Errorf("--target-mag set but source AppMag is unknown/zero")
		}
		ratio := desc.AppMag / float64(dsTargetMag)
		// Snap to nearest valid power-of-2 factor and verify it matches.
		f := int(ratio + 0.0001)
		if !isValidFactor(f) || float64(f) != ratio {
			return fmt.Errorf("source AppMag %g / target %d = %g is not a valid power-of-2 in {2,4,8,16}", desc.AppMag, dsTargetMag, ratio)
		}
		dsFactor = f
	}
	if !isValidFactor(dsFactor) {
		return fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", dsFactor)
	}

	// Compute output L0 dimensions.
	srcL0 := src.Levels()[0]
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H
	outW := srcW / dsFactor
	outH := srcH / dsFactor
	if outW <= 0 || outH <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outW, outH, dsFactor)
	}

	// Mutate the ImageDescription for the new magnification + geometry.
	desc.MutateForDownsample(dsFactor, uint32(outW), uint32(outH))

	// Predict output size to decide BigTIFF promotion.
	bigtiffMode := tiff.BigTIFFOff
	if predictBigTIFFNeeded(srcL0, src.Levels(), dsFactor) {
		bigtiffMode = tiff.BigTIFFOn
	}

	order, err := tileorder.ByName(dsTileOrder)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	// Open writer (atomic .tmp + rename via streamwriter.Close).
	w, err := streamwriter.Create(dsOutput, streamwriter.Options{
		BigTIFF:          bigtiffMode,
		ImageDescription: desc.Encode(),
		ToolsVersion:     Version,
		SourceFormat:     string(src.Format()),
		FormatName:       "svs",
		AcceptedOrders:   acceptedOrdersForFormat("svs"),
		DefaultOrder:     order,
		MPPX:             desc.MPP,
		MPPY:             desc.MPP,
		Magnification:    desc.AppMag,
	})
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}

	// Build pyramid (Abort the writer + remove the tmp file on any error
	// path; Close is the success path).
	closed := false
	defer func() {
		if !closed {
			w.Abort()
		}
	}()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	slog.Info("starting downsample",
		"input", input,
		"output", dsOutput,
		"factor", dsFactor,
		"quality", dsQuality,
		"jobs", dsJobs,
		"src_w", srcW,
		"src_h", srcH,
		"out_w", outW,
		"out_h", outH,
	)

	// Segregate associated images by Kind so we can interleave them into the
	// IFD stream in Aperio's quirky order:
	//   L0, [thumbnail], L1, L2, …, LN, label, macro
	// opentile-go's SVS classifier takes the LAST 2 trailing pages as
	// label + macro (by SubfileType 9 = macro, else label), so they MUST come
	// after all pyramid levels. thumbnail (non-tiled) belongs between L0 and L1.
	var thumbnail, label, macro opentile.AssociatedImage
	var otherAssoc []opentile.AssociatedImage
	for _, a := range src.Associated() {
		switch a.Type() {
		case "thumbnail":
			thumbnail = a
		case "label":
			label = a
		case "macro", "overview":
			macro = a
		default:
			otherAssoc = append(otherAssoc, a)
		}
	}
	if len(otherAssoc) > 0 {
		var kinds []string
		for _, a := range otherAssoc {
			kinds = append(kinds, a.Type())
		}
		slog.Warn("dropping associated images not supported by Aperio classifier",
			"count", len(otherAssoc),
			"kinds", kinds,
		)
	}

	// postL0Hook is called by buildPyramid immediately after writing IFD 0 (L0)
	// and before writing L1+. This is where we inject the thumbnail so it lands
	// at IFD position 1 — the slot opentile-go's classifier uses for thumbnail.
	postL0Hook := func() error {
		if thumbnail == nil {
			return nil
		}
		return writeOneAssociated(w, thumbnail)
	}

	start := time.Now()
	if err := buildPyramid(ctx, src, w, dsFactor, dsQuality, dsJobs, postL0Hook); err != nil {
		return fmt.Errorf("build pyramid: %w", err)
	}

	// Write label then macro at the end — they must be the last 2 trailing pages
	// so opentile-go's classifyPages picks them up correctly.
	if label != nil {
		if err := writeOneAssociated(w, label); err != nil {
			return fmt.Errorf("write associated label: %w", err)
		}
	}
	if macro != nil {
		if err := writeOneAssociated(w, macro); err != nil {
			return fmt.Errorf("write associated macro/overview: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	closed = true

	elapsed := time.Since(start)

	// Report output file size.
	var outSizeStr string
	if fi, err := os.Stat(dsOutput); err == nil {
		outSizeStr = formatBytes(fi.Size())
	}

	slog.Info("downsample complete",
		"output", dsOutput,
		"elapsed", elapsed.Round(time.Millisecond).String(),
		"output_size", outSizeStr,
	)
	fmt.Printf("wrote %s (%s, %s)\n", dsOutput, outSizeStr, elapsed.Round(time.Millisecond))
	return nil
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// isValidFactor reports whether f is one of {2, 4, 8, 16} (the supported
// libjpeg-turbo fast-scale powers of two for the JPEG path; JP2K path falls
// back to chained Area2x2).
func isValidFactor(f int) bool {
	switch f {
	case 2, 4, 8, 16:
		return true
	}
	return false
}

// predictBigTIFFNeeded estimates whether the output's tile-data + IFD region
// will exceed the classic-TIFF 4 GiB / 32-bit offset ceiling. Heuristic: sum
// of (W*H/factor^2) bytes for an RGB raster across all output levels at JPEG
// compressed roughly 1/8 average → divide by 8. If that exceeds 2 GiB,
// promote to BigTIFF.
func predictBigTIFFNeeded(srcL0 opentile.Level, levels []opentile.Level, factor int) bool {
	var total int64
	for _, l := range levels {
		w := int64(l.Size.W / factor)
		h := int64(l.Size.H / factor)
		// JPEG 1/8 compression ratio rough estimate for RGB888.
		total += w * h * 3 / 8
	}
	return total > bigTIFFThreshold
}

// countTilesForLevel returns the number of 256×256 tiles needed to cover a
// raster of the given dimensions.
func countTilesForLevel(w, h int) int {
	tilesX := (w + outputTileSize - 1) / outputTileSize
	tilesY := (h + outputTileSize - 1) / outputTileSize
	return tilesX * tilesY
}

// buildPyramid materialises the output L0 raster from the source L0 (with
// 1/factor fast-scale decode), then iteratively encodes + writes each output
// pyramid level (256x256 tiled JPEG). L1+ rasters are computed in-memory
// from the previous level via 2x2 area-average.
//
// postL0Hook, if non-nil, is called after writing L0 and before writing L1.
// The caller uses this to inject the thumbnail IFD between L0 and L1 to match
// Aperio's quirky IFD ordering convention.
func buildPyramid(ctx context.Context, src *opentile.Slide, w *streamwriter.Writer, factor, quality, workers int, postL0Hook func() error) error {
	srcLevels := src.Levels()
	srcL0 := srcLevels[0]

	srcW := srcL0.Size.W
	srcH := srcL0.Size.H
	outW := srcW / factor
	outH := srcH / factor

	// Compute total tile count across all output levels upfront for the
	// progress bar.
	nLevels := len(srcLevels)
	var totalTiles int64
	{
		lw, lh := outW, outH
		for lvl := 0; lvl < nLevels; lvl++ {
			totalTiles += int64(countTilesForLevel(lw, lh))
			if lvl < nLevels-1 {
				lw /= 2
				lh /= 2
				if lw == 0 || lh == 0 {
					break
				}
			}
		}
	}

	// Set up progress bar on stderr (suppressed when --quiet).
	var progress *mpb.Progress
	var bar *mpb.Bar
	if !flagQuiet {
		progress = mpb.New(mpb.WithOutput(os.Stderr))
		bar = progress.AddBar(totalTiles,
			mpb.PrependDecorators(
				decor.Name("encoding "),
				decor.Percentage(decor.WCSyncSpace),
			),
			mpb.AppendDecorators(
				decor.EwmaSpeed(0, "%.0f tiles/s", 30),
				decor.Name(" ETA "),
				decor.EwmaETA(decor.ET_STYLE_GO, 30),
			),
		)
	}

	// 1. Materialise output L0 raster from source L0.
	rasterBytes := int64(outW) * int64(outH) * 3
	if rasterBytes < 0 {
		return fmt.Errorf("output L0 raster size overflows int64")
	}
	slog.Debug("materialising output L0 raster", "out_w", outW, "out_h", outH, "raster_mb", rasterBytes/(1024*1024))
	outL0 := make([]byte, rasterBytes)
	if err := materializeOutputL0(ctx, src, srcL0, outL0, outW, outH, factor); err != nil {
		if progress != nil {
			progress.Wait()
		}
		return err
	}

	// 2. Determine output level count: keep one level per source level. For
	// each level, encode the in-memory raster into 256x256 JPEG tiles and
	// write via the wsiwriter level handle. Build the next-level raster via
	// 2x2 area-average.
	currentRaster := outL0
	currentW, currentH := outW, outH

	for outLvl := 0; outLvl < nLevels; outLvl++ {
		lvlStart := time.Now()
		tiles := countTilesForLevel(currentW, currentH)
		slog.Debug("encoding level", "level", outLvl, "w", currentW, "h", currentH, "tiles", tiles)

		if err := encodeAndWriteLevel(ctx, w, currentRaster, currentW, currentH, quality, workers, bar); err != nil {
			if progress != nil {
				progress.Wait()
			}
			return fmt.Errorf("level %d: %w", outLvl, err)
		}

		if flagVerbose {
			slog.Info("encoded level",
				"level", outLvl,
				"w", currentW,
				"h", currentH,
				"tiles", tiles,
				"elapsed", time.Since(lvlStart).Round(time.Millisecond).String(),
			)
		}

		// Aperio IFD ordering: thumbnail goes between L0 and L1.
		if outLvl == 0 && postL0Hook != nil {
			if err := postL0Hook(); err != nil {
				if progress != nil {
					progress.Wait()
				}
				return fmt.Errorf("post-L0 hook: %w", err)
			}
		}

		if outLvl < nLevels-1 {
			// Area2x2 requires even dimensions; pad by 1 if odd so the last
			// row/column is duplicated (cheap mirror padding via simple +1
			// allocation grows). For v0.1 we just truncate to even bounds and
			// drop the last odd row/column — acceptable error of <1 pixel at
			// the slide edge per level.
			evenW := currentW &^ 1
			evenH := currentH &^ 1
			if evenW != currentW || evenH != currentH {
				currentRaster = cropRaster(currentRaster, currentW, currentH, evenW, evenH)
				currentW, currentH = evenW, evenH
			}
			srcImg := &otdecoder.Image{
				Width:  currentW,
				Height: currentH,
				Stride: currentW * 3,
				Format: otdecoder.PixelFormatRGB,
				Pix:    currentRaster,
			}
			dstImg := otdecoder.NewImageFormat(currentW/2, currentH/2, otdecoder.PixelFormatRGB)
			if err := otresample.ImageInto(srcImg, dstImg, otresample.Box); err != nil {
				if progress != nil {
					progress.Wait()
				}
				return fmt.Errorf("Box halving level %d→%d: %w", outLvl, outLvl+1, err)
			}
			currentRaster = dstImg.Pix
			currentW /= 2
			currentH /= 2
			if currentW == 0 || currentH == 0 {
				// No more useful resolution; stop early. (Possible for very
				// shallow source pyramids combined with large factor.)
				break
			}
		}
	}

	if progress != nil {
		progress.Wait()
	}
	return nil
}

// cropRaster returns a fresh RGB888 buffer of size dstW*dstH*3 containing the
// top-left dstW×dstH region of src (which has stride srcW*3). Used to even up
// dimensions before Area2x2.
func cropRaster(src []byte, srcW, srcH, dstW, dstH int) []byte {
	dst := make([]byte, dstW*dstH*3)
	rowBytes := dstW * 3
	for y := 0; y < dstH; y++ {
		copy(dst[y*rowBytes:(y+1)*rowBytes], src[y*srcW*3:y*srcW*3+rowBytes])
	}
	return dst
}

// materializeOutputL0 decodes every source-L0 tile at libjpeg-turbo fast-scale
// 1/factor (JPEG path) or full-decode + chained 2x2 area-average (JP2K path)
// and pastes the result into outL0 at the correct image-space position.
func materializeOutputL0(ctx context.Context, src *opentile.Slide, srcL0 opentile.Level, outL0 []byte, outW, outH, factor int) error {
	srcCompression := srcL0.Compression
	srcGrid := srcL0.Grid
	srcTileW := srcL0.TileSize.W
	srcTileH := srcL0.TileSize.H
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H

	// libjpeg-turbo's scale formula: outDim = ceil(inDim * 1 / factor).
	// For interior tiles this is srcTileW/factor, srcTileH/factor exactly
	// (we choose factors that divide common tile sizes 240/256 cleanly:
	// 240/2=120, 240/4=60, 240/8=30, 240/16=15; same shape for 256).
	jpegFac, jpegOK := otdecoder.Get("jpeg")
	jp2kFac, jp2kOK := otdecoder.Get("jpeg2000")

	tileBuf := make([]byte, src.TileMaxSize(srcL0.Index))

	for ty := 0; ty < srcGrid.H; ty++ {
		for tx := 0; tx < srcGrid.W; tx++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			n, err := src.RawTileInto(srcL0.Index, tx, ty, tileBuf)
			if err != nil {
				return fmt.Errorf("read source tile (%d,%d): %w", tx, ty, err)
			}
			compressed := tileBuf[:n]
			// Compute the image-space destination rect for this source tile.
			// The source tile covers [sx0, sx1) × [sy0, sy1) in source-pixel
			// space, clamped at the image bounds. The corresponding output
			// region is [sx0/factor, sx1/factor) × [sy0/factor, sy1/factor).
			sx0 := tx * srcTileW
			sy0 := ty * srcTileH
			sx1 := sx0 + srcTileW
			sy1 := sy0 + srcTileH
			if sx1 > srcW {
				sx1 = srcW
			}
			if sy1 > srcH {
				sy1 = srcH
			}
			validSrcW := sx1 - sx0
			validSrcH := sy1 - sy0

			var decoded []byte
			var decW, decH int
			switch srcCompression {
			case opentile.CompressionJPEG:
				// Fast-scale decode: tjDecompress produces ceil(srcTileW/factor)
				// × ceil(srcTileH/factor) RGB. The "valid" sub-region inside
				// the decoded tile is ceil(validSrcW/factor) × ceil(validSrcH/factor)
				// — at slide edges, padding pixels follow.
				if !jpegOK {
					return fmt.Errorf("jpeg decoder not registered")
				}
				dec := jpegFac.New()
				defer dec.Close()
				img, err := dec.Decode(compressed, otdecoder.DecodeOptions{
					Scale:  factor,
					Format: otdecoder.PixelFormatRGB,
				})
				if err != nil {
					return fmt.Errorf("decode JPEG tile (%d,%d): %w", tx, ty, err)
				}
				decoded = img.Pix
				decW = (srcTileW + factor - 1) / factor
				decH = (srcTileH + factor - 1) / factor
			case opentile.CompressionJP2K:
				// Full-decode then chained Box-halving factor/2 times.
				if !jp2kOK {
					return fmt.Errorf("jpeg2000 decoder not registered")
				}
				dec := jp2kFac.New()
				defer dec.Close()
				fullImg, err := dec.Decode(compressed, otdecoder.DecodeOptions{
					Scale:  1,
					Format: otdecoder.PixelFormatRGB,
				})
				if err != nil {
					return fmt.Errorf("decode JP2K tile (%d,%d): %w", tx, ty, err)
				}
				decoded, decW, decH, err = downsampleByPowerOf2(fullImg.Pix, srcTileW, srcTileH, factor)
				if err != nil {
					return fmt.Errorf("downsample JP2K tile (%d,%d): %w", tx, ty, err)
				}
			default:
				return fmt.Errorf("unsupported source compression: %s", srcCompression)
			}

			// The valid region inside the decoded tile (in decoded-pixel
			// units): only the pixels corresponding to actual image content,
			// not padding past the slide edge.
			validDecW := (validSrcW + factor - 1) / factor
			validDecH := (validSrcH + factor - 1) / factor
			if validDecW > decW {
				validDecW = decW
			}
			if validDecH > decH {
				validDecH = decH
			}

			// Destination position in the output L0 raster.
			dx := sx0 / factor
			dy := sy0 / factor
			// Clamp to output bounds (defensive: rounding could nudge past
			// outW/outH at the slide edge).
			if dx+validDecW > outW {
				validDecW = outW - dx
			}
			if dy+validDecH > outH {
				validDecH = outH - dy
			}
			pasteIntoRaster(outL0, outW, outH, dx, dy, decoded, decW, validDecW, validDecH)
		}
	}
	return nil
}

// pasteIntoRaster copies the top-left validW×validH region of the decoded RGB
// tile (which has stride decW*3) into the dst raster at position (dx, dy).
// Caller must have clamped validW/validH to fit inside dst.
func pasteIntoRaster(dst []byte, dstW, dstH, dx, dy int, src []byte, srcStrideW, validW, validH int) {
	if validW <= 0 || validH <= 0 {
		return
	}
	rowBytes := validW * 3
	srcStride := srcStrideW * 3
	dstStride := dstW * 3
	for y := 0; y < validH; y++ {
		srcOff := y * srcStride
		dstOff := (dy+y)*dstStride + dx*3
		copy(dst[dstOff:dstOff+rowBytes], src[srcOff:srcOff+rowBytes])
	}
}

// downsampleByPowerOf2 chains Box-halving steps log2(factor) times to produce
// a downsampled RGB buffer. Returns the downsampled bytes and its dimensions.
// Requires factor to be a power of two and srcW/srcH to be even at every
// step (the v0.1 caller ensures this by choosing factor ∈ {2,4,8,16} and
// using source tile sizes that are multiples of 16).
func downsampleByPowerOf2(rgb []byte, srcW, srcH, factor int) ([]byte, int, int, error) {
	cur := rgb
	curW, curH := srcW, srcH
	for f := factor; f > 1; f /= 2 {
		srcImg := &otdecoder.Image{
			Width:  curW,
			Height: curH,
			Stride: curW * 3,
			Format: otdecoder.PixelFormatRGB,
			Pix:    cur,
		}
		dstImg := otdecoder.NewImageFormat(curW/2, curH/2, otdecoder.PixelFormatRGB)
		if err := otresample.ImageInto(srcImg, dstImg, otresample.Box); err != nil {
			return nil, 0, 0, err
		}
		cur = dstImg.Pix
		curW /= 2
		curH /= 2
	}
	return cur, curW, curH, nil
}

// encodeAndWriteLevel encodes the in-memory RGB raster into 256x256 abbreviated
// JPEG tiles and writes them via a streamwriter LevelHandle. All pyramid IFDs
// use NewSubfileType=0 — opentile-go's SVS classifier rejects pyramid levels
// with the reduced bit set. bar may be nil when --quiet is set.
func encodeAndWriteLevel(ctx context.Context, w *streamwriter.Writer, raster []byte, levelW, levelH, quality, workers int, bar *mpb.Bar) error {
	enc, err := jpegcodec.Factory{}.NewEncoder(codec.LevelGeometry{
		TileWidth:   outputTileSize,
		TileHeight:  outputTileSize,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}})
	if err != nil {
		return fmt.Errorf("new encoder: %w", err)
	}
	defer enc.Close()

	tables := enc.LevelHeader()
	lh, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth:      uint32(levelW),
		ImageHeight:     uint32(levelH),
		TileWidth:       outputTileSize,
		TileHeight:      outputTileSize,
		Compression:     tiff.CompressionJPEG,
		Photometric:     2, // RGB (Aperio)
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		JPEGTables:      tables,
		NewSubfileType:  0,
		WSIImageType:    tiff.WSIImageTypePyramid,
	})
	if err != nil {
		return fmt.Errorf("AddLevel: %w", err)
	}

	tilesX := (levelW + outputTileSize - 1) / outputTileSize
	tilesY := (levelH + outputTileSize - 1) / outputTileSize

	source := func(ctx context.Context, emit func(pipeline.Tile) error) error {
		for ty := 0; ty < tilesY; ty++ {
			for tx := 0; tx < tilesX; tx++ {
				tile, err := extractTileFromRaster(raster, levelW, levelH, tx, ty)
				if err != nil {
					return err
				}
				if err := emit(pipeline.Tile{X: uint32(tx), Y: uint32(ty), Bytes: tile}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	process := func(t pipeline.Tile) (pipeline.Tile, error) {
		out, err := enc.EncodeTile(t.Bytes, outputTileSize, outputTileSize, nil)
		if err != nil {
			return pipeline.Tile{}, err
		}
		t.Bytes = out
		return t, nil
	}
	sink := func(t pipeline.Tile) error {
		if err := lh.WriteTile(t.X, t.Y, t.Bytes); err != nil {
			return err
		}
		if bar != nil {
			bar.Increment()
		}
		return nil
	}

	// Run the ordered drain concurrently with pipeline.Run.
	drainErr := make(chan error, 1)
	go func() {
		for {
			idx, bytes, ok, err := lh.NextReady()
			if err != nil {
				drainErr <- err
				return
			}
			if !ok {
				drainErr <- nil
				return
			}
			if err := lh.WriteTileAtIndex(idx, bytes); err != nil {
				lh.Abort(err)
				drainErr <- err
				return
			}
		}
	}()

	pipeErr := pipeline.Run(ctx, pipeline.Config{
		Workers: workers,
		Source:  source,
		Process: process,
		Sink:    sink,
	})

	// Tell the buffer no more tiles will arrive.
	lh.CloseInput()

	// Propagate pipeline error (or wait for drain to finish).
	if pipeErr != nil {
		lh.Abort(pipeErr)
		<-drainErr
		return pipeErr
	}
	return <-drainErr
}

// extractTileFromRaster cuts a 256x256 RGB tile out of the level raster at
// tile coord (tx, ty). Edge tiles are padded with zero where the raster
// doesn't extend that far. Always returns a fresh outputTileSize×outputTileSize
// buffer for the encoder.
func extractTileFromRaster(raster []byte, rasterW, rasterH, tx, ty int) ([]byte, error) {
	tile := make([]byte, outputTileSize*outputTileSize*3)
	x0 := tx * outputTileSize
	y0 := ty * outputTileSize
	if x0 >= rasterW || y0 >= rasterH {
		return tile, nil // empty edge — full zero pad
	}
	copyW := outputTileSize
	if x0+copyW > rasterW {
		copyW = rasterW - x0
	}
	copyH := outputTileSize
	if y0+copyH > rasterH {
		copyH = rasterH - y0
	}
	srcStride := rasterW * 3
	dstStride := outputTileSize * 3
	for y := 0; y < copyH; y++ {
		srcOff := (y0+y)*srcStride + x0*3
		dstOff := y * dstStride
		copy(tile[dstOff:dstOff+copyW*3], raster[srcOff:srcOff+copyW*3])
	}
	return tile, nil
}

// writeOneAssociated writes a single associated image verbatim into the output
// as a single-strip IFD. NewSubfileType is set per the SVS reader classifier
// convention: thumbnail=0, label=1 (reduced bit), overview/macro=9 (reduced +
// macro bit). Compression tag mirrors the source.
func writeOneAssociated(w *streamwriter.Writer, a opentile.AssociatedImage) error {
	bs, err := a.Bytes()
	if err != nil {
		return fmt.Errorf("associated %q bytes: %w", a.Type(), err)
	}
	var subfileType uint32
	var wsiImageType string
	switch a.Type() {
	case "thumbnail":
		subfileType = 0
		wsiImageType = tiff.WSIImageTypeThumbnail
	case "label":
		subfileType = 1
		wsiImageType = tiff.WSIImageTypeLabel
	case "overview":
		subfileType = 9
		wsiImageType = tiff.WSIImageTypeOverview
	case "macro":
		subfileType = 9
		wsiImageType = tiff.WSIImageTypeMacro
	default:
		subfileType = 0
		wsiImageType = tiff.WSIImageTypeAssociated
	}
	comp, photo, err := mapAssociatedCompression(a.Compression())
	if err != nil {
		return fmt.Errorf("associated %q compression: %w", a.Type(), err)
	}
	if err := w.AddStripped(streamwriter.StrippedSpec{
		Width:           uint32(a.Size().W),
		Height:          uint32(a.Size().H),
		RowsPerStrip:    uint32(a.Size().H),
		BitsPerSample:   []uint16{8, 8, 8},
		SamplesPerPixel: 3,
		Photometric:     photo,
		Compression:     comp,
		StripBytes:      bs,
		NewSubfileType:  subfileType,
		WSIImageType:    wsiImageType,
	}); err != nil {
		return fmt.Errorf("AddStripped %q: %w", a.Type(), err)
	}
	return nil
}

// mapAssociatedCompression maps an opentile.Compression to (TIFF tag 259
// value, TIFF tag 262 PhotometricInterpretation). Photometric is RGB (=2)
// for JPEG/LZW/None — Aperio stores label/macro/thumbnail in this shape.
func mapAssociatedCompression(c opentile.Compression) (uint16, uint16, error) {
	switch c {
	case opentile.CompressionJPEG:
		return tiff.CompressionJPEG, 2, nil
	case opentile.CompressionLZW:
		return tiff.CompressionLZW, 2, nil
	case opentile.CompressionNone:
		return tiff.CompressionNone, 2, nil
	default:
		return 0, 0, fmt.Errorf("unsupported associated compression: %s", c)
	}
}
