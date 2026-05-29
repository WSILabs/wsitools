package main

import (
	"encoding/binary"
	"fmt"
	"math"
)

// decodeValue turns a raw TIFF entry byte slice into a native Go value.
// For count==1 returns a scalar; for count>1 returns a slice (except
// ASCII, which always returns a string; and BYTE/UNDEFINED/SBYTE/unknown,
// which always return []byte/[]int8).
func decodeValue(typ uint16, count uint64, raw []byte, bo binary.ByteOrder) (any, error) {
	if raw == nil {
		return nil, fmt.Errorf("unreadable")
	}
	switch typ {
	case 1, 7:
		return append([]byte(nil), raw...), nil
	case 2:
		s := string(raw)
		for i := 0; i < len(s); i++ {
			if s[i] == 0 {
				s = s[:i]
				break
			}
		}
		return s, nil
	case 3:
		return decodeUintArray(raw, count, 2, bo)
	case 4, 13:
		return decodeUintArray(raw, count, 4, bo)
	case 5:
		return decodeRationalArray(raw, count, bo, false)
	case 6:
		out := make([]int8, len(raw))
		for i, b := range raw {
			out[i] = int8(b)
		}
		return out, nil
	case 8:
		return decodeIntArray(raw, count, 2, bo)
	case 9:
		return decodeIntArray(raw, count, 4, bo)
	case 10:
		return decodeRationalArray(raw, count, bo, true)
	case 11:
		if count == 1 && len(raw) >= 4 {
			return float64(math.Float32frombits(bo.Uint32(raw[:4]))), nil
		}
		out := make([]float64, count)
		for i := uint64(0); i < count; i++ {
			out[i] = float64(math.Float32frombits(bo.Uint32(raw[i*4 : i*4+4])))
		}
		return out, nil
	case 12:
		if count == 1 && len(raw) >= 8 {
			return math.Float64frombits(bo.Uint64(raw[:8])), nil
		}
		out := make([]float64, count)
		for i := uint64(0); i < count; i++ {
			out[i] = math.Float64frombits(bo.Uint64(raw[i*8 : i*8+8]))
		}
		return out, nil
	case 16, 18:
		return decodeUintArray(raw, count, 8, bo)
	case 17:
		return decodeIntArray(raw, count, 8, bo)
	default:
		return append([]byte(nil), raw...), nil
	}
}

func decodeUintArray(raw []byte, count uint64, elem int, bo binary.ByteOrder) (any, error) {
	readOne := func(b []byte) uint64 {
		switch elem {
		case 2:
			return uint64(bo.Uint16(b))
		case 4:
			return uint64(bo.Uint32(b))
		case 8:
			return bo.Uint64(b)
		}
		return 0
	}
	if count == 1 && len(raw) >= elem {
		return readOne(raw[:elem]), nil
	}
	out := make([]uint64, count)
	for i := uint64(0); i < count; i++ {
		out[i] = readOne(raw[int(i)*elem : int(i)*elem+elem])
	}
	return out, nil
}

func decodeIntArray(raw []byte, count uint64, elem int, bo binary.ByteOrder) (any, error) {
	readOne := func(b []byte) int64 {
		switch elem {
		case 2:
			return int64(int16(bo.Uint16(b)))
		case 4:
			return int64(int32(bo.Uint32(b)))
		case 8:
			return int64(bo.Uint64(b))
		}
		return 0
	}
	if count == 1 && len(raw) >= elem {
		return readOne(raw[:elem]), nil
	}
	out := make([]int64, count)
	for i := uint64(0); i < count; i++ {
		out[i] = readOne(raw[int(i)*elem : int(i)*elem+elem])
	}
	return out, nil
}

// Truncation caps used by truncateValue when fullValues==false.
const (
	maxArrayEntries = 8
	maxAsciiChars   = 200
	maxBlobBytes    = 64
)

// truncateValue applies smart-truncation rules to a decoded value. When
// fullValues is true, returns the value unchanged with truncated=false.
func truncateValue(v any, fullValues bool) (any, bool) {
	if fullValues {
		return v, false
	}
	switch x := v.(type) {
	case []uint64:
		if len(x) > maxArrayEntries {
			return x[:maxArrayEntries], true
		}
	case []int64:
		if len(x) > maxArrayEntries {
			return x[:maxArrayEntries], true
		}
	case []float64:
		if len(x) > maxArrayEntries {
			return x[:maxArrayEntries], true
		}
	case []string:
		if len(x) > maxArrayEntries {
			return x[:maxArrayEntries], true
		}
	case []byte:
		if len(x) > maxBlobBytes {
			return x[:maxBlobBytes], true
		}
	case []int8:
		if len(x) > maxBlobBytes {
			return x[:maxBlobBytes], true
		}
	case string:
		if len(x) > maxAsciiChars {
			return x[:maxAsciiChars], true
		}
	}
	return v, false
}

func decodeRationalArray(raw []byte, count uint64, bo binary.ByteOrder, signed bool) (any, error) {
	render := func(off int) string {
		if signed {
			n := int32(bo.Uint32(raw[off : off+4]))
			d := int32(bo.Uint32(raw[off+4 : off+8]))
			return fmt.Sprintf("%d/%d", n, d)
		}
		n := bo.Uint32(raw[off : off+4])
		d := bo.Uint32(raw[off+4 : off+8])
		return fmt.Sprintf("%d/%d", n, d)
	}
	if count == 1 && len(raw) >= 8 {
		return render(0), nil
	}
	out := make([]string, count)
	for i := uint64(0); i < count; i++ {
		out[i] = render(int(i) * 8)
	}
	return out, nil
}
