// Package main: the downsample subcommand wires opentile-go (read), our
// codecs, and the streamwriter (write) into a single command that produces a
// power-of-2-downsampled, format-preserving pyramid.
//
// Architecture: downsample streams the source L0 region through the shared
// retile engine (buildPyramid -> buildEnginePyramid) to a reduced output L0
// (outL0 = L0/factor), emitting octave-floored levels. The engine reads via
// opentile's ScaledStrips and encodes tile-by-tile through a worker pool, so
// memory is bounded (working strips + the tile-encode pool) rather than holding
// the full L0 raster in RAM. Associated images (label, macro, thumbnail/
// overview) are copied byte-faithfully via opentile-go's AssociatedSourceOf
// (verbatim source strips + Predictor/JPEGTables), falling back to decode+
// re-encode when no faithful source form is available.
//
// dispatchDownsampleByTarget routes to the per-format emitter (downsampleToSVS/
// TIFF/OMETIFF/COGWSI/DICOM); the SVS/TIFF family share buildEnginePyramid with
// crop and convert --factor.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"

	otdecoder "github.com/wsilabs/opentile-go/decoder"
	otresample "github.com/wsilabs/opentile-go/resample"
	codec "github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/downscale"
	"github.com/wsilabs/wsitools/internal/pipeline"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

const (
	// bigTIFFThreshold is the predicted output size at which we auto-promote
	// to BigTIFF (8-byte offsets). Classic TIFF tops out at 4 GiB but we
	// promote earlier with safety margin against late-IFD growth.
	bigTIFFThreshold = int64(2) * 1024 * 1024 * 1024
)

var (
	dsOutput    string
	dsFactor    int
	dsTargetMag int
	dsQuality   int
	dsWorkers   int
	dsForce     bool
	dsTileOrder string
)

var downsampleCmd = &cobra.Command{
	Use:   "downsample [flags] <input>",
	Short: "Downsample a WSI by a power-of-2 factor (format-preserving)",
	Long: `Downsample a WSI by an integer power-of-2 factor (default 2 = 40x → 20x).
Regenerates the entire pyramid from the new L0; passes through associated
images (label, macro, thumbnail, overview) verbatim.

The output container matches the source format:
  SVS        → svs
  OME-TIFF   → ome-tiff
  Generic-TIFF → tiff (plain pyramidal TIFF)
  COG-WSI    → cog-wsi
  DICOM      → dicom (a pyramid directory of level-<n>.dcm instances)

For formats without a matching writer (NDPI, Philips-TIFF, BIF, IFE,
Leica SCN, SZI, …) use 'convert --to {svs|tiff|ome-tiff|cog-wsi}
--factor N' to downsample into a different container.

Examples:

  # 40x → 20x (same format as source)
  wsitools downsample -o slide-20x.svs slide-40x.svs

  # 40x → 10x at higher quality, 8 workers
  wsitools downsample --factor 4 --quality 95 --workers 8 -o out.svs in.svs`,
	Args: cobra.ExactArgs(1),
	RunE: runDownsample,
}

func init() {
	downsampleCmd.Flags().StringVarP(&dsOutput, "output", "o", "", "output file path (required)")
	downsampleCmd.Flags().IntVar(&dsFactor, "factor", 2, "downsample factor (must be a power of 2 in {2,4,8,16})")
	downsampleCmd.Flags().IntVar(&dsTargetMag, "target-mag", 0, "alternative to --factor: derive factor from source AppMag")
	downsampleCmd.Flags().IntVar(&dsQuality, "quality", 85, "JPEG quality 1..100")
	downsampleCmd.Flags().IntVar(&dsWorkers, "workers", runtime.NumCPU(), "worker goroutines")
	downsampleCmd.Flags().BoolVarP(&dsForce, "force", "f", false, "overwrite output if it exists")
	downsampleCmd.Flags().StringVar(&dsTileOrder, "tile-order", "row-major",
		"Tile emission order within each level (row-major|hilbert|morton). "+
			"Format-restricted: SVS accepts row-major only; COG-WSI / TIFF / OME-TIFF "+
			"accept all three.")
	_ = downsampleCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(downsampleCmd)
}

