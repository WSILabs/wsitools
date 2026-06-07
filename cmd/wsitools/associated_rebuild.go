package main

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/wsilabs/wsitools/internal/source"
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
	for _, lvl := range src.Levels() {
		spec := cogwsiwriter.LevelSpec{
			ImageWidth:      uint32(lvl.Size().X),
			ImageHeight:     uint32(lvl.Size().Y),
			TileWidth:       uint32(lvl.TileSize().X),
			TileHeight:      uint32(lvl.TileSize().Y),
			Compression:     compressionTagFor(lvl.Compression()),
			Photometric:     2,
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

	if plan.dropAll {
		return nil
	}

	replaced := false
	for _, a := range src.Associated() {
		if a.Type() == plan.remove {
			continue
		}
		if plan.replace != "" && a.Type() == plan.replace {
			if err := w.AddAssociated(*plan.spec); err != nil {
				return fmt.Errorf("add replacement %s: %w", plan.replace, err)
			}
			replaced = true
			continue
		}
		bs, err := a.Bytes()
		if err != nil {
			return fmt.Errorf("read associated %s: %w", a.Type(), err)
		}
		spec := cogwsiwriter.AssociatedSpec{
			Type:        a.Type(),
			Width:       uint32(a.Size().X),
			Height:      uint32(a.Size().Y),
			Compression: compressionTagFor(a.Compression()),
			Photometric: 2,
			Bytes:       bs,
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
