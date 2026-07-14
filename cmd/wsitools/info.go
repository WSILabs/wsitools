package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
	_ "github.com/wsilabs/wsitools/cmd/wsitools/quality/all"
	"github.com/wsilabs/wsitools/internal/cliout"
	"github.com/wsilabs/wsitools/internal/source"

	opentile "github.com/wsilabs/opentile-go"
	otdecoder "github.com/wsilabs/opentile-go/decoder"
)

var (
	infoJSON       *bool
	infoProperties bool
)

var infoCmd = &cobra.Command{
	Use:   "info <file>",
	Short: "Print slide summary (format, levels, metadata, associated images)",
	Long: `Print a summary of a whole-slide image: file size, format,
scanner metadata (make/model/serial/software/writer/datetime/MPP/
magnification/ICC-profile presence),
pyramid levels (dimensions + tile size + compression + a per-level quality
summary, incl. the effective/decoded colorspace), and
associated images (label/macro/thumbnail/overview).

Use --json to emit machine-readable JSON instead of human-readable text.
Use --properties to list the reader's full vendor/provenance property bag
(aperio.*, wsi-tools.*, …) in text mode; --json always includes it.`,
	Args: cobra.ExactArgs(1),
	RunE: runInfo,
}

func init() {
	infoJSON = cliout.RegisterJSONFlag(infoCmd)
	infoCmd.Flags().BoolVar(&infoProperties, "properties", false,
		"list all vendor/provenance properties in text output (--json always includes them)")
	rootCmd.AddCommand(infoCmd)
}

type infoLevel struct {
	Index       int           `json:"index"`
	Width       int           `json:"width"`
	Height      int           `json:"height"`
	TileWidth   int           `json:"tile_width"`
	TileHeight  int           `json:"tile_height"`
	Compression string        `json:"compression"`
	Quality     *quality.Info `json:"quality,omitempty"`
}