func runDownsample(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true // past arg parsing: runtime errors shouldn't dump the flag wall
	input := args[0]
	start := time.Now()

	slog.Info("starting downsample",
		"input", input,
		"output", dsOutput,
		"factor", dsFactor,
		"target_mag", dsTargetMag,
		"quality", dsQuality,
		"workers", dsWorkers,
	)

	// Probe source format to determine output target.
	src, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	srcFormat := string(src.Format())
	src.Close()

	target, ok := downsampleTargetForFormat(srcFormat)
	if !ok {
		return fmt.Errorf(
			"downsample preserves the source format; %s has no matching writer — "+
				"use 'convert --to {svs|tiff|ome-tiff|cog-wsi} --factor N' to downsample into a different container",
			srcFormat,
		)
	}

	// Delegate to the shared dispatch (same engines used by convert --to X --factor N).
	// bigtiff="" means auto; noAssociated=false (downsample always passes through associated images).
	if err := dispatchDownsampleByTarget(
		cmd.Context(),
		target,
		input,
		dsOutput,
		dsFactor,
		dsTargetMag,
		dsQuality,
		dsWorkers,
		dsTileOrder,
		"", // bigtiff: "" means auto (downsample has no --bigtiff flag)
		dsForce,
		false, // noAssociated: downsample always passes through associated images
		"jpeg",
		// qualityStr: downsample is jpeg-only, so bridge the int flag (default 85,
		// the jpeg codec default) directly to the encoder's knob resolution. This is
		// what makes `downsample --quality N` reach the encoder (the emitters resolve
		// the codec from this string, not from the convert-global cvQuality).
		strconv.Itoa(dsQuality),
	); err != nil {
		return err
	}

	slog.Info("downsample complete",
		"output", dsOutput,
		"elapsed", time.Since(start).Round(time.Millisecond).String(),
	)
	return nil
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// isValidFactor reports whether f is one of {2, 4, 8, 16} (the supported
// libjpeg-turbo fast-scale powers of two for the JPEG path; JP2K path falls
// back to chained Area2x2).
func isValidFactor(f int) bool {
	switch f {
	case 2, 4, 8, 16:
		return true
	}
	return false
}

// predictBigTIFFNeeded estimates whether the output's tile-data + IFD region
// will exceed the classic-TIFF 4 GiB / 32-bit offset ceiling. Heuristic: sum
// of (W*H/factor^2) bytes for an RGB raster across all output levels at JPEG
// compressed roughly 1/8 average → divide by 8. If that exceeds 2 GiB,
// promote to BigTIFF.
func predictBigTIFFNeeded(srcL0 *opentile.Level, levels []*opentile.Level, factor int) bool {
	var total int64
	for _, l := range levels {
		w := int64(l.Size.W / factor)
		h := int64(l.Size.H / factor)
		// JPEG 1/8 compression ratio rough estimate for RGB888.
		total += w * h * 3 / 8
	}
	return total > bigTIFFThreshold
}

// countTilesForLevel returns the number of outTile×outTile tiles needed to
// cover a raster of the given dimensions.
func countTilesForLevel(w, h, outTile int) int {
	tilesX := (w + outTile - 1) / outTile
	tilesY := (h + outTile - 1) / outTile
	return tilesX * tilesY
}

// buildPyramid materialises the output L0 raster from the source L0 (with
// 1/factor fast-scale decode), then iteratively encodes + writes each output
// pyramid level. L1+ rasters are computed in-memory from the previous level
// via 2x2 area-average.
//
// postL0Hook, if non-nil, is called after writing L0 and before writing L1.
// The caller uses this to inject the thumbnail IFD between L0 and L1 to match
// Aperio's quirky IFD ordering convention.
//
// fac+knobs select the tile encoder; pass jpegcodec.Factory{}+{"q":strconv.Itoa(quality)}
// for the default JPEG path.
func buildPyramid(ctx context.Context, src *opentile.Slide, w *streamwriter.Writer, factor int, fac codec.EncoderFactory, knobs map[string]string, workers int, postL0Hook func() error) error {
	srcL0 := src.Levels()[0]
	srcSize := opentile.Size{W: srcL0.Size.W, H: srcL0.Size.H}
	outL0 := opentile.Size{W: srcSize.W / factor, H: srcSize.H / factor}
	if outL0.W <= 0 || outL0.H <= 0 {
		return fmt.Errorf("output L0 dimensions degenerate: %dx%d (factor %d too large)", outL0.W, outL0.H, factor)
	}
	outTile := resolveTileSize(srcL0.TileSize.W, cvTileSize)
	// A downsample rebuilds the pyramid at a reduced L0; a full octave chain is
	// the current behavior (preserving the source's sparse ratios under a
	// resolution change is a separate, nuanced heuristic).
	levels := octaveLevelSpecsFor(outL0, outTile)
	return buildEnginePyramid(ctx, src, w, opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: srcSize}, outL0, levels, outTile, fac, knobs, workers, postL0Hook)
}

