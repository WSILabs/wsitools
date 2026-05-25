// Package jpeg implements the JPEG quality inspector. Registers
// itself with quality.Register in init().
package jpeg

import (
	"encoding/binary"
	"fmt"
	"math"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
)

// libjpegTableCoeffOfVariation returns the coefficient of variation (stdev/mean)
// of the per-entry ratios qt[i]/standardLumaTable[i]. A libjpeg-scaled table
// has a nearly constant ratio (CV ≈ 0.05–0.15); a custom or non-standard table
// will have CV >> 0.3.
func libjpegTableCoeffOfVariation(qt []int) float64 {
	if len(qt) == 0 {
		return 0
	}
	var mean float64
	for i := 0; i < 64 && i < len(qt); i++ {
		if standardLumaTable[i] == 0 {
			continue
		}
		mean += float64(qt[i]) / float64(standardLumaTable[i])
	}
	mean /= 64
	if mean == 0 {
		return 0
	}
	var variance float64
	for i := 0; i < 64 && i < len(qt); i++ {
		if standardLumaTable[i] == 0 {
			continue
		}
		r := float64(qt[i]) / float64(standardLumaTable[i])
		variance += (r - mean) * (r - mean)
	}
	variance /= 64
	return math.Sqrt(variance) / mean
}

func init() {
	quality.Register(&inspector{})
}

type inspector struct{}

func (*inspector) Compression() opentile.Compression { return opentile.CompressionJPEG }

func (*inspector) Inspect(tileBytes []byte) (quality.Info, error) {
	if len(tileBytes) < 4 || tileBytes[0] != 0xFF || tileBytes[1] != 0xD8 {
		return quality.Info{}, quality.ErrCorruptOrMismatch
	}

	var (
		// dqtTables holds all quantization tables keyed by tableID (0–3).
		// We accumulate across all DQT segments and prefer tableID 0 (luma).
		dqtTables    [4]*[64]int
		dqtFound     bool
		sofComponents []sofComponent
		sofFound     bool
	)

	// Scan marker segments. Stop at SOS (0xFFDA) — past that is
	// entropy-coded data, not parseable as markers.
	i := 2 // skip SOI
	for i < len(tileBytes)-1 {
		if tileBytes[i] != 0xFF {
			return quality.Info{}, fmt.Errorf("jpeg: expected marker prefix 0xFF at offset %d, got %#x", i, tileBytes[i])
		}
		// Skip fill bytes (0xFF 0xFF...).
		for i < len(tileBytes) && tileBytes[i] == 0xFF {
			i++
		}
		if i >= len(tileBytes) {
			break
		}
		marker := tileBytes[i]
		i++

		// Markers without segment payload.
		switch marker {
		case 0xD8, 0xD9, 0xD0, 0xD1, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7:
			continue
		case 0xDA: // SOS — stop parsing
			goto done
		}

		// All other markers carry a 2-byte big-endian length (incl. itself).
		if i+2 > len(tileBytes) {
			return quality.Info{}, fmt.Errorf("jpeg: truncated marker length at offset %d", i)
		}
		segLen := int(binary.BigEndian.Uint16(tileBytes[i : i+2]))
		if segLen < 2 || i+segLen > len(tileBytes) {
			return quality.Info{}, fmt.Errorf("jpeg: invalid segment length %d at offset %d", segLen, i)
		}
		segData := tileBytes[i+2 : i+segLen]
		i += segLen

		switch marker {
		case 0xDB: // DQT — accumulate all tables; a single DQT segment may carry multiple tables.
			parseDQTSegment(segData, &dqtTables)
			dqtFound = true
		case 0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF: // SOFn (excluding 0xC4 DHT and 0xC8 reserved)
			if !sofFound {
				comps := parseSOF(segData)
				sofComponents = comps
				sofFound = true
			}
		}
	}
done:

	if !dqtFound || !sofFound {
		return quality.Info{}, fmt.Errorf("jpeg: missing %s",
			missingStr(dqtFound, sofFound))
	}

	// Prefer tableID 0 (luma). Fall back to the first non-nil table if absent.
	lumaTable := dqtTables[0]
	if lumaTable == nil {
		for _, t := range dqtTables {
			if t != nil {
				lumaTable = t
				break
			}
		}
	}
	if lumaTable == nil {
		return quality.Info{}, fmt.Errorf("jpeg: DQT segment present but no table parsed")
	}

	q := estimateQuality(lumaTable[:])
	cs, notes := chromaSubsampling(sofComponents)

	// Detect non-libjpeg-standard quantization tables. Aperio SVS and some
	// other encoders use custom tables that don't follow the libjpeg scaling
	// formula (Q=50 baseline scaled by S). For such tables the Q estimate is
	// derived mechanically from the libjpeg inverse formula and may not
	// reflect the encoder's intended perceptual quality level.
	if cv := libjpegTableCoeffOfVariation(lumaTable[:]); cv > 0.3 {
		customNote := "Q estimate is best-effort; encoder uses a custom quantization table (non-libjpeg)"
		if notes == "" {
			notes = customNote
		} else {
			notes = notes + "; " + customNote
		}
	}

	return quality.Info{
		Codec:             "JPEG",
		Lossless:          false,
		QualityEstimate:   q,
		ChromaSubsampling: cs,
		Notes:             notes,
	}, nil
}

func missingStr(dqt, sof bool) string {
	switch {
	case !dqt && !sof:
		return "DQT and SOF markers"
	case !dqt:
		return "DQT marker"
	default:
		return "SOF marker"
	}
}

