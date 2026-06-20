package main

import (
	"fmt"
	"strings"
)

// containerCaps describes a container's codec support. Codecs in neither set are
// unsupported (no encoder / no container slot).
type containerCaps struct {
	conformant    []string // wsitools writes it AND opentile reads it back
	nonconformant []string // writable bytes, NOT readable as this format
	redirect      string   // hint appended to an unsupported-codec error
}

// containerCapabilities is the single source of truth for codec×container support.
// Values are VERIFIED by the Phase-2 round-trip matrix (see the plan/spec).
// Forward-looking: this is the seam to delegate to an opentile capability API.
func containerCapabilities(container string) containerCaps {
	switch container {
	case "tiff":
		return containerCaps{
			conformant:    []string{"jpeg", "jpeg2000", "htj2k", "avif", "webp"},
			nonconformant: []string{"jpegxl"},
		}
	case "svs":
		return containerCaps{
			conformant:    []string{"jpeg", "jpeg2000"},
			nonconformant: []string{"htj2k", "avif", "webp", "jpegxl"},
		}
	case "ome-tiff":
		return containerCaps{
			conformant:    []string{"jpeg", "jpeg2000"},
			nonconformant: []string{"htj2k", "avif", "webp", "jpegxl"},
		}
	case "cog-wsi":
		return containerCaps{
			conformant: []string{"jpeg", "jpeg2000", "htj2k", "avif", "webp", "jpegxl"},
		}
	case "dicom":
		return containerCaps{
			conformant: []string{"jpeg", "jpeg2000", "htj2k"},
			redirect:   "DICOM has no transfer syntax for that codec; use jpeg, jpeg2000, or htj2k",
		}
	case "dzi", "szi":
		return containerCaps{
			conformant: []string{"jpeg", "png"},
			redirect:   "Deep Zoom tiles are jpeg or png",
		}
	case "bif":
		return containerCaps{
			conformant: []string{"jpeg"},
			redirect:   "BIF is written by verbatim tile-copy only",
		}
	default:
		return containerCaps{}
	}
}

func codecInSet(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// validateCodec classifies a (container, codec) pair into conformant / nonconformant
// / unsupported. Returns a non-empty warning for nonconformant-but-allowed; a
// non-nil error (abort before I/O) for nonconformant-without-allow or unsupported.
func validateCodec(container, codec string, allowNonconformant bool) (string, error) {
	caps := containerCapabilities(container)
	if codecInSet(caps.conformant, codec) {
		return "", nil
	}
	if codecInSet(caps.nonconformant, codec) {
		if allowNonconformant {
			return fmt.Sprintf("--codec %s into %s is non-conformant: the bytes are valid but this tool's reader cannot open them as %s", codec, container, container), nil
		}
		return "", fmt.Errorf("--codec %s produces a non-conformant %s (not readable as %s); pass --allow-nonconformant to write it anyway", codec, container, container)
	}
	msg := fmt.Sprintf("--codec %s is not supported for --to %s", codec, container)
	if caps.redirect != "" {
		msg += " (" + caps.redirect + ")"
	}
	if len(caps.conformant) > 0 {
		msg += "; supported: " + strings.Join(caps.conformant, ", ")
	}
	return "", fmt.Errorf("%s", msg)
}
