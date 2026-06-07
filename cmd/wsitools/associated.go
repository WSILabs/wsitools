package main

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	// Image decoders for --image: PNG, JPEG, and TIFF.
	_ "image/jpeg"
	_ "image/png"

	xtiff "golang.org/x/image/tiff"

	"github.com/wsilabs/opentile-go"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/edit"
)

func init() {
	// Importing golang.org/x/image/tiff registers TIFF with image.Decode via
	// its package init, so PNG/JPEG/TIFF --image inputs all decode through
	// image.Decode; xtiff.Decode is also used directly as a fallback below.
	for _, ty := range []string{"label", "macro", "thumbnail", "overview"} {
		rootCmd.AddCommand(newAssocTypeCmd(ty))
	}
}

// ---------- shared flag structs ----------

type assocCommonFlags struct {
	output    string
	inPlace   bool
	overwrite bool
	fsync     bool
	quiet     bool
}

type removeFlags struct{ assocCommonFlags }

type replaceFlags struct {
	assocCommonFlags
	image       string
	compression string
	resize      string
	bgHex       string
	labelDims   string
	force       bool
}

// ---------- format gating ----------

// assocFormatSupported reports whether associated-image editing is supported
// for the given source format string. Only SVS and generic-TIFF are supported.
func assocFormatSupported(format string) bool {
	switch format {
	case string(opentile.FormatSVS), string(opentile.FormatGenericTIFF), string(opentile.FormatCOGWSI), string(opentile.FormatOMETIFF):
		return true
	default:
		return false
	}
}

func gateFormat(src source.Source) error {
	f := src.Format()
	if !assocFormatSupported(f) {
		return fmt.Errorf("%w: associated editing not yet supported for %s "+
			"(SVS, generic-TIFF, COG-WSI, and OME-TIFF only — "+
			"for other transforms use 'wsitools convert')", ErrUnsupportedAssoc, f)
	}
	return nil
}

// ---------- output-path resolution ----------

// resolveAssocOutput resolves the final output path.
//   - inPlace: returns input (Splice writes a sibling temp then renames over it).
//   - out=="": derive "<stem>_relabeled<ext>" next to input (numbered suffix if exists, unless overwrite).
//   - out!="": use it; error if exists and !overwrite.
//   - error if inPlace && out!="" ; error if resolved == input (unless inPlace).
func resolveAssocOutput(input, out string, inPlace, overwrite bool) (string, error) {
	if inPlace && out != "" {
		return "", fmt.Errorf("--in-place and -o/--output are mutually exclusive")
	}
	if inPlace {
		return input, nil
	}

	absInput, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("abs input: %w", err)
	}

	var resolved string
	if out == "" {
		resolved = deriveAssocPath(absInput, overwrite)
	} else {
		absOut, err := filepath.Abs(out)
		if err != nil {
			return "", fmt.Errorf("abs out: %w", err)
		}
		if _, err := os.Stat(absOut); err == nil && !overwrite {
			return "", fmt.Errorf("output file already exists: %s (use --overwrite)", absOut)
		}
		resolved = absOut
	}

	if resolved == absInput {
		return "", fmt.Errorf("input and output paths are the same: %s", resolved)
	}
	return resolved, nil
}

func deriveAssocPath(absInput string, overwrite bool) string {
	dir := filepath.Dir(absInput)
	base := filepath.Base(absInput)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	first := filepath.Join(dir, stem+"_relabeled"+ext)
	if overwrite {
		return first
	}
	if _, err := os.Stat(first); os.IsNotExist(err) {
		return first
	}
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s_relabeled_%d%s", stem, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// ---------- run logic ----------

// parseSlideFile parses the IFD structure of a TIFF slide without materializing
// the whole file in memory: edit.Parse reads only IFD records and small tag
// blobs via bounded ReadAt calls. The handle is closed before returning —
// edit.Splice reopens the input itself for the prefix copy.
func parseSlideFile(input string) (*edit.File, error) {
	fh, err := os.Open(input)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", input, err)
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", input, err)
	}
	f, err := edit.Parse(fh, st.Size())
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", input, err)
	}
	return f, nil
}

