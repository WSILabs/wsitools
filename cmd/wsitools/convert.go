package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

var (
	cvOutput       string
	cvTo           string
	cvForce        bool
	cvBigTIFFFlag  string
	cvNoAssociated bool
	cvTileOrder    string
	cvCodec        string
	cvQuality      string
	cvWorkers      int
	cvJobs         int

	cvDZITileSize int
	cvDZIOverlap  int
	cvDZIFormat   string

	cvLossless bool

	cvFactor    int
	cvTargetMag int

	cvRect  string
	cvRectX int
	cvRectY int
	cvRectW int
	cvRectH int

	cvAllowNonconformant bool
)

var convertCmd = &cobra.Command{
	Use:   "convert [--to <target>] -o <output> [flags] <input>",
	Short: "Convert a WSI to a new container losslessly (tile-copy)",
	Long: `Convert losslessly copies compressed tile bytes from a source WSI
into a new container without decoding or re-encoding. In v0.6 the only
supported target is COG-WSI (--to cog-wsi).

COG-WSI is an extension of Cloud Optimized GeoTIFF for whole-slide images:
header-front IFDs, reverse-order tile data (lowest-resolution overview
first), and an associated-image (label/macro/thumbnail) tail section.

Bit-exact tile-copy promise: for natively-tiled sources (SVS, Philips,
OME-tiled, BIF, IFE, generic-TIFF, COG-WSI, SZI, single-image Leica-SCN),
the source's compressed tile bytes appear verbatim in the destination —
the operation is a pure copy with no re-encoding.

For striped sources (NDPI, OME-OneFrame), the source file has no tile
bytes — only strip bytes. opentile-go synthesizes JPEG tile bytes on
demand by extracting MCU-aligned sub-regions. The COG-WSI output is
reproducible byte-for-byte from the same source (deterministic
synthesis), but the bytes in the output are opentile-go's synthesized
JPEG bytes, not the source's strip bytes.

Examples:

  wsitools convert --to cog-wsi -o slide.cog.tiff slide.svs
  wsitools convert --to cog-wsi --no-associated -o slide.cog.tiff slide.tiff`,
	Args: cobra.ExactArgs(1),
	RunE: runConvert,
}

func init() {
	convertCmd.Flags().StringVarP(&cvOutput, "output", "o", "", "output file path (required)")
	convertCmd.Flags().StringVar(&cvTo, "to", "", "conversion target (cog-wsi|svs|tiff|ome-tiff|dzi|szi|dicom|bif)")
	convertCmd.Flags().BoolVarP(&cvForce, "force", "f", false, "overwrite output if it exists")
	convertCmd.Flags().StringVar(&cvBigTIFFFlag, "bigtiff", "auto", "auto|on|off")
	convertCmd.Flags().BoolVar(&cvNoAssociated, "no-associated", false, "skip label/macro/thumbnail/overview")
	convertCmd.Flags().StringVar(&cvTileOrder, "tile-order", "row-major",
		"Tile emission order within each level (row-major|hilbert|morton). "+
			"Format-restricted: SVS accepts row-major only; COG-WSI / TIFF / OME-TIFF "+
			"accept all three.")
	convertCmd.Flags().StringVar(&cvCodec, "codec", "", "output tile codec (jpeg|jpeg2000|jpegxl|avif|webp|htj2k; jpeg|png for dzi|szi); absent = tile-copy when eligible")
	convertCmd.Flags().StringVar(&cvQuality, "quality", "", "codec quality (codec-specific; comma-separated k=v knobs accepted)")
	convertCmd.Flags().IntVar(&cvWorkers, "workers", 0, "pipeline workers (0 = GOMAXPROCS)")
	convertCmd.Flags().IntVar(&cvJobs, "jobs", 0, "alias of --workers")
	convertCmd.Flags().BoolVar(&cvLossless, "lossless", false, "lossless --to dzi|szi: copy source JPEG base tiles verbatim (no re-encode)")
	convertCmd.Flags().IntVar(&cvFactor, "factor", 1, "downsample factor for svs|tiff|ome-tiff|cog-wsi|dicom|dzi|szi (1 = no scaling; one of {2,4,8,16})")
	convertCmd.Flags().IntVar(&cvTargetMag, "target-mag", 0, "alternative to --factor: derive factor from source AppMag")
	convertCmd.Flags().IntVar(&cvDZITileSize, "dzi-tile-size", 256, "DZI/SZI tile size in pixels")
	convertCmd.Flags().IntVar(&cvDZIOverlap, "dzi-overlap", 1, "DZI/SZI tile overlap pixels on each side")
	convertCmd.Flags().StringVar(&cvDZIFormat, "dzi-format", "jpeg", "DZI/SZI tile codec: jpeg or png")
	convertCmd.Flags().BoolVar(&cvAllowNonconformant, "allow-nonconformant", false, "write a valid-but-non-readable output (e.g. non-jpeg OME-TIFF) with a warning")
	registerRectFlags(convertCmd)
	_ = convertCmd.Flags().MarkDeprecated("dzi-format", "use --codec jpeg|png")
	_ = convertCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(convertCmd)
}

