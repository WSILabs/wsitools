package main

import "github.com/wsilabs/wsitools/internal/source"

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