// parseDQTSegment parses a DQT segment payload and stores all quantization
// tables found in it into tables, keyed by tableID (0–3). A single DQT
// segment may carry multiple concatenated tables. Handles both 8-bit
// (precision=0) and 16-bit (precision=1) tables.
func parseDQTSegment(seg []byte, tables *[4]*[64]int) {
	offset := 0
	for offset < len(seg) {
		if offset+1 > len(seg) {
			break
		}
		hdr := seg[offset]
		precision := (hdr >> 4) & 0x0F // 0 = 8-bit, 1 = 16-bit
		tableID := hdr & 0x0F
		offset++

		tableSize := 64
		if precision == 1 {
			tableSize = 128 // 64 * 2 bytes
		}
		if offset+tableSize > len(seg) {
			break // truncated; ignore remainder
		}
		data := seg[offset : offset+tableSize]
		offset += tableSize

		if tableID > 3 {
			continue // invalid table ID; skip
		}
		t := new([64]int)
		if precision == 0 {
			for i := 0; i < 64; i++ {
				t[i] = int(data[i])
			}
		} else {
			for i := 0; i < 64; i++ {
				t[i] = int(binary.BigEndian.Uint16(data[i*2 : i*2+2]))
			}
		}
		tables[tableID] = t
	}
}

type sofComponent struct {
	id       byte
	hSamp    byte // horizontal sampling factor
	vSamp    byte // vertical sampling factor
	quantSel byte // quantization table selector
}

// parseSOF parses an SOFn segment payload. Returns the per-component
// sampling factors.
//
// SOF layout (after segment-length):
//   - 1 byte: sample precision (typically 8)
//   - 2 bytes: image height
//   - 2 bytes: image width
//   - 1 byte: number of components
//   - per component: 1 byte id, 1 byte (Hi<<4|Vi), 1 byte Tqi
func parseSOF(seg []byte) []sofComponent {
	if len(seg) < 6 {
		return nil
	}
	nf := int(seg[5])
	if len(seg) < 6+nf*3 {
		return nil
	}
	comps := make([]sofComponent, nf)
	for i := 0; i < nf; i++ {
		off := 6 + i*3
		comps[i] = sofComponent{
			id:       seg[off],
			hSamp:    (seg[off+1] >> 4) & 0x0F,
			vSamp:    seg[off+1] & 0x0F,
			quantSel: seg[off+2],
		}
	}
	return comps
}

// standardLumaTable is the libjpeg baseline luma quantization table at Q=50
// (zigzag scan order). Used to invert the libjpeg scaling formula and recover
// the original Q value from an encoded DQT table.
var standardLumaTable = [64]int{
	16, 11, 10, 16, 24, 40, 51, 61,
	12, 12, 14, 19, 26, 58, 60, 55,
	14, 13, 16, 24, 40, 57, 69, 56,
	14, 17, 22, 29, 51, 87, 80, 62,
	18, 22, 37, 56, 68, 109, 103, 77,
	24, 35, 55, 64, 81, 104, 113, 92,
	49, 64, 78, 87, 103, 121, 120, 101,
	72, 92, 95, 98, 112, 100, 103, 99,
}

// estimateQuality maps the first quantization table to an approximate Q value
// using the libjpeg scaling formula in reverse.
//
// libjpeg scales the standard table at Q=50 by a factor S:
//
//	S = 5000/Q  for Q < 50
//	S = 200-2*Q for Q >= 50
//
// Each encoded entry ≈ standardTable[i] * S / 100. Averaging S estimates
// across all 64 entries and inverting gives an approximate Q. Accuracy is
// typically ±10 for JPEG files produced by libjpeg-compatible encoders.
func estimateQuality(qt []int) int {
	if len(qt) == 0 {
		return 0
	}
	var sumS float64
	var n int
	for i := 0; i < 64 && i < len(qt); i++ {
		if standardLumaTable[i] == 0 || qt[i] == 0 {
			continue
		}
		s := float64(qt[i]) * 100.0 / float64(standardLumaTable[i])
		sumS += s
		n++
	}
	if n == 0 {
		return 0
	}
	avgS := sumS / float64(n)
	var q float64
	if avgS >= 100 {
		// Q < 50: S = 5000/Q → Q = 5000/S
		q = 5000.0 / avgS
	} else {
		// Q >= 50: S = 200-2*Q → Q = (200-S)/2
		q = (200.0 - avgS) / 2.0
	}
	qi := int(math.Round(q))
	if qi < 1 {
		qi = 1
	}
	if qi > 100 {
		qi = 100
	}
	return qi
}

// chromaSubsampling returns the subsampling string from SOF components.
// Returns ("", "grayscale") for single-component SOFs.
func chromaSubsampling(comps []sofComponent) (string, string) {
	if len(comps) < 2 {
		return "", "grayscale"
	}
	// Y is comps[0]; assume 3-component YCbCr.
	if len(comps) < 3 {
		return "", ""
	}
	y, cb, cr := comps[0], comps[1], comps[2]
	if cb.hSamp != 1 || cb.vSamp != 1 || cr.hSamp != 1 || cr.vSamp != 1 {
		// Non-standard chroma sampling; fall through but note it.
		return "", fmt.Sprintf("non-standard sampling Y=%d,%d Cb=%d,%d Cr=%d,%d",
			y.hSamp, y.vSamp, cb.hSamp, cb.vSamp, cr.hSamp, cr.vSamp)
	}
	switch {
	case y.hSamp == 1 && y.vSamp == 1:
		return "4:4:4", ""
	case y.hSamp == 2 && y.vSamp == 1:
		return "4:2:2", ""
	case y.hSamp == 2 && y.vSamp == 2:
		return "4:2:0", ""
	case y.hSamp == 4 && y.vSamp == 1:
		return "4:1:1", ""
	}
	return "", fmt.Sprintf("unrecognized Y sampling %d,%d", y.hSamp, y.vSamp)
}
