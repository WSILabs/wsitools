package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

var (
	cvOutput       string
	cvTo           string
	cvForce        bool
	cvBigTIFFFlag  string
	cvNoAssociated bool
	cvTileOrder    string
)

var convertCmd = &cobra.Command{
	Use:   "convert --to <target> -o <output> [flags] <input>",
	Short: "Convert a WSI to a new container losslessly (tile-copy)",
	Long: `Convert losslessly copies compressed tile bytes from a source WSI
into a new container without decoding or re-encoding. In v0.6 the only
supported target is COG-WSI (--to cog-wsi).

COG-WSI is an extension of Cloud Optimized GeoTIFF for whole-slide images:
header-front IFDs, reverse-order tile data (lowest-resolution overview
first), and an associated-image (label/macro/thumbnail) tail section.

Examples:

  wsitools convert --to cog-wsi -o slide.cog.tiff slide.svs
  wsitools convert --to cog-wsi --no-associated -o slide.cog.tiff slide.tiff`,
	Args: cobra.ExactArgs(1),
	RunE: runConvert,
}

func init() {
	convertCmd.Flags().StringVarP(&cvOutput, "output", "o", "", "output file path (required)")
	convertCmd.Flags().StringVar(&cvTo, "to", "", "conversion target (only 'cog-wsi' in v0.6)")
	convertCmd.Flags().BoolVarP(&cvForce, "force", "f", false, "overwrite output if it exists")
	convertCmd.Flags().StringVar(&cvBigTIFFFlag, "bigtiff", "auto", "auto|on|off")
	convertCmd.Flags().BoolVar(&cvNoAssociated, "no-associated", false, "skip label/macro/thumbnail/overview")
	convertCmd.Flags().StringVar(&cvTileOrder, "tile-order", "row-major",
		"Tile emission order within each level (row-major|hilbert|morton). "+
			"Format-restricted: SVS accepts row-major only; COG-WSI / TIFF / OME-TIFF "+
			"accept all three.")
	_ = convertCmd.MarkFlagRequired("output")
	_ = convertCmd.MarkFlagRequired("to")
	rootCmd.AddCommand(convertCmd)
}

func runConvert(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	input := args[0]
	start := time.Now()

	if cvTo != "cog-wsi" {
		return fmt.Errorf("--to %q: only 'cog-wsi' is supported in v0.6", cvTo)
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", cvOutput)
		}
	}
	bigTIFFMode, err := parseBigTIFFFlag(cvBigTIFFFlag)
	if err != nil {
		return err
	}

	order, err := tileorder.ByName(cvTileOrder)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	src, err := source.Open(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	for _, lvl := range src.Levels() {
		if compressionTagFor(lvl.Compression()) == 0 {
			return fmt.Errorf("level %d: source compression %s has no standard TIFF Compression tag; cannot tile-copy",
				lvl.Index(), lvl.Compression())
		}
	}
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}

	md := src.Metadata()
	opts := cogwsiwriter.Options{
		BigTIFF:      bigTIFFMode,
		ToolsVersion: Version,
		DefaultOrder: order,
		Metadata: cogwsiwriter.Metadata{
			MPPX:                md.MPP,
			MPPY:                md.MPP, // MPP is currently single-axis in source.Metadata
			Magnification:       md.Magnification,
			Make:                md.Make,
			Model:               md.Model,
			Software:            md.Software,
			AcquisitionDateTime: md.AcquisitionDateTime,
			SourceFormat:        src.Format(),
			SourceImageDesc:     fmt.Sprintf("wsitools/%s convert source=%s", Version, src.Format()),
		},
	}

	w, err := cogwsiwriter.Create(cvOutput, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}

	// Tile-copy: full-resolution first (source order).
	for _, lvl := range src.Levels() {
		spec := cogwsiwriter.LevelSpec{
			ImageWidth:      uint32(lvl.Size().X),
			ImageHeight:     uint32(lvl.Size().Y),
			TileWidth:       uint32(lvl.TileSize().X),
			TileHeight:      uint32(lvl.TileSize().Y),
			Compression:     compressionTagFor(lvl.Compression()),
			Photometric:     2, // RGB; lossless copy preserves source codec's color model
			SamplesPerPixel: 3,
			BitsPerSample:   []uint16{8, 8, 8},
			IsL0:            lvl.Index() == 0,
		}
		h, err := w.AddLevel(spec)
		if err != nil {
			w.Abort()
			return fmt.Errorf("add level %d: %w", lvl.Index(), err)
		}
		buf := make([]byte, lvl.TileMaxSize())
		grid := lvl.Grid()
		for ty := 0; ty < grid.Y; ty++ {
			for tx := 0; tx < grid.X; tx++ {
				n, err := lvl.TileInto(tx, ty, buf)
				if err != nil {
					w.Abort()
					return fmt.Errorf("read tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
				if err := h.WriteTile(uint32(tx), uint32(ty), buf[:n]); err != nil {
					w.Abort()
					return fmt.Errorf("write tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
			}
		}
	}

	if !cvNoAssociated {
		for _, a := range src.Associated() {
			bs, err := a.Bytes()
			if err != nil {
				w.Abort()
				return fmt.Errorf("read associated %s: %w", a.Kind(), err)
			}
			if err := w.AddAssociated(cogwsiwriter.AssociatedSpec{
				Kind:        a.Kind(),
				Width:       uint32(a.Size().X),
				Height:      uint32(a.Size().Y),
				Compression: compressionTagFor(a.Compression()),
				Photometric: 2,
				Bytes:       bs,
			}); err != nil {
				if errors.Is(err, cogwsiwriter.ErrInvalidAssocKind) {
					slog.Warn("skipping associated image with unsupported kind",
						"kind", a.Kind(), "reason", err)
					continue
				}
				w.Abort()
				return fmt.Errorf("add associated %s: %w", a.Kind(), err)
			}
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}

	if stat, _ := os.Stat(cvOutput); stat != nil {
		slog.Info("convert complete",
			"output", cvOutput,
			"size", formatBytes(stat.Size()),
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		fmt.Printf("wrote %s (%s, %s)\n", cvOutput, formatBytes(stat.Size()), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

func parseBigTIFFFlag(v string) (cogwsiwriter.BigTIFFMode, error) {
	switch v {
	case "auto":
		return cogwsiwriter.BigTIFFAuto, nil
	case "on":
		return cogwsiwriter.BigTIFFOn, nil
	case "off":
		return cogwsiwriter.BigTIFFOff, nil
	}
	return 0, fmt.Errorf("--bigtiff %q: want auto|on|off", v)
}

// compressionTagFor maps source.Compression to a TIFF Compression tag value.
func compressionTagFor(c source.Compression) uint16 {
	switch c {
	case source.CompressionJPEG:
		return 7
	case source.CompressionJPEG2000:
		return 33003 // Aperio / OpenJPEG codestream
	case source.CompressionLZW:
		return 5
	case source.CompressionDeflate:
		return 8
	case source.CompressionNone:
		return 1
	}
	// Other codecs (AVIF, WebP, JPEGXL, HTJ2K, Iris): no standardized TIFF tag.
	// Return 0; preflight (Task 10) will surface this as a clean error.
	return 0
}
