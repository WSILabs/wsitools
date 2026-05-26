package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// runConvertTIFF dispatches --to svs / tiff / ome-tiff. With --codec
// specified, it invokes the existing transcode re-encode pipeline via
// the helpers in transcode.go. Without --codec, tile-copy applies —
// that path lands in Task 9.
func runConvertTIFF(cmd *cobra.Command, input, target string, start time.Time) error {
	if cvCodec == "" {
		return fmt.Errorf("--codec required for --to %s (tile-copy path lands in Task 9)", target)
	}
	// Re-encode path: delegate to the existing transcode flow.
	return runTranscodeAsConvert(cmd, input, target, cvCodec, cvQuality, cvWorkers, start)
}
