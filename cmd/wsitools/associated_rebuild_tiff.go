package main

import (
	"fmt"
	"os"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// baseRebuildOpts builds the streamwriter.Options shared by the TIFF-family
// associated-edit rebuild fallbacks (generic-TIFF, SVS): row-major tile order,
// carried MPP/mag/ICC/Make/Model/Software/DateTime, container FormatName /
// AcceptedOrders. Container-specific fields (ImageDescription, SVS L0 conformance
// tags) are set by the caller.
func baseRebuildOpts(src source.Source, formatName string) (streamwriter.Options, error) {
	order, err := tileorder.ByName("row-major")
	if err != nil {
		return streamwriter.Options{}, fmt.Errorf("tile order: %w", err)
	}
	md := src.Metadata()
	opts := streamwriter.Options{
		BigTIFF:        resolveBigTIFFMode("auto", src),
		ToolsVersion:   Version,
		SourceFormat:   src.Format(),
		FormatName:     formatName,
		AcceptedOrders: acceptedOrdersForFormat(formatName),
		DefaultOrder:   order,
		MPPX:           md.MPPX,
		MPPY:           md.MPPY,
		Magnification:  md.Magnification,
		ICCProfile:     md.ICCProfile,
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
	return opts, nil
}

// finalizeRebuild re-finalizes src at outPath via the streamwriter with the
// associated-edit plan applied, tile-copying the pyramid verbatim (pixel-
// identical). Writes a sibling temp then atomically renames (safe for
// --in-place). Shared by the generic-TIFF and SVS rebuild fallbacks.
func finalizeRebuild(src source.Source, outPath, container, l0Desc string, omeSynthetic bool, opts streamwriter.Options, plan omeEditPlan, fsync bool) error {
	if len(src.Levels()) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
	tmp := fmt.Sprintf("%s.tmp-%d", outPath, os.Getpid())
	w, err := streamwriter.Create(tmp, opts)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	if err := writeTIFFTileCopy(w, src, container, l0Desc, omeSynthetic, plan); err != nil {
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

// rebuildGenericTIFF re-finalizes src as a generic-TIFF at outPath with the
// associated-edit plan applied. Fallback for associated remove/replace when the
// in-place splice engine can't handle the source's byte layout — notably a
// wsitools-produced generic-TIFF whose streamwriter layout puts the L0 directory
// past the splice "cutoff". generic-TIFF carries no OME-XML, so nothing
// descriptive is lost; only byte offsets change.
func rebuildGenericTIFF(src source.Source, outPath string, plan omeEditPlan, fsync bool) error {
	opts, err := baseRebuildOpts(src, "tiff")
	if err != nil {
		return err
	}
	opts.ImageDescription = buildProvenanceDesc(src, "associated-edit", src.Metadata())
	return finalizeRebuild(src, outPath, "tiff", "" /*l0Desc*/, false /*omeSynthetic*/, opts, plan, fsync)
}
