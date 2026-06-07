package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

func runConvertCOGWSI(cmd *cobra.Command, input string, start time.Time) error {
	// --factor / --target-mag: reduce-then-rebuild via runConvertFactor.
	if cvFactor != 1 || cvTargetMag != 0 {
		return runConvertFactor(cmd, input, "cog-wsi", start)
	}

	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", cvOutput)
		}
	}
	bigTIFFMode, err := parseBigTIFFFlag(cvBigTIFFFlag)
	if err != nil {
		return err
	}

	order, err := tileorder.ByName(cvTileOrder)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	src, err := source.Open(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	for _, lvl := range src.Levels() {
		if compressionTagFor(lvl.Compression()) == 0 {
			return fmt.Errorf("level %d: source compression %s has no standard TIFF Compression tag; cannot tile-copy",
				lvl.Index(), lvl.Compression())
		}
	}
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}

	md := src.Metadata()
	opts := cogwsiwriter.Options{
		BigTIFF:      bigTIFFMode,
		ToolsVersion: Version,
		DefaultOrder: order,
		Metadata: cogwsiwriter.Metadata{
			MPPX:                md.MPPX,
			MPPY:                md.MPPY,
			Magnification:       md.Magnification,
			ICCProfile:          md.ICCProfile,
			Make:                md.Make,
			Model:               md.Model,
			Software:            md.Software,
			AcquisitionDateTime: md.AcquisitionDateTime,
			SourceFormat:        src.Format(),
			SourceImageDesc:     fmt.Sprintf("wsitools/%s convert source=%s", Version, src.Format()),
		},
	}

	w, err := cogwsiwriter.Create(cvOutput, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}

	plan := assocEditPlan{dropAll: cvNoAssociated}
	if err := writeCOGWSI(w, src, plan); err != nil {
		w.Abort()
		return err
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}

	if stat, _ := os.Stat(cvOutput); stat != nil {
		slog.Info("convert complete",
			"output", cvOutput,
			"size", formatBytes(stat.Size()),
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		fmt.Printf("wrote %s (%s, %s)\n", cvOutput, formatBytes(stat.Size()), time.Since(start).Round(time.Millisecond))
	}
	return nil
}
