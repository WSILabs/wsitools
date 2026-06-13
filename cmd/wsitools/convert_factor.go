package main

// convert_factor.go — runConvertFactor dispatches convert --factor/--target-mag
// for targets that support reduce-then-rebuild (svs, tiff, cog-wsi, ome-tiff).
//
// The SVS path reuses the exact same engine as runDownsample: it calls
// downsampleToSVS, a helper factored out of runDownsample that takes all
// parameters explicitly so both callers can share the body without flag-
// variable collisions.
//
// The TIFF path (downsampleToTIFF) mirrors downsampleToSVS but uses FormatName
// "tiff" and emits scaled MPP / magnification directly from metadata (no Aperio
// ImageDescription mutation).
//
// The COG-WSI path (downsampleToCOGWSI + buildPyramidCOGWSI) routes the same
// reduced L0 + pyramid through the cogwsiwriter instead of the streamwriter.
//
// dispatchDownsampleByTarget is the shared dispatch table used by both
// runConvertFactor (convert --to X --factor N) and runDownsample (format-
// preserving reduce). Both front-ends resolve their flags and call this
// function with explicit parameters — no global flag variables are read here.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"

	codec "github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/downscale"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// downsampleTargetForFormat maps the four writable source formats to their
// matching downsample target string. Returns ("", false) for formats that have
// no writable target (ndpi, philips-tiff, bif, ife, leica-scn, dicom, szi, …).
func downsampleTargetForFormat(format string) (string, bool) {
	switch opentile.Format(format) {
	case opentile.FormatSVS:
		return "svs", true
	case opentile.FormatOMETIFF:
		return "ome-tiff", true
	case opentile.FormatGenericTIFF:
		return "tiff", true
	case opentile.FormatCOGWSI:
		return "cog-wsi", true
	default:
		return "", false
	}
}

// dispatchDownsampleByTarget is the shared dispatch used by both runConvertFactor
// (convert --to X --factor N) and runDownsample (format-preserving reduce).
// All parameters are explicit — no global flag variables are read here.
func dispatchDownsampleByTarget(
	ctx context.Context,
	target, input, output string,
	factor, targetMag, quality, workers int,
	tileOrderName string,
	bigtiffFlag string,
	force, noAssociated bool,
) error {
	switch target {
	case "svs":
		return downsampleToSVS(ctx, input, output, factor, targetMag, quality, workers, tileOrderName, force, bigtiffFlag, noAssociated)
	case "tiff":
		return downsampleToTIFF(ctx, input, output, factor, targetMag, quality, workers, tileOrderName, force, bigtiffFlag, noAssociated)
	case "cog-wsi":
		return downsampleToCOGWSI(ctx, input, output, factor, targetMag, quality, workers, tileOrderName, force, bigtiffFlag, noAssociated)
	case "ome-tiff":
		return downsampleToOMETIFF(ctx, input, output, factor, targetMag, quality, workers, tileOrderName, force, bigtiffFlag, noAssociated)
	default:
		return fmt.Errorf("--factor for --to %s not yet implemented", target)
	}
}

