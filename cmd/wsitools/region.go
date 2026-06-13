package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
)

// Package-level flag globals (cobra binds Var/IntVar/StringVar into
// these). Reset at test entry where mutation matters.
var (
	regionLevel  int
	regionRect   string
	regionX      int
	regionY      int
	regionW      int
	regionH      int
	regionImage  int
	regionFormat string
	regionOutput string
	regionForce  bool
)

// parseRect splits "X,Y,W,H" into four integers. Whitespace around
// commas is allowed. Returns an error if the format or value
// constraints don't match.
func parseRect(s string) (x, y, w, h int, err error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("--rect: expected X,Y,W,H (4 comma-separated integers), got %q", s)
	}
	vals := make([]int, 4)
	for i, p := range parts {
		v, e := strconv.Atoi(strings.TrimSpace(p))
		if e != nil {
			return 0, 0, 0, 0, fmt.Errorf("--rect: not an integer: %q", strings.TrimSpace(p))
		}
		vals[i] = v
	}
	if vals[2] <= 0 || vals[3] <= 0 {
		return 0, 0, 0, 0, fmt.Errorf("--rect: W and H must be positive (got W=%d H=%d)", vals[2], vals[3])
	}
	return vals[0], vals[1], vals[2], vals[3], nil
}

// resolveRect figures out which form the user used (--rect vs
// --x/--y/--w/--h) and returns the resolved rectangle.
func resolveRect(cmd *cobra.Command) (x, y, w, h int, err error) {
	rectSet := cmd.Flags().Changed("rect")
	xSet := cmd.Flags().Changed("x")
	ySet := cmd.Flags().Changed("y")
	wSet := cmd.Flags().Changed("w")
	hSet := cmd.Flags().Changed("h")
	anyIndividual := xSet || ySet || wSet || hSet

	if rectSet && anyIndividual {
		return 0, 0, 0, 0, fmt.Errorf("--rect and --x/--y/--w/--h are mutually exclusive; use one form or the other")
	}
	if rectSet {
		return parseRect(regionRect)
	}
	if !anyIndividual {
		return 0, 0, 0, 0, fmt.Errorf("must specify either --rect or all of --x/--y/--w/--h")
	}
	missing := []string{}
	if !xSet {
		missing = append(missing, "--x")
	}
	if !ySet {
		missing = append(missing, "--y")
	}
	if !wSet {
		missing = append(missing, "--w")
	}
	if !hSet {
		missing = append(missing, "--h")
	}
	if len(missing) > 0 {
		return 0, 0, 0, 0, fmt.Errorf("must specify all of --x/--y/--w/--h (missing: %s)", strings.Join(missing, " "))
	}
	if regionW <= 0 || regionH <= 0 {
		return 0, 0, 0, 0, fmt.Errorf("--w and --h must be positive (got W=%d H=%d)", regionW, regionH)
	}
	return regionX, regionY, regionW, regionH, nil
}

var regionCmd = &cobra.Command{
	Use:   "region [flags] <slide>",
	Short: "Extract a rectangular pixel region from a slide as PNG",
	Long: `region extracts a rectangle of decoded pixels at a chosen
pyramid level and writes it as a PNG file.

Out-of-bounds regions are auto-clipped against the slide's level
dimensions; pixels outside the slide are white-filled.`,
	Args: cobra.ExactArgs(1),
	RunE: runRegion,
}

