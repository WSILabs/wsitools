package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	"github.com/wsilabs/wsitools/internal/downscale"
	"github.com/wsilabs/wsitools/internal/source"
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
	cropLossless  bool
)

var cropCmd = &cobra.Command{
	Use:   "crop [flags] <slide>",
	Short: "Crop a rectangular region of a WSI into a new pyramidal file (same container)",
	Long: `crop extracts a rectangular region (level-0 pixel coordinates) of a
source WSI and writes a new tiled-pyramid file of just that region, anchored at
pixel origin (0,0). It is format-preserving: the output is written back to the
source's own container (SVS, OME-TIFF, generic TIFF, or COG-WSI).
Resolution/magnification are preserved; the pyramid is rebuilt for the cropped
extent.

The default crop is a full re-encode (one JPEG generation). For SVS it matches
how Aperio ImageScope crops: the ImageDescription records the crop geometry and
the source's provenance chain, and the thumbnail is regenerated to the crop
aspect. Associated images (label/macro/overview) and the ICC profile pass
through unchanged.

With --lossless, the crop is snapped to the L0 tile grid and the full-resolution
tiles are copied verbatim (byte-identical); the output is a tile-aligned superset
of the requested rect (up to ~255px larger per edge), and the command prints the
effective snapped rect when the input is not already tile-aligned.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true // past arg parsing: runtime errors shouldn't dump the flag wall
		x, y, w, h, err := resolveRectValues(cmd, cropRect, cropX, cropY, cropW, cropH)
		if err != nil {
			return err
		}
		workers := cropWorkers
		return runCrop(cmd.Context(), args[0], cropOutput, x, y, w, h,
			cropQuality, workers, 1, cropTileOrder, cropBigTIFF, cropForce, cropNoAssoc, cropLossless, "", "", "", time.Now())
	},
}

func init() {
	cropCmd.Flags().StringVar(&cropRect, "rect", "", "Crop rectangle as X,Y,W,H (level-0 coords)")
	cropCmd.Flags().IntVar(&cropX, "x", 0, "Crop X (level-0 coords)")
	cropCmd.Flags().IntVar(&cropY, "y", 0, "Crop Y (level-0 coords)")
	cropCmd.Flags().IntVar(&cropW, "w", 0, "Crop width (level-0 pixels)")
	cropCmd.Flags().IntVar(&cropH, "h", 0, "Crop height (level-0 pixels)")
	cropCmd.Flags().StringVarP(&cropOutput, "output", "o", "", "Output path, same container as source (required)")
	cropCmd.Flags().IntVar(&cropQuality, "quality", 0, "JPEG quality 1-100 (default: per-codec, 85 for jpeg)")
	cropCmd.Flags().IntVar(&cropWorkers, "workers", 0, "Encode workers (default: NumCPU)")
	cropCmd.Flags().StringVar(&cropTileOrder, "tile-order", "row-major", "Tile order: row-major|hilbert|morton")
	cropCmd.Flags().StringVar(&cropBigTIFF, "bigtiff", "auto", "BigTIFF mode: auto|on|off")
	cropCmd.Flags().BoolVarP(&cropForce, "force", "f", false, "Overwrite existing output")
	cropCmd.Flags().BoolVar(&cropNoAssoc, "no-associated", false, "Skip label/macro/thumbnail/overview")
	cropCmd.Flags().BoolVar(&cropLossless, "lossless", false, "Lossless crop: snap to the tile grid and copy L0 tiles verbatim (byte-identical; output is a tile-aligned superset of the rect)")
	rootCmd.AddCommand(cropCmd)
}

// snapRectToTiles snaps a requested L0 rect to the tile grid for a lossless
// crop: origin DOWN to the enclosing tile boundary, far edge UP (clamped to the
// image), producing the tile-aligned bounding box of the request. Returns the
// snapped rect, the source tile-coordinate origin of the block (stx0,sty0), and
// the output tile-grid dimensions (outTilesX,outTilesY). A tile-aligned origin
// means output tile (ox,oy) maps 1:1 onto source tile (stx0+ox, sty0+oy).
func snapRectToTiles(x, y, w, h, tileW, tileH, baseW, baseH int) (snapX, snapY, snapW, snapH, stx0, sty0, outTilesX, outTilesY int) {
	snapX = (x / tileW) * tileW
	snapY = (y / tileH) * tileH
	endX := ((x + w + tileW - 1) / tileW) * tileW
	endY := ((y + h + tileH - 1) / tileH) * tileH
	if endX > baseW {
		endX = baseW
	}
	if endY > baseH {
		endY = baseH
	}
	snapW = endX - snapX
	snapH = endY - snapY
	stx0 = snapX / tileW
	sty0 = snapY / tileH
	outTilesX = (snapW + tileW - 1) / tileW
	outTilesY = (snapH + tileH - 1) / tileH
	return
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

// runCrop takes all options as explicit parameters (no global-flag reads),
// mirroring downsampleToSVS, so it stays testable and reusable. The cobra RunE
// closure resolves the flag globals and passes them in.
// codecName selects the tile encoder (empty ⇒ jpeg); qualityStr is the raw
// --quality value (empty ⇒ use quality int as fallback).
func runCrop(ctx context.Context, input, output string, x, y, w, h, quality, workers, factor int, tileOrderName, bigtiffFlag string, force, noAssociated, lossless bool, target, codecName, qualityStr string, start time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if output == "" {
		return fmt.Errorf("--output is required")
	}
	if workers == 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if err := assertRGB8Source(input); err != nil {
		return err
	}
	if !force {
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

	// Crop/downsample-with-rect decode → re-encode → write, so the SOURCE only
	// needs to be readable; the writer comes from --to. The source-format→writer
	// map is therefore only needed to DERIVE a default target when --to is omitted
	// (format-preserving crop). With an explicit --to, proceed on readability alone
	// — this admits readable-but-writerless sources (BIF, IFE) into a supported
	// container. An unsupported explicit target is still rejected by the dispatch
	// switch below. (wsitools#32)
	if target == "" {
		srcTarget, ok := downsampleTargetForFormat(string(src.Format()))
		if !ok {
			return fmt.Errorf("crop: cannot infer an output format for source %q; "+
				"pass --to {svs|ome-tiff|tiff|cog-wsi|dicom}", src.Format())
		}
		target = srcTarget
	}

	if factor < 1 {
		factor = 1
	}
	if factor != 1 && lossless {
		return fmt.Errorf("--lossless cannot be combined with downsampling")
	}

	srcL0 := src.Levels()[0]
	baseW, baseH := srcL0.Size.W, srcL0.Size.H
	if err := validateCropBounds(x, y, w, h, baseW, baseH); err != nil {
		return err
	}
	order, err := tileorder.ByName(tileOrderName)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	// Resolve codec: empty codecName → jpeg. qualityStr is the raw --quality
	// string; when absent, resolveTransformCodec seeds knobs from the per-codec
	// default (q=85 for q-scale codecs). When the caller passes a non-zero int
	// quality (legacy crop command path), encode it as a string so it overrides
	// the per-codec default.
	qualityStrForResolve := qualityStr
	if qualityStrForResolve == "" && quality != 0 {
		qualityStrForResolve = strconv.Itoa(quality)
	}
	// crop only crops — it preserves the source codec (to change codec, use
	// convert). When the source codec has a wsitools encoder (jpeg/jpeg2000/…),
	// re-encode in the SAME codec; only fall back to the jpeg default when it has
	// none (LZW / uncompressed / Deflate ImageScope exports).
	if codecName == "" {
		if c, cerr := reencodeCodecFor(source.CompressionOf(srcL0.Compression), ""); cerr == nil {
			codecName = c
		}
	}
	fac, knobs, resolvedCodec, err := resolveTransformCodec(codecName, qualityStrForResolve)
	if err != nil {
		return err
	}
	// Quality floor: when the user gave no --quality (quality == 0), the default
	// is a floor — honor a source whose own quality is higher.
	if quality == 0 {
		knobs = withSourceQualityFloor(knobs, src)
	}

	// SVS guard: SVS emitters support jpeg and jpeg2000 only (Aperio format
	// constraint). Reject any other explicit --codec so the user gets a clear
	// error instead of a silent fallback.
	if target == "svs" && codecName != "" && codecName != "jpeg" && codecName != "jpeg2000" {
		return fmt.Errorf("SVS crop/downsample supports jpeg or jpeg2000; use --to tiff for %s", codecName)
	}

	// Re-encode quality: derive from resolved knobs (85 by default, or the
	// value set by --quality / the legacy int quality param).
	q := qFromKnobs(knobs)
	if q < 1 || q > 100 {
		return fmt.Errorf("--quality must be in [1,100], got %d", q)
	}

	// Effective rect: lossless snaps to the tile grid; re-encode uses exact rect.
	ex, ey, ew, eh := x, y, w, h
	var stx0, sty0, outTilesX, outTilesY int
	if lossless {
		ex, ey, ew, eh, stx0, sty0, outTilesX, outTilesY = snapRectToTiles(x, y, w, h, srcL0.TileSize.W, srcL0.TileSize.H, baseW, baseH)
		if ex != x || ey != y || ew != w || eh != h {
			infof("lossless: snapped crop to %d,%d %dx%d (tile-aligned)\n", ex, ey, ew, eh)
		}
	}
	// The raster L0 is only needed by the lossless rebuild (lower levels +
	// thumbnail + lossless DICOM L0). The lossy engine targets (tiff/ome-tiff/
	// cog-wsi/dicom) stream from the source and never touch p.l0, so skip
	// materialization there.
	var outL0 []byte
	if lossless {
		rasterBytes := int64(ew) * int64(eh) * 3
		if rasterBytes < 0 {
			return fmt.Errorf("cropped L0 raster size overflows int64")
		}
		outL0 = make([]byte, rasterBytes)
		if err := downscale.MaterializeCroppedL0(ctx, srcL0, outL0, ex, ey, ew, eh); err != nil {
			return fmt.Errorf("materialize cropped L0: %w", err)
		}
	}
	outW, outH := outDimsForFactor(ew, eh, factor)
	if outW <= 0 || outH <= 0 {
		return fmt.Errorf("--factor %d too large for crop extent %dx%d", factor, ew, eh)
	}
	outTile := resolveTileSize(srcL0.TileSize.W, cvTileSize)
	nLevels := flooredLevelCount(outW, outH, outTile)

	p := cropEmitParams{
		ctx: ctx, src: src, srcL0: srcL0, input: input, output: output,
		l0: outL0, l0W: ew, l0H: eh, ex: ex, ey: ey, nLevels: nLevels, quality: q, workers: workers,
		order: order, bigtiffFlag: bigtiffFlag, noAssociated: noAssociated, force: force,
		factor: factor, outW: outW, outH: outH,
		lossless: lossless, stx0: stx0, sty0: sty0, outTilesX: outTilesX, outTilesY: outTilesY,
		start: start,
		fac:   fac, knobs: knobs, codecName: resolvedCodec, outTile: outTile,
	}
	switch target {
	case "svs":
		return cropToSVS(p)
	case "tiff":
		return cropToTIFF(p)
	case "ome-tiff":
		return cropToOMETIFF(p)
	case "cog-wsi":
		return cropToCOGWSI(p)
	case "dicom":
		return cropToDICOM(p)
	default:
		return fmt.Errorf("crop: target %q not implemented", target)
	}
}
