package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wsilabs/opentile-go/decoder"

	"github.com/wsilabs/wsitools/internal/source"
)

var (
	extractType   string
	extractOutput string
	extractFormat string
	extractForce  bool
)

var extractCmd = &cobra.Command{
	Use:   "extract <file>",
	Short: "Save an associated image (label/macro/thumbnail/overview) as PNG or JPEG",
	Long: `Save an associated image embedded in a WSI as a standalone PNG or JPEG file.

Available associated-image types depend on the source format and the slide:
typically label, macro, thumbnail, overview. Run 'wsitools info <file>'
to list which types the slide carries.

For --format jpeg, when the source associated image is already JPEG-compressed,
the original bytes are passed through verbatim (no decode/re-encode loss).
For --format png, the image is decoded to RGB and re-encoded as PNG.`,
	Args: cobra.ExactArgs(1),
	RunE: runExtract,
}

func init() {
	extractCmd.Flags().StringVar(&extractType, "type", "", "associated-image type (label|macro|thumbnail|overview)")
	extractCmd.Flags().StringVarP(&extractOutput, "output", "o", "", "output file path")
	extractCmd.Flags().StringVar(&extractFormat, "format", "png", "output format: png|jpeg")
	extractCmd.Flags().BoolVarP(&extractForce, "force", "f", false, "overwrite output if it exists")
	_ = extractCmd.MarkFlagRequired("type")
	_ = extractCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(extractCmd)
}

func runExtract(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	path := args[0]

	if extractFormat != "png" && extractFormat != "jpeg" {
		return fmt.Errorf("--format must be png or jpeg, got %q", extractFormat)
	}
	if !extractForce {
		if _, err := os.Stat(extractOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", extractOutput)
		}
	}

	src, err := source.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	var match source.AssociatedImage
	var available []string
	for _, a := range src.Associated() {
		available = append(available, a.Type())
		if a.Type() == extractType {
			match = a
		}
	}
	if match == nil {
		return fmt.Errorf("no associated image with type %q (available: %s)",
			extractType, strings.Join(available, ", "))
	}

	bytesIn, err := match.Bytes()
	if err != nil {
		return fmt.Errorf("read associated %s: %w", extractType, err)
	}
	srcComp := match.Compression()

	// JPEG byte-pass-through path: source is JPEG and user wants JPEG.
	if extractFormat == "jpeg" && srcComp == source.CompressionJPEG {
		if err := os.WriteFile(extractOutput, bytesIn, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", extractOutput, err)
		}
		infof("wrote %s (%s)\n", extractOutput, formatBytes(int64(len(bytesIn))))
		return nil
	}

	// Decode → re-encode path (opentile-go owns all codec/predictor handling).
	di, err := match.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
	if err != nil {
		return fmt.Errorf("decode associated %s: %w", extractType, err)
	}
	img := rgbToImage(packTightRGB(di), di.Width, di.Height)

	out, err := os.Create(extractOutput)
	if err != nil {
		return fmt.Errorf("create %s: %w", extractOutput, err)
	}
	defer out.Close()
	switch extractFormat {
	case "png":
		if err := png.Encode(out, img); err != nil {
			return fmt.Errorf("encode png: %w", err)
		}
	case "jpeg":
		if err := jpeg.Encode(out, img, &jpeg.Options{Quality: 90}); err != nil {
			return fmt.Errorf("encode jpeg: %w", err)
		}
	}
	stat, _ := os.Stat(extractOutput)
	if stat != nil {
		infof("wrote %s (%s)\n", extractOutput, formatBytes(stat.Size()))
	}
	return nil
}

// packTightRGB returns a tightly-packed (Stride == Width*3) RGB byte slice
// from a decoded image. decoder.Image.Stride may over-allocate for SIMD
// alignment, so rows must be repacked before rgbToImage's (y*w+x)*3 indexing.
func packTightRGB(di *decoder.Image) []byte {
	w3 := di.Width * 3
	if di.Stride == w3 {
		return di.Pix
	}
	out := make([]byte, w3*di.Height)
	for y := 0; y < di.Height; y++ {
		copy(out[y*w3:(y+1)*w3], di.Pix[y*di.Stride:y*di.Stride+w3])
	}
	return out
}

func rgbToImage(rgb []byte, w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			si := (y*w + x) * 3
			di := y*img.Stride + x*4
			img.Pix[di+0] = rgb[si+0]
			img.Pix[di+1] = rgb[si+1]
			img.Pix[di+2] = rgb[si+2]
			img.Pix[di+3] = 0xFF
		}
	}
	return img
}