func init() {
	regionCmd.Flags().IntVar(&regionLevel, "level", 0, "Pyramid level index")
	regionCmd.Flags().StringVar(&regionRect, "rect", "", "Region rectangle as X,Y,W,H (level coords)")
	regionCmd.Flags().IntVar(&regionX, "x", 0, "Region X (level coords)")
	regionCmd.Flags().IntVar(&regionY, "y", 0, "Region Y (level coords)")
	regionCmd.Flags().IntVar(&regionW, "w", 0, "Region width (level pixels)")
	regionCmd.Flags().IntVar(&regionH, "h", 0, "Region height (level pixels)")
	regionCmd.Flags().IntVar(&regionImage, "image", 0, "Image index (multi-image OME-TIFF)")
	regionCmd.Flags().StringVar(&regionFormat, "format", "rgb", "Output pixel format: rgb|rgba")
	regionCmd.Flags().StringVarP(&regionOutput, "output", "o", "", "Output PNG path")
	regionCmd.Flags().BoolVarP(&regionForce, "force", "f", false, "Overwrite existing output file")
	_ = regionCmd.MarkFlagRequired("level")
	_ = regionCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(regionCmd)
}

func runRegion(cmd *cobra.Command, args []string) error {
	slidePath := args[0]

	// Early-fail validation (cheap; no file I/O).
	if !strings.HasSuffix(strings.ToLower(regionOutput), ".png") {
		return fmt.Errorf("--output: only PNG output is supported in v0.13 (got %q)", regionOutput)
	}
	if _, err := os.Stat(regionOutput); err == nil && !regionForce {
		return fmt.Errorf("output exists; pass --force to overwrite (%q)", regionOutput)
	}

	x, y, w, h, err := resolveRect(cmd)
	if err != nil {
		return err
	}

	// Open slide.
	slide, err := opentile.OpenFile(slidePath)
	if err != nil {
		return fmt.Errorf("opening slide %q: %w", slidePath, err)
	}
	defer slide.Close()

	// Validate --image and --level against opened slide.
	images := slide.Pyramids()
	if regionImage < 0 || regionImage >= len(images) {
		return fmt.Errorf("--image %d out of range [0, %d)", regionImage, len(images))
	}
	levels := images[regionImage].Levels
	if regionLevel < 0 || regionLevel >= len(levels) {
		return fmt.Errorf("--level %d out of range [0, %d)", regionLevel, len(levels))
	}

	// Decode options.
	var opts []opentile.DecodeOption
	switch regionFormat {
	case "rgb":
		opts = append(opts, opentile.WithFormat(decoder.PixelFormatRGB))
	case "rgba":
		opts = append(opts, opentile.WithFormat(decoder.PixelFormatRGBA))
	default:
		return fmt.Errorf("--format: expected \"rgb\" or \"rgba\", got %q", regionFormat)
	}

	// Read the region.
	lv, err := slide.Pyramid(regionImage).Level(regionLevel)
	if err != nil {
		return fmt.Errorf("level (%d,%d): %w", regionImage, regionLevel, err)
	}
	img, err := lv.ReadRegion(opentile.Region{Origin: opentile.Point{X: x, Y: y}, Size: opentile.Size{W: w, H: h}}, opts...)
	if err != nil {
		return fmt.Errorf("reading region: %w", err)
	}

	// Encode + write.
	if err := writeDecoderImagePNG(img, regionOutput); err != nil {
		return fmt.Errorf("writing PNG: %w", err)
	}

	fmt.Fprintf(os.Stderr, "wrote %s (%dx%d, %s)\n", regionOutput, img.Width, img.Height, regionFormat)
	return nil
}

// writeDecoderImagePNG converts a *decoder.Image to a stdlib image
// type and writes it as PNG at path.
func writeDecoderImagePNG(img *decoder.Image, path string) error {
	var stdimg image.Image
	if img.Format == decoder.PixelFormatRGBA {
		// Zero-copy: NRGBA's pixel layout matches decoder.PixelFormatRGBA
		// (R, G, B, A bytes per pixel).
		stdimg = &image.NRGBA{
			Pix:    img.Pix,
			Stride: img.Stride,
			Rect:   image.Rect(0, 0, img.Width, img.Height),
		}
	} else {
		// RGB → image.RGBA (which is 4 bytes/pixel; alpha=0xFF synthesized).
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
		stdimg = rgba
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, stdimg)
}
