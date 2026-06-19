package main

import (
	"strconv"

	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
)

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
