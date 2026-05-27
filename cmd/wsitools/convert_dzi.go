package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"

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
	images := slide.Images()
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

// emitDZIPyramid renders every DZI level by calling
// slide.ReadRegionScaled for each tile region.
func emitDZIPyramid(ctx context.Context, slide *opentile.Slide, w dziTileSink, cfg dzi.Config) error {
	max := dzi.MaxLevel(cfg.Width, cfg.Height)
	quality := parseDZIQuality(cvQuality)
	for lvl := max; lvl >= 0; lvl-- {
		lw, lh := dzi.LevelDims(cfg.Width, cfg.Height, lvl)
		cols, rows := dzi.GridDims(lw, lh, cfg.TileSize)
		for row := 0; row < rows; row++ {
			for col := 0; col < cols; col++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				body, err := renderDZITile(slide, cfg, lvl, col, row, quality)
				if err != nil {
					return fmt.Errorf("level %d (%d,%d): %w", lvl, col, row, err)
				}
				if err := w.WriteTile(lvl, col, row, body); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// renderDZITile produces the encoded tile bytes for a single
// (level, col, row) of the DZI pyramid.
func renderDZITile(slide *opentile.Slide, cfg dzi.Config, level, col, row, quality int) ([]byte, error) {
	lw, lh := dzi.LevelDims(cfg.Width, cfg.Height, level)
	max := dzi.MaxLevel(cfg.Width, cfg.Height)
	scale := 1 << (max - level) // L0 pixels per level-pixel

	// Content rect in level coords.
	cx := col * cfg.TileSize
	cy := row * cfg.TileSize
	cw, ch := dzi.EdgeTileDims(lw, lh, cfg.TileSize, col, row)

	// Overlap on each side (suppressed at image edges).
	leftOv := cfg.Overlap
	if col == 0 {
		leftOv = 0
	}
	topOv := cfg.Overlap
	if row == 0 {
		topOv = 0
	}
	rightOv := cfg.Overlap
	if cx+cw >= lw {
		rightOv = 0
	}
	bottomOv := cfg.Overlap
	if cy+ch >= lh {
		bottomOv = 0
	}

	outW := cw + leftOv + rightOv
	outH := ch + topOv + bottomOv

	// Project tile rect (with overlap) into L0 coordinates, clipped to L0.
	l0X := (cx - leftOv) * scale
	l0Y := (cy - topOv) * scale
	l0W := outW * scale
	l0H := outH * scale
	if l0X < 0 {
		l0W += l0X
		l0X = 0
	}
	if l0Y < 0 {
		l0H += l0Y
		l0Y = 0
	}
	if l0X+l0W > cfg.Width {
		l0W = cfg.Width - l0X
	}
	if l0Y+l0H > cfg.Height {
		l0H = cfg.Height - l0Y
	}

	img, err := slide.ReadRegionScaled(l0X, l0Y, l0W, l0H, outW, outH,
		opentile.WithFormat(decoder.PixelFormatRGB))
	if err != nil {
		return nil, fmt.Errorf("ReadRegionScaled: %w", err)
	}

	stdimg := decoderToStdImage(img)
	var out bytes.Buffer
	switch cfg.Format {
	case "jpeg":
		if err := jpeg.Encode(&out, stdimg, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	case "png":
		if err := png.Encode(&out, stdimg); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported --dzi-format %q", cfg.Format)
	}
	return out.Bytes(), nil
}

// decoderToStdImage converts a decoder.Image (RGB or RGBA) into a
// stdlib image.Image. Mirrors writeDecoderImagePNG in region.go.
func decoderToStdImage(img *decoder.Image) image.Image {
	if img.Format == decoder.PixelFormatRGBA {
		return &image.NRGBA{
			Pix:    img.Pix,
			Stride: img.Stride,
			Rect:   image.Rect(0, 0, img.Width, img.Height),
		}
	}
	rgba := image.NewRGBA(image.Rect(0, 0, img.Width, img.Height))
	for y := 0; y < img.Height; y++ {
		srcRow := img.Pix[y*img.Stride : y*img.Stride+img.Width*3]
		dstRow := rgba.Pix[y*rgba.Stride : y*rgba.Stride+img.Width*4]
		for x := 0; x < img.Width; x++ {
			dstRow[x*4+0] = srcRow[x*3+0]
			dstRow[x*4+1] = srcRow[x*3+1]
			dstRow[x*4+2] = srcRow[x*3+2]
			dstRow[x*4+3] = 0xFF
		}
	}
	return rgba
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
