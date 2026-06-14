package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

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

	cvDZITileSize int
	cvDZIOverlap  int
	cvDZIFormat   string

	cvFactor    int
	cvTargetMag int
)

var convertCmd = &cobra.Command{
	Use:   "convert --to <target> -o <output> [flags] <input>",
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
	convertCmd.Flags().StringVar(&cvTo, "to", "", "conversion target (cog-wsi|svs|tiff|ome-tiff|dzi|szi|dicom)")
	convertCmd.Flags().BoolVarP(&cvForce, "force", "f", false, "overwrite output if it exists")
	convertCmd.Flags().StringVar(&cvBigTIFFFlag, "bigtiff", "auto", "auto|on|off")
	convertCmd.Flags().BoolVar(&cvNoAssociated, "no-associated", false, "skip label/macro/thumbnail/overview")
	convertCmd.Flags().StringVar(&cvTileOrder, "tile-order", "row-major",
		"Tile emission order within each level (row-major|hilbert|morton). "+
			"Format-restricted: SVS accepts row-major only; COG-WSI / TIFF / OME-TIFF "+
			"accept all three.")
	convertCmd.Flags().StringVar(&cvCodec, "codec", "", "output tile codec (jpeg|jpegxl|avif|webp|htj2k); absent = tile-copy when eligible")
	convertCmd.Flags().StringVar(&cvQuality, "quality", "", "codec quality (codec-specific; comma-separated k=v knobs accepted)")
	convertCmd.Flags().IntVar(&cvWorkers, "workers", 0, "pipeline workers (0 = GOMAXPROCS)")
	convertCmd.Flags().IntVar(&cvFactor, "factor", 1, "downsample factor for svs|tiff|ome-tiff|cog-wsi (1 = no scaling; one of {2,4,8,16})")
	convertCmd.Flags().IntVar(&cvTargetMag, "target-mag", 0, "alternative to --factor: derive factor from source AppMag")
	convertCmd.Flags().IntVar(&cvDZITileSize, "dzi-tile-size", 256, "DZI/SZI tile size in pixels")
	convertCmd.Flags().IntVar(&cvDZIOverlap, "dzi-overlap", 1, "DZI/SZI tile overlap pixels on each side")
	convertCmd.Flags().StringVar(&cvDZIFormat, "dzi-format", "jpeg", "DZI/SZI tile codec: jpeg or png")
	_ = convertCmd.MarkFlagRequired("output")
	_ = convertCmd.MarkFlagRequired("to")
	rootCmd.AddCommand(convertCmd)
}

func runConvert(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	input := args[0]
	start := time.Now()

	if cvFactor != 1 || cvTargetMag != 0 {
		if cvTo == "dzi" || cvTo == "szi" {
			return fmt.Errorf("--factor/--target-mag not supported for --to %s (yet)", cvTo)
		}
		if cvFactor != 1 && !isValidFactor(cvFactor) {
			return fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", cvFactor)
		}
	}

	if (cvTo == "dzi" || cvTo == "szi") && cvCodec != "" {
		return fmt.Errorf("--codec is not valid with --to %s (use --dzi-format)", cvTo)
	}
	if (cvTo == "dzi" || cvTo == "szi") && cvDZIFormat != "jpeg" && cvDZIFormat != "png" {
		return fmt.Errorf("--dzi-format must be jpeg or png, got %q", cvDZIFormat)
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
	case "":
		return fmt.Errorf("--to is required")
	default:
		return fmt.Errorf("--to %q: unknown target (cog-wsi|svs|tiff|ome-tiff|dzi|szi|dicom)", cvTo)
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
