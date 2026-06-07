package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// omeEditPlan parameterizes associated-image output. At most one of
// remove/replace is set; empty plan writes all verbatim; dropAll writes none.
type omeEditPlan struct {
	remove  string
	replace string
	spec    *streamwriter.StrippedSpec
	dropAll bool
}

// omeTIFFLossyWarning is emitted on EVERY OME-TIFF edit, regardless of flags.
const omeTIFFLossyWarning = "OME-TIFF editing rebuilds the file with a regenerated, minimal OME-XML — instrument, acquisition, channel, and vendor annotations are NOT preserved (pixels, geometry/MPP/magnification, and the other associated images are). wsitools' OME-TIFF support is rudimentary; for serious OME-TIFF work use Bio-Formats. See docs/ome-tiff-limitations.md."

func warnOMETIFFLossy() { slog.Warn(omeTIFFLossyWarning) }

// rebuildOMETIFF re-finalizes src as an OME-TIFF at outPath with plan applied,
// forcing a synthetic (minimal) OME-XML built from the plan-edited associated
// set. Writes a sibling temp then atomically renames (safe for --in-place).
func rebuildOMETIFF(src source.Source, outPath string, plan omeEditPlan, fsync bool) error {
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
	md := src.Metadata()
	l0 := src.Levels()[0]
	srcSoft := strings.TrimSpace(md.Make + " " + md.Model)
	// Forced synthetic OME-XML reflecting the edit. arg order:
	// SyntheticOMEDescription(l0W, l0H uint32, mppX, mppY float64,
	//   name, srcSoftware string, assoc []OMEAssoc).
	l0Desc := SyntheticOMEDescription(
		uint32(l0.Size().X), uint32(l0.Size().Y),
		md.MPP, md.MPP, "Image", srcSoft,
		omeAssociatedSpecs(src, plan),
	)
	opts := streamwriter.Options{
		BigTIFF:              resolveBigTIFFMode("auto", src),
		ToolsVersion:         Version,
		SourceFormat:         src.Format(),
		FormatName:           "ome-tiff",
		SubResolutionPyramid: true,
		SampleFormat:         1,
		MPPX:                 md.MPPX,
		MPPY:                 md.MPPY,
		Magnification:        md.Magnification,
		ICCProfile:           md.ICCProfile,
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
	tmp := fmt.Sprintf("%s.tmp-%d", outPath, os.Getpid())
	w, err := streamwriter.Create(tmp, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	if err := writeTIFFTileCopy(w, src, "ome-tiff", l0Desc, true /*omeSynthetic*/, plan); err != nil {
		w.Abort()
		os.Remove(tmp)
		return err
	}
	if err := w.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("finalize output: %w", err)
	}
	if fsync {
		f, e := os.Open(tmp)
		if e != nil {
			os.Remove(tmp)
			return fmt.Errorf("fsync open: %w", e)
		}
		syncErr := f.Sync()
		closeErr := f.Close()
		if syncErr != nil {
			os.Remove(tmp)
			return fmt.Errorf("fsync: %w", syncErr)
		}
		if closeErr != nil {
			os.Remove(tmp)
			return fmt.Errorf("fsync close: %w", closeErr)
		}
	}
	if err := os.Rename(tmp, outPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// runAssociatedRemoveForOMETIFF removes the typ associated image from an
// OME-TIFF by rebuilding the file (lossy — see warnOMETIFFLossy).
func runAssociatedRemoveForOMETIFF(typ, input, outPath string, fl removeFlags) error {
	src, err := source.Open(input)
	if err != nil {
		return err
	}
	defer src.Close()
	found := false
	for _, a := range src.Associated() {
		if a.Type() == typ {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no %s image in slide", typ)
	}
	warnOMETIFFLossy()
	if err := rebuildOMETIFF(src, outPath, omeEditPlan{remove: typ}, fl.fsync); err != nil {
		return err
	}
	if !fl.quiet {
		fmt.Printf("wsitools: removed %s: %s -> %s\n", typ, input, outPath)
	}
	return nil
}

// runAssociatedReplaceForOMETIFF replaces (or adds) the typ associated image on
// an OME-TIFF: decode the user's image, resize/encode it into a StrippedSpec, and
// rebuild the file with that spec substituted/appended (lossy — see
// warnOMETIFFLossy). OME-TIFF associated images are self-contained JPEG/LZW, so a
// replacement round-trips on read-back.
func runAssociatedReplaceForOMETIFF(typ, input, outPath string, fl replaceFlags) error {
	src, err := source.Open(input)
	if err != nil {
		return err
	}
	defer src.Close()

	var existing source.AssociatedImage
	for _, a := range src.Associated() {
		if a.Type() == typ {
			existing = a
		}
	}
	img, err := decodeReplacementImage(fl.image)
	if err != nil {
		return err
	}
	tw, th, err := resolveTargetDims(typ, img, existing, existing != nil, fl.labelDims)
	if err != nil {
		return err
	}
	bg, err := parseHexColor(fl.bgHex)
	if err != nil {
		return err
	}
	// opentile-go's OME-TIFF associated reader decodes only JPEG (7) and none (1);
	// an explicit LZW/Deflate associated image is written faithfully but reads back
	// as CompressionUnknown (not decodable). Warn rather than silently override.
	if c := strings.ToLower(fl.compression); c == "lzw" || c == "deflate" {
		slog.Warn("OME-TIFF associated images only round-trip with jpeg or none compression; "+
			"the chosen codec will be written but is not decodable on read-back by opentile-go",
			"compression", c)
	}
	resize := fl.resize
	if resize == "" {
		resize = "fit"
	}
	spec, err := buildReplacementStrippedSpec(img, replaceOpts{
		typ:         typ,
		compression: fl.compression,
		resize:      resize,
		bg:          bg,
		targetW:     tw,
		targetH:     th,
		force:       fl.force,
	})
	if err != nil {
		return err
	}
	warnOMETIFFLossy()
	if err := rebuildOMETIFF(src, outPath, omeEditPlan{replace: typ, spec: spec}, fl.fsync); err != nil {
		return err
	}
	if !fl.quiet {
		verb := "replaced"
		if existing == nil {
			verb = "added"
		}
		fmt.Printf("wsitools: %s %s: %s -> %s\n", verb, typ, input, outPath)
	}
	return nil
}
