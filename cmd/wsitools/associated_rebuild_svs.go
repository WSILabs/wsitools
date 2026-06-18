package main

import (
	qualityjpeg "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
)

// rebuildSVS re-finalizes src as an SVS at outPath with the associated-edit plan
// applied, tile-copying the pyramid verbatim (pixel-identical). Fallback for SVS
// associated remove/replace when the in-place splice can't handle the layout —
// specifically the thumbnail, which Aperio stores at IFD 1 (before the tiled
// pyramid). writeTIFFTileCopy now re-emits the thumbnail at IFD 1, so the rebuilt
// file classifies it correctly. The Aperio L0 ImageDescription is carried
// verbatim so the output re-detects as SVS; ImageDepth/YCbCrSubSampling mirror
// runConvertTIFFTileCopy's svs branch.
func rebuildSVS(src source.Source, outPath string, plan omeEditPlan, fsync bool) error {
	opts, err := baseRebuildOpts(src, "svs")
	if err != nil {
		return err
	}
	opts.ImageDepth = 1
	l0 := src.Levels()[0]
	if compressionTagFor(l0.Compression()) == tiff.CompressionJPEG {
		buf := make([]byte, l0.TileMaxSize())
		if n, terr := l0.TileInto(0, 0, buf); terr == nil {
			if h, v, ok := qualityjpeg.LumaSampling(buf[:n]); ok {
				opts.YCbCrSubSampling = []uint16{h, v}
			}
		}
	}
	l0Desc := src.SourceImageDescription()
	return finalizeRebuild(src, outPath, "svs", l0Desc, false /*omeSynthetic*/, opts, plan, fsync)
}