type infoAssoc struct {
	Type        string `json:"type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Compression string `json:"compression"`
}

type infoMetadata struct {
	Make            string            `json:"make"`
	Model           string            `json:"model"`
	SerialNumber    string            `json:"serial_number,omitempty"`
	Software        string            `json:"software"`
	Writer          string            `json:"writer,omitempty"`
	DateTime        string            `json:"datetime"`
	MPP             float64           `json:"mpp"`
	MPPX            float64           `json:"mpp_x"`
	MPPY            float64           `json:"mpp_y"`
	Magnification   float64           `json:"magnification"`
	ICCProfileBytes int               `json:"icc_profile_bytes,omitempty"`
	Properties      map[string]string `json:"properties,omitempty"`
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
			Make:            md.Make,
			Model:           md.Model,
			SerialNumber:    md.SerialNumber,
			Software:        md.Software,
			Writer:          md.Writer,
			MPP:             md.MPP,
			MPPX:            md.MPPX,
			MPPY:            md.MPPY,
			Magnification:   md.Magnification,
			ICCProfileBytes: len(md.ICCProfile),
			Properties:      md.Properties,
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
			Quality:     inspectLevel(lvl),
		})
	}
	for _, a := range src.Associated() {
		result.Associated = append(result.Associated, infoAssoc{
			Type:        a.Type(),
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
	if r.Metadata.SerialNumber != "" {
		fmt.Fprintf(w, "Serial:  %s\n", r.Metadata.SerialNumber)
	}
	if r.Metadata.Software != "" {
		fmt.Fprintf(w, "Software: %s\n", r.Metadata.Software)
	}
	// Writer (who wrote this file) is shown only when it differs from Software
	// (the scanner/acquisition software) — i.e. for transcoded/wsitools output,
	// where Software is preserved from the source but the file was rewritten.
	if r.Metadata.Writer != "" && r.Metadata.Writer != r.Metadata.Software {
		fmt.Fprintf(w, "Writer:  %s\n", r.Metadata.Writer)
	}
	if r.Metadata.DateTime != "" {
		fmt.Fprintf(w, "DateTime: %s\n", r.Metadata.DateTime)
	}
	// MPP/Magnification == 0 means "unknown/unset" per source.Metadata; omit
	// from human text. JSON always serializes the raw value for scripting.
	if r.Metadata.MPPX > 0 && r.Metadata.MPPX == r.Metadata.MPPY {
		fmt.Fprintf(w, "MPP:     %g\n", r.Metadata.MPPX)
	} else if r.Metadata.MPPX > 0 || r.Metadata.MPPY > 0 {
		fmt.Fprintf(w, "MPP:     %g × %g (x,y)\n", r.Metadata.MPPX, r.Metadata.MPPY)
	}
	if r.Metadata.Magnification > 0 {
		fmt.Fprintf(w, "Magnification: %gx\n", r.Metadata.Magnification)
	}
	if r.Metadata.ICCProfileBytes > 0 {
		fmt.Fprintf(w, "ICC:     present (%s)\n", formatBytes(int64(r.Metadata.ICCProfileBytes)))
	}
	// Properties: a count by default (keeps the summary compact), the full sorted
	// list under --properties. --json always carries the map.
	if n := len(r.Metadata.Properties); n > 0 {
		if infoProperties {
			fmt.Fprintf(w, "Properties (%d):\n", n)
			keys := make([]string, 0, n)
			for k := range r.Metadata.Properties {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(w, "  %s = %s\n", k, r.Metadata.Properties[k])
			}
		} else {
			fmt.Fprintf(w, "Properties: %d (use --properties to list)\n", n)
		}
	}

	if len(r.Levels) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Levels:")
		for _, lvl := range r.Levels {
			fmt.Fprintf(w, "  L%d  %d × %d   tile %d×%d   %s",
				lvl.Index, lvl.Width, lvl.Height,
				lvl.TileWidth, lvl.TileHeight, lvl.Compression)
			if lvl.Quality != nil {
				fmt.Fprintf(w, "  %s", formatQuality(lvl.Quality))
			}
			fmt.Fprintln(w)
		}
	}
	if len(r.Associated) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Associated images:")
		for _, a := range r.Associated {
			fmt.Fprintf(w, "  %-10s %d × %d    %s\n",
				a.Type, a.Width, a.Height, a.Compression)
		}
	}
	return nil
}

// sourceToOpentileCompression maps source.Compression to the equivalent
// opentile.Compression used as the registry key in the quality package.
func sourceToOpentileCompression(c source.Compression) opentile.Compression {
	switch c {
	case source.CompressionJPEG:
		return opentile.CompressionJPEG
	case source.CompressionJPEG2000:
		return opentile.CompressionJP2K
	case source.CompressionLZW:
		return opentile.CompressionLZW
	case source.CompressionDeflate:
		return opentile.CompressionDeflate
	case source.CompressionNone:
		return opentile.CompressionNone
	case source.CompressionAVIF:
		return opentile.CompressionAVIF
	case source.CompressionWebP:
		return opentile.CompressionWebP
	case source.CompressionJPEGXL:
		return opentile.CompressionJPEGXL
	case source.CompressionHTJ2K:
		return opentile.CompressionHTJ2K
	}
	return opentile.CompressionUnknown
}

// inspectLevel reads a representative tile from the given level and
// runs the registered quality inspector for the level's codec.
// Returns a fallback Info (codec name + lossless flag) if no inspector
// is registered, or nil if the tile read or inspection fails in an
// unexpected way.
func inspectLevel(lvl source.Level) *quality.Info {
	oc := sourceToOpentileCompression(lvl.Compression())
	insp, ok := quality.For(oc)
	if !ok {
		// Fallback: codec name + lossless flag for known-lossless codecs.
		fallback := quality.Info{
			Codec:    lvl.Compression().String(),
			Lossless: isLosslessCompression(lvl.Compression()),
		}
		return &fallback
	}
	maxSize := lvl.TileMaxSize()
	if maxSize <= 0 {
		return nil
	}
	buf := make([]byte, maxSize)
	n, err := lvl.TileInto(0, 0, buf)
	if err != nil || n == 0 {
		return nil
	}
	info, err := insp.Inspect(buf[:n])
	if err != nil {
		return nil
	}
	enrichFromCodestream(&info, oc, buf[:n])
	return &info
}

// decoderNameFor maps a codec to the opentile decoder-registry name for the
// codecs that expose a CodestreamInspector (jpeg, jpeg2000, htj2k, jpegxl).
// Returns "" for codecs with no header-only colorspace signal.
func decoderNameFor(oc opentile.Compression) string {
	switch oc {
	case opentile.CompressionJPEG:
		return "jpeg"
	case opentile.CompressionJP2K:
		return "jpeg2000"
	case opentile.CompressionHTJ2K:
		return "htj2k"
	case opentile.CompressionJPEGXL:
		return "jpegxl"
	}
	return ""
}

// enrichFromCodestream inspects the tile's codestream header (for codecs that
// expose one — jpeg / jpeg2000 / htj2k / jpegxl) and fills in header-only facts
// about what the tile actually contains: the effective (decoded) colorspace, the
// bit depth, and — only when the per-codec inspector didn't already set it —
// the chroma subsampling. The effective colorspace reports "RGB" for an MCT
// (ICT/RCT) JPEG 2000 codestream (the transform is inverted on decode), via the
// shared effectiveColorspace helper that validate's #44 check uses. No-op when
// the codec has no CodestreamInspector or the header can't be parsed.
func enrichFromCodestream(info *quality.Info, oc opentile.Compression, tileBytes []byte) {
	name := decoderNameFor(oc)
	if name == "" {
		return
	}
	fac, ok := otdecoder.Get(name)
	if !ok {
		return
	}
	insp, ok := fac.(otdecoder.CodestreamInspector)
	if !ok {
		return
	}
	ci, err := insp.Inspect(tileBytes)
	if err != nil {
		return
	}
	info.Colorspace = effectiveColorspace(ci.ColorEncoding)
	if ci.BitDepth > 0 {
		info.BitDepth = ci.BitDepth
	}
	// The JPEG inspector derives chroma from the SOF/DQT path already; only
	// gap-fill for the codestream codecs (JP2K / HTJ2K / JXL) whose per-codec
	// inspectors don't, reading it from the SIZ.
	if info.ChromaSubsampling == "" {
		info.ChromaSubsampling = chromaString(ci.ChromaSubsampling)
	}
}

// chromaString renders a codec-domain chroma subsampling as the conventional
// J:a:b notation, or "" for grayscale (no chroma) / unknown — so info shows a
// subsampling ratio only when there's a meaningful one.
func chromaString(cs otdecoder.ChromaSubsampling) string {
	switch cs {
	case otdecoder.Subsampling444, otdecoder.Subsampling422, otdecoder.Subsampling420,
		otdecoder.Subsampling440, otdecoder.Subsampling411:
		return cs.String()
	}
	return ""
}

func isLosslessCompression(c source.Compression) bool {
	switch c {
	case source.CompressionNone, source.CompressionLZW, source.CompressionDeflate:
		return true
	}
	return false
}

func formatQuality(q *quality.Info) string {
	var body string
	switch {
	case q.Lossless && q.LayerCount > 0:
		body = fmt.Sprintf("lossless, %d layers", q.LayerCount)
	case q.Lossless:
		body = "lossless"
	case q.LayerCount > 0:
		body = fmt.Sprintf("lossy, %d layers", q.LayerCount)
	case q.QualityEstimate > 0:
		body = fmt.Sprintf("Q≈%d", q.QualityEstimate)
	default:
		body = q.Codec
	}
	// Group the header-only codestream facts (effective colorspace, bit depth,
	// chroma subsampling) as a prefix, separated from the quality/layer body by
	// a double space: e.g. "RGB 8-bit 4:2:0  Q≈93" or "RGB 8-bit  lossy, 1 layers".
	var facts []string
	if q.Colorspace != "" {
		facts = append(facts, q.Colorspace)
	}
	if q.BitDepth > 0 {
		facts = append(facts, fmt.Sprintf("%d-bit", q.BitDepth))
	}
	if q.ChromaSubsampling != "" {
		facts = append(facts, q.ChromaSubsampling)
	}
	if len(facts) > 0 {
		return strings.Join(facts, " ") + "  " + body
	}
	return body
}
