package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/resample"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/dzi"
	"github.com/wsilabs/wsitools/internal/retile"
	"github.com/wsilabs/wsitools/internal/source"
)

// runConvertDZI emits a Deep Zoom Image pyramid from input to cvOutput.
// cvOutput names the manifest file (e.g. /tmp/foo.dzi); the tile-tree
// directory is derived by stripping the .dzi extension and appending
// _files.
func runConvertDZI(cmd *cobra.Command, input string, start time.Time) error {
	src, slide, err := source.OpenWithSlide(input)
	if err != nil {
		return fmt.Errorf("open slide: %w", err)
	}
	defer src.Close()

	base := strings.TrimSuffix(cvOutput, ".dzi")
	manifestPath := base + ".dzi"
	if !cvForce {
		if _, err := os.Stat(manifestPath); err == nil {
			return fmt.Errorf("%s exists (use --force)", manifestPath)
		}
		if _, err := os.Stat(base + "_files"); err == nil {
			return fmt.Errorf("%s_files exists (use --force)", base)
		}
	}
	root := filepath.Dir(manifestPath)
	name := filepath.Base(base)

	// Source L0 dimensions; the output is reduced by --factor / --target-mag.
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
	srcRegion, err := resolveConvertRect(cmd, srcW, srcH)
	if err != nil {
		return err
	}
	outW, outH, err := reducedDims(srcRegion.Size.W, srcRegion.Size.H, factor)
	if err != nil {
		return err
	}

	dziFormat, err := resolveDZIFormat(cvCodec, cmd.Flags().Changed("codec"), cvDZIFormat)
	if err != nil {
		return err
	}
	tileSize, overlap := resolveTileSize(l0.TileSize.W, cvTileSize), cvDZIOverlap
	if cvLossless {
		res, lerr := losslessDZIConfig(losslessDZIInputs{
			isJPEG:          src.Levels()[0].Compression() == source.CompressionJPEG,
			srcTileSize:     l0.TileSize.W,
			factor:          factor,
			rectSet:         rectFlagsSet(cmd),
			userSetTileSize: cmd.Flags().Changed("tile-size"),
			userSetOverlap:  cmd.Flags().Changed("dzi-overlap"),
			reqTileSize:     resolveTileSize(l0.TileSize.W, cvTileSize),
			reqOverlap:      cvDZIOverlap,
		})
		if lerr != nil {
			return lerr
		}
		tileSize, overlap = res.tileSize, res.overlap
		fmt.Printf("lossless: base tiles copied verbatim (tile-size %d, overlap 0); edges + lower levels regenerated\n", tileSize)
	}
	cfg := dzi.Config{
		Name: name, Width: outW, Height: outH,
		Format: dziFormat, TileSize: tileSize, Overlap: overlap,
	}
	w, err := dzi.NewWriter(&dirFS{root: root}, cfg)
	if err != nil {
		return err
	}
	if err := emitDZIPyramid(cmd.Context(), slide, w, cfg, srcRegion, cvLossless, &l0); err != nil {
		return err
	}
	if err := writeAssociatedPNGs(src, w.WriteAssociated); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %s + %s_files/ (%s)\n", manifestPath, base, time.Since(start).Round(time.Millisecond))
	return nil
}

// dirFS is a dzi.WriteFS backed by the local filesystem.
type dirFS struct{ root string }

