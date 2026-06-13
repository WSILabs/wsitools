package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	"github.com/wsilabs/wsitools/internal/downscale"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

var (
	cropRect      string
	cropX         int
	cropY         int
	cropW         int
	cropH         int
	cropOutput    string
	cropQuality   int
	cropWorkers   int
	cropTileOrder string
	cropBigTIFF   string
	cropForce     bool
	cropNoAssoc   bool
)

var cropCmd = &cobra.Command{
	Use:   "crop [flags] <slide.svs>",
	Short: "Crop a rectangular region of an SVS into a new pyramidal SVS",
	Long: `crop extracts a rectangular region (level-0 pixel coordinates) of a
source SVS and writes a new tiled-pyramid SVS of just that region, anchored at
pixel origin (0,0). Resolution/magnification are preserved; the pyramid is
rebuilt for the cropped extent.

The crop is a full re-encode (one JPEG generation) — it matches how Aperio
ImageScope crops. The ImageDescription records the crop geometry and the
source's provenance chain; the thumbnail is regenerated to the crop aspect;
label, overview, and ICC profile pass through unchanged.`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: false,
	RunE: func(cmd *cobra.Command, args []string) error {
		x, y, w, h, err := resolveRectValues(cmd, cropRect, cropX, cropY, cropW, cropH)
		if err != nil {
			return err
		}
		return runCrop(cmd.Context(), args[0], cropOutput, x, y, w, h, time.Now())
	},
}

func init() {
	cropCmd.Flags().StringVar(&cropRect, "rect", "", "Crop rectangle as X,Y,W,H (level-0 coords)")
	cropCmd.Flags().IntVar(&cropX, "x", 0, "Crop X (level-0 coords)")
	cropCmd.Flags().IntVar(&cropY, "y", 0, "Crop Y (level-0 coords)")
	cropCmd.Flags().IntVar(&cropW, "w", 0, "Crop width (level-0 pixels)")
	cropCmd.Flags().IntVar(&cropH, "h", 0, "Crop height (level-0 pixels)")
	cropCmd.Flags().StringVarP(&cropOutput, "output", "o", "", "Output SVS path (required)")
	cropCmd.Flags().IntVar(&cropQuality, "quality", 0, "JPEG quality 1-100 (default: source Q)")
	cropCmd.Flags().IntVar(&cropWorkers, "workers", 0, "Encode workers (default: NumCPU)")
	cropCmd.Flags().StringVar(&cropTileOrder, "tile-order", "row-major", "Tile order: row-major|hilbert|morton")
	cropCmd.Flags().StringVar(&cropBigTIFF, "bigtiff", "auto", "BigTIFF mode: auto|on|off")
	cropCmd.Flags().BoolVarP(&cropForce, "force", "f", false, "Overwrite existing output")
	cropCmd.Flags().BoolVar(&cropNoAssoc, "no-associated", false, "Skip label/macro/thumbnail/overview")
	rootCmd.AddCommand(cropCmd)
}

// cropPyramidLevels returns the number of pyramid levels (L0 included) to emit
// for an L0 of l0W×l0H, box-halving while both dimensions stay >= tileSize.
func cropPyramidLevels(l0W, l0H, tileSize int) int {
	n := 1
	w, h := l0W, l0H
	for w/2 >= tileSize && h/2 >= tileSize {
		w /= 2
		h /= 2
		n++
	}
	return n
}

// validateCropBounds ensures the rect lies fully inside an l0W×l0H level.
func validateCropBounds(x, y, w, h, l0W, l0H int) error {
	if x < 0 || y < 0 {
		return fmt.Errorf("crop origin must be non-negative (got X=%d Y=%d)", x, y)
	}
	if x+w > l0W {
		return fmt.Errorf("crop right edge X+W=%d exceeds L0 width %d", x+w, l0W)
	}
	if y+h > l0H {
		return fmt.Errorf("crop bottom edge Y+H=%d exceeds L0 height %d", y+h, l0H)
	}
	return nil
}

