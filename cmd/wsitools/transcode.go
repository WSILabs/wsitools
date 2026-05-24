package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/spf13/cobra"

	codec "github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/pipeline"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

var (
	tcOutput     string
	tcCodec      string
	tcQuality    int
	tcCodecOpts  []string
	tcContainer  string
	tcJobs       int
	tcBigTIFF    string
	tcForce      bool
	tcTileOrder  string
)

var transcodeCmd = &cobra.Command{
	Use:   "transcode [flags] <input>",
	Short: "Re-encode the pyramid tiles in a different compression codec",
	Long: `Re-encode the pyramid tiles of a WSI in a different compression codec
while preserving the source's tile geometry and metadata. Associated images
(label, macro, thumbnail, overview) are passed through verbatim.

Output container defaults:
  --codec jpeg on SVS source: SVS-shaped output (Aperio convention).
  Everything else: generic pyramidal TIFF with WSIImageType-tagged IFDs.

v0.2.0 supported source formats: SVS, Philips-TIFF, OME-TIFF (tiled), BIF, IFE,
generic-TIFF. NDPI, OME-OneFrame, and Leica SCN error cleanly with
ErrUnsupportedFormat.

Examples:

  # SVS to JPEG-XL (generic TIFF output, since JPEG-XL doesn't fit SVS).
  wsitools transcode --codec jpegxl -o slide-jxl.tiff slide.svs

  # SVS re-encoded as JPEG at a different quality (still SVS-shaped).
  wsitools transcode --codec jpeg --quality 75 -o slide-q75.svs slide.svs

  # AVIF with a faster encoder preset.
  wsitools transcode --codec avif --codec-opt avif.speed=8 -o out.tiff in.svs

  # Lossless WebP for archival.
  wsitools transcode --codec webp --codec-opt webp.lossless=true -o out.tiff in.svs`,
	Args: cobra.ExactArgs(1),
	RunE: runTranscode,
}

func init() {
	transcodeCmd.Flags().StringVarP(&tcOutput, "output", "o", "", "output file path (required)")
	transcodeCmd.Flags().StringVar(&tcCodec, "codec", "", "target codec: jpeg|jpegxl|avif|webp|htj2k")
	transcodeCmd.Flags().IntVar(&tcQuality, "quality", 85, "codec-agnostic quality 1..100")
	transcodeCmd.Flags().StringSliceVar(&tcCodecOpts, "codec-opt", nil, "codec-specific KEY=VAL (repeatable)")
	transcodeCmd.Flags().StringVar(&tcContainer, "container", "", "output container: svs|tiff (default depends on source + codec)")
	transcodeCmd.Flags().IntVar(&tcJobs, "jobs", runtime.NumCPU(), "worker goroutines")
	transcodeCmd.Flags().StringVar(&tcBigTIFF, "bigtiff", "auto", "auto|on|off")
	transcodeCmd.Flags().BoolVarP(&tcForce, "force", "f", false, "overwrite output if it exists")
	transcodeCmd.Flags().StringVar(&tcTileOrder, "tile-order", "row-major",
		"Tile emission order within each level (row-major|hilbert|morton). "+
			"Format-restricted: SVS accepts row-major only; COG-WSI / TIFF / OME-TIFF "+
			"accept all three.")
	_ = transcodeCmd.MarkFlagRequired("output")
	_ = transcodeCmd.MarkFlagRequired("codec")
	rootCmd.AddCommand(transcodeCmd)
}

