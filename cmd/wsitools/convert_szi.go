package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	opentile "github.com/wsilabs/opentile-go"

	"github.com/wsilabs/wsitools/internal/dzi"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/szi"
)

// runConvertSZI emits an SZI archive (ZIP-wrapped DZI pyramid) to cvOutput.
func runConvertSZI(cmd *cobra.Command, input string, start time.Time) error {
	// Need both: opentile slide (for ReadRegionScaled) + source wrapper (for Metadata).
	slide, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open slide: %w", err)
	}
	defer slide.Close()
	src, err := source.Open(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("%s exists (use --force)", cvOutput)
		}
	}
	f, err := os.Create(cvOutput)
	if err != nil {
		return err
	}
	defer f.Close()

	name := strings.TrimSuffix(filepath.Base(cvOutput), ".szi")

	images := slide.Pyramids()
	if len(images) == 0 || len(images[0].Levels) == 0 {
		return fmt.Errorf("slide has no pyramid levels")
	}
	l0 := images[0].Levels[0]
	srcW, srcH := l0.Size.W, l0.Size.H
	factor, err := resolveFactor(src, input, cvFactor, cvTargetMag)
	if err != nil {
		return err
	}
	outW, outH, err := reducedDims(srcW, srcH, factor)
	if err != nil {
		return err
	}

	dziFormat, err := resolveDZIFormat(cvCodec, cmd.Flags().Changed("codec"), cvDZIFormat, cmd.Flags().Changed("dzi-format"))
	if err != nil {
		return err
	}
	w, err := szi.NewWriter(f, szi.Config{
		Name: name, Width: outW, Height: outH,
		Format: dziFormat, TileSize: cvDZITileSize, Overlap: cvDZIOverlap,
	})
	if err != nil {
		return err
	}
	cfg := dzi.Config{
		Name: name, Width: outW, Height: outH,
		Format: dziFormat, TileSize: cvDZITileSize, Overlap: cvDZIOverlap,
	}
	if err := emitDZIPyramid(cmd.Context(), slide, w, cfg, srcW, srcH); err != nil {
		return err
	}
	if err := w.WriteScanProperties(src.Metadata()); err != nil {
		return err
	}
	if err := writeAssociatedPNGs(src, w.WriteAssociated); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%s)\n", cvOutput, time.Since(start).Round(time.Millisecond))
	return nil
}
