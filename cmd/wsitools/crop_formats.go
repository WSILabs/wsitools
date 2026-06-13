package main

import (
	"context"
	"fmt"
	"os"
	"time"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

// cropSourceScale returns the source MPP (X,Y) and magnification, preferring the
// Aperio ImageDescription, else opentile metadata. Crop preserves resolution, so
// these are emitted unchanged (no factor scaling).
func cropSourceScale(input string, src *opentile.Slide) (mppX, mppY, mag float64) {
	rawDesc, _ := source.ReadSourceImageDescription(input)
	if desc, err := ParseImageDescription(rawDesc); err == nil {
		return desc.MPP, desc.MPP, desc.AppMag
	}
	md := src.Metadata()
	return md.MPP.X, md.MPP.Y, md.Magnification
}

// streamwriterBigTIFF resolves the BigTIFF mode for streamwriter formats; "auto"
// promotes when the uncompressed L0 raster exceeds 4 GiB.
func streamwriterBigTIFF(flag string, w, h int) tiff.BigTIFFMode {
	switch flag {
	case "on":
		return tiff.BigTIFFOn
	case "off":
		return tiff.BigTIFFOff
	default:
		if int64(w)*int64(h)*3 > (int64(4) << 30) {
			return tiff.BigTIFFOn
		}
		return tiff.BigTIFFOff
	}
}

// reportWrote prints the standard wrote line.
func reportWrote(output string, start time.Time) {
	var sz string
	if fi, err := os.Stat(output); err == nil {
		sz = formatBytes(fi.Size())
	}
	fmt.Printf("wrote %s (%s) in %s\n", output, sz, time.Since(start).Round(time.Millisecond))
}

func cropToTIFF(ctx context.Context, src *opentile.Slide, input, output string, l0 []byte, l0W, l0H, nLevels, quality, workers int, order tileorder.OrderStrategy, bigtiffFlag string, noAssociated bool, start time.Time) error {
	mppX, mppY, mag := cropSourceScale(input, src)
	bigtiffMode := streamwriterBigTIFF(bigtiffFlag, l0W, l0H)

	imageDesc := fmt.Sprintf("wsi-tools/%s crop source=%s codec=jpeg mpp=%v mag=%vx", Version, src.Format(), mppX, mag)
	w, err := streamwriter.Create(output, streamwriter.Options{
		BigTIFF:          bigtiffMode,
		ImageDescription: imageDesc,
		ToolsVersion:     Version,
		SourceFormat:     string(src.Format()),
		FormatName:       "tiff",
		AcceptedOrders:   acceptedOrdersForFormat("tiff"),
		DefaultOrder:     order,
		MPPX:             mppX,
		MPPY:             mppY,
		Magnification:    mag,
		ICCProfile:       src.ICCProfile(),
	})
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			w.Abort()
		}
	}()
	if err := buildPyramidFromRaster(ctx, w, l0, l0W, l0H, nLevels, quality, workers, nil); err != nil {
		return fmt.Errorf("build pyramid: %w", err)
	}
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if err := writeOneAssociated(w, a); err != nil {
				return fmt.Errorf("write associated %s: %w", a.Type(), err)
			}
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	closed = true
	reportWrote(output, start)
	return nil
}

func cropToOMETIFF(ctx context.Context, src *opentile.Slide, input, output string, l0 []byte, l0W, l0H, nLevels, quality, workers int, order tileorder.OrderStrategy, bigtiffFlag string, noAssociated bool, start time.Time) error {
	mppX, mppY, mag := cropSourceScale(input, src)
	bigtiffMode := streamwriterBigTIFF(bigtiffFlag, l0W, l0H)

	var omeAssocs []OMEAssoc
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if name := omeAssocName(string(a.Type())); name != "" {
				omeAssocs = append(omeAssocs, OMEAssoc{Name: name, W: uint32(a.Size().W), H: uint32(a.Size().H)})
			}
		}
	}
	omeXML := SyntheticOMEDescriptionWithMag(uint32(l0W), uint32(l0H), mppX, mppY, mag, "Image", string(src.Format()), omeAssocs)

	w, err := streamwriter.Create(output, streamwriter.Options{
		BigTIFF:              bigtiffMode,
		ImageDescription:     omeXML,
		ToolsVersion:         Version,
		SourceFormat:         string(src.Format()),
		FormatName:           "ome-tiff",
		AcceptedOrders:       acceptedOrdersForFormat("ome-tiff"),
		DefaultOrder:         order,
		MPPX:                 mppX,
		MPPY:                 mppY,
		Magnification:        mag,
		ICCProfile:           src.ICCProfile(),
		SubResolutionPyramid: true,
		SampleFormat:         1,
	})
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			w.Abort()
		}
	}()
	if err := buildPyramidFromRaster(ctx, w, l0, l0W, l0H, nLevels, quality, workers, nil); err != nil {
		return fmt.Errorf("build pyramid: %w", err)
	}
	if !noAssociated {
		for _, a := range src.AssociatedImages() {
			if omeAssocName(string(a.Type())) == "" {
				continue
			}
			if err := writeOneAssociated(w, a); err != nil {
				return fmt.Errorf("write associated %s: %w", a.Type(), err)
			}
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	closed = true
	reportWrote(output, start)
	return nil
}

// cropToCOGWSI is implemented in the next task.
func cropToCOGWSI(ctx context.Context, src *opentile.Slide, input, output string, l0 []byte, l0W, l0H, nLevels, quality, workers int, order tileorder.OrderStrategy, bigtiffFlag string, noAssociated bool, start time.Time) error {
	return fmt.Errorf("crop cog-wsi: implemented in the next task")
}
