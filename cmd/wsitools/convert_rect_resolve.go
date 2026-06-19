package main

import (
	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
)

// rectFlagsSet reports whether any of --rect/--x/--y/--w/--h was provided.
func rectFlagsSet(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("rect") || cmd.Flags().Changed("x") ||
		cmd.Flags().Changed("y") || cmd.Flags().Changed("w") || cmd.Flags().Changed("h")
}

// resolveConvertRect returns the source region for a convert operation: the full
// L0 (srcW×srcH) when no rect flag is set, else the validated crop rect.
func resolveConvertRect(cmd *cobra.Command, srcW, srcH int) (opentile.Region, error) {
	if !rectFlagsSet(cmd) {
		return opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: opentile.Size{W: srcW, H: srcH}}, nil
	}
	rx, ry, rw, rh, err := resolveRectValues(cmd, cvRect, cvRectX, cvRectY, cvRectW, cvRectH)
	if err != nil {
		return opentile.Region{}, err
	}
	if err := validateCropBounds(rx, ry, rw, rh, srcW, srcH); err != nil {
		return opentile.Region{}, err
	}
	return opentile.Region{Origin: opentile.Point{X: rx, Y: ry}, Size: opentile.Size{W: rw, H: rh}}, nil
}
