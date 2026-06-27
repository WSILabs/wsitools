package main

import (
	"fmt"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/source"
)

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
func tileCopyEligible(target, codecFlag string, src source.Compression, srcNativelyTiled bool) bool {
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
