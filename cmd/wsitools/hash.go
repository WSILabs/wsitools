package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/wsilabs/opentile-go/decoder"

	"github.com/wsilabs/wsitools/internal/cliout"
	"github.com/wsilabs/wsitools/internal/source"
)

var (
	hashMode string
	hashJSON *bool
)

var hashCmd = &cobra.Command{
	Use:   "hash <file>",
	Short: "Content hash (file or pixel mode) — openslide-quickhash1 analog",
	Long: `Compute a SHA-256 hash of a slide file.

--mode file (default): SHA-256 of the file bytes — equivalent to
sha256sum. Cheap and works for every format.

--mode pixel: SHA-256 of L0 tiles decoded to RGB in raster order.
Stable across re-encode at the same nominal quality. Errors cleanly if
the L0 compression isn't decodable. NOT byte-for-byte compatible with
openslide's quickhash1 algorithm.

The output prefix (sha256: vs sha256-pixel:) names the algorithm so any
future algorithm change can use a different prefix.`,
	Args: cobra.ExactArgs(1),
	RunE: runHash,
}

func init() {
	hashCmd.Flags().StringVar(&hashMode, "mode", "file", "hash mode: file|pixel")
	hashJSON = cliout.RegisterJSONFlag(hashCmd)
	rootCmd.AddCommand(hashCmd)
}

type hashResult struct {
	Algorithm string `json:"algorithm"`
	Mode      string `json:"mode"`
	Hex       string `json:"hex"`
	Path      string `json:"path"`
}

func runHash(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	path := args[0]

	switch hashMode {
	case "file":
		h, err := hashFile(path)
		if err != nil {
			return err
		}
		return emitHash(cmd, "sha256", "file", h, path)
	case "pixel":
		h, err := hashL0Pixels(path)
		if err != nil {
			return err
		}
		return emitHash(cmd, "sha256", "pixel", h, path)
	}
	return fmt.Errorf("--mode must be file or pixel, got %q", hashMode)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashL0Pixels(path string) (string, error) {
	src, err := source.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	levels := src.Levels()
	if len(levels) == 0 {
		return "", fmt.Errorf("no levels in %s", path)
	}
	l0 := levels[0]
	fac := pickDecoderForCompression(l0.Compression())
	if fac == nil {
		return "", fmt.Errorf("L0 compression %s is not decodable for pixel hash",
			l0.Compression())
	}

	tileBuf := make([]byte, l0.TileMaxSize())
	h := sha256.New()
	grid := l0.Grid()
	for ty := 0; ty < grid.Y; ty++ {
		for tx := 0; tx < grid.X; tx++ {
			n, err := l0.TileInto(tx, ty, tileBuf)
			if err != nil {
				return "", fmt.Errorf("read tile (%d,%d): %w", tx, ty, err)
			}
			dec := fac.New()
			img, err := dec.Decode(tileBuf[:n], decoder.DecodeOptions{Scale: 1, Format: decoder.PixelFormatRGB})
			dec.Close()
			if err != nil {
				return "", fmt.Errorf("decode tile (%d,%d): %w", tx, ty, err)
			}
			h.Write(img.Pix)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func pickDecoderForCompression(c source.Compression) decoder.Factory {
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

func emitHash(cmd *cobra.Command, algorithm, mode, hexStr, path string) error {
	r := hashResult{Algorithm: algorithm, Mode: mode, Hex: hexStr, Path: path}
	return cliout.Render(*hashJSON, cmd.OutOrStdout(),
		func(w io.Writer) error {
			prefix := "sha256"
			if mode == "pixel" {
				prefix = "sha256-pixel"
			}
			fmt.Fprintf(w, "%s:%s %s\n", prefix, hexStr, path)
			return nil
		}, r)
}
