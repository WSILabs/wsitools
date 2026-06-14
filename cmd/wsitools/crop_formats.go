package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
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

// cropEmitParams carries everything a per-format crop emitter needs. The
// lossless, srcL0 and stx0/sty0/outTilesX/outTilesY tile-block fields are used
// only on the lossless path; re-encode ignores them.
type cropEmitParams struct {
	ctx          context.Context
	src          *opentile.Slide
	srcL0        *opentile.Level
	input        string
	output       string
	l0           []byte
	l0W, l0H     int
	nLevels      int
	quality      int
	workers      int
	order        tileorder.OrderStrategy
	bigtiffFlag  string
	noAssociated bool
	lossless     bool
	stx0, sty0   int
	outTilesX    int
	outTilesY    int
	start        time.Time
}

func cropToTIFF(p cropEmitParams) error {
	mppX, mppY, mag := cropSourceScale(p.input, p.src)
	bigtiffMode := streamwriterBigTIFF(p.bigtiffFlag, p.l0W, p.l0H)

	codec := "jpeg"
	if p.lossless {
		codec = "verbatim" // L0 tiles copied byte-identical; lower levels re-encoded JPEG
	}
	imageDesc := fmt.Sprintf("wsi-tools/%s crop source=%s codec=%s mpp=%v mag=%vx", Version, p.src.Format(), codec, mppX, mag)
	w, err := streamwriter.Create(p.output, streamwriter.Options{
		BigTIFF:          bigtiffMode,
		ImageDescription: imageDesc,
		ToolsVersion:     Version,
		SourceFormat:     string(p.src.Format()),
		FormatName:       "tiff",
		AcceptedOrders:   acceptedOrdersForFormat("tiff"),
		DefaultOrder:     p.order,
		MPPX:             mppX,
		MPPY:             mppY,
		Magnification:    mag,
		ICCProfile:       p.src.ICCProfile(),
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
	if p.lossless {
		if err := writeLosslessL0(w, p.srcL0, p.stx0, p.sty0, p.outTilesX, p.outTilesY, p.l0W, p.l0H); err != nil {
			return fmt.Errorf("write lossless L0: %w", err)
		}
		if p.nLevels > 1 {
			l1, l1W, l1H, err := halveRaster(p.l0, p.l0W, p.l0H)
			if err != nil {
				return fmt.Errorf("halve L0→L1: %w", err)
			}
			if err := buildPyramidFromRaster(p.ctx, w, l1, l1W, l1H, p.nLevels-1, p.quality, p.workers, nil); err != nil {
				return fmt.Errorf("build pyramid: %w", err)
			}
		}
	} else {
		if err := buildPyramidFromRaster(p.ctx, w, p.l0, p.l0W, p.l0H, p.nLevels, p.quality, p.workers, nil); err != nil {
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
	if !p.noAssociated {
		for _, a := range p.src.AssociatedImages() {
			if a.Type() == opentile.AssociatedThumbnail {
				if err := regenCropThumbnail(w, p.l0, p.l0W, p.l0H, p.quality); err != nil {
					return fmt.Errorf("regenerate thumbnail: %w", err)
				}
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
	reportWrote(p.output, p.start)
	return nil
}

func cropToOMETIFF(p cropEmitParams) error {
	mppX, mppY, mag := cropSourceScale(p.input, p.src)
	bigtiffMode := streamwriterBigTIFF(p.bigtiffFlag, p.l0W, p.l0H)

	ttw, tth := thumbDims(p.l0W, p.l0H, thumbLongSide)
	var omeAssocs []OMEAssoc
	if !p.noAssociated {
		for _, a := range p.src.AssociatedImages() {
			name := omeAssocName(string(a.Type()))
			if name == "" {
				continue
			}
			aw, ah := uint32(a.Size().W), uint32(a.Size().H)
			if a.Type() == opentile.AssociatedThumbnail {
				aw, ah = uint32(ttw), uint32(tth) // regenerated dims must match the written IFD
			}
			omeAssocs = append(omeAssocs, OMEAssoc{Name: name, W: aw, H: ah})
		}
	}
	omeXML := SyntheticOMEDescriptionWithMag(uint32(p.l0W), uint32(p.l0H), mppX, mppY, mag, "Image", string(p.src.Format()), omeAssocs)

	w, err := streamwriter.Create(p.output, streamwriter.Options{
		BigTIFF:              bigtiffMode,
		ImageDescription:     omeXML,
		ToolsVersion:         Version,
		SourceFormat:         string(p.src.Format()),
		FormatName:           "ome-tiff",
		AcceptedOrders:       acceptedOrdersForFormat("ome-tiff"),
		DefaultOrder:         p.order,
		MPPX:                 mppX,
		MPPY:                 mppY,
		Magnification:        mag,
		ICCProfile:           p.src.ICCProfile(),
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
	if p.lossless {
		if err := writeLosslessL0(w, p.srcL0, p.stx0, p.sty0, p.outTilesX, p.outTilesY, p.l0W, p.l0H); err != nil {
			return fmt.Errorf("write lossless L0: %w", err)
		}
		if p.nLevels > 1 {
			l1, l1W, l1H, err := halveRaster(p.l0, p.l0W, p.l0H)
			if err != nil {
				return fmt.Errorf("halve L0→L1: %w", err)
			}
			if err := buildPyramidFromRaster(p.ctx, w, l1, l1W, l1H, p.nLevels-1, p.quality, p.workers, nil); err != nil {
				return fmt.Errorf("build pyramid: %w", err)
			}
		}
	} else {
		if err := buildPyramidFromRaster(p.ctx, w, p.l0, p.l0W, p.l0H, p.nLevels, p.quality, p.workers, nil); err != nil {
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
	if !p.noAssociated {
		for _, a := range p.src.AssociatedImages() {
			if omeAssocName(string(a.Type())) == "" {
				continue
			}
			if a.Type() == opentile.AssociatedThumbnail {
				if err := regenCropThumbnail(w, p.l0, p.l0W, p.l0H, p.quality); err != nil {
					return fmt.Errorf("regenerate thumbnail: %w", err)
				}
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
	reportWrote(p.output, p.start)
	return nil
}

// cropToCOGWSI writes the cropped L0 + rebuilt pyramid as a COG-WSI TIFF,
// mirroring downsampleToCOGWSI with an exact-extent cropped L0 and preserved
// MPP/magnification.
func cropToCOGWSI(p cropEmitParams) error {
	mppX, mppY, mag := cropSourceScale(p.input, p.src)

	bigTIFFMode, err := parseBigTIFFFlag(p.bigtiffFlag)
	if err != nil {
		if p.bigtiffFlag == "" {
			bigTIFFMode = cogwsiwriter.BigTIFFAuto
		} else {
			return err
		}
	}

	w, err := cogwsiwriter.Create(p.output, cogwsiwriter.Options{
		BigTIFF:      bigTIFFMode,
		ToolsVersion: Version,
		DefaultOrder: p.order,
		Metadata: cogwsiwriter.Metadata{
			MPPX:            mppX,
			MPPY:            mppY,
			Magnification:   mag,
			ICCProfile:      p.src.ICCProfile(),
			SourceFormat:    string(p.src.Format()),
			SourceImageDesc: fmt.Sprintf("wsitools/%s crop source=%s", Version, p.src.Format()),
		},
	})
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	aborted := false
	defer func() {
		if aborted {
			w.Abort()
		}
	}()

	if p.lossless {
		if err := writeLosslessL0COGWSI(w, p.srcL0, p.stx0, p.sty0, p.outTilesX, p.outTilesY, p.l0W, p.l0H); err != nil {
			aborted = true
			return fmt.Errorf("write lossless L0: %w", err)
		}
		if p.nLevels > 1 {
			l1, l1W, l1H, err := halveRaster(p.l0, p.l0W, p.l0H)
			if err != nil {
				aborted = true
				return fmt.Errorf("halve L0→L1: %w", err)
			}
			if err := buildPyramidFromRasterCOGWSI(p.ctx, w, l1, l1W, l1H, p.nLevels-1, p.quality); err != nil {
				aborted = true
				return fmt.Errorf("build pyramid: %w", err)
			}
		}
	} else {
		if err := buildPyramidFromRasterCOGWSI(p.ctx, w, p.l0, p.l0W, p.l0H, p.nLevels, p.quality); err != nil {
			aborted = true
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
	if !p.noAssociated {
		for _, a := range p.src.AssociatedImages() {
			if a.Type() == opentile.AssociatedThumbnail {
				if err := regenCropThumbnailCOGWSI(w, p.l0, p.l0W, p.l0H, p.quality); err != nil {
					aborted = true
					return fmt.Errorf("regenerate thumbnail: %w", err)
				}
				continue
			}
			spec, err := faithfulCOGWSISpecOT(a)
			if err != nil {
				if errors.Is(err, errSkipAssociated) {
					slog.Warn("skipping associated image", "type", a.Type(), "reason", err)
					continue
				}
				aborted = true
				return err
			}
			if err := w.AddAssociated(spec); err != nil {
				aborted = true
				return fmt.Errorf("add associated %s: %w", a.Type(), err)
			}
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	reportWrote(p.output, p.start)
	return nil
}