// buildEnginePyramid builds a streamwriter pyramid by streaming srcRegion
// through the retile engine to outL0 (octave-floored levels). The codec is
// selected by fac+knobs; Compression is derived from enc.TIFFCompressionTag().
// postL0Hook runs after L0's AddLevel, before L1 (the thumbnail-IFD interleave).
// Shared by downsample (full-L0 region, outL0=L0/factor) and crop (rect region, identity).
// `levels` is the full engine level chain (finest-first). It MAY contain
// Intermediate (non-emitting) octaves — a select-octave chain that preserves a
// source's sparse level ratios. Only the non-intermediate levels become output
// IFDs; the engine still computes the intermediates to feed the box descent.
func buildEnginePyramid(ctx context.Context, slide *opentile.Slide, w *streamwriter.Writer, srcRegion opentile.Region, outL0 opentile.Size, levels []retile.LevelSpec, outTile int, fac codec.EncoderFactory, knobs map[string]string, workers int, postL0Hook func() error) error {
	enc, err := fac.NewEncoder(codec.LevelGeometry{
		TileWidth: outTile, TileHeight: outTile, PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: knobs})
	if err != nil {
		return fmt.Errorf("new encoder: %w", err)
	}
	defer enc.Close()
	tables := enc.LevelHeader()

	var emitted []retile.LevelSpec
	for _, ls := range levels {
		if !ls.Intermediate {
			emitted = append(emitted, ls)
		}
	}

	specFor := func(ls retile.LevelSpec) streamwriter.LevelSpec {
		return streamwriter.LevelSpec{
			ImageWidth:      uint32(ls.Width),
			ImageHeight:     uint32(ls.Height),
			TileWidth:       uint32(outTile),
			TileHeight:      uint32(outTile),
			Compression:     enc.TIFFCompressionTag(),
			Photometric:     enc.TIFFPhotometric(),
			SamplesPerPixel: 3,
			BitsPerSample:   []uint16{8, 8, 8},
			JPEGTables:      tables,
			NewSubfileType:  0,
			WSIImageType:    tiff.WSIImageTypePyramid,
		}
	}

	handles := make([]*streamwriter.LevelHandle, len(emitted))
	h0, err := w.AddLevel(specFor(emitted[0]))
	if err != nil {
		return fmt.Errorf("add level 0: %w", err)
	}
	handles[0] = h0
	// postL0Hook (thumbnail IFD) must land at IFD 1 — between L0 and L1 AddLevels.
	if postL0Hook != nil {
		if err := postL0Hook(); err != nil {
			return fmt.Errorf("post-L0 hook: %w", err)
		}
	}
	for e := 1; e < len(emitted); e++ {
		h, err := w.AddLevel(specFor(emitted[e]))
		if err != nil {
			return fmt.Errorf("add level %d: %w", e, err)
		}
		handles[e] = h
	}

	sink := newStreamwriterSink(handles)
	return runEngineRetile(ctx, slide, srcRegion, outL0, levels, &codecTileEncoder{enc: enc, tileW: outTile, tileH: outTile}, sink, workers)
}

