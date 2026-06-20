package main

import (
	"strconv"

	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
)

// codecDefaultKnobs is wsitools' single source of truth for each codec's default
// encode knobs when --quality is absent. Values start from the codecs' own encoder
// defaults (q=85 for the q-scale codecs; jpegxl's native "visually lossless"
// distance 1.0). A forced uniform "q" would mis-set codecs whose quality scale
// isn't 1–100 (notably jpegxl, where q=90 maps to a MORE-lossy distance 1.5).
func codecDefaultKnobs(codec string) map[string]string {
	switch codec {
	case "jpegxl":
		return map[string]string{"distance": "1.0"}
	case "png":
		return map[string]string{}
	default: // jpeg, jpeg2000, htj2k, avif, webp, and unknown
		return map[string]string{"q": "85"}
	}
}

// qFromKnobs extracts a 1–100 integer quality from knobs for metadata that needs a
// number (the Aperio "Q=" token). Returns 85 when "q" is absent or invalid.
func qFromKnobs(knobs map[string]string) int {
	if v, ok := knobs["q"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			return n
		}
	}
	return 85
}

// resolveTransformCodec maps --codec/--quality to an encoder factory + knobs for
// the crop/downsample engine path. Returns the codec name actually used (for the
// DICOM frame encoder + ImageDescription).
//
// The default quality is the caller's fallbackQ (e.g. 90 for downsample / non-SVS
// crop) — applied regardless of codec name, so an absent --quality keeps the
// transform path's historical default. An explicit --quality string overrides it
// (and carries any codec-specific k=v knobs). An empty or "jpeg" codecName both
// select the jpeg encoder.
func resolveTransformCodec(codecName, quality string, fallbackQ int) (codec.EncoderFactory, map[string]string, string, error) {
	knobs := map[string]string{"q": strconv.Itoa(fallbackQ)}
	if quality != "" {
		parsed, err := parseQualityKnobs(quality)
		if err != nil {
			return nil, nil, "", err
		}
		knobs = parsed
	}
	if codecName == "" || codecName == "jpeg" {
		return jpegcodec.Factory{}, knobs, "jpeg", nil
	}
	fac, err := codec.Lookup(codecName)
	if err != nil {
		return nil, nil, "", err
	}
	return fac, knobs, codecName, nil
}
