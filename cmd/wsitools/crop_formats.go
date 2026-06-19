package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/derivedsource"
	"github.com/wsilabs/wsitools/internal/dicomwriter"
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
	ex, ey       int
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
		rect := opentile.Region{Origin: opentile.Point{X: p.ex, Y: p.ey}, Size: opentile.Size{W: p.l0W, H: p.l0H}}
		var postL0Hook func() error
		if !p.noAssociated {
			postL0Hook = func() error {
				jpegBytes, tw, th, terr := streamCropThumbnail(p.src, rect, p.l0W, p.l0H, p.quality)
				if terr != nil {
					return terr
				}
				return addCropThumbnailStripped(w, jpegBytes, tw, th)
			}
		}
		if err := buildEnginePyramid(p.ctx, p.src, w, rect, opentile.Size{W: p.l0W, H: p.l0H}, p.quality, p.workers, postL0Hook); err != nil {
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
	if !p.noAssociated {
		for _, a := range p.src.AssociatedImages() {
			if a.Type() == opentile.AssociatedThumbnail {
				// Lossy already emitted the thumbnail via the post-L0 hook; only
				// the lossless (raster) path regenerates it here.
				if p.lossless {
					if err := regenCropThumbnail(w, p.l0, p.l0W, p.l0H, p.quality); err != nil {
						return fmt.Errorf("regenerate thumbnail: %w", err)
					}
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
		rect := opentile.Region{Origin: opentile.Point{X: p.ex, Y: p.ey}, Size: opentile.Size{W: p.l0W, H: p.l0H}}
		var postL0Hook func() error
		if !p.noAssociated {
			postL0Hook = func() error {
				jpegBytes, tw, th, terr := streamCropThumbnail(p.src, rect, p.l0W, p.l0H, p.quality)
				if terr != nil {
					return terr
				}
				return addCropThumbnailStripped(w, jpegBytes, tw, th)
			}
		}
		if err := buildEnginePyramid(p.ctx, p.src, w, rect, opentile.Size{W: p.l0W, H: p.l0H}, p.quality, p.workers, postL0Hook); err != nil {
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
	if !p.noAssociated {
		for _, a := range p.src.AssociatedImages() {
			if omeAssocName(string(a.Type())) == "" {
				continue
			}
			if a.Type() == opentile.AssociatedThumbnail {
				// Lossy already emitted the thumbnail via the post-L0 hook; only
				// the lossless (raster) path regenerates it here.
				if p.lossless {
					if err := regenCropThumbnail(w, p.l0, p.l0W, p.l0H, p.quality); err != nil {
						return fmt.Errorf("regenerate thumbnail: %w", err)
					}
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
		rect := opentile.Region{Origin: opentile.Point{X: p.ex, Y: p.ey}, Size: opentile.Size{W: p.l0W, H: p.l0H}}
		if err := buildEnginePyramidCOGWSI(p.ctx, p.src, w, rect, opentile.Size{W: p.l0W, H: p.l0H}, p.quality, p.workers); err != nil {
			aborted = true
			return fmt.Errorf("build pyramid: %w", err)
		}
	}
	if !p.noAssociated {
		for _, a := range p.src.AssociatedImages() {
			if a.Type() == opentile.AssociatedThumbnail {
				if p.lossless {
					// Lossless holds the decoded crop raster; downscale it.
					if err := regenCropThumbnailCOGWSI(w, p.l0, p.l0W, p.l0H, p.quality); err != nil {
						aborted = true
						return fmt.Errorf("regenerate thumbnail: %w", err)
					}
				} else {
					// Lossy: read+downscale the crop rect from the source (no raster).
					rect := opentile.Region{Origin: opentile.Point{X: p.ex, Y: p.ey}, Size: opentile.Size{W: p.l0W, H: p.l0H}}
					jpegBytes, tw, th, terr := streamCropThumbnail(p.src, rect, p.l0W, p.l0H, p.quality)
					if terr != nil {
						aborted = true
						return fmt.Errorf("regenerate thumbnail: %w", terr)
					}
					if err := w.AddAssociated(cogwsiwriter.AssociatedSpec{
						Type:            tiff.WSIImageTypeThumbnail,
						Width:           uint32(tw),
						Height:          uint32(th),
						Compression:     tiff.CompressionJPEG,
						Photometric:     6,
						BitsPerSample:   []uint16{8, 8, 8},
						SamplesPerPixel: 3,
						Bytes:           jpegBytes,
						RowsPerStrip:    uint32(th),
					}); err != nil {
						aborted = true
						return fmt.Errorf("add thumbnail: %w", err)
					}
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

// cropToDICOM emits a cropped DICOM-WSM pyramid. Default (p.lossless == false):
// the cropped L0 raster + box-halved lowers are JPEG re-encoded. Lossless: L0 is
// a passthrough over the source's verbatim frames for the tile-snapped region
// (p.stx0/p.sty0 offset, p.outTilesX/Y grid), and lower levels are re-encoded
// from the decoded snapped raster. Crop preserves L0 MPP/magnification, so the
// derived source's metadata is the source's unchanged.
func cropToDICOM(p cropEmitParams) error {
	src := source.FromSlide(p.src, p.input)
	md := src.Metadata()
	assoc := src.Associated()
	if p.noAssociated {
		assoc = nil
	}

	if p.lossless {
		// Replace the whole-slide thumbnail with one rendered from the crop L0
		// (the snapped region) so the emitted thumbnail reflects the crop, not the
		// full slide. label/macro/overview pass through (they describe the whole
		// physical slide). The lossless path holds the cropped raster (p.l0).
		if !p.noAssociated {
			var rerr error
			assoc, rerr = regenCropThumbnailAssoc(assoc, p.l0, p.l0W, p.l0H, p.quality)
			if rerr != nil {
				return fmt.Errorf("regenerate crop thumbnail: %w", rerr)
			}
		}
		comp := src.Levels()[0].Compression()
		if comp != source.CompressionJPEG && comp != source.CompressionJPEG2000 {
			return fmt.Errorf("--lossless into DICOM needs JPEG or JPEG 2000 source frames; got %s", comp)
		}
		ds, err := derivedsource.WithLosslessL0(
			src.Levels()[0], p.stx0, p.sty0, p.outTilesX, p.outTilesY, p.l0W, p.l0H,
			p.l0, p.nLevels, outputTileSize, p.quality, p.workers, src.Format(), md, assoc)
		if err != nil {
			return fmt.Errorf("build derived source: %w", err)
		}
		if err := emitDICOM(ds, dicomwriter.Options{
			Associated: !p.noAssociated,
			// Crop extracts a spatial region at full resolution: ImageType[3]=NONE,
			// not RESAMPLED (which downsample uses to signal spatial reduction).
			L0ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
		}, p.output, cropForce); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", p.output)
		return nil
	}

	// Lossy: stream the crop rect through the retile engine into a spool, then
	// emit DICOM. No cropped raster is materialized (p.l0 is nil here).
	rect := opentile.Region{Origin: opentile.Point{X: p.ex, Y: p.ey}, Size: opentile.Size{W: p.l0W, H: p.l0H}}
	// Regenerate the crop thumbnail from a small streaming read (no p.l0 raster).
	if !p.noAssociated {
		jpegBytes, tw, th, terr := streamCropThumbnail(p.src, rect, p.l0W, p.l0H, p.quality)
		if terr != nil {
			return fmt.Errorf("regenerate crop thumbnail: %w", terr)
		}
		assoc = replaceThumbnailAssoc(assoc, jpegBytes, tw, th)
	}
	return runDICOMEngine(p.ctx, p.src, rect, opentile.Size{W: p.l0W, H: p.l0H}, "jpeg", p.quality, p.workers, src.Format(), md, assoc, dicomwriter.Options{
		Associated:  !p.noAssociated,
		L0ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"},
	}, p.output, cropForce)
}
