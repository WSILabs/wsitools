package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	cvOutput string
	cvTo     string
	cvForce  bool
)

var convertCmd = &cobra.Command{
	Use:   "convert --to <target> -o <output> [flags] <input>",
	Short: "Convert a WSI to a new container losslessly (tile-copy)",
	Long: `Convert losslessly copies compressed tile bytes from a source WSI
into a new container without decoding or re-encoding. In v0.6 the only
supported target is COG-WSI (--to cog-wsi).

COG-WSI is an extension of Cloud Optimized GeoTIFF for whole-slide images:
header-front IFDs, reverse-order tile data (lowest-resolution overview
first), and an associated-image (label/macro/thumbnail) tail section.`,
	Args: cobra.ExactArgs(1),
	RunE: runConvert,
}

func init() {
	convertCmd.Flags().StringVarP(&cvOutput, "output", "o", "", "output file path (required)")
	convertCmd.Flags().StringVar(&cvTo, "to", "", "conversion target (only 'cog-wsi' in v0.6)")
	convertCmd.Flags().BoolVarP(&cvForce, "force", "f", false, "overwrite output if it exists")
	_ = convertCmd.MarkFlagRequired("output")
	_ = convertCmd.MarkFlagRequired("to")
	rootCmd.AddCommand(convertCmd)
}

func runConvert(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	return fmt.Errorf("convert: not yet implemented")
}