// buildPyramidFromRaster encodes an in-memory RGB888 L0 raster into a tiled
// JPEG pyramid via the streamwriter, box-halving between levels. nLevels is the
// total number of pyramid levels to emit (L0 included). postL0Hook runs
// immediately after L0 (used to interleave the thumbnail IFD). bar may be nil.
func buildPyramidFromRaster(ctx context.Context, w *streamwriter.Writer, l0 []byte, l0W, l0H, nLevels, quality, workers, outTile int, postL0Hook func() error) error {
	// Total tile count across all output levels for the progress bar.
	var totalTiles int64
	{
		lw, lh := l0W, l0H
		for lvl := 0; lvl < nLevels; lvl++ {
			totalTiles += int64(countTilesForLevel(lw, lh, outTile))
			if lvl < nLevels-1 {
				lw /= 2
				lh /= 2
				if lw == 0 || lh == 0 {
					break
				}
			}
		}
	}

	var progress *mpb.Progress
	var bar *mpb.Bar
	if !flagQuiet {
		progress = mpb.New(mpb.WithOutput(os.Stderr))
		bar = progress.AddBar(totalTiles,
			mpb.PrependDecorators(
				decor.Name("encoding "),
				decor.Percentage(decor.WCSyncSpace),
			),
			mpb.AppendDecorators(
				decor.EwmaSpeed(0, "%.0f tiles/s", 30),
				decor.Name(" ETA "),
				decor.EwmaETA(decor.ET_STYLE_GO, 30),
			),
		)
	}

	currentRaster := l0
	currentW, currentH := l0W, l0H

	for outLvl := 0; outLvl < nLevels; outLvl++ {
		lvlStart := time.Now()
		tiles := countTilesForLevel(currentW, currentH, outTile)
		slog.Debug("encoding level", "level", outLvl, "w", currentW, "h", currentH, "tiles", tiles)

		if err := encodeAndWriteLevel(ctx, w, currentRaster, currentW, currentH, quality, workers, outTile, bar); err != nil {
			if progress != nil {
				progress.Wait()
			}
			return fmt.Errorf("level %d: %w", outLvl, err)
		}

		if flagVerbose {
			slog.Info("encoded level", "level", outLvl, "w", currentW, "h", currentH, "tiles", tiles,
				"elapsed", time.Since(lvlStart).Round(time.Millisecond).String())
		}

		if outLvl == 0 && postL0Hook != nil {
			if err := postL0Hook(); err != nil {
				if progress != nil {
					progress.Wait()
				}
				return fmt.Errorf("post-L0 hook: %w", err)
			}
		}

		if outLvl < nLevels-1 {
			var herr error
			currentRaster, currentW, currentH, herr = halveRaster(currentRaster, currentW, currentH)
			if herr != nil {
				if progress != nil {
					progress.Wait()
				}
				return fmt.Errorf("Box halving level %d→%d: %w", outLvl, outLvl+1, herr)
			}
			if currentW == 0 || currentH == 0 {
				break
			}
		}
	}

	if progress != nil {
		progress.Wait()
	}
	return nil
}

// halveRaster box-downscales an RGB888 raster by 2×, truncating odd dimensions
// to even first, returning the half-size raster and its new dimensions. Shared
// by buildPyramidFromRaster's inter-level reduction and the lossless crop's
// L0→L1 reduction so both produce byte-identical L1 pixels.
func halveRaster(raster []byte, w, h int) ([]byte, int, int, error) {
	evenW := w &^ 1
	evenH := h &^ 1
	if evenW != w || evenH != h {
		raster = cropRaster(raster, w, h, evenW, evenH)
		w, h = evenW, evenH
	}
	src := &otdecoder.Image{
		Width:  w,
		Height: h,
		Stride: w * 3,
		Format: otdecoder.PixelFormatRGB,
		Pix:    raster,
	}
	dst := otdecoder.NewImageFormat(w/2, h/2, otdecoder.PixelFormatRGB)
	if err := otresample.ImageInto(src, dst, otresample.Box); err != nil {
		return nil, 0, 0, err
	}
	return dst.Pix, w / 2, h / 2, nil
}

// cropRaster returns a fresh RGB888 buffer of size dstW*dstH*3 containing the
// top-left dstW×dstH region of src (which has stride srcW*3). Used to even up
// dimensions before Area2x2.
func cropRaster(src []byte, srcW, srcH, dstW, dstH int) []byte {
	dst := make([]byte, dstW*dstH*3)
	rowBytes := dstW * 3
	for y := 0; y < dstH; y++ {
		copy(dst[y*rowBytes:(y+1)*rowBytes], src[y*srcW*3:y*srcW*3+rowBytes])
	}
	return dst
}

