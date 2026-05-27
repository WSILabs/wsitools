package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// runConvertTIFF dispatches --to svs / tiff / ome-tiff. With --codec
// specified, it invokes the existing transcode re-encode pipeline via
// the helpers in transcode.go. Without --codec, tile-copy applies when
// the source is natively tiled and the target container accepts the
// source codec verbatim.
func runConvertTIFF(cmd *cobra.Command, input, target string, start time.Time) error {
	src, err := source.Open(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported at v0.2.0: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}

	l0 := src.Levels()[0]
	srcCodec := l0.Compression()
	tiled := nativelyTiled(src.Format())

	if tileCopyEligible(target, cvCodec, srcCodec, tiled) {
		return runConvertTIFFTileCopy(cmd, src, input, target, start)
	}
	if cvCodec == "" {
		return fmt.Errorf("--codec required for --to %s with source codec %s (no tile-copy path)",
			target, srcCodec)
	}
	return runTranscodeAsConvert(cmd, input, target, cvCodec, cvQuality, cvWorkers, start)
}

func runConvertTIFFTileCopy(_ *cobra.Command, src source.Source, input, target string, start time.Time) error {
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", cvOutput)
		}
	}

	// Validate all levels have a representable TIFF compression tag.
	for _, lvl := range src.Levels() {
		if compressionTagFor(lvl.Compression()) == 0 {
			return fmt.Errorf("level %d: source compression %s has no standard TIFF Compression tag; cannot tile-copy",
				lvl.Index(), lvl.Compression())
		}
	}

	// Resolve the container name: --to svs|tiff|ome-tiff is the user's
	// explicit target, but we pass it through resolveContainer to apply
	// the same SVS-shape override logic that runTranscodeAsConvert uses.
	container := resolveContainer(src.Format(), "", target)

	bigtiffMode := resolveBigTIFFMode(cvBigTIFFFlag, src)

	order, err := tileorder.ByName(cvTileOrder)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	md := src.Metadata()

	opts := streamwriter.Options{
		BigTIFF:        bigtiffMode,
		ToolsVersion:   Version,
		SourceFormat:   src.Format(),
		FormatName:     container,
		AcceptedOrders: acceptedOrdersForFormat(container),
		DefaultOrder:   order,
	}
	if md.Make != "" {
		opts.Make = md.Make
	}
	if md.Model != "" {
		opts.Model = md.Model
	}
	if md.Software != "" {
		opts.Software = md.Software
	}
	if !md.AcquisitionDateTime.IsZero() {
		opts.DateTime = md.AcquisitionDateTime
	}

	// ImageDescription handling: for SVS → SVS tile-copy, preserve the
	// source's Aperio ImageDescription verbatim on L0 via ExtraTags.
	// For other containers, emit a wsitools provenance string.
	var srcImageDesc string
	if container == "svs" && src.Format() == string(opentile.FormatSVS) {
		srcImageDesc = src.SourceImageDescription()
	} else {
		opts.ImageDescription = buildProvenanceDesc(src, "tile-copy", md)
	}

	w, err := streamwriter.Create(cvOutput, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}

	// Tile-copy: emit levels in source order (L0 first).
	for _, lvl := range src.Levels() {
		spec := streamwriter.LevelSpec{
			ImageWidth:      uint32(lvl.Size().X),
			ImageHeight:     uint32(lvl.Size().Y),
			TileWidth:       uint32(lvl.TileSize().X),
			TileHeight:      uint32(lvl.TileSize().Y),
			Compression:     compressionTagFor(lvl.Compression()),
			Photometric:     2, // RGB; lossless copy preserves source codec's colour model
			SamplesPerPixel: 3,
			BitsPerSample:   []uint16{8, 8, 8},
			NewSubfileType:  newSubfileTypeForLevel(lvl.Index(), container),
			WSIImageType:    tiff.WSIImageTypePyramid,
		}
		// SVS-shaped output: emit Aperio ImageDescription verbatim on L0.
		if container == "svs" && lvl.Index() == 0 && srcImageDesc != "" {
			spec.ExtraTags = buildSVSL0ExtraTags(srcImageDesc)
		}

		lh, err := w.AddLevel(spec)
		if err != nil {
			w.Abort()
			return fmt.Errorf("add level %d: %w", lvl.Index(), err)
		}

		buf := make([]byte, lvl.TileMaxSize())
		grid := lvl.Grid()
		for ty := 0; ty < grid.Y; ty++ {
			for tx := 0; tx < grid.X; tx++ {
				n, err := lvl.TileInto(tx, ty, buf)
				if err != nil {
					w.Abort()
					return fmt.Errorf("read tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
				// WriteTile submits asynchronously into a reorder buffer
				// and may reference the slice past return; copy out of the
				// reused tile-read buffer before handing off.
				tile := append([]byte(nil), buf[:n]...)
				if err := lh.WriteTile(uint32(tx), uint32(ty), tile); err != nil {
					w.Abort()
					return fmt.Errorf("write tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
			}
		}
		// Signal end of input for this level; the reorder buffer will
		// drain synchronously during Close/buildLevelEntries.
		lh.CloseInput()
	}

	if !cvNoAssociated {
		if err := writeAssociatedImages(src, w, container); err != nil {
			w.Abort()
			return err
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}

	stat, _ := os.Stat(cvOutput)
	if stat != nil {
		slog.Info("convert complete",
			"output", cvOutput,
			"size", formatBytes(stat.Size()),
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		fmt.Printf("wrote %s (%s, %s)\n", cvOutput, formatBytes(stat.Size()), time.Since(start).Round(time.Millisecond))
	}
	return nil
}
