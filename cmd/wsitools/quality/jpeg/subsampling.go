package jpeg

import "encoding/binary"

// LumaSampling parses the first SOF marker of a JPEG bytestream and
// returns the luma (component 0) horizontal and vertical sampling
// factors. These equal the TIFF YCbCrSubSampling [H, V] pair:
// 4:2:0 → (2,2), 4:2:2 → (2,1), 4:4:4 → (1,1). ok is false if the input
// is not a JPEG (no SOI) or has no parseable SOF before SOS.
func LumaSampling(tileBytes []byte) (h, v uint16, ok bool) {
	if len(tileBytes) < 4 || tileBytes[0] != 0xFF || tileBytes[1] != 0xD8 {
		return 0, 0, false
	}
	i := 2 // skip SOI
	for i < len(tileBytes)-1 {
		if tileBytes[i] != 0xFF {
			return 0, 0, false
		}
		for i < len(tileBytes) && tileBytes[i] == 0xFF {
			i++ // skip fill bytes
		}
		if i >= len(tileBytes) {
			break
		}
		marker := tileBytes[i]
		i++
		switch marker {
		case 0xD8, 0xD9, 0xD0, 0xD1, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7:
			continue // standalone markers, no payload
		case 0xDA: // SOS — entropy-coded data follows; stop.
			return 0, 0, false
		}
		if i+2 > len(tileBytes) {
			return 0, 0, false
		}
		segLen := int(binary.BigEndian.Uint16(tileBytes[i : i+2]))
		if segLen < 2 || i+segLen > len(tileBytes) {
			return 0, 0, false
		}
		segData := tileBytes[i+2 : i+segLen]
		i += segLen
		switch marker {
		case 0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF: // SOFn (excl. C4 DHT, C8 reserved)
			comps := parseSOF(segData)
			if len(comps) == 0 {
				return 0, 0, false
			}
			return uint16(comps[0].hSamp), uint16(comps[0].vSamp), true
		}
	}
	return 0, 0, false
}
