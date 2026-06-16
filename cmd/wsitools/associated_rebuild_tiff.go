package main

import (
	"fmt"
	"os"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// rebuildGenericTIFF re-finalizes src as a generic-TIFF at outPath with the
// associated-edit plan applied, by tile-copying the pyramid verbatim (no
// re-encode → pixel-identical) through the streamwriter. It is the fallback for
// associated remove/replace when the in-place splice engine can't handle the
// source's byte layout — notably a wsitools-produced generic-TIFF, whose
// streamwriter layout puts the L0 directory past the splice "cutoff". Unlike
// OME-TIFF rebuild, generic-TIFF carries no OME-XML, so nothing descriptive is
// lost; only the exact byte offsets change (fresh container). Writes a sibling
// temp then atomically renames (safe for --in-place).
func rebuildGenericTIFF(src source.Source, outPath string, plan omeEditPlan, fsync bool) error {
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
	order, err := tileorder.ByName("row-major")
	if err != nil {
		return fmt.Errorf("tile order: %w", err)
	}
	md := src.Metadata()
	opts := streamwriter.Options{
		BigTIFF:          resolveBigTIFFMode("auto", src),
		ToolsVersion:     Version,
		SourceFormat:     src.Format(),
		FormatName:       "tiff",
		AcceptedOrders:   acceptedOrdersForFormat("tiff"),
		DefaultOrder:     order,
		MPPX:             md.MPPX,
		MPPY:             md.MPPY,
		Magnification:    md.Magnification,
		ICCProfile:       md.ICCProfile,
		ImageDescription: buildProvenanceDesc(src, "associated-edit", md),
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
	// container "tiff", no synthetic OME-XML, no L0-description ExtraTag.
	if err := writeTIFFTileCopy(w, src, "tiff", "" /*l0Desc*/, false /*omeSynthetic*/, plan); err != nil {
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