// runConvertFactor is called by runConvertTIFF when --factor or --target-mag is
// set (factor != 1 or targetMag != 0). Supported targets: svs, tiff, cog-wsi, ome-tiff.
func runConvertFactor(cmd *cobra.Command, input, target string, start time.Time) error {
	// Parse common flags shared by all targets.
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

	return dispatchDownsampleByTarget(
		cmd.Context(),
		target,
		input,
		cvOutput,
		cvFactor,
		cvTargetMag,
		quality,
		workers,
		cvTileOrder,
		cvBigTIFFFlag,
		cvForce,
		cvNoAssociated,
	)
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
		for _, a := range src.AssociatedImages() {
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

// downsampleToTIFF is the reduce-then-rebuild body for convert --to tiff --factor N.
// It mirrors downsampleToSVS but uses FormatName="tiff" and emits scaled
// MPPX/MPPY/Magnification from the parsed Aperio ImageDescription (no Aperio
// ImageDescription mutation — plain TIFF output carries no Aperio metadata).
func downsampleToTIFF(
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

	src, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	// Resolve source MPP and magnification — prefer the Aperio ImageDescription
	// (SVS sources) so that AppMag/MPP are preserved verbatim; fall back to
	// opentile metadata for all other formats.
	var srcMPPX, srcMPPY, srcMag float64
	rawDesc, _ := source.ReadSourceImageDescription(input)
	desc, descErr := ParseImageDescription(rawDesc)
	if descErr == nil {
		srcMPPX = desc.MPP
		srcMPPY = desc.MPP
		srcMag = desc.AppMag
	} else {
		md := src.Metadata()
		srcMPPX = md.MPP.X
		srcMPPY = md.MPP.Y
		srcMag = md.Magnification
	}

	// Resolve --target-mag if set.
	if targetMag > 0 {
		if srcMag <= 0 {
			return fmt.Errorf("--target-mag set but source AppMag is unknown/zero")
		}
		ratio := srcMag / float64(targetMag)
		f := int(ratio + 0.0001)
		if !isValidFactor(f) || float64(f) != ratio {
			return fmt.Errorf("source AppMag %g / target %d = %g is not a valid power-of-2 in {2,4,8,16}", srcMag, targetMag, ratio)
		}
		factor = f
	}
	if !isValidFactor(factor) {
		return fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", factor)
	}

	srcL0 := src.Levels()[0]
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H
	outW := srcW / factor
	outH := srcH / factor
	if outW <= 0 || outH <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outW, outH, factor)
	}

	// Scale metadata: MPP grows by factor, magnification shrinks by factor.
	mppX := srcMPPX * float64(factor)
	mppY := srcMPPY * float64(factor)
	mag := srcMag / float64(factor)

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

	// Build a wsi-tools provenance ImageDescription so the generictiff reader
	// can recover Magnification and MPP on re-open (opentile-go's generictiff
	// format reads both fields from the "wsi-tools/<v> … mag=…x mpp=…" string).
	imageDesc := fmt.Sprintf("wsi-tools/%s transcode source=%s codec=jpeg mpp=%v mag=%vx",
		Version, src.Format(), mppX, mag)

	w, err := streamwriter.Create(output, streamwriter.Options{
		BigTIFF:          bigtiffMode,
		ImageDescription: imageDesc,
		ToolsVersion:     Version,
		SourceFormat:     string(src.Format()),
		FormatName:       "tiff",
		AcceptedOrders:   acceptedOrdersForFormat("tiff"),
		DefaultOrder:     order,
		MPPX:             mppX,
		MPPY:             mppY,
		Magnification:    mag,
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

	// For plain TIFF output there is no SVS-shaped IFD ordering requirement;
	// pass no postL0Hook and write associated images at the end.
	if err := buildPyramid(ctx, src, w, factor, quality, workers, nil); err != nil {
		return fmt.Errorf("build pyramid: %w", err)
	}

	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if err := writeOneAssociated(w, a); err != nil {
				return fmt.Errorf("write associated %s: %w", a.Type(), err)
			}
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

// downsampleToCOGWSI is the reduce-then-rebuild body for convert --to cog-wsi --factor N.
// It routes the reduced L0 + pyramid through the cogwsiwriter with scaled metadata.
func downsampleToCOGWSI(
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

	src, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	// Resolve source MPP and magnification — prefer the Aperio ImageDescription
	// (SVS sources) so that AppMag/MPP are preserved verbatim; fall back to
	// opentile metadata for all other formats.
	var srcMPPX, srcMPPY, srcMag float64
	rawDescCOG, _ := source.ReadSourceImageDescription(input)
	descCOG, descErrCOG := ParseImageDescription(rawDescCOG)
	if descErrCOG == nil {
		srcMPPX = descCOG.MPP
		srcMPPY = descCOG.MPP
		srcMag = descCOG.AppMag
	} else {
		md := src.Metadata()
		srcMPPX = md.MPP.X
		srcMPPY = md.MPP.Y
		srcMag = md.Magnification
	}

	if targetMag > 0 {
		if srcMag <= 0 {
			return fmt.Errorf("--target-mag set but source AppMag is unknown/zero")
		}
		ratio := srcMag / float64(targetMag)
		f := int(ratio + 0.0001)
		if !isValidFactor(f) || float64(f) != ratio {
			return fmt.Errorf("source AppMag %g / target %d = %g is not a valid power-of-2 in {2,4,8,16}", srcMag, targetMag, ratio)
		}
		factor = f
	}
	if !isValidFactor(factor) {
		return fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", factor)
	}

	srcL0 := src.Levels()[0]
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H
	outW := srcW / factor
	outH := srcH / factor
	if outW <= 0 || outH <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outW, outH, factor)
	}

	mppX := srcMPPX * float64(factor)
	mppY := srcMPPY * float64(factor)
	mag := srcMag / float64(factor)

	bigTIFFMode, err := parseBigTIFFFlag(bigtiffFlag)
	if err != nil {
		// bigtiffFlag may be "auto" (from default) — parseBigTIFFFlag handles that.
		// If it's empty, treat as auto.
		if bigtiffFlag == "" {
			bigTIFFMode = cogwsiwriter.BigTIFFAuto
		} else {
			return err
		}
	}

	order, err := tileorder.ByName(tileOrderName)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	w, err := cogwsiwriter.Create(output, cogwsiwriter.Options{
		BigTIFF:      bigTIFFMode,
		ToolsVersion: Version,
		DefaultOrder: order,
		Metadata: cogwsiwriter.Metadata{
			MPPX:            mppX,
			MPPY:            mppY,
			Magnification:   mag,
			ICCProfile:      src.ICCProfile(),
			SourceFormat:    string(src.Format()),
			SourceImageDesc: fmt.Sprintf("wsitools/%s convert --factor %d source=%s", Version, factor, src.Format()),
		},
	})
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}

	aborted := false
	defer func() {
		if !aborted {
			return
		}
		w.Abort()
	}()

	if ctx == nil {
		ctx = context.Background()
	}

	if err := buildPyramidCOGWSI(ctx, src, w, factor, quality, workers); err != nil {
		aborted = true
		return fmt.Errorf("build pyramid: %w", err)
	}

	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			spec, err := faithfulCOGWSISpecOT(a)
			if err != nil {
				if errors.Is(err, errSkipAssociated) {
					slog.Warn("skipping associated image", "type", a.Type(), "reason", err)
					continue
				}
				aborted = true
				return err
			}
			if err := w.AddAssociated(spec); err != nil {
				aborted = true
				return fmt.Errorf("add associated %s: %w", a.Type(), err)
			}
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}

	var outSizeStr string
	if fi, err := os.Stat(output); err == nil {
		outSizeStr = formatBytes(fi.Size())
	}
	fmt.Printf("wrote %s (%s)\n", output, outSizeStr)
	return nil
}