func (fs *dirFS) Create(path string) (io.WriteCloser, error) {
	full := filepath.Join(fs.root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	return os.Create(full)
}

// dziTileSink is implemented by both *dzi.Writer and *szi.Writer.
type dziTileSink interface {
	WriteTile(level, col, row int, body []byte) error
}

// emitDZIPyramid drives the streaming retile engine to fill the DZI/SZI tile
// tree. srcRegion is the source L0 region to read (full slide or a crop rect);
// cfg.Width/Height are the (possibly --factor-reduced) output dimensions. The
// engine scales srcRegion to the output and descends the octave pyramid.
// When lossless is true, a losslessDZISink wraps the writer sink so that interior
// base tiles are copied verbatim from srcL0 instead of being re-encoded; edge base
// tiles and all lower levels continue to use the engine's encoded output.
func emitDZIPyramid(ctx context.Context, slide *opentile.Slide, w dziTileSink, cfg dzi.Config, srcRegion opentile.Region, lossless bool, srcL0 losslessTileReader) error {
	levels := retile.ComputeLevels(
		opentile.Size{W: cfg.Width, H: cfg.Height},
		cfg.TileSize, cfg.TileSize, cfg.Overlap,
		2 /*octave*/, dziOctaveCount(cfg.Width, cfg.Height),
	)
	enc, err := newDZIStandaloneEncoder(cfg.Format, cfg.TileSize, parseDZIQuality(cvQuality))
	if err != nil {
		return err
	}
	defer enc.Close()

	// Nearest at identity scale (no --factor or --rect) — matches the profiled
	// fast path; Box (area-averaging) when the top read is a real downscale.
	kernel := resample.Nearest
	if srcRegion.Size.W != cfg.Width || srcRegion.Size.H != cfg.Height {
		kernel = resample.Box
	}

	var sink retile.TileSink = newDZIWriterSink(w, len(levels))
	if lossless {
		sink = &losslessDZISink{
			inner:    sink,
			src:      srcL0,
			baseW:    cfg.Width,
			baseH:    cfg.Height,
			tileSize: cfg.TileSize,
		}
	}

	return retile.Run(ctx, retile.Spec{
		Slide:     slide,
		SrcRegion: srcRegion,
		OutL0:     opentile.Size{W: cfg.Width, H: cfg.Height},
		Levels:    levels,
		Kernel:    kernel,
		Encoder:   enc,
		Sink:      sink,
		Workers:   cvWorkers,
	})
}

// dziOctaveCount returns the number of DZI levels (native down to 1×1).
func dziOctaveCount(w, h int) int { return dzi.MaxLevel(w, h) + 1 }

// dziWriterSink adapts a dziTileSink (dzi.Writer/szi.Writer) to retile.TileSink.
// The engine numbers levels finest-first (k=0); DZI numbers them coarsest-first
// (level 0 = 1×1, level MaxLevel = native). With nLevels engine levels, engine k
// maps to DZI level (nLevels-1) - k.
type dziWriterSink struct {
	w       dziTileSink
	nLevels int
}

func newDZIWriterSink(w dziTileSink, nLevels int) *dziWriterSink {
	return &dziWriterSink{w: w, nLevels: nLevels}
}

func (s *dziWriterSink) WriteTile(level, col, row int, encoded []byte) error {
	return s.w.WriteTile(s.nLevels-1-level, col, row, encoded)
}

// losslessTileReader is the subset of opentile's *Level the lossless sink needs.
type losslessTileReader interface {
	TileMaxSize() int
	TileInto(tx, ty int, dst []byte) (int, error)
}

// losslessDZISink wraps a TileSink. For the engine's finest level (level 0 = DZI
// base = native) it substitutes the verbatim source tile for INTERIOR tiles (a
// complete standalone JPEG from src.TileInto), giving byte-identical interior base
// tiles with no re-encode. Edge base tiles and all lower levels pass the engine's
// encoded bytes through unchanged.
type losslessDZISink struct {
	inner    retile.TileSink
	src      losslessTileReader
	baseW    int
	baseH    int
	tileSize int
}

func (s *losslessDZISink) WriteTile(level, col, row int, encoded []byte) error {
	if level == 0 {
		tw, th := dzi.EdgeTileDims(s.baseW, s.baseH, s.tileSize, col, row)
		if tw == s.tileSize && th == s.tileSize { // interior
			buf := make([]byte, s.src.TileMaxSize())
			n, err := s.src.TileInto(col, row, buf)
			if err != nil {
				return fmt.Errorf("lossless: read source tile (%d,%d): %w", col, row, err)
			}
			return s.inner.WriteTile(level, col, row, buf[:n])
		}
	}
	return s.inner.WriteTile(level, col, row, encoded)
}

// dziStandaloneEncoder produces self-contained tiles: JPEG via libjpeg-turbo
// (EncodeStandalone) or PNG via the registered internal/codec/png encoder.
// Implements retile.TileEncoder.
type dziStandaloneEncoder struct {
	format string
	jpeg   *jpeg.Encoder // non-nil for jpeg
	png    codec.Encoder // non-nil for png
}

func newDZIStandaloneEncoder(format string, tileSize, quality int) (*dziStandaloneEncoder, error) {
	switch format {
	case "jpeg":
		enc, err := jpeg.New(
			codec.LevelGeometry{TileWidth: tileSize, TileHeight: tileSize},
			codec.Quality{Knobs: map[string]string{"q": strconv.Itoa(quality)}},
		)
		if err != nil {
			return nil, fmt.Errorf("jpeg.New: %w", err)
		}
		return &dziStandaloneEncoder{format: "jpeg", jpeg: enc}, nil
	case "png":
		fac, err := codec.Lookup("png")
		if err != nil {
			return nil, fmt.Errorf("png codec unavailable: %w", err)
		}
		enc, err := fac.NewEncoder(
			codec.LevelGeometry{TileWidth: tileSize, TileHeight: tileSize, PixelFormat: codec.PixelFormatRGB8},
			codec.Quality{},
		)
		if err != nil {
			return nil, fmt.Errorf("png.NewEncoder: %w", err)
		}
		return &dziStandaloneEncoder{format: "png", png: enc}, nil
	default:
		return nil, fmt.Errorf("unsupported dzi format %q", format)
	}
}

func (e *dziStandaloneEncoder) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	switch e.format {
	case "jpeg":
		return e.jpeg.EncodeStandalone(rgb, w, h)
	case "png":
		return e.png.EncodeTile(rgb, w, h, nil)
	default:
		return nil, fmt.Errorf("unsupported dzi format %q", e.format)
	}
}

