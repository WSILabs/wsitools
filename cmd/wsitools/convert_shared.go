package main

import (
	"fmt"
	"strconv"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
	qualityjpeg "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"
	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/source"
)

// engineYCbCrSubSampling returns the YCbCrSubSampling(530) tag value [h,v] that
// matches what the engine re-encode will produce for `slide`: the source's own
// subsampling for JPEG output (which buildEnginePyramid injects into the
// encoder). nil for non-JPEG output (RGB tiles carry no such tag). Lets the
// crop/downsample/--factor streamwriters emit a tag that agrees with the encoded
// tiles, instead of leaving it absent (defaulting to 4:2:0) so readers must
// auto-correct from the JPEG SOF and log a warning.
func engineYCbCrSubSampling(fac codec.EncoderFactory, knobs map[string]string, slide *opentile.Slide) []uint16 {
	if fac.Name() != "jpeg" {
		return nil
	}
	if sub, ok := encoderChromaSubsampling(fac, withSourceSubsampling(knobs, fac.Name(), slide)); ok {
		return sub
	}
	return nil
}

// sourceQualityEstimate returns the estimated quality (0–100) of the source L0's
// tiles, or 0 if unknown (lossless source, no inspector, or unreadable). Used to
// let a re-encode honor a source whose quality exceeds our default.
func sourceQualityEstimate(slide *opentile.Slide) int {
	lvls := slide.Pyramid(0).Levels
	if len(lvls) == 0 {
		return 0
	}
	l0 := lvls[0]
	insp, ok := quality.For(l0.Compression)
	if !ok {
		return 0
	}
	b, err := l0.Tile(0, 0)
	if err != nil {
		return 0
	}
	info, err := insp.Inspect(b)
	if err != nil {
		return 0
	}
	return info.QualityEstimate
}

// sourceQualityEstimateFor opens input just to estimate its L0 quality (for the
// downsample / --factor paths, which don't hold the slide when resolving knobs).
func sourceQualityEstimateFor(input string) int {
	sl, err := opentile.OpenFile(input)
	if err != nil {
		return 0
	}
	defer sl.Close()
	return sourceQualityEstimate(sl)
}

// withSourceQualityFloor treats the resolved "q" knob as a FLOOR: if the source's
// own estimated quality is higher, raise "q" to it so re-encoding a high-quality
// slide doesn't needlessly degrade it. Never lowers "q". Caller gates this on the
// user NOT having set --quality explicitly.
func withSourceQualityFloor(knobs map[string]string, slide *opentile.Slide) map[string]string {
	cur, err := strconv.Atoi(knobs["q"])
	if err != nil {
		return knobs
	}
	srcQ := sourceQualityEstimate(slide)
	if srcQ <= cur {
		return knobs
	}
	out := make(map[string]string, len(knobs))
	for k, v := range knobs {
		out[k] = v
	}
	out["q"] = strconv.Itoa(srcQ)
	return out
}

// preservedSourceCodec returns the source's own codec name when it has a
// wsitools encoder, so a single-axis transform (downsample / --factor / crop,
// which don't expose --codec) keeps the source codec instead of forcing JPEG.
// Falls back to "jpeg" for source codecs with no encoder (LZW / uncompressed /
// Deflate). Opening errors also fall back to "jpeg" (the caller re-opens and
// surfaces a real error).
func preservedSourceCodec(input string) string {
	src, err := source.Open(input)
	if err != nil {
		return "jpeg"
	}
	defer src.Close()
	if len(src.Levels()) == 0 {
		return "jpeg"
	}
	if c, err := reencodeCodecFor(src.Levels()[0].Compression(), ""); err == nil {
		return c
	}
	return "jpeg"
}

// sourceJPEGSubsampling returns the chroma-subsampling knob ("444"/"422"/"440"/
// "420") matching the source L0's JPEG tiles, or "" if the source isn't JPEG or
// can't be sampled. Lets a JPEG re-encode honor the source subsampling instead
// of forcing 4:2:0.
func sourceJPEGSubsampling(slide *opentile.Slide) string {
	lvls := slide.Pyramid(0).Levels
	if len(lvls) == 0 {
		return ""
	}
	b, err := lvls[0].Tile(0, 0)
	if err != nil {
		return ""
	}
	h, v, ok := qualityjpeg.LumaSampling(b)
	if !ok {
		return ""
	}
	switch {
	case h == 1 && v == 1:
		return "444"
	case h == 2 && v == 1:
		return "422"
	case h == 1 && v == 2:
		return "440"
	case h == 2 && v == 2:
		return "420"
	}
	return ""
}

