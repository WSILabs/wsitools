package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
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
			).Encode()
		}
	case "ome-tiff":
		if src.Format() == string(opentile.FormatOMETIFF) {
			srcImageDesc = src.SourceImageDescription()
		} else {
			srcImageDesc = SyntheticOMEDescription(
				uint32(l0.Size().X), uint32(l0.Size().Y),
				md.MPP, md.MPP, "Image", srcSoft,
				omeAssociatedSpecs(src),
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
		if lvl.Index() == 0 && srcImageDesc != "" && (container == "svs" || container == "ome-tiff") {
			spec.ExtraTags = buildL0ImageDescriptionTag(srcImageDesc)
		}

		lh, err := w.AddLevel(spec)
		if err != nil {
			w.Abort()
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
					w.Abort()
					return fmt.Errorf("read tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
				// WriteTile submits asynchronously into a reorder buffer
				// and may reference the slice past return; copy out of the
				// reused tile-read buffer before handing off.
				tile := append([]byte(nil), buf[:n]...)
				if err := lh.WriteTile(uint32(tx), uint32(ty), tile); err != nil {
					lh.Abort(err)
					<-drainErr
					w.Abort()
					return fmt.Errorf("write tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
			}
		}
		// Signal end of input; wait for the concurrent drain to finish
		// writing all tiles for this level.
		lh.CloseInput()
		if err := <-drainErr; err != nil {
			w.Abort()
			return fmt.Errorf("drain level %d: %w", lvl.Index(), err)
		}
	}

	if !cvNoAssociated {
		omeSynthetic := container == "ome-tiff" && src.Format() != string(opentile.FormatOMETIFF)
		if err := writeAssociatedImages(src, w, container, omeSynthetic); err != nil {
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

	// Parse quality: default 85 when absent.
	qualityInt := 85
	if quality != "" {
		if _, err := fmt.Sscanf(quality, "%d", &qualityInt); err != nil {
			return fmt.Errorf("--quality %q: must be an integer 1..100", quality)
		}
	}
	if qualityInt < 1 || qualityInt > 100 {
		return fmt.Errorf("--quality must be 1..100")
	}

	// Workers: 0 means GOMAXPROCS.
	if workers == 0 {
		workers = runtime.NumCPU()
	}

	fac, err := codec.Lookup(codecName)
	if err != nil {
		return fmt.Errorf("--codec %q: %w", codecName, err)
	}

	src, err := source.Open(input)
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

	knobs := map[string]string{"q": fmt.Sprintf("%d", qualityInt)}

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
			).Encode()
		}
	case "ome-tiff":
		if src.Format() == string(opentile.FormatOMETIFF) {
			srcImageDesc = src.SourceImageDescription()
		} else {
			srcImageDesc = SyntheticOMEDescription(
				uint32(l0.Size().X), uint32(l0.Size().Y),
				md.MPP, md.MPP, "Image", srcSoft,
				omeAssociatedSpecs(src),
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

	if err := transcodePyramid(cmd.Context(), src, w, fac, knobs, workers, resolvedContainer, srcImageDesc); err != nil {
		w.Abort()
		return err
	}

	if !cvNoAssociated {
		omeSynthetic := resolvedContainer == "ome-tiff" && src.Format() != string(opentile.FormatOMETIFF)
		if err := writeAssociatedImages(src, w, resolvedContainer, omeSynthetic); err != nil {
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

func transcodePyramid(ctx context.Context, src source.Source, w *streamwriter.Writer, fac codec.EncoderFactory, knobs map[string]string, workers int, container, srcImageDesc string) error {
	for _, lvl := range src.Levels() {
		if err := transcodeLevel(ctx, lvl, w, fac, knobs, workers, container, srcImageDesc); err != nil {
			return fmt.Errorf("level %d: %w", lvl.Index(), err)
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

	decFac := pickDecoder(lvl.Compression())
	if decFac == nil {
		return fmt.Errorf("no decoder for source compression %s", lvl.Compression())
	}

	grid := lvl.Grid()
	tileW := lvl.TileSize().X
	tileH := lvl.TileSize().Y
	maxTileBytes := lvl.TileMaxSize()
	pool := &sync.Pool{
		New: func() any {
			b := make([]byte, maxTileBytes)
			return &b
		},
	}

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
		Source: func(ctx context.Context, emit func(pipeline.Tile) error) error {
			for ty := 0; ty < grid.Y; ty++ {
				for tx := 0; tx < grid.X; tx++ {
					bufp := pool.Get().(*[]byte)
					n, err := lvl.TileInto(tx, ty, *bufp)
					if err != nil {
						pool.Put(bufp)
						return err
					}
					t := pipeline.Tile{
						Level:   lvl.Index(),
						X:       uint32(tx),
						Y:       uint32(ty),
						Bytes:   (*bufp)[:n],
						Release: func() { pool.Put(bufp) },
					}
					if err := emit(t); err != nil {
						pool.Put(bufp)
						return err
					}
				}
			}
			return nil
		},
		Process: func(t pipeline.Tile) (pipeline.Tile, error) {
			dec := decFac.New()
			defer dec.Close()
			img, err := dec.Decode(t.Bytes, decoder.DecodeOptions{
				Scale:  1,
				Format: decoder.PixelFormatRGB,
			})
			if t.Release != nil {
				t.Release()
				t.Release = nil
			}
			if err != nil {
				return pipeline.Tile{}, err
			}
			encoded, err := enc.EncodeTile(img.Pix, tileW, tileH, nil)
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

func pickDecoder(c source.Compression) decoder.Factory {
	var name string
	switch c {
	case source.CompressionJPEG:
		name = "jpeg"
	case source.CompressionJPEG2000:
		name = "jpeg2000"
	default:
		return nil
	}
	fac, ok := decoder.Get(name)
	if !ok {
		return nil
	}
	return fac
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
// so OME-XML <Image> positions line up with top-level IFD positions.
func omeAssociatedSpecs(src source.Source) []OMEAssoc {
	var out []OMEAssoc
	for _, a := range src.Associated() {
		name := omeAssocName(a.Type())
		if name == "" {
			slog.Debug("ome: dropping associated image with no OME mapping", "type", a.Type())
			continue
		}
		out = append(out, OMEAssoc{Name: name, W: uint32(a.Size().X), H: uint32(a.Size().Y)})
	}
	return out
}

func writeAssociatedImages(src source.Source, w *streamwriter.Writer, container string, omeSynthetic bool) error {
	for _, a := range src.Associated() {
		// Synthetic OME path only: keep the written associated IFDs in sync
		// with the <Image> entries omeAssociatedSpecs emitted (recognized
		// types, same order). The native ome→ome path keeps the verbatim
		// source OME-XML, which already describes its own associated images,
		// so it is not filtered here.
		if container == "ome-tiff" && omeSynthetic && omeAssocName(a.Type()) == "" {
			continue
		}
		bs, err := a.Bytes()
		if err != nil {
			return fmt.Errorf("associated %s: %w", a.Type(), err)
		}
		spec := streamwriter.StrippedSpec{
			Width:           uint32(a.Size().X),
			Height:          uint32(a.Size().Y),
			RowsPerStrip:    uint32(a.Size().Y),
			BitsPerSample:   []uint16{8, 8, 8},
			SamplesPerPixel: 3,
			Photometric:     2,
			Compression:     mapCompressionForOutput(a.Compression()),
			StripBytes:      bs,
			NewSubfileType:  newSubfileTypeForAssoc(container, a.Type()),
			WSIImageType:    a.Type(),
		}
		// SVS-shaped output: emit Aperio-flavored NewSubfileType via
		// ExtraTags (macro=9, label=1). Clear spec.NewSubfileType so the
		// writer doesn't also emit a default value — EntryBuilder doesn't
		// dedup, so a duplicate tag would corrupt the IFD.
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
			return fmt.Errorf("write associated %s: %w", a.Type(), err)
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
