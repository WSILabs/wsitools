package dicomwriter

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// JPEGColor is the encoded colorspace of a JPEG stream's pixel samples.
type JPEGColor int

const (
	ColorYCbCr JPEGColor = iota // default per JFIF when no Adobe APP14 marker
	ColorRGB                    // Adobe APP14 ColorTransform = 0 (e.g. classic Aperio SVS)
)

// JPEGInfo is what Inspect extracts from a JPEG stream's header.
type JPEGInfo struct {
	Color      JPEGColor
	Components int
	Precision  int
	Subsampled bool
}

// Inspect parses a JPEG stream's markers far enough to determine its colorspace
// and chroma subsampling. It scans the whole header up to SOS — NOT stopping at
// SOF — because Aperio SVS tiles place the Adobe APP14 marker AFTER SOF0, and the
// APP14 ColorTransform flag is what distinguishes raw-RGB tiles from YCbCr ones.
func Inspect(j []byte) (JPEGInfo, error) {
	if len(j) < 2 || j[0] != 0xFF || j[1] != 0xD8 {
		return JPEGInfo{}, errors.New("jpegmeta: not a JPEG (missing SOI)")
	}
	var (
		info       JPEGInfo
		sawSOF     bool
		sawAPP14   bool
		app14Color JPEGColor
	)
	i := 2
	for i+1 < len(j) {
		if j[i] != 0xFF {
			i++
			continue
		}
		m := j[i+1]
		if m == 0xFF { // fill byte
			i++
			continue
		}
		// Standalone markers (no length): TEM, RSTn.
		if m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			i += 2
			continue
		}
		if m == 0xD9 || m == 0xDA { // EOI or SOS → header done
			break
		}
		if i+4 > len(j) {
			return JPEGInfo{}, errors.New("jpegmeta: truncated before segment length")
		}
		segLen := int(binary.BigEndian.Uint16(j[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(j) {
			return JPEGInfo{}, fmt.Errorf("jpegmeta: bad segment length %d", segLen)
		}
		payload := j[i+4 : i+2+segLen]
		switch {
		case m == 0xEE: // APP14 Adobe
			if len(payload) >= 12 && string(payload[:5]) == "Adobe" {
				sawAPP14 = true
				if payload[11] == 0 {
					app14Color = ColorRGB
				} else {
					app14Color = ColorYCbCr // 1 = YCbCr, 2 = YCCK
				}
			}
		case m == 0xC0 || m == 0xC1: // SOF0 baseline / SOF1 extended sequential
			if len(payload) < 6 {
				return JPEGInfo{}, errors.New("jpegmeta: short SOF")
			}
			info.Precision = int(payload[0])
			nc := int(payload[5])
			info.Components = nc
			maxH, maxV := 0, 0
			hv := make([][2]int, 0, nc)
			for c := 0; c < nc; c++ {
				off := 6 + c*3
				if off+1 >= len(payload) {
					return JPEGInfo{}, errors.New("jpegmeta: short SOF component table")
				}
				h := int(payload[off+1] >> 4)
				v := int(payload[off+1] & 0x0F)
				hv = append(hv, [2]int{h, v})
				if h > maxH {
					maxH = h
				}
				if v > maxV {
					maxV = v
				}
			}
			for _, s := range hv {
				if s[0] < maxH || s[1] < maxV {
					info.Subsampled = true
				}
			}
			sawSOF = true
		case m >= 0xC2 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC:
			// SOF2/3/5/6/7/9/.. = progressive/lossless/arithmetic: not baseline.
			return JPEGInfo{}, fmt.Errorf("jpegmeta: non-baseline SOF marker 0xFF%02X", m)
		}
		i += 2 + segLen
	}
	if !sawSOF {
		return JPEGInfo{}, errors.New("jpegmeta: no SOF marker found")
	}
	if sawAPP14 {
		info.Color = app14Color
	} else {
		info.Color = ColorYCbCr // JFIF convention
	}
	return info, nil
}

// Photometric maps a JPEGInfo to the DICOM PhotometricInterpretation value for a
// verbatim tile-copy of that stream.
func Photometric(info JPEGInfo) (string, error) {
	if info.Precision != 8 {
		return "", fmt.Errorf("jpegmeta: unsupported precision %d (want 8)", info.Precision)
	}
	switch info.Components {
	case 1:
		return "MONOCHROME2", nil
	case 3:
		if info.Color == ColorRGB {
			return "RGB", nil
		}
		if info.Subsampled {
			return "YBR_FULL_422", nil
		}
		return "YBR_FULL", nil
	default:
		return "", fmt.Errorf("jpegmeta: unsupported component count %d (want 1 or 3)", info.Components)
	}
}
