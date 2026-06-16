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

	"github.com/wsilabs/wsitools/internal/dzi"
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
	outW, outH, err := reducedDims(srcW, srcH, factor)
	if err != nil {
		return err
	}

	cfg := dzi.Config{
		Name: name, Width: outW, Height: outH,
		Format: cvDZIFormat, TileSize: cvDZITileSize, Overlap: cvDZIOverlap,
	}
	w, err := dzi.NewWriter(&dirFS{root: root}, cfg)
	if err != nil {
		return err
	}
	if err := emitDZIPyramid(cmd.Context(), slide, w, cfg, srcW, srcH); err != nil {
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

// emitDZIPyramid drives the v0.17 pyramid-descent pipeline. srcW/srcH are the
// source L0 dimensions; cfg.Width/Height are the (possibly --factor-reduced)
// output dimensions — the descent scales the source region down to them.
// See runDescent in convert_dzi_descent.go.
func emitDZIPyramid(ctx context.Context, slide *opentile.Slide, w dziTileSink, cfg dzi.Config, srcW, srcH int) error {
	return runDescent(ctx, slide, w, cfg, srcW, srcH, cvWorkers, parseDZIQuality(cvQuality))
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