func runCrop(ctx context.Context, input, output string, x, y, w, h int, start time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if output == "" {
		return fmt.Errorf("--output is required")
	}
	workers := cropWorkers
	if workers == 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if !cropForce {
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("output exists (use --force to overwrite): %s", output)
		}
	}
	absIn, _ := filepath.Abs(input)
	absOut, _ := filepath.Abs(output)
	if absIn == absOut {
		return fmt.Errorf("input and output paths are the same")
	}

	src, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()
	if src.Format() != opentile.FormatSVS {
		return fmt.Errorf("crop supports SVS sources only; got %s", src.Format())
	}

	srcL0 := src.Levels()[0]
	baseW, baseH := srcL0.Size.W, srcL0.Size.H
	if err := validateCropBounds(x, y, w, h, baseW, baseH); err != nil {
		return err
	}

	rawDesc, err := source.ReadSourceImageDescription(input)
	if err != nil {
		return fmt.Errorf("read source ImageDescription: %w", err)
	}
	desc, err := ParseImageDescription(rawDesc)
	if err != nil {
		return fmt.Errorf("parse source ImageDescription: %w", err)
	}

	quality := cropQuality
	if quality == 0 {
		if q, ok := desc.Quality(); ok {
			quality = q
		} else {
			quality = 30
		}
	}
	if quality < 1 || quality > 100 {
		return fmt.Errorf("--quality must be in [1,100], got %d", quality)
	}

	cropDesc := BuildCropImageDescription(rawDesc, baseW, baseH, x, y, w, h, outputTileSize, outputTileSize, quality)

	var bigtiffMode tiff.BigTIFFMode
	switch cropBigTIFF {
	case "on":
		bigtiffMode = tiff.BigTIFFOn
	case "off":
		bigtiffMode = tiff.BigTIFFOff
	default: // auto
		if int64(w)*int64(h)*3 > (int64(4) << 30) {
			bigtiffMode = tiff.BigTIFFOn
		} else {
			bigtiffMode = tiff.BigTIFFOff
		}
	}

	order, err := tileorder.ByName(cropTileOrder)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	wtr, err := streamwriter.Create(output, streamwriter.Options{
		BigTIFF:          bigtiffMode,
		ImageDescription: cropDesc,
		ToolsVersion:     Version,
		SourceFormat:     string(src.Format()),
		FormatName:       "svs",
		AcceptedOrders:   acceptedOrdersForFormat("svs"),
		DefaultOrder:     order,
		MPPX:             desc.MPP,
		MPPY:             desc.MPP,
		Magnification:    desc.AppMag,
		ICCProfile:       src.ICCProfile(),
	})
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			wtr.Abort()
		}
	}()

	rasterBytes := int64(w) * int64(h) * 3
	if rasterBytes < 0 {
		return fmt.Errorf("cropped L0 raster size overflows int64")
	}
	outL0 := make([]byte, rasterBytes)
	if err := downscale.MaterializeCroppedL0(ctx, srcL0, outL0, x, y, w, h); err != nil {
		return fmt.Errorf("materialize cropped L0: %w", err)
	}

	var label, macro opentile.AssociatedImage
	if !cropNoAssoc {
		for _, a := range src.AssociatedImages() {
			switch a.Type() {
			case "label":
				label = a
			case "macro", "overview":
				macro = a
			}
		}
	}

	postL0Hook := func() error {
		if cropNoAssoc {
			return nil
		}
		return regenCropThumbnail(wtr, outL0, w, h, quality)
	}

	nLevels := cropPyramidLevels(w, h, outputTileSize)
	if err := buildPyramidFromRaster(ctx, wtr, outL0, w, h, nLevels, quality, workers, postL0Hook); err != nil {
		return fmt.Errorf("build pyramid: %w", err)
	}

	if label != nil {
		if err := writeOneAssociated(wtr, label); err != nil {
			return fmt.Errorf("write associated label: %w", err)
		}
	}
	if macro != nil {
		if err := writeOneAssociated(wtr, macro); err != nil {
			return fmt.Errorf("write associated macro/overview: %w", err)
		}
	}

	if err := wtr.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	closed = true

	var outSizeStr string
	if fi, err := os.Stat(output); err == nil {
		outSizeStr = formatBytes(fi.Size())
	}
	fmt.Printf("wrote %s (%s) in %s\n", output, outSizeStr, time.Since(start).Round(time.Millisecond))
	return nil
}
