package main

// convert_factor.go — runConvertFactor dispatches convert --factor/--target-mag
// for targets that support reduce-then-rebuild (currently svs only).
//
// The SVS path reuses the exact same engine as runDownsample: it calls
// downsampleToSVS, a helper factored out of runDownsample that takes all
// parameters explicitly so both callers can share the body without flag-
// variable collisions.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// runConvertFactor is called by runConvertTIFF when --factor or --target-mag is
// set (factor != 1 or targetMag != 0). Currently only --to svs is implemented.
func runConvertFactor(cmd *cobra.Command, input, target string, start time.Time) error {
	switch target {
	case "svs":
		// Parse quality from the convert --quality flag (int string or empty).
		quality := 90
		if cvQuality != "" {
			if _, err := fmt.Sscanf(cvQuality, "%d", &quality); err != nil {
				return fmt.Errorf("--quality %q: must be an integer 1..100", cvQuality)
			}
		}
		if quality < 1 || quality > 100 {
			return fmt.Errorf("--quality must be 1..100")
		}
		workers := cvWorkers
		if workers == 0 {
			workers = runtime.NumCPU()
		}
		return downsampleToSVS(
			cmd.Context(),
			input,
			cvOutput,
			cvFactor,
			cvTargetMag,
			quality,
			workers,
			cvTileOrder,
			cvForce,
			cvBigTIFFFlag,
			cvNoAssociated,
		)
	default:
		return fmt.Errorf("--factor for --to %s not yet implemented", target)
	}
}

// downsampleToSVS is the shared reduce-then-rebuild body used by both
// runConvertFactor (convert --to svs --factor N) and (eventually) runDownsample.
// All parameters are explicit — no global flag variables are read here.
func downsampleToSVS(
	ctx context.Context,
	input, output string,
	factor, targetMag int,
	quality, workers int,
	tileOrderName string,
	force bool,
	bigtiffFlag string,
	noAssociated bool,
) error {
	if quality < 1 || quality > 100 {
		return fmt.Errorf("--quality must be in [1, 100], got %d", quality)
	}
	if workers < 1 {
		workers = 1
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input: %w", err)
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

	// Open source via opentile-go (SVS-only for now).
	src, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()
	if src.Format() != opentile.FormatSVS {
		return fmt.Errorf("--factor SVS output supports SVS sources only; got %s", src.Format())
	}

	// Parse source ImageDescription.
	rawDesc, err := source.ReadSourceImageDescription(input)
	if err != nil {
		return fmt.Errorf("read source ImageDescription: %w", err)
	}
	desc, err := ParseImageDescription(rawDesc)
	if err != nil {
		return fmt.Errorf("parse source ImageDescription: %w", err)
	}

	// Resolve --target-mag if set.
	if targetMag > 0 {
		if desc.AppMag <= 0 {
			return fmt.Errorf("--target-mag set but source AppMag is unknown/zero")
		}
		ratio := desc.AppMag / float64(targetMag)
		f := int(ratio + 0.0001)
		if !isValidFactor(f) || float64(f) != ratio {
			return fmt.Errorf("source AppMag %g / target %d = %g is not a valid power-of-2 in {2,4,8,16}", desc.AppMag, targetMag, ratio)
		}
		factor = f
	}
	if !isValidFactor(factor) {
		return fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", factor)
	}

	// Compute output L0 dimensions.
	srcL0 := src.Levels()[0]
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H
	outW := srcW / factor
	outH := srcH / factor
	if outW <= 0 || outH <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outW, outH, factor)
	}

	// Mutate the ImageDescription for the new magnification + geometry.
	desc.MutateForDownsample(factor, uint32(outW), uint32(outH))

	// Predict BigTIFF need.
	var bigtiffMode tiff.BigTIFFMode
	switch bigtiffFlag {
	case "on":
		bigtiffMode = tiff.BigTIFFOn
	case "off":
		bigtiffMode = tiff.BigTIFFOff
	default: // "auto" or ""
		if predictBigTIFFNeeded(srcL0, src.Levels(), factor) {
			bigtiffMode = tiff.BigTIFFOn
		} else {
			bigtiffMode = tiff.BigTIFFOff
		}
	}

	order, err := tileorder.ByName(tileOrderName)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	// Open writer.
	w, err := streamwriter.Create(output, streamwriter.Options{
		BigTIFF:          bigtiffMode,
		ImageDescription: desc.Encode(),
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
			w.Abort()
		}
	}()

	if ctx == nil {
		ctx = context.Background()
	}

	// Segregate associated images: thumbnail between L0 and L1; label then
	// macro/overview at the end — mirrors runDownsample exactly.
	var thumbnail, label, macro opentile.AssociatedImage
	if !noAssociated {
		for _, a := range src.Associated() {
			switch a.Type() {
			case "thumbnail":
				thumbnail = a
			case "label":
				label = a
			case "macro", "overview":
				macro = a
			}
		}
	}

	postL0Hook := func() error {
		if thumbnail == nil {
			return nil
		}
		return writeOneAssociated(w, thumbnail)
	}

	if err := buildPyramid(ctx, src, w, factor, quality, workers, postL0Hook); err != nil {
		return fmt.Errorf("build pyramid: %w", err)
	}

	if label != nil {
		if err := writeOneAssociated(w, label); err != nil {
			return fmt.Errorf("write associated label: %w", err)
		}
	}
	if macro != nil {
		if err := writeOneAssociated(w, macro); err != nil {
			return fmt.Errorf("write associated macro/overview: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	closed = true

	var outSizeStr string
	if fi, err := os.Stat(output); err == nil {
		outSizeStr = formatBytes(fi.Size())
	}
	fmt.Printf("wrote %s (%s)\n", output, outSizeStr)
	return nil
}