func runConvert(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	input := args[0]
	start := time.Now()
	cvWorkers = resolveWorkers(cvWorkers, cmd.Flags().Changed("workers"), cvJobs, cmd.Flags().Changed("jobs"))

	if cvFactor != 1 || cvTargetMag != 0 {
		if cvFactor != 1 && !isValidFactor(cvFactor) {
			return fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", cvFactor)
		}
	}

	// Infer --to from the source format when the flag is absent.
	if cvTo == "" {
		f, err := opentile.OpenFile(input)
		if err != nil {
			return fmt.Errorf("open source: %w", err)
		}
		srcFormat := string(f.Format())
		_ = f.Close()
		resolved, err := resolveConvertTarget(cvTo, srcFormat)
		if err != nil {
			return err
		}
		cvTo = resolved
	}

	if cvLossless && cvTo != "dzi" && cvTo != "szi" {
		return fmt.Errorf("--lossless is only supported with --to dzi|szi; use `crop --lossless` for the TIFF family")
	}

	codecSet := cmd.Flags().Changed("codec")
	if cvTo == "dzi" || cvTo == "szi" {
		// DZI/SZI: --codec (or the deprecated --dzi-format) selects the tile
		// format; validated to jpeg|png by the resolver in runConvertDZI/SZI.
		if _, err := resolveDZIFormat(cvCodec, codecSet, cvDZIFormat); err != nil {
			return err
		}
	}

	if cvCodec != "" {
		warn, verr := validateCodec(cvTo, cvCodec, cvAllowNonconformant)
		if verr != nil {
			return verr
		}
		if warn != "" {
			fmt.Fprintln(os.Stderr, "warning:", warn)
		}
	}

	if rectFlagsSet(cmd) && cvTo != "dzi" && cvTo != "szi" {
		if err := validateRectCombo(true, cvFactor, cvTargetMag, cvCodec, cvTo); err != nil {
			return err
		}
		f, err := opentile.OpenFile(input)
		if err != nil {
			return fmt.Errorf("open source: %w", err)
		}
		factor, ferr := resolveFactor(source.FromSlide(f, input), input, cvFactor, cvTargetMag)
		_ = f.Close()
		if ferr != nil {
			return ferr
		}
		rx, ry, rw, rh, err := resolveRectValues(cmd, cvRect, cvRectX, cvRectY, cvRectW, cvRectH)
		if err != nil {
			return err
		}
		// convert --rect is always lossy in Phase 1 (--lossless stays a crop flag).
		return runCrop(cmd.Context(), input, cvOutput, rx, ry, rw, rh,
			qualityIntForConvert(), cvWorkers, factor, cvTileOrder, cvBigTIFFFlag, cvForce, cvNoAssociated, false, cvTo, cvCodec, cvQuality, start)
	}

	// Refuse overlapping/stitched sources (BIF) → per-tile targets, which can't
	// composite them (dzi/szi exempt — they use the streaming descent).
	if err := guardStitchedSource(input, cvTo); err != nil {
		return err
	}

	switch cvTo {
	case "cog-wsi":
		return runConvertCOGWSI(cmd, input, start)
	case "svs", "tiff", "ome-tiff":
		return runConvertTIFF(cmd, input, cvTo, start)
	case "dzi":
		return runConvertDZI(cmd, input, start)
	case "szi":
		return runConvertSZI(cmd, input, start)
	case "dicom":
		if cvFactor != 1 || cvTargetMag != 0 {
			if cmd.Flags().Changed("level") {
				return fmt.Errorf("--factor/--target-mag and --level are mutually exclusive (--factor emits the full reduced pyramid)")
			}
			return runConvertFactor(cmd, input, "dicom", start)
		}
		return runConvertDICOM(cmd, input, start)
	case "bif":
		return runConvertBIF(cmd, input, start)
	case "":
		return fmt.Errorf("internal: --to unresolved")
	default:
		return fmt.Errorf("--to %q: unknown target (cog-wsi|svs|tiff|ome-tiff|dzi|szi|dicom|bif)", cvTo)
	}
}