// encodeAndWriteLevel encodes the in-memory RGB raster into 256x256 abbreviated
// JPEG tiles and writes them via a streamwriter LevelHandle. All pyramid IFDs
// use NewSubfileType=0 — opentile-go's SVS classifier rejects pyramid levels
// with the reduced bit set. bar may be nil when --quiet is set.
func encodeAndWriteLevel(ctx context.Context, w *streamwriter.Writer, raster []byte, levelW, levelH, quality, workers, outTile int, bar *mpb.Bar) error {
	enc, err := jpegcodec.Factory{}.NewEncoder(codec.LevelGeometry{
		TileWidth:   outTile,
		TileHeight:  outTile,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}})
	if err != nil {
		return fmt.Errorf("new encoder: %w", err)
	}
	defer enc.Close()

	tables := enc.LevelHeader()
	lh, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth:      uint32(levelW),
		ImageHeight:     uint32(levelH),
		TileWidth:       uint32(outTile),
		TileHeight:      uint32(outTile),
		Compression:     tiff.CompressionJPEG,
		Photometric:     codec.PhotometricYCbCr, // JPEG tiles are YCbCr
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		JPEGTables:      tables,
		NewSubfileType:  0,
		WSIImageType:    tiff.WSIImageTypePyramid,
	})
	if err != nil {
		return fmt.Errorf("AddLevel: %w", err)
	}

	tilesX := (levelW + outTile - 1) / outTile
	tilesY := (levelH + outTile - 1) / outTile

	source := func(ctx context.Context, emit func(pipeline.Tile) error) error {
		for ty := 0; ty < tilesY; ty++ {
			for tx := 0; tx < tilesX; tx++ {
				tile, err := extractTileFromRaster(raster, levelW, levelH, tx, ty, outTile)
				if err != nil {
					return err
				}
				if err := emit(pipeline.Tile{X: uint32(tx), Y: uint32(ty), Bytes: tile}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	process := func(t pipeline.Tile) (pipeline.Tile, error) {
		out, err := enc.EncodeTile(t.Bytes, outTile, outTile, nil)
		if err != nil {
			return pipeline.Tile{}, err
		}
		t.Bytes = out
		return t, nil
	}
	sink := func(t pipeline.Tile) error {
		if err := lh.WriteTile(t.X, t.Y, t.Bytes); err != nil {
			return err
		}
		if bar != nil {
			bar.Increment()
		}
		return nil
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
		Source:  source,
		Process: process,
		Sink:    sink,
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

// extractTileFromRaster cuts an outTile×outTile RGB tile out of the level
// raster at tile coord (tx, ty). Edge tiles are padded with zero where the
// raster doesn't extend that far. Always returns a fresh outTile×outTile
// buffer for the encoder.
func extractTileFromRaster(raster []byte, rasterW, rasterH, tx, ty, outTile int) ([]byte, error) {
	return downscale.ExtractTile(raster, rasterW, rasterH, tx, ty, outTile), nil
}

// writeOneAssociated writes a single associated image verbatim into the output
// as a single-strip IFD. NewSubfileType is set per the SVS reader classifier
// convention: thumbnail=0, label=1 (reduced bit), overview/macro=9 (reduced +
// macro bit). Compression tag mirrors the source.
func writeOneAssociated(w *streamwriter.Writer, a opentile.AssociatedImage) error {
	spec, err := faithfulStrippedSpecOT(a)
	if err != nil {
		if errors.Is(err, errSkipAssociated) {
			slog.Warn("skipping associated image", "type", a.Type(), "reason", err)
			return nil
		}
		return fmt.Errorf("associated %q: %w", a.Type(), err)
	}
	var subfileType uint32
	var wsiImageType string
	switch a.Type() {
	case "thumbnail":
		subfileType = 0
		wsiImageType = tiff.WSIImageTypeThumbnail
	case "label":
		subfileType = 1
		wsiImageType = tiff.WSIImageTypeLabel
	case "overview":
		subfileType = 9
		wsiImageType = tiff.WSIImageTypeOverview
	case "macro":
		subfileType = 9
		wsiImageType = tiff.WSIImageTypeMacro
	default:
		subfileType = 0
		wsiImageType = tiff.WSIImageTypeAssociated
	}
	spec.BitsPerSample = []uint16{8, 8, 8}
	spec.NewSubfileType = subfileType
	spec.WSIImageType = wsiImageType
	if err := w.AddStripped(spec); err != nil {
		return fmt.Errorf("AddStripped %q: %w", a.Type(), err)
	}
	return nil
}
