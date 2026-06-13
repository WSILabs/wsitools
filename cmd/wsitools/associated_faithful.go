package main

import (
	"errors"
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// errSkipAssociated marks an associated image that can't be emitted (decode
// failure on the non-faithful fallback); callers log + skip rather than fail.
var errSkipAssociated = errors.New("associated image skipped")

// faithfulCOGWSISpec builds a cogwsiwriter.AssociatedSpec that re-emits a's
// associated image byte-faithfully when opentile exposes its source form
// (verbatim strips + Predictor/JPEGTables), else decodes and re-encodes as a
// self-contained LZW strip (no predictor).
func faithfulCOGWSISpec(a source.AssociatedImage) (cogwsiwriter.AssociatedSpec, error) {
	t := a.Type()
	if src, ok := a.Source(); ok {
		sz := a.Size()
		return cogwsiwriter.AssociatedSpec{
			Type: t, Width: uint32(sz.X), Height: uint32(sz.Y),
			Compression:     mapCompressionForOutput(a.Compression()),
			Photometric:     uint16(src.Photometric),
			SamplesPerPixel: uint16(src.Samples),
			Strips:          src.Strips,
			Predictor:       uint16(src.Predictor),
			JPEGTables:      src.JPEGTables,
			RowsPerStrip:    uint32(src.RowsPerStrip),
		}, nil
	}
	di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
	if err != nil {
		return cogwsiwriter.AssociatedSpec{}, fmt.Errorf("%w: %s decode: %v", errSkipAssociated, t, err)
	}
	rgb := packTightRGB(di)
	return cogwsiwriter.AssociatedSpec{
		Type: t, Width: uint32(di.Width), Height: uint32(di.Height),
		Compression: tiff.CompressionLZW, Photometric: 2, SamplesPerPixel: 3,
		Strips: [][]byte{encodeLZW(rgb)}, RowsPerStrip: uint32(di.Height),
	}, nil
}

// faithfulStrippedSpec is the streamwriter.StrippedSpec equivalent (SVS /
// generic-TIFF / OME-TIFF). The caller's flavor logic may override
// WSIImageType/NewSubfileType; this builder fills geometry + codec faithfully
// with sensible defaults.
func faithfulStrippedSpec(a source.AssociatedImage) (streamwriter.StrippedSpec, error) {
	t := a.Type()
	if src, ok := a.Source(); ok {
		sz := a.Size()
		return streamwriter.StrippedSpec{
			Width: uint32(sz.X), Height: uint32(sz.Y),
			RowsPerStrip: uint32(src.RowsPerStrip), SamplesPerPixel: uint16(src.Samples),
			Photometric: uint16(src.Photometric), Compression: mapCompressionForOutput(a.Compression()),
			Strips: src.Strips, Predictor: uint16(src.Predictor), JPEGTables: src.JPEGTables,
			WSIImageType: t, NewSubfileType: 1,
		}, nil
	}
	di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
	if err != nil {
		return streamwriter.StrippedSpec{}, fmt.Errorf("%w: %s decode: %v", errSkipAssociated, t, err)
	}
	rgb := packTightRGB(di)
	return streamwriter.StrippedSpec{
		Width: uint32(di.Width), Height: uint32(di.Height),
		RowsPerStrip: uint32(di.Height), SamplesPerPixel: 3, Photometric: 2,
		Compression: tiff.CompressionLZW, Strips: [][]byte{encodeLZW(rgb)},
		WSIImageType: t, NewSubfileType: 1,
	}, nil
}

// faithfulCOGWSISpecOT is the opentile-direct equivalent of faithfulCOGWSISpec
// for the *opentile.Slide paths (convert --factor cog-wsi). It re-emits a's
// associated image byte-faithfully when opentile exposes its source form via
// AssociatedSourceOf (verbatim strips + Predictor/JPEGTables), else decodes and
// re-encodes as a self-contained LZW strip (no predictor).
func faithfulCOGWSISpecOT(slide *opentile.Slide, a opentile.AssociatedImage) (cogwsiwriter.AssociatedSpec, error) {
	t := a.Type()
	if src, ok := slide.AssociatedSourceOf(a); ok {
		sz := a.Size()
		return cogwsiwriter.AssociatedSpec{
			Type: t, Width: uint32(sz.W), Height: uint32(sz.H),
			Compression:     opentile.CompressionToTIFFTag(a.Compression()),
			Photometric:     uint16(src.Photometric),
			SamplesPerPixel: uint16(src.Samples),
			Strips:          src.Strips,
			Predictor:       uint16(src.Predictor),
			JPEGTables:      src.JPEGTables,
			RowsPerStrip:    uint32(src.RowsPerStrip),
		}, nil
	}
	di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
	if err != nil {
		return cogwsiwriter.AssociatedSpec{}, fmt.Errorf("%w: %s decode: %v", errSkipAssociated, t, err)
	}
	rgb := packTightRGB(di)
	return cogwsiwriter.AssociatedSpec{
		Type: t, Width: uint32(di.Width), Height: uint32(di.Height),
		Compression: tiff.CompressionLZW, Photometric: 2, SamplesPerPixel: 3,
		Strips: [][]byte{encodeLZW(rgb)}, RowsPerStrip: uint32(di.Height),
	}, nil
}

// faithfulStrippedSpecOT is the opentile-direct equivalent of
// faithfulStrippedSpec for the *opentile.Slide paths (convert --factor
// svs|tiff|ome-tiff). It fills geometry + codec + strips faithfully; the
// caller overlays flavor (NewSubfileType / WSIImageType / ExtraTags).
func faithfulStrippedSpecOT(slide *opentile.Slide, a opentile.AssociatedImage) (streamwriter.StrippedSpec, error) {
	t := a.Type()
	if src, ok := slide.AssociatedSourceOf(a); ok {
		sz := a.Size()
		return streamwriter.StrippedSpec{
			Width: uint32(sz.W), Height: uint32(sz.H),
			RowsPerStrip: uint32(src.RowsPerStrip), SamplesPerPixel: uint16(src.Samples),
			Photometric: uint16(src.Photometric), Compression: opentile.CompressionToTIFFTag(a.Compression()),
			Strips: src.Strips, Predictor: uint16(src.Predictor), JPEGTables: src.JPEGTables,
			BitsPerSample: []uint16{8, 8, 8}, WSIImageType: t, NewSubfileType: 1,
		}, nil
	}
	di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
	if err != nil {
		return streamwriter.StrippedSpec{}, fmt.Errorf("%w: %s decode: %v", errSkipAssociated, t, err)
	}
	rgb := packTightRGB(di)
	return streamwriter.StrippedSpec{
		Width: uint32(di.Width), Height: uint32(di.Height),
		RowsPerStrip: uint32(di.Height), SamplesPerPixel: 3, Photometric: 2,
		Compression: tiff.CompressionLZW, Strips: [][]byte{encodeLZW(rgb)},
		BitsPerSample: []uint16{8, 8, 8}, WSIImageType: t, NewSubfileType: 1,
	}, nil
}