func parseBigTIFFFlag(v string) (cogwsiwriter.BigTIFFMode, error) {
	switch v {
	case "auto":
		return cogwsiwriter.BigTIFFAuto, nil
	case "on":
		return cogwsiwriter.BigTIFFOn, nil
	case "off":
		return cogwsiwriter.BigTIFFOff, nil
	}
	return 0, fmt.Errorf("--bigtiff %q: want auto|on|off", v)
}

// registerRectFlags binds --rect/--x/--y/--w/--h on cmd to the cv* rect globals.
func registerRectFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cvRect, "rect", "", "crop rectangle X,Y,W,H (level-0 coords); crops before container change")
	cmd.Flags().IntVar(&cvRectX, "x", 0, "crop X (level-0 coords; with --y/--w/--h)")
	cmd.Flags().IntVar(&cvRectY, "y", 0, "crop Y (level-0 coords)")
	cmd.Flags().IntVar(&cvRectW, "w", 0, "crop width (level-0 pixels)")
	cmd.Flags().IntVar(&cvRectH, "h", 0, "crop height (level-0 pixels)")
}

// validateRectCombo gates --rect combinations for the crop-emitter targets
// (svs/tiff/ome-tiff/cog-wsi/dicom). --rect composes with --factor/--target-mag
// AND --codec for the crop-emitter targets: one decode/rebuild handles crop +
// downsample + transcode + container change. SVS restricts codec to jpeg
// (guarded in runCrop); dzi/szi handle --rect directly in runConvertDZI/SZI
// and are not routed through here.
func validateRectCombo(rectSet bool, factor, targetMag int, codec, to string) error {
	if !rectSet {
		return nil
	}
	return nil
}

// qualityIntForConvert maps convert's string --quality to runCrop's int quality.
// Empty/unparseable → 0 (runCrop applies its source-Q-for-SVS-else-90 default).
// A bare integer (e.g. "85") is honored.
func qualityIntForConvert() int {
	if cvQuality == "" {
		return 0
	}
	if q, err := strconv.Atoi(strings.TrimSpace(cvQuality)); err == nil {
		return q
	}
	return 0
}

// compressionTagFor maps source.Compression to a TIFF Compression tag value.
func compressionTagFor(c source.Compression) uint16 {
	switch c {
	case source.CompressionJPEG:
		return 7
	case source.CompressionJPEG2000:
		return 33003 // Aperio / OpenJPEG codestream
	case source.CompressionLZW:
		return 5
	case source.CompressionDeflate:
		return 8
	case source.CompressionNone:
		return 1
	}
	// Other codecs (AVIF, WebP, JPEGXL, HTJ2K, Iris): no standardized TIFF tag.
	// Return 0; preflight (Task 10) will surface this as a clean error.
	return 0
}
