package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/internal/cliout"
	"github.com/wsilabs/wsitools/internal/source"
)

var (
	dumpIFDsJSON    *bool
	dumpIFDsRaw     bool
	dumpIFDsRawFull bool
)

var dumpIFDsCmd = &cobra.Command{
	Use:   "dump-ifds <file>",
	Short: "Format-aware per-IFD layout dump (slim tiffinfo analog)",
	Long: `Dump every IFD in a TIFF-shaped WSI file in file order, annotated
with wsitools' format-aware classification (pyramid L0/L1/.../label/
macro/thumbnail/overview/probability/map). For each IFD: dimensions,
tile size (if tiled), compression, and SubfileType. Plus a separate
WSI-tags section listing wsitools' private tags 65080–65084 if present.

Use --raw for a full per-tag dump (tiffinfo analog): every tag with
name, type, count, value, and enum interpretation. Long arrays and
binary blobs are summarised; use --raw-full to disable that truncation.

Use --json to emit machine-readable JSON instead of human-readable text.
--json composes with --raw.`,
	Args: cobra.ExactArgs(1),
	RunE: runDumpIFDs,
}

func init() {
	dumpIFDsJSON = cliout.RegisterJSONFlag(dumpIFDsCmd)
	dumpIFDsCmd.Flags().BoolVar(&dumpIFDsRaw, "raw", false,
		"emit every TIFF tag per IFD (tiffinfo-style)")
	dumpIFDsCmd.Flags().BoolVar(&dumpIFDsRawFull, "raw-full", false,
		"disable smart truncation of long arrays/strings/blobs (requires --raw)")
	rootCmd.AddCommand(dumpIFDsCmd)
}

type dumpIFDEntry struct {
	Index           int     `json:"index"`
	ImageType       string  `json:"image_type"`  // "pyramid", "label", "macro", "thumbnail", "overview", "probability", "map", "(unclassified)"
	LevelIndex      *int    `json:"level_index,omitempty"`
	Width           uint64  `json:"width"`
	Height          uint64  `json:"height"`
	TileWidth       uint64  `json:"tile_width"`
	TileHeight      uint64  `json:"tile_height"`
	Compression     uint64  `json:"compression_tag"`
	CompressionName string  `json:"compression"`
	SubfileType     uint64  `json:"subfile_type"`
	IsSubIFD        bool    `json:"is_subifd,omitempty"`
	WSITags         *wsiTag `json:"wsi_tags,omitempty"`
}

type wsiTag struct {
	WSIImageType    string  `json:"WSIImageType,omitempty"`
	WSILevelIndex   *uint64 `json:"WSILevelIndex,omitempty"`
	WSILevelCount   *uint64 `json:"WSILevelCount,omitempty"`
	WSISourceFormat string  `json:"WSISourceFormat,omitempty"`
	WSIToolsVersion string  `json:"WSIToolsVersion,omitempty"`
}

type dumpIFDsResult struct {
	Path   string         `json:"path"`
	Format string         `json:"format"`
	IFDs   []dumpIFDEntry `json:"ifds"`
}

func runDumpIFDs(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	path := args[0]

	// dump-ifds reads raw TIFF IFDs; non-TIFF-dialect sources (DICOM, IFE) have none.
	if s, err := source.Open(path); err == nil {
		f := s.Format()
		s.Close()
		if f == "dicom" || f == "ife" {
			return fmt.Errorf("dump-ifds requires a TIFF-dialect source; %s is %s (no TIFF IFDs to dump)", path, f)
		}
	}

	if dumpIFDsRaw {
		return runDumpIFDsRaw(cmd, path)
	}

	ifds, err := source.WalkIFDs(path)
	if err != nil {
		return fmt.Errorf("walk IFDs: %w", err)
	}

	src, err := source.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	classifier := buildIFDClassifier(src)

	result := dumpIFDsResult{
		Path:   path,
		Format: src.Format(),
	}
	for _, ifd := range ifds {
		entry := dumpIFDEntry{
			Index:           ifd.Index,
			Width:           ifd.Width,
			Height:          ifd.Height,
			TileWidth:       ifd.TileWidth,
			TileHeight:      ifd.TileHeight,
			Compression:     ifd.Compression,
			CompressionName: tiffCompressionName(ifd.Compression),
			SubfileType:     ifd.NewSubfileType,
			IsSubIFD:        ifd.IsSubIFD,
		}
		entry.ImageType, entry.LevelIndex = classifier(ifd)
		if ifd.HasWSITags() {
			entry.WSITags = &wsiTag{
				WSIImageType:    ifd.WSIImageType,
				WSILevelIndex:   ifd.WSILevelIndex,
				WSILevelCount:   ifd.WSILevelCount,
				WSISourceFormat: ifd.WSISourceFormat,
				WSIToolsVersion: ifd.WSIToolsVersion,
			}
		}
		result.IFDs = append(result.IFDs, entry)
	}

	return cliout.Render(*dumpIFDsJSON, cmd.OutOrStdout(),
		func(w io.Writer) error { return renderDumpIFDsText(w, &result) },
		result)
}