func runAssociatedRemoveFor(typ, input, outPath string, fl removeFlags) error {
	src, err := source.Open(input)
	if err != nil {
		return err
	}

	if err := gateFormat(src); err != nil {
		src.Close()
		return err
	}

	// COG-WSI can't be Slice-1-spliced (it would break COG ghost-area/alignment
	// invariants); re-finalize through cogwsiwriter instead. Close our handle
	// first — the cog-wsi engine opens its own.
	if src.Format() == string(opentile.FormatCOGWSI) {
		src.Close()
		return runAssociatedRemoveForCOGWSI(typ, input, outPath, fl)
	}
	// OME-TIFF can't be Slice-1-spliced; rebuild through the ome-tiff streamwriter
	// (lossy — regenerates a minimal OME-XML). Close our handle first.
	if src.Format() == string(opentile.FormatOMETIFF) {
		src.Close()
		return runAssociatedRemoveForOMETIFF(typ, input, outPath, fl)
	}
	defer src.Close()

	f, err := parseSlideFile(input)
	if err != nil {
		return err
	}

	idx, _, err := locateAssociated(src, f, typ)
	if err != nil {
		if errors.Is(err, ErrNoSuchAssociated) {
			return fmt.Errorf("no %s image in slide", typ)
		}
		return err
	}

	if err := edit.Splice(edit.SpliceParams{
		InPath:    input,
		OutPath:   outPath,
		File:      f,
		Mode:      edit.SpliceRemove,
		TargetIdx: idx,
		Fsync:     fl.fsync,
	}); err != nil {
		return err
	}

	if !fl.quiet {
		fmt.Printf("wsitools: removed %s: %s -> %s\n", typ, input, outPath)
	}
	return nil
}

func runAssociatedReplaceFor(typ, input, outPath string, fl replaceFlags) error {
	src, err := source.Open(input)
	if err != nil {
		return err
	}

	if err := gateFormat(src); err != nil {
		src.Close()
		return err
	}

	// COG-WSI can't be Slice-1-spliced; re-finalize through cogwsiwriter instead.
	// Close our handle first — the cog-wsi engine opens its own.
	if src.Format() == string(opentile.FormatCOGWSI) {
		src.Close()
		return runAssociatedReplaceForCOGWSI(typ, input, outPath, fl)
	}
	defer src.Close()

	// SVS replace is supported only for the label today. opentile-go reads
	// Aperio thumbnail/macro/overview as abbreviated JPEG (tables in the
	// JPEGTables tag, reassembled via ConcatenateScans), which a standalone
	// re-encode does not yet satisfy — a replaced image would be unreadable.
	// remove works for every type; generic-TIFF supports replacing any type.
	if src.Format() == "svs" && typ != "label" {
		return fmt.Errorf("%w: replacing the %s on SVS is not yet supported (only label); remove works for all types, and generic-TIFF supports replacing any type", ErrUnsupportedAssoc, typ)
	}

	f, err := parseSlideFile(input)
	if err != nil {
		return err
	}

	idx, existing, locErr := locateAssociated(src, f, typ)
	found := locErr == nil
	if locErr != nil && !errors.Is(locErr, ErrNoSuchAssociated) {
		return locErr
	}

	// Decode the replacement image.
	img, err := decodeReplacementImage(fl.image)
	if err != nil {
		return err
	}

	// Determine target dims.
	tw, th, err := resolveTargetDims(typ, img, existing, found, fl.labelDims)
	if err != nil {
		return err
	}

	// Parse background.
	bg, err := parseHexColor(fl.bgHex)
	if err != nil {
		return err
	}

	resize := fl.resize
	if resize == "" {
		resize = "fit"
	}

	rep, err := buildReplacementIFD(img, replaceOpts{
		typ:         typ,
		format:      src.Format(),
		compression: fl.compression,
		desc:        "",
		resize:      resize,
		bg:          bg,
		targetW:     tw,
		targetH:     th,
		force:       fl.force,
	})
	if err != nil {
		return err
	}

	mode := edit.SpliceReplace
	targetIdx := idx
	verb := "replaced"
	if !found {
		mode = edit.SpliceAppend
		targetIdx = len(f.IFDs)
		verb = "added"
	}

	if err := edit.Splice(edit.SpliceParams{
		InPath:      input,
		OutPath:     outPath,
		File:        f,
		Mode:        mode,
		TargetIdx:   targetIdx,
		Replacement: rep,
		Fsync:       fl.fsync,
	}); err != nil {
		return err
	}

	if !fl.quiet {
		fmt.Printf("wsitools: %s %s: %s -> %s\n", verb, typ, input, outPath)
	}
	return nil
}

// decodeReplacementImage decodes a PNG/JPEG/TIFF file from disk.
func decodeReplacementImage(path string) (image.Image, error) {
	if path == "" {
		return nil, fmt.Errorf("--image is required")
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open --image %s: %w", path, err)
	}
	defer fh.Close()

	img, _, err := image.Decode(fh)
	if err == nil {
		return img, nil
	}
	// Fallback: try x/image/tiff explicitly.
	if _, serr := fh.Seek(0, 0); serr == nil {
		if timg, terr := xtiff.Decode(fh); terr == nil {
			return timg, nil
		}
	}
	return nil, fmt.Errorf("decode --image %s: %w", path, err)
}

