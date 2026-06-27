package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

// assocEditPlan parameterizes writeCOGWSI's associated-image output.
// At most one of remove/replace is set. The empty plan writes all associated
// images verbatim; dropAll writes none (convert's --no-associated).
type assocEditPlan struct {
	remove  string                       // type to drop ("" = none)
	replace string                       // type to substitute/append ("" = none)
	spec    *cogwsiwriter.AssociatedSpec // replacement (when replace != "")
	dropAll bool                         // write no associated images
}

// writeCOGWSI copies the source pyramid (verbatim tiles) into w and writes its
// associated images per plan. It does NOT Abort/Close w — the caller owns the
// writer lifecycle. Pyramid tile bytes are copied unmodified (no re-encode).
func writeCOGWSI(w *cogwsiwriter.Writer, src source.Source, plan assocEditPlan) error {
	levels := src.Levels()
	// Verbatim JPEG tiles must carry the photometric matching their own framing
	// (JFIF/Adobe-YCbCr → YCbCr(6); bare/Aperio → RGB(2)); sampled once from L0.
	jpegPhoto := uint16(2)
	if len(levels) > 0 && compressionTagFor(levels[0].Compression()) == tiff.CompressionJPEG {
		probe := make([]byte, levels[0].TileMaxSize())
		if n, err := levels[0].TileInto(0, 0, probe); err == nil {
			jpegPhoto = jpegTilePhotometric(probe[:n])
		}
	}
	for _, lvl := range levels {
		photometric := uint16(2)
		if compressionTagFor(lvl.Compression()) == tiff.CompressionJPEG {
			photometric = jpegPhoto
		}
		spec := cogwsiwriter.LevelSpec{
			ImageWidth:      uint32(lvl.Size().X),
			ImageHeight:     uint32(lvl.Size().Y),
			TileWidth:       uint32(lvl.TileSize().X),
			TileHeight:      uint32(lvl.TileSize().Y),
			Compression:     compressionTagFor(lvl.Compression()),
			Photometric:     photometric,
			SamplesPerPixel: 3,
			BitsPerSample:   []uint16{8, 8, 8},
			IsL0:            lvl.Index() == 0,
		}
		h, err := w.AddLevel(spec)
		if err != nil {
			return fmt.Errorf("add level %d: %w", lvl.Index(), err)
		}
		buf := make([]byte, lvl.TileMaxSize())
		grid := lvl.Grid()
		for ty := 0; ty < grid.Y; ty++ {
			for tx := 0; tx < grid.X; tx++ {
				n, err := lvl.TileInto(tx, ty, buf)
				if err != nil {
					return fmt.Errorf("read tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
				if err := h.WriteTile(uint32(tx), uint32(ty), buf[:n]); err != nil {
					return fmt.Errorf("write tile L%d(%d,%d): %w", lvl.Index(), tx, ty, err)
				}
			}
		}
	}

	return writeCOGWSIAssociated(w, src, plan)
}

// writeCOGWSIAssociated writes src's associated images into w per plan. It does
// NOT Abort/Close w — the caller owns the writer lifecycle. Split out of
// writeCOGWSI so the retile-engine path (which builds the pyramid itself) can
// reuse the verbatim associated-image copy.
func writeCOGWSIAssociated(w *cogwsiwriter.Writer, src source.Source, plan assocEditPlan) error {
	if plan.dropAll {
		return nil
	}

	replaced := false
	for _, a := range src.Associated() {
		if plan.remove != "" && a.Type() == plan.remove {
			continue
		}
		if plan.replace != "" && a.Type() == plan.replace {
			if err := w.AddAssociated(*plan.spec); err != nil {
				return fmt.Errorf("add replacement %s: %w", plan.replace, err)
			}
			replaced = true
			continue
		}
		spec, err := faithfulCOGWSISpec(a)
		if err != nil {
			if errors.Is(err, errSkipAssociated) {
				slog.Warn("skipping associated", "type", a.Type(), "reason", err)
				continue
			}
			return err
		}
		if err := w.AddAssociated(spec); err != nil {
			if errors.Is(err, cogwsiwriter.ErrInvalidAssocType) {
				slog.Warn("skipping associated image with unsupported type", "type", a.Type(), "reason", err)
				continue
			}
			return fmt.Errorf("add associated %s: %w", a.Type(), err)
		}
	}
	// Upsert: replace of an absent type appends the new image.
	if plan.replace != "" && !replaced {
		if err := w.AddAssociated(*plan.spec); err != nil {
			return fmt.Errorf("add new %s: %w", plan.replace, err)
		}
	}
	return nil
}

// rebuildCOGWSI re-finalizes src as a COG-WSI at outPath with plan applied. It
// writes to a sibling temp file then atomically renames over outPath, so both
// -o and --in-place (outPath == input) are crash-safe.
func rebuildCOGWSI(src source.Source, outPath string, plan assocEditPlan, fsync bool) error {
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
	md := src.Metadata()
	tmp := fmt.Sprintf("%s.tmp-%d", outPath, os.Getpid())
	opts := cogwsiwriter.Options{
		BigTIFF:      cogwsiwriter.BigTIFFAuto,
		ToolsVersion: Version,
		Metadata: cogwsiwriter.Metadata{
			MPPX:                md.MPPX,
			MPPY:                md.MPPY,
			Magnification:       md.Magnification,
			ICCProfile:          md.ICCProfile,
			Make:                md.Make,
			Model:               md.Model,
			Software:            md.Software,
			AcquisitionDateTime: md.AcquisitionDateTime,
			SourceFormat:        src.Format(),
			SourceImageDesc:     fmt.Sprintf("wsitools/%s %s source=%s", Version, "associated-edit", src.Format()),
		},
	}
	w, err := cogwsiwriter.Create(tmp, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	if err := writeCOGWSI(w, src, plan); err != nil {
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

// runAssociatedRemoveForCOGWSI removes the typ associated image from a COG-WSI.
func runAssociatedRemoveForCOGWSI(typ, input, outPath string, fl removeFlags) error {
	src, err := source.Open(input)
	if err != nil {
		return err
	}
	defer src.Close()
	// Confirm the type is present (contract: removing an absent image is an error).
	found := false
	for _, a := range src.Associated() {
		if a.Type() == typ {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no %s image in slide", typ)
	}
	if err := rebuildCOGWSI(src, outPath, assocEditPlan{remove: typ}, fl.fsync); err != nil {
		return err
	}
	if !fl.quiet {
		fmt.Printf("wsitools: removed %s: %s -> %s\n", typ, input, outPath)
	}
	return nil
}

// runAssociatedReplaceForCOGWSI replaces (or adds) the typ associated image on a
// COG-WSI: decode the user's image, resize/encode it into an AssociatedSpec, and
// re-finalize the slide with that spec substituted/appended.
func runAssociatedReplaceForCOGWSI(typ, input, outPath string, fl replaceFlags) error {
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
	resize := fl.resize
	if resize == "" {
		resize = "fit"
	}
	spec, err := buildReplacementAssocSpec(img, replaceOpts{
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
	if err := rebuildCOGWSI(src, outPath, assocEditPlan{replace: typ, spec: spec}, fl.fsync); err != nil {
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