func runDumpIFDsRaw(cmd *cobra.Command, path string) error {
	ifds, err := source.WalkIFDsRaw(path)
	if err != nil {
		return fmt.Errorf("walk IFDs (raw): %w", err)
	}
	if *dumpIFDsJSON {
		return renderRawJSON(cmd.OutOrStdout(), ifds, dumpIFDsRawFull)
	}
	return renderRawText(cmd.OutOrStdout(), ifds, dumpIFDsRawFull)
}

// buildIFDClassifier returns a function that, given an IFDRecord, returns
// (imageType, levelIndex). It crossrefs against source.Source's Levels() and
// Associated() by matching (width, height, compression-string) tuples.
func buildIFDClassifier(src source.Source) func(source.IFDRecord) (string, *int) {
	type key struct {
		w, h uint64
		comp string
	}
	type val struct {
		imageType string
		level     *int
	}
	m := map[key]val{}
	for _, lvl := range src.Levels() {
		k := key{
			w:    uint64(lvl.Size().X),
			h:    uint64(lvl.Size().Y),
			comp: lvl.Compression().String(),
		}
		idx := lvl.Index()
		m[k] = val{imageType: "pyramid", level: &idx}
	}
	for _, a := range src.Associated() {
		k := key{
			w:    uint64(a.Size().X),
			h:    uint64(a.Size().Y),
			comp: a.Compression().String(),
		}
		// Don't overwrite a level mapping if dimensions+comp collide
		// (extremely rare, but be safe).
		if _, ok := m[k]; !ok {
			m[k] = val{imageType: a.Type()}
		}
	}
	return func(ifd source.IFDRecord) (string, *int) {
		comp := tiffCompressionName(ifd.Compression)
		k := key{w: ifd.Width, h: ifd.Height, comp: comp}
		if v, ok := m[k]; ok {
			return v.imageType, v.level
		}
		return "(unclassified)", nil
	}
}

// tiffCompressionName maps the TIFF compression tag (259) value to a
// human-readable name matching opentile-go's Compression.String() output
// for the codes opentile-go knows about; falls back to "tag-N" otherwise.
func tiffCompressionName(tag uint64) string {
	switch tag {
	case 1:
		return "none"
	case 5:
		return "lzw"
	case 7:
		return "jpeg"
	case 8, 32946:
		return "deflate"
	case 33003, 33005, 34712:
		return "jpeg2000"
	case 50001:
		return "webp"
	case 50002:
		return "jpegxl"
	case 60001:
		return "avif"
	case 60003:
		return "htj2k"
	}
	return fmt.Sprintf("tag-%d", tag)
}

func renderDumpIFDsText(w io.Writer, r *dumpIFDsResult) error {
	for _, ifd := range r.IFDs {
		label := ifd.ImageType
		if ifd.LevelIndex != nil {
			label = fmt.Sprintf("pyramid L%d", *ifd.LevelIndex)
		}
		tile := ""
		if ifd.TileWidth > 0 && ifd.TileHeight > 0 {
			tile = fmt.Sprintf("  tile %d×%d", ifd.TileWidth, ifd.TileHeight)
		}
		fmt.Fprintf(w, "IFD %d  %-12s  %d × %d%s   %s   SubfileType=%d\n",
			ifd.Index, label, ifd.Width, ifd.Height, tile,
			ifd.CompressionName, ifd.SubfileType)
	}

	// WSI tags section: collate all WSI-tag-bearing IFDs.
	var hasWSITags bool
	for _, ifd := range r.IFDs {
		if ifd.WSITags != nil {
			hasWSITags = true
			break
		}
	}
	if hasWSITags {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "WSI tags (private 65080–65084):")
		for _, ifd := range r.IFDs {
			if ifd.WSITags == nil {
				continue
			}
			t := ifd.WSITags
			fmt.Fprintf(w, "  IFD %d:", ifd.Index)
			if t.WSIImageType != "" {
				fmt.Fprintf(w, " WSIImageType=%s", t.WSIImageType)
			}
			if t.WSILevelIndex != nil {
				fmt.Fprintf(w, " WSILevelIndex=%d", *t.WSILevelIndex)
			}
			if t.WSILevelCount != nil {
				fmt.Fprintf(w, " WSILevelCount=%d", *t.WSILevelCount)
			}
			if t.WSISourceFormat != "" {
				fmt.Fprintf(w, " WSISourceFormat=%s", t.WSISourceFormat)
			}
			if t.WSIToolsVersion != "" {
				fmt.Fprintf(w, " WSIToolsVersion=%s", t.WSIToolsVersion)
			}
			fmt.Fprintln(w)
		}
	}
	return nil
}
