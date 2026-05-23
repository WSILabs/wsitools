package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/internal/cliout"
	"github.com/wsilabs/wsitools/internal/source"
)

var infoJSON *bool

var infoCmd = &cobra.Command{
	Use:   "info <file>",
	Short: "Print slide summary (format, levels, metadata, associated images)",
	Long: `Print a summary of a whole-slide image: file size, format,
scanner metadata (make/model/software/datetime/MPP/magnification),
pyramid levels (dimensions + tile size + compression per level), and
associated images (label/macro/thumbnail/overview).

Use --json to emit machine-readable JSON instead of human-readable text.`,
	Args: cobra.ExactArgs(1),
	RunE: runInfo,
}

func init() {
	infoJSON = cliout.RegisterJSONFlag(infoCmd)
	rootCmd.AddCommand(infoCmd)
}

type infoLevel struct {
	Index       int    `json:"index"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	TileWidth   int    `json:"tile_width"`
	TileHeight  int    `json:"tile_height"`
	Compression string `json:"compression"`
}

type infoAssoc struct {
	Kind        string `json:"kind"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Compression string `json:"compression"`
}

type infoMetadata struct {
	Make          string  `json:"make"`
	Model         string  `json:"model"`
	Software      string  `json:"software"`
	DateTime      string  `json:"datetime"`
	MPP           float64 `json:"mpp"`
	Magnification float64 `json:"magnification"`
}

type infoResult struct {
	Path       string       `json:"path"`
	SizeBytes  int64        `json:"size_bytes"`
	Format     string       `json:"format"`
	Metadata   infoMetadata `json:"metadata"`
	Levels     []infoLevel  `json:"levels"`
	Associated []infoAssoc  `json:"associated_images"`
}

func runInfo(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	path := args[0]

	stat, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	src, err := source.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	md := src.Metadata()
	result := infoResult{
		Path:      path,
		SizeBytes: stat.Size(),
		Format:    src.Format(),
		Metadata: infoMetadata{
			Make:          md.Make,
			Model:         md.Model,
			Software:      md.Software,
			MPP:           md.MPP,
			Magnification: md.Magnification,
		},
	}
	if !md.AcquisitionDateTime.IsZero() {
		result.Metadata.DateTime = md.AcquisitionDateTime.Format(time.RFC3339)
	}
	for _, lvl := range src.Levels() {
		result.Levels = append(result.Levels, infoLevel{
			Index:       lvl.Index(),
			Width:       lvl.Size().X,
			Height:      lvl.Size().Y,
			TileWidth:   lvl.TileSize().X,
			TileHeight:  lvl.TileSize().Y,
			Compression: lvl.Compression().String(),
		})
	}
	for _, a := range src.Associated() {
		result.Associated = append(result.Associated, infoAssoc{
			Kind:        a.Kind(),
			Width:       a.Size().X,
			Height:      a.Size().Y,
			Compression: a.Compression().String(),
		})
	}

	return cliout.Render(*infoJSON, cmd.OutOrStdout(),
		func(w io.Writer) error { return renderInfoText(w, &result) },
		result)
}

func renderInfoText(w io.Writer, r *infoResult) error {
	fmt.Fprintf(w, "File:    %s (%s)\n", r.Path, formatBytes(r.SizeBytes))
	fmt.Fprintf(w, "Format:  %s\n", r.Format)
	if r.Metadata.Make != "" {
		fmt.Fprintf(w, "Make:    %s\n", r.Metadata.Make)
	}
	if r.Metadata.Model != "" {
		fmt.Fprintf(w, "Model:   %s\n", r.Metadata.Model)
	}
	if r.Metadata.Software != "" {
		fmt.Fprintf(w, "Software: %s\n", r.Metadata.Software)
	}
	if r.Metadata.DateTime != "" {
		fmt.Fprintf(w, "DateTime: %s\n", r.Metadata.DateTime)
	}
	// MPP/Magnification == 0 means "unknown/unset" per source.Metadata; omit
	// from human text. JSON always serializes the raw value for scripting.
	if r.Metadata.MPP > 0 {
		fmt.Fprintf(w, "MPP:     %g\n", r.Metadata.MPP)
	}
	if r.Metadata.Magnification > 0 {
		fmt.Fprintf(w, "Magnification: %gx\n", r.Metadata.Magnification)
	}

	if len(r.Levels) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Levels:")
		for _, lvl := range r.Levels {
			fmt.Fprintf(w, "  L%d  %d × %d   tile %d×%d   %s\n",
				lvl.Index, lvl.Width, lvl.Height,
				lvl.TileWidth, lvl.TileHeight, lvl.Compression)
		}
	}
	if len(r.Associated) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Associated images:")
		for _, a := range r.Associated {
			fmt.Fprintf(w, "  %-10s %d × %d    %s\n",
				a.Kind, a.Width, a.Height, a.Compression)
		}
	}
	return nil
}