func (e *dziStandaloneEncoder) Close() error {
	if e.jpeg != nil {
		return e.jpeg.Close()
	}
	if e.png != nil {
		return e.png.Close()
	}
	return nil
}

// resolveFactor resolves the effective downsample factor from --factor /
// --target-mag for the dzi/szi targets (which have no metadata to mutate, so
// only the factor matters). --target-mag derives the factor from the source's
// Aperio AppMag (SVS) or opentile magnification; otherwise --factor is used
// directly. Returns a validated power-of-2 in {2,4,8,16}, or 1 for no scaling.
func resolveFactor(src source.Source, input string, factor, targetMag int) (int, error) {
	if targetMag > 0 {
		var srcMag float64
		rawDesc, _ := source.ReadSourceImageDescription(input)
		if desc, derr := ParseImageDescription(rawDesc); derr == nil && src.Format() == string(opentile.FormatSVS) {
			srcMag = desc.AppMag
		} else {
			srcMag = src.Metadata().Magnification
		}
		if srcMag <= 0 {
			return 0, fmt.Errorf("--target-mag set but source AppMag is unknown/zero")
		}
		ratio := srcMag / float64(targetMag)
		f := int(ratio + 0.0001)
		if !isValidFactor(f) || float64(f) != ratio {
			return 0, fmt.Errorf("source AppMag %g / target %d = %g is not a valid power-of-2 in {2,4,8,16}", srcMag, targetMag, ratio)
		}
		return f, nil
	}
	if factor != 1 && !isValidFactor(factor) {
		return 0, fmt.Errorf("--factor must be one of {2,4,8,16}, got %d", factor)
	}
	return factor, nil
}

// reducedDims returns the source dims divided by factor, erroring if the image
// is too small for the factor (a reduced dimension would be < 1).
func reducedDims(srcW, srcH, factor int) (int, int, error) {
	w, h := srcW/factor, srcH/factor
	if w < 1 || h < 1 {
		return 0, 0, fmt.Errorf("--factor %d too large for L0 %dx%d (reduced dim < 1)", factor, srcW, srcH)
	}
	return w, h, nil
}

// parseDZIQuality parses --quality as a JPEG quality (1..100).
// Empty string → 85.
func parseDZIQuality(s string) int {
	if s == "" {
		return 85
	}
	q, err := strconv.Atoi(s)
	if err != nil || q < 1 || q > 100 {
		return 85
	}
	return q
}
