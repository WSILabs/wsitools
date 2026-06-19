package main

import "fmt"

// resolveDZIFormat picks the effective DZI/SZI tile codec. --codec is the
// canonical flag; --dzi-format is the deprecated alias. When --codec is set it
// wins; otherwise --dzi-format (default "jpeg") applies. The result must be a
// Deep Zoom tile format (jpeg or png) — browser deep-zoom viewers render nothing
// else.
func resolveDZIFormat(codec string, codecSet bool, dziFormat string, dziFormatSet bool) (string, error) {
	format := dziFormat
	if format == "" {
		format = "jpeg"
	}
	if codecSet {
		format = codec
	}
	switch format {
	case "jpeg", "png":
		return format, nil
	default:
		return "", fmt.Errorf("DZI/SZI tiles must be jpeg or png, got %q", format)
	}
}
