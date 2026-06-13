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
)

// runConvertDZI emits a Deep Zoom Image pyramid from input to cvOutput.
// cvOutput names the manifest file (e.g. /tmp/foo.dzi); the tile-tree
// directory is derived by stripping the .dzi extension and appending
// _files.
func runConvertDZI(cmd *cobra.Command, input string, start time.Time) error {
	slide, err := opentile.OpenFile(input)
	if err != nil {
		return fmt.Errorf("open slide: %w", err)
	}
	defer slide.Close()

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

	// L0 dimensions drive the pyramid math.
	images := slide.Pyramids()
	if len(images) == 0 || len(images[0].Levels) == 0 {
		return fmt.Errorf("slide has no pyramid levels")
	}
	l0 := images[0].Levels[0]
	width, height := l0.Size.W, l0.Size.H

	cfg := dzi.Config{
		Name: name, Width: width, Height: height,
		Format: cvDZIFormat, TileSize: cvDZITileSize, Overlap: cvDZIOverlap,
	}
	w, err := dzi.NewWriter(&dirFS{root: root}, cfg)
	if err != nil {
		return err
	}
	if err := emitDZIPyramid(cmd.Context(), slide, w, cfg); err != nil {
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

// emitDZIPyramid drives the v0.17 pyramid-descent pipeline.
// See runDescent in convert_dzi_descent.go.
func emitDZIPyramid(ctx context.Context, slide *opentile.Slide, w dziTileSink, cfg dzi.Config) error {
	return runDescent(ctx, slide, w, cfg, cvWorkers, parseDZIQuality(cvQuality))
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