// withSourceSubsampling returns knobs (a copy) with the "subsampling" knob set
// from the source L0 when the output codec is JPEG and the user hasn't set it —
// so a re-encode matches the source's chroma subsampling. A no-op for non-JPEG
// output or a non-JPEG / unsampleable source.
func withSourceSubsampling(knobs map[string]string, facName string, slide *opentile.Slide) map[string]string {
	if facName != "jpeg" || knobs["subsampling"] != "" {
		return knobs
	}
	ss := sourceJPEGSubsampling(slide)
	if ss == "" {
		return knobs
	}
	out := make(map[string]string, len(knobs)+1)
	for k, v := range knobs {
		out[k] = v
	}
	out["subsampling"] = ss
	return out
}

// resolveTileSize returns the output tile edge: the user's --tile-size when >0,
// else the source level-0 tile width, else 256 when the source has no usable
// square tile geometry.
func resolveTileSize(srcL0TileW, flag int) int {
	if flag > 0 {
		return flag
	}
	if srcL0TileW > 0 {
		return srcL0TileW
	}
	return 256
}

// reencodeCodecFor picks the codec for a forced re-encode (e.g. --tile-size
// differs from the source tiling). An explicit codecFlag always wins. Otherwise
// the source's own codec is preserved — source.Compression.String() yields the
// codec-registry name. If the source codec has no wsitools encoder
// (LZW/Deflate/None/…), it errors asking for an explicit --codec.
func reencodeCodecFor(src source.Compression, codecFlag string) (string, error) {
	if codecFlag != "" {
		return codecFlag, nil
	}
	name := src.String()
	if _, err := codec.Lookup(name); err != nil {
		return "", fmt.Errorf("re-encoding required (e.g. --tile-size differs from source) but no encoder for source codec %q; pass --codec", name)
	}
	return name, nil
}

// acceptedOrdersForFormat returns the per-format whitelist of tile-order names.
// nil = permissive (all registered strategies allowed).
func acceptedOrdersForFormat(format string) []string {
	switch format {
	case "svs":
		return []string{"row-major"}
	case "tiff", "ome-tiff":
		return nil // permissive
	}
	return nil
}

// nativelyTiled returns true if the source format is natively tile-based
// (not strip-synthesized). Striped formats: NDPI, OME-OneFrame. opentile-go's
// readers synthesize tile geometry for striped sources; tile-copy still
// works on synthesized tiles (the bytes are reproducible standalone
// JPEGs), but our "bit-exact" guarantee applies only to natively-tiled
// sources. Striped sources take the re-encode path.
func nativelyTiled(format string) bool {
	switch format {
	case "ndpi", "ome-tiff-oneframe":
		return false
	}
	return true
}

// targetAcceptsCodec returns true iff the target container can hold
// tiles in the given source codec verbatim (i.e. tile-copy is
// representable in the target's TIFF tag set).
func targetAcceptsCodec(target string, c source.Compression) bool {
	switch target {
	case "cog-wsi", "tiff", "ome-tiff":
		switch c {
		case source.CompressionJPEG, source.CompressionWebP,
			source.CompressionJPEG2000, source.CompressionAVIF,
			source.CompressionJPEGXL, source.CompressionHTJ2K:
			return true
		}
	case "svs":
		return c == source.CompressionJPEG
	}
	return false
}

// tileCopyEligible returns true iff the convert request can use the
// bit-exact tile-copy fast path. dzi/szi targets always re-encode
// (overlap + extra pyramid levels make tile-copy impossible).
func tileCopyEligible(target, codecFlag string, src source.Compression, srcNativelyTiled bool, tileSize, srcL0TileW int) bool {
	// A verbatim tile-copy cannot change tile size; a --tile-size that differs
	// from the source forces a re-encode, so disqualify the copy.
	if tileSize > 0 && tileSize != srcL0TileW {
		return false
	}
	if target == "dzi" || target == "szi" {
		return false
	}
	if codecFlag != "" {
		return false
	}
	if !srcNativelyTiled {
		return false
	}
	return targetAcceptsCodec(target, src)
}
