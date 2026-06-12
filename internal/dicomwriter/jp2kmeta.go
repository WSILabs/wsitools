package dicomwriter

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// JP2KInfo is what InspectJP2K extracts from a JPEG 2000 codestream's main header.
type JP2KInfo struct {
	Components int
	Precision  int  // bit depth of component 0
	MCT        bool // multiple-component transform used (COD multi-comp byte == 1)
	Reversible bool // reversible 5/3 wavelet (COD transform byte == 1) → lossless
}

// InspectJP2K parses a raw J2K codestream's SIZ + COD markers. It expects the
// codestream to start at SOC (FF4F) — exactly what opentile's TileInto returns
// for a JPEG-2000 tile.
func InspectJP2K(j []byte) (JP2KInfo, error) {
	if len(j) < 4 || j[0] != 0xFF || j[1] != 0x4F {
		return JP2KInfo{}, errors.New("jp2kmeta: not a J2K codestream (missing SOC)")
	}
	var (
		info   JP2KInfo
		sawSIZ bool
		sawCOD bool
	)
	i := 2
	for i+2 <= len(j) {
		if j[i] != 0xFF {
			return JP2KInfo{}, fmt.Errorf("jp2kmeta: expected marker at offset %d, got %#x", i, j[i])
		}
		m := j[i+1]
		i += 2
		if m == 0x93 || m == 0xD9 { // SOD or EOC → main header done
			break
		}
		if i+2 > len(j) {
			return JP2KInfo{}, errors.New("jp2kmeta: truncated marker length")
		}
		segLen := int(binary.BigEndian.Uint16(j[i : i+2]))
		if segLen < 2 || i+segLen > len(j) {
			return JP2KInfo{}, fmt.Errorf("jp2kmeta: invalid segment length %d", segLen)
		}
		seg := j[i+2 : i+segLen]
		i += segLen
		switch m {
		case 0x51: // SIZ
			if len(seg) < 37 {
				return JP2KInfo{}, errors.New("jp2kmeta: short SIZ")
			}
			info.Components = int(binary.BigEndian.Uint16(seg[34:36]))
			info.Precision = int(seg[36]&0x7F) + 1
			sawSIZ = true
		case 0x52: // COD
			if len(seg) < 10 {
				return JP2KInfo{}, errors.New("jp2kmeta: short COD")
			}
			info.MCT = seg[4] == 1
			info.Reversible = seg[9] == 1
			sawCOD = true
		}
		if sawSIZ && sawCOD {
			break
		}
	}
	if !sawSIZ {
		return JP2KInfo{}, errors.New("jp2kmeta: no SIZ marker found")
	}
	if !sawCOD {
		return JP2KInfo{}, errors.New("jp2kmeta: no COD marker found")
	}
	return info, nil
}

// PhotometricJP2K maps a JP2KInfo to the DICOM PhotometricInterpretation for a
// verbatim tile-copy of that codestream.
func PhotometricJP2K(info JP2KInfo) (string, error) {
	if info.Precision != 8 {
		return "", fmt.Errorf("jp2kmeta: unsupported precision %d (want 8)", info.Precision)
	}
	switch info.Components {
	case 1:
		return "MONOCHROME2", nil
	case 3:
		if !info.MCT {
			return "RGB", nil
		}
		if info.Reversible {
			return "YBR_RCT", nil
		}
		return "YBR_ICT", nil
	default:
		return "", fmt.Errorf("jp2kmeta: unsupported component count %d (want 1 or 3)", info.Components)
	}
}
