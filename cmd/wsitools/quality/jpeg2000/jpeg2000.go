// Package jpeg2000 implements the JPEG 2000 quality inspector.
// Parses codestream markers (SOC + SIZ + COD + QCD) to extract
// transform type (reversible/irreversible) and layer count.
package jpeg2000

import (
	"encoding/binary"
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
)

func init() {
	quality.Register(&inspector{})
}

type inspector struct{}

func (*inspector) Compression() opentile.Compression { return opentile.CompressionJP2K }

func (*inspector) Inspect(tileBytes []byte) (quality.Info, error) {
	if len(tileBytes) < 2 {
		return quality.Info{}, quality.ErrCorruptOrMismatch
	}
	// SOC marker = 0xFF 0x4F.
	if tileBytes[0] != 0xFF || tileBytes[1] != 0x4F {
		return quality.Info{}, quality.ErrCorruptOrMismatch
	}

	// Scan for COD (0xFF52). It's the only marker we need for v0.14.
	cod, err := findCOD(tileBytes)
	if err != nil {
		return quality.Info{}, err
	}

	// COD payload layout (Scod, progression, layers[2], multi-comp, decomp,
	// cbW, cbH, cbStyle, transform):
	if len(cod) < 10 {
		return quality.Info{}, fmt.Errorf("jp2k: COD segment too short (%d bytes)", len(cod))
	}
	layers := int(binary.BigEndian.Uint16(cod[2:4]))
	decompLevels := int(cod[5])
	transformType := cod[9]
	reversible := transformType == 1

	transformName := "irreversible (9/7)"
	if reversible {
		transformName = "reversible (5/3)"
	}

	return quality.Info{
		Codec:      "JPEG 2000",
		Lossless:   reversible,
		LayerCount: layers,
		Notes:      fmt.Sprintf("%s transform, %d decomposition levels", transformName, decompLevels),
	}, nil
}

// findCOD scans the codestream for the COD marker (0xFF52) and
// returns the marker's payload (everything after the 2-byte length
// field). Returns an error if COD is not found.
func findCOD(buf []byte) ([]byte, error) {
	i := 2 // skip SOC
	for i+4 <= len(buf) {
		if buf[i] != 0xFF {
			return nil, fmt.Errorf("jp2k: expected marker prefix at offset %d, got %#x", i, buf[i])
		}
		marker := buf[i+1]
		i += 2

		// SOC itself has no length; other markers above 0xFF30 do.
		// SOD (0x93) marks start of data — past it the codestream is
		// not parseable.
		if marker == 0x93 || marker == 0xD9 {
			return nil, fmt.Errorf("jp2k: COD marker not found before %s", markerName(marker))
		}

		if i+2 > len(buf) {
			return nil, fmt.Errorf("jp2k: truncated marker length at offset %d", i)
		}
		segLen := int(binary.BigEndian.Uint16(buf[i : i+2]))
		if segLen < 2 || i+segLen > len(buf) {
			return nil, fmt.Errorf("jp2k: invalid segment length %d at offset %d", segLen, i)
		}
		segData := buf[i+2 : i+segLen]
		i += segLen

		if marker == 0x52 { // COD
			return segData, nil
		}
	}
	return nil, fmt.Errorf("jp2k: COD marker not found")
}

func markerName(m byte) string {
	switch m {
	case 0x93:
		return "SOD"
	case 0xD9:
		return "EOC"
	}
	return fmt.Sprintf("marker 0xFF%02X", m)
}
