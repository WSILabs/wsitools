package main

import "github.com/spf13/cobra"

// transcodeCmd re-encodes a WSI's tiles to a different codec in the SAME
// container with the SAME geometry. It is the codec-axis single-op alias of
// convert: it binds convert's cv* flag globals and delegates to runConvert with
// the transform axes neutralized (--to = source format, no crop, no downsample).
var transcodeCmd = &cobra.Command{
	Use:   "transcode --codec <codec> -o <output> [flags] <input>",
	Short: "Re-encode a WSI's tiles to a different codec (same container, same geometry)",
	Long: `transcode re-encodes the tiles of a WSI to a different codec while
preserving the container and the pyramid geometry. It is the codec-axis sibling of
crop (space) and downsample (resolution); to combine axes, use convert.

Examples:

  wsitools transcode --codec avif -o slide.avif.tiff slide.tiff
  wsitools transcode --codec jpeg2000 --quality reversible=true -o out.svs in.svs`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cvTo = ""
		cvRect, cvRectX, cvRectY, cvRectW, cvRectH = "", 0, 0, 0, 0
		cvFactor, cvTargetMag = 1, 0
		return runConvert(cmd, args)
	},
}

func init() {
	transcodeCmd.Flags().StringVar(&cvCodec, "codec", "", "output tile codec (jpeg|jpeg2000|jpegxl|avif|webp|htj2k) (required)")
	transcodeCmd.Flags().StringVar(&cvQuality, "quality", "", "codec quality (codec-specific; comma-separated k=v knobs)")
	transcodeCmd.Flags().StringVarP(&cvOutput, "output", "o", "", "output file path (required)")
	transcodeCmd.Flags().IntVar(&cvWorkers, "workers", 0, "pipeline workers (0 = GOMAXPROCS)")
	transcodeCmd.Flags().IntVar(&cvJobs, "jobs", 0, "alias of --workers")
	transcodeCmd.Flags().BoolVarP(&cvForce, "force", "f", false, "overwrite output if it exists")
	transcodeCmd.Flags().BoolVar(&cvNoAssociated, "no-associated", false, "skip label/macro/thumbnail/overview")
	transcodeCmd.Flags().StringVar(&cvTileOrder, "tile-order", "row-major", "tile emission order (row-major|hilbert|morton)")
	transcodeCmd.Flags().StringVar(&cvBigTIFFFlag, "bigtiff", "auto", "auto|on|off")
	_ = transcodeCmd.MarkFlagRequired("codec")
	_ = transcodeCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(transcodeCmd)
}
