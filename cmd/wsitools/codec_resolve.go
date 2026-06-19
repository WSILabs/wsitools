package main

import (
	"strconv"

	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
)

// resolveTransformCodec maps --codec/--quality to an encoder factory + knobs for
// the crop/downsample engine path. Empty codecName ⇒ jpeg at fallbackQ. Returns
// the codec name actually used (for the DICOM frame encoder + ImageDescription).
func resolveTransformCodec(codecName, quality string, fallbackQ int) (codec.EncoderFactory, map[string]string, string, error) {
	if codecName == "" {
		return jpegcodec.Factory{}, map[string]string{"q": strconv.Itoa(fallbackQ)}, "jpeg", nil
	}
	knobs, err := parseQualityKnobs(quality)
	if err != nil {
		return nil, nil, "", err
	}
	fac, err := codec.Lookup(codecName)
	if err != nil {
		return nil, nil, "", err
	}
	return fac, knobs, codecName, nil
}