// resolveTargetDims picks the target dimensions for the replacement.
//   - If the associated image already exists, use its Size().
//   - Else if --label-dims WxH given, parse it.
//   - Else per-type default: label 1200x848; others: the decoded image's bounds.
func resolveTargetDims(typ string, img image.Image, existing source.AssociatedImage, found bool, labelDims string) (int, int, error) {
	if found && existing != nil {
		sz := existing.Size()
		return sz.X, sz.Y, nil
	}
	if labelDims != "" {
		w, h, err := parseWxH(labelDims)
		if err != nil {
			return 0, 0, fmt.Errorf("--label-dims: %w", err)
		}
		return w, h, nil
	}
	if typ == "label" {
		return 1200, 848, nil
	}
	b := img.Bounds()
	return b.Dx(), b.Dy(), nil
}

func parseWxH(s string) (int, int, error) {
	parts := strings.Split(strings.ToLower(s), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid dimensions %q (want WxH)", s)
	}
	w, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || w <= 0 {
		return 0, 0, fmt.Errorf("invalid width in %q", s)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || h <= 0 {
		return 0, 0, fmt.Errorf("invalid height in %q", s)
	}
	return w, h, nil
}

// parseHexColor parses an RRGGBB hex string into an opaque color.RGBA.
func parseHexColor(s string) (color.RGBA, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return color.RGBA{}, fmt.Errorf("invalid --bg color %q (want RRGGBB hex)", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("invalid --bg color %q: %w", s, err)
	}
	return color.RGBA{
		R: uint8(v >> 16),
		G: uint8(v >> 8),
		B: uint8(v),
		A: 0xFF,
	}, nil
}

// ---------- cobra wiring ----------

func newAssocTypeCmd(typ string) *cobra.Command {
	parent := &cobra.Command{
		Use:   typ,
		Short: "Edit the " + typ + " associated image",
	}

	// remove subcommand — independent flag struct per command group.
	rmFlags := &removeFlags{}
	removeCmd := &cobra.Command{
		Use:   "remove <slide>",
		Short: "Remove the " + typ + " associated image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			input := args[0]
			out, err := resolveAssocOutput(input, rmFlags.output, rmFlags.inPlace, rmFlags.overwrite)
			if err != nil {
				return err
			}
			return runAssociatedRemoveFor(typ, input, out, *rmFlags)
		},
	}
	bindCommonFlags(removeCmd, &rmFlags.assocCommonFlags)

	// replace subcommand.
	rpFlags := &replaceFlags{}
	replaceCmd := &cobra.Command{
		Use:   "replace <slide>",
		Short: "Replace (or add) the " + typ + " associated image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			input := args[0]
			out, err := resolveAssocOutput(input, rpFlags.output, rpFlags.inPlace, rpFlags.overwrite)
			if err != nil {
				return err
			}
			return runAssociatedReplaceFor(typ, input, out, *rpFlags)
		},
	}
	bindCommonFlags(replaceCmd, &rpFlags.assocCommonFlags)
	replaceCmd.Flags().StringVar(&rpFlags.image, "image", "", "replacement image file (PNG/JPEG/TIFF) [required]")
	replaceCmd.Flags().StringVar(&rpFlags.compression, "compression", "", "TIFF compression: jpeg|lzw|deflate|none (default per-type)")
	replaceCmd.Flags().StringVar(&rpFlags.resize, "resize", "fit", "resize mode: fit|stretch|none")
	replaceCmd.Flags().StringVar(&rpFlags.bgHex, "bg", "F5F5E6", "letterbox background color (RRGGBB hex)")
	replaceCmd.Flags().StringVar(&rpFlags.labelDims, "label-dims", "", "target dimensions WxH when adding a new image")
	replaceCmd.Flags().BoolVar(&rpFlags.force, "force", false, "bypass aspect-ratio guard")
	_ = replaceCmd.MarkFlagRequired("image")

	parent.AddCommand(removeCmd, replaceCmd)
	return parent
}

func bindCommonFlags(cmd *cobra.Command, fl *assocCommonFlags) {
	cmd.Flags().StringVarP(&fl.output, "output", "o", "", "output file path (default: <stem>_relabeled<ext>)")
	cmd.Flags().BoolVar(&fl.inPlace, "in-place", false, "edit the slide in place")
	cmd.Flags().BoolVar(&fl.overwrite, "overwrite", false, "overwrite output if it exists")
	cmd.Flags().BoolVar(&fl.fsync, "fsync", true, "fsync the output before rename")
	cmd.Flags().BoolVarP(&fl.quiet, "quiet", "q", false, "suppress success output")
}
