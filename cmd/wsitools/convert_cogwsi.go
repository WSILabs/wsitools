package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"

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

	src, slide, err := source.OpenWithSlide(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
	l0 := src.Levels()[0]
	srcCodec := l0.Compression()
	overlapping := sourceIsOverlapping(src)

	// Re-encode only when explicitly requested (--codec), forced (--tile-size
	// differs from the source), or required (overlapping/stitched source, which the
	// engine must recomposite). Otherwise tile-copy verbatim — which preserves ANY
	// TIFF-representable source compression (JPEG-family, but also LZW / Deflate /
	// uncompressed), matching the historical cog-wsi tile-copy behavior. (This is
	// looser than runConvertTIFF's tileCopyEligible, which tile-copies only the
	// JPEG-family codecs; cog-wsi has always tile-copied the wider set.)
	tileSizeDiffers := cvTileSize > 0 && cvTileSize != l0.TileSize().X
	reencode := overlapping || cvCodec != "" || tileSizeDiffers

	if !reencode {
		// Verbatim tile-copy requires a representable TIFF Compression tag.
		for _, lvl := range src.Levels() {
			if compressionTagFor(lvl.Compression()) == 0 {
				return fmt.Errorf("level %d: source compression %s has no standard TIFF Compression tag; cannot tile-copy",
					lvl.Index(), lvl.Compression())
			}
		}
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
	if reencode {
		err = reencodeCOGWSI(cmd.Context(), slide, src, w, plan, srcCodec, overlapping)
	} else {
		err = writeCOGWSI(w, src, plan)
	}
	if err != nil {
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
		infof("wrote %s (%s, %s)\n", cvOutput, formatBytes(stat.Size()), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// reencodeCOGWSI re-encodes src into the COG-WSI writer w with the requested
// codec (--codec, else the source codec preserved). It honors the source pyramid
// structure (select-octave) for a plain non-overlapping octave-aligned source —
// mirroring the svs/tiff/ome-tiff re-encode; an overlapping (BIF) or non-octave-
// aligned source takes the full-octave engine path (convertStitchedCOGWSI).
func reencodeCOGWSI(ctx context.Context, slide *opentile.Slide, src source.Source, w *cogwsiwriter.Writer, plan assocEditPlan, srcCodec source.Compression, overlapping bool) error {
	codecName, err := reencodeCodecFor(srcCodec, cvCodec)
	if err != nil {
		return err
	}
	knobs, err := parseQualityKnobs(cvQuality)
	if err != nil {
		return fmt.Errorf("--quality: %w", err)
	}
	// Honor the source chroma subsampling (JPEG) and treat the default quality as a
	// floor — same as the TIFF-family re-encode.
	knobs = withSourceSubsampling(knobs, codecName, slide)
	if cvQuality == "" {
		knobs = withSourceQualityFloor(knobs, slide)
	}
	workers := cvWorkers
	if workers == 0 {
		workers = runtime.NumCPU()
	}
	if !overlapping {
		if levels, ok := transcodeOctaveLevels(srcLevelDimsFromSlide(slide)); ok {
			return convertTranscodeCOGWSI(ctx, slide, src, w, plan, workers, codecName, knobs, levels)
		}
	}
	// Overlapping source, or a non-octave-aligned pyramid: full-octave engine path.
	return convertStitchedCOGWSI(ctx, slide, src, w, plan, workers, knobs, codecName)
}
