package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"

	qualityjpeg "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"
	codec "github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/pipeline"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// runConvertTIFF dispatches --to svs / tiff / ome-tiff. With --codec
// specified, it invokes the re-encode pipeline via runConvertTIFFReencode.
// Without --codec, tile-copy applies when the source is natively tiled and
// the target container accepts the source codec verbatim.
func runConvertTIFF(cmd *cobra.Command, input, target string, start time.Time) error {
	// --factor / --target-mag: reduce-then-rebuild via runConvertFactor.
	if cvFactor != 1 || cvTargetMag != 0 {
		return runConvertFactor(cmd, input, target, start)
	}

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

	// Overlapping/stitched source (Ventana BIF) cannot be tile-copied; it must be
	// recomposited via the retile engine. Force the re-encode path (default codec
	// jpeg) and skip tile-copy eligibility.
	if sourceIsOverlapping(src) {
		codecName := cvCodec
		if codecName == "" {
			codecName = "jpeg"
		}
		return runConvertTIFFReencode(cmd, input, target, codecName, cvQuality, cvWorkers, start)
	}

	if tileCopyEligible(target, cvCodec, srcCodec, tiled) {
		return runConvertTIFFTileCopy(cmd, src, input, target, start)
	}
	if cvCodec == "" {
		return fmt.Errorf("--codec required for --to %s with source codec %s (no tile-copy path)",
			target, srcCodec)
	}
	return runConvertTIFFReencode(cmd, input, target, cvCodec, cvQuality, cvWorkers, start)
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
	// the same SVS-shape override logic that runConvertTIFFReencode uses.
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
		MPPX:           md.MPPX,
		MPPY:           md.MPPY,
		Magnification:  md.Magnification,
		ICCProfile:     md.ICCProfile,
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
	if container == "ome-tiff" {
		opts.SubResolutionPyramid = true
		opts.SampleFormat = 1
	}

	// ImageDescription handling: each container has its own L0
	// detection signal — SVS needs "Aperio" prefix, OME needs "OME>"
	// suffix. For native source→native target we preserve the source's
	// description verbatim; for cross-format conversion we synthesize a
	// minimal but detection-passing document. Other containers emit a
	// plain wsitools provenance string.
	var srcImageDesc string
	l0 := src.Levels()[0]
	srcSoft := strings.TrimSpace(md.Make + " " + md.Model)
	switch container {
	case "svs":
		if src.Format() == string(opentile.FormatSVS) {
			srcImageDesc = src.SourceImageDescription()
		} else {
			srcImageDesc = SyntheticAperioDescription(
				uint32(l0.Size().X), uint32(l0.Size().Y),
				uint32(l0.TileSize().X), uint32(l0.TileSize().Y),
				0, // Q unknown on tile-copy
				md.MPP, md.Magnification,
				srcSoft,
				"jpeg",
			).Encode()
		}
	case "ome-tiff":
		if src.Format() == string(opentile.FormatOMETIFF) {
			srcImageDesc = src.SourceImageDescription()
		} else {
			srcImageDesc = SyntheticOMEDescription(
				uint32(l0.Size().X), uint32(l0.Size().Y),
				md.MPP, md.MPP, "Image", srcSoft,
				omeAssociatedSpecs(src, omeEditPlan{}),
			)
		}
	default:
		opts.ImageDescription = buildProvenanceDesc(src, "tile-copy", md)
	}

	// SVS Aperio-conformance L0 tags. ImageDepth is always 1 (2D output).
	// YCbCrSubSampling is emitted only for JPEG output, parsed from the
	// actual source tile we copy verbatim ("match what we are writing");
	// with Photometric=2 (RGB) it is informational, so a parse miss simply
	// omits it rather than risking a wrong value.
	if container == "svs" {
		opts.ImageDepth = 1
		if compressionTagFor(l0.Compression()) == tiff.CompressionJPEG {
			buf := make([]byte, l0.TileMaxSize())
			// Independent of the write loop below; samples tile (0,0)'s
			// chroma subsampling to describe the JPEG bytes we copy.
			if n, err := l0.TileInto(0, 0, buf); err == nil {
				if h, v, ok := qualityjpeg.LumaSampling(buf[:n]); ok {
					opts.YCbCrSubSampling = []uint16{h, v}
				}
			}
		}
	}

	w, err := streamwriter.Create(cvOutput, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}

	omeSynthetic := container == "ome-tiff" && src.Format() != string(opentile.FormatOMETIFF)
	if err := writeTIFFTileCopy(w, src, container, srcImageDesc, omeSynthetic, omeEditPlan{dropAll: cvNoAssociated}); err != nil {
		w.Abort()
		return err
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

// runConvertTIFFReencode is the convert-side entry into the transcode
// re-encode pipeline. It accepts the convert command's flag values
// directly (instead of reading the tc* globals) so convert can drive
// the same code path without flag-name collisions.
func runConvertTIFFReencode(cmd *cobra.Command, input, container, codecName, quality string, workers int, start time.Time) error {
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", cvOutput)
		}
	}

	// --quality is a bare integer (the q knob) or comma-separated k=v knobs
	// (e.g. "q=85,reversible=true"). Codec-specific knobs pass through.
	knobs, err := parseQualityKnobs(quality)
	if err != nil {
		return err
	}
	qualityInt, _ := strconv.Atoi(knobs["q"]) // validated 1..100 by parseQualityKnobs

	// Workers: 0 means GOMAXPROCS.
	if workers == 0 {
		workers = runtime.NumCPU()
	}

	fac, err := codec.Lookup(codecName)
	if err != nil {
		return fmt.Errorf("--codec %q: %w", codecName, err)
	}

	src, slide, err := source.OpenWithSlide(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported at v0.2.0: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	// container arg from --to; resolveContainer maps it to the canonical name.
	resolvedContainer := resolveContainer(src.Format(), codecName, container)
	bigtiffMode := resolveBigTIFFMode(cvBigTIFFFlag, src)

	order, err := tileorder.ByName(cvTileOrder)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	md := src.Metadata()

	// Build writer options.
	opts := streamwriter.Options{
		BigTIFF:        bigtiffMode,
		ToolsVersion:   Version,
		SourceFormat:   src.Format(),
		FormatName:     resolvedContainer,
		AcceptedOrders: acceptedOrdersForFormat(resolvedContainer),
		DefaultOrder:   order,
		MPPX:           md.MPPX,
		MPPY:           md.MPPY,
		Magnification:  md.Magnification,
		ICCProfile:     md.ICCProfile,
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
	if resolvedContainer == "ome-tiff" {
		opts.SubResolutionPyramid = true
		opts.SampleFormat = 1
	}

	// ImageDescription handling (same logic as runConvertTIFFTileCopy).
	var srcImageDesc string
	l0 := src.Levels()[0]
	srcSoft := strings.TrimSpace(md.Make + " " + md.Model)
	switch resolvedContainer {
	case "svs":
		if src.Format() == string(opentile.FormatSVS) {
			srcImageDesc = src.SourceImageDescription()
		} else {
			srcImageDesc = SyntheticAperioDescription(
				uint32(l0.Size().X), uint32(l0.Size().Y),
				uint32(l0.TileSize().X), uint32(l0.TileSize().Y),
				qualityInt,
				md.MPP, md.Magnification,
				srcSoft,
				"jpeg",
			).Encode()
		}
	case "ome-tiff":
		if src.Format() == string(opentile.FormatOMETIFF) {
			srcImageDesc = src.SourceImageDescription()
		} else {
			srcImageDesc = SyntheticOMEDescription(
				uint32(l0.Size().X), uint32(l0.Size().Y),
				md.MPP, md.MPP, "Image", srcSoft,
				omeAssociatedSpecs(src, omeEditPlan{}),
			)
		}
	default:
		opts.ImageDescription = buildProvenanceDesc(src, codecName, md)
	}

	// SVS Aperio-conformance L0 tags. ImageDepth is always 1 (2D output).
	// YCbCrSubSampling is probed from the encoder's actual output, so it
	// matches the JPEG bytes we write (and is omitted for non-JPEG codecs).
	if resolvedContainer == "svs" {
		opts.ImageDepth = 1
		if sub, ok := encoderChromaSubsampling(fac, knobs); ok {
			opts.YCbCrSubSampling = sub
		}
	}

	w, err := streamwriter.Create(cvOutput, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}

	omeSynthetic := resolvedContainer == "ome-tiff" && src.Format() != string(opentile.FormatOMETIFF)

	// Lossless transcode must preserve every source level byte-exactly, which the
	// engine's ScaledStrips read cannot guarantee — route it to the per-level
	// path. Probe the encoder for its lossless mode (codec-owned signal).
	losslessTranscode := false
	if probe, perr := fac.NewEncoder(codec.LevelGeometry{TileWidth: 256, TileHeight: 256, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: knobs}); perr == nil {
		losslessTranscode = encoderIsLossless(probe)
		_ = probe.Close()
	}

	if sourceIsOverlapping(src) {
		// Overlapping/stitched source: the retile engine recomposites L0 and
		// derives the pyramid; convertStitchedTIFF emits associated images itself.
		if err := convertStitchedTIFF(cmd.Context(), slide, src, w, resolvedContainer, srcImageDesc, omeEditPlan{dropAll: cvNoAssociated}, omeSynthetic, workers, fac, knobs); err != nil {
			w.Abort()
			return err
		}
	} else if levels, ok := transcodeOctaveLevels(srcLevelDimsFromSlide(slide)); ok && !losslessTranscode {
		// Same-geometry transcode: route through the engine, preserving the
		// source pyramid structure (select-octave). Emits associated itself.
		if err := convertTranscodeTIFF(cmd.Context(), slide, src, w, resolvedContainer, srcImageDesc, omeEditPlan{dropAll: cvNoAssociated}, omeSynthetic, workers, fac, knobs, levels); err != nil {
			w.Abort()
			return err
		}
	} else {
		if err := transcodePyramid(cmd.Context(), src, w, fac, knobs, workers, resolvedContainer, srcImageDesc, omeEditPlan{dropAll: cvNoAssociated}, omeSynthetic); err != nil {
			w.Abort()
			return err
		}

		if !cvNoAssociated {
			if err := writeAssociatedImages(src, w, resolvedContainer, omeSynthetic, omeEditPlan{}); err != nil {
				w.Abort()
				return err
			}
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

func resolveContainer(srcFormat, codecName, override string) string {
	if override != "" {
		return override
	}
	if srcFormat == string(opentile.FormatSVS) && codecName == "jpeg" {
		return "svs"
	}
	return "tiff"
}

// resolveBigTIFFMode maps the CLI --bigtiff flag to a tiff.BigTIFFMode.
// "auto" promotes to BigTIFFOn when the source pixel count exceeds the
// 2 GiB classic-TIFF safety threshold.
func resolveBigTIFFMode(mode string, src source.Source) tiff.BigTIFFMode {
	switch mode {
	case "on":
		return tiff.BigTIFFOn
	case "off":
		return tiff.BigTIFFOff
	}
	// auto: predict output size; promote when > 2 GiB.
	// Estimate ~1 byte per pixel for lossy codecs.
	var total int64
	for _, lvl := range src.Levels() {
		total += int64(lvl.Size().X) * int64(lvl.Size().Y)
	}
	if total > (2 << 30) {
		return tiff.BigTIFFOn
	}
	return tiff.BigTIFFOff
}

// parseQualityKnobs parses the --quality value into codec knobs. It accepts a
// bare integer (the "q" knob) or comma-separated key=value pairs (e.g.
// "q=90,reversible=true" for jpeg2000 lossless). The "q" knob defaults to 90
// (codec standard default) and is range-checked 1..100; codec-specific knobs
// pass through to the encoder.
func parseQualityKnobs(quality string) (map[string]string, error) {
	knobs := map[string]string{"q": "90"}
	if quality != "" {
		if strings.Contains(quality, "=") {
			for _, kv := range strings.Split(quality, ",") {
				k, v, ok := strings.Cut(kv, "=")
				k, v = strings.TrimSpace(k), strings.TrimSpace(v)
				if !ok || k == "" {
					return nil, fmt.Errorf("--quality knob %q: want key=value", kv)
				}
				knobs[k] = v
			}
		} else {
			if _, err := strconv.Atoi(strings.TrimSpace(quality)); err != nil {
				return nil, fmt.Errorf("--quality %q: must be an integer 1..100 or comma-separated k=v knobs", quality)
			}
			knobs["q"] = strings.TrimSpace(quality)
		}
	}
	if n, err := strconv.Atoi(knobs["q"]); err != nil || n < 1 || n > 100 {
		return nil, fmt.Errorf("--quality q must be an integer 1..100, got %q", knobs["q"])
	}
	return knobs, nil
}

func transcodePyramid(ctx context.Context, src source.Source, w *streamwriter.Writer, fac codec.EncoderFactory, knobs map[string]string, workers int, container, srcImageDesc string, plan omeEditPlan, omeSynthetic bool) error {
	for _, lvl := range src.Levels() {
		if err := transcodeLevel(ctx, lvl, w, fac, knobs, workers, container, srcImageDesc); err != nil {
			return fmt.Errorf("level %d: %w", lvl.Index(), err)
		}
		if _, err := emitSVSThumbnailAtL0(src, w, lvl.Index(), container, omeSynthetic, plan); err != nil {
			return err
		}
	}
	return nil
}

func transcodeLevel(ctx context.Context, lvl source.Level, w *streamwriter.Writer, fac codec.EncoderFactory, knobs map[string]string, workers int, container, srcImageDesc string) error {
	enc, err := fac.NewEncoder(codec.LevelGeometry{
		TileWidth: lvl.TileSize().X, TileHeight: lvl.TileSize().Y,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: knobs})
	if err != nil {
		return err
	}
	defer enc.Close()

	spec := streamwriter.LevelSpec{
		ImageWidth:      uint32(lvl.Size().X),
		ImageHeight:     uint32(lvl.Size().Y),
		TileWidth:       uint32(lvl.TileSize().X),
		TileHeight:      uint32(lvl.TileSize().Y),
		Compression:     enc.TIFFCompressionTag(),
		Photometric:     2, // RGB; codecs carry their own colour model
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		JPEGTables:      enc.LevelHeader(),
		NewSubfileType:  newSubfileTypeForLevel(lvl.Index(), container),
		WSIImageType:    tiff.WSIImageTypePyramid,
	}
	// SVS / OME-TIFF: emit container-shaped ImageDescription on L0.
	if lvl.Index() == 0 && srcImageDesc != "" && (container == "svs" || container == "ome-tiff") {
		spec.ExtraTags = buildL0ImageDescriptionTag(srcImageDesc)
	}
	// NOTE: codec ExtraTIFFTags() currently returns nil for every
	// registered codec and still uses the legacy TIFFTag type from the
	// old writer package. Wiring the codec interface to tiff.RawTag is a
	// future task; the loop is dropped here so this file has zero
	// references to the legacy writer package.

	lh, err := w.AddLevel(spec)
	if err != nil {
		return err
	}

	grid := lvl.Grid()
	tileW := lvl.TileSize().X
	tileH := lvl.TileSize().Y

	sink := func(t pipeline.Tile) error {
		return lh.WriteTile(t.X, t.Y, t.Bytes)
	}

	// Run the ordered drain concurrently with pipeline.Run.
	drainErr := make(chan error, 1)
	go func() {
		for {
			idx, bytes, ok, err := lh.NextReady()
			if err != nil {
				drainErr <- err
				return
			}
			if !ok {
				drainErr <- nil
				return
			}
			if err := lh.WriteTileAtIndex(idx, bytes); err != nil {
				lh.Abort(err)
				drainErr <- err
				return
			}
		}
	}()

	pipeErr := pipeline.Run(ctx, pipeline.Config{
		Workers: workers,
		// Coordinate-only producer: the Process stage decodes each tile by
		// (x,y) via lvl.DecodedTile, which routes through opentile-go's
		// level-decode — handling every source compression (LZW / uncompressed
		// / Deflate / JPEG / JP2K / …), not just the JPEG/JP2K a standalone
		// codec-of-bytes decode covers. DecodedTile is safe for concurrent use
		// (ReadAt-based tile reads + a mutex/channel decoder pool), so decode
		// stays parallel across the worker pool.
		Source: func(ctx context.Context, emit func(pipeline.Tile) error) error {
			for ty := 0; ty < grid.Y; ty++ {
				for tx := 0; tx < grid.X; tx++ {
					if err := emit(pipeline.Tile{
						Level: lvl.Index(),
						X:     uint32(tx),
						Y:     uint32(ty),
					}); err != nil {
						return err
					}
				}
			}
			return nil
		},
		Process: func(t pipeline.Tile) (pipeline.Tile, error) {
			img, err := lvl.DecodedTile(int(t.X), int(t.Y))
			if err != nil {
				return pipeline.Tile{}, err
			}
			tileRGB := tightRGB(img)
			encoded, err := enc.EncodeTile(tileRGB, tileW, tileH, nil)
			if err != nil {
				return pipeline.Tile{}, err
			}
			t.Bytes = encoded
			return t, nil
		},
		Sink: sink,
	})

	// Tell the buffer no more tiles will arrive.
	lh.CloseInput()

	// Propagate pipeline error (or wait for drain to finish).
	if pipeErr != nil {
		lh.Abort(pipeErr)
		<-drainErr
		return pipeErr
	}
	return <-drainErr
}

// encoderChromaSubsampling probes the codec by encoding a tiny throwaway
// tile and parsing the chroma subsampling actually present in the bytes it
// produces. Returns ok=false for codecs whose output is not a JPEG (no SOF
// marker) — so YCbCrSubSampling (530) is emitted only for JPEG output, with
// a value that matches what we write rather than an assumed constant.
func encoderChromaSubsampling(fac codec.EncoderFactory, knobs map[string]string) ([]uint16, bool) {
	const probe = 16
	enc, err := fac.NewEncoder(codec.LevelGeometry{
		TileWidth: probe, TileHeight: probe, PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: knobs})
	if err != nil {
		return nil, false
	}
	defer enc.Close()
	// Pixel content is irrelevant; chroma subsampling is fixed at encoder
	// construction, so an all-zero probe tile reveals the real value.
	out, err := enc.EncodeTile(make([]byte, probe*probe*3), probe, probe, nil)
	if err != nil {
		return nil, false
	}
	if h, v, ok := qualityjpeg.LumaSampling(out); ok {
		return []uint16{h, v}, true
	}
	return nil, false
}

// tightRGB returns the decoded tile's pixels as a tightly packed RGB buffer
// (tileW*tileH*3, no per-row stride padding) — the form the codec EncodeTile
// expects. When the decoder already produced a tight buffer (Stride ==
// Width*3) the backing slice is returned directly; otherwise rows are
// compacted into a fresh buffer.
func tightRGB(img *decoder.Image) []byte {
	rowBytes := img.Width * 3
	if img.Stride == rowBytes {
		return img.Pix[:img.Height*rowBytes]
	}
	out := make([]byte, img.Height*rowBytes)
	for y := 0; y < img.Height; y++ {
		copy(out[y*rowBytes:(y+1)*rowBytes], img.Pix[y*img.Stride:y*img.Stride+rowBytes])
	}
	return out
}

// writeTIFFTileCopy copies src's pyramid into w (verbatim tiles, async drain)
// and writes its associated images per plan. l0Desc is the L0 ImageDescription
// (OME-XML / Aperio header) emitted as an L0-only ExtraTag for svs/ome-tiff.
// Caller owns Create/Close/Abort. Does NOT call w.Abort() on error — returns it.
func writeTIFFTileCopy(w *streamwriter.Writer, src source.Source, container, l0Desc string, omeSynthetic bool, plan omeEditPlan) error {
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
		// SVS / OME-TIFF: emit the container-shaped ImageDescription on L0.
		// SVS needs the "Aperio" prefix; OME needs the "OME>" suffix; both
		// are emitted as L0-only ExtraTags so other IFDs aren't tagged.
		if lvl.Index() == 0 && l0Desc != "" && (container == "svs" || container == "ome-tiff") {
			spec.ExtraTags = buildL0ImageDescriptionTag(l0Desc)
		}

		lh, err := w.AddLevel(spec)
		if err != nil {
			return fmt.Errorf("add level %d: %w", lvl.Index(), err)
		}

		// Drain the reorder buffer concurrently with the submit loop. The
		// buffer is bounded (capacity ~1024); without a concurrent drainer,
		// WriteTile blocks forever once a level has more tiles than the
		// capacity (deferring the drain to Close deadlocks the submit loop).
		// Mirrors the re-encode path's drain in transcodePyramid.
		drainErr := make(chan error, 1)
		go func() {
			for {
				idx, bytes, ok, err := lh.NextReady()
				if err != nil {
					drainErr <- err
					return
				}
				if !ok {
					drainErr <- nil
					return
				}
				if err := lh.WriteTileAtIndex(idx, bytes); err != nil {
					lh.Abort(err)
					drainErr <- err
					return
				}
			}
		}()

		buf := make([]byte, lvl.TileMaxSize())
		grid := lvl.Grid()
		for ty := 0; ty < grid.Y; ty++ {
			for tx := 0; tx < grid.X; tx++ {
				n, err := lvl.TileInto(tx, ty, buf)
				if err != nil {
					lh.Abort(err)
					<-drainErr
					return fmt.Errorf("read tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
				// WriteTile submits asynchronously into a reorder buffer
				// and may reference the slice past return; copy out of the
				// reused tile-read buffer before handing off.
				tile := append([]byte(nil), buf[:n]...)
				if err := lh.WriteTile(uint32(tx), uint32(ty), tile); err != nil {
					lh.Abort(err)
					<-drainErr
					return fmt.Errorf("write tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
			}
		}
		// Signal end of input; wait for the concurrent drain to finish
		// writing all tiles for this level.
		lh.CloseInput()
		if err := <-drainErr; err != nil {
			return fmt.Errorf("drain level %d: %w", lvl.Index(), err)
		}
		if _, err := emitSVSThumbnailAtL0(src, w, lvl.Index(), container, omeSynthetic, plan); err != nil {
			return err
		}
	}

	if err := writeAssociatedImages(src, w, container, omeSynthetic, plan); err != nil {
		return err
	}
	return nil
}

// omeAssocName maps a wsitools associated-image type to the OME-XML Image Name
// the reader recognizes ("label"/"macro"/"thumbnail"), or "" if the type has
// no OME equivalent and must be omitted from OME output (otherwise the reader
// would mis-classify it as a second pyramid).
func omeAssocName(typ string) string {
	switch typ {
	case "label":
		return "label"
	case "macro", "overview":
		return "macro"
	case "thumbnail":
		return "thumbnail"
	}
	return ""
}

// omeAssociatedSpecs returns the recognized associated images for OME output,
// in src.Associated() order — the SAME order writeAssociatedImages writes them,
// so OME-XML <Image> positions line up with top-level IFD positions. The plan
// applies the same edit as writeAssociatedImages: skip plan.remove, substitute
// (or upsert) plan.replace, or emit nothing when plan.dropAll.
func omeAssociatedSpecs(src source.Source, plan omeEditPlan) []OMEAssoc {
	if plan.dropAll {
		return nil
	}
	var out []OMEAssoc
	replaced := false
	for _, a := range src.Associated() {
		if plan.remove != "" && a.Type() == plan.remove {
			continue
		}
		if plan.replace != "" && a.Type() == plan.replace {
			name := omeAssocName(plan.replace)
			if name == "" {
				slog.Debug("ome: dropping associated image with no OME mapping", "type", plan.replace)
				continue
			}
			out = append(out, OMEAssoc{Name: name, W: plan.spec.Width, H: plan.spec.Height})
			replaced = true
			continue
		}
		name := omeAssocName(a.Type())
		if name == "" {
			slog.Debug("ome: dropping associated image with no OME mapping", "type", a.Type())
			continue
		}
		out = append(out, OMEAssoc{Name: name, W: uint32(a.Size().X), H: uint32(a.Size().Y)})
	}
	// Upsert: plan.replace was not present in the source set.
	if plan.replace != "" && !replaced {
		if name := omeAssocName(plan.replace); name != "" {
			out = append(out, OMEAssoc{Name: name, W: plan.spec.Width, H: plan.spec.Height})
		} else {
			slog.Debug("ome: dropping associated image with no OME mapping", "type", plan.replace)
		}
	}
	return out
}

// emitOneAssociated emits associated image `a` as a single stripped IFD, applying
// the edit plan and the container's classification tags. Returns whether an IFD
// was written (false when the plan removes it, an OME-unmapped type is filtered
// under synthetic OME output, or the codec can't be faithfully copied). The
// caller owns plan.replace bookkeeping (the `replaced` flag) — this helper does
// not track it.
func emitOneAssociated(src source.Source, w *streamwriter.Writer, a source.AssociatedImage, container string, omeSynthetic bool, plan omeEditPlan) (bool, error) {
	if plan.remove != "" && a.Type() == plan.remove {
		return false, nil
	}
	if plan.replace != "" && a.Type() == plan.replace {
		// Under synthetic OME output, an OME-unmapped type emits no <Image>, so
		// it must emit no IFD either (else OME-XML and IFDs desync).
		if container == "ome-tiff" && omeSynthetic && omeAssocName(plan.replace) == "" {
			return false, nil
		}
		if err := w.AddStripped(*plan.spec); err != nil {
			return false, fmt.Errorf("write associated %s: %w", a.Type(), err)
		}
		return true, nil
	}
	if container == "ome-tiff" && omeSynthetic && omeAssocName(a.Type()) == "" {
		return false, nil
	}
	spec, err := faithfulStrippedSpec(a)
	if err != nil {
		if errors.Is(err, errSkipAssociated) {
			slog.Warn("skipping associated", "type", a.Type(), "reason", err)
			return false, nil
		}
		return false, fmt.Errorf("associated %s: %w", a.Type(), err)
	}
	spec.BitsPerSample = []uint16{8, 8, 8}
	spec.NewSubfileType = newSubfileTypeForAssoc(container, a.Type())
	spec.WSIImageType = a.Type()
	// SVS-shaped output: emit Aperio-flavored NewSubfileType via ExtraTags
	// (macro=9, label=1). Clear spec.NewSubfileType so the writer doesn't also
	// emit a default value — EntryBuilder doesn't dedup, so a duplicate tag
	// would corrupt the IFD.
	if container == "svs" {
		switch a.Type() {
		case "macro", "overview":
			spec.NewSubfileType = 0
			spec.ExtraTags = buildSVSMacroExtraTags()
		case "label":
			spec.NewSubfileType = 0
			spec.ExtraTags = buildSVSLabelExtraTags()
		}
	}
	if err := w.AddStripped(spec); err != nil {
		return false, fmt.Errorf("write associated %s: %w", a.Type(), err)
	}
	return true, nil
}

// emitSVSThumbnailAtL0 emits the SVS thumbnail as IFD 1, called right after L0 in
// each level loop. opentile classifies the SVS thumbnail positionally (page 1,
// non-tiled), so on a multi-level slide it must precede L1. No-op unless
// container=="svs" && lvlIndex==0. Honors the plan via emitOneAssociated
// (dropAll/remove emit nothing; replace emits plan.spec). Handles the upsert
// (replace a thumbnail the source lacks). Returns whether an IFD was emitted.
func emitSVSThumbnailAtL0(src source.Source, w *streamwriter.Writer, lvlIndex int, container string, omeSynthetic bool, plan omeEditPlan) (bool, error) {
	if container != "svs" || lvlIndex != 0 || plan.dropAll {
		return false, nil
	}
	for _, a := range src.Associated() {
		if a.Type() == "thumbnail" {
			return emitOneAssociated(src, w, a, container, omeSynthetic, plan)
		}
	}
	// Upsert: source has no thumbnail but the plan replaces (adds) one.
	if plan.replace == "thumbnail" {
		if err := w.AddStripped(*plan.spec); err != nil {
			return false, fmt.Errorf("write thumbnail: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func writeAssociatedImages(src source.Source, w *streamwriter.Writer, container string, omeSynthetic bool, plan omeEditPlan) error {
	if plan.dropAll {
		return nil
	}
	replaced := false
	for _, a := range src.Associated() {
		if container == "svs" && a.Type() == "thumbnail" {
			if plan.replace == "thumbnail" {
				replaced = true // handled at IFD 1 by emitSVSThumbnailAtL0
			}
			continue
		}
		if plan.replace != "" && a.Type() == plan.replace {
			replaced = true
		}
		if _, err := emitOneAssociated(src, w, a, container, omeSynthetic, plan); err != nil {
			return err
		}
	}
	// Upsert: plan.replace absent from source. Skip OME-unmapped synthetic types
	// and the SVS thumbnail (the latter is upserted at IFD 1 by emitSVSThumbnailAtL0).
	if plan.replace != "" && !replaced &&
		!(container == "ome-tiff" && omeSynthetic && omeAssocName(plan.replace) == "") &&
		!(container == "svs" && plan.replace == "thumbnail") {
		if err := w.AddStripped(*plan.spec); err != nil {
			return fmt.Errorf("write associated %s: %w", plan.replace, err)
		}
	}
	return nil
}

func mapCompressionForOutput(c source.Compression) uint16 {
	switch c {
	case source.CompressionJPEG:
		return tiff.CompressionJPEG
	case source.CompressionLZW:
		return tiff.CompressionLZW
	case source.CompressionJPEG2000:
		return tiff.CompressionJPEG2000
	case source.CompressionDeflate:
		return tiff.CompressionDeflate
	}
	return tiff.CompressionNone
}

// newSubfileTypeForLevel returns the NewSubfileType for a pyramid IFD.
// L0 is non-reduced (0); all other levels are reduced-resolution (1).
// The Aperio convention is the same for SVS-shaped output.
func newSubfileTypeForLevel(idx int, container string) uint32 {
	// Aperio SVS convention: all pyramid IFDs use NewSubfileType=0.
	// Setting the reduced-res bit on L1+ would make opentile-go's SVS
	// reader (which follows tifffile's _series_svs algorithm) terminate
	// the baseline walk at L1 and misclassify subsequent pyramid pages
	// as Label/Macro.
	if container == "svs" {
		return 0
	}
	if idx == 0 {
		return 0
	}
	return 1
}

// newSubfileTypeForAssoc returns the default NewSubfileType for an
// associated image. Any associated image is reduced-resolution (1).
// SVS-shaped output adds the Aperio-private macro=9 marker via
// ExtraTags in writeAssociatedImages — that path doesn't need special
// handling here.
func newSubfileTypeForAssoc(container, typ string) uint32 {
	_ = container
	_ = typ
	return 1
}

func buildProvenanceDesc(src source.Source, codecName string, md source.Metadata) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wsi-tools/%s transcode source=%s codec=%s", Version, src.Format(), codecName)
	if md.MPP > 0 {
		fmt.Fprintf(&b, " mpp=%v", md.MPP)
	}
	if md.Magnification > 0 {
		fmt.Fprintf(&b, " mag=%vx", md.Magnification)
	}
	if md.Make != "" || md.Model != "" {
		fmt.Fprintf(&b, " scanner=%q", strings.TrimSpace(md.Make+" "+md.Model))
	}
	if !md.AcquisitionDateTime.IsZero() {
		fmt.Fprintf(&b, " date=%s", md.AcquisitionDateTime.Format("2006-01-02"))
	}
	return b.String()
}