func runTranscode(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	input := args[0]
	start := time.Now()

	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !tcForce {
		if _, err := os.Stat(tcOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", tcOutput)
		}
	}
	if tcQuality < 1 || tcQuality > 100 {
		return fmt.Errorf("--quality must be 1..100")
	}

	fac, err := codec.Lookup(tcCodec)
	if err != nil {
		return fmt.Errorf("--codec %q: %w", tcCodec, err)
	}

	src, err := source.Open(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported at v0.2.0: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	container := resolveContainer(src.Format(), tcCodec, tcContainer)
	bigtiffMode := resolveBigTIFFMode(tcBigTIFF, src)

	order, err := tileorder.ByName(tcTileOrder)
	if err != nil {
		return fmt.Errorf("--tile-order: %w", err)
	}

	knobs := map[string]string{"q": fmt.Sprintf("%d", tcQuality)}
	for _, opt := range tcCodecOpts {
		k, v, ok := strings.Cut(opt, "=")
		if !ok {
			return fmt.Errorf("--codec-opt %q: missing '='", opt)
		}
		// Strip codec prefix when present (e.g. "jxl.distance=1.5" → "distance").
		if pfx := tcCodec + "."; strings.HasPrefix(k, pfx) {
			k = k[len(pfx):]
		} else if dotPfx := strings.SplitN(k, ".", 2); len(dotPfx) == 2 {
			k = dotPfx[1]
		}
		knobs[k] = v
	}

	md := src.Metadata()

	// Build writer options.
	opts := streamwriter.Options{
		BigTIFF:        bigtiffMode,
		ToolsVersion:   Version,
		SourceFormat:   src.Format(),
		FormatName:     container,
		AcceptedOrders: acceptedOrdersForFormat(container),
		DefaultOrder:   order,
	}
	if md.Make != "" {
		opts.Make = md.Make
	}
	if md.Model != "" {
		opts.Model = md.Model
	}
	if md.Software != "" {
		opts.Software = md.Software
	}
	if !md.AcquisitionDateTime.IsZero() {
		opts.DateTime = md.AcquisitionDateTime
	}

	// ImageDescription handling.
	//   - SVS container: leave opts.ImageDescription empty; the L0 IFD
	//     gets the Aperio ImageDescription via LevelSpec.ExtraTags
	//     (buildSVSL0ExtraTags). srcImageDesc is threaded into
	//     transcodeLevel below.
	//   - Generic container: set opts.ImageDescription to a wsitools
	//     provenance string.
	var srcImageDesc string
	if container == "svs" && src.Format() == string(opentile.FormatSVS) {
		srcImageDesc = src.SourceImageDescription()
	} else {
		opts.ImageDescription = buildProvenanceDesc(src, tcCodec, md)
	}

	w, err := streamwriter.Create(tcOutput, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}

	if err := transcodePyramid(cmd.Context(), src, w, fac, knobs, tcJobs, container, srcImageDesc); err != nil {
		w.Abort()
		return err
	}

	if err := writeAssociatedImages(src, w, container); err != nil {
		w.Abort()
		return err
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}

	stat, _ := os.Stat(tcOutput)
	if stat != nil {
		slog.Info("transcode complete",
			"output", tcOutput,
			"size", formatBytes(stat.Size()),
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		fmt.Printf("wrote %s (%s, %s)\n", tcOutput, formatBytes(stat.Size()), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

func resolveContainer(srcFormat, codecName, override string) string {
	if override != "" {
		return override
	}
	if srcFormat == string(opentile.FormatSVS) && codecName == "jpeg" {
		return "svs"
	}
	return "tiff"
}

// resolveBigTIFFMode maps the CLI --bigtiff flag to a tiff.BigTIFFMode.
// "auto" promotes to BigTIFFOn when the source pixel count exceeds the
// 2 GiB classic-TIFF safety threshold.
func resolveBigTIFFMode(mode string, src source.Source) tiff.BigTIFFMode {
	switch mode {
	case "on":
		return tiff.BigTIFFOn
	case "off":
		return tiff.BigTIFFOff
	}
	// auto: predict output size; promote when > 2 GiB.
	// Estimate ~1 byte per pixel for lossy codecs.
	var total int64
	for _, lvl := range src.Levels() {
		total += int64(lvl.Size().X) * int64(lvl.Size().Y)
	}
	if total > (2 << 30) {
		return tiff.BigTIFFOn
	}
	return tiff.BigTIFFOff
}

func transcodePyramid(ctx context.Context, src source.Source, w *streamwriter.Writer, fac codec.EncoderFactory, knobs map[string]string, workers int, container, srcImageDesc string) error {
	for _, lvl := range src.Levels() {
		if err := transcodeLevel(ctx, lvl, w, fac, knobs, workers, container, srcImageDesc); err != nil {
			return fmt.Errorf("level %d: %w", lvl.Index(), err)
		}
	}
	return nil
}

func transcodeLevel(ctx context.Context, lvl source.Level, w *streamwriter.Writer, fac codec.EncoderFactory, knobs map[string]string, workers int, container, srcImageDesc string) error {
	enc, err := fac.NewEncoder(codec.LevelGeometry{
		TileWidth: lvl.TileSize().X, TileHeight: lvl.TileSize().Y,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: knobs})
	if err != nil {
		return err
	}
	defer enc.Close()

	spec := streamwriter.LevelSpec{
		ImageWidth:      uint32(lvl.Size().X),
		ImageHeight:     uint32(lvl.Size().Y),
		TileWidth:       uint32(lvl.TileSize().X),
		TileHeight:      uint32(lvl.TileSize().Y),
		Compression:     enc.TIFFCompressionTag(),
		Photometric:     2, // RGB; codecs carry their own colour model
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		JPEGTables:      enc.LevelHeader(),
		NewSubfileType:  newSubfileTypeForLevel(lvl.Index(), container),
		WSIImageType:    tiff.WSIImageTypePyramid,
	}
	// SVS-shaped output: emit Aperio ImageDescription verbatim on L0.
	if container == "svs" && lvl.Index() == 0 && srcImageDesc != "" {
		spec.ExtraTags = buildSVSL0ExtraTags(srcImageDesc)
	}
	// NOTE: codec ExtraTIFFTags() currently returns nil for every
	// registered codec and still uses the legacy TIFFTag type from the
	// old writer package. Wiring the codec interface to tiff.RawTag is a
	// future task; the loop is dropped here so this file has zero
	// references to the legacy writer package.

	lh, err := w.AddLevel(spec)
	if err != nil {
		return err
	}

	decFac := pickDecoder(lvl.Compression())
	if decFac == nil {
		return fmt.Errorf("no decoder for source compression %s", lvl.Compression())
	}

	grid := lvl.Grid()
	tileW := lvl.TileSize().X
	tileH := lvl.TileSize().Y
	maxTileBytes := lvl.TileMaxSize()
	pool := &sync.Pool{
		New: func() any {
			b := make([]byte, maxTileBytes)
			return &b
		},
	}

	sink := func(t pipeline.Tile) error {
		return lh.WriteTile(t.X, t.Y, t.Bytes)
	}

	// Run the ordered drain concurrently with pipeline.Run.
	drainErr := make(chan error, 1)
	go func() {
		for {
			idx, bytes, ok, err := lh.NextReady()
			if err != nil {
				drainErr <- err
				return
			}
			if !ok {
				drainErr <- nil
				return
			}
			if err := lh.WriteTileAtIndex(idx, bytes); err != nil {
				lh.Abort(err)
				drainErr <- err
				return
			}
		}
	}()

	pipeErr := pipeline.Run(ctx, pipeline.Config{
		Workers: workers,
		Source: func(ctx context.Context, emit func(pipeline.Tile) error) error {
			for ty := 0; ty < grid.Y; ty++ {
				for tx := 0; tx < grid.X; tx++ {
					bufp := pool.Get().(*[]byte)
					n, err := lvl.TileInto(tx, ty, *bufp)
					if err != nil {
						pool.Put(bufp)
						return err
					}
					t := pipeline.Tile{
						Level:   lvl.Index(),
						X:       uint32(tx),
						Y:       uint32(ty),
						Bytes:   (*bufp)[:n],
						Release: func() { pool.Put(bufp) },
					}
					if err := emit(t); err != nil {
						pool.Put(bufp)
						return err
					}
				}
			}
			return nil
		},
		Process: func(t pipeline.Tile) (pipeline.Tile, error) {
			dec := decFac.New()
			defer dec.Close()
			img, err := dec.Decode(t.Bytes, decoder.DecodeOptions{
				Scale:  1,
				Format: decoder.PixelFormatRGB,
			})
			if t.Release != nil {
				t.Release()
				t.Release = nil
			}
			if err != nil {
				return pipeline.Tile{}, err
			}
			encoded, err := enc.EncodeTile(img.Pix, tileW, tileH, nil)
			if err != nil {
				return pipeline.Tile{}, err
			}
			t.Bytes = encoded
			return t, nil
		},
		Sink: sink,
	})

	// Tell the buffer no more tiles will arrive.
	lh.CloseInput()

	// Propagate pipeline error (or wait for drain to finish).
	if pipeErr != nil {
		lh.Abort(pipeErr)
		<-drainErr
		return pipeErr
	}
	return <-drainErr
}

func pickDecoder(c source.Compression) decoder.Factory {
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

func writeAssociatedImages(src source.Source, w *streamwriter.Writer, container string) error {
	for _, a := range src.Associated() {
		bs, err := a.Bytes()
		if err != nil {
			return fmt.Errorf("associated %s: %w", a.Kind(), err)
		}
		spec := streamwriter.StrippedSpec{
			Width:           uint32(a.Size().X),
			Height:          uint32(a.Size().Y),
			RowsPerStrip:    uint32(a.Size().Y),
			BitsPerSample:   []uint16{8, 8, 8},
			SamplesPerPixel: 3,
			Photometric:     2,
			Compression:     mapCompressionForOutput(a.Compression()),
			StripBytes:      bs,
			NewSubfileType:  newSubfileTypeForAssoc(container, a.Kind()),
			WSIImageType:    a.Kind(),
		}
		// SVS-shaped output: emit Aperio-flavored NewSubfileType via
		// ExtraTags (macro=9, label=1). Clear spec.NewSubfileType so the
		// writer doesn't also emit a default value — EntryBuilder doesn't
		// dedup, so a duplicate tag would corrupt the IFD.
		if container == "svs" {
			switch a.Kind() {
			case "macro", "overview":
				spec.NewSubfileType = 0
				spec.ExtraTags = buildSVSMacroExtraTags()
			case "label":
				spec.NewSubfileType = 0
				spec.ExtraTags = buildSVSLabelExtraTags()
			}
		}
		if err := w.AddStripped(spec); err != nil {
			return fmt.Errorf("write associated %s: %w", a.Kind(), err)
		}
	}
	return nil
}

func mapCompressionForOutput(c source.Compression) uint16 {
	switch c {
	case source.CompressionJPEG:
		return tiff.CompressionJPEG
	case source.CompressionLZW:
		return tiff.CompressionLZW
	case source.CompressionJPEG2000:
		return tiff.CompressionJPEG2000
	}
	return tiff.CompressionNone
}

// newSubfileTypeForLevel returns the NewSubfileType for a pyramid IFD.
// L0 is non-reduced (0); all other levels are reduced-resolution (1).
// The Aperio convention is the same for SVS-shaped output.
func newSubfileTypeForLevel(idx int, container string) uint32 {
	_ = container
	if idx == 0 {
		return 0
	}
	return 1
}

// newSubfileTypeForAssoc returns the default NewSubfileType for an
// associated image. Any associated image is reduced-resolution (1).
// SVS-shaped output adds the Aperio-private macro=9 marker via
// ExtraTags in writeAssociatedImages — that path doesn't need special
// handling here.
func newSubfileTypeForAssoc(container, kind string) uint32 {
	_ = container
	_ = kind
	return 1
}

// acceptedOrdersForFormat returns the per-format whitelist of tile-order names.
// nil = permissive (all registered strategies allowed).
func acceptedOrdersForFormat(format string) []string {
	switch format {
	case "svs":
		return []string{"row-major"}
	case "tiff", "ome-tiff":
		return nil // permissive
	}
	return nil
}

func buildProvenanceDesc(src source.Source, codecName string, md source.Metadata) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wsi-tools/%s transcode source=%s codec=%s", Version, src.Format(), codecName)
	if md.MPP > 0 {
		fmt.Fprintf(&b, " mpp=%v", md.MPP)
	}
	if md.Magnification > 0 {
		fmt.Fprintf(&b, " mag=%vx", md.Magnification)
	}
	if md.Make != "" || md.Model != "" {
		fmt.Fprintf(&b, " scanner=%q", strings.TrimSpace(md.Make+" "+md.Model))
	}
	if !md.AcquisitionDateTime.IsZero() {
		fmt.Fprintf(&b, " date=%s", md.AcquisitionDateTime.Format("2006-01-02"))
	}
	return b.String()
}
