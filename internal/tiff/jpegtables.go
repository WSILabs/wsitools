package tiff

import (
	"bytes"
	"fmt"
)

// JPEG marker constants. Two-byte sequences 0xFF, 0x?? define markers.
const (
	jpegSOI = 0xD8
	jpegEOI = 0xD9
	jpegDQT = 0xDB
	jpegDHT = 0xC4
	jpegDRI = 0xDD
	jpegSOS = 0xDA
)

// ExtractJPEGTables walks a self-contained JPEG and returns a tables-only JPEG
// containing SOI + all DQT + all DHT + (optional DRI) + EOI markers, suitable
// for writing into TIFF tag 347 (JPEGTables).
//
// The tables-only JPEG must end before SOS — once SOS is reached, scan data
// follows and must be excluded.
func ExtractJPEGTables(jpg []byte) ([]byte, error) {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != jpegSOI {
		return nil, fmt.Errorf("tiff: not a JPEG (no SOI)")
	}
	out := []byte{0xFF, jpegSOI}
	i := 2
	for i < len(jpg)-1 {
		if jpg[i] != 0xFF {
			i++
			continue
		}
		marker := jpg[i+1]
		if marker == 0xFF {
			i++ // padding fill byte
			continue
		}
		if marker == jpegSOI || marker == jpegEOI || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if marker == jpegSOS {
			break
		}
		if i+4 > len(jpg) {
			return nil, fmt.Errorf("tiff: truncated JPEG marker length")
		}
		segLen := int(jpg[i+2])<<8 | int(jpg[i+3])
		segEnd := i + 2 + segLen
		if segEnd > len(jpg) {
			return nil, fmt.Errorf("tiff: truncated JPEG segment")
		}
		if marker == jpegDQT || marker == jpegDHT || marker == jpegDRI {
			out = append(out, jpg[i:segEnd]...)
		}
		i = segEnd
	}
	out = append(out, 0xFF, jpegEOI)
	return out, nil
}

// StripJPEGTables walks a self-contained JPEG and returns a copy with all DQT
// and DHT markers removed. Result is the abbreviated-form tile bytes that pair
// with a JPEGTables tag of the same shared tables.
func StripJPEGTables(jpg []byte) ([]byte, error) {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != jpegSOI {
		return nil, fmt.Errorf("tiff: not a JPEG")
	}
	var out bytes.Buffer
	out.Write([]byte{0xFF, jpegSOI})
	i := 2
	for i < len(jpg)-1 {
		if jpg[i] != 0xFF {
			i++
			continue
		}
		marker := jpg[i+1]
		if marker == 0xFF {
			i++
			continue
		}
		if marker == jpegSOI || marker == jpegEOI || (marker >= 0xD0 && marker <= 0xD7) {
			out.Write(jpg[i : i+2])
			i += 2
			continue
		}
		if marker == jpegSOS {
			out.Write(jpg[i:])
			return out.Bytes(), nil
		}
		segLen := int(jpg[i+2])<<8 | int(jpg[i+3])
		segEnd := i + 2 + segLen
		if marker != jpegDQT && marker != jpegDHT {
			out.Write(jpg[i:segEnd])
		}
		i = segEnd
	}
	return out.Bytes(), nil
}