// downsampleToOMETIFF is the reduce-then-rebuild body for convert --to ome-tiff --factor N.
// It mirrors downsampleToTIFF but emits a conformant OME-XML ImageDescription on L0
// (PhysicalSizeX/Y = scaled MPP) and uses the OME-TIFF streamwriter options
// (SubResolutionPyramid=true, SampleFormat=1). Associated images are filtered to
// recognized OME types (label/macro/thumbnail) so the OME-XML Image list stays
// consistent with the written IFDs.
func downsampleToOMETIFF(
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

	src, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	// Resolve source MPP and magnification — prefer the Aperio ImageDescription
	// (SVS sources) so that AppMag/MPP are preserved verbatim; fall back to
	// opentile metadata for all other formats.
	var srcMPPX, srcMPPY, srcMag float64
	rawDescOME, _ := source.ReadSourceImageDescription(input)
	descOME, descErrOME := ParseImageDescription(rawDescOME)
	if descErrOME == nil {
		srcMPPX = descOME.MPP
		srcMPPY = descOME.MPP
		srcMag = descOME.AppMag
	} else {
		md := src.Metadata()
		srcMPPX = md.MPP.X
		srcMPPY = md.MPP.Y
		srcMag = md.Magnification
	}

	// Resolve --target-mag if set.
	if targetMag > 0 {
		if srcMag <= 0 {
			return fmt.Errorf("--target-mag set but source AppMag is unknown/zero")
		}
		ratio := srcMag / float64(targetMag)
		f := int(ratio + 0.0001)
		if !isValidFactor(f) || float64(f) != ratio {
			return fmt.Errorf("source AppMag %g / target %d = %g is not a valid power-of-2 in {2,4,8,16}", srcMag, targetMag, ratio)
		}
		factor = f
	}
	if !isValidFactor(factor) {
		return fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", factor)
	}

	srcL0 := src.Levels()[0]
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H
	outW := srcW / factor
	outH := srcH / factor
	if outW <= 0 || outH <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outW, outH, factor)
	}

	// Scale metadata: MPP grows by factor, magnification shrinks by factor.
	mppX := srcMPPX * float64(factor)
	mppY := srcMPPY * float64(factor)
	mag := srcMag / float64(factor)

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

	// Build associated-image specs for OME-XML: only recognized types
	// (label/macro/thumbnail) are listed so IFD positions stay consistent.
	var omeAssocs []OMEAssoc
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			name := omeAssocName(string(a.Type()))
			if name == "" {
				continue
			}
			omeAssocs = append(omeAssocs, OMEAssoc{Name: name, W: uint32(a.Size().W), H: uint32(a.Size().H)})
		}
	}

	// Build OME-XML with reduced L0 dimensions, scaled MPP, and scaled
	// magnification so opentile-go's OME reader populates md.Magnification.
	omeXML := SyntheticOMEDescriptionWithMag(
		uint32(outW), uint32(outH),
		mppX, mppY, mag,
		"Image", string(src.Format()),
		omeAssocs,
	)

	w, err := streamwriter.Create(output, streamwriter.Options{
		BigTIFF:              bigtiffMode,
		ImageDescription:     omeXML,
		ToolsVersion:         Version,
		SourceFormat:         string(src.Format()),
		FormatName:           "ome-tiff",
		AcceptedOrders:       acceptedOrdersForFormat("ome-tiff"),
		DefaultOrder:         order,
		MPPX:                 mppX,
		MPPY:                 mppY,
		Magnification:        mag,
		ICCProfile:           src.ICCProfile(),
		SubResolutionPyramid: true,
		SampleFormat:         1,
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

	// Build pyramid using the shared reduce-then-rebuild path (no postL0Hook
	// needed for OME-TIFF; associated images follow after all pyramid levels).
	if err := buildPyramid(ctx, src, w, factor, quality, workers, nil); err != nil {
		return fmt.Errorf("build pyramid: %w", err)
	}

	// Write associated images filtered to recognized OME types (consistent
	// with omeAssocs above so IFD count matches OME-XML Image list).
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if omeAssocName(string(a.Type())) == "" {
				continue
			}
			if err := writeOneAssociated(w, a); err != nil {
				return fmt.Errorf("write associated %s: %w", a.Type(), err)
			}
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

// buildPyramidCOGWSI materialises the reduced L0 raster and iterates through
// all pyramid levels encoding JPEG tiles into a cogwsiwriter.Writer. It mirrors
// buildPyramid (which targets streamwriter.Writer) but does not support a
// postL0Hook (COG-WSI layout has no SVS-style interleaved thumbnail IFD).
func buildPyramidCOGWSI(ctx context.Context, src *opentile.Slide, w *cogwsiwriter.Writer, factor, quality, workers int) error {
	srcLevels := src.Levels()
	srcL0 := srcLevels[0]
	srcW := srcL0.Size.W
	srcH := srcL0.Size.H
	outW := srcW / factor
	outH := srcH / factor

	nLevels := len(srcLevels)

	rasterBytes := int64(outW) * int64(outH) * 3
	if rasterBytes < 0 {
		return fmt.Errorf("output L0 raster size overflows int64")
	}
	outL0 := make([]byte, rasterBytes)
	if err := downscale.MaterializeReducedL0(ctx, srcL0, outL0, outW, outH, factor); err != nil {
		return err
	}

	_ = workers // reserved for future parallel encode path
	return buildPyramidFromRasterCOGWSI(ctx, w, outL0, outW, outH, nLevels, quality)
}

// buildPyramidFromRasterCOGWSI encodes an in-memory RGB888 L0 raster into a
// cogwsiwriter pyramid, box-halving between levels via halveRaster. nLevels is
// the total level count (L0 included). Shared by buildPyramidCOGWSI (downsample)
// and cropToCOGWSI.
func buildPyramidFromRasterCOGWSI(ctx context.Context, w *cogwsiwriter.Writer, l0 []byte, l0W, l0H, nLevels, quality int) error {
	currentRaster := l0
	currentW, currentH := l0W, l0H
	for outLvl := 0; outLvl < nLevels; outLvl++ {
		if err := encodeAndWriteLevelCOGWSI(ctx, w, currentRaster, currentW, currentH, quality, outLvl == 0); err != nil {
			return fmt.Errorf("level %d: %w", outLvl, err)
		}
		if outLvl < nLevels-1 {
			var herr error
			currentRaster, currentW, currentH, herr = halveRaster(currentRaster, currentW, currentH)
			if herr != nil {
				return fmt.Errorf("Box halving level %d→%d: %w", outLvl, outLvl+1, herr)
			}
			if currentW == 0 || currentH == 0 {
				break
			}
		}
	}
	return nil
}

// encodeAndWriteLevelCOGWSI encodes one pyramid level into 256×256 JPEG tiles
// and writes them row-major into a cogwsiwriter level handle.
// cogwsiwriter.WriteTile enforces strict row-major order, so we encode and
// write sequentially in (ty, tx) order.
func encodeAndWriteLevelCOGWSI(ctx context.Context, w *cogwsiwriter.Writer, raster []byte, levelW, levelH, quality int, isL0 bool) error {
	enc, err := jpegcodec.Factory{}.NewEncoder(codec.LevelGeometry{
		TileWidth:   outputTileSize,
		TileHeight:  outputTileSize,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": fmt.Sprintf("%d", quality)}})
	if err != nil {
		return fmt.Errorf("new encoder: %w", err)
	}
	defer enc.Close()

	tables := enc.LevelHeader()
	spec := cogwsiwriter.LevelSpec{
		ImageWidth:      uint32(levelW),
		ImageHeight:     uint32(levelH),
		TileWidth:       outputTileSize,
		TileHeight:      outputTileSize,
		Compression:     tiff.CompressionJPEG,
		Photometric:     2, // RGB
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		JPEGTables:      tables,
		IsL0:            isL0,
	}
	h, err := w.AddLevel(spec)
	if err != nil {
		return fmt.Errorf("AddLevel: %w", err)
	}

	tilesX := (levelW + outputTileSize - 1) / outputTileSize
	tilesY := (levelH + outputTileSize - 1) / outputTileSize

	for ty := 0; ty < tilesY; ty++ {
		for tx := 0; tx < tilesX; tx++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			raw, err := extractTileFromRaster(raster, levelW, levelH, tx, ty)
			if err != nil {
				return fmt.Errorf("extract tile (%d,%d): %w", tx, ty, err)
			}
			compressed, err := enc.EncodeTile(raw, outputTileSize, outputTileSize, nil)
			if err != nil {
				return fmt.Errorf("encode tile (%d,%d): %w", tx, ty, err)
			}
			if err := h.WriteTile(uint32(tx), uint32(ty), compressed); err != nil {
				return fmt.Errorf("write tile (%d,%d): %w", tx, ty, err)
			}
		}
	}
	return nil
}
